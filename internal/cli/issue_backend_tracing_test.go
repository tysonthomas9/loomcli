package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestTracedIssueBackendPreservesClaimIssueParams(t *testing.T) {
	inner := NewMockIssueBackend()
	wrapped := wrapIssueBackendWithTracing(inner)
	params := backend.ClaimIssueParams{ID: "TASK-1", LockTTL: time.Minute, OwnerActor: "planner"}

	if err := wrapped.ClaimIssue(context.Background(), params); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
	if len(inner.Calls) == 0 || inner.Calls[len(inner.Calls)-1].Method != "ClaimIssue" {
		t.Fatalf("calls = %#v, want final ClaimIssue", inner.Calls)
	}
	claimCall := inner.Calls[len(inner.Calls)-1]
	got, ok := claimCall.Args[0].(backend.ClaimIssueParams)
	if !ok {
		t.Fatalf("ClaimIssue arg = %T, want backend.ClaimIssueParams", claimCall.Args[0])
	}
	if got != params {
		t.Fatalf("ClaimIssue params = %#v, want %#v", got, params)
	}
}

// claimReleaserMockBackend is a MockIssueBackend that also implements
// backend.ClaimReleaser so we can assert tracedIssueBackend forwards the
// call when the inner backend supports it.
type claimReleaserMockBackend struct {
	*MockIssueBackend
	calls     int
	lastID    string
	lastActor string
	releaseE  error
}

var _ backend.ClaimReleaser = (*claimReleaserMockBackend)(nil)

func (m *claimReleaserMockBackend) ReleaseClaim(_ context.Context, id, actor string) error {
	m.calls++
	m.lastID = id
	m.lastActor = actor
	return m.releaseE
}

// TestTracedIssueBackendPreservesReleaseClaim asserts that the tracing
// decorator implements backend.ClaimReleaser and delegates to the inner
// backend when the inner supports it. Without this, the LOOM-1 fix is
// silently a no-op for the CLI's default backend (which is always wrapped
// in tracing — see internal/cli/deps.go).
func TestTracedIssueBackendPreservesReleaseClaim(t *testing.T) {
	inner := &claimReleaserMockBackend{MockIssueBackend: NewMockIssueBackend()}
	wrapped := wrapIssueBackendWithTracing(inner)

	r, ok := wrapped.(backend.ClaimReleaser)
	if !ok {
		t.Fatal("tracedIssueBackend should implement ClaimReleaser when inner does")
	}
	if err := r.ReleaseClaim(context.Background(), "TASK-1", "planner"); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}
	if inner.calls != 1 || inner.lastID != "TASK-1" {
		t.Fatalf("inner.ReleaseClaim = calls=%d id=%q, want 1 / TASK-1", inner.calls, inner.lastID)
	}
	if inner.lastActor != "planner" {
		t.Fatalf("inner.ReleaseClaim actor = %q, want planner", inner.lastActor)
	}
}

// TestTracedIssueBackendReleaseClaim_NoopWhenInnerLacksCapability asserts the
// capability-detection branch: if the inner backend does NOT implement
// ClaimReleaser, the wrapper still implements it (we always advertise the
// method) and returns nil. This matches the contract documented on the
// interface — non-fleet backends silently no-op rather than panicking.
func TestTracedIssueBackendReleaseClaim_NoopWhenInnerLacksCapability(t *testing.T) {
	inner := NewMockIssueBackend() // does NOT implement ClaimReleaser
	wrapped := wrapIssueBackendWithTracing(inner)

	r, ok := wrapped.(backend.ClaimReleaser)
	if !ok {
		t.Fatal("tracedIssueBackend should still expose ClaimReleaser; capability check happens inside")
	}
	if err := r.ReleaseClaim(context.Background(), "TASK-1", "planner"); err != nil {
		t.Errorf("expected no-op nil when inner lacks capability, got %v", err)
	}
}

// TestTracedIssueBackendReleaseClaim_PropagatesError asserts the wrapper
// forwards inner errors unchanged so callers (e.g. loom complete) can log
// them. The span records the error per the trace contract, but the error
// shape returned to the caller must not be transformed.
func TestTracedIssueBackendReleaseClaim_PropagatesError(t *testing.T) {
	want := errors.New("simulated release failure")
	inner := &claimReleaserMockBackend{MockIssueBackend: NewMockIssueBackend(), releaseE: want}
	wrapped := wrapIssueBackendWithTracing(inner)

	r := wrapped.(backend.ClaimReleaser)
	got := r.ReleaseClaim(context.Background(), "TASK-1", "planner")
	if !errors.Is(got, want) {
		t.Errorf("ReleaseClaim error = %v, want %v", got, want)
	}
}
