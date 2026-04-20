package backends

import (
	"errors"
	"os/exec"
	"strings"
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
