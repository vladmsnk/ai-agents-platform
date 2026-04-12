# AI Agents Platform

Open-source LLM Gateway and A2A Agent Registry with semantic discovery, smart routing, and full observability.

Proxy OpenAI-compatible requests to multiple LLM providers with load balancing, then let AI agents discover and call each other through the gateway using the [Google A2A protocol](https://a2a-protocol.org/).

## Features

**LLM Gateway**
- OpenAI-compatible `/v1/chat/completions` and `/v1/embeddings` proxy
- Multi-provider load balancing (weighted round-robin, latency-based, priority-based)
- Per-provider circuit breaker, rate limiter, and health checking
- Streaming support (SSE) with TTFT/TPOT tracking
- Token counting and cost tracking per request
- Automatic fallback to next provider on failure

**A2A Agent Registry**
- Register AI agents with descriptions and skills
- Semantic discovery — find agents via natural language queries using embedding similarity
- Agents get vectorized on registration via `/v1/embeddings` (OpenRouter Gemini or mock fallback)
- Cosine similarity search over cached embeddings, ranked results with confidence scores

**A2A Gateway Proxy**
- `POST /a2a` — auto-routes tasks to the best matching agent via semantic search
- `POST /a2a/{agent-id}` — explicit routing to a specific agent
- A2A v1.0 protocol (`message/send`, `message/stream`) with v0.3 backward compatibility
- Agent-to-agent delegation through the gateway with full tracing
- Task lifecycle management (`tasks/get`, `tasks/list`, `tasks/cancel`)

**Authentication (Keycloak)**
- OAuth2 client_credentials flow — each agent gets its own identity
- JWT validation on all protected endpoints via JWKS
- Auto-provisioning: agent registration returns OAuth2 `client_id` + `client_secret`
- Gateway authenticates itself when dispatching to downstream agents
- Optional — disable with `keycloak.enabled: false`

**Observability**
- OpenTelemetry metrics exported to Prometheus (23 metrics across LLM and A2A)
- Distributed tracing via OTLP to Jaeger
- Pre-built Grafana dashboard with 12 panels (LLM + A2A)
- Per-agent health checking every 10 seconds

**Admin UI**
- Provider management — add, edit, test, enable/disable LLM providers
- Model analytics — per-model KPIs, per-provider traffic distribution
- Agent management — 4-tab interface: Overview, Agents, Discover, Playground
- A2A Playground — Postman-like interface to send tasks and see responses live
- Semantic discovery UI with relevance score visualization
- Real-time monitoring dashboard with auto-refresh

## Architecture

```mermaid
flowchart LR
    Client([Client / UI]) -->|Bearer JWT| GW
    GW[**Gateway**\nProxy · Balancer · A2A Router\nAgent Registry · Auth] -->|API key| LLM[LLM Providers\nOpenRouter · OpenAI · Anthropic]
    GW -->|Bearer JWT| Agents[A2A Agents\n8 registered agents]
    Agents -.->|"LLM calls +\ndiscovery"| GW
    GW --> DB[(PostgreSQL)]
    GW <-->|JWKS + tokens| KC[Keycloak\nOAuth2 / OIDC]
    GW -.->|metrics| Obs[Prometheus → Grafana\nJaeger Tracing]
```

## Quick Start

```bash
# Clone
git clone https://github.com/vladmsnk/ai-agents-platform.git
cd ai-agents-platform

# Configure
cp config.example.yaml config.yaml
echo "OPENROUTER_API_KEY=sk-or-..." > .env

# Run everything (gateway + 8 demo agents + monitoring)
docker-compose up --build -d

# Verify
curl http://localhost:8080/health
```

Open http://localhost:8080 for the Admin UI.

### Demo Agents

Docker Compose starts 8 mock agents that auto-register with the gateway:

| Agent | Skills | Delegation |
|-------|--------|------------|
| translator | translate, localize | — |
| summarizer | summarize, extract key points | — |
| code-reviewer | review code, suggest fixes, find bugs | Delegates translation tasks |
| sentiment-analyzer | sentiment analysis, emotion detection | — |
| data-extractor | entity extraction, NER, data parsing | — |
| content-writer | copywriting, blog writing, creative writing | Delegates translation tasks |
| math-solver | math, algebra, calculus, statistics | — |
| security-scanner | vulnerability scanning, OWASP, security audit | Delegates summarization tasks |

## API

### Authentication

When Keycloak is enabled, all API/A2A endpoints require a Bearer token. Open endpoints (`/health`, `/metrics`, `/.well-known/agent.json`) skip auth.

```bash
# Get a token (client_credentials flow)
TOKEN=$(curl -s -X POST 'http://localhost:8180/realms/agents/protocol/openid-connect/token' \
  -d 'grant_type=client_credentials&client_id=test-agent&client_secret=test-secret' \
  | jq -r '.access_token')

# Use the token with any API call
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/agents
```

Registering a new agent auto-provisions a Keycloak client and returns credentials:

```bash
curl -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8080/api/agents \
  -H "Content-Type: application/json" \
  -d '{"id":"my-agent","name":"My Agent","url":"http://my-agent:9001"}'
# Response: {"agent": {...}, "credentials": {"client_id": "agent-my-agent", "client_secret": "..."}}
```

### LLM Proxy

```bash
# Chat completion (OpenAI-compatible)
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model": "google/gemma-4-26b-a4b-it:free", "messages": [{"role": "user", "content": "Hello"}]}'

# Embeddings
curl -X POST http://localhost:8080/v1/embeddings \
  -H "Content-Type: application/json" \
  -d '{"model": "google/gemini-embedding-001", "input": "Hello world"}'
```

### Agent Discovery

```bash
# Semantic search — find the best agent for a task
curl -X POST http://localhost:8080/api/agents/discover \
  -H "Content-Type: application/json" \
  -d '{"query": "translate code review to Japanese", "top_n": 3}'
```

### A2A Protocol

```bash
# Auto-route — gateway picks the best agent
curl -X POST http://localhost:8080/a2a \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "message/send",
    "id": 1,
    "params": {
      "id": "task-1",
      "message": {"role": "user", "parts": [{"type": "text", "text": "Translate hello to Japanese"}]}
    }
  }'

# Explicit route — send to a specific agent
curl -X POST http://localhost:8080/a2a/translator \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "message/send",
    "id": 1,
    "params": {
      "id": "task-2",
      "message": {"role": "user", "parts": [{"type": "text", "text": "Translate hello to French"}]}
    }
  }'
```

### REST API

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/v1/chat/completions` | LLM proxy (OpenAI-compatible) |
| POST | `/v1/embeddings` | Embeddings proxy |
| GET | `/api/providers` | List providers with health status |
| POST | `/api/providers` | Add provider |
| PUT | `/api/providers/{name}` | Update provider |
| DELETE | `/api/providers/{name}` | Delete provider |
| GET | `/api/agents` | List registered agents |
| POST | `/api/agents` | Register agent |
| PUT | `/api/agents/{id}` | Update agent |
| DELETE | `/api/agents/{id}` | Delete agent |
| POST | `/api/agents/discover` | Semantic agent search |
| GET | `/api/agents/{id}/health` | Agent health status |
| POST | `/a2a` | A2A gateway (auto-route) |
| POST | `/a2a/{agent-id}` | A2A proxy (explicit route) |
| GET | `/api/stats` | Request and agent statistics |
| GET | `/api/health` | Provider health statuses |
| GET | `/.well-known/agent.json` | Gateway's A2A agent card |
| GET | `/metrics` | Prometheus metrics |

## Configuration

```yaml
listen: ":8080"
database_url: "postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable"
balancer_strategy: "weighted"  # weighted | latency | priority
jaeger_url: "jaeger:4318"
embedding_model: "google/gemini-embedding-001"
gateway_url: "http://localhost:8080"

keycloak:
  url: "http://keycloak:8180"
  realm: "agents"
  client_id: "gateway"
  client_secret: "gateway-secret"
  enabled: true  # set to false to disable auth

providers:
  - name: openrouter
    url: https://openrouter.ai/api
    key_env: OPENROUTER_API_KEY
    models:
      - google/gemma-4-26b-a4b-it:free
      - openai/gpt-4o-mini
    weight: 5
    priority: 1
    timeout_seconds: 120
```

Environment variables: `DATABASE_URL`, `GATEWAY_URL`, `KEYCLOAK_URL`, `OPENROUTER_API_KEY` (or any key via `key_env`).

## Services

| Service | Port | URL |
|---------|------|-----|
| Admin UI | 8080 | http://localhost:8080 |
| Keycloak | 8180 | http://localhost:8180 |
| Grafana | 3000 | http://localhost:3000 |
| Jaeger | 16686 | http://localhost:16686 |
| Prometheus | 9090 | http://localhost:9090 |
| PostgreSQL | 5432 | localhost:5432 |

## Tech Stack

- **Backend**: Go 1.25, stdlib `net/http`
- **Database**: PostgreSQL 17 + pgx/v5
- **Frontend**: React 19, TypeScript, Tailwind CSS 4, Vite, Recharts
- **Observability**: OpenTelemetry, Prometheus, Grafana, Jaeger
- **Protocol**: [Google A2A](https://a2a-protocol.org/) v1.0 with v0.3 backward compatibility
- **Auth**: Keycloak 26.2 (OAuth2 client_credentials, JWT/JWKS)
- **Embeddings**: OpenRouter (Gemini Embedding 001) with deterministic mock fallback
- **Containers**: Docker Compose (15 services)

## Testing

```bash
# Run basic E2E tests (69 tests — auto-acquires auth token if Keycloak is available)
./scripts/test-a2a-e2e.sh

# Run auth E2E tests (37 tests — Keycloak integration, token validation, provisioning)
./scripts/test-auth-e2e.sh

# Run advanced tests (61 tests — concurrency, delegation chains, edge cases)
./scripts/test-a2a-advanced.sh

# Load test the LLM proxy
./scripts/load-test.sh [requests] [concurrency]
```

## Development

```bash
# Backend
go build ./...
go vet ./...

# Frontend
cd frontend && npm ci && npm run dev
```

## License

MIT
