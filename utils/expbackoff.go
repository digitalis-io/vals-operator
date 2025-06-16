package utils

import (
	"time"
)

// ExponentialBackoff implements an exponential backoff strategy
type ExponentialBackoff struct {
	Initial     time.Duration
	Max         time.Duration
	Multiplier  float64
	MaxAttempts int

	currentBackoff time.Duration
	attemptCount   int
}

// NewExponentialBackoff creates a new exponential backoff instance
func NewExponentialBackoff(initial, max time.Duration, multiplier float64, maxAttempts int) *ExponentialBackoff {
	return &ExponentialBackoff{
		Initial:        initial,
		Max:            max,
		Multiplier:     multiplier,
		MaxAttempts:    maxAttempts,
		currentBackoff: initial,
		attemptCount:   0,
	}
}

// Reset resets the backoff to initial values
func (e *ExponentialBackoff) Reset() {
	e.currentBackoff = e.Initial
	e.attemptCount = 0
}

// NextBackoff returns the next backoff duration and increments the attempt count
func (e *ExponentialBackoff) NextBackoff() time.Duration {
	if e.attemptCount >= e.MaxAttempts && e.MaxAttempts > 0 {
		return 0 // No more attempts allowed
	}

	current := e.currentBackoff
	e.attemptCount++

	// Calculate next backoff
	nextBackoff := time.Duration(float64(e.currentBackoff) * e.Multiplier)
	if nextBackoff > e.Max {
		nextBackoff = e.Max
	}
	e.currentBackoff = nextBackoff

	return current
}

// AttemptCount returns the current attempt count
func (e *ExponentialBackoff) AttemptCount() int {
	return e.attemptCount
}

// ShouldAttempt returns true if more attempts are allowed
func (e *ExponentialBackoff) ShouldAttempt() bool {
	return e.MaxAttempts <= 0 || e.attemptCount < e.MaxAttempts
}

// Sleep sleeps for the next backoff duration
func (e *ExponentialBackoff) Sleep() {
	if duration := e.NextBackoff(); duration > 0 {
		time.Sleep(duration)
	}
}
