package backends

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/chat"
	"github.com/olesho/harness-wrapper/pkg/chat/memstore"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

// The screens below are the two shapes the classifier has to tell apart on an
// auth verdict. Both carry the same logged-out banner; only one of them also
// has a live composer, which is the readiness-miss shape ScreenEvidence exists
// to make visible (a real login wall has no composer).
const (
	bannerOnlyScreen = "" +
		"╭──────────────────────────────────────────╮\n" +
		"│  Welcome to Claude Code                  │\n" +
		"│                                          │\n" +
		"│  Not logged in.                          │\n" +
		"╰──────────────────────────────────────────╯\n"

	composerPresentScreen = "" +
		"⏺ Claude Code\n" +
		"\n" +
		"  Invalid API key · check your credentials\n" +
		"\n" +
		"╭──────────────────────────────────────────╮\n" +
		"│ ❯ try \"fix the build\"                    │\n" +
		"╰──────────────────────────────────────────╯\n"
)

// withScreen substitutes the conversation screen reader for the duration of a
// test. chat.Conversation's screen is unexported and only chat.Open can fill
// it, so this is the seam that lets a unit test put a chosen screen behind
// conversationTurnError. The end-to-end behavior of the real reader is pinned
// separately by TestConversationSend_AuthSurfacesAsErroredTurn.
func withScreen(t *testing.T, text string) {
	t.Helper()
	prev := conversationScreenText
	conversationScreenText = func(*chat.Conversation) string { return text }
	t.Cleanup(func() { conversationScreenText = prev })
}

// The acceptance criterion: an auth-relabelled turn carries NO text of its own,
// so without the screen the classifier would describe the verdict as unscanned.
// With it, the tail that reaches agenterr holds the marker AND the screen, and
// the recorded ScreenEvidence distinguishes a bare login wall from a wall that
// somehow has a composer on it.
func TestConversationTurnError_CarriesScreenToClassifier(t *testing.T) {
	cases := []struct {
		name          string
		screen        string
		wantRule      string
		wantComposer  bool
		wantScreenSub string
	}{
		{"banner only", bannerOnlyScreen, "claude.loggedout.not_logged_in", false, "Not logged in"},
		{"composer present", composerPresentScreen, "claude.loggedout.invalid_api_key", true, "Invalid API key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withScreen(t, tc.screen)

			// The turn the wrapper actually emits: a reason, and no text at all.
			err := conversationTurnError(&chat.Conversation{}, chat.Turn{
				Role:   chat.RoleAssistant,
				State:  chat.TurnStateErrored,
				Reason: chat.ReasonAuthRequired,
			})
			var ie *InvocationError
			if !errors.As(err, &ie) {
				t.Fatalf("want *InvocationError, got %T", err)
			}
			if !strings.Contains(ie.OutputTail, agenterr.AuthRequiredMarker) {
				t.Fatalf("OutputTail lost the marker: %q", ie.OutputTail)
			}
			if !strings.Contains(ie.OutputTail, tc.wantScreenSub) {
				t.Fatalf("OutputTail does not carry the screen (%q): %q", tc.wantScreenSub, ie.OutputTail)
			}

			agentErr := agenterr.ClassifyFromOutput(ie.OutputTail, ie.ExitCode, "claude")
			if got := agentErr.Class.String(); got != "AuthFailure" {
				t.Fatalf("Class = %q, want AuthFailure", got)
			}
			screen := agentErr.Evidence.Screen
			if screen == nil || !screen.Scanned {
				t.Fatalf("Evidence.Screen = %+v, want a scanned screen", screen)
			}
			if screen.BannerRule != tc.wantRule {
				t.Errorf("BannerRule = %q, want %q", screen.BannerRule, tc.wantRule)
			}
			if screen.ComposerWitnessed == nil || *screen.ComposerWitnessed != tc.wantComposer {
				t.Errorf("ComposerWitnessed = %v, want %v", screen.ComposerWitnessed, tc.wantComposer)
			}
		})
	}
}

// Without a conversation there is nothing to read, and the verdict must still
// travel: the marker is carried, and the classifier records "we had no screen"
// rather than pretending it looked at one.
func TestConversationTurnError_NoConversationStillClassifies(t *testing.T) {
	err := conversationTurnError(nil, chat.Turn{Reason: chat.ReasonAuthRequired})
	var ie *InvocationError
	if !errors.As(err, &ie) {
		t.Fatalf("want *InvocationError, got %T", err)
	}
	agentErr := agenterr.ClassifyFromOutput(ie.OutputTail, ie.ExitCode, "claude")
	if got := agentErr.Class.String(); got != "AuthFailure" {
		t.Fatalf("Class = %q, want AuthFailure", got)
	}
	if screen := agentErr.Evidence.Screen; screen == nil || screen.BannerRule != "" {
		t.Fatalf("Screen = %+v, want no banner rule with no screen to read", screen)
	}
}

// The screen tail is bounded: a pathological emulator state must not push an
// unbounded blob into an error that is logged, stored and shipped as an event.
func TestScreenEvidence_IsBounded(t *testing.T) {
	withScreen(t, strings.Repeat("x", conversationScreenTailCap*3))
	if got := len(screenEvidence(&chat.Conversation{})); got != conversationScreenTailCap {
		t.Fatalf("screen tail = %d bytes, want the %d-byte cap", got, conversationScreenTailCap)
	}
}

// The one-shot half of the same criterion: the login banner lives on the raw
// PTY tail parked on Turn.Text, while the assistant history holds the
// pre-failure conversation. The terminal evidence window must offer both, and
// must not repeat either.
func TestClaudeTerminalEvidence_CarriesRawTailAndHistory(t *testing.T) {
	res := claudeRunTurnResult{
		Turn: chat.Turn{Reason: chat.ReasonAuthRequired, Text: bannerOnlyScreen},
		History: []chat.Turn{
			{Role: "assistant", Text: "working on the task"},
		},
	}
	evidence := claudeTerminalEvidence(res)
	for _, want := range []string{chat.ReasonAuthRequired, "working on the task", "Not logged in"} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("evidence lost %q: %q", want, evidence)
		}
	}

	agentErr := agenterr.ClassifyFromOutput(
		terminalTurnInvocationError(chat.ReasonAuthRequired, evidence).OutputTail, 1, "claude")
	if got := agentErr.Class.String(); got != "AuthFailure" {
		t.Fatalf("Class = %q, want AuthFailure", got)
	}
	if screen := agentErr.Evidence.Screen; screen == nil || screen.BannerRule != "claude.loggedout.not_logged_in" {
		t.Fatalf("Screen = %+v, want the logged-out banner rule", screen)
	}

	// A turn with no history must not have its own text repeated twice.
	bare := claudeRunTurnResult{Turn: chat.Turn{Reason: chat.ReasonAuthRequired, Text: "not logged in"}}
	if got := strings.Count(claudeTerminalEvidence(bare), "not logged in"); got != 1 {
		t.Fatalf("bare turn text appears %d times, want 1: %q", got, claudeTerminalEvidence(bare))
	}
}

// CONTRACT TEST against the pinned harness-wrapper.
//
// The defensive arm in runConversationTurn assumes conv.Send does NOT return
// chat.ErrAuthRequired: on v0.7.7 waitReadyForSend's auth short-circuit is
// caught inside Send (pkg/chat/send.go:45-56), which emits a terminal assistant
// turn instead. This pins that behavior against a real PTY-driven session, so
// a wrapper bump that flips it fails HERE — deliberately making the defensive
// arm live — rather than being discovered in production as a silent Unknown.
func TestConversationSend_AuthSurfacesAsErroredTurn(t *testing.T) {
	// A harness that paints an onboarding login wall and then just sits there:
	// the wall never becomes ready, which is exactly the state under test.
	conv, err := chat.Open(context.Background(), chat.Options{
		Harness:    "claude-code",
		BinaryPath: "/bin/sh",
		Args: []string{"-c",
			`printf '\n  Select login method\n\n  1. Claude account with subscription\n  2. Anthropic Console account\n\n'; sleep 30`},
		Store: memstore.New(),
		Cols:  120,
		Rows:  40,
	})
	if err != nil {
		t.Skipf("cannot open a PTY-backed conversation here: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conv.Close(ctx)
	})

	release, err := conv.AcquireControl(context.Background())
	if err != nil {
		t.Fatalf("AcquireControl: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	turnID, err := conv.Send(ctx, "do the thing")
	if errors.Is(err, chat.ErrAuthRequired) {
		t.Fatalf("PINNED CONTRACT BROKEN: Send now returns chat.ErrAuthRequired directly. " +
			"The defensive arm in runConversationTurn is live — verify it carries the screen, then update this test.")
	}
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if turnID == "" {
		t.Fatalf("Send returned no turn id for the auth short-circuit")
	}

	turn := waitErroredTurn(ctx, t, conv, turnID)
	if turn.Reason != chat.ReasonAuthRequired {
		t.Fatalf("Reason = %q, want ReasonAuthRequired", turn.Reason)
	}
	if strings.TrimSpace(turn.Text) != "" {
		t.Fatalf("Turn.Text = %q; the wrapper is expected to ship NO text with this verdict", turn.Text)
	}

	// End to end over a real screen: the invocation error the conversation path
	// builds carries the login wall the wrapper withheld.
	var ie *InvocationError
	if !errors.As(conversationTurnError(conv, turn), &ie) {
		t.Fatalf("want *InvocationError from the auth turn")
	}
	if !strings.Contains(ie.OutputTail, "Select login method") {
		t.Fatalf("OutputTail did not carry the live screen: %q", ie.OutputTail)
	}
	agentErr := agenterr.ClassifyFromOutput(ie.OutputTail, ie.ExitCode, "claude")
	if got := agentErr.Class.String(); got != "AuthFailure" {
		t.Fatalf("Class = %q, want AuthFailure", got)
	}
	if screen := agentErr.Evidence.Screen; screen == nil || screen.BannerRule != "claude.onboarding.select_login_method" {
		t.Fatalf("Screen = %+v, want the login-method onboarding rule", screen)
	}
}

// waitErroredTurn drains conversation events until the named assistant turn
// reaches its terminal state.
func waitErroredTurn(ctx context.Context, t *testing.T, conv *chat.Conversation, turnID string) chat.Turn {
	t.Helper()
	events := conv.Events()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("no terminal turn before the deadline: %v", ctx.Err())
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("conversation ended before the turn completed")
			}
			if ev.Type != chat.EventTurn || ev.Turn.Role != chat.RoleAssistant {
				continue
			}
			if ev.Turn.ID == turnID && ev.Turn.State == chat.TurnStateErrored {
				return ev.Turn
			}
		}
	}
}
