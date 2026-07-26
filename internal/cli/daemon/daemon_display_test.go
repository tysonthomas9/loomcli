package daemon

import (
	"os"
	"strings"
	"testing"
)

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it. Mirrors the os.Pipe capture pattern used in
// internal/cli/monitor/monitor_test.go.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = oldStdout

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	return string(buf[:n])
}

// ---------------------------------------------------------------------------
// printAgentDiagnostics — NoWork(spawned) line (DOGFOOD-36)
// ---------------------------------------------------------------------------

func TestPrintAgentDiagnostics_NoWorkSpawnCount_Printed(t *testing.T) {
	agent := DaemonAgentStatus{
		Worktree:         "critic",
		Role:             "critic",
		NoWorkSpawnCount: 3,
	}

	output := captureStdout(t, func() { printAgentDiagnostics(agent) })

	if !strings.Contains(output, "NoWork(spawned): 3") {
		t.Errorf("expected output to contain %q, got: %q", "NoWork(spawned): 3", output)
	}
}

func TestPrintAgentDiagnostics_NoWorkSpawnCount_ZeroOmitted(t *testing.T) {
	agent := DaemonAgentStatus{
		Worktree:         "critic",
		Role:             "critic",
		NoWorkSpawnCount: 0,
	}

	output := captureStdout(t, func() { printAgentDiagnostics(agent) })

	if strings.Contains(output, "NoWork(spawned)") {
		t.Errorf("expected NoWork(spawned) line to be omitted when count is 0, got: %q", output)
	}
}

func TestPrintAgentDiagnostics_NoWorkCountAndSpawnCount_BothPrinted(t *testing.T) {
	// NoWorkCount (all NoWork exits) and NoWorkSpawnCount (post-spawn subset)
	// are independent counters and can both be non-zero at once.
	agent := DaemonAgentStatus{
		Worktree:         "critic",
		Role:             "critic",
		NoWorkCount:      8,
		NoWorkSpawnCount: 3,
	}

	output := captureStdout(t, func() { printAgentDiagnostics(agent) })

	if !strings.Contains(output, "NoWork: 8") {
		t.Errorf("expected output to contain %q, got: %q", "NoWork: 8", output)
	}
	if !strings.Contains(output, "NoWork(spawned): 3") {
		t.Errorf("expected output to contain %q, got: %q", "NoWork(spawned): 3", output)
	}
}
