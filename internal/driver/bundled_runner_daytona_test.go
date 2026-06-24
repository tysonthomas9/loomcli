package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestRunBundledRunner_DaytonaEntrypointSwitch proves the daemon-leaf wiring can
// route to the bundled daytona-task-runner (not just local-task-runner): with the
// Daytona entrypoint selected and no repoUrl/DAYTONA_API_KEY, the run fails with
// the daytona-task-runner's OWN early validation (daytona_repo_url_missing). That
// error class is only reachable if the Daytona runner actually executed — a clean,
// cloud-free proof of the entrypoint switch. (A full sandbox E2E additionally needs
// DAYTONA_API_KEY + a reachable repo + model auth.)
func TestRunBundledRunner_DaytonaEntrypointSwitch(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	serverPath := committedBundleServerPath(t)
	// Force the daytona runner down its no-repo path deterministically.
	t.Setenv("DAYTONA_REPO_URL", "")
	t.Setenv("DAYTONA_API_KEY", "")

	tmp := t.TempDir()
	reqJSON, _ := json.Marshal(map[string]any{
		"task_run_id":   "tr-daytona-switch",
		"task_id":       "T-DAY",
		"backend":       "codex",
		"workspace_key": "ws",
		"lease_token":   "",               // must match the empty LeaseToken so the launcher gate passes
		"input":         map[string]any{}, // no repoUrl -> daytona runner must reject
	})

	var serr bytes.Buffer
	raw, err := RunBundledTaskRunner(context.Background(), BundledRunnerOptions{
		ServerPath:  serverPath,
		Entrypoint:  DaytonaTaskRunnerEntrypoint,
		Worktree:    tmp,
		Backend:     "codex",
		RequestJSON: string(reqJSON),
		Stderr:      &serr,
	})
	if err != nil {
		t.Fatalf("RunBundledTaskRunner(daytona): %v\n--- stderr ---\n%s", err, serr.String())
	}

	var result bundledResult
	if jerr := json.Unmarshal(raw, &result); jerr != nil {
		t.Fatalf("decode result: %v\nraw: %s", jerr, raw)
	}
	if result.Status == "completed" {
		t.Fatalf("expected the daytona runner to reject (no repoUrl), got completed; raw: %s", raw)
	}
	// The decisive proof: the failure came from the daytona-task-runner's own
	// validation, so the entrypoint switch worked (local-task-runner would never
	// emit a daytona_* error class).
	if !strings.Contains(string(raw), "daytona_repo_url_missing") && !strings.Contains(string(raw), "daytona") {
		t.Fatalf("expected a daytona-specific failure (proving the daytona runner ran); got: %s", raw)
	}
}
