package monitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
)

func TestRunMonitorOneShotRendersDashboard(t *testing.T) {
	origNoWatch := monitorNoWatch
	origBranch := monitorBranch
	t.Cleanup(func() {
		monitorNoWatch = origNoWatch
		monitorBranch = origBranch
	})
	monitorNoWatch = true
	monitorBranch = "main"

	runMonitor(nil, nil)
}

func TestRunMonitorWatchModeInitialPass(t *testing.T) {
	type stopWatch struct{}
	origNoWatch := monitorNoWatch
	origBranch := monitorBranch
	origInterval := monitorInterval
	origCollect := monitorCollectDataFn
	origSleep := monitorSleepFn
	t.Cleanup(func() {
		monitorNoWatch = origNoWatch
		monitorBranch = origBranch
		monitorInterval = origInterval
		monitorCollectDataFn = origCollect
		monitorSleepFn = origSleep
	})
	monitorNoWatch = false
	monitorBranch = "main"
	monitorInterval = 1
	calls := 0
	monitorCollectDataFn = func(limit int, branch string) *MonitorData {
		if limit != 10000 || branch != "main" {
			t.Fatalf("CollectMonitorData args limit=%d branch=%q", limit, branch)
		}
		calls++
		return &MonitorData{Timestamp: time.Now()}
	}
	monitorSleepFn = func(time.Duration) { panic(stopWatch{}) }

	defer func() {
		if got := recover(); got == nil {
			t.Fatal("runMonitor watch mode did not reach sleep")
		} else if _, ok := got.(stopWatch); !ok {
			panic(got)
		}
		if calls != 1 {
			t.Fatalf("CollectMonitorData calls = %d, want 1", calls)
		}
	}()
	runMonitor(nil, nil)
}

func TestMonitorCollectionBranchHelpers(t *testing.T) {
	deps := &cli.Deps{
		IssueBackend: &clitest.MockIssueBackend{
			GetResult: &backend.IssueDetailData{IssueData: backend.IssueData{ID: "TASK-1", Status: "review"}},
		},
	}
	review := refineLockTaskStatus(deps, "planning: ... (1s)", TaskInfo{ID: "TASK-1"}, " (1s)")
	if review != "review: TASK-1 (1s)" {
		t.Fatalf("review lock status = %q", review)
	}
	deps.IssueBackend = &clitest.MockIssueBackend{GetResult: &backend.IssueDetailData{IssueData: backend.IssueData{ID: "TASK-2", Status: "closed"}}}
	done := refineLockTaskStatus(deps, "working: ...", TaskInfo{ID: "TASK-2"}, "")
	if done != "done: TASK-2" {
		t.Fatalf("done lock status = %q", done)
	}
	deps.IssueBackend = &clitest.MockIssueBackend{GetResult: &backend.IssueDetailData{IssueData: backend.IssueData{ID: "TASK-3", Status: "open"}}}
	working := refineLockStatus(deps, "idle (2s)", &cli.LockInfo{Command: "task"}, "nova", map[string]TaskInfo{
		"nova": {ID: "TASK-3"},
	})
	if working != "working: TASK-3" {
		t.Fatalf("idle refined status = %q", working)
	}
	if got := lockStatusForCommand("plan"); got != "planning: ..." {
		t.Fatalf("plan lock status = %q", got)
	}
	if got := lockStatusForCommand("task"); got != "working: ..." {
		t.Fatalf("task lock status = %q", got)
	}

	var summary TaskSummary
	if needs, ready := processReadyIssues(nil, errors.New("ready failed"), &summary, nil); needs != nil || ready != nil {
		t.Fatalf("error ready issues = %+v %+v", needs, ready)
	}
	issues := []backend.IssueData{
		{ID: "CLOSED", Status: "closed"},
		{ID: "EPIC", Status: "open", IssueType: "epic"},
		{ID: "DOC", Status: "open", IssueType: "gate"},
		{ID: "BLOCKED", Status: "open"},
		{ID: "PLAN", Title: "plan it", Status: "open", Priority: 1},
		{ID: "READY", Title: "build it", Status: "open", Design: "approved", Priority: 2},
	}
	needs, ready := processReadyIssues(issues, nil, &summary, map[string]bool{"BLOCKED": true})
	if summary.Epics != 1 || summary.NeedsPlanning != 1 || summary.ReadyToImplement != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if len(needs) != 1 || needs[0].ID != "PLAN" || len(ready) != 1 || ready[0].ID != "READY" {
		t.Fatalf("needs=%+v ready=%+v", needs, ready)
	}

	agentTasks := make(map[string]TaskInfo)
	inProgress := processInProgressIssues([]backend.IssueData{{ID: "DOING", Assignee: "nova", Status: "in_progress"}}, nil, &summary, agentTasks)
	if len(inProgress) != 1 || agentTasks["nova"].ID != "DOING" {
		t.Fatalf("inProgress=%+v agentTasks=%+v", inProgress, agentTasks)
	}
	if got := processReviewIssues(nil, errors.New("review failed"), &summary); got != nil {
		t.Fatalf("review error = %+v", got)
	}
	if got := processBacklogIssues(nil, errors.New("blocked failed"), &summary); got != nil {
		t.Fatalf("backlog error = %+v", got)
	}
	if got := processClosedIssues(nil, errors.New("closed failed")); got != nil {
		t.Fatalf("closed error = %+v", got)
	}
}

func TestBuildAgentStatusWithGitFallbacksAndCommits(t *testing.T) {
	exec := &clitest.MockExecRunner{RunFunc: func(_ string, name string, args ...string) cli.CommandResult {
		if name != "git" {
			return cli.CommandResult{Err: errors.New("unexpected command")}
		}
		joined := strings.Join(args, " ")
		switch {
		case joined == "status --porcelain":
			return cli.CommandResult{Stdout: " M file.go\n?? new.txt\n"}
		case strings.HasPrefix(joined, "rev-list --left-right --count"):
			return cli.CommandResult{Stdout: "1\t2\n"}
		case joined == "remote get-url origin":
			return cli.CommandResult{Stdout: "git@github.com:org/repo.git\n"}
		case strings.HasPrefix(joined, "log "):
			return cli.CommandResult{Stdout: "abc123|first\nbad-line\ndef456|second\n"}
		default:
			return cli.CommandResult{Err: errors.New("unexpected git args: " + joined)}
		}
	}}
	deps := &cli.Deps{Git: &clitest.ExecBridgeGitRunner{Exec: exec}}
	taskIDToAgents := make(map[string][]string)
	status := buildAgentStatus(
		deps,
		cli.WorktreeInfo{Name: "nova", Path: t.TempDir(), Branch: "feature/nova", Repo: nil},
		map[string]DaemonAgentInfo{"nova": {Managed: true, Role: "task", Repo: "api"}},
		map[string]TaskInfo{},
		taskIDToAgents,
		"main",
		"https://github.com/org/repo",
		"",
	)
	if status.Status != "2 changes" || status.Ahead != 2 || status.Behind != 1 || len(status.Changes) != 2 {
		t.Fatalf("status = %+v", status)
	}
	if len(status.Commits) != 2 || status.Commits[0].URL != "https://github.com/org/repo/commit/abc123" {
		t.Fatalf("commits = %+v", status.Commits)
	}

	stats := collectStatisticsDeps(&cli.Deps{IssueBackend: &clitest.MockIssueBackend{
		StatsResult: &backend.StatsData{TotalIssues: 4, ClosedIssues: 6, OpenIssues: 1},
		CountResult: 2,
	}})
	if stats.Remaining != 0 || stats.Review != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if got := collectStatisticsDeps(&cli.Deps{IssueBackend: &clitest.MockIssueBackend{StatsErr: errors.New("stats failed")}}); got.Total != 0 {
		t.Fatalf("failed stats = %+v", got)
	}
}

func TestCollectMonitorDataDepsDetectsTaskConflicts(t *testing.T) {
	mock := &clitest.MockIssueBackend{
		StatsResult:   &backend.StatsData{TotalIssues: 1, OpenIssues: 1},
		ReadyResult:   []backend.IssueData{{ID: "READY", Status: "open", Design: "approved"}},
		BlockedResult: []backend.IssueData{},
	}
	mock.ListFn = func(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
		if opts.Status == "in_progress" {
			return []backend.IssueData{{ID: "TASK", Status: "in_progress", Assignee: "nova"}}, nil
		}
		return nil, nil
	}
	data := collectMonitorDataDeps(&cli.Deps{IssueBackend: mock}, 5, "")
	if data.Tasks.ReadyToImplement != 1 || data.AgentTasks["nova"].ID != "TASK" {
		t.Fatalf("monitor data = %+v", data)
	}
}

func TestCollectAgentGitCachesAvoidRepeatedGitCommands(t *testing.T) {
	worktree := t.TempDir()
	gitDir := filepath.Join(worktree, ".git")
	for _, dir := range []string{
		filepath.Join(gitDir, "refs", "heads"),
		filepath.Join(gitDir, "refs", "remotes", "origin"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/feature\n"), 0644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "index"), []byte("index"), 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "refs", "heads", "feature"), []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"), 0644); err != nil {
		t.Fatalf("write local ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "refs", "remotes", "origin", "main"), []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n"), 0644); err != nil {
		t.Fatalf("write remote ref: %v", err)
	}

	oldDetector := globalChangeDetector
	oldCommitCache := globalCommitCache
	globalChangeDetector = &worktreeChangeDetector{entries: make(map[string]*worktreeCacheEntry)}
	globalCommitCache = &commitCache{entries: make(map[string][]CommitDetail)}
	t.Cleanup(func() {
		globalChangeDetector = oldDetector
		globalCommitCache = oldCommitCache
	})

	calls := make(map[string]int)
	execRunner := &clitest.MockExecRunner{RunFunc: func(_ string, name string, args ...string) cli.CommandResult {
		if name != "git" {
			return cli.CommandResult{Err: errors.New("unexpected command")}
		}
		joined := strings.Join(args, " ")
		calls[joined]++
		switch {
		case strings.HasPrefix(joined, "rev-list --left-right --count"):
			return cli.CommandResult{Stdout: "3\t4\n"}
		case strings.HasPrefix(joined, "log "):
			return cli.CommandResult{Stdout: "abc123|first\n"}
		default:
			return cli.CommandResult{Err: errors.New("unexpected git args: " + joined)}
		}
	}}
	deps := &cli.Deps{Git: &clitest.ExecBridgeGitRunner{Exec: execRunner}}
	wt := cli.WorktreeInfo{Name: "nova", Path: worktree, Branch: "feature"}

	ahead, behind := collectAgentAheadBehind(deps, wt, "feature", "main", "")
	ahead2, behind2 := collectAgentAheadBehind(deps, wt, "feature", "main", "")
	if ahead != 4 || behind != 3 || ahead2 != 4 || behind2 != 3 {
		t.Fatalf("ahead/behind = %d/%d then %d/%d, want 4/3", ahead, behind, ahead2, behind2)
	}
	if calls["rev-list --left-right --count origin/main...HEAD"] != 1 {
		t.Fatalf("rev-list calls = %+v, want one cached call", calls)
	}

	commits := collectAgentCommits(deps, wt, "feature", "main", "https://github.com/org/repo", "")
	commits2 := collectAgentCommits(deps, wt, "feature", "main", "https://github.com/org/repo", "")
	if len(commits) != 1 || len(commits2) != 1 || commits[0].URL != "https://github.com/org/repo/commit/abc123" {
		t.Fatalf("commits = %+v then %+v", commits, commits2)
	}
	if calls["log origin/main..HEAD --format=%H|%s -n 10"] != 1 {
		t.Fatalf("log calls = %+v, want one cached call", calls)
	}
}
