package domain

import (
	"errors"
	"fmt"
	"time"
)

// Operational constants for stack publish admission. Mirrored from fleet-db's
// StackPublishLeaseService contract (STACKED-PRS-78). These bound forge HTTP/git
// calls and takeover grace — they are not Loom agent session caps.
const (
	DefaultStackPublishLeaseTTL   = 120 * time.Second
	MinStackPublishLeaseTTL       = 90 * time.Second // FleetDB floor: max call (60s) + skew (30s)
	MaxStackPublishLeaseTTL       = 600 * time.Second
	MaxStackPublishCallBound      = 60 * time.Second
	StackPublishClockSkew         = 30 * time.Second
	DefaultStackPublishLeaseGrace = MaxStackPublishCallBound + StackPublishClockSkew // 90s
	MinStackPublishLeaseGrace     = MaxStackPublishCallBound + StackPublishClockSkew // 90s
	MaxStackPublishLeaseGrace     = 300 * time.Second
)

var (
	// ErrStackPublishLeaseBusy reports that another holder owns the live lease
	// or the key is still inside reuse_after grace.
	ErrStackPublishLeaseBusy = errors.New("domain: stack publish lease is busy")
	// ErrStackPublishLeaseTokenMismatch reports a renew/release against a
	// successor's generation.
	ErrStackPublishLeaseTokenMismatch = errors.New("domain: stack publish lease token mismatch")
	// ErrStackPublishLeaseStoreUnavailable means the lease API/store cannot be
	// reached. Mutating publish must fail closed (no degrade-unlocked path).
	ErrStackPublishLeaseStoreUnavailable = errors.New("domain: stack publish lease store unavailable")
	// ErrStackPublishLeaseLost is returned when renew fails or the verified
	// lease validity is exhausted mid-session. Outcome is uncertain: stop new
	// forge calls and never report success.
	ErrStackPublishLeaseLost = errors.New("domain: stack publish lease lost; outcome uncertain")
)

// StackPublishLease is fleet-db's client-facing admission grant.
//
// Guarantee boundary: FleetDB fences one live lease generation per
// (workspace, stack_id). The token does not fence GitHub — an already-sent
// request or an old client can still race after cancellation.
type StackPublishLease struct {
	Token      string
	StackID    string
	Holder     string
	Generation int64
	ExpiresAt  time.Time
	ReuseAfter time.Time
}

// StackPublishLeaseBusyError carries holder metadata for a typed retryable busy.
type StackPublishLeaseBusyError struct {
	Message    string
	Holder     string
	Generation int64
	ExpiresAt  time.Time
	ReuseAfter time.Time
}

func (e *StackPublishLeaseBusyError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("stack publish lease held by %s generation %d until %s (reuse_after %s)",
		e.Holder, e.Generation, e.ExpiresAt.Format(time.RFC3339Nano), e.ReuseAfter.Format(time.RFC3339Nano))
}

func (e *StackPublishLeaseBusyError) Unwrap() error { return ErrStackPublishLeaseBusy }

// RetryAt is the earliest time a well-behaved successor may begin forge
// mutations (reuse_after).
func (e *StackPublishLeaseBusyError) RetryAt() time.Time { return e.ReuseAfter }
