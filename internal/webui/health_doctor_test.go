package webui

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

func TestHealthDoctor_BackoffDuration(t *testing.T) {
	hd := NewHealthDoctor(nil, nil, slog.Default(), DefaultHealthDoctorConfig())

	tests := []struct {
		count int
		want  time.Duration
	}{
		{0, 30 * time.Second},
		{1, 60 * time.Second},
		{2, 120 * time.Second},
		{3, 240 * time.Second},
		{4, 480 * time.Second},
		{10, 10 * time.Minute}, // capped
	}
	for _, tt := range tests {
		got := hd.backoffDuration(tt.count)
		if got != tt.want {
			t.Errorf("backoffDuration(%d) = %v, want %v", tt.count, got, tt.want)
		}
	}
}

func TestHealthDoctor_ClosedBreakerNoAction(t *testing.T) {
	// A healthy breaker should not trigger recovery.
	mp := daemon.NewMultiPool(func(ctx context.Context) string { return "ws1" }, 2)
	pool := newTestProtectedPool(t)
	if err := mp.Register("ws1", pool); err != nil {
		t.Fatal(err)
	}

	recoveryCalled := false
	hd := NewHealthDoctor(mp, func() (map[string]string, error) {
		recoveryCalled = true
		return map[string]string{"ws1": "/tmp/test"}, nil
	}, slog.Default(), HealthDoctorConfig{
		CheckInterval:  10 * time.Millisecond,
		StuckThreshold: 50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	hd.Run(ctx)

	if recoveryCalled {
		t.Error("recovery should not be called for healthy breaker")
	}
}

func TestHealthDoctor_DetectsStuckBreaker(t *testing.T) {
	mp := daemon.NewMultiPool(func(ctx context.Context) string { return "ws1" }, 2)

	// Create a breaker that will immediately trip.
	breaker := circuitbreaker.NewBreaker("test", circuitbreaker.Config{
		FailureThreshold:  1,
		OpenTimeout:       1 * time.Hour, // stay open
		HalfOpenMaxProbes: 1,
	})
	// Trip the breaker.
	_ = breaker.Execute(func() error { return daemon.ErrDaemonNotRunning })

	if breaker.GetState() != circuitbreaker.StateOpen {
		t.Fatalf("breaker should be open, got %v", breaker.GetState())
	}

	socketPath := t.TempDir() + "/test.sock"
	rawPool, err := daemon.NewConnectionPool(socketPath, 2)
	if err != nil {
		t.Fatal(err)
	}
	pool := daemon.NewProtectedPool(rawPool, breaker)
	if err := mp.Register("ws1", pool); err != nil {
		t.Fatal(err)
	}

	// The doctor should detect the stuck breaker and attempt recovery.
	hd := NewHealthDoctor(mp, func() (map[string]string, error) {
		return map[string]string{"ws1": "/nonexistent"}, nil
	}, slog.Default(), HealthDoctorConfig{
		CheckInterval:  10 * time.Millisecond,
		StuckThreshold: 30 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	hd.Run(ctx)

	// Verify the watch was created and tracked the unhealthy state.
	hd.mu.Lock()
	w, ok := hd.watches["ws1"]
	hd.mu.Unlock()

	if !ok {
		t.Fatal("expected watch entry for ws1")
	}
	if w.restartCount == 0 {
		t.Error("expected at least one recovery attempt")
	}
}

func TestHealthDoctor_ClearsOnRecovery(t *testing.T) {
	mp := daemon.NewMultiPool(func(ctx context.Context) string { return "ws1" }, 2)
	pool := newTestProtectedPool(t)
	if err := mp.Register("ws1", pool); err != nil {
		t.Fatal(err)
	}

	hd := NewHealthDoctor(mp, func() (map[string]string, error) {
		return map[string]string{"ws1": "/tmp"}, nil
	}, slog.Default(), DefaultHealthDoctorConfig())

	// Simulate an unhealthy→healthy transition.
	hd.mu.Lock()
	hd.watches["ws1"] = &breakerWatch{
		unhealthySince: time.Now().Add(-5 * time.Minute),
		restartCount:   3,
	}
	hd.mu.Unlock()

	// Run one check cycle — breaker is closed, so watch should clear.
	hd.checkAllWorkspaces(context.Background())

	hd.mu.Lock()
	w := hd.watches["ws1"]
	hd.mu.Unlock()

	if !w.unhealthySince.IsZero() {
		t.Error("unhealthySince should be cleared after recovery")
	}
	if w.restartCount != 0 {
		t.Error("restartCount should be reset after recovery")
	}
}

// newTestProtectedPool creates a ProtectedPool with a healthy (closed) breaker for testing.
func newTestProtectedPool(t *testing.T) *daemon.ProtectedPool {
	t.Helper()
	socketPath := t.TempDir() + "/test.sock"
	rawPool, err := daemon.NewConnectionPool(socketPath, 2)
	if err != nil {
		t.Fatal(err)
	}
	breaker := circuitbreaker.NewBreaker("test", circuitbreaker.Config{
		FailureThreshold: 5,
		OpenTimeout:      30 * time.Second,
	})
	return daemon.NewProtectedPool(rawPool, breaker)
}
