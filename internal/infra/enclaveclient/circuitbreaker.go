package enclaveclient

import (
	"sync"
	"time"
)

// breakerState 表示连接池当前健康状况。
type breakerState string

const (
	stateHealthy  breakerState = "healthy"
	stateDegraded breakerState = "degraded"
	stateDraining breakerState = "draining"
)

// circuitBreaker 用于异常分级与摘除/恢复策略。
type circuitBreaker struct {
	threshold int
	cooldown  time.Duration

	mu         sync.Mutex
	state      breakerState
	failures   int
	lastChange time.Time
}

func newCircuitBreaker(threshold int, cooldown time.Duration) *circuitBreaker {
	return &circuitBreaker{
		threshold:  threshold,
		cooldown:   cooldown,
		state:      stateHealthy,
		lastChange: time.Now(),
	}
}

func (cb *circuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state == stateDraining {
		return false
	}
	if cb.state == stateDegraded && time.Since(cb.lastChange) > cb.cooldown {
		cb.state = stateHealthy
		cb.failures = 0
		cb.lastChange = time.Now()
	}
	return true
}

func (cb *circuitBreaker) Success() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	if cb.state != stateHealthy {
		cb.state = stateHealthy
		cb.lastChange = time.Now()
	}
}

func (cb *circuitBreaker) Failure() (tripped bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	if cb.failures >= cb.threshold && cb.state == stateHealthy {
		cb.state = stateDegraded
		cb.lastChange = time.Now()
		return true
	}
	return false
}

func (cb *circuitBreaker) Drain() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = stateDraining
	cb.lastChange = time.Now()
}

func (cb *circuitBreaker) State() breakerState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

func (cb *circuitBreaker) Timestamp() time.Time {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.lastChange
}
