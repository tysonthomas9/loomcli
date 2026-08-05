package service

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// plainBackend implements only IssueBackend — the pre-fix serve client shape.
type plainBackend struct {
	backend.IssueBackend
	claimedAs []string
}

func (b *plainBackend) ClaimIssue(_ context.Context, id string, _ time.Duration) error {
	b.claimedAs = append(b.claimedAs, "(configured actor) "+id)
	return nil
}

// actorBackend also implements the actor-scoped capability.
type actorBackend struct {
	plainBackend
	gotActor string
}

func (b *actorBackend) ClaimIssueAsActor(_ context.Context, id string, _ time.Duration, actor string) error {
	b.gotActor = actor
	return nil
}

// The selection rule is the whole fix: with an actor AND a capable backend,
// the claim must carry the worker identity. Falling back here is what made
// fleet-db see one claimant for every sibling.
func TestClaimAsActor_PrefersTheActorScopedCall(t *testing.T) {
	be := &actorBackend{}
	if err := claimAsActor(context.Background(), be, "T-1", "worker-7"); err != nil {
		t.Fatalf("claimAsActor: %v", err)
	}
	if be.gotActor != "worker-7" {
		t.Fatalf("actor = %q, want worker-7", be.gotActor)
	}
	if len(be.claimedAs) != 0 {
		t.Fatalf("plain ClaimIssue was used despite the capability: %v", be.claimedAs)
	}
}

// Legacy paths must keep working: no actor supplied (an old client, or the web
// UI acting as itself) uses the plain claim.
func TestClaimAsActor_NoActorUsesPlainClaim(t *testing.T) {
	be := &actorBackend{}
	if err := claimAsActor(context.Background(), be, "T-1", ""); err != nil {
		t.Fatalf("claimAsActor: %v", err)
	}
	if be.gotActor != "" {
		t.Fatalf("actor-scoped call used with no actor (actor=%q)", be.gotActor)
	}
	if len(be.claimedAs) != 1 {
		t.Fatalf("plain ClaimIssue not used: %v", be.claimedAs)
	}
}

// A backend that cannot scope a claim still works — it just cannot arbitrate,
// which is why the API backend gaining the capability is the other half of
// this change.
func TestClaimAsActor_IncapableBackendFallsBack(t *testing.T) {
	be := &plainBackend{}
	if err := claimAsActor(context.Background(), be, "T-1", "worker-7"); err != nil {
		t.Fatalf("claimAsActor: %v", err)
	}
	if len(be.claimedAs) != 1 {
		t.Fatalf("plain ClaimIssue not used: %v", be.claimedAs)
	}
}
