package epicrunner

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestWorkerNameAddsHashToAvoidSanitizedCollisions(t *testing.T) {
	a := WorkerName("epic", "TASK/1")
	b := WorkerName("epic", "TASK:1")
	if a == b {
		t.Fatalf("WorkerName collision: %q", a)
	}
	if len(a) > 63 || len(b) > 63 {
		t.Fatalf("WorkerName length = %d/%d, want <= 63", len(a), len(b))
	}
	if strings.ContainsAny(a, "/:") || strings.ContainsAny(b, "/:") {
		t.Fatalf("WorkerName contains unsanitized chars: %q %q", a, b)
	}
}

func TestNewRunnerRequiresReposBeforeBindingLead(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createLead(t, st, "ws", "nova", "", "")
	ib := clitest.NewMockIssueBackend()
	ib.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{ID: "EPIC-1", IssueType: "epic"}}

	_, _, err := NewRunner(ctx, RunnerConfig{
		Store:            st,
		IssueBackend:     ib,
		WorkspaceKey:     "ws",
		EpicID:           "EPIC-1",
		LeadName:         "nova",
		MutateLead:       true,
		RequireRepos:     true,
		ValidateEpic:     true,
		PrepareWorktrees: false,
	})
	if ErrorKindOf(err) != ErrorKindValidation || !strings.Contains(err.Error(), "has no repos attached") {
		t.Fatalf("NewRunner() error = %v, want no-repos validation", err)
	}
	got, err := st.Agents().Get(ctx, "ws", "nova")
	if err != nil {
		t.Fatalf("get lead: %v", err)
	}
	if got.Parent != "" {
		t.Fatalf("lead parent = %q, want no bind after preflight failure", got.Parent)
	}
}

func TestNewRunnerDryRunSuccessAndHeaderOutput(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createLead(t, st, "ws", "nova", "", "orch-1")
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "ws", Name: "api"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	ib := clitest.NewMockIssueBackend()
	ib.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{ID: "EPIC-1", IssueType: "epic"}}

	var out bytes.Buffer
	r, result, err := NewRunner(ctx, RunnerConfig{
		Store:                 st,
		IssueBackend:          ib,
		WorkspaceKey:          " ws ",
		EpicID:                " EPIC-1 ",
		LeadName:              " nova ",
		Role:                  "",
		Backend:               " codex ",
		MaxConcurrency:        0,
		Interval:              0,
		OrchestratorSessionID: " orch-1 ",
		DryRun:                true,
		RequireRepos:          true,
		ValidateEpic:          true,
		Out:                   &out,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if result == nil || result.State != StartStateDryRun || result.LeadName != "nova" {
		t.Fatalf("result = %+v, want dry-run lead result", result)
	}
	if r.role != "task" || r.maxConcurrency != 1 || r.interval != 5*time.Second || r.prefix != "epic-1" {
		t.Fatalf("runner defaults = role=%q max=%d interval=%s prefix=%q", r.role, r.maxConcurrency, r.interval, r.prefix)
	}
	r.PrintHeader()
	header := out.String()
	for _, want := range []string{"Epic runner", "workspace:        ws", "lead agent:       nova", "backend:          codex", "orchestrator:     orch-1", "dry-run:          true"} {
		if !strings.Contains(header, want) {
			t.Fatalf("header missing %q:\n%s", want, header)
		}
	}
}

func TestValidateEpicIssueAndBackendRunErrorKinds(t *testing.T) {
	ctx := context.Background()
	ib := clitest.NewMockIssueBackend()
	ib.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{ID: "EPIC-1", IssueType: "epic"}}
	if err := validateEpicIssue(ctx, ib, "EPIC-1"); err != nil {
		t.Fatalf("validateEpicIssue valid: %v", err)
	}
	ib.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{ID: "TASK-1", IssueType: "task"}}
	if err := validateEpicIssue(ctx, ib, "TASK-1"); ErrorKindOf(err) != ErrorKindValidation {
		t.Fatalf("validateEpicIssue task error = %v, want validation", err)
	}
	ib.GetResult = nil
	if err := validateEpicIssue(ctx, ib, "MISSING"); ErrorKindOf(err) != ErrorKindNotFound {
		t.Fatalf("validateEpicIssue nil detail error = %v, want not found", err)
	}

	for _, tt := range []struct {
		kind backend.ErrorKind
		want ErrorKind
	}{
		{backend.KindNotFound, ErrorKindNotFound},
		{backend.KindValidation, ErrorKindValidation},
		{backend.KindConflict, ErrorKindConflict},
		{backend.KindUnavailable, ErrorKindUnavailable},
		{backend.KindTimeout, ErrorKindUnavailable},
		{backend.KindCanceled, ErrorKindUnavailable},
		{backend.KindInternal, ErrorKindInternal},
	} {
		err := backendRunError(ErrorKindInternal, "backend failed", backend.NewBackendError(tt.kind, "Get", "boom", nil))
		if ErrorKindOf(err) != tt.want {
			t.Fatalf("backend kind %s mapped to %s, want %s (err=%v)", tt.kind, ErrorKindOf(err), tt.want, err)
		}
	}
	if ErrorKindOf(backendRunError(ErrorKindValidation, "plain", errors.New("plain"))) != ErrorKindValidation {
		t.Fatalf("plain backendRunError did not use default kind")
	}
}

func TestEnsureLocalWorkerWorktreesGuardBranches(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	agent := domain.Agent{WorkspaceKey: "ws", Name: "worker", RoleName: "task"}
	r := &Runner{store: st}

	if err := r.ensureLocalWorkerWorktrees(ctx, agent); err != nil {
		t.Fatalf("ensureLocalWorkerWorktrees without local path: %v", err)
	}
	if err := bootstrap.MutateWorkspaceLocalState("ws", func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = t.TempDir()
		return nil
	}); err != nil {
		t.Fatalf("MutateWorkspaceLocalState: %v", err)
	}
	if err := r.ensureLocalWorkerWorktrees(ctx, agent); err == nil || !strings.Contains(err.Error(), "has no repos") {
		t.Fatalf("ensureLocalWorkerWorktrees no repos err = %v, want no repos", err)
	}
}

func TestSelectTargetNodeIDRequiresSingleActiveNode(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	_, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    "ws",
		NodeID:          "node-1",
		RuntimeProvider: domain.RuntimeProviderLocal,
		DrainState:      domain.NodeDrainActive,
		TTL:             time.Minute,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	got, err := SelectTargetNodeID(ctx, st, "ws")
	if err != nil {
		t.Fatalf("SelectTargetNodeID() error = %v", err)
	}
	if got != "node-1" {
		t.Fatalf("SelectTargetNodeID() = %q, want node-1", got)
	}

	_, err = st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    "ws",
		NodeID:          "node-2",
		RuntimeProvider: domain.RuntimeProviderLocal,
		DrainState:      domain.NodeDrainActive,
		TTL:             time.Minute,
	})
	if err != nil {
		t.Fatalf("create second node: %v", err)
	}
	if _, err := SelectTargetNodeID(ctx, st, "ws"); ErrorKindOf(err) != ErrorKindConflict {
		t.Fatalf("SelectTargetNodeID() error = %v, want conflict", err)
	}
}

func TestReconcileOnceDefersStalledWorkerFatalOnFirstObservation(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	taskID := "EPIC-2"
	worker := WorkerName("epic-1", taskID)
	createStoppedWorker(t, st, "ws", worker)

	ib := clitest.NewMockIssueBackend()
	ib.ListResult = []backend.IssueData{{
		ID:       taskID,
		Title:    "second task",
		Status:   "in_progress",
		Assignee: worker,
	}}

	r := &Runner{
		store:          st,
		ib:             ib,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic-1",
		maxConcurrency: 1,
	}

	result, err := r.ReconcileOnce(ctx)
	if result.Done {
		t.Fatal("ReconcileOnce done = true, want false")
	}
	if err != nil {
		t.Fatalf("ReconcileOnce error = %v, want nil during first stopped-worker observation", err)
	}
}

func TestReconcileOnceDetectsStoppedDeterministicWorkerAfterRestart(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	taskID := "EPIC-2"
	worker := WorkerName("epic-1", taskID)
	createStoppedWorker(t, st, "ws", worker)

	ib := clitest.NewMockIssueBackend()
	ib.ListResult = []backend.IssueData{{
		ID:       taskID,
		Title:    "second task",
		Status:   "in_progress",
		Assignee: worker,
	}}

	r := &Runner{
		store:          st,
		ib:             ib,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic-1",
		maxConcurrency: 1,
	}

	if _, err := r.ReconcileOnce(ctx); err != nil {
		t.Fatalf("first ReconcileOnce error = %v, want grace pass before fatal stall", err)
	}
	_, err := r.ReconcileOnce(ctx)
	if !errors.Is(err, ErrStalledWorker) {
		t.Fatalf("ReconcileOnce error = %v, want ErrStalledWorker", err)
	}
}

func TestReconcileOnceRetriesOpenTaskWhenSpawnedWorkerMissing(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	taskID := "EPIC-2"
	task := backend.IssueData{ID: taskID, Title: "retry task", Status: "open"}

	ib := clitest.NewMockIssueBackend()
	ib.ReadyResult = []backend.IssueData{task}
	ib.ListResult = []backend.IssueData{task}

	r := &Runner{
		store:                 st,
		ib:                    ib,
		workspace:             "ws",
		parent:                "EPIC-1",
		prefix:                "epic-1",
		role:                  "task",
		maxConcurrency:        1,
		orchestratorSessionID: "lead-session-1",
	}

	result, err := r.ReconcileOnce(ctx)
	if err != nil {
		t.Fatalf("ReconcileOnce error = %v", err)
	}
	if result.Done {
		t.Fatal("ReconcileOnce done = true, want false")
	}
	if result.DispatchedCount != 1 {
		t.Fatalf("DispatchedCount = %d, want 1", result.DispatchedCount)
	}

	newWorker := WorkerName("epic-1", taskID)
	if _, err := st.Agents().Get(ctx, "ws", newWorker); err != nil {
		t.Fatalf("expected retry to create worker %q: %v", newWorker, err)
	}
	cmds, err := st.AgentCommands().List(ctx, "ws", store.AgentCommandFilter{TargetAgentID: newWorker})
	if err != nil {
		t.Fatalf("list agent commands: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Payload["task_id"] != taskID || cmds[0].Payload["parent_session_id"] != "lead-session-1" {
		t.Fatalf("commands = %#v, want one start command for %s with lead session", cmds, taskID)
	}
}

func TestSpawnWorkerPinsConfiguredBackend(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	r := &Runner{
		store:          st,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic-1",
		role:           "task",
		backend:        "codex",
		maxConcurrency: 1,
	}
	task := backend.IssueData{ID: "EPIC-2", Title: "worker backend", Status: "open"}

	if err := r.spawnWorker(ctx, task); err != nil {
		t.Fatalf("spawnWorker() error = %v", err)
	}

	worker := WorkerName("epic-1", task.ID)
	agent, err := st.Agents().Get(ctx, "ws", worker)
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	if agent.Backend != "codex" {
		t.Fatalf("worker backend = %q, want codex", agent.Backend)
	}
}

func TestReconcileOnceSkipsReadyTaskWithLiveStartCommand(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	taskID := "EPIC-2"
	worker := WorkerName("epic-1", taskID)
	task := backend.IssueData{ID: taskID, Title: "queued task", Status: "open"}
	if _, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws",
		TargetAgentID: worker,
		Type:          "start",
		Payload:       map[string]string{"task_id": taskID},
	}); err != nil {
		t.Fatalf("create command: %v", err)
	}

	ib := clitest.NewMockIssueBackend()
	ib.ReadyResult = []backend.IssueData{task}
	ib.ListResult = []backend.IssueData{task}

	r := &Runner{
		store:          st,
		ib:             ib,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic-1",
		role:           "task",
		maxConcurrency: 1,
	}

	result, err := r.ReconcileOnce(ctx)
	if err != nil {
		t.Fatalf("ReconcileOnce error = %v", err)
	}
	if result.Done {
		t.Fatal("ReconcileOnce done = true, want false")
	}
	cmds, err := st.AgentCommands().List(ctx, "ws", store.AgentCommandFilter{TargetAgentID: worker})
	if err != nil {
		t.Fatalf("list agent commands: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("commands = %d, want existing command only", len(cmds))
	}
}

func TestReconcileOnceLiveSessionConsumesConcurrency(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	activeTaskID := "EPIC-2"
	readyTaskID := "EPIC-3"
	activeWorker := WorkerName("epic-1", activeTaskID)
	readyWorker := WorkerName("epic-1", readyTaskID)
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "ws",
		SessionID:    "sess-1",
		AgentID:      activeWorker,
		Kind:         domain.AgentSessionKindTask,
		TaskID:       activeTaskID,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	activeTask := backend.IssueData{ID: activeTaskID, Title: "active task", Status: "in_progress", Assignee: activeWorker}
	readyTask := backend.IssueData{ID: readyTaskID, Title: "ready task", Status: "open"}
	ib := clitest.NewMockIssueBackend()
	ib.ReadyResult = []backend.IssueData{readyTask}
	ib.ListResult = []backend.IssueData{activeTask, readyTask}

	r := &Runner{
		store:          st,
		ib:             ib,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic-1",
		role:           "task",
		maxConcurrency: 1,
	}

	result, err := r.ReconcileOnce(ctx)
	if err != nil {
		t.Fatalf("ReconcileOnce error = %v", err)
	}
	if result.Done {
		t.Fatal("ReconcileOnce done = true, want false")
	}
	cmds, err := st.AgentCommands().List(ctx, "ws", store.AgentCommandFilter{TargetAgentID: readyWorker})
	if err != nil {
		t.Fatalf("list agent commands: %v", err)
	}
	if len(cmds) != 0 {
		t.Fatalf("commands = %d, want no dispatch while active session consumes cap", len(cmds))
	}
}

func TestReconcileOnceReportsOnlyBlockedChildrenAsTerminal(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	blockedTask := backend.IssueData{ID: "EPIC-2", Title: "blocked task", Status: "blocked"}

	ib := clitest.NewMockIssueBackend()
	ib.BlockedResult = []backend.IssueData{blockedTask}
	ib.ListResult = []backend.IssueData{blockedTask}

	r := &Runner{
		store:          st,
		ib:             ib,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic-1",
		role:           "task",
		maxConcurrency: 1,
	}

	result, err := r.ReconcileOnce(ctx)
	if err != nil {
		t.Fatalf("ReconcileOnce error = %v", err)
	}
	if result.Done {
		t.Fatal("Done = true, want false because blocked child work still needs user action")
	}
	if !result.Blocked {
		t.Fatal("Blocked = false, want true when only blocked children remain")
	}
	if result.BlockedCount != 1 || result.ActiveWorkers != 0 || result.ReadyCount != 0 {
		t.Fatalf("result = %+v, want blocked=1 active=0 ready=0", result)
	}
}

func TestRunLoopExitsWhenOnlyBlockedChildrenRemain(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	blockedTask := backend.IssueData{ID: "EPIC-2", Title: "blocked task", Status: "blocked"}

	ib := clitest.NewMockIssueBackend()
	ib.BlockedResult = []backend.IssueData{blockedTask}
	ib.ListResult = []backend.IssueData{blockedTask}

	var out bytes.Buffer
	r := &Runner{
		store:          st,
		ib:             ib,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic-1",
		role:           "task",
		maxConcurrency: 1,
		interval:       time.Hour,
		out:            &out,
	}

	if err := r.RunLoop(ctx); err != nil {
		t.Fatalf("RunLoop error = %v", err)
	}
	if !strings.Contains(out.String(), "blocked") || !strings.Contains(out.String(), "EPIC-2") {
		t.Fatalf("RunLoop output = %q, want blocked terminal summary with task id", out.String())
	}
}

func createStoppedWorker(t *testing.T, st store.Store, workspace, name string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: workspace,
		Name:         name,
		RoleName:     "task",
		Mode:         domain.AgentModeEphemeral,
		Parent:       "EPIC-1",
		DesiredState: domain.AgentDesiredStopped,
	}); err != nil {
		t.Fatalf("create stopped worker: %v", err)
	}
	stopped := domain.AgentStateStopped
	if _, err := st.Agents().Update(ctx, workspace, name, store.AgentUpdate{State: &stopped}); err != nil {
		t.Fatalf("mark worker stopped: %v", err)
	}
}
