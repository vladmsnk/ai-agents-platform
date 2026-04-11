package a2a

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/vymoiseenkov/ai-agents-platform/internal/telemetry"
)

// AgentHealthChecker periodically pings registered agents to verify they are alive.
type AgentHealthChecker struct {
	registry *Registry
	logger   *slog.Logger
	metrics  *telemetry.Metrics
	client   *http.Client
	interval time.Duration
	stop     chan struct{}
	wg       sync.WaitGroup
}

func NewAgentHealthChecker(registry *Registry, logger *slog.Logger, metrics *telemetry.Metrics) *AgentHealthChecker {
	return &AgentHealthChecker{
		registry: registry,
		logger:   logger,
		metrics:  metrics,
		client:   &http.Client{Timeout: 5 * time.Second},
		interval: 10 * time.Second,
		stop:     make(chan struct{}),
	}
}

func (h *AgentHealthChecker) Start() {
	h.wg.Add(1)
	go h.loop()
	h.logger.Info("agent health checker started", "interval", h.interval)
}

func (h *AgentHealthChecker) Stop() {
	close(h.stop)
	h.wg.Wait()
	h.logger.Info("agent health checker stopped")
}

func (h *AgentHealthChecker) loop() {
	defer h.wg.Done()
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			h.checkAll()
		}
	}
}

func (h *AgentHealthChecker) checkAll() {
	agents := h.registry.List()
	ctx := context.Background()

	var wg sync.WaitGroup
	var healthy, unhealthy int
	var mu sync.Mutex

	for _, agent := range agents {
		if agent.URL == "" {
			continue
		}
		wg.Add(1)
		go func(agent AgentCard) {
			defer wg.Done()

			start := time.Now()
			isHealthy := h.ping(agent.URL)
			latency := time.Since(start)

			newStatus := StatusActive
			if !isHealthy {
				newStatus = StatusUnhealthy
			}

			// Record health check metrics
			if h.metrics != nil {
				h.metrics.RecordAgentHealth(ctx, agent.ID, isHealthy, latency)
			}

			mu.Lock()
			if isHealthy {
				healthy++
			} else {
				unhealthy++
			}
			mu.Unlock()

			if agent.Status != newStatus {
				h.logger.Info("agent status changed",
					"agent", agent.ID,
					"old_status", agent.Status,
					"new_status", newStatus,
				)
				h.registry.SetStatus(ctx, agent.ID, newStatus)
			}
		}(agent)
	}
	wg.Wait()

	// Update agent count gauges
	if h.metrics != nil {
		h.metrics.SetAgentCounts(len(agents), healthy, unhealthy)
	}
}

func (h *AgentHealthChecker) ping(baseURL string) bool {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return false
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
