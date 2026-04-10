package ratelimit

import (
	"sync"

	"golang.org/x/time/rate"
)

// Limiter manages per-provider rate limiters based on RPM (requests per minute).
type Limiter struct {
	mu       sync.RWMutex
	limiters map[string]*rate.Limiter
}

func New() *Limiter {
	return &Limiter{
		limiters: make(map[string]*rate.Limiter),
	}
}

// SetProvider creates or updates the rate limiter for a provider.
// rpm=0 means no rate limit.
func (l *Limiter) SetProvider(name string, rpm int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if rpm <= 0 {
		delete(l.limiters, name)
		return
	}
	l.upsert(name, rpm)
}

// Allow consumes one token and returns true if the provider has capacity.
// Use this when actually dispatching a request.
func (l *Limiter) Allow(name string) bool {
	l.mu.RLock()
	lim, ok := l.limiters[name]
	l.mu.RUnlock()

	if !ok {
		return true
	}
	return lim.Allow()
}

// CanAllow returns true if the provider likely has capacity, without consuming a token.
// Use this for scoring/ranking candidates before selection.
func (l *Limiter) CanAllow(name string) bool {
	l.mu.RLock()
	lim, ok := l.limiters[name]
	l.mu.RUnlock()

	if !ok {
		return true
	}
	return lim.Tokens() >= 1
}

// RemoveProvider removes the rate limiter for a provider.
func (l *Limiter) RemoveProvider(name string) {
	l.mu.Lock()
	delete(l.limiters, name)
	l.mu.Unlock()
}

// UpdateAll replaces all rate limiters based on provider configs.
func (l *Limiter) UpdateAll(providers map[string]int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for name := range l.limiters {
		if _, ok := providers[name]; !ok {
			delete(l.limiters, name)
		}
	}

	for name, rpm := range providers {
		if rpm <= 0 {
			delete(l.limiters, name)
			continue
		}
		l.upsert(name, rpm)
	}
}

func (l *Limiter) upsert(name string, rpm int) {
	rps := float64(rpm) / 60.0
	burst := max(rpm/10, 1)
	if existing, ok := l.limiters[name]; ok {
		existing.SetLimit(rate.Limit(rps))
		existing.SetBurst(burst)
	} else {
		l.limiters[name] = rate.NewLimiter(rate.Limit(rps), burst)
	}
}
