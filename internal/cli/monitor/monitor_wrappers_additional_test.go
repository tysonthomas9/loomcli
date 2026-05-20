package monitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
)

func TestCollectMonitorDataWithIssueBackendAndWrappers(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origDir)
		ResetWorkspaceRuntimeDirCache()
	})
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ResetWorkspaceRuntimeDirCache()

	mock := NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "PLAN-1", Title: "needs plan", Status: "open"},
		{ID: "READY-1", Title: "ready", Status: "open", Design: "approved"},
		{ID: "EPIC-1", Title: "epic", Status: "open", IssueType: "epic"},
	}
	mock.ListFn = func(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
		switch opts.Status {
		case "in_progress":
			return []backend.IssueData{{ID: "TASK-1", Title: "doing", Status: "in_progress", Assignee: "nova"}}, nil
		case "review":
			return []backend.IssueData{{ID: "TASK-2", Title: "review", Status: "review"}}, nil
		case "closed":
			return []backend.IssueData{{ID: "TASK-3", Title: "closed", Status: "closed"}}, nil
		default:
			return nil, nil
		}
	}
	mock.BlockedResult = []backend.IssueData{{ID: "BLOCKED-1", Title: "blocked", Status: "open"}}
	mock.StatsResult = &backend.StatsData{TotalIssues: 8, OpenIssues: 4, InProgressIssues: 1, ClosedIssues: 2, BlockedIssues: 1}
	mock.CountErr = errors.New("count unsupported")

	data := CollectMonitorDataWithIssueBackend(mock, 10, "")
	if data == nil {
		t.Fatal("CollectMonitorDataWithIssueBackend returned nil")
	}
	if data.Tasks.NeedsPlanning != 1 || data.Tasks.ReadyToImplement != 1 || data.Tasks.Epics != 1 || data.Tasks.InProgress != 1 {
		t.Fatalf("task summary = %+v", data.Tasks)
	}
	if data.Stats.Total != 8 || data.Stats.Remaining != 6 {
		t.Fatalf("stats = %+v", data.Stats)
	}
	if data.AgentTasks["nova"].ID != "TASK-1" {
		t.Fatalf("agent task map = %+v", data.AgentTasks)
	}

	setDefaultIssueBackend(mock)
	t.Cleanup(resetDefaultIssueBackend)
	stats := collectStatistics()
	if stats.Total != 8 || stats.Review != 0 {
		t.Fatalf("collectStatistics = %+v", stats)
	}
	if sync := collectStoreSyncStatus(); !sync.DBSynced || sync.DBLastSync != "live" {
		t.Fatalf("collectStoreSyncStatus = %+v", sync)
	}
	sync := collectSyncStatus([]AgentStatus{
		{Name: "ahead", Ahead: 2},
		{Name: "behind", Behind: 3},
	})
	if sync.GitNeedsPush != 1 || sync.GitNeedsPull != 1 || len(sync.GitPushDetails) != 1 || len(sync.GitPullDetails) != 1 {
		t.Fatalf("collectSyncStatus = %+v", sync)
	}
}

func TestDefaultGitHelperWrappersUseDefaultDeps(t *testing.T) {
	dd := cli.TestingGetDefaultDeps()
	oldGit := dd.Git
	t.Cleanup(func() { dd.Git = oldGit })

	mock := &clitest.MockExecRunner{RunFunc: func(_ string, name string, args ...string) cli.CommandResult {
		if name != "git" {
			return cli.CommandResult{Err: errors.New("unexpected command")}
		}
		switch args[0] {
		case "remote":
			return cli.CommandResult{Stdout: "git@github.com:org/repo.git\n"}
		case "log":
			return cli.CommandResult{Stdout: "abc123|first commit\nbad line\ndef456|second commit\n"}
		case "status":
			return cli.CommandResult{Stdout: " M file.go\n?? new.txt\n"}
		case "rev-list":
			return cli.CommandResult{Stdout: "4\t2\n"}
		default:
			return cli.CommandResult{Err: errors.New("unexpected git args")}
		}
	}}
	dd.Git = &clitest.ExecBridgeGitRunner{Exec: mock}

	if got := getGitHubRemoteURL(t.TempDir()); got != "https://github.com/org/repo" {
		t.Fatalf("getGitHubRemoteURL = %q", got)
	}
	commits := getWorktreeCommitDetails(t.TempDir(), "main", 2, "https://github.com/org/repo", "")
	if len(commits) != 2 || commits[0].URL != "https://github.com/org/repo/commit/abc123" {
		t.Fatalf("getWorktreeCommitDetails = %+v", commits)
	}
	changes := getWorktreeFileChanges(t.TempDir())
	if len(changes) != 2 || changes[0].Status != "M" || changes[1].Path != "new.txt" {
		t.Fatalf("getWorktreeFileChanges = %+v", changes)
	}
	ahead, behind := GetWorktreeGitSyncStatus(t.TempDir(), "main", "")
	if ahead != 2 || behind != 4 {
		t.Fatalf("GetWorktreeGitSyncStatus ahead=%d behind=%d", ahead, behind)
	}
}

func TestCommitCacheKeyAndResolveIdleFallback(t *testing.T) {
	if got := commitCacheKey("head", "remote", "main", "https://example/repo"); got != "head|remote|main|https://example/repo" {
		t.Fatalf("commitCacheKey = %q", got)
	}

	dd := cli.TestingGetDefaultDeps()
	oldGit := dd.Git
	t.Cleanup(func() { dd.Git = oldGit })
	dd.Git = &clitest.ExecBridgeGitRunner{Exec: &clitest.MockExecRunner{RunFunc: func(_ string, _ string, args ...string) cli.CommandResult {
		if len(args) > 0 && args[0] == "status" {
			return cli.CommandResult{Stdout: ""}
		}
		return cli.CommandResult{}
	}}}

	wtPath := filepath.Join(t.TempDir(), "not-a-git-worktree")
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}
	status, changes := resolveIdleStatus(cli.TestingGetDefaultDeps(), wtPath)
	if status != "ready" || changes != nil {
		t.Fatalf("resolveIdleStatus = %q %+v", status, changes)
	}
}
