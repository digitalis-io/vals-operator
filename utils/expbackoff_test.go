package utils

import (
	"testing"
	"time"
)

func TestExponentialBackoffBasicFlow(t *testing.T) {
	backoff := NewExponentialBackoff(
		100*time.Millisecond,
		1*time.Second,
		2.0,
		5,
	)

	// Test initial values
	if backoff.Initial != 100*time.Millisecond {
		t.Errorf("Expected initial 100ms, got %v", backoff.Initial)
	}
	if backoff.Max != 1*time.Second {
		t.Errorf("Expected max 1s, got %v", backoff.Max)
	}
	if backoff.Multiplier != 2.0 {
		t.Errorf("Expected multiplier 2.0, got %f", backoff.Multiplier)
	}
	if backoff.MaxAttempts != 5 {
		t.Errorf("Expected max attempts 5, got %d", backoff.MaxAttempts)
	}

	// Test progression
	expected := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1 * time.Second, // capped at max
	}

	for i, exp := range expected {
		if !backoff.ShouldAttempt() {
			t.Errorf("Should be able to attempt at iteration %d", i)
		}

		actual := backoff.NextBackoff()
		if actual != exp {
			t.Errorf("Iteration %d: expected %v, got %v", i, exp, actual)
		}

		if backoff.AttemptCount() != i+1 {
			t.Errorf("Iteration %d: expected attempt count %d, got %d", i, i+1, backoff.AttemptCount())
		}
	}

	// After max attempts
	if backoff.ShouldAttempt() {
		t.Error("Should not attempt after max attempts")
	}
	if d := backoff.NextBackoff(); d != 0 {
		t.Errorf("Expected 0 duration after max attempts, got %v", d)
	}
}

func TestExponentialBackoffReset(t *testing.T) {
	backoff := NewExponentialBackoff(50*time.Millisecond, 500*time.Millisecond, 2.0, 3)

	// Use up some attempts
	backoff.NextBackoff()
	backoff.NextBackoff()

	if backoff.AttemptCount() != 2 {
		t.Errorf("Expected 2 attempts, got %d", backoff.AttemptCount())
	}

	// Reset
	backoff.Reset()

	// Should be back to initial state
	if backoff.AttemptCount() != 0 {
		t.Errorf("Expected 0 attempts after reset, got %d", backoff.AttemptCount())
	}
	if !backoff.ShouldAttempt() {
		t.Error("Should be able to attempt after reset")
	}
	if d := backoff.NextBackoff(); d != 50*time.Millisecond {
		t.Errorf("Expected initial backoff after reset, got %v", d)
	}
}

func TestExponentialBackoffInfiniteAttempts(t *testing.T) {
	// MaxAttempts = 0 means infinite attempts
	backoff := NewExponentialBackoff(10*time.Millisecond, 100*time.Millisecond, 2.0, 0)

	// Should always allow attempts
	for i := 0; i < 100; i++ {
		if !backoff.ShouldAttempt() {
			t.Errorf("Should always attempt with MaxAttempts=0, failed at %d", i)
		}
		backoff.NextBackoff()
	}

	// Should still be true
	if !backoff.ShouldAttempt() {
		t.Error("Should always attempt with MaxAttempts=0")
	}
}

func TestExponentialBackoffSleep(t *testing.T) {
	backoff := NewExponentialBackoff(10*time.Millisecond, 50*time.Millisecond, 2.0, 3)

	// Test that Sleep actually sleeps
	start := time.Now()
	backoff.Sleep()
	elapsed := time.Since(start)

	// Should have slept for at least 10ms (allowing some tolerance)
	if elapsed < 9*time.Millisecond {
		t.Errorf("Expected to sleep for at least 10ms, but only slept for %v", elapsed)
	}

	// Use up all attempts
	backoff.NextBackoff() // 2nd attempt
	backoff.NextBackoff() // 3rd attempt

	// Sleep after max attempts should not sleep
	start = time.Now()
	backoff.Sleep()
	elapsed = time.Since(start)

	if elapsed > 1*time.Millisecond {
		t.Errorf("Should not sleep after max attempts, but slept for %v", elapsed)
	}
}

func TestExponentialBackoffMaxCapping(t *testing.T) {
	backoff := NewExponentialBackoff(100*time.Millisecond, 300*time.Millisecond, 10.0, 10)

	// First attempt: 100ms
	if d := backoff.NextBackoff(); d != 100*time.Millisecond {
		t.Errorf("Expected 100ms, got %v", d)
	}

	// Second attempt: would be 1000ms but should cap at 300ms
	if d := backoff.NextBackoff(); d != 300*time.Millisecond {
		t.Errorf("Expected 300ms (capped), got %v", d)
	}

	// All subsequent attempts should also be capped
	for i := 0; i < 5; i++ {
		if d := backoff.NextBackoff(); d != 300*time.Millisecond {
			t.Errorf("Attempt %d: Expected 300ms (capped), got %v", i+3, d)
		}
	}
}
