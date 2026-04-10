package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"

	"github.com/vymoiseenkov/ai-agents-platform/internal/api"
	"github.com/vymoiseenkov/ai-agents-platform/internal/balancer"
	"github.com/vymoiseenkov/ai-agents-platform/internal/config"
	"github.com/vymoiseenkov/ai-agents-platform/internal/health"
	"github.com/vymoiseenkov/ai-agents-platform/internal/proxy"
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
	)

	// OpenTelemetry metrics
	metrics, err := telemetry.Setup()
	if err != nil {
		logger.Error("failed to setup telemetry", "error", err)
		os.Exit(1)
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

	// Load providers from DB
	providers, err := store.ListProviders(ctx)
	if err != nil {
		logger.Error("failed to load providers from db", "error", err)
		os.Exit(1)
	}
	logger.Info("loaded providers from database", "count", len(providers))

	b := balancer.New(providers)
	collector := stats.NewCollector()
	defer collector.Stop()
	checker := health.NewChecker(logger)

	checker.SetProviders(config.ProviderURLs(providers))
	checker.Start()
	defer checker.Stop()

	for _, m := range b.Models() {
		logger.Info("registered model", "model", m)
	}

	p := proxy.New(b, logger, collector, metrics)
	mgmt := api.New(store, b, checker, collector, logger)

	mux := http.NewServeMux()
	mux.Handle("/v1/chat/completions", p)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("/metrics", telemetry.Handler())
	mgmt.Register(mux)

	handler := corsMiddleware(mux)

	logger.Info("starting gateway", "listen", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, handler); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
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
