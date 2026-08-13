package serve

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestRetryServeStartupTransient_RetriesUnavailableUntilSuccess(t *testing.T) {
	t.Parallel()

	attempts := 0
	err := retryServeStartupTransient(
		context.Background(),
		serveStartupRetryPolicy{Timeout: time.Second, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond},
		func(context.Context) error {
			attempts++
			if attempts < 3 {
				return errors.Join(domain.ErrUnavailable, errors.New("FleetDB circuit open"))
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("retryServeStartupTransient() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestRetryServeStartupTransient_DoesNotRetryPermanentFailure(t *testing.T) {
	t.Parallel()

	permanent := errors.New("invalid credentials")
	attempts := 0
	err := retryServeStartupTransient(
		context.Background(),
		serveStartupRetryPolicy{Timeout: time.Second, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond},
		func(context.Context) error {
			attempts++
			return permanent
		},
	)
	if !errors.Is(err, permanent) {
		t.Fatalf("error = %v, want permanent failure", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestRetryServeStartupTransient_TimesOutWithLastUnavailableFailure(t *testing.T) {
	t.Parallel()

	transient := errors.Join(domain.ErrUnavailable, errors.New("FleetDB circuit open"))
	err := retryServeStartupTransient(
		context.Background(),
		serveStartupRetryPolicy{Timeout: 5 * time.Millisecond, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond},
		func(context.Context) error { return transient },
	)
	if !errors.Is(err, transient) {
		t.Fatalf("error = %v, want last transient failure", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestRetryServeStartupTransient_RespectsParentCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := retryServeStartupTransient(
		ctx,
		serveStartupRetryPolicy{Timeout: time.Second, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond},
		func(context.Context) error { return domain.ErrUnavailable },
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}
