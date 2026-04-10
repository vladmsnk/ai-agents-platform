package balancer

import (
	"sync"
	"sync/atomic"
)

// WeightedRoundRobin selects providers using weighted round-robin, skipping
// providers that are circuit-open, rate-limited, or unhealthy.
type WeightedRoundRobin struct {
	mu       sync.RWMutex
	counters map[string]*atomic.Uint64
}

func NewWeightedRoundRobin() *WeightedRoundRobin {
	return &WeightedRoundRobin{
		counters: make(map[string]*atomic.Uint64),
	}
}

func (w *WeightedRoundRobin) Select(model string, candidates []ScoredProvider) (primary ScoredProvider, fallbacks []ScoredProvider, ok bool) {
	available := filterAvailable(candidates)
	if len(available) == 0 {
		return ScoredProvider{}, nil, false
	}

	// Build weighted route
	var route []int
	for i, sp := range available {
		weight := sp.Weight
		if weight <= 0 {
			weight = 1
		}
		for n := 0; n < weight; n++ {
			route = append(route, i)
		}
	}

	w.mu.Lock()
	counter, exists := w.counters[model]
	if !exists {
		counter = &atomic.Uint64{}
		w.counters[model] = counter
	}
	w.mu.Unlock()

	pos := counter.Add(1) - 1
	idx := int(pos % uint64(len(route)))
	primaryIdx := route[idx]

	// Build fallbacks from remaining unique providers
	seen := map[int]bool{primaryIdx: true}
	for _, i := range route {
		if !seen[i] {
			fallbacks = append(fallbacks, available[i])
			seen[i] = true
		}
	}

	return available[primaryIdx], fallbacks, true
}

func filterAvailable(candidates []ScoredProvider) []ScoredProvider {
	var available []ScoredProvider
	for _, sp := range candidates {
		if sp.CircuitOpen || sp.RateLimited || sp.HealthScore <= 0 {
			continue
		}
		available = append(available, sp)
	}
	return available
}
