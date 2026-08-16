package serve

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type serveStartupRetryPolicy struct {
	Timeout        time.Duration
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

func defaultServeStartupRetryPolicy() serveStartupRetryPolicy {
	return serveStartupRetryPolicy{
		Timeout:        45 * time.Second,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     2 * time.Second,
	}
}

// retryServeStartupTransient gives a required dependency a bounded recovery
// window during process startup. It retries only the shared unavailable
// sentinel; configuration, authorization, and contract failures remain
// fail-fast.
func retryServeStartupTransient(
	ctx context.Context,
	policy serveStartupRetryPolicy,
	op func(context.Context) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if op == nil {
		return errors.New("serve startup retry operation is required")
	}
	if policy.Timeout <= 0 || policy.InitialBackoff <= 0 || policy.MaxBackoff <= 0 {
		return errors.New("serve startup retry policy durations must be positive")
	}
	if policy.MaxBackoff < policy.InitialBackoff {
		policy.MaxBackoff = policy.InitialBackoff
	}

	retryCtx, cancel := context.WithTimeout(ctx, policy.Timeout)
	defer cancel()

	backoff := policy.InitialBackoff
	var lastErr error
	for {
		if err := retryCtx.Err(); err != nil {
			if lastErr == nil {
				return err
			}
			return fmt.Errorf("startup dependency did not recover within %s: %w", policy.Timeout, errors.Join(lastErr, err))
		}

		err := op(retryCtx)
		if err == nil {
			return nil
		}
		if !errors.Is(err, persistence.ErrUnavailable) {
			return err
		}
		lastErr = err

		timer := time.NewTimer(backoff)
		select {
		case <-retryCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return fmt.Errorf("startup dependency did not recover within %s: %w", policy.Timeout, errors.Join(lastErr, retryCtx.Err()))
		case <-timer.C:
		}
		backoff = min(backoff*2, policy.MaxBackoff)
	}
}
