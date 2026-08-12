package leadcontrol

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/chat"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

// TestRunHarnessLeadRuntimeDrainsQueuedInbox reproduces the live delivery
// path for a message enqueued by ANOTHER process (serve): the message lands
// in the inbox as queued, and the harness runtime's drain ticker — not a
// direct in-process DeliverLeadMessage call — must pick it up and inject it
// into the conversation.
func TestRunHarnessLeadRuntimeDrainsQueuedInbox(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createHarnessLeadSession(t, st)

	fake := newFakeHarnessConversation()
	origOpen := openHarnessConversation
	openHarnessConversation = func(_ context.Context, _ chat.Options) (harnessConversation, error) {
		return fake, nil
	}
	t.Cleanup(func() { openHarnessConversation = origOpen })

	var out bytes.Buffer
	runtimeErr := make(chan error, 1)
	go func() {
		runtimeErr <- RunHarnessLeadRuntime(ctx, HarnessLeadRuntimeConfig{
			Store:     st,
			Workspace: "WS",
			LeadName:  "nova",
			SessionID: "lead-session",
			WorkDir:   "/repo",
			Prompt:    "lead prompt",
			Backend:   "claude",
			Stdin:     strings.NewReader(""),
			Stdout:    &out,
			Stderr:    &out,
		})
	}()
	waitForCondition(t, func() bool { return lookupLeadConversation("lead-session") != nil },
		"conversation was not registered")
	waitForCondition(t, func() bool {
		return getLeadSession(t, st).Metadata[MetadataRuntimeStatus] != RuntimeStatusStarting
	}, "runtime status never left starting")

	// Enqueue exactly the way serve does when the registry lookup misses:
	// a queued inbox message with NO in-process delivery.
	const message = "follow-up question from the chat panel"
	if _, err := createLeadInboxMessage(ctx, st, "WS", "nova", "lead-session", message, LeadMessageDeliveryOptions{
		SourceKind: "user_chat",
		DedupeKey:  "drain-test-1",
	}); err != nil {
		t.Fatalf("enqueue inbox message: %v", err)
	}

	// The 2s drain ticker must deliver it without any external nudge.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(string(fake.stdinBytes()), message) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(string(fake.stdinBytes()), message) {
		t.Fatalf("drain never delivered the queued message; staged stdin = %q", fake.stdinBytes())
	}

	close(fake.waitCh)
	select {
	case <-runtimeErr:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not exit")
	}
}
