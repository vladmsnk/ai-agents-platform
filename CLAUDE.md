# CLAUDE.md — AI Agents Platform

## What is this project?

An LLM Gateway / AI Agents Platform that proxies OpenAI-compatible `/v1/chat/completions` requests to multiple LLM providers with load balancing, health checking, observability, and an A2A agent registry.

## Tech Stack

- **Backend**: Go 1.25, stdlib `net/http` (no framework)
- **Database**: PostgreSQL 17 via `pgx/v5` connection pool
- **Frontend**: React 19 + TypeScript + Tailwind CSS 4 + Vite
- **Observability**: OpenTelemetry (metrics → Prometheus, traces → Jaeger), Grafana dashboards
- **Containers**: Docker Compose with 6 services (postgres, gateway, frontend, prometheus, grafana, jaeger)

## Architecture

```
cmd/gateway/main.go          — entry point, wires all components
internal/
  config/config.go           — YAML config loading, Provider struct, strategy constants
  storage/postgres.go        — PostgreSQL CRUD for providers (pgxpool, auto-migration)
  storage/agents.go          — PostgreSQL CRUD for A2A agents
  balancer/balancer.go       — Strategy-based provider selection with health/circuit/ratelimit scoring
  balancer/circuit.go        — Per-provider circuit breaker (closed/open/half-open)
  balancer/strategy_*.go     — Weighted round-robin, latency-based, priority-based strategies
  ratelimit/ratelimit.go     — Per-provider token-bucket rate limiter (x/time/rate)
  health/health.go           — Periodic health checker, implements balancer.HealthSource
  proxy/proxy.go             — HTTP reverse proxy with fallback, tracing spans, token/cost tracking
  proxy/embeddings.go        — /v1/embeddings proxy with mock fallback (deterministic hash-based vectors)
  proxy/tokencount.go        — Parse usage from OpenAI responses (streaming + non-streaming)
  proxy/timing.go            — TimingReader for TTFT/TPOT measurement
  stats/stats.go             — In-memory request stats with time series, background cleanup
  telemetry/telemetry.go     — OTel metrics (Prometheus) + tracing (OTLP → Jaeger)
  api/api.go                 — REST API handlers for providers, agents, stats, health
  a2a/agentcard.go           — Google A2A spec types (AgentCard, JSON-RPC, Task)
  a2a/handler.go             — A2A v1.0 JSON-RPC gateway with semantic routing (message/send, message/stream, tasks/get, tasks/cancel, tasks/list) + v0.3 backward compat
  a2a/registry.go            — Agent registry with semantic search (in-memory cache + DB + embeddings)
  a2a/embedder.go            — Embedding client, calls gateway's /v1/embeddings for vectorization
  a2a/healthcheck.go         — Background agent health checker (pings /health every 10s)
frontend/src/
  api.ts                     — TypeScript API client with all types
  App.tsx                    — Layout with sidebar navigation
  pages/Providers.tsx        — Provider CRUD with pricing, rate limit, priority fields
  pages/Models.tsx           — Model analytics dashboard: per-model KPIs, per-provider traffic distribution
  pages/Agents.tsx           — A2A agent registration/management with semantic discover search
  pages/Monitoring.tsx       — Real-time dashboard: metric cards, Grafana panels, bar charts, stats table, error feed
```

## Key Design Decisions

- **Strategy pattern for balancer**: `balancer.Strategy` interface with 3 implementations. Configurable via `balancer_strategy` in config.yaml (`weighted`|`latency`|`priority`). Use `config.StrategyWeighted` etc. constants.
- **Circuit breaker uses Mutex (not RWMutex)**: To avoid TOCTOU race during state transitions in `IsOpen()`.
- **Rate limiter has `CanAllow()` (non-consuming) vs `Allow()` (consuming)**: `CanAllow` is used in balancer scoring to avoid wasting tokens on candidates that won't be selected.
- **Sentinel errors**: `storage.ErrNotFound` used across storage layer; check with `errors.Is()`.
- **Provider defaults**: Use `config.ApplyDefaults()` — sets weight=1, timeout=60, priority=10.
- **Shared HTTP clients**: Proxy has `client` (with timeout) and `streamClient` (no timeout). A2A handler has its own shared `client`. Never create per-request clients.
- **OTel tracing spans**: Parent `llm.chat_completion` per request, child `llm.provider_call` per provider attempt, `a2a.dispatch` for agent calls. Always pass `context.Context` for trace propagation.
- **Stats cleanup**: Background goroutine in `stats.Collector` runs every 30s. A2A task entries have 30min TTL.
- **Agent embeddings**: Stored as `float8[]` in Postgres (no pgvector). Cosine similarity computed in-memory over cached agents. Sufficient for <1000 agents.
- **Mock embeddings**: When no provider supports the embedding model, gateway returns deterministic hash-based vectors (SHA-256 seeded). Same input always gives same vector, enabling meaningful cosine similarity in demos.
- **Agent health checker**: Background goroutine pings each agent's `/health` every 10s. Updates status to `active`/`unhealthy`. Unhealthy agents excluded from discover results by default.
- **Embedder self-calls**: Registry's `Embedder` calls the gateway's own `/v1/embeddings` endpoint. Config: `gateway_url` + `embedding_model` in config.yaml.
- **A2A semantic routing**: `POST /a2a` extracts message text, calls `Discover()` to find the best matching agent, dispatches to it. `POST /a2a/{agent-id}` bypasses discovery for explicit targeting.
- **A2A protocol v1.0 + v0.3 compat**: Handler accepts both `message/send` and `tasks/send`, both `message/stream` and `tasks/sendSubscribe`. Dispatch always uses `message/send` to downstream agents.
- **Discover results include `proxy_url`**: Each result has a `proxy_url` field (e.g. `http://gateway:8080/a2a/translator`) so agents can route through the gateway.

## Build & Run

```bash
# Local development
go build ./...
cd frontend && npm ci && npm run dev

# Docker (all 9 services: 6 core + 3 mock agents)
docker-compose up --build -d

# Run load test
./scripts/load-test.sh [requests] [concurrency]
```

## Services & Ports

| Service    | Port  | URL                        |
|------------|-------|----------------------------|
| Frontend   | 8080  | http://localhost:8080       |
| Grafana    | 3000  | http://localhost:3000       |
| Jaeger UI  | 16686 | http://localhost:16686      |
| Prometheus | 9090  | http://localhost:9090       |
| PostgreSQL | 5432  | localhost:5432              |
| Jaeger OTLP| 4318  | (internal, gateway → jaeger)|

## API Endpoints

- `POST /v1/chat/completions` — proxy to LLM providers
- `POST /v1/embeddings` — proxy to embedding providers (falls back to mock deterministic vectors)
- `GET /api/providers` — list providers (with health)
- `POST /api/providers` — add provider
- `PUT /api/providers/{name}` — update provider
- `DELETE /api/providers/{name}` — delete provider
- `GET /api/stats` — request stats (tokens, cost, latency, TTFT, time series)
- `GET /api/health` — all provider health statuses
- `GET /api/agents` — list A2A agents
- `POST /api/agents` — register agent (auto-computes embedding)
- `POST /api/agents/discover` — semantic search: `{"query": "...", "top_n": 5, "min_score": 0.1}`
- `GET /api/agents/{id}/health` — cached agent health status
- `GET /.well-known/agent.json` — gateway's own A2A agent card
- `POST /a2a` — A2A JSON-RPC gateway (semantic auto-routing to best agent)
- `POST /a2a/{agent-id}` — A2A JSON-RPC proxy to specific agent
- `GET /metrics` — Prometheus metrics
- `GET /health` — gateway health check

## Code Conventions

- **Error handling**: Use sentinel errors (`storage.ErrNotFound`), check with `errors.Is()`. Never compare error strings.
- **Database queries**: Use `providerColumns` constant and `scanProvider()`/`providerValues()` helpers to keep column lists in sync. Agents use `scanAgent()`/`marshalAgentJSON()`.
- **Config defaults**: All in `config.ApplyDefaults()`, not scattered across handlers.
- **Provider URL map**: Use `config.ProviderURLs()` helper, not inline loops.
- **Metrics recording**: Use `proxy.record()` helper for error paths (stats + metrics). Success path records directly due to additional token/cost/trace data.
- **Frontend types**: All API types in `frontend/src/api.ts`. Keep in sync with Go JSON tags.
- **Grafana dashboard**: JSON at `monitoring/grafana/dashboards/llm-gateway.json`. Metric names use OTel convention (dots → underscores in Prometheus, e.g., `gateway.requests.total` → `gateway_requests_total`).
