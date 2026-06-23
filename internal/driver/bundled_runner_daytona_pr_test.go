package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunBundledRunner_DaytonaLivePR runs ONE real code task end-to-end through the
// Daytona task runner against a real GitHub repo, and opens a real PR. Gated +
// skipped by default (real sandbox + model + an outward-facing PR):
//
//	export DAYTONA_API_KEY=...     # aether-test-framework/.env
//	export DAYTONA_PR_REPO_URL=https://github.com/tysonthomas9/loomcli.git
//	LOOM_LIVE_DAYTONA_PR=1 go test ./internal/driver/ -run DaytonaLivePR -v -count=1 -timeout 1800s
//
// The GitHub token comes from `gh auth token` (staged to GITHUB_TOKEN_FILE — never a
// raw env secret). The task is a small new-doc change (taskPrompt input), so the PR
// is clean and easy to close.
func TestRunBundledRunner_DaytonaLivePR(t *testing.T) {
	if os.Getenv("LOOM_LIVE_DAYTONA_PR") == "" {
		t.Skip("set LOOM_LIVE_DAYTONA_PR=1 (+ DAYTONA_API_KEY, DAYTONA_PR_REPO_URL) to run")
	}
	if os.Getenv("DAYTONA_API_KEY") == "" {
		t.Skip("DAYTONA_API_KEY not set")
	}
	repoURL := os.Getenv("DAYTONA_PR_REPO_URL")
	if repoURL == "" {
		t.Skip("DAYTONA_PR_REPO_URL not set")
	}
	for _, bin := range []string{"node", "gh", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	serverPath, err := filepath.Abs(filepath.Join("..", "workflows", "builtin-dist", "epic-runner", "dist", "server.mjs"))
	if err != nil {
		t.Fatal(err)
	}

	tmp := t.TempDir()
	// Stage the Daytona key + a gh token as files (the runner reads files, not raw env).
	keyFile := filepath.Join(tmp, "daytona_key")
	if err := os.WriteFile(keyFile, []byte(os.Getenv("DAYTONA_API_KEY")), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DAYTONA_CREDENTIAL_FILE", keyFile)

	ghTokOut, err := exec.Command("gh", "auth", "token").Output()
	if err != nil || len(bytes.TrimSpace(ghTokOut)) == 0 {
		t.Skipf("could not get a gh token: %v", err)
	}
	tokFile := filepath.Join(tmp, "gh_token")
	if err := os.WriteFile(tokFile, bytes.TrimSpace(ghTokOut), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN_FILE", tokFile)
	t.Setenv("DAYTONA_REPO_URL", repoURL)
	// The host-side flue codex agent configures its model auth from the codex auth
	// file (CODEX_HOME/auth.json). Without it the agent can't run -> 0 transcript
	// entries -> no edit. The stack gets this from the mounted /root/.codex.
	if home := os.Getenv("HOME"); home != "" {
		t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
		t.Setenv("LOOM_CODEX_AUTH_FILE", filepath.Join(home, ".codex", "auth.json"))
	}
	t.Setenv("DAYTONA_CLONE_TIMEOUT_SECONDS", "600")
	t.Setenv("DAYTONA_AUTO_STOP_MINUTES", "10")
	t.Setenv("DAYTONA_AUTO_DELETE_MINUTES", "30")

	taskPrompt := "Make exactly ONE change: create a new file at path `docs/daytona-runner-e2e.md` " +
		"(create the docs/ directory if needed) containing EXACTLY this content:\n\n" +
		"# Daytona Runner E2E\n\n" +
		"This file was created automatically by the Loom Daytona task runner as an end-to-end test " +
		"that the runner can edit real code and open a pull request.\n\n" +
		"Do not read, list, or modify any other files. Do not explore the repository. " +
		"Just write that one file using your file-edit tool, then stop."
	reqJSON, _ := json.Marshal(map[string]any{
		"task_run_id":   "daytona-pr-e2e",
		"task_id":       "DAYTONA-PR-E2E",
		"backend":       "codex",
		"workspace_key": "ws",
		"lease_token":   "",
		"input": map[string]any{
			"repoUrl":         repoURL,
			"openPullRequest": true,
			"taskPrompt":      taskPrompt,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 28*time.Minute)
	defer cancel()

	var serr bytes.Buffer
	raw, err := RunBundledLocalTaskRunner(ctx, BundledRunnerOptions{
		ServerPath:   serverPath,
		Entrypoint:   DaytonaTaskRunnerEntrypoint,
		Worktree:     tmp,
		Backend:      "codex",
		RequestJSON:  string(reqJSON),
		StreamStderr: true,
		Stderr:       &serr,
	})
	if err != nil {
		t.Fatalf("RunBundledLocalTaskRunner(daytona PR): %v\n--- stderr tail ---\n%s", err, tailStr(serr.String(), 5000))
	}

	var result struct {
		Status          string         `json:"status"`
		ErrorClass      string         `json:"error_class"`
		ErrorMessage    string         `json:"error_message"`
		RuntimeMetadata map[string]any `json:"runtime_metadata"`
	}
	_ = os.WriteFile("/tmp/daytona-pr-result.json", raw, 0o644)
	if jerr := json.Unmarshal(raw, &result); jerr != nil {
		t.Fatalf("decode result: %v\nraw tail: %s", jerr, tailStr(string(raw), 3000))
	}
	t.Logf("[daytona-pr] status=%q sandbox=%v\n  PR-related metadata: %s",
		result.Status, result.RuntimeMetadata["daytona_sandbox_id"], prMetadata(result.RuntimeMetadata))

	if result.Status != "completed" {
		t.Fatalf("status=%q class=%q msg=%q; raw tail: %s", result.Status, result.ErrorClass, result.ErrorMessage, tailStr(string(raw), 3500))
	}
}

func prMetadata(m map[string]any) string {
	if m == nil {
		return "{}"
	}
	parts := []string{}
	for _, k := range []string{"pull_request", "pull_request_url", "pr_url", "branch", "head_branch", "commit_sha", "daytona_repo_url"} {
		if v, ok := m[k]; ok {
			parts = append(parts, k+"="+strings.TrimSpace(toStr(v)))
		}
	}
	return "{" + strings.Join(parts, " ") + "}"
}

func toStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
