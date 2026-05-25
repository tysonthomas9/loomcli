package backends

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/olesho/harness-wrapper/pkg/wrapper"
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
