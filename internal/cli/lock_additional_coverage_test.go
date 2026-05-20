package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func writeLockInfo(t *testing.T, dir string, info LockInfo) {
	t.Helper()
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, LockFileName), data, 0600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
}

func TestLockMutationErrorBranches(t *testing.T) {
	dir := t.TempDir()
	if err := UpdateLockTask(dir, "T-1", "Task"); err == nil || !strings.Contains(err.Error(), "no active lock") {
		t.Fatalf("UpdateLockTask missing err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, LockFileName), []byte("{bad"), 0600); err != nil {
		t.Fatalf("write invalid lock: %v", err)
	}
	if err := UpdateLockState(dir, StateIdle); err == nil || !strings.Contains(err.Error(), "invalid lock") {
		t.Fatalf("UpdateLockState invalid err = %v", err)
	}
	writeLockInfo(t, dir, LockInfo{PID: os.Getpid() + 100000, StartedAt: time.Now()})
	if err := ClearLockTaskID(dir); err == nil || !strings.Contains(err.Error(), "different process") {
		t.Fatalf("ClearLockTaskID foreign err = %v", err)
	}
	if err := UpdateLockClaudeSessionID(dir, "claude-1"); err == nil || !strings.Contains(err.Error(), "different process") {
		t.Fatalf("UpdateLockClaudeSessionID foreign err = %v", err)
	}
	if err := ClearLockClaudeSessionID(dir); err == nil || !strings.Contains(err.Error(), "different process") {
		t.Fatalf("ClearLockClaudeSessionID foreign err = %v", err)
	}
}

func TestLockMutationSuccessBranches(t *testing.T) {
	dir := t.TempDir()
	writeLockInfo(t, dir, LockInfo{
		PID:             os.Getpid(),
		Command:         "task",
		StartedAt:       time.Now().Add(-time.Minute),
		TaskID:          "old",
		TaskTitle:       "Old",
		TaskStartedAt:   time.Now().Add(-time.Minute),
		State:           StateActive,
		ClaudeSessionID: "claude-old",
	})

	if err := UpdateLockTask(dir, "T-1", "Task one"); err != nil {
		t.Fatalf("UpdateLockTask: %v", err)
	}
	if err := UpdateLockState(dir, StateIdle); err != nil {
		t.Fatalf("UpdateLockState: %v", err)
	}
	if err := UpdateLockClaudeSessionID(dir, "claude-new"); err != nil {
		t.Fatalf("UpdateLockClaudeSessionID: %v", err)
	}
	info, err := ReadLockFile(dir)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if info.TaskID != "T-1" || info.TaskTitle != "Task one" || info.State != StateIdle || info.ClaudeSessionID != "claude-new" {
		t.Fatalf("lock after updates = %+v", info)
	}
	if err := ClearLockTaskID(dir); err != nil {
		t.Fatalf("ClearLockTaskID: %v", err)
	}
	if err := ClearLockClaudeSessionID(dir); err != nil {
		t.Fatalf("ClearLockClaudeSessionID: %v", err)
	}
	info, err = ReadLockFile(dir)
	if err != nil {
		t.Fatalf("ReadLockFile after clear: %v", err)
	}
	if info.TaskID != "" || info.TaskTitle != "" || !info.TaskStartedAt.IsZero() || info.ClaudeSessionID != "" {
		t.Fatalf("lock after clears = %+v", info)
	}
	if err := ClearLockClaudeSessionID(t.TempDir()); err != nil {
		t.Fatalf("ClearLockClaudeSessionID missing lock: %v", err)
	}
}

func TestResolveWorkspaceNameWithFleetConfig(t *testing.T) {
	requireLockFleetDB(t)

	ctx := context.Background()
	dataDir := t.TempDir()
	workspaceDir := t.TempDir()
	repoDir := filepath.Join(workspaceDir, "api")
	if err := os.MkdirAll(filepath.Join(repoDir, "nested"), 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	t.Setenv("LOOM_CONFIG_DIR", dataDir)
	t.Setenv(bootstrap.EnvFleetDBActor, "lock-coverage-test")
	t.Setenv(bootstrap.EnvFleetDBURL, "")

	handle, err := bootstrap.OpenStore(ctx, dataDir, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if _, err := handle.Store.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := handle.Store.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "WS", Name: "api"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		LastWorkspace: "WS",
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS": {Path: workspaceDir, Repos: map[string]string{"api": repoDir}},
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	for _, path := range []string{workspaceDir, filepath.Join(workspaceDir, "child"), repoDir, filepath.Join(repoDir, "nested")} {
		if got := resolveWorkspaceName(path); got != "WS" {
			t.Fatalf("resolveWorkspaceName(%q) = %q, want WS", path, got)
		}
	}
	if got := resolveWorkspaceName(t.TempDir()); got != "" {
		t.Fatalf("resolveWorkspaceName(outside) = %q, want empty", got)
	}
}

func TestAcquireLockRetryStaleRunningAndContentionBranches(t *testing.T) {
	staleDir := t.TempDir()
	writeLockInfo(t, staleDir, LockInfo{
		PID:       os.Getpid() + 100000,
		Command:   "task",
		StartedAt: time.Now().Add(-time.Minute),
	})
	file, err := acquireLockRetry(staleDir, filepath.Join(staleDir, LockFileName))
	if err != nil {
		t.Fatalf("acquireLockRetry stale lock: %v", err)
	}
	_ = file.Close()

	runningDir := t.TempDir()
	writeLockInfo(t, runningDir, LockInfo{
		PID:       os.Getpid(),
		Command:   "plan",
		StartedAt: time.Now().Add(-time.Minute),
	})
	if _, err := acquireLockRetry(runningDir, filepath.Join(runningDir, LockFileName)); err == nil ||
		!strings.Contains(err.Error(), "agent already running") {
		t.Fatalf("acquireLockRetry running err = %v", err)
	}

	contentionDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(contentionDir, LockFileName), []byte("{bad"), 0600); err != nil {
		t.Fatalf("write invalid lock: %v", err)
	}
	if _, err := acquireLockRetry(contentionDir, filepath.Join(contentionDir, LockFileName)); err == nil ||
		!strings.Contains(err.Error(), "concurrent lock contention") {
		t.Fatalf("acquireLockRetry contention err = %v", err)
	}
}

func requireLockFleetDB(t *testing.T) {
	t.Helper()
	if os.Getenv(bootstrap.EnvFleetDBBin) != "" {
		return
	}
	if _, err := exec.LookPath("fleet-db"); err != nil {
		t.Skip("fleet-db binary not available")
	}
}

func TestGetTaskStatusDepsBranches(t *testing.T) {
	deps := &Deps{IssueBackend: &MockIssueBackend{GetResult: &backend.IssueDetailData{IssueData: backend.IssueData{Status: "review"}}}}
	if got := GetTaskStatusDeps(deps, "T-1"); got != "needs_review" {
		t.Fatalf("review status = %q", got)
	}
	deps.IssueBackend = &MockIssueBackend{GetResult: &backend.IssueDetailData{IssueData: backend.IssueData{Status: "closed"}}}
	if got := GetTaskStatusDeps(deps, "T-1"); got != "closed" {
		t.Fatalf("closed status = %q", got)
	}
	deps.IssueBackend = &MockIssueBackend{GetErr: os.ErrNotExist}
	if got := GetTaskStatusDeps(deps, "T-1"); got != "" {
		t.Fatalf("error status = %q, want empty", got)
	}
}
