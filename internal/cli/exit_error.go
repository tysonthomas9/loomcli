package cli

import (
	"errors"
	"fmt"
)

type commandExitError struct {
	code int
	err  error
}

func (e *commandExitError) Error() string { return e.err.Error() }
func (e *commandExitError) Unwrap() error { return e.err }
func (e *commandExitError) CLIExitCode() int {
	return e.code
}

// NewCommandExitError returns an error that asks the loom executable to use
// code while preserving err for display and errors.Is/errors.As.
func NewCommandExitError(code int, err error) error {
	if code <= 0 {
		panic("CLI command exit code must be positive")
	}
	if err == nil {
		err = fmt.Errorf("command exited with status %d", code)
	}
	return &commandExitError{code: code, err: err}
}

// CommandExitCode returns the requested process exit code, or 1 for ordinary
// command errors.
func CommandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var coded interface{ CLIExitCode() int }
	if errors.As(err, &coded) && coded.CLIExitCode() > 0 {
		return coded.CLIExitCode()
	}
	return 1
}
