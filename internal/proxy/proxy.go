package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/vymoiseenkov/ai-agents-platform/internal/balancer"
	"github.com/vymoiseenkov/ai-agents-platform/internal/config"
	"github.com/vymoiseenkov/ai-agents-platform/internal/stats"
	"github.com/vymoiseenkov/ai-agents-platform/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Proxy struct {
	balancer     *balancer.Balancer
	logger       *slog.Logger
	collector    *stats.Collector
	metrics      *telemetry.Metrics
	tracer       trace.Tracer
	client       *http.Client
	streamClient *http.Client
}

func New(b *balancer.Balancer, logger *slog.Logger, collector *stats.Collector, metrics *telemetry.Metrics) *Proxy {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Proxy{
		balancer:     b,
		logger:       logger,
		collector:    collector,
		metrics:      metrics,
		tracer:       telemetry.Tracer(),
		client:       &http.Client{Transport: transport},
		streamClient: &http.Client{Transport: transport, Timeout: 0},
	}
}

type chatRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.Model == "" {
		http.Error(w, `"model" field is required`, http.StatusBadRequest)
		return
	}

	ctx, span := p.tracer.Start(r.Context(), "llm.chat_completion",
		trace.WithAttributes(
			attribute.String("llm.model", req.Model),
			attribute.Bool("llm.stream", req.Stream),
		),
	)
	defer span.End()
	r = r.WithContext(ctx)

	primary, fallbacks, ok := p.balancer.Next(req.Model)
	if !ok {
		span.SetStatus(codes.Error, "model not found")
		http.Error(w, fmt.Sprintf("model %q not found", req.Model), http.StatusNotFound)
		return
	}

	span.SetAttributes(attribute.String("llm.provider.primary", primary.Name))

	if p.metrics != nil {
		p.metrics.TrackInflight(r.Context(), 1)
		defer p.metrics.TrackInflight(r.Context(), -1)
	}

	if p.tryProxy(w, r, primary, body, req.Stream, req.Model) {
		return
	}

	for _, fb := range fallbacks {
		p.logger.Warn("retrying with fallback provider",
			"model", req.Model,
			"failed", primary.Name,
			"fallback", fb.Name,
		)
		span.AddEvent("fallback", trace.WithAttributes(attribute.String("provider", fb.Name)))
		if p.tryProxy(w, r, fb, body, req.Stream, req.Model) {
			return
		}
	}

	span.SetStatus(codes.Error, "all providers failed")
	http.Error(w, "all providers failed", http.StatusBadGateway)
}

func (p *Proxy) tryProxy(w http.ResponseWriter, origReq *http.Request, provider config.Provider, body []byte, stream bool, model string) bool {
	ctx, provSpan := p.tracer.Start(origReq.Context(), "llm.provider_call",
		trace.WithAttributes(
			attribute.String("llm.provider", provider.Name),
			attribute.String("llm.model", model),
			attribute.Bool("llm.stream", stream),
		),
	)
	defer provSpan.End()
	origReq = origReq.WithContext(ctx)

	url := strings.TrimRight(provider.URL, "/") + "/v1/chat/completions"
	start := time.Now()

	req, err := http.NewRequestWithContext(origReq.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		p.logger.Error("failed to create request", "provider", provider.Name, "error", err)
		p.record(origReq.Context(), provider.Name, model, 0, time.Since(start), true, err.Error())
		p.balancer.RecordFailure(provider.Name)
		provSpan.SetStatus(codes.Error, err.Error())
		return false
	}

	req.Header.Set("Content-Type", "application/json")
	if provider.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	}

	client := p.client
	if stream {
		client = p.streamClient
	}
	if !stream && provider.Timeout > 0 {
		tctx, cancel := context.WithTimeout(origReq.Context(), time.Duration(provider.Timeout)*time.Second)
		defer cancel()
		req = req.WithContext(tctx)
	}

	resp, err := client.Do(req)
	latency := time.Since(start)

	if err != nil {
		p.logger.Error("request failed", "provider", provider.Name, "error", err)
		p.record(origReq.Context(), provider.Name, model, 0, latency, true, err.Error())
		p.balancer.RecordFailure(provider.Name)
		provSpan.SetStatus(codes.Error, err.Error())
		return false
	}

	if resp.StatusCode >= 500 {
		resp.Body.Close()
		errMsg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		p.logger.Warn("provider returned 5xx", "provider", provider.Name, "status", resp.StatusCode)
		p.record(origReq.Context(), provider.Name, model, resp.StatusCode, latency, true, errMsg)
		p.balancer.RecordFailure(provider.Name)
		provSpan.SetStatus(codes.Error, errMsg)
		return false
	}

	p.balancer.RecordSuccess(provider.Name)

	p.logger.Info("proxying response", "provider", provider.Name, "status", resp.StatusCode, "stream", stream)

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	var usage Usage
	var ttft, tpot time.Duration

	if stream {
		tr := NewTimingReader(resp.Body, start)
		p.streamResponse(w, tr)
		totalDuration := time.Since(start)
		ttft = tr.TTFT()
		usage = tr.StreamUsage()
		if usage.CompletionTokens > 0 {
			tpot = tr.TPOT(totalDuration, usage.CompletionTokens)
		}
	} else {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			p.logger.Error("failed to read response body", "provider", provider.Name, "error", err)
		}
		w.Write(respBody)
		usage = parseUsageFromBody(respBody)
		ttft = latency // for non-streaming, TTFT ≈ full latency
	}
	resp.Body.Close()

	totalLatency := time.Since(start)
	cost := usage.ComputeCost(provider.PricePerInputToken, provider.PricePerOutputToken)

	p.collector.Record(stats.RequestRecord{
		Provider:     provider.Name,
		Model:        model,
		Status:       resp.StatusCode,
		Latency:      totalLatency,
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		Cost:         cost,
		TTFT:         ttft,
	})
	if p.metrics != nil {
		p.metrics.RecordRequest(origReq.Context(), provider.Name, model, resp.StatusCode, totalLatency, false)
		p.metrics.RecordTokens(origReq.Context(), provider.Name, model, usage.PromptTokens, usage.CompletionTokens, cost)
		p.metrics.RecordTTFT(origReq.Context(), provider.Name, model, ttft)
		if tpot > 0 {
			p.metrics.RecordTPOT(origReq.Context(), provider.Name, model, tpot)
		}
	}

	provSpan.SetAttributes(
		attribute.Int("llm.status_code", resp.StatusCode),
		attribute.Int64("llm.latency_ms", totalLatency.Milliseconds()),
		attribute.Int64("llm.ttft_ms", ttft.Milliseconds()),
		attribute.Int("llm.input_tokens", usage.PromptTokens),
		attribute.Int("llm.output_tokens", usage.CompletionTokens),
		attribute.Float64("llm.cost_usd", cost),
	)
	if tpot > 0 {
		provSpan.SetAttributes(attribute.Int64("llm.tpot_ms", tpot.Milliseconds()))
	}

	return true
}

func (p *Proxy) record(ctx context.Context, provider, model string, status int, latency time.Duration, isError bool, errMsg string) {
	p.collector.Record(stats.RequestRecord{
		Provider: provider, Model: model, Status: status, Error: isError, ErrorMsg: errMsg, Latency: latency,
	})
	if p.metrics != nil {
		p.metrics.RecordRequest(ctx, provider, model, status, latency, isError)
	}
}

func (p *Proxy) streamResponse(w http.ResponseWriter, src io.Reader) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		io.Copy(w, src)
		return
	}

	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			flusher.Flush()
		}
		if err != nil {
			return
		}
	}
}
