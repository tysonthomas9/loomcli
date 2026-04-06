package hooks

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// TerminalManager → terminal.TerminalManager
type TerminalManager = terminal.TerminalManager

// NewTerminalManager → terminal.NewTerminalManager
var NewTerminalManager = terminal.NewTerminalManager

// MultiWorkspaceSubscriber → subscription.MultiWorkspaceSubscriber
type MultiWorkspaceSubscriber = subscription.MultiWorkspaceSubscriber

// NewMultiWorkspaceSubscriber → subscription.NewMultiWorkspaceSubscriber
var NewMultiWorkspaceSubscriber = subscription.NewMultiWorkspaceSubscriber

// ---------------------------------------------------------------------------
// Test helpers (duplicated from terminal/manager_test.go, since test files
// cannot be imported across packages)
// ---------------------------------------------------------------------------

// testRunPrefix scopes all tmux session names to this test process.
var testRunPrefix = fmt.Sprintf("tr%d", os.Getpid())

// skipIfNoTmux skips the test if tmux is not installed.
func skipIfNoTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available, skipping test")
	}
}

// killTmuxSession is a cleanup helper that kills a tmux session by name.
func killTmuxSession(t *testing.T, name string) {
	t.Helper()
	cmd := exec.Command("tmux", "kill-session", "-t", name) //nolint:norawexec
	_ = cmd.Run()
}

// tmuxSessionExists checks whether a tmux session with the given name exists.
func tmuxSessionExists(name string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", name) //nolint:norawexec
	return cmd.Run() == nil
}
