package a2a

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// AgentHealthChecker periodically pings registered agents to verify they are alive.
type AgentHealthChecker struct {
	registry *Registry
	logger   *slog.Logger
	client   *http.Client
	interval time.Duration
	stop     chan struct{}
	wg       sync.WaitGroup
}

func NewAgentHealthChecker(registry *Registry, logger *slog.Logger) *AgentHealthChecker {
	return &AgentHealthChecker{
		registry: registry,
		logger:   logger,
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
	for _, agent := range agents {
		if agent.URL == "" {
			continue
		}
		wg.Add(1)
		go func(agent AgentCard) {
			defer wg.Done()

			healthy := h.ping(agent.URL)
			newStatus := StatusActive
			if !healthy {
				newStatus = StatusUnhealthy
			}

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
