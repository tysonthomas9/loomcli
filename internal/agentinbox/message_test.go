package agentinbox

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestEnqueueCreatesGenericAgentInboxMessage(t *testing.T) {
	st := memstore.New()
	inbox := testInboxEnqueuer{store: st}

	msg, err := Enqueue(context.Background(), inbox, " WS ", " agent-1 ", " hello ", MessageOptions{
		SessionID:         "session-1",
		SourceKind:        "workflow",
		SourceRef:         "workflow://run",
		DriverRunID:       "run-1",
		TaskRunID:         "task-run-1",
		TriggerEventID:    "event-1",
		TriggerDeliveryID: "delivery-1",
		DedupeKey:         "dedupe-1",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if msg.WorkspaceKey != "WS" || msg.TargetAgentID != "agent-1" || msg.Body != "hello" {
		t.Fatalf("message core fields = %#v", msg)
	}
	if msg.SessionID != "session-1" || msg.SourceKind != "workflow" || msg.SourceRef != "workflow://run" || msg.DriverRunID != "run-1" || msg.TaskRunID != "task-run-1" || msg.TriggerEventID != "event-1" || msg.TriggerDeliveryID != "delivery-1" || msg.DedupeKey != "dedupe-1" {
		t.Fatalf("message metadata = %#v", msg)
	}
	if msg.Status != domain.AgentInboxMessageQueued {
		t.Fatalf("status = %q, want queued", msg.Status)
	}
}

func TestEnqueueDoesNotDedupeWithoutProducerKey(t *testing.T) {
	st := memstore.New()
	inbox := testInboxEnqueuer{store: st}

	first, err := Enqueue(context.Background(), inbox, "WS", "agent-1", "same body", MessageOptions{})
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	second, err := Enqueue(context.Background(), inbox, "WS", "agent-1", "same body", MessageOptions{})
	if err != nil {
		t.Fatalf("second enqueue: %v", err)
	}
	if first.InboxMessageID == second.InboxMessageID {
		t.Fatalf("duplicate message ID %q", first.InboxMessageID)
	}
}

func TestEnqueueValidatesRequiredFields(t *testing.T) {
	st := memstore.New()
	inbox := testInboxEnqueuer{store: st}

	for _, tc := range []struct {
		name      string
		workspace string
		agent     string
		body      string
	}{
		{name: "workspace", workspace: "", agent: "agent-1", body: "hello"},
		{name: "agent", workspace: "WS", agent: "", body: "hello"},
		{name: "body", workspace: "WS", agent: "agent-1", body: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Enqueue(context.Background(), inbox, tc.workspace, tc.agent, tc.body, MessageOptions{})
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

type testInboxEnqueuer struct {
	store store.Store
}

func (enqueuer testInboxEnqueuer) Enqueue(
	ctx context.Context,
	command interaction.EnqueueInboxCommand,
) (*interaction.InboxMessage, error) {
	message, err := enqueuer.store.AgentInboxMessages().Create(ctx, store.AgentInboxMessageCreate{
		WorkspaceKey: command.WorkspaceKey, TargetAgentID: command.TargetAgentID,
		SessionID: command.SessionID, Body: command.Body,
		SourceKind: command.SourceKind, SourceRef: command.SourceRef,
		DriverRunID: command.DriverRunID, TaskRunID: command.TaskRunID,
		TriggerEventID: command.TriggerEventID, TriggerDeliveryID: command.TriggerDeliveryID,
		DedupeKey: command.DedupeKey,
	})
	if err != nil {
		return nil, err
	}
	return &interaction.InboxMessage{
		WorkspaceKey: message.WorkspaceKey, MessageID: message.InboxMessageID,
		Cursor: message.Cursor, TargetAgentID: message.TargetAgentID,
		SessionID: message.SessionID, Body: message.Body,
		Status:     interaction.InboxStatus(message.Status),
		SourceKind: message.SourceKind, SourceRef: message.SourceRef,
		DriverRunID: message.DriverRunID, TaskRunID: message.TaskRunID,
		TriggerEventID: message.TriggerEventID, TriggerDeliveryID: message.TriggerDeliveryID,
		DedupeKey: message.DedupeKey, Attempt: message.Attempt,
		ClaimedBy: message.ClaimedBy, ClaimExpiresAt: message.ClaimExpiresAt,
		ErrorClass: message.ErrorClass, DeliveredThreadID: message.DeliveredThreadID,
		DeliveredAt: message.DeliveredAt, CreatedAt: message.CreatedAt,
		UpdatedAt: message.UpdatedAt,
	}, nil
}

func TestContentDedupeKeyIsStableAndNamespaced(t *testing.T) {
	a := ContentDedupeKey("workflow", "session-1", "hello")
	b := ContentDedupeKey("workflow", "session-1", "hello")
	c := ContentDedupeKey("trigger", "session-1", "hello")
	if a != b {
		t.Fatalf("dedupe key not stable: %q != %q", a, b)
	}
	if a == c {
		t.Fatalf("dedupe key should include namespace: %q", a)
	}
}
