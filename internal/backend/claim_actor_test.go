package backend

import (
	"context"
	"strings"
	"testing"
	"time"
)

// plainClaimBackend implements only IssueBackend — the shape of a backend that
// cannot express an actor, and the shape most test fakes have.
type plainClaimBackend struct {
	IssueBackend
	plainCalls int
}

func (b *plainClaimBackend) ClaimIssue(_ context.Context, _ string, _ time.Duration) error {
	b.plainCalls++
	return nil
}

// capableClaimBackend also implements ActorClaimer.
type capableClaimBackend struct {
	plainClaimBackend
	gotActor string
	gotTTL   time.Duration
}

func (b *capableClaimBackend) ClaimIssueAsActor(_ context.Context, _ string, ttl time.Duration, actor string) error {
	b.gotActor = actor
	b.gotTTL = ttl
	return nil
}

// With an actor and a capable backend the claim must carry the worker identity:
// fleet-db arbitrates locks BY actor, so losing it is what made every sibling
// look like one claimant.
func TestClaimAs_CapableBackendCarriesTheActor(t *testing.T) {
	be := &capableClaimBackend{}
	if err := ClaimAs(context.Background(), be, "T-1", 30*time.Second, "worker-2"); err != nil {
		t.Fatalf("ClaimAs: %v", err)
	}
	if be.gotActor != "worker-2" {
		t.Errorf("actor = %q, want worker-2", be.gotActor)
	}
	if be.gotTTL != 30*time.Second {
		t.Errorf("lockTTL = %v, want 30s", be.gotTTL)
	}
	if be.plainCalls != 0 {
		t.Errorf("plain ClaimIssue used despite the capability (%d calls)", be.plainCalls)
	}
}

// The weakness #348 left: an actor supplied to a backend that cannot scope a
// claim used to fall through to the plain claim, taking the lock under the
// client's own identity. That must now refuse.
func TestClaimAs_IncapableBackendWithActorRefuses(t *testing.T) {
	be := &plainClaimBackend{}
	err := ClaimAs(context.Background(), be, "T-1", 0, "worker-2")
	if err == nil {
		t.Fatal("ClaimAs succeeded on a backend that cannot scope the claim; it must refuse rather than claim as someone else")
	}
	if !IsKind(err, KindNotImplemented) {
		t.Errorf("kind = %v, want %v", err, KindNotImplemented)
	}
	if be.plainCalls != 0 {
		t.Errorf("plain ClaimIssue was still called (%d times) — the lock was taken under the wrong identity", be.plainCalls)
	}
	// The message must name the backend, otherwise the operator cannot tell
	// which component lost the capability.
	if !strings.Contains(err.Error(), "plainClaimBackend") {
		t.Errorf("error %q does not name the concrete backend type", err.Error())
	}
	if !strings.Contains(err.Error(), "worker-2") {
		t.Errorf("error %q does not name the actor that was dropped", err.Error())
	}
}

// No actor is the legitimate case: nothing is configured, so there is no
// identity to lose. It must keep working on backends without the capability —
// this is also what keeps existing test fakes valid.
func TestClaimAs_NoActorUsesPlainClaimOnIncapableBackend(t *testing.T) {
	be := &plainClaimBackend{}
	if err := ClaimAs(context.Background(), be, "T-1", 0, ""); err != nil {
		t.Fatalf("ClaimAs with no actor: %v", err)
	}
	if be.plainCalls != 1 {
		t.Errorf("plain ClaimIssue calls = %d, want 1", be.plainCalls)
	}
}

// A blank actor is no actor: trimming happens once, here, so a stray header
// value of "  " cannot select the actor-scoped path with an empty identity.
func TestClaimAs_BlankActorIsNoActor(t *testing.T) {
	be := &capableClaimBackend{}
	if err := ClaimAs(context.Background(), be, "T-1", 0, "   "); err != nil {
		t.Fatalf("ClaimAs with blank actor: %v", err)
	}
	if be.gotActor != "" {
		t.Errorf("actor-scoped claim used with a blank actor (actor=%q)", be.gotActor)
	}
	if be.plainCalls != 1 {
		t.Errorf("plain ClaimIssue calls = %d, want 1", be.plainCalls)
	}
}

// A capable backend with no actor still takes the plain claim: the caller is
// deliberately acting as itself (the web UI, an operator CLI).
func TestClaimAs_NoActorUsesPlainClaimOnCapableBackend(t *testing.T) {
	be := &capableClaimBackend{}
	if err := ClaimAs(context.Background(), be, "T-1", 0, ""); err != nil {
		t.Fatalf("ClaimAs: %v", err)
	}
	if be.gotActor != "" {
		t.Errorf("actor-scoped claim used with no actor (actor=%q)", be.gotActor)
	}
	if be.plainCalls != 1 {
		t.Errorf("plain ClaimIssue calls = %d, want 1", be.plainCalls)
	}
}

// The actor is trimmed before it reaches the backend, so " worker-2 " and
// "worker-2" are the same claimant to fleet-db's per-actor arbitration.
func TestClaimAs_TrimsTheActor(t *testing.T) {
	be := &capableClaimBackend{}
	if err := ClaimAs(context.Background(), be, "T-1", 0, "  worker-2\t"); err != nil {
		t.Fatalf("ClaimAs: %v", err)
	}
	if be.gotActor != "worker-2" {
		t.Errorf("actor = %q, want the trimmed worker-2", be.gotActor)
	}
}

func TestClaimAs_NilBackendIsValidationError(t *testing.T) {
	if err := ClaimAs(context.Background(), nil, "T-1", 0, "worker-2"); !IsKind(err, KindValidation) {
		t.Errorf("err = %v, want a validation error", err)
	}
}
