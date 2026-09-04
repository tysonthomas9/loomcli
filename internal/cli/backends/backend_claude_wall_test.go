package backends

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/chat"
	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

// The send-time login wall: pkg/chat refuses to type into an onboarding screen
// and returns chat.ErrAuthRequired, whose text matches none of the residual
// auth patterns. Without the arm in wrapInvocationError this classified as
// Unknown and burned the restart budget.
func TestWrapInvocationErrorAuthRequired(t *testing.T) {
	err := wrapInvocationError(chat.ErrAuthRequired, "some screen output")
	var ie *InvocationError
	if !errors.As(err, &ie) {
		t.Fatalf("wrapInvocationError returned %T, want *InvocationError", err)
	}
	if !strings.Contains(ie.Error(), agenterr.AuthRequiredMarker) {
		t.Fatalf("error %q missing the auth marker", ie.Error())
	}
	ae := agenterr.ClassifyFromOutput(ie.OutputTail, ie.ExitCode, "claude")
	if ae.Class != agenterr.OutcomeFromHarness(wrapper.ErrAuth) {
		t.Fatalf("classified %v, want ErrAuth", ae.Class)
	}
}

// The fast path: the harness errored the turn on a billing banner without
// naming one of its two canonical reasons, so there is no watchdog wait at all.
func TestConversationTurnErrorDetectsBillingWall(t *testing.T) {
	err := conversationTurnError(chat.Turn{
		State: chat.TurnStateErrored,
		Text:  "⏺ Your credit balance is too low to run this request.",
	})
	var ie *InvocationError
	if !errors.As(err, &ie) {
		t.Fatalf("conversationTurnError returned %T, want *InvocationError", err)
	}
	if !strings.Contains(ie.Error(), agenterr.BillingWallMarker) {
		t.Fatalf("error %q missing the billing marker", ie.Error())
	}
}

// A turn errored for an ordinary reason keeps the plain path: no marker
// invented for a wall that is not there.
func TestConversationTurnErrorPlainReasonIsUnmarked(t *testing.T) {
	err := conversationTurnError(chat.Turn{State: chat.TurnStateErrored, Reason: "stream closed", Text: "partial output"})
	if strings.Contains(err.Error(), "loom: harness") {
		t.Fatalf("plain errored turn acquired a marker: %q", err.Error())
	}
}

func TestClaudeTurnWithRetryDoesNotRetryWall(t *testing.T) {
	calls := 0
	wall := wallInvocationError(wallBilling, "Your credit balance is too low.", "")
	_, err := runClaudeTurnWithRetry(context.Background(), func() (claudeRunTurnResult, error) {
		calls++
		return claudeRunTurnResult{}, wall
	})
	if calls != 1 {
		t.Fatalf("invoke called %d times, want 1 — a wall must never be retried", calls)
	}
	if !errors.Is(err, error(wall)) {
		t.Fatalf("err = %v, want the wall error", err)
	}
}

// End-to-end at leaf level: a harness that prints the banner and then parks
// forever must return a classified billing error PROMPTLY — bounded by the
// settle window, not by the test's own timeout.
func TestInvokeClaudeRunTurnFiresOnParkedBillingWall(t *testing.T) {
	t.Setenv(envWallSettleSeconds, "1")

	prev := claudeRunTurn
	t.Cleanup(func() { claudeRunTurn = prev })
	claudeRunTurn = func(ctx context.Context, cfg claudeRunTurnConfig) (claudeRunTurnResult, error) {
		if cfg.Output != nil {
			_, _ = cfg.Output.Write([]byte("\x1b[2J⏺ Your credit balance is too low to run this request.\r\n"))
		}
		<-ctx.Done()
		return claudeRunTurnResult{}, ctx.Err()
	}

	done := make(chan error, 1)
	go func() {
		_, err := invokeClaudeRunTurn(context.Background(), t.TempDir(), "do work", "agent", "", nil, nil)
		done <- err
	}()

	select {
	case err := <-done:
		var ie *InvocationError
		if !errors.As(err, &ie) {
			t.Fatalf("invokeClaudeRunTurn returned %T (%v), want *InvocationError", err, err)
		}
		if !strings.Contains(ie.Error(), agenterr.BillingWallMarker) {
			t.Fatalf("error %q missing the billing marker", ie.Error())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("invokeClaudeRunTurn never returned: the wall watcher did not end the parked turn")
	}
}

// The off switch: with the window at zero no watcher is started, so a parked
// harness behaves exactly as it did before this detector existed.
func TestInvokeClaudeRunTurnDisabledByZeroSettle(t *testing.T) {
	t.Setenv(envWallSettleSeconds, "0")

	prev := claudeRunTurn
	t.Cleanup(func() { claudeRunTurn = prev })
	ctx, cancel := context.WithCancel(context.Background())
	claudeRunTurn = func(ctx context.Context, cfg claudeRunTurnConfig) (claudeRunTurnResult, error) {
		if cfg.Output != nil {
			_, _ = cfg.Output.Write([]byte("Your credit balance is too low.\n"))
		}
		<-ctx.Done()
		return claudeRunTurnResult{}, ctx.Err()
	}

	done := make(chan error, 1)
	go func() {
		_, err := invokeClaudeRunTurn(ctx, t.TempDir(), "do work", "agent", "", nil, nil)
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("turn returned %v with the detector disabled; it must wait for its caller", err)
	case <-time.After(3 * time.Second):
	}

	cancel()
	select {
	case err := <-done:
		if strings.Contains(err.Error(), agenterr.BillingWallMarker) {
			t.Fatalf("disabled detector still marked the error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("turn did not unwind after its context was cancelled")
	}
}
