package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/shirou/gopsutil/v4/cpu"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

type Metrics struct {
	RequestsTotal  otelmetric.Int64Counter
	RequestLatency otelmetric.Float64Histogram
	ActiveRequests otelmetric.Int64UpDownCounter
	CPUUsage       otelmetric.Float64ObservableGauge
	GoroutineCount otelmetric.Int64ObservableGauge

	// LLM-specific metrics
	TTFT              otelmetric.Float64Histogram
	TPOT              otelmetric.Float64Histogram
	InputTokensTotal  otelmetric.Int64Counter
	OutputTokensTotal otelmetric.Int64Counter
	RequestCost       otelmetric.Float64Counter

	provider *sdkmetric.MeterProvider
	stop     chan struct{}
}

func Setup() (*Metrics, error) {
	exporter, err := otelprometheus.New()
	if err != nil {
		return nil, fmt.Errorf("prometheus exporter: %w", err)
	}

	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	otel.SetMeterProvider(provider)

	meter := provider.Meter("llm-gateway")

	m := &Metrics{
		provider: provider,
		stop:     make(chan struct{}),
	}

	m.RequestsTotal, err = meter.Int64Counter("gateway.requests.total",
		otelmetric.WithDescription("Total number of proxied requests"),
	)
	if err != nil {
		return nil, err
	}

	m.RequestLatency, err = meter.Float64Histogram("gateway.request.duration",
		otelmetric.WithDescription("Request latency in milliseconds"),
		otelmetric.WithUnit("ms"),
		otelmetric.WithExplicitBucketBoundaries(10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000),
	)
	if err != nil {
		return nil, err
	}

	m.ActiveRequests, err = meter.Int64UpDownCounter("gateway.requests.active",
		otelmetric.WithDescription("Number of in-flight requests"),
	)
	if err != nil {
		return nil, err
	}

	m.TTFT, err = meter.Float64Histogram("gateway.ttft",
		otelmetric.WithDescription("Time to first token in milliseconds"),
		otelmetric.WithUnit("ms"),
		otelmetric.WithExplicitBucketBoundaries(10, 25, 50, 100, 250, 500, 1000, 2500, 5000),
	)
	if err != nil {
		return nil, err
	}

	m.TPOT, err = meter.Float64Histogram("gateway.tpot",
		otelmetric.WithDescription("Time per output token in milliseconds"),
		otelmetric.WithUnit("ms"),
		otelmetric.WithExplicitBucketBoundaries(1, 5, 10, 25, 50, 100, 250, 500),
	)
	if err != nil {
		return nil, err
	}

	m.InputTokensTotal, err = meter.Int64Counter("gateway.tokens.input",
		otelmetric.WithDescription("Total input tokens processed"),
	)
	if err != nil {
		return nil, err
	}

	m.OutputTokensTotal, err = meter.Int64Counter("gateway.tokens.output",
		otelmetric.WithDescription("Total output tokens generated"),
	)
	if err != nil {
		return nil, err
	}

	m.RequestCost, err = meter.Float64Counter("gateway.request.cost",
		otelmetric.WithDescription("Total request cost in USD"),
		otelmetric.WithUnit("USD"),
	)
	if err != nil {
		return nil, err
	}

	m.CPUUsage, err = meter.Float64ObservableGauge("gateway.cpu.usage",
		otelmetric.WithDescription("CPU usage percentage"),
		otelmetric.WithUnit("%"),
		otelmetric.WithFloat64Callback(func(_ context.Context, o otelmetric.Float64Observer) error {
			percents, err := cpu.Percent(0, false)
			if err != nil || len(percents) == 0 {
				return nil
			}
			o.Observe(percents[0])
			return nil
		}),
	)
	if err != nil {
		return nil, err
	}

	m.GoroutineCount, err = meter.Int64ObservableGauge("gateway.goroutines",
		otelmetric.WithDescription("Number of goroutines"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			o.Observe(int64(runtime.NumGoroutine()))
			return nil
		}),
	)
	if err != nil {
		return nil, err
	}

	return m, nil
}

func (m *Metrics) RecordRequest(ctx context.Context, provider, model string, statusCode int, latency time.Duration, isError bool) {
	attrs := otelmetric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("model", model),
		attribute.Int("status_code", statusCode),
		attribute.Bool("error", isError),
	)
	m.RequestsTotal.Add(ctx, 1, attrs)
	m.RequestLatency.Record(ctx, float64(latency.Milliseconds()), attrs)
}

func (m *Metrics) RecordTokens(ctx context.Context, provider, model string, inputTokens, outputTokens int, cost float64) {
	attrs := otelmetric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("model", model),
	)
	if inputTokens > 0 {
		m.InputTokensTotal.Add(ctx, int64(inputTokens), attrs)
	}
	if outputTokens > 0 {
		m.OutputTokensTotal.Add(ctx, int64(outputTokens), attrs)
	}
	if cost > 0 {
		m.RequestCost.Add(ctx, cost, attrs)
	}
}

func (m *Metrics) RecordTTFT(ctx context.Context, provider, model string, ttft time.Duration) {
	attrs := otelmetric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("model", model),
	)
	m.TTFT.Record(ctx, float64(ttft.Milliseconds()), attrs)
}

func (m *Metrics) RecordTPOT(ctx context.Context, provider, model string, tpot time.Duration) {
	attrs := otelmetric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("model", model),
	)
	m.TPOT.Record(ctx, float64(tpot.Milliseconds()), attrs)
}

func (m *Metrics) TrackInflight(ctx context.Context, delta int64) {
	m.ActiveRequests.Add(ctx, delta)
}

func (m *Metrics) Shutdown(ctx context.Context) error {
	close(m.stop)
	return m.provider.Shutdown(ctx)
}

func Handler() http.Handler {
	return promhttp.Handler()
}
