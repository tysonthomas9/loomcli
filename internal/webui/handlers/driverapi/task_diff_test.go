package driverapi

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

func TestTaskDiffReturnsLocalBranchDiffFromFilesystemOrigin(t *testing.T) {
	origin, head := createLocalBranchOrigin(t, "changed\n")
	h := newTestHarness(t)
	seedTaskDiffRepo(t, h, origin)
	h.backend.epic = &workitems.IssueDetail{
		ID:          "TASK-1",
		Status:      "review",
		SourceRepo:  "source-repo",
		ExternalRef: "local-branch:loom/TASK-1@" + head,
	}

	resp, decoded := h.do(t, opRequest{
		op:      "task-diff",
		headers: h.ownerHeaders(t),
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
	h := newTestHarness(t)
	if _, err := h.store.Repos().Create(t.Context(), workspaceowner.RepoCreate{
		WorkspaceKey:  "WS",
		Name:          "source-repo",
		RemoteURL:     "https://github.com/example/source-repo.git",
		DefaultBranch: "main",
		SourceRepoID:  "source-repo",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	h.backend.epic = &workitems.IssueDetail{
		ID:          "TASK-1",
		Status:      "review",
		SourceRepo:  "source-repo",
		ExternalRef: "local-branch:loom/TASK-1@abcdef1",
	}

	resp, decoded := h.do(t, opRequest{
		op:      "task-diff",
		headers: h.ownerHeaders(t),
		body:    map[string]any{"taskId": "TASK-1"},
	})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "task_diff_origin_not_filesystem" {
		t.Fatalf("error code = %q, want task_diff_origin_not_filesystem", code)
	}
}

func TestTaskDiffReturnsLocalBranchDiffFromVerifiedWorkspaceCheckout(t *testing.T) {
	checkout, head := createLocalBranchCheckout(t, "https://github.com/example/source-repo.git", "changed locally\n")
	h := newTestHarness(t)
	if _, err := h.store.Repos().Create(t.Context(), workspaceowner.RepoCreate{
		WorkspaceKey:  "WS",
		Name:          "source-repo",
		RemoteURL:     "https://github.com/example/source-repo.git",
		DefaultBranch: "main",
		SourceRepoID:  "source-repo",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	h.module.localRepoPath = func(workspaceKey, repoName string) string {
		if workspaceKey == "WS" && repoName == "source-repo" {
			return checkout
		}
		return ""
	}
	h.backend.epic = &workitems.IssueDetail{
		ID:          "TASK-1",
		Status:      "review",
		SourceRepo:  "source-repo",
		ExternalRef: "local-branch:loom/TASK-1@" + head,
	}

	resp, decoded := h.do(t, opRequest{
		op:      "task-diff",
		headers: h.ownerHeaders(t),
		body:    map[string]any{"taskId": "TASK-1"},
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decoded)
	}
	if decoded["resolvedHead"] != head || decoded["egressMechanism"] != "workspace-checkout" {
		t.Fatalf("decoded checkout evidence = %v", decoded)
	}
	diff, _ := decoded["diff"].(string)
	if !strings.Contains(diff, "+changed locally") {
		t.Fatalf("diff = %q, want local checkout change", diff)
	}
}

func TestTaskDiffReturnsLocalBranchDiffFromWorkspaceCheckoutWithoutOrigin(t *testing.T) {
	checkout, head := createLocalBranchCheckout(t, "", "changed without origin\n")
	h := newTestHarness(t)
	if _, err := h.store.Repos().Create(t.Context(), workspaceowner.RepoCreate{
		WorkspaceKey:  "WS",
		Name:          "source-repo",
		DefaultBranch: "main",
		SourceRepoID:  "source-repo",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	h.module.localRepoPath = func(workspaceKey, repoName string) string {
		if workspaceKey == "WS" && repoName == "source-repo" {
			return checkout
		}
		return ""
	}
	h.backend.epic = &workitems.IssueDetail{
		ID:          "TASK-1",
		Status:      "review",
		SourceRepo:  "source-repo",
		ExternalRef: "local-branch:loom/TASK-1@" + head,
	}

	resp, decoded := h.do(t, opRequest{
		op:      "task-diff",
		headers: h.ownerHeaders(t),
		body:    map[string]any{"taskId": "TASK-1"},
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decoded)
	}
	if decoded["resolvedHead"] != head || decoded["baseRef"] != "main" ||
		decoded["egressMechanism"] != "workspace-checkout" {
		t.Fatalf("decoded checkout evidence = %v", decoded)
	}
	diff, _ := decoded["diff"].(string)
	if !strings.Contains(diff, "+changed without origin") {
		t.Fatalf("diff = %q, want no-origin checkout change", diff)
	}
}

func TestTaskDiffRejectsMissingConfiguredFilesystemOrigin(t *testing.T) {
	missingOrigin := filepath.Join(t.TempDir(), "missing-origin.git")
	checkout, head := createLocalBranchCheckout(t, missingOrigin, "changed locally\n")
	h := newTestHarness(t)
	if _, err := h.store.Repos().Create(t.Context(), workspaceowner.RepoCreate{
		WorkspaceKey:  "WS",
		Name:          "source-repo",
		RemoteURL:     missingOrigin,
		DefaultBranch: "main",
		SourceRepoID:  "source-repo",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	h.module.localRepoPath = func(workspaceKey, repoName string) string {
		if workspaceKey == "WS" && repoName == "source-repo" {
			return checkout
		}
		return ""
	}
	h.backend.epic = &workitems.IssueDetail{
		ID:          "TASK-1",
		Status:      "review",
		SourceRepo:  "source-repo",
		ExternalRef: "local-branch:loom/TASK-1@" + head,
	}

	resp, decoded := h.do(t, opRequest{
		op:      "task-diff",
		headers: h.ownerHeaders(t),
		body:    map[string]any{"taskId": "TASK-1"},
	})

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "task_diff_origin_missing" {
		t.Fatalf("error code = %q, want task_diff_origin_missing", code)
	}
}

func TestTaskDiffUsesRepoDefaultInsteadOfWorkspaceIsolationBranch(t *testing.T) {
	origin, head := createLocalBranchOrigin(t, "changed through local delivery\n")
	h := newTestHarness(t)
	if _, err := h.store.Workspaces().Create(t.Context(), workspaceowner.WorkspaceCreate{
		Key:           "WS",
		Name:          "WS",
		DefaultBranch: "WS",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := h.store.Repos().Create(t.Context(), workspaceowner.RepoCreate{
		WorkspaceKey:  "WS",
		Name:          "source-repo",
		RemoteURL:     origin,
		DefaultBranch: "main",
		SourceRepoID:  "source-repo",
	}); err != nil {
		t.Fatalf("create attached repo metadata: %v", err)
	}

	h.backend.epic = &workitems.IssueDetail{
		ID:          "TASK-1",
		Status:      "review",
		SourceRepo:  "source-repo",
		ExternalRef: "local-branch:loom/TASK-1@" + head,
	}
	resp, decoded := h.do(t, opRequest{
		op:      "task-diff",
		headers: h.ownerHeaders(t),
		body:    map[string]any{"taskId": "TASK-1"},
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decoded)
	}
	if decoded["baseRef"] != "main" || decoded["resolvedHead"] != head {
		t.Fatalf("decoded branch evidence = %v", decoded)
	}
	diff, _ := decoded["diff"].(string)
	if !strings.Contains(diff, "+changed through local delivery") {
		t.Fatalf("diff = %q, want attached-repo local branch change", diff)
	}
}

func TestTaskDiffRejectsWorkspaceCheckoutRemoteMismatch(t *testing.T) {
	checkout, _ := createLocalBranchCheckout(t, "https://github.com/other/repo.git", "changed\n")
	h := newTestHarness(t)
	if _, err := h.store.Repos().Create(t.Context(), workspaceowner.RepoCreate{
		WorkspaceKey:  "WS",
		Name:          "source-repo",
		RemoteURL:     "https://github.com/example/source-repo.git",
		DefaultBranch: "main",
		SourceRepoID:  "source-repo",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	h.module.localRepoPath = func(workspaceKey, repoName string) string {
		if workspaceKey == "WS" && repoName == "source-repo" {
			return checkout
		}
		return ""
	}
	h.backend.epic = &workitems.IssueDetail{
		ID:          "TASK-1",
		Status:      "review",
		SourceRepo:  "source-repo",
		ExternalRef: "local-branch:loom/TASK-1@abcdef1",
	}

	resp, decoded := h.do(t, opRequest{
		op:      "task-diff",
		headers: h.ownerHeaders(t),
		body:    map[string]any{"taskId": "TASK-1"},
	})

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "task_diff_checkout_remote_mismatch" {
		t.Fatalf("error code = %q, want task_diff_checkout_remote_mismatch", code)
	}
}

func TestTaskDiffEnforcesDiffSizeCap(t *testing.T) {
	origin, head := createLocalBranchOrigin(t, strings.Repeat("x", taskDiffMaxBytes+2048)+"\n")
	h := newTestHarness(t)
	seedTaskDiffRepo(t, h, origin)
	h.backend.epic = &workitems.IssueDetail{
		ID:          "TASK-1",
		Status:      "review",
		SourceRepo:  "source-repo",
		ExternalRef: "local-branch:loom/TASK-1@" + head,
	}

	resp, decoded := h.do(t, opRequest{
		op:      "task-diff",
		headers: h.ownerHeaders(t),
		body:    map[string]any{"taskId": "TASK-1"},
	})

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "task_diff_too_large" {
		t.Fatalf("error code = %q, want task_diff_too_large", code)
	}
}

func createLocalBranchCheckout(t *testing.T, remoteURL, branchContent string) (string, string) {
	t.Helper()
	worktree := filepath.Join(t.TempDir(), "work")
	runGit(t, "", "init", worktree)
	runGit(t, worktree, "checkout", "-B", "main")
	runGit(t, worktree, "config", "user.email", "loom@example.test")
	runGit(t, worktree, "config", "user.name", "Loom Test")
	if err := os.WriteFile(filepath.Join(worktree, "file.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	runGit(t, worktree, "add", "file.txt")
	runGit(t, worktree, "commit", "-m", "base")
	if strings.TrimSpace(remoteURL) != "" {
		runGit(t, worktree, "remote", "add", "origin", remoteURL)
	}
	runGit(t, worktree, "checkout", "-B", "loom/TASK-1")
	if err := os.WriteFile(filepath.Join(worktree, "file.txt"), []byte(branchContent), 0o644); err != nil {
		t.Fatalf("write branch file: %v", err)
	}
	runGit(t, worktree, "add", "file.txt")
	runGit(t, worktree, "commit", "-m", "task change")
	return worktree, strings.TrimSpace(runGit(t, worktree, "rev-parse", "HEAD"))
}

func seedTaskDiffRepo(t *testing.T, h *testHarness, origin string) {
	t.Helper()
	if _, err := h.store.Repos().Create(t.Context(), workspaceowner.RepoCreate{
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
	cmd := exec.Command("git", fullArgs...) //nolint:norawexec,gosec // Test helper invokes fixed git commands with temp paths.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(fullArgs, " "), err, out)
	}
	return string(out)
}
