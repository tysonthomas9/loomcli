package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestRunBundledRunner_DaytonaEntrypointSwitch proves a standalone daemon leaf
// cannot bypass the lease-authenticated TaskRun facade to reach Daytona.
func TestRunBundledRunner_DaytonaEntrypointSwitch(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	serverPath := committedBundleServerPath(t)
	tmp := t.TempDir()
	reqJSON, _ := json.Marshal(map[string]any{
		"task_run_id":   "tr-daytona-switch",
		"task_id":       "T-DAY",
		"backend":       "codex",
		"workspace_key": "ws",
		"lease_token":   "", // must match the empty LeaseToken so the launcher gate passes
		"input":         map[string]any{},
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
	// The decisive proof: the provider-blind runner refused the invocation
	// before any provider call because the daemon leaf has no TaskRun lease
	// facade. A local-task-runner would never emit this error class.
	if !strings.Contains(string(raw), "daytona_task_context_failed") {
		t.Fatalf("expected the TaskRun-authenticated Daytona boundary; got: %s", raw)
	}
}
