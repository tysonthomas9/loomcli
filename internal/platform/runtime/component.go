// Package runtime provides capability-independent lifecycle ownership for
// background components hosted by Loom processes.
package runtime //nolint:revive // The approved target architecture names this platform mechanism runtime.

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"time"
)

// ComponentID is the stable, audit-friendly identity of one managed runtime
// component.
type ComponentID string

var componentIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Component performs one bounded reconciliation pass. Implementations must not
// start their own long-lived goroutines: Host owns repetition and lifecycle.
// The supplied time is the host clock's value at the start of the pass.
type Component interface {
	ID() ComponentID
	RunOnce(context.Context, time.Time) error
}

// Backoff configures host-level delay after consecutive failed passes. It is
// distinct from any capability-owned retry schedule persisted in product data.
// The zero value disables host-level failure backoff.
type Backoff struct {
	Initial    time.Duration
	Max        time.Duration
	Multiplier float64
}

// Policy defines the lifecycle mechanics owned by Host. Jitter is a maximum
// additional delay, never an early run. A zero Timeout bounds a pass only by
// host cancellation. A zero FailureBackoff preserves cadence after failures.
type Policy struct {
	Cadence        time.Duration
	Immediate      bool
	Jitter         time.Duration
	Timeout        time.Duration
	FailureBackoff Backoff
}

// Registration joins one component to its host-owned lifecycle policy.
type Registration struct {
	Component Component
	Policy    Policy
}

func validateRegistration(registration Registration) error {
	if isNilInterface(registration.Component) {
		return fmt.Errorf("runtime component is required")
	}
	id := registration.Component.ID()
	if !componentIDPattern.MatchString(string(id)) {
		return fmt.Errorf("runtime component id %q must be lowercase kebab-case", id)
	}
	if err := validatePolicy(registration.Policy); err != nil {
		return fmt.Errorf("runtime component %q: %w", id, err)
	}
	return nil
}

func validatePolicy(policy Policy) error {
	if policy.Cadence <= 0 {
		return fmt.Errorf("cadence must be positive")
	}
	if policy.Jitter < 0 {
		return fmt.Errorf("jitter must not be negative")
	}
	if policy.Jitter >= policy.Cadence {
		return fmt.Errorf("jitter must be less than cadence")
	}
	if policy.Timeout < 0 {
		return fmt.Errorf("timeout must not be negative")
	}
	return validateBackoff(policy.FailureBackoff)
}

func validateBackoff(backoff Backoff) error {
	if backoff == (Backoff{}) {
		return nil
	}
	if backoff.Initial <= 0 {
		return fmt.Errorf("failure backoff initial delay must be positive")
	}
	if backoff.Max < backoff.Initial {
		return fmt.Errorf("failure backoff maximum must not be less than initial delay")
	}
	if math.IsNaN(backoff.Multiplier) || math.IsInf(backoff.Multiplier, 0) || backoff.Multiplier < 1 {
		return fmt.Errorf("failure backoff multiplier must be finite and at least one")
	}
	return nil
}

func (backoff Backoff) delay(consecutiveFailures uint64) time.Duration {
	if backoff == (Backoff{}) || consecutiveFailures == 0 {
		return 0
	}
	scaled := float64(backoff.Initial) * math.Pow(backoff.Multiplier, float64(consecutiveFailures-1))
	if math.IsInf(scaled, 0) || scaled >= float64(backoff.Max) {
		return backoff.Max
	}
	return time.Duration(scaled)
}
