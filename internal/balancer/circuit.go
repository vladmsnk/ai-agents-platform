package balancer

import (
	"sync"
	"time"
)

type circuitState int

const (
	stateClosed   circuitState = iota // normal operation
	stateOpen                         // failures exceeded threshold, skip provider
	stateHalfOpen                     // recovery period, allow one probe
)

const (
	CircuitClosed   = "closed"
	CircuitOpen     = "open"
	CircuitHalfOpen = "half-open"
)

// CircuitBreaker tracks per-provider failure state.
type CircuitBreaker struct {
	mu               sync.Mutex
	states           map[string]*providerCircuit
	failureThreshold int
	recoveryTimeout  time.Duration
}

type providerCircuit struct {
	state            circuitState
	consecutiveFails int
	lastFailure      time.Time
	lastAttempt      time.Time
}

func NewCircuitBreaker(failureThreshold int, recoveryTimeout time.Duration) *CircuitBreaker {
	if failureThreshold <= 0 {
		failureThreshold = 5
	}
	if recoveryTimeout <= 0 {
		recoveryTimeout = 30 * time.Second
	}
	return &CircuitBreaker{
		states:           make(map[string]*providerCircuit),
		failureThreshold: failureThreshold,
		recoveryTimeout:  recoveryTimeout,
	}
}

// IsOpen returns true if the circuit is open (provider should be skipped).
// Uses a full mutex to safely handle state transitions.
func (cb *CircuitBreaker) IsOpen(name string) bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	pc, ok := cb.states[name]
	if !ok {
		return false
	}

	switch pc.state {
	case stateClosed:
		return false
	case stateOpen:
		if time.Since(pc.lastFailure) > cb.recoveryTimeout {
			pc.state = stateHalfOpen
			return false // allow one probe
		}
		return true
	case stateHalfOpen:
		if time.Since(pc.lastAttempt) < 2*time.Second {
			return true
		}
		pc.lastAttempt = time.Now()
		return false
	}
	return false
}

// RecordSuccess resets the circuit to closed.
func (cb *CircuitBreaker) RecordSuccess(name string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	pc, ok := cb.states[name]
	if !ok {
		return
	}
	pc.state = stateClosed
	pc.consecutiveFails = 0
}

// RecordFailure increments the failure counter and may open the circuit.
func (cb *CircuitBreaker) RecordFailure(name string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	pc, ok := cb.states[name]
	if !ok {
		pc = &providerCircuit{}
		cb.states[name] = pc
	}

	pc.consecutiveFails++
	pc.lastFailure = time.Now()

	if pc.consecutiveFails >= cb.failureThreshold {
		pc.state = stateOpen
	}
}

// Reset removes circuit state for a provider.
func (cb *CircuitBreaker) Reset(name string) {
	cb.mu.Lock()
	delete(cb.states, name)
	cb.mu.Unlock()
}

// State returns the current state for a provider (for observability).
func (cb *CircuitBreaker) State(name string) string {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	pc, ok := cb.states[name]
	if !ok {
		return CircuitClosed
	}

	switch pc.state {
	case stateClosed:
		return CircuitClosed
	case stateOpen:
		if time.Since(pc.lastFailure) > cb.recoveryTimeout {
			pc.state = stateHalfOpen
			return CircuitHalfOpen
		}
		return CircuitOpen
	case stateHalfOpen:
		return CircuitHalfOpen
	}
	return CircuitClosed
}
