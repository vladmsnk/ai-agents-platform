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
