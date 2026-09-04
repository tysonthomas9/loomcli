package backends

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/chat"
	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
)

func TestWrapWrapperResult_StatusMapping(t *testing.T) {
	tests := []struct {
		name           string
		res            wrapper.Result
		outputTail     string
		wantNil        bool
		wantCtxCancel  bool
		wantExitCode   int
		wantEvidenceIn []string
	}{
		{
			name:    "idle_returns_nil",
			res:     wrapper.Result{Status: wrapper.StatusIdle, ExitCode: 0},
			wantNil: true,
		},
		{
			name:          "interrupted_wraps_context_canceled",
			res:           wrapper.Result{Status: wrapper.StatusInterrupted, Reason: "stop requested"},
			wantCtxCancel: true,
		},
		{
			name:           "failed_with_exit_code",
			res:            wrapper.Result{Status: wrapper.StatusFailed, ExitCode: 2, Reason: "harness exited 2"},
			wantExitCode:   2,
			wantEvidenceIn: []string{"harness exited 2"},
		},
		{
			name:           "failed_zero_exit_code_promotes_to_1",
			res:            wrapper.Result{Status: wrapper.StatusFailed, ExitCode: 0, Reason: "weird"},
			wantExitCode:   1,
			wantEvidenceIn: []string{"weird"},
		},
		{
			name:           "blocked_by_cost_carries_reason",
			res:            wrapper.Result{Status: wrapper.StatusBlockedByCost, Reason: "quota exhausted"},
			wantExitCode:   1,
			wantEvidenceIn: []string{"quota exhausted"},
		},
		{
			name:           "retry_later_carries_reason",
			res:            wrapper.Result{Status: wrapper.StatusRetryLater, Reason: "rate limited"},
			wantExitCode:   1,
			wantEvidenceIn: []string{"rate limited"},
		},
		{
			name:           "api_error_carries_reason",
			res:            wrapper.Result{Status: wrapper.StatusAPIError, Reason: "API error: 529"},
			wantExitCode:   1,
			wantEvidenceIn: []string{"API error: 529"},
		},
		{
			name:           "unknown_synthesizes_reason",
			res:            wrapper.Result{Status: wrapper.StatusUnknown, ExitCode: 0},
			wantExitCode:   1,
			wantEvidenceIn: []string{"harness exited with status unknown"},
		},
		{
			// LOOM-4 contract: a categorical StatusBinaryNotFound from
			// the wrapper must surface the agenterr marker so the outer
			// classifier sees it as BackendUnavailable instead of
			// falling back to Unknown. Exit code 127 follows the Unix
			// "command not found" convention.
			name:           "binary_not_found_emits_marker_and_127",
			res:            wrapper.Result{Status: wrapper.StatusBinaryNotFound, ExitCode: -1, Reason: `wrapper: binary not found: exec: "codex": executable file not found in $PATH`},
			wantExitCode:   127,
			wantEvidenceIn: []string{"loom: backend binary not on PATH", `exec: "codex": executable file not found`},
		},
		{
			name:           "output_tail_merged_with_reason",
			res:            wrapper.Result{Status: wrapper.StatusFailed, ExitCode: 1, Reason: "bad happened"},
			outputTail:     "Step 1\nStep 2",
			wantExitCode:   1,
			wantEvidenceIn: []string{"bad happened", "Step 2"},
		},
		{
			name:           "output_tail_already_contains_reason_not_duplicated",
			res:            wrapper.Result{Status: wrapper.StatusFailed, ExitCode: 1, Reason: "rate limit"},
			outputTail:     "Step 1\nrate limit hit\nStep 2",
			wantExitCode:   1,
			wantEvidenceIn: []string{"rate limit hit", "Step 1", "Step 2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := wrapWrapperResult(tc.res, tc.outputTail)
			if tc.wantNil {
				if err != nil {
					t.Fatalf("got err %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("got nil, want non-nil error")
			}
			if tc.wantCtxCancel {
				if !errors.Is(err, context.Canceled) {
					t.Errorf("err: got %v, want errors.Is(context.Canceled)", err)
				}
				return
			}
			var invErr *InvocationError
			if !errors.As(err, &invErr) {
				t.Fatalf("err: got %T, want *InvocationError", err)
			}
			if invErr.ExitCode != tc.wantExitCode {
				t.Errorf("ExitCode: got %d, want %d", invErr.ExitCode, tc.wantExitCode)
			}
			for _, sub := range tc.wantEvidenceIn {
				if !strings.Contains(invErr.OutputTail, sub) {
					t.Errorf("OutputTail %q missing %q", invErr.OutputTail, sub)
				}
			}
		})
	}
}

// TestWrapInvocationError_PTYLaunchFailure asserts the LOOM contract that a
// harness-wrapper PTY allocation/read failure (the ENOEXEC "exec format error"
// seen when the backend binary is mid self-update) surfaces the stable
// AgentLaunchFailedMarker and round-trips through the classifier as a retryable
// SpawnFailure carrying the real reason — instead of the generic Unknown /
// "unclassified error (exit code 1)" that previously hid the cause from the UI.
func TestWrapInvocationError_PTYLaunchFailure(t *testing.T) {
	for _, sentinel := range []error{wrapper.ErrPTYAllocation, wrapper.ErrPTYRead} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			// Shape mirrors session.go: fmt.Errorf("%w: %v", ErrPTYAllocation, osErr).
			runErr := fmt.Errorf("%w: fork/exec /path/claude: exec format error", sentinel)

			err := wrapInvocationError(runErr, "")
			var invErr *InvocationError
			if !errors.As(err, &invErr) {
				t.Fatalf("err: got %T, want *InvocationError", err)
			}
			if invErr.ExitCode != 126 {
				t.Errorf("ExitCode: got %d, want 126", invErr.ExitCode)
			}
			if !strings.Contains(invErr.OutputTail, agenterr.AgentLaunchFailedMarker) {
				t.Errorf("OutputTail %q missing marker %q", invErr.OutputTail, agenterr.AgentLaunchFailedMarker)
			}
			if !strings.Contains(invErr.OutputTail, "exec format error") {
				t.Errorf("OutputTail %q dropped underlying reason", invErr.OutputTail)
			}

			// End-to-end: the classifier must map the marker to a retryable
			// SpawnFailure, not Unknown.
			ae := agenterr.ClassifyFromOutput(invErr.OutputTail, invErr.ExitCode, "claude")
			if ae.Class != agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome) {
				t.Errorf("classified Class: got %v, want SpawnFailure", ae.Class)
			}
			if got := agentpolicy.Decide(ae.Class).Decision; got != agentpolicy.Retry {
				t.Errorf("SpawnFailure decision = %v, want Retry", got)
			}
			if strings.Contains(ae.Message, "unclassified") {
				t.Errorf("Message %q should be specific, not the generic exit-code fallback", ae.Message)
			}
		})
	}
}

func TestWrapWrapperResult_OutputTailNotDuplicatedWhenReasonEmpty(t *testing.T) {
	err := wrapWrapperResult(
		wrapper.Result{Status: wrapper.StatusFailed, ExitCode: 1},
		"some tail",
	)
	var invErr *InvocationError
	if !errors.As(err, &invErr) {
		t.Fatalf("err: got %T, want *InvocationError", err)
	}
	if !strings.Contains(invErr.OutputTail, "some tail") {
		t.Errorf("OutputTail %q missing %q", invErr.OutputTail, "some tail")
	}
	// Synthesized reason should also be present since outputTail did not
	// already contain it.
	if !strings.Contains(invErr.OutputTail, "harness exited with status failed") {
		t.Errorf("OutputTail %q missing synthesized reason", invErr.OutputTail)
	}
}

// Moved here from backend_claude_wall_test.go when the screen-scrape wall
// detector was removed. The send-time login wall: pkg/chat refuses to type
// into an onboarding screen and returns chat.ErrAuthRequired, whose text
// matches none of the residual auth patterns. Without the arm in
// wrapInvocationError this classified as Unknown and burned the restart
// budget. It is a typed sentinel, not inferred screen text, which is why the
// removal keeps it — now via authRequiredInvocationError.
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

// The keep path, pinned: a wall the HARNESS named is still detected and
// classified exactly as before the screen-scrape detector was removed. This is
// the only route to a wall marker that remains.
func TestTerminalTurnInvocationErrorHarnessNamedWalls(t *testing.T) {
	tests := []struct {
		name       string
		reason     string
		wantMarker string
		wantClass  agenterr.Outcome
	}{
		{
			name:       "auth_required",
			reason:     chat.ReasonAuthRequired,
			wantMarker: agenterr.AuthRequiredMarker,
			wantClass:  agenterr.OutcomeFromHarness(wrapper.ErrAuth),
		},
		{
			name:       "usage_limited",
			reason:     chat.ReasonUsageLimited,
			wantMarker: agenterr.UsageLimitedMarker,
			wantClass:  agenterr.OutcomeFromHarness(wrapper.ErrRateLimited),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ie := terminalTurnInvocationError(tc.reason, "some output tail")
			if ie == nil {
				t.Fatalf("terminalTurnInvocationError(%q) = nil, want a marked error", tc.reason)
			}
			if !strings.Contains(ie.Error(), tc.wantMarker) {
				t.Fatalf("error %q missing marker %q", ie.Error(), tc.wantMarker)
			}
			ae := agenterr.ClassifyFromOutput(ie.OutputTail, ie.ExitCode, "claude")
			if ae.Class != tc.wantClass {
				t.Fatalf("classified %v, want %v", ae.Class, tc.wantClass)
			}
		})
	}
}

// The deliberate behavior change from removing the screen-scrape detector:
// an errored turn whose TEXT contains a billing phrase no longer acquires a
// wall marker. It classifies as an ordinary errored turn — retryable and
// non-fatal, which is what the code did before the detector existed. Pinned so
// it is not "fixed" back into a scrape by accident; the correct fix, if a real
// billing wall is ever observed here, is a harness-named billing reason.
func TestConversationTurnErrorBillingTextIsUnmarked(t *testing.T) {
	err := conversationTurnError(chat.Turn{
		State: chat.TurnStateErrored,
		Text:  "⏺ Your credit balance is too low to run this request.",
	})
	if err == nil {
		t.Fatal("conversationTurnError returned nil for an errored turn")
	}
	for _, marker := range []string{
		agenterr.BillingWallMarker,
		agenterr.AuthRequiredMarker,
		agenterr.UsageLimitedMarker,
	} {
		if strings.Contains(err.Error(), marker) {
			t.Fatalf("errored turn acquired marker %q from its own text: %q", marker, err.Error())
		}
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
