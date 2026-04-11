package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/shirou/gopsutil/v4/cpu"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Tracer returns the global tracer for the gateway.
func Tracer() trace.Tracer {
	return otel.Tracer("llm-gateway")
}

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

	// A2A agent metrics
	A2ARequestsTotal    otelmetric.Int64Counter
	A2ARequestLatency   otelmetric.Float64Histogram
	A2ARoutingScore     otelmetric.Float64Histogram
	A2ARoutingDuration  otelmetric.Float64Histogram
	A2ATasksActive      otelmetric.Int64UpDownCounter
	AgentsTotal         otelmetric.Int64ObservableGauge
	AgentsHealthy       otelmetric.Int64ObservableGauge
	AgentsUnhealthy     otelmetric.Int64ObservableGauge
	AgentHealthDuration otelmetric.Float64Histogram
	DiscoverRequests    otelmetric.Int64Counter
	DiscoverDuration    otelmetric.Float64Histogram
	EmbeddingRequests   otelmetric.Int64Counter
	EmbeddingDuration   otelmetric.Float64Histogram

	// Agent count callbacks (set externally)
	agentCountsMu      sync.Mutex
	agentCountTotal    int64
	agentCountHealthy  int64
	agentCountUnhealthy int64

	meterProvider *sdkmetric.MeterProvider
	traceProvider *sdktrace.TracerProvider
	stop          chan struct{}
}

// Setup initializes metrics (Prometheus) and tracing (OTLP → Jaeger).
// jaegerURL is the OTLP HTTP endpoint (e.g. "http://jaeger:4318"). Empty = tracing disabled.
func Setup(jaegerURL string) (*Metrics, error) {
	exporter, err := otelprometheus.New()
	if err != nil {
		return nil, fmt.Errorf("prometheus exporter: %w", err)
	}

	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	otel.SetMeterProvider(meterProvider)

	m := &Metrics{
		meterProvider: meterProvider,
		stop:          make(chan struct{}),
	}

	// Tracing
	if jaegerURL != "" {
		traceExporter, err := otlptracehttp.New(
			context.Background(),
			otlptracehttp.WithEndpoint(jaegerURL),
			otlptracehttp.WithInsecure(),
		)
		if err != nil {
			return nil, fmt.Errorf("trace exporter: %w", err)
		}

		res := resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("llm-gateway"),
			semconv.ServiceVersion("2.0.0"),
		)

		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExporter),
			sdktrace.WithSampler(sdktrace.AlwaysSample()),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(tp)
		m.traceProvider = tp
	}

	meter := meterProvider.Meter("llm-gateway")

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

	// A2A metrics
	m.A2ARequestsTotal, err = meter.Int64Counter("gateway.a2a.requests.total",
		otelmetric.WithDescription("Total A2A tasks processed"),
	)
	if err != nil {
		return nil, err
	}

	m.A2ARequestLatency, err = meter.Float64Histogram("gateway.a2a.request.duration",
		otelmetric.WithDescription("A2A task latency in milliseconds"),
		otelmetric.WithUnit("ms"),
		otelmetric.WithExplicitBucketBoundaries(50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000),
	)
	if err != nil {
		return nil, err
	}

	m.A2ARoutingScore, err = meter.Float64Histogram("gateway.a2a.routing.score",
		otelmetric.WithDescription("Semantic routing confidence score distribution"),
		otelmetric.WithExplicitBucketBoundaries(0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0),
	)
	if err != nil {
		return nil, err
	}

	m.A2ARoutingDuration, err = meter.Float64Histogram("gateway.a2a.routing.duration",
		otelmetric.WithDescription("Time spent on semantic routing (embed + cosine) in ms"),
		otelmetric.WithUnit("ms"),
		otelmetric.WithExplicitBucketBoundaries(5, 10, 25, 50, 100, 250, 500, 1000),
	)
	if err != nil {
		return nil, err
	}

	m.A2ATasksActive, err = meter.Int64UpDownCounter("gateway.a2a.tasks.active",
		otelmetric.WithDescription("Number of in-flight A2A tasks"),
	)
	if err != nil {
		return nil, err
	}

	m.AgentsTotal, err = meter.Int64ObservableGauge("gateway.agents.total",
		otelmetric.WithDescription("Total registered agents"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			m.agentCountsMu.Lock()
			o.Observe(m.agentCountTotal)
			m.agentCountsMu.Unlock()
			return nil
		}),
	)
	if err != nil {
		return nil, err
	}

	m.AgentsHealthy, err = meter.Int64ObservableGauge("gateway.agents.healthy",
		otelmetric.WithDescription("Number of healthy agents"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			m.agentCountsMu.Lock()
			o.Observe(m.agentCountHealthy)
			m.agentCountsMu.Unlock()
			return nil
		}),
	)
	if err != nil {
		return nil, err
	}

	m.AgentsUnhealthy, err = meter.Int64ObservableGauge("gateway.agents.unhealthy",
		otelmetric.WithDescription("Number of unhealthy agents"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			m.agentCountsMu.Lock()
			o.Observe(m.agentCountUnhealthy)
			m.agentCountsMu.Unlock()
			return nil
		}),
	)
	if err != nil {
		return nil, err
	}

	m.AgentHealthDuration, err = meter.Float64Histogram("gateway.agent.health.duration",
		otelmetric.WithDescription("Agent health check ping latency in ms"),
		otelmetric.WithUnit("ms"),
		otelmetric.WithExplicitBucketBoundaries(5, 10, 25, 50, 100, 250, 500, 1000, 5000),
	)
	if err != nil {
		return nil, err
	}

	m.DiscoverRequests, err = meter.Int64Counter("gateway.discover.requests",
		otelmetric.WithDescription("Total semantic discover requests"),
	)
	if err != nil {
		return nil, err
	}

	m.DiscoverDuration, err = meter.Float64Histogram("gateway.discover.duration",
		otelmetric.WithDescription("Discover endpoint latency in ms"),
		otelmetric.WithUnit("ms"),
		otelmetric.WithExplicitBucketBoundaries(10, 25, 50, 100, 250, 500, 1000, 2500),
	)
	if err != nil {
		return nil, err
	}

	m.EmbeddingRequests, err = meter.Int64Counter("gateway.embeddings.requests",
		otelmetric.WithDescription("Total embedding requests"),
	)
	if err != nil {
		return nil, err
	}

	m.EmbeddingDuration, err = meter.Float64Histogram("gateway.embeddings.duration",
		otelmetric.WithDescription("Embedding request latency in ms"),
		otelmetric.WithUnit("ms"),
		otelmetric.WithExplicitBucketBoundaries(10, 25, 50, 100, 250, 500, 1000, 2500),
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

// --- A2A recording methods ---

func (m *Metrics) RecordA2ARequest(ctx context.Context, agent, method, routingType string, latency time.Duration, isError bool) {
	attrs := otelmetric.WithAttributes(
		attribute.String("agent", agent),
		attribute.String("method", method),
		attribute.String("routing_type", routingType),
		attribute.Bool("error", isError),
	)
	m.A2ARequestsTotal.Add(ctx, 1, attrs)
	m.A2ARequestLatency.Record(ctx, float64(latency.Milliseconds()), attrs)
}

func (m *Metrics) RecordA2ARouting(ctx context.Context, agent string, score float64, routingDuration time.Duration) {
	m.A2ARoutingScore.Record(ctx, score, otelmetric.WithAttributes(attribute.String("agent", agent)))
	m.A2ARoutingDuration.Record(ctx, float64(routingDuration.Milliseconds()))
}

func (m *Metrics) TrackA2AInflight(ctx context.Context, delta int64) {
	m.A2ATasksActive.Add(ctx, delta)
}

func (m *Metrics) RecordDiscover(ctx context.Context, topResult string, latency time.Duration) {
	m.DiscoverRequests.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("top_result", topResult)))
	m.DiscoverDuration.Record(ctx, float64(latency.Milliseconds()))
}

func (m *Metrics) RecordEmbedding(ctx context.Context, model, provider string, statusCode int, latency time.Duration) {
	attrs := otelmetric.WithAttributes(
		attribute.String("model", model),
		attribute.String("provider", provider),
		attribute.Int("status_code", statusCode),
	)
	m.EmbeddingRequests.Add(ctx, 1, attrs)
	m.EmbeddingDuration.Record(ctx, float64(latency.Milliseconds()), attrs)
}

func (m *Metrics) RecordAgentHealth(ctx context.Context, agent string, healthy bool, latency time.Duration) {
	m.AgentHealthDuration.Record(ctx, float64(latency.Milliseconds()),
		otelmetric.WithAttributes(
			attribute.String("agent", agent),
			attribute.Bool("healthy", healthy),
		),
	)
}

func (m *Metrics) SetAgentCounts(total, healthy, unhealthy int) {
	m.agentCountsMu.Lock()
	m.agentCountTotal = int64(total)
	m.agentCountHealthy = int64(healthy)
	m.agentCountUnhealthy = int64(unhealthy)
	m.agentCountsMu.Unlock()
}

func (m *Metrics) Shutdown(ctx context.Context) error {
	close(m.stop)
	var errs []error
	if m.traceProvider != nil {
		errs = append(errs, m.traceProvider.Shutdown(ctx))
	}
	errs = append(errs, m.meterProvider.Shutdown(ctx))
	return errors.Join(errs...)
}

func Handler() http.Handler {
	return promhttp.Handler()
}
