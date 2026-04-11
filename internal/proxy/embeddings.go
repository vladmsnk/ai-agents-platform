package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/vymoiseenkov/ai-agents-platform/internal/config"
	"github.com/vymoiseenkov/ai-agents-platform/internal/stats"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type embeddingRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"` // string or []string
}

type embeddingResponse struct {
	Object string            `json:"object"`
	Data   []embeddingObject `json:"data"`
	Model  string            `json:"model"`
	Usage  embeddingUsage    `json:"usage"`
}

type embeddingObject struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

type embeddingUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

const mockEmbeddingDim = 256

// EmbeddingsHandler returns an http.Handler for /v1/embeddings.
func (p *Proxy) EmbeddingsHandler() http.Handler {
	return http.HandlerFunc(p.serveEmbeddings)
}

func (p *Proxy) serveEmbeddings(w http.ResponseWriter, r *http.Request) {
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

	var req embeddingRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Model == "" {
		http.Error(w, `"model" field is required`, http.StatusBadRequest)
		return
	}

	ctx, span := p.tracer.Start(r.Context(), "llm.embedding",
		trace.WithAttributes(
			attribute.String("llm.model", req.Model),
		),
	)
	defer span.End()
	r = r.WithContext(ctx)

	// Try to find a provider that supports this model
	primary, fallbacks, ok := p.balancer.Next(req.Model)
	if !ok {
		// No provider for this model — use mock embeddings
		p.logger.Info("no provider for embedding model, using mock", "model", req.Model)
		p.serveMockEmbedding(w, req)
		span.SetAttributes(attribute.String("llm.provider.primary", "mock"))
		return
	}

	span.SetAttributes(attribute.String("llm.provider.primary", primary.Name))

	if p.tryEmbedding(w, r, primary, body, req.Model) {
		return
	}

	for _, fb := range fallbacks {
		p.logger.Warn("retrying embedding with fallback provider",
			"model", req.Model,
			"failed", primary.Name,
			"fallback", fb.Name,
		)
		span.AddEvent("fallback", trace.WithAttributes(attribute.String("provider", fb.Name)))
		if p.tryEmbedding(w, r, fb, body, req.Model) {
			return
		}
	}

	// All providers failed — fall back to mock
	p.logger.Warn("all embedding providers failed, using mock", "model", req.Model)
	p.serveMockEmbedding(w, req)
}

func (p *Proxy) tryEmbedding(w http.ResponseWriter, origReq *http.Request, provider config.Provider, body []byte, model string) bool {
	ctx, provSpan := p.tracer.Start(origReq.Context(), "llm.embedding_call",
		trace.WithAttributes(
			attribute.String("llm.provider", provider.Name),
			attribute.String("llm.model", model),
		),
	)
	defer provSpan.End()

	url := strings.TrimRight(provider.URL, "/") + "/v1/embeddings"
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		p.logger.Error("failed to create embedding request", "provider", provider.Name, "error", err)
		p.record(origReq.Context(), provider.Name, model, 0, time.Since(start), true, err.Error())
		p.balancer.RecordFailure(provider.Name)
		provSpan.SetStatus(codes.Error, err.Error())
		return false
	}

	req.Header.Set("Content-Type", "application/json")
	if provider.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	}

	if provider.Timeout > 0 {
		tctx, cancel := context.WithTimeout(ctx, time.Duration(provider.Timeout)*time.Second)
		defer cancel()
		req = req.WithContext(tctx)
	}

	resp, err := p.client.Do(req)
	latency := time.Since(start)

	if err != nil {
		p.logger.Error("embedding request failed", "provider", provider.Name, "error", err)
		p.record(origReq.Context(), provider.Name, model, 0, latency, true, err.Error())
		p.balancer.RecordFailure(provider.Name)
		provSpan.SetStatus(codes.Error, err.Error())
		return false
	}

	// For embeddings, treat both 4xx (auth errors) and 5xx as failures
	// so we fall through to fallback providers or mock embeddings.
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		errMsg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		p.logger.Warn("embedding provider returned error", "provider", provider.Name, "status", resp.StatusCode)
		p.record(origReq.Context(), provider.Name, model, resp.StatusCode, latency, true, errMsg)
		if resp.StatusCode >= 500 {
			p.balancer.RecordFailure(provider.Name)
		}
		provSpan.SetStatus(codes.Error, errMsg)
		return false
	}

	p.balancer.RecordSuccess(provider.Name)

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
	resp.Body.Close()

	p.collector.Record(stats.RequestRecord{
		Provider: provider.Name,
		Model:    model,
		Status:   resp.StatusCode,
		Latency:  latency,
	})
	if p.metrics != nil {
		p.metrics.RecordRequest(origReq.Context(), provider.Name, model, resp.StatusCode, latency, false)
	}

	provSpan.SetAttributes(
		attribute.Int("llm.status_code", resp.StatusCode),
		attribute.Int64("llm.latency_ms", latency.Milliseconds()),
	)

	return true
}

// serveMockEmbedding returns a deterministic mock embedding based on input hash.
func (p *Proxy) serveMockEmbedding(w http.ResponseWriter, req embeddingRequest) {
	inputs := extractInputStrings(req.Input)

	data := make([]embeddingObject, len(inputs))
	for i, input := range inputs {
		data[i] = embeddingObject{
			Object:    "embedding",
			Index:     i,
			Embedding: deterministicEmbedding(input, mockEmbeddingDim),
		}
	}

	resp := embeddingResponse{
		Object: "list",
		Data:   data,
		Model:  req.Model,
		Usage: embeddingUsage{
			PromptTokens: len(inputs) * 10, // rough estimate
			TotalTokens:  len(inputs) * 10,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// extractInputStrings normalizes the input field to a []string.
func extractInputStrings(input any) []string {
	switch v := input.(type) {
	case string:
		return []string{v}
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return []string{""}
}

// deterministicEmbedding generates a unit-length vector from the SHA-256 hash of the input.
// Same input always produces the same vector. Semantically similar inputs will NOT produce
// similar vectors (this is a mock), but it's deterministic and normalized.
func deterministicEmbedding(text string, dim int) []float64 {
	// Use multiple rounds of hashing to fill the vector
	vec := make([]float64, dim)
	var sumSq float64

	for i := 0; i < dim; i++ {
		// Hash the text with the index to get different values per dimension
		h := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", i/4, text)))
		// Use 8 bytes from the hash (offset by position within the group)
		offset := (i % 4) * 8
		bits := binary.LittleEndian.Uint64(h[offset : offset+8])
		// Convert to float64 in [-1, 1]
		vec[i] = (float64(bits)/float64(math.MaxUint64))*2 - 1
		sumSq += vec[i] * vec[i]
	}

	// Normalize to unit vector
	norm := math.Sqrt(sumSq)
	if norm > 0 {
		for i := range vec {
			vec[i] /= norm
		}
	}

	return vec
}
