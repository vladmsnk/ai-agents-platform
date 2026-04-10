package balancer

import "sort"

// PriorityBased selects providers by their Priority field (lower = higher priority).
// Fallbacks are ordered by ascending priority.
type PriorityBased struct{}

func NewPriorityBased() *PriorityBased {
	return &PriorityBased{}
}

func (p *PriorityBased) Select(model string, candidates []ScoredProvider) (primary ScoredProvider, fallbacks []ScoredProvider, ok bool) {
	available := filterAvailable(candidates)
	if len(available) == 0 {
		return ScoredProvider{}, nil, false
	}

	// Sort by priority ascending (lower number = higher priority)
	sort.Slice(available, func(i, j int) bool {
		pi, pj := available[i].Priority, available[j].Priority
		if pi != pj {
			return pi < pj
		}
		return available[i].Weight > available[j].Weight
	})

	return available[0], available[1:], true
}
