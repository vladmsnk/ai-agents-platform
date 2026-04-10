# Release Notes

## v1.0.0 — Initial Release

### Features

**LLM Gateway** — a reverse proxy that routes OpenAI-compatible `/v1/chat/completions` requests to multiple LLM providers with weighted round-robin load balancing and automatic fallback.

#### Backend (Go)
- **Proxy**: Forwards chat completion requests to configured providers with connection pooling, streaming support, and per-provider timeout via context
- **Load Balancing**: Weighted round-robin across providers per model, with fallback to alternate providers on failure
- **Health Checking**: Periodic health checks (every 10s) with concurrent polling of all providers
- **REST API**: Full CRUD for providers (`/api/providers`), stats (`/api/stats`), and health (`/api/health`)
- **Stats Collector**: In-memory request statistics with time series, percentile latencies (P50/P95), error rates, and RPM — with automatic background cleanup
- **Telemetry**: OpenTelemetry + Prometheus metrics (request count, latency histogram, active requests, CPU usage, goroutine count)
- **PostgreSQL Storage**: Provider configuration persisted in PostgreSQL with auto-migration
- **Config**: YAML-based configuration with environment variable overrides for secrets

#### Frontend (React + TypeScript)
- **Providers Page**: Add, edit, delete, enable/disable providers with test-connection support and orphaned model warnings
- **Models Page**: View all models with their provider distribution, adjust weights with live percentage preview, apply in parallel
- **Monitoring Page**: Real-time stats dashboard (5s polling) with Grafana panel embeds, provider stats table, and error feed

#### Infrastructure
- Docker and Docker Compose setup with PostgreSQL, Prometheus, and Grafana
- Grafana dashboard with 9 panels (request rate, latency, error rate, CPU, goroutines, traffic distribution, response codes)
- End-to-end test scaffold

## v2.0.0 — AI Agents Platform

### A2A Agent Registry (Google A2A spec)
- `GET /.well-known/agent.json` — gateway's own agent card
- `POST /a2a` — JSON-RPC endpoint (tasks/send, tasks/sendSubscribe with SSE, tasks/get, tasks/cancel)
- `GET/POST/PUT/DELETE /api/agents` — admin CRUD for agent registration
- Agents page in admin UI for registration and management
- PostgreSQL-backed agent storage with in-memory cache

### Enhanced Provider Model
- New fields: `price_per_input_token`, `price_per_output_token`, `rate_limit_rpm`, `priority`
- Pricing and rate limit configuration in admin UI
- Database migration for existing installations

### Advanced Load Balancer
- **Strategy interface** with 3 implementations: weighted round-robin, latency-based, priority-based
- **Circuit breaker** — per-provider closed/open/half-open states with configurable failure threshold and recovery timeout
- **Per-provider rate limiter** — token-bucket based on RPM, non-consuming peek for candidate scoring
- **Health-aware routing** — unhealthy, circuit-open, and rate-limited providers are skipped automatically
- Configurable via `balancer_strategy` in config (`weighted`, `latency`, `priority`)

### LLM Observability
- **Token counting** — parses `usage` from streaming and non-streaming OpenAI-compatible responses
- **TTFT** (Time to First Token) — measured via response body wrapper
- **TPOT** (Time Per Output Token) — computed from streaming chunk timing
- **Cost tracking** — per-request cost based on provider pricing
- 5 new Prometheus/OTel metrics: `gateway.ttft`, `gateway.tpot`, `gateway.tokens.input`, `gateway.tokens.output`, `gateway.request.cost`
- Extended stats API with token counts, cost, and avg TTFT per provider
- Monitoring page updated with token/cost/TTFT cards and table columns

### MLflow Tracing
- Each LLM request logged as an MLflow run with params (model, provider, stream) and metrics (latency, TTFT, TPOT, tokens, cost)
- MLflow server added to docker-compose (port 5000)
- Configurable via `mlflow_url` in config (empty = disabled)
