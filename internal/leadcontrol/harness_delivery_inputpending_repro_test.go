package leadcontrol

// Safety regressions for lossy harness input-request lifecycle events.
//
// An earlier attempt "recovered" a wedged inputPending flag by clearing it on
// assistant turn activity. That was unsafe: harness-wrapper treats interactive
// input prompts as INDEPENDENT of turn state (pkg/chat conversation.go
// handleTurnsEvent), so a turn Pending/Streaming/Complete event can fire while a
// trust/permission dialog is genuinely open. Clearing inputPending on a turn
// event would drop the guard and paste a queued assignment into the open dialog.
//
// These tests lock in the correct behavior: turn activity never clears the
// event hint, while an authoritative probe under the conversation control token
// protects staging and repairs either kind of dropped lifecycle event.

import (
	"context"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/chat"
	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

// TestDeliveryRejectsDroppedInputRequestEvent models the contract of
// harness-wrapper's bounded Conversation.Events channel: when the channel is
// full, an EventInputRequest may be dropped. Delivery must still consult
// authoritative conversation state before staging bytes, otherwise a quiet
// permission dialog is indistinguishable from an idle composer here.
func TestDeliveryRejectsDroppedInputRequestEvent(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createHarnessLeadSession(t, st)
	setHarnessRuntimeMetadata(t, st, RuntimeStatusWaitingUserInput)
	fake := newFakeHarnessConversation()
	fake.setSnapshot(wrapper.Snapshot{
		Status:       wrapper.StatusWaitingForInput,
		LastOutputAt: time.Now().Add(-time.Minute),
	})
	fake.setInputPending(true)
	installFakeHarnessConversation(t, "lead-session", fake)

	// No EventInputRequest reaches the handle: it was dropped upstream.
	result, err := DeliverLeadMessage(ctx, st, "WS", "nova", "Task TASK-1 completed.")
	if err != nil {
		t.Fatalf("DeliverLeadMessage() error = %v", err)
	}
	if result.State != DeliveryStatePending {
		t.Fatalf("delivery state = %q, want pending while authoritative input state is pending", result.State)
	}
	if got := fake.stdinBytes(); len(got) != 0 {
		t.Fatalf("staged %q into an open input dialog after its request event was dropped", got)
	}
}

// TestDeliveryRecoversFromDroppedInputResolvedEvent proves the inverse case:
// the local event hint may remain true after the wrapper has authoritatively
// cleared the prompt. Delivery must refresh that hint and drain the queue.
func TestDeliveryRecoversFromDroppedInputResolvedEvent(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createHarnessLeadSession(t, st)
	setHarnessRuntimeMetadata(t, st, RuntimeStatusWaitingUserInput)
	fake := newFakeHarnessConversation()
	fake.setInputPending(true)
	handle := installFakeHarnessConversation(t, "lead-session", fake)
	handle.observeConversationEvent(chat.ConversationEvent{
		Type:  chat.EventInputRequest,
		Input: &chat.InputRequest{ID: "trust-1", Kind: "trust_prompt"},
	})

	// The dialog closes in harness-wrapper, but EventInputResolved is dropped.
	fake.setInputPending(false)
	result, err := DeliverLeadMessage(ctx, st, "WS", "nova", "Task TASK-1 completed.")
	if err != nil {
		t.Fatalf("DeliverLeadMessage() error = %v", err)
	}
	if result.State != DeliveryStateDelivered {
		t.Fatalf("delivery state = %q, want delivered after authoritative input state cleared (reason %q)", result.State, result.Reason)
	}
	if handle.hasPendingInput() {
		t.Fatal("dropped resolve event left the local input hint wedged after authoritative refresh")
	}
	if got := string(fake.stdinBytes()); got != "Task TASK-1 completed." {
		t.Fatalf("staged stdin after authoritative recovery = %q, want queued message", got)
	}
}

// TestInputPendingSurvivesTurnActivity: a surfaced input request must stay
// pending across every assistant turn event. Turn state is independent of input
// state, so none of these events proves the dialog resolved.
func TestInputPendingSurvivesTurnActivity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state chat.TurnState
	}{
		{"pending", chat.TurnStatePending},
		{"streaming", chat.TurnStateStreaming},
		{"complete", chat.TurnStateComplete},
		{"errored", chat.TurnStateErrored},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeHarnessConversation()
			handle := &leadConversationHandle{conv: fake}

			handle.observeConversationEvent(chat.ConversationEvent{
				Type:  chat.EventInputRequest,
				Input: &chat.InputRequest{ID: "trust-1", Kind: "trust_prompt"},
			})
			// A turn event fires while the dialog is still open (no resolve seen).
			handle.observeConversationEvent(chat.ConversationEvent{
				Type: chat.EventTurn,
				Turn: chat.Turn{Role: chat.RoleAssistant, State: tc.state},
			})
			if !handle.hasPendingInput() {
				t.Fatalf("hasPendingInput() = false after assistant %s; a turn event "+
					"cleared a still-open input dialog — an assignment could be pasted into it", tc.name)
			}
		})
	}
}

// TestDeliveryStaysBlockedWhileDialogOpenDespiteTurnEvents exercises the same
// guard at the delivery API: with an unresolved input request, delivery must
// keep returning "interactive input" no matter what turn events arrive.
func TestDeliveryStaysBlockedWhileDialogOpenDespiteTurnEvents(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createHarnessLeadSession(t, st)
	setHarnessRuntimeMetadata(t, st, RuntimeStatusWaitingUserInput)
	fake := newFakeHarnessConversation()
	fake.setInputPending(true)
	handle := installFakeHarnessConversation(t, "lead-session", fake)

	handle.observeConversationEvent(chat.ConversationEvent{
		Type:  chat.EventInputRequest,
		Input: &chat.InputRequest{ID: "trust-1", Kind: "trust_prompt"},
	})
	// A full turn appears to run and complete while the dialog is still open;
	// no EventInputResolved is observed.
	handle.observeConversationEvent(chat.ConversationEvent{
		Type: chat.EventTurn,
		Turn: chat.Turn{Role: chat.RoleAssistant, State: chat.TurnStateStreaming},
	})
	handle.observeConversationEvent(chat.ConversationEvent{
		Type: chat.EventTurn,
		Turn: chat.Turn{Role: chat.RoleAssistant, State: chat.TurnStateComplete},
	})

	result, err := DeliverLeadMessage(ctx, st, "WS", "nova", "Task TASK-1 completed.")
	if err != nil {
		t.Fatalf("DeliverLeadMessage() error = %v", err)
	}
	if result.State != DeliveryStatePending {
		t.Fatalf("delivery state = %q, want pending — turn events must not unblock an open dialog", result.State)
	}
	if got := fake.stdinBytes(); len(got) != 0 {
		t.Fatalf("staged %q into a still-open input dialog; nothing should be written", got)
	}

	// The dialog resolving IS the authoritative signal — delivery drains then.
	handle.observeConversationEvent(chat.ConversationEvent{
		Type:  chat.EventInputResolved,
		Input: &chat.InputRequest{ID: "trust-1"},
	})
	fake.setInputPending(false)
	result, err = DeliverPendingLeadMessages(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("DeliverPendingLeadMessages() error = %v", err)
	}
	if result.State != DeliveryStateDelivered {
		t.Fatalf("delivery state = %q, want delivered after input resolved (reason %q)", result.State, result.Reason)
	}
}
