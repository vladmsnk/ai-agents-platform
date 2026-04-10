.PHONY: run stop restart logs test build clean dev

# ── Production (all-in-Docker) ────────────────────────────────────

run: .env config.yaml ## Start everything in Docker
	docker compose up --build -d
	@echo ""
	@echo "  Frontend:   http://localhost:8080"
	@echo "  Grafana:    http://localhost:3000  (admin/admin)"
	@echo "  Prometheus: http://localhost:9090"
	@echo ""

stop: ## Stop all containers
	docker compose down

restart: ## Rebuild and restart everything
	docker compose down
	docker compose up --build -d

logs: ## Tail logs from all services
	docker compose logs -f

# ── Local development ─────────────────────────────────────────────

dev: .env config.yaml ## Start infra in Docker, gateway and frontend locally
	docker compose up -d postgres prometheus grafana --no-deps
	@echo "Waiting for postgres..."
	@until docker compose exec postgres pg_isready -U gateway -q 2>/dev/null; do sleep 1; done
	@echo "Postgres ready. Start the gateway and frontend:"
	@echo ""
	@echo "  Terminal 1:  source .env && export OPENROUTER_API_KEY && go run ./cmd/gateway"
	@echo "  Terminal 2:  cd frontend && npm run dev"
	@echo ""

# ── Tests ─────────────────────────────────────────────────────────

test: ## Run E2E tests (gateway must be running)
	go test ./e2e/ -v -count=1 -timeout 300s

# ── Build ─────────────────────────────────────────────────────────

build: ## Build Go binary
	CGO_ENABLED=0 go build -o bin/gateway ./cmd/gateway

clean: ## Remove build artifacts and Docker volumes
	rm -rf bin/
	docker compose down -v

# ── Bootstrapping ─────────────────────────────────────────────────

.env:
	@echo "Creating .env from .env.example..."
	cp .env.example .env
	@echo "Edit .env and add your API keys, then re-run."
	@exit 1

config.yaml:
	@echo "Creating config.yaml from config.example.yaml..."
	cp config.example.yaml config.yaml
	@echo "Edit config.yaml if needed, then re-run."
	@exit 1

# ── Help ──────────────────────────────────────────────────────────

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
