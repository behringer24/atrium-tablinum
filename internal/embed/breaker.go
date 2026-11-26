package embed

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrUnavailable is returned without contacting the service while the circuit breaker is open: the
// service failed several requests in a row, so further requests would only queue up behind it and
// time out one by one. Callers fall back to what works without a vector (full-text search, saving
// with the sections pending).
var ErrUnavailable = errors.New("the embedding service is temporarily unavailable")

const (
	// breakerFailures is the number of consecutive failed requests that opens the breaker.
	breakerFailures = 3
	// breakerCooldown is how long the breaker stays open before one request is let through to test
	// the service.
	breakerCooldown = 30 * time.Second
)

// CircuitStatus is the state of the circuit breaker, for admin_health.
type CircuitStatus struct {
	Open      bool
	RetryIn   time.Duration // until the next test request, while open
	LastError string
}

// StatusReporter is implemented by embedders that have a circuit breaker.
type StatusReporter interface {
	Circuit() CircuitStatus
}

// breaker opens after breakerFailures failed requests in a row and lets one test request through
// after breakerCooldown; its result closes or re-opens it. Only failures that say the service is
// overloaded or down count: not a caller that gave up, a wrong model, bad credentials or bad input.
type breaker struct {
	mu        sync.Mutex
	fails     int
	openUntil time.Time
	probing   bool
	lastErr   string
	now       func() time.Time
}

func newBreaker() *breaker { return &breaker{now: time.Now} }

func (b *breaker) openLocked() bool { return b.fails >= breakerFailures }

// peek reports without side effects whether requests are currently refused.
func (b *breaker) peek() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openLocked() && (b.probing || b.now().Before(b.openUntil)) {
		return b.refusal()
	}
	return nil
}

// allow decides whether a request may go to the service; after the cooldown exactly one is let
// through as a test.
func (b *breaker) allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.openLocked() {
		return nil
	}
	if b.probing || b.now().Before(b.openUntil) {
		return b.refusal()
	}
	b.probing = true
	return nil
}

func (b *breaker) refusal() error {
	wait := max(b.openUntil.Sub(b.now()), 0).Round(time.Second)
	return fmt.Errorf("%w (paused after repeated failures, next attempt in %s)", ErrUnavailable, wait)
}

// done records the result of a request that allow let through.
func (b *breaker) done(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
	switch {
	case err == nil:
		b.fails, b.lastErr = 0, ""
	case countsAsOutage(err):
		b.fails++
		b.lastErr = err.Error()
		if b.openLocked() {
			b.openUntil = b.now().Add(breakerCooldown)
		}
	}
}

func (b *breaker) status() CircuitStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.openLocked() {
		return CircuitStatus{}
	}
	return CircuitStatus{Open: true, RetryIn: max(b.openUntil.Sub(b.now()), 0), LastError: b.lastErr}
}

// StatusError is the answer of a service with a status other than 200.
type StatusError struct{ Code int }

func (e *StatusError) Error() string {
	switch e.Code {
	case 401, 403:
		return fmt.Sprintf("embedder rejects authentication (status %d), check credentials", e.Code)
	case 429:
		return "embedder throttles requests (status 429)"
	}
	return fmt.Sprintf("embedder responded with status %d", e.Code)
}

func countsAsOutage(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, ErrModelMismatch) {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code == 429 || se.Code >= 500
	}
	return true
}
