package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/vymoiseenkov/ai-agents-platform/internal/a2a"
	"github.com/vymoiseenkov/ai-agents-platform/internal/api"
	"github.com/vymoiseenkov/ai-agents-platform/internal/auth"
	"github.com/vymoiseenkov/ai-agents-platform/internal/balancer"
	"github.com/vymoiseenkov/ai-agents-platform/internal/config"
	"github.com/vymoiseenkov/ai-agents-platform/internal/health"
	"github.com/vymoiseenkov/ai-agents-platform/internal/proxy"
	"github.com/vymoiseenkov/ai-agents-platform/internal/ratelimit"
	"github.com/vymoiseenkov/ai-agents-platform/internal/stats"
	"github.com/vymoiseenkov/ai-agents-platform/internal/storage"
	"github.com/vymoiseenkov/ai-agents-platform/internal/telemetry"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger.Info("loaded config",
		"providers_in_config", len(cfg.Providers),
		"listen", cfg.Listen,
		"database", cfg.DatabaseURL != "",
		"balancer_strategy", cfg.BalancerStrategy,
	)

	// OpenTelemetry metrics + tracing
	metrics, err := telemetry.Setup(cfg.JaegerURL)
	if err != nil {
		logger.Error("failed to setup telemetry", "error", err)
		os.Exit(1)
	}
	if cfg.JaegerURL != "" {
		logger.Info("distributed tracing enabled", "jaeger", cfg.JaegerURL)
	}
	defer metrics.Shutdown(context.Background())

	// Database
	ctx := context.Background()
	store, err := storage.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	logger.Info("connected to database")

	// Seed providers from config file (only inserts if not already present)
	for _, p := range cfg.Providers {
		if err := store.UpsertProvider(ctx, p); err != nil {
			logger.Error("failed to seed provider", "name", p.Name, "error", err)
		}
	}

	// Load providers from DB and resolve env-based keys
	providers, err := store.ListProviders(ctx)
	if err != nil {
		logger.Error("failed to load providers from db", "error", err)
		os.Exit(1)
	}
	for i := range providers {
		config.ApplyDefaults(&providers[i])
	}
	logger.Info("loaded providers from database", "count", len(providers))

	// Health checker
	checker := health.NewChecker(logger)
	checker.SetProviders(config.ProviderURLs(providers))
	checker.Start()
	defer checker.Stop()

	// Rate limiter
	limiter := ratelimit.New()
	for _, p := range providers {
		limiter.SetProvider(p.Name, p.RateLimitRPM)
	}

	// Circuit breaker
	circuit := balancer.NewCircuitBreaker(5, 30*time.Second)

	// Balancer strategy
	var strategy balancer.Strategy
	switch cfg.BalancerStrategy {
	case config.StrategyLatency:
		strategy = balancer.NewLatencyBased()
		logger.Info("using latency-based balancer strategy")
	case config.StrategyPriority:
		strategy = balancer.NewPriorityBased()
		logger.Info("using priority-based balancer strategy")
	default:
		strategy = balancer.NewWeightedRoundRobin()
		logger.Info("using weighted round-robin balancer strategy")
	}

	b := balancer.New(providers, strategy, circuit, limiter, checker)

	collector := stats.NewCollector()
	defer collector.Stop()

	for _, m := range b.Models() {
		logger.Info("registered model", "model", m)
	}

	// Keycloak auth setup (optional — disabled if keycloak config is absent or enabled=false)
	var authMiddleware *auth.Middleware
	var tokenSource a2a.TokenSource
	var keycloakAdmin *auth.KeycloakAdmin
	if cfg.Keycloak != nil && cfg.Keycloak.Enabled {
		authMiddleware = auth.NewMiddleware(cfg.Keycloak.URL, cfg.Keycloak.IssuerURL, cfg.Keycloak.Realm, logger)
		if err := authMiddleware.WarmUp(ctx); err != nil {
			logger.Warn("auth: failed to pre-fetch JWKS (will retry on first request)", "error", err)
		}
		tokenClient := auth.NewTokenClient(cfg.Keycloak.URL, cfg.Keycloak.Realm, cfg.Keycloak.ClientID, cfg.Keycloak.ClientSecret)
		tokenSource = tokenClient
		keycloakAdmin = auth.NewKeycloakAdmin(cfg.Keycloak.URL, cfg.Keycloak.Realm, tokenClient, logger)
		logger.Info("keycloak auth enabled",
			"url", cfg.Keycloak.URL,
			"realm", cfg.Keycloak.Realm,
			"client_id", cfg.Keycloak.ClientID,
		)
	} else {
		logger.Info("keycloak auth disabled — all endpoints are open")
	}

	// A2A Agent Registry with semantic search
	embedder := a2a.NewEmbedder(cfg.GatewayURL, cfg.EmbeddingModel, tokenSource)
	registry := a2a.NewRegistry(store, embedder, logger)
	if err := registry.LoadFromDB(ctx); err != nil {
		logger.Error("failed to load agents from db", "error", err)
		os.Exit(1)
	}
	logger.Info("loaded agents from database", "count", len(registry.List()))

	// Agent health checker
	agentHealth := a2a.NewAgentHealthChecker(registry, logger, metrics)
	agentHealth.Start()
	defer agentHealth.Stop()

	selfCard := a2a.AgentCard{
		ID:          "llm-gateway",
		Name:        "LLM Gateway",
		Description: "AI Agents Platform — multi-provider LLM gateway with semantic agent routing and load balancing",
		URL:         "http://localhost" + cfg.Listen,
		Version:     "1.0.0",
		Capabilities: a2a.Capabilities{
			Streaming:              true,
			PushNotifications:      false,
			StateTransitionHistory: false,
		},
		Skills: []a2a.Skill{
			{ID: "route", Name: "Smart Routing", Description: "Routes tasks to the best matching agent using semantic search"},
			{ID: "llm-proxy", Name: "LLM Proxy", Description: "Proxies chat completions and embeddings to multiple LLM providers"},
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
	}
	a2aHandler := a2a.NewHandler(registry, logger, selfCard, metrics, collector, tokenSource)

	p := proxy.New(b, logger, collector, metrics)
	mgmt := api.New(store, b, checker, collector, registry, logger, cfg.GatewayURL, metrics, keycloakAdmin)

	mux := http.NewServeMux()
	mux.Handle("/v1/chat/completions", p)
	mux.Handle("/v1/embeddings", p.EmbeddingsHandler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("/metrics", telemetry.Handler())
	mgmt.Register(mux)
	a2aHandler.Register(mux)

	var handler http.Handler = corsMiddleware(mux)
	// Wrap only API/A2A/proxy routes with JWT validation; keep health, metrics, well-known open.
	if authMiddleware != nil {
		handler = authWithExclusions(authMiddleware, handler)
	}

	logger.Info("starting gateway", "listen", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, handler); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

// authWithExclusions applies JWT auth to all routes except health, metrics, and agent card discovery.
func authWithExclusions(m *auth.Middleware, next http.Handler) http.Handler {
	protected := m.Protect(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health",
			r.URL.Path == "/metrics",
			r.URL.Path == "/.well-known/agent.json":
			next.ServeHTTP(w, r)
		default:
			protected.ServeHTTP(w, r)
		}
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
