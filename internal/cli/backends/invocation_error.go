package backends

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/olesho/harness-wrapper/pkg/chat"
	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

// InvocationError carries invocation-local output evidence for classifying
// backend failures without relying on package-global state.
type InvocationError struct {
	Err        error
	OutputTail string
	ExitCode   int
}

func (e *InvocationError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *InvocationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// wrapInvocationError should be used by backend invokers whenever a subprocess
// may have emitted useful failure evidence. Otherwise automode falls back to
// err.Error(), which is often only "exit status N".
func wrapInvocationError(err error, outputTail string) error {
	if err == nil {
		return nil
	}

	// Categorical wrapper signals: when the wrapper reports the binary
	// is not on PATH, prepend the stable marker that agenterr classifies
	// as BackendUnavailable. Without this, the outer supervisor sees a
	// generic exec failure and falls back to Unknown (LOOM-4).
	if errors.Is(err, wrapper.ErrBinaryNotFound) {
		return binaryNotFoundInvocationError(err.Error(), outputTail)
	}

	// A PTY allocation/read failure means the wrapper could not launch the
	// backend process at all. In practice this is the ENOEXEC "exec format
	// error" seen when the backend binary is mid self-update and momentarily
	// not a valid executable. Mark it so the outer classifier records a
	// retryable SpawnFailure with the real reason, instead of a generic
	// Unknown that hides the cause from the operator and the Kanban UI.
	if errors.Is(err, wrapper.ErrPTYAllocation) || errors.Is(err, wrapper.ErrPTYRead) {
		return agentLaunchFailedInvocationError(err.Error(), outputTail)
	}

	// chat.ErrAuthRequired is the send-time login wall: pkg/chat refuses to
	// type a prompt into an onboarding screen and returns this instead. Its
	// text matches none of the residual auth patterns, so without an arm here
	// it classified as Unknown and burned the restart budget on a turn that
	// could not succeed.
	if errors.Is(err, chat.ErrAuthRequired) {
		return wallInvocationError(wallAuth, err.Error(), outputTail)
	}

	exitCode := 1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	}

	evidence := strings.TrimSpace(outputTail)
	raw := strings.TrimSpace(err.Error())
	if raw != "" && !strings.Contains(evidence, raw) {
		if evidence == "" {
			evidence = raw
		} else {
			evidence = raw + "\n" + evidence
		}
	}

	return &InvocationError{
		Err:        err,
		OutputTail: evidence,
		ExitCode:   exitCode,
	}
}

// binaryNotFoundInvocationError returns the canonical InvocationError
// for the wrapper-detected "binary missing" case. Used by both
// wrapWrapperResult (Status path) and wrapInvocationError (error path)
// so the marker text is consistent across entry points.
func binaryNotFoundInvocationError(reason, outputTail string) *InvocationError {
	msg := strings.TrimSpace(reason)
	if msg == "" {
		msg = "binary not found"
	}
	combined := agenterr.BackendUnavailableMarker + ": " + msg
	evidence := strings.TrimSpace(outputTail)
	if evidence == "" {
		evidence = combined
	} else if !strings.Contains(evidence, combined) {
		evidence = combined + "\n" + evidence
	}
	return &InvocationError{
		Err:        errors.New(combined),
		OutputTail: evidence,
		// 127 is the Unix convention for "command not found". Kept for
		// consumers that switch on ExitCode (the inner loom subprocess
		// itself exits with 1 via cobra; the marker text is the primary
		// signal for the outer classifier).
		ExitCode: 127,
	}
}

// agentLaunchFailedInvocationError returns the canonical InvocationError for
// the wrapper-detected "could not launch the backend process" case (PTY
// allocation/read failure, which surfaces the OS-level ENOEXEC "exec format
// error" when the backend binary is being replaced). Mirrors
// binaryNotFoundInvocationError so the marker text is consistent and the outer
// classifier maps it to a retryable SpawnFailure with the real reason.
func agentLaunchFailedInvocationError(reason, outputTail string) *InvocationError {
	msg := strings.TrimSpace(reason)
	if msg == "" {
		msg = "agent process failed to launch"
	}
	combined := agenterr.AgentLaunchFailedMarker + ": " + msg
	evidence := strings.TrimSpace(outputTail)
	if evidence == "" {
		evidence = combined
	} else if !strings.Contains(evidence, combined) {
		evidence = combined + "\n" + evidence
	}
	return &InvocationError{
		Err: errors.New(combined),
		// 126 is the Unix convention for "command found but not executable"
		// (exec format error / not executable), which matches a launch
		// failure where the binary exists but cannot be exec'd.
		OutputTail: evidence,
		ExitCode:   126,
	}
}

// terminalTurnInvocationError returns the canonical InvocationError for a turn
// the HARNESS declared terminal, carrying the marker that lets the outer
// classifier act on the harness's verdict instead of re-deriving it from prose.
//
// It is used for the two blameless reasons harness-wrapper reports
// categorically — an expired login and an exhausted quota window. Everything
// else keeps the plain errored path: the marker means "the harness told us
// what this is", and inventing one for a reason it did not name would recreate
// the guessing this removes.
//
// Returns nil when the reason is not one of the two, so callers can fall
// through to their existing handling with a single nil check. Its sibling
// wallInvocationError (terminal_wall.go) covers the other direction: a wall
// loom detected itself because the harness named no reason at all.
func terminalTurnInvocationError(reason, outputTail string) *InvocationError {
	var marker string
	switch {
	case strings.Contains(reason, chat.ReasonAuthRequired):
		marker = agenterr.AuthRequiredMarker
	case strings.Contains(reason, chat.ReasonUsageLimited):
		marker = agenterr.UsageLimitedMarker
	default:
		return nil
	}

	combined := marker + ": " + strings.TrimSpace(reason)
	evidence := strings.TrimSpace(outputTail)
	if evidence == "" {
		evidence = combined
	} else if !strings.Contains(evidence, combined) {
		evidence = combined + "\n" + evidence
	}
	return &InvocationError{
		Err:        errors.New(combined),
		OutputTail: evidence,
		// The marker text is the signal the outer classifier reads; the exit
		// code only has to be non-zero so the run is not mistaken for a clean
		// one. 1 keeps it uniform with the ordinary errored-turn path.
		ExitCode: 1,
	}
}

// wrapWrapperResult translates a terminal wrapper.Result into the same
// InvocationError shape that the legacy exec.Cmd path produces, so
// downstream classifiers (agenterr.ClassifyFromOutput) and consumers
// (automode.classifyInvokeError) keep working unchanged.
//
// Mapping:
//   - StatusIdle       → nil (success).
//   - StatusInterrupted → context.Canceled-wrapped error (callers
//     that use errors.Is(ctx.Canceled) keep working).
//   - Every other status → *InvocationError. The synthesized Err
//     carries the wrapper's Reason so agenterr's text-based
//     classification has something to match on.
func wrapWrapperResult(res wrapper.Result, outputTail string) error {
	switch res.Status {
	case wrapper.StatusIdle:
		return nil
	case wrapper.StatusInterrupted:
		if res.Reason != "" {
			return fmt.Errorf("%w: %s", context.Canceled, res.Reason)
		}
		return context.Canceled
	case wrapper.StatusBinaryNotFound:
		return binaryNotFoundInvocationError(res.Reason, outputTail)
	}

	reason := strings.TrimSpace(res.Reason)
	if reason == "" {
		reason = fmt.Sprintf("harness exited with status %s", res.Status)
	}
	exitCode := res.ExitCode
	if exitCode <= 0 {
		exitCode = 1
	}

	evidence := strings.TrimSpace(outputTail)
	if evidence == "" {
		evidence = reason
	} else if !strings.Contains(evidence, reason) {
		evidence = reason + "\n" + evidence
	}

	return &InvocationError{
		Err:        errors.New(reason),
		OutputTail: evidence,
		ExitCode:   exitCode,
	}
}
