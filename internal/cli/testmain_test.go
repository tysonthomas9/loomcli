package cli

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	killTestTmuxSessions()
	code := m.Run()
	killTestTmuxSessions()
	os.Exit(code)
}

// killTestTmuxSessions kills all tmux sessions with loom-test- or loom-e2e-test- prefixes.
// This cleans up zombie sessions left by crashed or timed-out test runs.
func killTestTmuxSessions() {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output() //nolint:norawexec
	if err != nil {
		return // tmux not installed or no server running
	}
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "loom-test-") || strings.HasPrefix(name, "loom-e2e-test-") {
			_ = exec.Command("tmux", "kill-session", "-t", name).Run() //nolint:norawexec
		}
	}
}
