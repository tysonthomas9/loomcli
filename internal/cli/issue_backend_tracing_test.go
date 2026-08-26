package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type actorClaimMockBackend struct {
	*MockIssueBackend
	claimedID string
	actor     string
	ttl       time.Duration
}

func (m *actorClaimMockBackend) ClaimIssueAsActor(_ context.Context, id string, ttl time.Duration, actor string) error {
	m.claimedID = id
	m.actor = actor
	m.ttl = ttl
	return nil
}

func TestTracedIssueBackendPreservesClaimIssueAsActor(t *testing.T) {
	inner := &actorClaimMockBackend{MockIssueBackend: NewMockIssueBackend()}
	wrapped := wrapIssueBackendWithTracing(inner)
	actorBackend, ok := wrapped.(interface {
		ClaimIssueAsActor(context.Context, string, time.Duration, string) error
	})
	if !ok {
		t.Fatal("traced issue backend should preserve ClaimIssueAsActor")
	}

	if err := actorBackend.ClaimIssueAsActor(context.Background(), "TASK-1", time.Minute, "planner"); err != nil {
		t.Fatalf("ClaimIssueAsActor: %v", err)
	}
	if inner.claimedID != "TASK-1" || inner.actor != "planner" || inner.ttl != time.Minute {
		t.Fatalf("claim = id %q actor %q ttl %s", inner.claimedID, inner.actor, inner.ttl)
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

type dependencyLineageMockBackend struct {
	*MockIssueBackend
	ids    []string
	called string
}

func (m *dependencyLineageMockBackend) DependencyTaskIDs(_ context.Context, id string) ([]string, error) {
	m.called = id
	return append([]string(nil), m.ids...), nil
}

func TestTracedIssueBackendPreservesDependencyLineage(t *testing.T) {
	inner := &dependencyLineageMockBackend{MockIssueBackend: NewMockIssueBackend(), ids: []string{"TASK-A"}}
	wrapped := wrapIssueBackendWithTracing(inner)

	lineage, ok := wrapped.(backend.DependencyLineageBackend)
	if !ok {
		t.Fatal("traced issue backend should preserve DependencyLineageBackend")
	}
	got, err := lineage.DependencyTaskIDs(context.Background(), "TASK-B")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "TASK-A" || inner.called != "TASK-B" {
		t.Fatalf("DependencyTaskIDs = %v called=%q, want [TASK-A] called TASK-B", got, inner.called)
	}
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
