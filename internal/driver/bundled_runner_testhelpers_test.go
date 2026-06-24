package driver

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// bundledResult is the shape RunBundledLocalTaskRunner returns. The tags match what
// the flue runners actually emit: errorClass/errorMessage are camelCase (the
// runner's failed() output), while usage, transcript_entries, and runtime_metadata
// are snake_case. Shared by the bundled-runner tests so the tags stay consistent.
type bundledResult struct {
	Status            string            `json:"status"`
	ErrorClass        string            `json:"errorClass"`
	ErrorMessage      string            `json:"errorMessage"`
	InputTokens       int64             `json:"input_tokens"`
	OutputTokens      int64             `json:"output_tokens"`
	CacheReadTokens   int64             `json:"cache_read_tokens"`
	CacheWriteTokens  int64             `json:"cache_write_tokens"`
	EstimatedCostUSD  float64           `json:"estimated_cost_usd"`
	TranscriptEntries []json.RawMessage `json:"transcript_entries"`
	RuntimeMetadata   map[string]any    `json:"runtime_metadata"`
}

// newGitWorktree initializes a throwaway git repo in dir with one commit, so the
// bundled runner can `git worktree add` from it.
func newGitWorktree(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@example.test")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "init")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// tailStr returns the last n bytes of s, prefixed with "..." when truncated.
func tailStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
