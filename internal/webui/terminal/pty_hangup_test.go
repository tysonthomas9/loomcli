package terminal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A killed session hangs up its PTY and lets the child finish its own cleanup
// before it is killed: `loom lead` stops its codex app-server on SIGHUP, and
// an immediate SIGKILL orphaned it (B1).
func TestKillLetsHungUpChildCleanUp(t *testing.T) {
	m := newTestManager(t)
	key := SessionKey{Workspace: "ws1", Name: "lead"}
	marker := filepath.Join(t.TempDir(), "cleaned-up")
	ready := filepath.Join(t.TempDir(), "ready")
	script := `trap 'sleep 0.5; touch "$MARKER"; exit 0' HUP; touch "$READY"; while :; do sleep 0.1; done`
	launch := &LaunchSpec{Argv: []string{"-c", script}, Env: map[string]string{"MARKER": marker, "READY": ready}}
	if _, _, err := m.AttachSession(key, 80, 24, launch); err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	waitUntil(t, func() bool { _, err := os.Stat(ready); return err == nil }, 5*time.Second, "child installed its hangup trap")

	if err := m.Kill(key); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("child was killed before its hangup cleanup finished: %v", err)
	}
}
