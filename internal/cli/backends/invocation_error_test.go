package backends

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/wrapper"
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
