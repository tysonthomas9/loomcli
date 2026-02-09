package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
)

func TestDaemonShouldTrip(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"daemon not running", ErrDaemonNotRunning, true},
		{"connection timeout", ErrConnectionTimeout, true},
		{"daemon unhealthy", ErrDaemonUnhealthy, true},
		{"pool exhausted", ErrPoolExhausted, true},
		{"pool closed", ErrPoolClosed, false},
		{"invalid socket path", ErrInvalidSocketPath, false},
		{"context canceled", context.Canceled, false},
		{"context deadline exceeded", context.DeadlineExceeded, false},
		{"unknown error", errors.New("something else"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DaemonShouldTrip(tt.err)
			if got != tt.want {
				t.Errorf("DaemonShouldTrip(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestDaemonShouldTrip_WrappedErrors(t *testing.T) {
	wrapped := errors.Join(errors.New("connection failed"), ErrDaemonNotRunning)
	if !DaemonShouldTrip(wrapped) {
		t.Error("expected wrapped ErrDaemonNotRunning to trip")
	}
}

func TestProtectedPool_FailsFast_WhenOpen(t *testing.T) {
	// Create a pool with an invalid socket path won't actually connect.
	// We just need the ProtectedPool structure.
	pool, err := NewConnectionPool("/tmp/nonexistent.sock", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	breaker := circuitbreaker.NewBreaker("daemon", circuitbreaker.Config{
		FailureThreshold: 1,
		OpenTimeout:      1 * time.Hour,
		ShouldTrip:       DaemonShouldTrip,
	})
	pp := NewProtectedPool(pool, breaker)

	// First call will fail (daemon not running) and trip the breaker
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = pp.Get(ctx)
	if err == nil {
		t.Fatal("expected error on first Get")
	}

	// Second call should fail fast with ErrCircuitOpen
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	_, err = pp.Get(ctx2)
	if !errors.Is(err, circuitbreaker.ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestProtectedPool_BreakerState(t *testing.T) {
	pool, err := NewConnectionPool("/tmp/nonexistent.sock", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	breaker := circuitbreaker.NewBreaker("daemon", circuitbreaker.Config{
		FailureThreshold: 1,
		OpenTimeout:      1 * time.Hour,
		ShouldTrip:       DaemonShouldTrip,
	})
	pp := NewProtectedPool(pool, breaker)

	if pp.BreakerState() != circuitbreaker.StateClosed {
		t.Fatal("expected initial state to be closed")
	}

	stats := pp.BreakerStats()
	if stats.State != circuitbreaker.StateClosed {
		t.Fatal("expected stats state to be closed")
	}
}

func TestProtectedPool_DelegatesStats(t *testing.T) {
	pool, err := NewConnectionPool("/tmp/nonexistent.sock", 3)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	breaker := circuitbreaker.NewBreaker("daemon", circuitbreaker.Config{})
	pp := NewProtectedPool(pool, breaker)

	stats := pp.Stats()
	if stats.Size != 3 {
		t.Fatalf("expected pool size 3, got %d", stats.Size)
	}
	if pp.Size() != 3 {
		t.Fatalf("expected Size() = 3, got %d", pp.Size())
	}
	if pp.SocketPath() != "/tmp/nonexistent.sock" {
		t.Fatalf("expected socket path /tmp/nonexistent.sock, got %s", pp.SocketPath())
	}
}

func TestErrCircuitOpen_NotRetryable(t *testing.T) {
	if IsRetryable(ErrCircuitOpen) {
		t.Error("ErrCircuitOpen should not be retryable")
	}
}

func TestIsRetryable_WithCircuitOpen(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"circuit open", ErrCircuitOpen, false},
		{"daemon not running", ErrDaemonNotRunning, true},
		{"connection timeout", ErrConnectionTimeout, true},
		{"daemon unhealthy", ErrDaemonUnhealthy, true},
		{"pool exhausted", ErrPoolExhausted, false},
		{"pool closed", ErrPoolClosed, false},
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRetryable(tt.err); got != tt.want {
				t.Errorf("IsRetryable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestProtectedPool_Put(t *testing.T) {
	socketPath := startMockDaemonServer(t)
	pool, err := NewConnectionPool(socketPath, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	breaker := circuitbreaker.NewBreaker("daemon", circuitbreaker.Config{
		ShouldTrip: DaemonShouldTrip,
	})
	pp := NewProtectedPool(pool, breaker)

	ctx := context.Background()
	client, err := pp.Get(ctx)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	pp.Put(client)

	stats := pp.Stats()
	if stats.Available != 1 {
		t.Errorf("stats.Available = %v after Put, want 1", stats.Available)
	}
}

func TestProtectedPool_Discard(t *testing.T) {
	socketPath := startMockDaemonServer(t)
	pool, err := NewConnectionPool(socketPath, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	breaker := circuitbreaker.NewBreaker("daemon", circuitbreaker.Config{
		ShouldTrip: DaemonShouldTrip,
	})
	pp := NewProtectedPool(pool, breaker)

	ctx := context.Background()
	client, err := pp.Get(ctx)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	pp.Discard(client)

	stats := pp.Stats()
	if stats.Active != 0 {
		t.Errorf("stats.Active = %v after Discard, want 0", stats.Active)
	}
	if stats.Created != 0 {
		t.Errorf("stats.Created = %v after Discard, want 0", stats.Created)
	}
}

func TestProtectedPool_Close(t *testing.T) {
	socketPath := startMockDaemonServer(t)
	pool, err := NewConnectionPool(socketPath, 5)
	if err != nil {
		t.Fatal(err)
	}

	breaker := circuitbreaker.NewBreaker("daemon", circuitbreaker.Config{
		ShouldTrip: DaemonShouldTrip,
	})
	pp := NewProtectedPool(pool, breaker)

	err = pp.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Get should fail after close
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = pp.Get(ctx)
	if err == nil {
		t.Error("expected error from Get after Close")
	}
}
