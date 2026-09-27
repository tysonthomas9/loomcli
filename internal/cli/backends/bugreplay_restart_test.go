//go:build daemon_bugreplay

// Bug-replay fault tests (restart group, crash/restart/orphan class) for
// backend invocation errors. See
// internal/cli/daemon/supervisor/bugreplay_restart_test.go.
package backends

import (
	"fmt"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

// F35 (#116, grouped #110; fixed at aeb665103) regression: a PTY allocation or
// read failure (ENOEXEC while the backend CLI binary is mid self-update) must
// classify as a retryable SpawnFailure that keeps the launch reason, not as
// "unclassified error (exit code 1)" / Unknown, which burned restart budgets
// and left tasks stuck in_progress.
func TestBugReplay_F35_PTYLaunchFailureIsSpawnFailure(t *testing.T) {
	for _, sentinel := range []error{wrapper.ErrPTYAllocation, wrapper.ErrPTYRead} {
		raw := fmt.Errorf("%w: fork/exec /usr/local/bin/claude: exec format error", sentinel)
		wrapped := wrapInvocationError(raw, "")
		ae := agenterr.ClassifyFromOutput(wrapped.Error(), 1, "claude")
		if ae == nil || !ae.Class.Is(agenterr.SpawnFailureOutcome) {
			t.Errorf("%v classified as %v, want SpawnFailure", sentinel, ae)
		}
	}
}
