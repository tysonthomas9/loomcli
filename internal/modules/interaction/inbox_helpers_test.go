package interaction

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type generatedInboxStub struct {
	command EnqueueInboxCommand
}

func (stub *generatedInboxStub) Enqueue(_ context.Context, command EnqueueInboxCommand) (*InboxMessage, error) {
	stub.command = command
	return &InboxMessage{
		WorkspaceKey:  command.WorkspaceKey,
		MessageID:     command.MessageID,
		TargetAgentID: command.TargetAgentID,
		Body:          command.Body,
		Status:        InboxQueued,
	}, nil
}

func TestEnqueueGeneratedUsesOwnerModel(t *testing.T) {
	stub := &generatedInboxStub{}
	message, err := EnqueueGenerated(context.Background(), stub, EnqueueInboxCommand{
		WorkspaceKey: " WS ", TargetAgentID: " agent-1 ", Body: " hello ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.MessageID == "" || stub.command.MessageID != message.MessageID {
		t.Fatalf("generated message ID = %q, command = %+v", message.MessageID, stub.command)
	}
	if message.WorkspaceKey != "WS" || message.TargetAgentID != "agent-1" || message.Body != "hello" {
		t.Fatalf("normalized message = %+v", message)
	}
}

func TestEnqueueGeneratedFailsClosed(t *testing.T) {
	_, err := EnqueueGenerated(context.Background(), nil, EnqueueInboxCommand{
		WorkspaceKey: "WS", TargetAgentID: "agent-1", Body: "hello",
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want unavailable", err)
	}
	_, err = EnqueueGenerated(context.Background(), &generatedInboxStub{}, EnqueueInboxCommand{})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want invalid", err)
	}
}

func TestContentDedupeKey(t *testing.T) {
	first := ContentDedupeKey(" driver-message ", " WS ", "run-1")
	second := ContentDedupeKey("driver-message", "WS", "run-1")
	if first != second || !strings.HasPrefix(first, "driver-message:") {
		t.Fatalf("dedupe keys = %q and %q", first, second)
	}
	if first == ContentDedupeKey("driver-message", "WS", "run-2") {
		t.Fatal("different content produced the same key")
	}
}
