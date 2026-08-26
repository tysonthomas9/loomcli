package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestClaimTask_SelectsEligibleTaskAndClaims(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "epic-1", IssueType: "epic", Status: "open", Priority: 0, Title: "Epic"},
		{ID: "task-2", IssueType: "task", Status: "open", Priority: 3, Title: "No design"},
		{ID: "task-1", IssueType: "task", Status: "open", Priority: 1, Title: "Ready", Design: "plan"},
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	if !s.claimTask(ap, "parent-1") {
		t.Fatal("claimTask returned false")
	}
	if ap.AssignedTaskID != "task-1" {
		t.Fatalf("AssignedTaskID = %q, want task-1", ap.AssignedTaskID)
	}
	if len(mock.Calls) != 2 {
		t.Fatalf("calls = %#v, want Ready and ClaimIssue", mock.Calls)
	}
	opts := mock.Calls[0].Args[0].(backend.ReadyOpts)
	if opts.ParentID != "parent-1" || opts.Limit != claimReadyLimit {
		t.Fatalf("ReadyOpts = %#v", opts)
	}
	if mock.Calls[1].Method != "ClaimIssue" || mock.Calls[1].Args[0] != "task-1" {
		t.Fatalf("claim call = %#v", mock.Calls[1])
	}
	if ttl := mock.Calls[1].Args[1].(time.Duration); ttl != 0 {
		t.Fatalf("claim TTL = %v, want zero", ttl)
	}
}

type recordingTaskWorktreeManager struct {
	req TaskWorktreeRequest
	got TaskWorktree
	err error
}

type historicalDependencyBackend struct {
	*clitest.MockIssueBackend
	dependencyIDs []string
}

func (b *historicalDependencyBackend) DependencyTaskIDs(context.Context, string) ([]string, error) {
	return append([]string(nil), b.dependencyIDs...), nil
}

func TestRequiredDependenciesUseDirectBlockingTasks(t *testing.T) {
	issue := &backend.IssueDetailData{Dependencies: []backend.DependencyData{
		{DependsOnID: "TASK-A", Type: "blocks"},
		{DependsOnID: "TASK-C", Type: "relates_to"},
		{DependsOnID: "TASK-B", Type: "blocks"},
	}}
	got := dependencyTaskIDs(issue)
	if len(got) != 2 || got[0] != "TASK-A" || got[1] != "TASK-B" {
		t.Fatalf("dependencyTaskIDs = %v", got)
	}
}

func TestPrepareClaimedTaskWorktreeUsesTaskRepoWithoutAgentAffinity(t *testing.T) {
	manager := &recordingTaskWorktreeManager{got: TaskWorktree{Path: "/workspace/task", Branch: "loom/task/ws/TASK-9"}}
	repo := &cfgpkg.RepoConfig{Name: "backend", SourceRepoID: "repo-back", Path: "/workspace/backend", DefaultBranch: "main"}
	issues := clitest.NewMockIssueBackend()
	issues.GetFn = func(context.Context, string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{
			IssueData:    backend.IssueData{ID: "TASK-9", SourceRepo: "repo-back"},
			Dependencies: []backend.DependencyData{{DependsOnID: "TASK-8", Type: "blocks"}},
		}, nil
	}
	s := &Supervisor{
		ProjectDir:    "/workspace",
		WorkspaceID:   "ws",
		TaskWorktrees: manager,
		IssueBackend:  issues,
		FindRepoConfig: func(key string) *cfgpkg.RepoConfig {
			if key == "repo-back" {
				return repo
			}
			return nil
		},
	}
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "generalist"}, AssignedTaskID: "TASK-9"}
	if err := s.prepareClaimedTaskWorktree(context.Background(), ap); err != nil {
		t.Fatal(err)
	}
	if ap.RepoConfig != repo || manager.req.RepoName != "backend" || len(manager.req.DependencyTaskIDs) != 1 || manager.req.DependencyTaskIDs[0] != "TASK-8" {
		t.Fatalf("resolved repo = %+v, request = %+v", ap.RepoConfig, manager.req)
	}
}

func TestPrepareClaimedTaskWorktreeUsesHistoricalDependenciesAfterBlockerCloses(t *testing.T) {
	manager := &recordingTaskWorktreeManager{got: TaskWorktree{Path: "/workspace/task", Branch: "loom/task/ws/TASK-B"}}
	issues := &historicalDependencyBackend{
		MockIssueBackend: clitest.NewMockIssueBackend(),
		dependencyIDs:    []string{"TASK-A"},
	}
	issues.GetFn = func(context.Context, string) (*backend.IssueDetailData, error) {
		// FleetDB removes blocking edges when TASK-A closes, so the claim-time
		// issue projection no longer contains TASK-A even though its delivery is
		// required as TASK-B's immutable input.
		return &backend.IssueDetailData{IssueData: backend.IssueData{ID: "TASK-B", SourceRepo: "repo-back"}}, nil
	}
	repo := &cfgpkg.RepoConfig{Name: "backend", SourceRepoID: "repo-back", Path: "/workspace/backend", DefaultBranch: "main"}
	s := &Supervisor{
		ProjectDir:    "/workspace",
		WorkspaceID:   "ws",
		TaskWorktrees: manager,
		IssueBackend:  issues,
		FindRepoConfig: func(string) *cfgpkg.RepoConfig {
			return repo
		},
	}
	ap := &AgentProcess{AssignedTaskID: "TASK-B"}

	if err := s.prepareClaimedTaskWorktree(context.Background(), ap); err != nil {
		t.Fatal(err)
	}
	if got := manager.req.DependencyTaskIDs; len(got) != 1 || got[0] != "TASK-A" {
		t.Fatalf("DependencyTaskIDs = %v, want closed blocker TASK-A from durable lineage", got)
	}
}

func TestDeliveryPublicationRequiresConfiguredSuccessFence(t *testing.T) {
	issues := clitest.NewMockIssueBackend()
	current := &backend.IssueDetailData{IssueData: backend.IssueData{ID: "TASK-7", Labels: []string{"backend"}}}
	issues.GetFn = func(context.Context, string) (*backend.IssueDetailData, error) { return current, nil }
	s := &Supervisor{IssueBackend: issues}
	ap := &AgentProcess{
		AssignedTaskID: "TASK-7",
		Entry: cfgpkg.AgentEntry{Hooks: &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
			{Type: domain.AgentHookActionRemoveLabel, Value: "delivery-pending"},
		}}},
	}
	got, err := s.shouldPublishTaskDelivery(context.Background(), ap)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("blocked/needs-revision run without delivery-pending was publishable")
	}
	current.Labels = append(current.Labels, "delivery-pending")
	got, err = s.shouldPublishTaskDelivery(context.Background(), ap)
	if err != nil || !got {
		t.Fatalf("successful fenced delivery publishable = %v, err=%v", got, err)
	}
}

func TestResolveTaskRepoUsesClaimedTaskSourceRepo(t *testing.T) {
	first := &cfgpkg.RepoConfig{Name: "frontend", SourceRepoID: "repo-front"}
	backendRepo := &cfgpkg.RepoConfig{Name: "backend", SourceRepoID: "repo-back"}
	got, err := resolveTaskRepo(first, "repo-back", func(key string) *cfgpkg.RepoConfig {
		if key == "repo-back" {
			return backendRepo
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != backendRepo {
		t.Fatalf("resolved repo = %+v, want backend source repo", got)
	}
}

func (m *recordingTaskWorktreeManager) Prepare(_ context.Context, req TaskWorktreeRequest) (TaskWorktree, error) {
	m.req = req
	return m.got, m.err
}

func (m *recordingTaskWorktreeManager) Publish(_ context.Context, _ TaskWorktreePublishRequest) (TaskWorktreeRevision, error) {
	return TaskWorktreeRevision{}, nil
}

func TestPrepareClaimedTaskWorktreeSwitchesExecutionBeforeSessionBaseline(t *testing.T) {
	manager := &recordingTaskWorktreeManager{got: TaskWorktree{
		Path:     "/workspace/.loom/task-worktrees/repo/TASK-7",
		Branch:   "loom/task/ws/TASK-7",
		InputSHA: "abc123",
		TreeSHA:  "tree123",
	}}
	s := &Supervisor{
		ProjectDir:    "/workspace",
		WorkspaceID:   "ws",
		TaskWorktrees: manager,
	}
	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "qa-engineer-1"},
		RepoConfig:     &cfgpkg.RepoConfig{Name: "repo", Path: "/workspace/repo", DefaultBranch: "main", Remote: "origin"},
		WorktreePath:   "/workspace/worktrees/repo/qa-engineer-1",
		AssignedTaskID: "TASK-7",
	}

	if err := s.prepareClaimedTaskWorktree(context.Background(), ap); err != nil {
		t.Fatal(err)
	}
	if ap.WorktreePath != manager.got.Path || ap.TaskBranch != manager.got.Branch || ap.TaskInputSHA != manager.got.InputSHA {
		t.Fatalf("agent task worktree state = path %q branch %q input %q", ap.WorktreePath, ap.TaskBranch, ap.TaskInputSHA)
	}
	if manager.req.TaskID != "TASK-7" || manager.req.RepoName != "repo" || manager.req.RepoPath != "/workspace/repo" {
		t.Fatalf("prepare request = %+v", manager.req)
	}
}

func TestTaskWorktreeRevisionIsIncludedInSessionMetadata(t *testing.T) {
	s := &Supervisor{}
	ap := &AgentProcess{
		Entry:             cfgpkg.AgentEntry{Worktree: "qa-engineer-1"},
		TaskBranch:        "loom/task/ws/TASK-7",
		TaskInputSHA:      "input123",
		TaskTreeSHA:       "inputtree123",
		TaskOutputSHA:     "output456",
		TaskOutputTreeSHA: "outputtree456",
	}
	ap.Mu.Lock()
	metadata := s.agentSessionMetadataLocked(ap, "codex")
	ap.Mu.Unlock()

	want := map[string]string{
		"task_branch":          "loom/task/ws/TASK-7",
		"task_input_sha":       "input123",
		"task_input_tree_sha":  "inputtree123",
		"task_output_sha":      "output456",
		"task_output_tree_sha": "outputtree456",
	}
	for key, value := range want {
		if metadata[key] != value {
			t.Errorf("metadata[%q] = %q, want %q", key, metadata[key], value)
		}
	}
}

func TestClearCompletedTaskSessionRestoresPrivateAgentWorktree(t *testing.T) {
	s := &Supervisor{}
	ap := &AgentProcess{
		AgentWorktreePath: "/workspace/.loom/worktrees/repo/backend-dev-1",
		WorktreePath:      "/workspace/.loom/task-worktrees/repo/TASK-7",
		TaskBranch:        "loom/task/ws/TASK-7",
		TaskInputSHA:      "input123",
		TaskOutputSHA:     "output456",
		AssignedTaskID:    "TASK-7",
	}

	s.clearAgentSessionState(ap)
	if ap.WorktreePath != ap.AgentWorktreePath {
		t.Fatalf("next recovery path = %q, want private agent worktree %q", ap.WorktreePath, ap.AgentWorktreePath)
	}
	if ap.TaskBranch != "" || ap.TaskInputSHA != "" || ap.TaskOutputSHA != "" {
		t.Fatalf("completed task delivery state survived next cycle: branch=%q input=%q output=%q", ap.TaskBranch, ap.TaskInputSHA, ap.TaskOutputSHA)
	}
}

func TestClaimTask_ClaimsRequestedTaskIgnoringRoleFilter(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-1", IssueType: "task", Status: "open", Priority: 1, Title: "No design", Assignee: "falcon"},
		{ID: "task-2", IssueType: "task", Status: "open", Priority: 0, Title: "Ready", Design: "plan"},
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:           cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig:      cfgpkg.RoleConfig{TaskFilter: "has_design"},
		RequestedTaskID: "task-1",
	}

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false")
	}
	if ap.AssignedTaskID != "task-1" {
		t.Fatalf("AssignedTaskID = %q, want task-1", ap.AssignedTaskID)
	}
	if ap.RequestedTaskID != "" {
		t.Fatalf("RequestedTaskID = %q, want empty", ap.RequestedTaskID)
	}
	if len(mock.Calls) != 2 {
		t.Fatalf("calls = %#v, want Ready and ClaimIssue", mock.Calls)
	}
	opts := mock.Calls[0].Args[0].(backend.ReadyOpts)
	if opts.Assignee != "" {
		t.Fatalf("ReadyOpts.Assignee = %q, want empty for requested task lookup", opts.Assignee)
	}
	if mock.Calls[1].Method != "ClaimIssue" || mock.Calls[1].Args[0] != "task-1" {
		t.Fatalf("claim call = %#v", mock.Calls[1])
	}
}

func TestClaimTask_RequestedTaskNotReadyDoesNotClaimFallback(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-2", IssueType: "task", Status: "open", Priority: 0, Title: "Ready", Design: "plan"},
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:           cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig:      cfgpkg.RoleConfig{TaskFilter: "has_design"},
		RequestedTaskID: "task-1",
	}

	if s.claimTask(ap, "") {
		t.Fatal("claimTask returned true")
	}
	if ap.AssignedTaskID != "" {
		t.Fatalf("AssignedTaskID = %q, want empty", ap.AssignedTaskID)
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("calls = %#v, want only requested-task Ready", mock.Calls)
	}
	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome) {
		t.Fatalf("LastError = %#v, want NoWork", ap.LastError)
	}
}

func TestClaimTask_EphemeralRequiresRequestedTask(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-1", IssueType: "task", Status: "open", Priority: 1, Title: "Ready", Design: "plan"},
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "worker", Role: "task", Mode: domain.AgentModeEphemeral},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	if s.claimTask(ap, "EPIC-1") {
		t.Fatal("claimTask returned true, want false for ephemeral worker without requested task")
	}
	if ap.AssignedTaskID != "" {
		t.Fatalf("AssignedTaskID = %q, want empty", ap.AssignedTaskID)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("calls = %#v, want no ready/claim calls", mock.Calls)
	}
	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome) {
		t.Fatalf("LastError = %#v, want NoWork", ap.LastError)
	}
}

func TestClaimTask_PrefersAssignedTasksBeforeGeneralQueue(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyFn = func(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
		if opts.Assignee == "falcon" {
			return []backend.IssueData{
				{ID: "task-assigned", IssueType: "task", Status: "open", Priority: 3, Title: "Assigned", Design: "plan", Assignee: "falcon"},
			}, nil
		}
		return []backend.IssueData{
			{ID: "task-general", IssueType: "task", Status: "open", Priority: 0, Title: "General", Design: "plan"},
		}, nil
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false")
	}
	if ap.AssignedTaskID != "task-assigned" {
		t.Fatalf("AssignedTaskID = %q, want task-assigned", ap.AssignedTaskID)
	}
	if len(mock.Calls) != 2 {
		t.Fatalf("calls = %#v, want assigned Ready and ClaimIssue", mock.Calls)
	}
	opts := mock.Calls[0].Args[0].(backend.ReadyOpts)
	if opts.Assignee != "falcon" {
		t.Fatalf("ReadyOpts.Assignee = %q, want falcon", opts.Assignee)
	}
}

func TestClaimTask_SkipsConflictedCandidate(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-1", IssueType: "task", Status: "open", Priority: 1, Title: "First", Design: "plan"},
		{ID: "task-2", IssueType: "task", Status: "open", Priority: 2, Title: "Second", Design: "plan"},
	}
	mock.ClaimIssueFn = func(_ context.Context, id string, _ time.Duration) error {
		if id == "task-1" {
			return backend.ErrConflict("ClaimIssue", "claimed")
		}
		return nil
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false")
	}
	if ap.AssignedTaskID != "task-2" {
		t.Fatalf("AssignedTaskID = %q, want task-2", ap.AssignedTaskID)
	}
}

func TestClaimTask_CapsConflictRetries(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	for i := 0; i < claimConflictRetryLimit+5; i++ {
		mock.ReadyResult = append(mock.ReadyResult, backend.IssueData{
			ID:        "task-" + string(rune('a'+i)),
			IssueType: "task",
			Status:    "open",
			Priority:  1,
			Title:     "Conflicted",
			Design:    "plan",
		})
	}
	claimCalls := 0
	mock.ClaimIssueFn = func(_ context.Context, _ string, _ time.Duration) error {
		claimCalls++
		return backend.ErrConflict("ClaimIssue", "claimed")
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	if s.claimTask(ap, "") {
		t.Fatal("claimTask returned true after all candidates conflicted")
	}
	if claimCalls != claimConflictRetryLimit {
		t.Fatalf("claim calls = %d, want capped at %d", claimCalls, claimConflictRetryLimit)
	}
	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome) {
		t.Fatalf("LastError = %#v, want LockConflict after conflict retry cap", ap.LastError)
	}
}

func TestClaimTask_NoMatchSetsNoWork(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "epic-1", IssueType: "epic", Status: "open", Priority: 1, Title: "Epic"},
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	if s.claimTask(ap, "") {
		t.Fatal("claimTask returned true, want false")
	}
	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome) {
		t.Fatalf("LastError = %#v, want NoWork", ap.LastError)
	}
}

func TestClaimTask_SkipsLongLivedRoleWithoutTaskFilter(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "sentinel", Role: "oncall"},
		RoleConfig: cfgpkg.RoleConfig{},
	}

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false")
	}
	if ap.AssignedTaskID != "" {
		t.Fatalf("AssignedTaskID = %q, want empty", ap.AssignedTaskID)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("backend calls = %#v, want none", mock.Calls)
	}
}

func TestClaimTask_CancelsReadyOnShutdown(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	readyStarted := make(chan struct{})
	mock.ReadyFn = func(ctx context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) {
		close(readyStarted)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
			return nil, context.DeadlineExceeded
		}
	}
	shutdown := make(chan struct{})
	s := &Supervisor{IssueBackend: mock, Shutdown: shutdown}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	done := make(chan bool, 1)
	go func() {
		done <- s.claimTask(ap, "")
	}()

	<-readyStarted
	close(shutdown)

	select {
	case ok := <-done:
		if ok {
			t.Fatal("claimTask returned true after shutdown")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("claimTask did not cancel Ready promptly after shutdown")
	}
}

func TestTaskIDForLifecycle_UsesAssignedFallback(t *testing.T) {
	s := &Supervisor{}
	ap := &AgentProcess{AssignedTaskID: "task-assigned"}

	if got := s.taskIDForLifecycle(ap, nil); got != "task-assigned" {
		t.Fatalf("taskIDForLifecycle(nil) = %q, want task-assigned", got)
	}
	if got := s.taskIDForLifecycle(ap, &cli.LockInfo{TaskID: "task-lock"}); got != "task-lock" {
		t.Fatalf("taskIDForLifecycle(lock) = %q, want task-lock", got)
	}
}
