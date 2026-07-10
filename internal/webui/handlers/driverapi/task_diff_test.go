package driverapi

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestTaskDiffReturnsLocalBranchDiffFromFilesystemOrigin(t *testing.T) {
	origin, head := createLocalBranchOrigin(t, "changed\n")
	h := newTestHarness(t, "")
	seedTaskDiffRepo(t, h, origin)
	h.backend.epic = &backend.IssueDetailData{IssueData: backend.IssueData{
		ID:          "TASK-1",
		Status:      "review",
		SourceRepo:  "source-repo",
		ExternalRef: "local-branch:loom/TASK-1@" + head,
	}}

	resp, decoded := h.do(t, opRequest{
		op:      "task-diff",
		headers: h.ownerHeaders(),
		body:    map[string]any{"taskId": "TASK-1"},
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decoded)
	}
	if decoded["taskId"] != "TASK-1" || decoded["branch"] != "loom/TASK-1" || decoded["baseRef"] != "main" {
		t.Fatalf("decoded identity fields = %v", decoded)
	}
	if decoded["resolvedHead"] != head {
		t.Fatalf("resolvedHead = %v, want %s", decoded["resolvedHead"], head)
	}
	diff, _ := decoded["diff"].(string)
	if !strings.Contains(diff, "+changed") {
		t.Fatalf("diff = %q, want local branch change", diff)
	}
	if decoded["limitBytes"] != float64(taskDiffMaxBytes) {
		t.Fatalf("limitBytes = %v, want %d", decoded["limitBytes"], taskDiffMaxBytes)
	}
}

func TestTaskDiffRejectsNonFilesystemOrigin(t *testing.T) {
	h := newTestHarness(t, "")
	if _, err := h.store.Repos().Create(t.Context(), store.RepoCreate{
		WorkspaceKey:  "WS",
		Name:          "source-repo",
		RemoteURL:     "https://github.com/example/source-repo.git",
		DefaultBranch: "main",
		SourceRepoID:  "source-repo",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	h.backend.epic = &backend.IssueDetailData{IssueData: backend.IssueData{
		ID:          "TASK-1",
		Status:      "review",
		SourceRepo:  "source-repo",
		ExternalRef: "local-branch:loom/TASK-1@abcdef1",
	}}

	resp, decoded := h.do(t, opRequest{
		op:      "task-diff",
		headers: h.ownerHeaders(),
		body:    map[string]any{"taskId": "TASK-1"},
	})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "task_diff_origin_not_filesystem" {
		t.Fatalf("error code = %q, want task_diff_origin_not_filesystem", code)
	}
}

func TestTaskDiffEnforcesDiffSizeCap(t *testing.T) {
	origin, head := createLocalBranchOrigin(t, strings.Repeat("x", taskDiffMaxBytes+2048)+"\n")
	h := newTestHarness(t, "")
	seedTaskDiffRepo(t, h, origin)
	h.backend.epic = &backend.IssueDetailData{IssueData: backend.IssueData{
		ID:          "TASK-1",
		Status:      "review",
		SourceRepo:  "source-repo",
		ExternalRef: "local-branch:loom/TASK-1@" + head,
	}}

	resp, decoded := h.do(t, opRequest{
		op:      "task-diff",
		headers: h.ownerHeaders(),
		body:    map[string]any{"taskId": "TASK-1"},
	})

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "task_diff_too_large" {
		t.Fatalf("error code = %q, want task_diff_too_large", code)
	}
}

func seedTaskDiffRepo(t *testing.T, h *testHarness, origin string) {
	t.Helper()
	if _, err := h.store.Repos().Create(t.Context(), store.RepoCreate{
		WorkspaceKey:  "WS",
		Name:          "source-repo",
		RemoteURL:     origin,
		DefaultBranch: "main",
		SourceRepoID:  "source-repo",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
}

func createLocalBranchOrigin(t *testing.T, branchContent string) (string, string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	worktree := filepath.Join(root, "work")
	runGit(t, "", "init", "--bare", origin)
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	runGit(t, "", "init", worktree)
	runGit(t, worktree, "checkout", "-B", "main")
	runGit(t, worktree, "config", "user.email", "loom@example.test")
	runGit(t, worktree, "config", "user.name", "Loom Test")
	if err := os.WriteFile(filepath.Join(worktree, "file.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	runGit(t, worktree, "add", "file.txt")
	runGit(t, worktree, "commit", "-m", "base")
	runGit(t, worktree, "remote", "add", "origin", origin)
	runGit(t, worktree, "push", "origin", "main")
	runGit(t, worktree, "checkout", "-B", "loom/TASK-1")
	if err := os.WriteFile(filepath.Join(worktree, "file.txt"), []byte(branchContent), 0o644); err != nil {
		t.Fatalf("write branch file: %v", err)
	}
	runGit(t, worktree, "add", "file.txt")
	runGit(t, worktree, "commit", "-m", "task change")
	head := strings.TrimSpace(runGit(t, worktree, "rev-parse", "HEAD"))
	runGit(t, worktree, "push", "origin", "HEAD:refs/heads/loom/TASK-1")
	return origin, head
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	fullArgs := args
	if strings.TrimSpace(dir) != "" {
		fullArgs = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", fullArgs...) //nolint:gosec // test helper invokes fixed git commands with temp paths. //nolint:norawexec
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(fullArgs, " "), err, out)
	}
	return string(out)
}
