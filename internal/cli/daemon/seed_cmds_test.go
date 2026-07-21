package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The seed-* family must refuse to run without the LOOM_TESTSUPPORT gate
// (docs/adr/0001): test-only code ships in the production binary, so the env
// check is the only guard besides Hidden.
func TestSeedCommandsRequireTestSupportGate(t *testing.T) {
	t.Setenv("LOOM_TESTSUPPORT", "")
	for _, run := range map[string]func() error{
		"seed-log":        func() error { return runDaemonSeedLog(nil, nil) },
		"seed-worktree":   func() error { return runDaemonSeedWorktree(nil, nil) },
		"seed-transcript": func() error { return runDaemonSeedTranscript(nil, nil) },
	} {
		if err := run(); err == nil || !strings.Contains(err.Error(), "LOOM_TESTSUPPORT") {
			t.Fatalf("expected gate error, got %v", err)
		}
	}
}

// seed-log writes through cliagent.OpenAgentArchiveLog — the same
// writer/resolver pair the supervisor and the web UI Logs tab use — so the
// seeded file must land at the runtime's own path and append, not truncate.
func TestSeedLogWritesViaProductResolver(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	t.Setenv("LOOM_TESTSUPPORT", "1")

	content := filepath.Join(t.TempDir(), "log.txt")
	if err := os.WriteFile(content, []byte("line one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedLogWorkspace = "WS-SEED"
	seedLogAgent = "nova"
	seedLogFile = content
	t.Cleanup(func() { seedLogWorkspace, seedLogAgent, seedLogFile = "", "", "" })

	if err := runDaemonSeedLog(nil, nil); err != nil {
		t.Fatalf("first seed-log: %v", err)
	}
	if err := os.WriteFile(content, []byte("line two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runDaemonSeedLog(nil, nil); err != nil {
		t.Fatalf("second seed-log: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(runtimeDir, ".loom", "logs", "WS-SEED", "agents", "nova.log"))
	if err != nil {
		t.Fatalf("seeded log not at the runtime resolver's path: %v", err)
	}
	if string(got) != "line one\nline two\n" {
		t.Fatalf("expected appended content, got %q", string(got))
	}
}

// seedCommitFile refuses paths that escape the worktree and commits inside it.
func TestSeedCommitFileStaysInsideWorktree(t *testing.T) {
	worktree := t.TempDir()
	if err := seedCommitFile(worktree, "../escape.txt", []byte("x"), "m"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}
