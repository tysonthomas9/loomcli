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

type eventHistoryMockBackend struct {
	*MockIssueBackend
	result *backend.EventHistoryData
	params backend.EventHistoryParams
}

var _ backend.EventHistoryBackend = (*eventHistoryMockBackend)(nil)

func (m *eventHistoryMockBackend) ListEventHistory(
	_ context.Context,
	_ string,
	params backend.EventHistoryParams,
) (*backend.EventHistoryData, error) {
	m.params = params
	return m.result, nil
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

func TestTracedIssueBackendPreservesEventHistoryCapability(t *testing.T) {
	since := "cursor-200"
	inner := &eventHistoryMockBackend{
		MockIssueBackend: NewMockIssueBackend(),
		result: &backend.EventHistoryData{
			Events:      []backend.EventData{{ID: "event-201"}},
			Cursor:      "cursor-201",
			HasMore:     true,
			TotalEvents: 295,
		},
	}

	historyBackend, ok := wrapIssueBackendWithTracing(inner).(backend.EventHistoryBackend)
	if !ok {
		t.Fatal("traced backend should preserve EventHistoryBackend")
	}
	result, err := historyBackend.ListEventHistory(context.Background(), "TASK-1", backend.EventHistoryParams{
		Limit: 200,
		Since: &since,
	})
	if err != nil {
		t.Fatalf("ListEventHistory: %v", err)
	}
	if result.Cursor != "cursor-201" || result.TotalEvents != 295 {
		t.Errorf("result = %+v, want forwarded event-history result", result)
	}
	if inner.params.Since == nil || *inner.params.Since != since || inner.params.Limit != 200 {
		t.Errorf("params = %+v, want since %q and limit 200", inner.params, since)
	}
}

func TestTracedIssueBackend_EventHistoryUnsupportedByInner(t *testing.T) {
	historyBackend, ok := wrapIssueBackendWithTracing(NewMockIssueBackend()).(backend.EventHistoryBackend)
	if !ok {
		t.Fatal("traced backend should preserve EventHistoryBackend capability checks")
	}
	if _, err := historyBackend.ListEventHistory(context.Background(), "TASK-1", backend.EventHistoryParams{}); !backend.IsKind(err, backend.KindNotImplemented) {
		t.Fatalf("error = %v, want KindNotImplemented", err)
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

// actorAccessMockBackend is a MockIssueBackend that also answers the doctor's
// actor-authorization probe.
type actorAccessMockBackend struct {
	*MockIssueBackend
	calls     int
	lastActor string
	probeErr  error
}

func (m *actorAccessMockBackend) CheckActorAccess(_ context.Context, actor string) error {
	m.calls++
	m.lastActor = actor
	return m.probeErr
}

func (m *actorAccessMockBackend) Workspace() string { return "PUPPET" }

// TestTracedIssueBackendForwardsCheckActorAccess pins the forwarding the
// doctor check depends on. The CLI always wraps its backend in tracing, so an
// optional interface that is not re-implemented here is invisible in
// production while still passing tests that hold the bare backend.
func TestTracedIssueBackendForwardsCheckActorAccess(t *testing.T) {
	probeErr := errors.New("workspace access denied")
	inner := &actorAccessMockBackend{MockIssueBackend: NewMockIssueBackend(), probeErr: probeErr}
	wrapped := wrapIssueBackendWithTracing(inner)

	checker, ok := wrapped.(interface {
		CheckActorAccess(context.Context, string) error
		Workspace() string
	})
	if !ok {
		t.Fatal("tracedIssueBackend should expose the actor access probe")
	}
	if err := checker.CheckActorAccess(context.Background(), "operator@local"); !errors.Is(err, probeErr) {
		t.Fatalf("CheckActorAccess = %v, want the inner backend's verdict", err)
	}
	if inner.calls != 1 || inner.lastActor != "operator@local" {
		t.Fatalf("inner probe = calls=%d actor=%q, want 1 / operator@local", inner.calls, inner.lastActor)
	}
	if checker.Workspace() != "PUPPET" {
		t.Errorf("Workspace() = %q, want the inner backend's workspace", checker.Workspace())
	}
}

// A backend with no probe must report unsupported, never "authorized": the
// wrapper satisfies the interface unconditionally, so returning nil would make
// every non-fleet backend look like a passing check.
func TestTracedIssueBackendCheckActorAccessUnsupported(t *testing.T) {
	wrapped := wrapIssueBackendWithTracing(NewMockIssueBackend())
	checker, ok := wrapped.(interface {
		CheckActorAccess(context.Context, string) error
	})
	if !ok {
		t.Fatal("tracedIssueBackend should expose the actor access probe")
	}
	err := checker.CheckActorAccess(context.Background(), "operator@local")
	if err == nil {
		t.Fatal("unsupported probe returned nil, which reads as authorized")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("err = %v, want a validation error", err)
	}
}
