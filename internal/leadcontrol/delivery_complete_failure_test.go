package leadcontrol

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestAssignmentCompletionFailureIsVisibleAndNotRedelivered: the turn lands
// but the inbox completion fails. The assignment must still read delivered,
// the failure must be recorded, and once the claim lapses the drain must
// finish the message without starting the same turn again.
func TestAssignmentCompletionFailureIsVisibleAndNotRedelivered(t *testing.T) {
	ctx := context.Background()
	base := memstore.New()
	st := &failFirstCompleteStore{Store: base}
	createAssignedLeadSession(t, base, "complete-failure", nil)
	fake := installFakeCodexClient(t, CodexThreadStatus{Type: "idle"})
	setCodexRuntimeMetadata(t, base, "WS", "lead-session", "ws://codex.test", "thread-1")

	result, err := DeliverCurrentAssignment(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("DeliverCurrentAssignment() error = %v", err)
	}
	if result.State != DeliveryStateDelivered || fake.turns != 1 {
		t.Fatalf("delivery = %+v, turns %d; want delivered once", result, fake.turns)
	}
	session, err := base.AgentSessions().Get(ctx, "WS", "lead-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Metadata[MetadataDeliveryVersion] == "" {
		t.Fatalf("assignment not marked delivered after the turn landed: %#v", session.Metadata)
	}
	if got := session.Metadata[MetadataDeliveryError]; !strings.Contains(got, "inbox completion failed") {
		t.Fatalf("delivery error = %q, want the completion failure recorded", got)
	}

	// The lapsed claim puts the message back in the queue; the lead's drain
	// picks it up again.
	drained, err := DeliverPendingLeadMessages(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("DeliverPendingLeadMessages() error = %v", err)
	}
	if fake.turns != 1 {
		t.Fatalf("turns after lease expiry = %d, want no redelivery", fake.turns)
	}
	if drained.State != DeliveryStateDelivered {
		t.Fatalf("drain state = %q, want delivered", drained.State)
	}
	msg, err := base.AgentInboxMessages().Get(ctx, "WS", result.InboxMessageID)
	if err != nil {
		t.Fatalf("get inbox message: %v", err)
	}
	if msg.Status != domain.AgentInboxMessageDelivered {
		t.Fatalf("inbox status = %q, want delivered", msg.Status)
	}
}

// failFirstCompleteStore fails the first inbox completion and requeues the
// message, standing in for a claim whose lease then expires.
type failFirstCompleteStore struct {
	store.Store
	failed bool
}

func (s *failFirstCompleteStore) AgentInboxMessages() store.AgentInboxMessageStore {
	return failFirstCompleteInbox{AgentInboxMessageStore: s.Store.AgentInboxMessages(), parent: s}
}

type failFirstCompleteInbox struct {
	store.AgentInboxMessageStore
	parent *failFirstCompleteStore
}

func (f failFirstCompleteInbox) Complete(ctx context.Context, ws, id string, update store.AgentInboxMessageComplete) (*domain.AgentInboxMessage, error) {
	if f.parent.failed {
		return f.AgentInboxMessageStore.Complete(ctx, ws, id, update)
	}
	f.parent.failed = true
	if _, err := f.AgentInboxMessageStore.Complete(ctx, ws, id, store.AgentInboxMessageComplete{Outcome: "retry", ClaimedBy: update.ClaimedBy}); err != nil {
		return nil, err
	}
	return nil, errors.New("injected inbox completion failure")
}
