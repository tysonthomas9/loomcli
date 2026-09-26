package daemon

import (
	"strings"
	"testing"
)

// The seed-* family must refuse to run without the LOOM_TESTSUPPORT gate
// (docs/adr/0001): test-only code ships in the production binary, so the env
// check is the only guard besides Hidden.
func TestSeedCommandsRequireTestSupportGate(t *testing.T) {
	t.Setenv("LOOM_TESTSUPPORT", "")
	for _, run := range map[string]func() error{
		"seed-worktree":   func() error { return runDaemonSeedWorktree(nil, nil) },
		"seed-transcript": func() error { return runDaemonSeedTranscript(nil, nil) },
	} {
		if err := run(); err == nil || !strings.Contains(err.Error(), "LOOM_TESTSUPPORT") {
			t.Fatalf("expected gate error, got %v", err)
		}
	}
}

// seedCommitFile refuses paths that escape the worktree and commits inside it.
func TestSeedCommitFileStaysInsideWorktree(t *testing.T) {
	worktree := t.TempDir()
	if err := seedCommitFile(worktree, "../escape.txt", []byte("x"), "m"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}
