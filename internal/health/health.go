package health

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type Status struct {
	Healthy   bool   `json:"healthy"`
	Latency   int64  `json:"latency_ms"`
	LastCheck string `json:"last_check"`
	Error     string `json:"error,omitempty"`
}

type Checker struct {
	mu       sync.RWMutex
	statuses map[string]Status // provider name -> status
	urls     map[string]string // provider name -> health URL
	logger   *slog.Logger
	client   *http.Client
	stop     chan struct{}
}

func NewChecker(logger *slog.Logger) *Checker {
	return &Checker{
		statuses: make(map[string]Status),
		urls:     make(map[string]string),
		logger:   logger,
		client:   &http.Client{Timeout: 5 * time.Second},
		stop:     make(chan struct{}),
	}
}

func (c *Checker) SetProviders(providers map[string]string) {
	c.mu.Lock()
	c.urls = providers
	// remove statuses for providers that no longer exist
	for name := range c.statuses {
		if _, ok := providers[name]; !ok {
			delete(c.statuses, name)
		}
	}
	c.mu.Unlock()
}

func (c *Checker) Get(name string) (Status, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.statuses[name]
	return s, ok
}

// IsHealthy returns whether a provider is healthy (implements balancer.HealthSource).
func (c *Checker) IsHealthy(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.statuses[name]
	if !ok {
		return true // unknown = assume healthy
	}
	return s.Healthy
}

// AvgLatency returns the last known latency for a provider (implements balancer.HealthSource).
func (c *Checker) AvgLatency(name string) time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.statuses[name]
	if !ok {
		return 0
	}
	return time.Duration(s.Latency) * time.Millisecond
}

func (c *Checker) All() map[string]Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make(map[string]Status, len(c.statuses))
	for k, v := range c.statuses {
		result[k] = v
	}
	return result
}

func (c *Checker) CheckOne(name, url string) Status {
	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try GET to the base URL — most providers respond to GET /
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Status{
			Healthy:   false,
			Latency:   time.Since(start).Milliseconds(),
			LastCheck: time.Now().Format(time.RFC3339),
			Error:     fmt.Sprintf("create request: %v", err),
		}
	}

	resp, err := c.client.Do(req)
	latency := time.Since(start).Milliseconds()
	now := time.Now().Format(time.RFC3339)

	if err != nil {
		return Status{
			Healthy:   false,
			Latency:   latency,
			LastCheck: now,
			Error:     err.Error(),
		}
	}
	resp.Body.Close()

	healthy := resp.StatusCode < 500
	s := Status{
		Healthy:   healthy,
		Latency:   latency,
		LastCheck: now,
	}
	if !healthy {
		s.Error = fmt.Sprintf("status %d", resp.StatusCode)
	}
	return s
}

func (c *Checker) Start() {
	// Initial check
	c.checkAll()

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.checkAll()
			case <-c.stop:
				return
			}
		}
	}()
}

func (c *Checker) Stop() {
	close(c.stop)
}

func (c *Checker) checkAll() {
	c.mu.RLock()
	urls := make(map[string]string, len(c.urls))
	for k, v := range c.urls {
		urls[k] = v
	}
	c.mu.RUnlock()

	var wg sync.WaitGroup
	results := make(map[string]Status, len(urls))
	var resultsMu sync.Mutex

	for name, url := range urls {
		wg.Add(1)
		go func(n, u string) {
			defer wg.Done()
			s := c.CheckOne(n, u)
			resultsMu.Lock()
			results[n] = s
			resultsMu.Unlock()
			if !s.Healthy {
				c.logger.Warn("health check failed", "provider", n, "error", s.Error)
			}
		}(name, url)
	}
	wg.Wait()

	c.mu.Lock()
	for name, status := range results {
		c.statuses[name] = status
	}
	c.mu.Unlock()
}
