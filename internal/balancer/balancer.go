package balancer

import (
	"sync"
	"sync/atomic"

	"github.com/vymoiseenkov/ai-agents-platform/internal/config"
)

type Balancer struct {
	routes    map[string][]int
	counters  map[string]*atomic.Uint64
	providers []config.Provider
	mu        sync.RWMutex
}

func New(providers []config.Provider) *Balancer {
	b := &Balancer{
		routes:   make(map[string][]int),
		counters: make(map[string]*atomic.Uint64),
	}
	b.setProviders(providers)
	return b
}

func (b *Balancer) setProviders(providers []config.Provider) {
	b.providers = providers
	b.routes = make(map[string][]int)
	newCounters := make(map[string]*atomic.Uint64)

	for i, p := range b.providers {
		if !p.Enabled {
			continue
		}
		for _, model := range p.Models {
			for w := 0; w < p.Weight; w++ {
				b.routes[model] = append(b.routes[model], i)
			}
			if c, ok := b.counters[model]; ok {
				newCounters[model] = c
			} else {
				newCounters[model] = &atomic.Uint64{}
			}
		}
	}
	b.counters = newCounters
}

// UpdateProviders replaces all providers and rebuilds routes.
func (b *Balancer) UpdateProviders(providers []config.Provider) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setProviders(providers)
}

// Next returns the next provider for the given model using weighted round-robin.
func (b *Balancer) Next(model string) (primary config.Provider, fallbacks []config.Provider, ok bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	route, exists := b.routes[model]
	if !exists || len(route) == 0 {
		return config.Provider{}, nil, false
	}

	counter := b.counters[model]
	pos := counter.Add(1) - 1
	idx := int(pos % uint64(len(route)))
	primaryIdx := route[idx]

	seen := map[int]bool{primaryIdx: true}
	for _, provIdx := range route {
		if !seen[provIdx] {
			fallbacks = append(fallbacks, b.providers[provIdx])
			seen[provIdx] = true
		}
	}

	return b.providers[primaryIdx], fallbacks, true
}

func (b *Balancer) Models() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	models := make([]string, 0, len(b.routes))
	for m := range b.routes {
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
