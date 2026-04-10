package balancer

import (
	"sync"
	"time"

	"github.com/vymoiseenkov/ai-agents-platform/internal/config"
	"github.com/vymoiseenkov/ai-agents-platform/internal/ratelimit"
)

// ScoredProvider wraps a provider with runtime scoring data.
type ScoredProvider struct {
	config.Provider
	AvgLatency  time.Duration
	HealthScore float64 // 0.0 = unhealthy, 1.0 = healthy
	CircuitOpen bool
	RateLimited bool
}

// Strategy selects a primary provider and fallbacks for a model.
type Strategy interface {
	Select(model string, candidates []ScoredProvider) (primary ScoredProvider, fallbacks []ScoredProvider, ok bool)
}

// HealthSource provides health and latency data for providers.
type HealthSource interface {
	IsHealthy(name string) bool
	AvgLatency(name string) time.Duration
}

type Balancer struct {
	mu        sync.RWMutex
	providers []config.Provider
	modelMap  map[string][]int // model -> provider indices

	strategy Strategy
	circuit  *CircuitBreaker
	limiter  *ratelimit.Limiter
	health   HealthSource
}

func New(providers []config.Provider, strategy Strategy, circuit *CircuitBreaker, limiter *ratelimit.Limiter, health HealthSource) *Balancer {
	b := &Balancer{
		modelMap: make(map[string][]int),
		strategy: strategy,
		circuit:  circuit,
		limiter:  limiter,
		health:   health,
	}
	b.setProviders(providers)
	return b
}

func (b *Balancer) setProviders(providers []config.Provider) {
	b.providers = providers
	b.modelMap = make(map[string][]int)

	for i, p := range b.providers {
		if !p.Enabled {
			continue
		}
		for _, model := range p.Models {
			b.modelMap[model] = append(b.modelMap[model], i)
		}
	}
}

// UpdateProviders replaces all providers and rebuilds routes.
func (b *Balancer) UpdateProviders(providers []config.Provider) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setProviders(providers)
}

// Next selects the next provider for the given model using the configured strategy.
func (b *Balancer) Next(model string) (primary config.Provider, fallbacks []config.Provider, ok bool) {
	b.mu.RLock()
	indices, exists := b.modelMap[model]
	if !exists || len(indices) == 0 {
		b.mu.RUnlock()
		return config.Provider{}, nil, false
	}

	// Build scored candidates
	candidates := make([]ScoredProvider, 0, len(indices))
	for _, idx := range indices {
		p := b.providers[idx]
		sp := ScoredProvider{
			Provider:    p,
			HealthScore: 1.0,
		}

		if b.health != nil {
			if !b.health.IsHealthy(p.Name) {
				sp.HealthScore = 0
			}
			sp.AvgLatency = b.health.AvgLatency(p.Name)
		}
		if b.circuit != nil {
			sp.CircuitOpen = b.circuit.IsOpen(p.Name)
		}
		if b.limiter != nil {
			sp.RateLimited = !b.limiter.CanAllow(p.Name)
		}

		candidates = append(candidates, sp)
	}
	b.mu.RUnlock()

	prim, fbs, found := b.strategy.Select(model, candidates)
	if !found {
		return config.Provider{}, nil, false
	}

	fbProviders := make([]config.Provider, len(fbs))
	for i, fb := range fbs {
		fbProviders[i] = fb.Provider
	}

	return prim.Provider, fbProviders, true
}

// RecordSuccess notifies the circuit breaker of a successful request.
func (b *Balancer) RecordSuccess(provider string) {
	if b.circuit != nil {
		b.circuit.RecordSuccess(provider)
	}
}

// RecordFailure notifies the circuit breaker of a failed request.
func (b *Balancer) RecordFailure(provider string) {
	if b.circuit != nil {
		b.circuit.RecordFailure(provider)
	}
}

func (b *Balancer) Models() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	models := make([]string, 0, len(b.modelMap))
	for m := range b.modelMap {
		models = append(models, m)
	}
	return models
}

// Providers returns a copy of the current provider list.
func (b *Balancer) Providers() []config.Provider {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]config.Provider, len(b.providers))
	copy(result, b.providers)
	return result
}

// CircuitState returns the circuit breaker state for a provider.
func (b *Balancer) CircuitState(name string) string {
	if b.circuit == nil {
		return "closed"
	}
	return b.circuit.State(name)
}
