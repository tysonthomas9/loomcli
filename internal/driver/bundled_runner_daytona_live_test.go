package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestRunBundledRunner_DaytonaLive runs the bundled daytona-task-runner against a
// REAL Daytona sandbox via the same path the daemon leaf uses, proving the .ts agent
// executes inside Daytona. Gated + skipped by default (real sandbox + model spend):
//
//	export DAYTONA_API_KEY=...            # from aether-test-framework/.env
//	export DAYTONA_REPO_URL=https://github.com/octocat/Hello-World.git
//	LOOM_LIVE_DAYTONA=1 go test ./internal/driver/ -run DaytonaLive -v -count=1 -timeout 900s
//
// Uses DAYTONA_TASK_MODE=e2e-smoke: the sandbox agent writes a marker file + runs
// git status (self-contained; no loom CLI needed in the sandbox). Host-side model
// auth (e.g. ~/.codex) is used by the host harness.
func TestRunBundledRunner_DaytonaLive(t *testing.T) {
	if os.Getenv("LOOM_LIVE_DAYTONA") == "" {
		t.Skip("set LOOM_LIVE_DAYTONA=1 (+ DAYTONA_API_KEY, DAYTONA_REPO_URL) to run this live test")
	}
	if os.Getenv("DAYTONA_API_KEY") == "" {
		t.Skip("DAYTONA_API_KEY not set")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	serverPath := committedBundleServerPath(t)
	if os.Getenv("DAYTONA_REPO_URL") == "" {
		t.Setenv("DAYTONA_REPO_URL", "https://github.com/octocat/Hello-World.git")
	}
	t.Setenv("DAYTONA_TASK_MODE", "e2e-smoke")
	t.Setenv("LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES", "1") // e2e-smoke is a gated demo path
	t.Setenv("DAYTONA_AUTO_STOP_MINUTES", "10")

	tmp := t.TempDir()
	// The daytona runner resolves its key via loom's runtime credential API (needs a
	// driver baseUrl) or DAYTONA_CREDENTIAL_FILE. Standalone, use the file fallback.
	keyFile := filepath.Join(tmp, "daytona_key")
	if err := os.WriteFile(keyFile, []byte(os.Getenv("DAYTONA_API_KEY")), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DAYTONA_CREDENTIAL_FILE", keyFile)

	reqJSON, _ := json.Marshal(map[string]any{
		"task_run_id":   "tr-daytona-live",
		"task_id":       "T-DAY-LIVE",
		"backend":       "codex",
		"workspace_key": "ws",
		"lease_token":   "",
		"input":         map[string]any{"mode": "e2e-smoke", "repoUrl": os.Getenv("DAYTONA_REPO_URL")},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()

	var serr bytes.Buffer
	raw, err := RunBundledTaskRunner(ctx, BundledRunnerOptions{
		ServerPath:   serverPath,
		Entrypoint:   DaytonaTaskRunnerEntrypoint,
		Worktree:     tmp,
		Backend:      "codex",
		RequestJSON:  string(reqJSON),
		LeaseToken:   "",
		StreamStderr: true,
		Stderr:       &serr,
	})
	if err != nil {
		t.Fatalf("RunBundledTaskRunner(daytona live): %v\n--- stderr (tail) ---\n%s", err, tailStr(serr.String(), 4000))
	}

	var result bundledResult
	if jerr := json.Unmarshal(raw, &result); jerr != nil {
		t.Fatalf("decode result: %v\nraw: %s", jerr, tailStr(string(raw), 2000))
	}
	t.Logf("[daytona-live] status=%q sandbox_id=%v entries=%d usage in=%d out=%d",
		result.Status, result.RuntimeMetadata["daytona_sandbox_id"], len(result.TranscriptEntries),
		result.InputTokens, result.OutputTokens)

	if result.Status != "completed" {
		t.Fatalf("status=%q class=%q msg=%q; raw tail: %s", result.Status, result.ErrorClass, result.ErrorMessage, tailStr(string(raw), 2500))
	}
	if result.RuntimeMetadata["sandbox_provider"] != "daytona" {
		t.Errorf("expected sandbox_provider=daytona, metadata: %v", result.RuntimeMetadata)
	}
	if sid, _ := result.RuntimeMetadata["daytona_sandbox_id"].(string); sid == "" {
		t.Errorf("no daytona_sandbox_id in metadata (did a real sandbox provision?): %v", result.RuntimeMetadata)
	}
	if len(result.TranscriptEntries) == 0 {
		t.Errorf("no transcript_entries from the sandbox run")
	}
}
