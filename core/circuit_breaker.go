package core

import (
	"sync"
	"time"
)

type cbState int

const (
	cbClosed   cbState = iota // normal operation
	cbOpen                    // rejecting requests
	cbHalfOpen                // testing recovery
)

const (
	cbFailureThreshold = 5
	cbRecoveryTimeout  = 30 * time.Second
)

type circuitBreaker struct {
	mu           sync.Mutex
	state        cbState
	failures     int
	lastFailure  time.Time
}

func (cb *circuitBreaker) allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case cbOpen:
		if time.Since(cb.lastFailure) >= cbRecoveryTimeout {
			cb.state = cbHalfOpen
			return true
		}
		return false
	default:
		return true
	}
}

func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.state = cbClosed
}

func (cb *circuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	cb.lastFailure = time.Now()
	if cb.failures >= cbFailureThreshold {
		cb.state = cbOpen
	}
}
