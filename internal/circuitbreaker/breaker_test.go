package circuitbreaker

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errTest = errors.New("test error")

func TestBreaker_ClosedState_PassesThrough(t *testing.T) {
	b := NewBreaker("test", Config{FailureThreshold: 5})

	called := false
	err := b.Execute(func() error {
		called = true
		return nil
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !called {
		t.Fatal("expected function to be called")
	}
	if b.GetState() != StateClosed {
		t.Fatalf("expected closed state, got %v", b.GetState())
	}
}

func TestBreaker_TripsAfterThreshold(t *testing.T) {
	b := NewBreaker("test", Config{
		FailureThreshold: 3,
		OpenTimeout:      10 * time.Second,
	})

	// First 2 failures should not trip
	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errTest })
		if b.GetState() != StateClosed {
			t.Fatalf("breaker tripped too early at failure %d", i+1)
		}
	}

	// 3rd failure should trip
	_ = b.Execute(func() error { return errTest })
	if b.GetState() != StateOpen {
		t.Fatalf("expected open state after %d failures, got %v", 3, b.GetState())
	}
}

func TestBreaker_OpenState_FailsFast(t *testing.T) {
	b := NewBreaker("test", Config{
		FailureThreshold: 1,
		OpenTimeout:      1 * time.Hour, // won't expire during test
	})

	// Trip the breaker
	_ = b.Execute(func() error { return errTest })

	// Subsequent calls should fail fast
	called := false
	err := b.Execute(func() error {
		called = true
		return nil
	})

	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
	if called {
		t.Fatal("function should not have been called when circuit is open")
	}
}

func TestBreaker_TransitionsToHalfOpen(t *testing.T) {
	b := NewBreaker("test", Config{
		FailureThreshold: 1,
		OpenTimeout:      50 * time.Millisecond,
	})

	// Trip the breaker
	_ = b.Execute(func() error { return errTest })
	if b.GetState() != StateOpen {
		t.Fatalf("expected open, got %v", b.GetState())
	}

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// State should now be half-open
	if b.GetState() != StateHalfOpen {
		t.Fatalf("expected half-open after timeout, got %v", b.GetState())
	}
}

func TestBreaker_HalfOpen_ProbeSuccess_ClosesCircuit(t *testing.T) {
	b := NewBreaker("test", Config{
		FailureThreshold:  1,
		OpenTimeout:       50 * time.Millisecond,
		HalfOpenMaxProbes: 1,
	})

	// Trip the breaker
	_ = b.Execute(func() error { return errTest })

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Probe succeeds
	err := b.Execute(func() error { return nil })
	if err != nil {
		t.Fatalf("expected probe to succeed, got %v", err)
	}
	if b.GetState() != StateClosed {
		t.Fatalf("expected closed after successful probe, got %v", b.GetState())
	}
}

func TestBreaker_HalfOpen_ProbeFailure_ReopensCircuit(t *testing.T) {
	b := NewBreaker("test", Config{
		FailureThreshold:  1,
		OpenTimeout:       50 * time.Millisecond,
		HalfOpenMaxProbes: 1,
	})

	// Trip the breaker
	_ = b.Execute(func() error { return errTest })

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Probe fails
	_ = b.Execute(func() error { return errTest })
	if b.GetState() != StateOpen {
		t.Fatalf("expected open after failed probe, got %v", b.GetState())
	}
}

func TestBreaker_HalfOpen_ProbeFailure_AllowsNewProbeAfterTimeout(t *testing.T) {
	b := NewBreaker("test", Config{
		FailureThreshold:  1,
		OpenTimeout:       50 * time.Millisecond,
		HalfOpenMaxProbes: 1,
	})

	// Trip the breaker
	_ = b.Execute(func() error { return errTest })

	// Wait for timeout -> half-open
	time.Sleep(60 * time.Millisecond)

	// First probe fails -> re-open
	_ = b.Execute(func() error { return errTest })
	if b.GetState() != StateOpen {
		t.Fatalf("expected open after failed probe, got %v", b.GetState())
	}

	// Wait for timeout again -> half-open again
	time.Sleep(60 * time.Millisecond)

	// Second probe should be allowed (halfOpenProbes was reset)
	err := b.Execute(func() error { return nil })
	if err != nil {
		t.Fatalf("expected second probe to be allowed, got %v", err)
	}
	if b.GetState() != StateClosed {
		t.Fatalf("expected closed after successful second probe, got %v", b.GetState())
	}
}

func TestBreaker_HalfOpen_ExcessProbes_Rejected(t *testing.T) {
	b := NewBreaker("test", Config{
		FailureThreshold:  1,
		OpenTimeout:       50 * time.Millisecond,
		HalfOpenMaxProbes: 1,
	})

	// Trip the breaker
	_ = b.Execute(func() error { return errTest })

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// First request in half-open is allowed (the probe)
	// We need to hold it open while sending the second request.
	// Use a channel to coordinate.
	ready := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		err := b.Execute(func() error {
			ready <- struct{}{} // signal we're inside
			time.Sleep(100 * time.Millisecond)
			return nil
		})
		done <- err
	}()

	<-ready // wait for first probe to be inside

	// Second request should be rejected
	err := b.Execute(func() error { return nil })
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen for excess probe, got %v", err)
	}

	// Wait for first probe to complete
	if err := <-done; err != nil {
		t.Fatalf("first probe should have succeeded, got %v", err)
	}
}

func TestBreaker_ShouldTrip_Classification(t *testing.T) {
	appErr := errors.New("not found")
	connErr := errors.New("connection refused")

	b := NewBreaker("test", Config{
		FailureThreshold: 2,
		ShouldTrip: func(err error) bool {
			return errors.Is(err, connErr)
		},
	})

	// Application errors should not trip the breaker
	for i := 0; i < 10; i++ {
		_ = b.Execute(func() error { return appErr })
	}
	if b.GetState() != StateClosed {
		t.Fatal("application errors should not trip the breaker")
	}

	// Connection errors should trip
	_ = b.Execute(func() error { return connErr })
	_ = b.Execute(func() error { return connErr })
	if b.GetState() != StateOpen {
		t.Fatal("connection errors should trip the breaker")
	}
}

func TestBreaker_SuccessResetsConsecutiveFailures(t *testing.T) {
	b := NewBreaker("test", Config{FailureThreshold: 3})

	// 2 failures
	_ = b.Execute(func() error { return errTest })
	_ = b.Execute(func() error { return errTest })

	// 1 success resets consecutive count
	_ = b.Execute(func() error { return nil })

	// 2 more failures should not trip (reset happened)
	_ = b.Execute(func() error { return errTest })
	_ = b.Execute(func() error { return errTest })

	if b.GetState() != StateClosed {
		t.Fatal("breaker should still be closed after reset by success")
	}
}

func TestBreaker_ConcurrentAccess(t *testing.T) {
	b := NewBreaker("test", Config{
		FailureThreshold: 100,
		OpenTimeout:      1 * time.Second,
	})

	var wg sync.WaitGroup
	var errCount atomic.Int64

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := b.Execute(func() error {
				return errTest
			})
			if err != nil {
				errCount.Add(1)
			}
		}()
	}

	wg.Wait()

	stats := b.Stats()
	if stats.Failures != 100 {
		t.Fatalf("expected 100 failures, got %d", stats.Failures)
	}
}

func TestBreaker_Reset(t *testing.T) {
	b := NewBreaker("test", Config{
		FailureThreshold: 1,
		OpenTimeout:      1 * time.Hour,
	})

	// Trip the breaker
	_ = b.Execute(func() error { return errTest })
	if b.GetState() != StateOpen {
		t.Fatal("expected open state")
	}

	// Reset
	b.Reset()
	if b.GetState() != StateClosed {
		t.Fatal("expected closed state after reset")
	}

	// Should work again
	err := b.Execute(func() error { return nil })
	if err != nil {
		t.Fatalf("expected no error after reset, got %v", err)
	}
}

func TestBreaker_OnStateChange(t *testing.T) {
	var transitions []struct{ from, to State }
	var mu sync.Mutex

	b := NewBreaker("test", Config{
		FailureThreshold: 1,
		OpenTimeout:      50 * time.Millisecond,
		OnStateChange: func(from, to State) {
			mu.Lock()
			transitions = append(transitions, struct{ from, to State }{from, to})
			mu.Unlock()
		},
	})

	// Trip: closed -> open
	_ = b.Execute(func() error { return errTest })
	time.Sleep(10 * time.Millisecond) // let callback fire

	// Wait for half-open
	time.Sleep(60 * time.Millisecond)

	// Probe success: half-open -> closed
	_ = b.Execute(func() error { return nil })
	time.Sleep(10 * time.Millisecond) // let callback fire

	mu.Lock()
	defer mu.Unlock()

	if len(transitions) < 2 {
		t.Fatalf("expected at least 2 transitions, got %d", len(transitions))
	}
	if transitions[0].from != StateClosed || transitions[0].to != StateOpen {
		t.Fatalf("first transition: expected closed->open, got %v->%v", transitions[0].from, transitions[0].to)
	}
	// The second transition involves half-open -> closed
	// There may be an open -> half-open transition too
	last := transitions[len(transitions)-1]
	if last.to != StateClosed {
		t.Fatalf("last transition should end at closed, got %v", last.to)
	}
}

func TestExecuteWithResult(t *testing.T) {
	b := NewBreaker("test", Config{FailureThreshold: 5})

	result, err := ExecuteWithResult(b, func() (int, error) {
		return 42, nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result != 42 {
		t.Fatalf("expected 42, got %d", result)
	}
}

func TestExecuteWithResult_CircuitOpen(t *testing.T) {
	b := NewBreaker("test", Config{
		FailureThreshold: 1,
		OpenTimeout:      1 * time.Hour,
	})

	// Trip the breaker
	_, _ = ExecuteWithResult(b, func() (int, error) {
		return 0, errTest
	})

	// Should fail fast
	result, err := ExecuteWithResult(b, func() (int, error) {
		return 42, nil
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
	if result != 0 {
		t.Fatalf("expected zero value, got %d", result)
	}
}

func TestBreaker_StateString(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half-open"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestBreaker_DefaultConfig(t *testing.T) {
	b := NewBreaker("test", Config{})

	// Verify defaults were applied
	if b.config.FailureThreshold != 5 {
		t.Fatalf("expected default threshold 5, got %d", b.config.FailureThreshold)
	}
	if b.config.OpenTimeout != 30*time.Second {
		t.Fatalf("expected default open timeout 30s, got %v", b.config.OpenTimeout)
	}
	if b.config.HalfOpenMaxProbes != 1 {
		t.Fatalf("expected default max probes 1, got %d", b.config.HalfOpenMaxProbes)
	}
}

func TestBreaker_Stats(t *testing.T) {
	b := NewBreaker("test", Config{FailureThreshold: 5})

	_ = b.Execute(func() error { return nil })
	_ = b.Execute(func() error { return errTest })
	_ = b.Execute(func() error { return nil })

	stats := b.Stats()
	if stats.State != StateClosed {
		t.Fatalf("expected closed, got %v", stats.State)
	}
	if stats.Failures != 1 {
		t.Fatalf("expected 1 failure, got %d", stats.Failures)
	}
	if stats.Successes != 2 {
		t.Fatalf("expected 2 successes, got %d", stats.Successes)
	}
	if stats.ConsecutiveFail != 0 {
		t.Fatalf("expected 0 consecutive failures (reset by success), got %d", stats.ConsecutiveFail)
	}
	if stats.LastFailure.IsZero() {
		t.Fatal("expected last failure time to be set")
	}
}
