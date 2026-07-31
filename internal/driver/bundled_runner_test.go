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

func TestBuildLeafRunnerEnvFiltersForgeAndControlPlaneCredentials(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "forge-secret")
	t.Setenv("GH_TOKEN", "forge-secret")
	t.Setenv("LOOM_FLEET_DB_API_KEY", "fleet-secret")
	t.Setenv("LOOM_AGENT_LEASE_TOKEN", "agent-secret")
	t.Setenv("OPENAI_API_KEY", "model-secret")
	t.Setenv("PATH", "/usr/bin")

	got := envMap(buildLeafRunnerEnv(BundledRunnerOptions{
		ServerPath:  "/tmp/bundle/server.mjs",
		Worktree:    "/tmp/worktree",
		Backend:     "codex",
		LeaseToken:  "task-scoped-token",
		RequestJSON: `{}`,
	}, "workflows/local-task-runner.ts", `{}`))

	for _, key := range []string{"GITHUB_TOKEN", "GH_TOKEN", "LOOM_FLEET_DB_API_KEY", "LOOM_AGENT_LEASE_TOKEN"} {
		if _, present := got[key]; present {
			t.Fatalf("%s reached bundled leaf environment: %#v", key, got)
		}
	}
	if got["OPENAI_API_KEY"] != "model-secret" {
		t.Fatalf("AI provider credential missing from local backend leaf: %#v", got)
	}
	if got["LOOM_TASK_RUN_LEASE_TOKEN"] != "task-scoped-token" {
		t.Fatalf("task-scoped credential missing from leaf envelope: %#v", got)
	}
}

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
// the runner returns a transcript + top-level usage and emits a credential-safe
// activity signal to the caller's stderr (the supervisor watchdog feed). No podman
// required.
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
	// The watchdog needs proof of backend activity, never raw backend bytes: a
	// credential can span arbitrary stream chunks and cannot be redacted safely
	// while live-streaming.
	if !bytes.Contains(serr.Bytes(), []byte("[loom task-runner] backend activity")) {
		t.Errorf("backend activity signal did not reach caller stderr (watchdog feed); stderr:\n%s", serr.String())
	}
	if bytes.Contains(serr.Bytes(), []byte("hi from fake")) {
		t.Errorf("raw backend output leaked to caller stderr; stderr:\n%s", serr.String())
	}
}
