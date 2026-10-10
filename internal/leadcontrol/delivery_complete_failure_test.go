package leadcontrol

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestAssignmentCompletionFailureIsVisibleAndNotRedelivered: the turn lands
// but the inbox completion fails, so the message stays claimed. The
// assignment must still read delivered with the failure exposed; once the
// real lease lapses the drain reclaims the message and finishes it without
// starting the same turn again.
func TestAssignmentCompletionFailureIsVisibleAndNotRedelivered(t *testing.T) {
	ctx := context.Background()
	origTTL := leadInboxLeaseTTL
	leadInboxLeaseTTL = 50 * time.Millisecond
	t.Cleanup(func() { leadInboxLeaseTTL = origTTL })

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
	if !strings.Contains(result.DeliveryError, "inbox completion failed") {
		t.Fatalf("result delivery error = %q, want the completion failure", result.DeliveryError)
	}
	session, err := base.AgentSessions().Get(ctx, "WS", "lead-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Metadata[MetadataDeliveryVersion] == "" {
		t.Fatalf("assignment not marked delivered after the turn landed: %#v", session.Metadata)
	}
	if got := session.Metadata[MetadataDeliveryError]; !strings.Contains(got, "inbox completion failed") {
		t.Fatalf("session delivery error = %q, want the completion failure recorded", got)
	}

	// While the lease is live nothing can reclaim the message.
	if _, err := base.AgentInboxMessages().ClaimNext(ctx, store.AgentInboxMessageClaim{
		WorkspaceKey: "WS", TargetAgentID: "nova", SessionID: "lead-session", ClaimedBy: "probe", LeaseTTL: time.Minute,
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("claim during live lease err = %v, want not found", err)
	}
	time.Sleep(3 * leadInboxLeaseTTL)

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
	if msg.Status != domain.AgentInboxMessageDelivered || msg.Attempt != 2 {
		t.Fatalf("inbox status = %q attempt %d, want delivered on the reclaim (attempt 2)", msg.Status, msg.Attempt)
	}
}

// failFirstCompleteStore fails the first inbox completion and leaves the
// claim in place, so only lease expiry can free the message.
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
	return nil, errors.New("injected inbox completion failure")
}
