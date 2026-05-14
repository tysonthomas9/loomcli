package cli

import (
	"context"
	"testing"
	"time"
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
