package terminal

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A PTY child routinely spawns its own children — `loom lead` starts
// `codex app-server`, a billed model process. Killing only the direct child left
// that grandchild running after the terminal was gone, because SIGKILL cannot be
// trapped so the parent's cleanup never ran. This pins that the whole process
// group dies with the session.
func TestKillSession_KillsGrandchildProcess(t *testing.T) {
	m := newTestManager(t)
	key := SessionKey{Workspace: "ws1", Name: "tree"}

	// The shell backgrounds a long-lived grandchild, records its pid, then blocks.
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	script := "sleep 300 & echo $! > " + pidFile + "; wait"

	if _, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", script}}); err != nil {
		t.Fatalf("AttachSession: %v", err)
	}

	var grandchild int
	for i := 0; i < 100; i++ {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil && pid > 0 {
				grandchild = pid
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if grandchild == 0 {
		t.Fatal("grandchild never reported its pid")
	}
	if err := syscall.Kill(grandchild, 0); err != nil {
		t.Fatalf("grandchild %d should be alive before the kill: %v", grandchild, err)
	}

	if err := m.Kill(key); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	// Signal 0 only probes existence. Give the OS a moment to reap.
	alive := true
	for i := 0; i < 100; i++ {
		if err := syscall.Kill(grandchild, 0); err != nil {
			alive = false
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if alive {
		_ = syscall.Kill(grandchild, syscall.SIGKILL) // do not leak it out of the test
		t.Fatalf("grandchild %d survived the session kill — the process group was not killed", grandchild)
	}
}
