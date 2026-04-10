package balancer

import "sort"

// LatencyBased selects the provider with the lowest average latency,
// using weight as a tiebreaker. Skips unavailable providers.
type LatencyBased struct{}

func NewLatencyBased() *LatencyBased {
	return &LatencyBased{}
}

func (l *LatencyBased) Select(model string, candidates []ScoredProvider) (primary ScoredProvider, fallbacks []ScoredProvider, ok bool) {
	available := filterAvailable(candidates)
	if len(available) == 0 {
		return ScoredProvider{}, nil, false
	}

	// Sort by latency (ascending), then by weight (descending) as tiebreaker
	sort.Slice(available, func(i, j int) bool {
		li, lj := available[i].AvgLatency, available[j].AvgLatency
		if li != lj {
			return li < lj
		}
		return available[i].Weight > available[j].Weight
	})

	return available[0], available[1:], true
}
