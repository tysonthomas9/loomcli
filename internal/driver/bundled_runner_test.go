package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fakeCodexBackend is a stand-in codex CLI: it drains stdin (the prompt) then emits
// codex `exec --json` stream-json with a turn.completed usage event, so the bundled
// local-task-runner produces a real transcript + usage without a live model.
const fakeCodexBackend = `#!/usr/bin/env node
let buf = "";
process.stdin.on("data", (c) => { buf += c; });
process.stdin.on("end", () => {
  process.stdout.write(JSON.stringify({ type: "item.completed", item: { type: "agent_message", text: "hi from fake" } }) + "\n");
  process.stdout.write(JSON.stringify({ type: "turn.completed", usage: { input_tokens: 50, output_tokens: 5, cached_input_tokens: 2 } }) + "\n");
  process.exit(0);
});
`

// TestRunBundledTaskRunner_RealBundle (Phase U / U1, increment 2) proves the
// core delegation mechanism end-to-end LOCALLY: it runs the actual committed bundle's
// local-task-runner against a real git worktree + a fake codex backend, and asserts
// the runner returns a transcript + top-level usage and tees the backend's live output
// to the caller's stderr (the supervisor watchdog feed). No podman required.
func TestRunBundledTaskRunner_RealBundle(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	serverPath := committedBundleServerPath(t)

	tmp := t.TempDir()
	worktree := filepath.Join(tmp, "wt")
	newGitWorktree(t, worktree)

	fakeBin := filepath.Join(tmp, "fake-codex.mjs")
	if err := os.WriteFile(fakeBin, []byte(fakeCodexBackend), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_CODEX_BIN", fakeBin)

	reqJSON, _ := json.Marshal(map[string]any{
		"task_run_id":   "tr-test",
		"task_id":       "T-1",
		"backend":       "codex",
		"workspace_key": "ws",
		"lease_token":   "",
		"input":         map[string]any{"title": "do the thing"},
	})

	var serr bytes.Buffer
	raw, err := RunBundledTaskRunner(context.Background(), BundledRunnerOptions{
		ServerPath:   serverPath,
		Worktree:     worktree,
		Backend:      "codex",
		RequestJSON:  string(reqJSON),
		LeaseToken:   "",
		StreamStderr: true,
		Stderr:       &serr,
	})
	if err != nil {
		t.Fatalf("RunBundledTaskRunner: %v\n--- stderr ---\n%s", err, serr.String())
	}

	var result bundledResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode result: %v\nraw: %s", err, raw)
	}
	if result.Status != "completed" {
		t.Errorf("status = %q, want completed; raw: %s", result.Status, raw)
	}
	if len(result.TranscriptEntries) == 0 {
		t.Errorf("no transcript_entries in result: %s", raw)
	}
	// The fix this whole phase rests on: the bundled runner surfaces top-level usage.
	if result.InputTokens != 50 || result.OutputTokens != 5 || result.CacheReadTokens != 2 {
		t.Errorf("usage = in:%d out:%d cacheRead:%d, want 50/5/2; raw: %s", result.InputTokens, result.OutputTokens, result.CacheReadTokens, raw)
	}
	// The backend's live output must reach the caller's stderr (the watchdog feed).
	if !bytes.Contains(serr.Bytes(), []byte("hi from fake")) {
		t.Errorf("backend output not teed to caller stderr (watchdog feed); stderr:\n%s", serr.String())
	}
}
