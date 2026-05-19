package backends

import (
	"errors"
	"strings"
	"testing"
)

func TestInvocationErrorAndWrapping(t *testing.T) {
	if wrapInvocationError(nil, "ignored") != nil {
		t.Fatal("wrapInvocationError(nil) returned error")
	}

	base := errors.New("exit status 42")
	err := wrapInvocationError(base, "tail evidence").(*InvocationError)
	if err.Error() != "exit status 42" {
		t.Fatalf("Error() = %q", err.Error())
	}
	if !errors.Is(err, base) {
		t.Fatal("InvocationError should unwrap to original error")
	}
	if err.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want default 1", err.ExitCode)
	}
	if !strings.Contains(err.OutputTail, "exit status 42") || !strings.Contains(err.OutputTail, "tail evidence") {
		t.Fatalf("OutputTail = %q, want original error and tail evidence", err.OutputTail)
	}

	if got := (*InvocationError)(nil).Error(); got != "" {
		t.Fatalf("nil Error() = %q", got)
	}
	if got := (&InvocationError{}).Error(); got != "" {
		t.Fatalf("empty Error() = %q", got)
	}
	if got := (*InvocationError)(nil).Unwrap(); got != nil {
		t.Fatalf("nil Unwrap() = %v", got)
	}
}
