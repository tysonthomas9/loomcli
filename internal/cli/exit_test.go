package cli

import (
	"errors"
	"fmt"
	"testing"
)

func TestCommandExitCode(t *testing.T) {
	cause := errors.New("blocked")
	err := fmt.Errorf("wrapped: %w", NewCommandExitError(2, cause))
	if got := CommandExitCode(err); got != 2 {
		t.Fatalf("CommandExitCode() = %d, want 2", got)
	}
	if !errors.Is(err, cause) {
		t.Fatal("coded error did not preserve its cause")
	}
	if got := CommandExitCode(errors.New("ordinary")); got != 1 {
		t.Fatalf("ordinary CommandExitCode() = %d, want 1", got)
	}
	if got := CommandExitCode(nil); got != 0 {
		t.Fatalf("nil CommandExitCode() = %d, want 0", got)
	}
}
