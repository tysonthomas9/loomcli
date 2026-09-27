//go:build daemon_bugreplay

package leadcontrol

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Bug-replay fault tests, group "claims" (claim/ownership/lease, bucket C).
// Catalogue row #736; no model invariant (bucket C). The local invariant is
// "a lease-holder completes a claimed inbox message under the claim it
// holds": the Complete request echoes the claimed_by the claim recorded.
// fleet-db #322 made completion claimer-only, so a Complete without it is
// refused (403) and the handled message is redelivered.
//
// v5 @ 1c6dabfc8: internal/leadcontrol/delivery.go:393,414 send no claimant
// and store.AgentInboxMessageComplete (control_plane_store.go:359-364) has no
// claimed_by field. The check goes through the JSON wire form so the file
// compiles both before and after the field exists.
// Expected: FAIL on v5, PASS on the #736 head (9a8e8e155).

const claimsHeldClaim = "codex:lead-session:thread-42"

type claimsRecordingStore struct {
	store.Store
	inbox *claimsRecordingInbox
}

func (s *claimsRecordingStore) AgentInboxMessages() store.AgentInboxMessageStore { return s.inbox }

type claimsRecordingInbox struct {
	store.AgentInboxMessageStore
	completes []store.AgentInboxMessageComplete
}

func (s *claimsRecordingInbox) Complete(
	_ context.Context, ws, id string, update store.AgentInboxMessageComplete,
) (*domain.AgentInboxMessage, error) {
	s.completes = append(s.completes, update)
	return &domain.AgentInboxMessage{WorkspaceKey: ws, InboxMessageID: id}, nil
}

func claimsWireClaimedBy(t *testing.T, update store.AgentInboxMessageComplete) string {
	t.Helper()
	raw, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal complete update: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal complete update: %v", err)
	}
	got, _ := body["claimed_by"].(string)
	return got
}

func TestBugReplay_PR736_InboxCompleteCarriesHeldClaim(t *testing.T) {
	tests := []struct {
		name     string
		complete func(context.Context, store.Store, *domain.AgentInboxMessage) error
	}{
		{
			name: "delivered",
			complete: func(ctx context.Context, st store.Store, msg *domain.AgentInboxMessage) error {
				_, err := completeLeadInboxDelivered(ctx, st, "WS", "lead-session",
					&materializingTurnDeliverer{}, msg, &DeliveryResult{State: DeliveryStateDelivered})
				return err
			},
		},
		{
			name: "retry",
			complete: func(ctx context.Context, st store.Store, msg *domain.AgentInboxMessage) error {
				_, err := completeLeadInboxRetry(ctx, st, "WS", "lead-session",
					&materializingTurnDeliverer{}, msg, &DeliveryResult{})
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inbox := &claimsRecordingInbox{}
			st := &claimsRecordingStore{inbox: inbox}
			msg := &domain.AgentInboxMessage{
				WorkspaceKey:   "WS",
				InboxMessageID: "msg-1",
				ClaimedBy:      claimsHeldClaim,
			}
			if err := tt.complete(t.Context(), st, msg); err != nil {
				t.Fatalf("complete: %v", err)
			}
			if len(inbox.completes) != 1 {
				t.Fatalf("Complete calls = %d, want 1", len(inbox.completes))
			}
			if got := claimsWireClaimedBy(t, inbox.completes[0]); got != claimsHeldClaim {
				t.Fatalf("Complete claimed_by = %q, want %q: completion omits the claim we hold, so claimer-only fleet-db refuses it",
					got, claimsHeldClaim)
			}
		})
	}
}
