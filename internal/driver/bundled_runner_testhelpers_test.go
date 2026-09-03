package driver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// bundledResult is the shape RunBundledTaskRunner returns. The tags match what
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
	Patch             string            `json:"patch"`
}

// newGitWorktree initializes a throwaway git repo in dir with one commit, so the
// bundled runner can `git worktree add` from it.
func newGitWorktree(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "init", "-q")
	gitCmd(t, dir, "config", "user.email", "t@example.test")
	gitCmd(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "init")
}

// committedBundleServerPath resolves the committed builtin bundle's server.mjs and skips
// the test if it's absent (run scripts/rebuild-builtin-bundle.sh to produce it).
func committedBundleServerPath(t *testing.T) string {
	t.Helper()
	serverPath, err := filepath.Abs(filepath.Join("..", "workflows", "builtin-dist", "epic-runner", "dist", "server.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(serverPath); err != nil {
		t.Skipf("committed bundle not present (%v); run scripts/rebuild-builtin-bundle.sh", err)
	}
	return serverPath
}

// tailStr returns the last n bytes of s, prefixed with "..." when truncated.
func tailStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
