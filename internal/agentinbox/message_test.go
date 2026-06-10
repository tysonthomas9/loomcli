package agentinbox

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

func TestEnqueueCreatesGenericAgentInboxMessage(t *testing.T) {
	st := memstore.New()

	msg, err := Enqueue(context.Background(), st, " WS ", " agent-1 ", " hello ", MessageOptions{
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

	first, err := Enqueue(context.Background(), st, "WS", "agent-1", "same body", MessageOptions{})
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	second, err := Enqueue(context.Background(), st, "WS", "agent-1", "same body", MessageOptions{})
	if err != nil {
		t.Fatalf("second enqueue: %v", err)
	}
	if first.InboxMessageID == second.InboxMessageID {
		t.Fatalf("duplicate message ID %q", first.InboxMessageID)
	}
}

func TestEnqueueValidatesRequiredFields(t *testing.T) {
	st := memstore.New()

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
			_, err := Enqueue(context.Background(), st, tc.workspace, tc.agent, tc.body, MessageOptions{})
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
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
