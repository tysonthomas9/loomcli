package epic

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestWorkerNameAddsHashToAvoidSanitizedCollisions(t *testing.T) {
	a := workerName("epic", "TASK/1")
	b := workerName("epic", "TASK:1")
	if a == b {
		t.Fatalf("workerName collision: %q", a)
	}
	if len(a) > 63 || len(b) > 63 {
		t.Fatalf("workerName length = %d/%d, want <= 63", len(a), len(b))
	}
	if strings.ContainsAny(a, "/:") || strings.ContainsAny(b, "/:") {
		t.Fatalf("workerName contains unsanitized chars: %q %q", a, b)
	}
}

func TestBindLeadAgentAssignsEmptyParent(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createTestLead(t, st, "ws", "nova", "", "")

	leadName, orch, err := bindLeadAgent(ctx, st, "ws", "nova", "EPIC-1", "session-1", true)
	if err != nil {
		t.Fatalf("bindLeadAgent() error = %v", err)
	}
	if leadName != "nova" || orch != "session-1" {
		t.Fatalf("bindLeadAgent() = (%q, %q), want nova/session-1", leadName, orch)
	}
	got, err := st.Agents().Get(ctx, "ws", "nova")
	if err != nil {
		t.Fatalf("get lead: %v", err)
	}
	if got.Parent != "EPIC-1" {
		t.Fatalf("lead parent = %q, want EPIC-1", got.Parent)
	}
	if got.OrchestratorSessionID != "session-1" {
		t.Fatalf("lead orchestrator = %q, want session-1", got.OrchestratorSessionID)
	}
}

func TestBindLeadAgentAllowsSameParent(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createTestLead(t, st, "ws", "nova", "EPIC-1", "session-1")

	_, orch, err := bindLeadAgent(ctx, st, "ws", "nova", "EPIC-1", "", true)
	if err != nil {
		t.Fatalf("bindLeadAgent() error = %v", err)
	}
	if orch != "session-1" {
		t.Fatalf("orchestrator = %q, want existing session-1", orch)
	}
}

func TestBindLeadAgentRejectsDifferentParent(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createTestLead(t, st, "ws", "nova", "EPIC-1", "")

	_, _, err := bindLeadAgent(ctx, st, "ws", "nova", "EPIC-2", "", true)
	if err == nil {
		t.Fatal("bindLeadAgent() error = nil, want conflict")
	}
	if !strings.Contains(err.Error(), "already running epic EPIC-1") {
		t.Fatalf("error = %v, want active epic message", err)
	}
}

func TestBindLeadAgentRejectsNonLeadRole(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws",
		Name:         "worker",
		RoleName:     "task",
	}); err != nil {
		t.Fatalf("create task agent: %v", err)
	}

	_, _, err := bindLeadAgent(ctx, st, "ws", "worker", "EPIC-1", "", true)
	if err == nil {
		t.Fatal("bindLeadAgent() error = nil, want non-lead role error")
	}
	if !strings.Contains(err.Error(), "requires a lead agent") {
		t.Fatalf("error = %v, want lead role message", err)
	}
}

func TestBindLeadAgentDryRunDoesNotAssignParent(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createTestLead(t, st, "ws", "nova", "", "")

	if _, _, err := bindLeadAgent(ctx, st, "ws", "nova", "EPIC-1", "", false); err != nil {
		t.Fatalf("bindLeadAgent() error = %v", err)
	}
	got, err := st.Agents().Get(ctx, "ws", "nova")
	if err != nil {
		t.Fatalf("get lead: %v", err)
	}
	if got.Parent != "" {
		t.Fatalf("lead parent = %q, want empty in dry-run", got.Parent)
	}
}

func TestBindLeadAgentMissingLead(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	_, _, err := bindLeadAgent(ctx, st, "ws", "missing", "EPIC-1", "", true)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("bindLeadAgent() error = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("error = %v, want not found message", err)
	}
}

func TestBindLeadAgentSerializesConcurrentParentClaims(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createTestLead(t, st, "ws", "nova", "", "")

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	parents := []string{"EPIC-1", "EPIC-2"}
	for _, parent := range parents {
		parent := parent
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := bindLeadAgent(ctx, st, "ws", "nova", parent, "", true)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	successes := 0
	conflicts := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case strings.Contains(err.Error(), "already running epic"):
			conflicts++
		default:
			t.Fatalf("unexpected bind error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes/conflicts = %d/%d, want 1/1", successes, conflicts)
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
	got, err := selectTargetNodeID(ctx, st, "ws")
	if err != nil {
		t.Fatalf("selectTargetNodeID() error = %v", err)
	}
	if got != "node-1" {
		t.Fatalf("selectTargetNodeID() = %q, want node-1", got)
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
	if _, err := selectTargetNodeID(ctx, st, "ws"); err == nil {
		t.Fatal("selectTargetNodeID() error = nil, want multiple-node error")
	}
}

func TestReconcileOnceDefersStalledWorkerFatalOnFirstObservation(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	taskID := "EPIC-2"
	worker := workerName("epic-1", taskID)
	createStoppedWorker(t, st, "ws", worker)

	ib := clitest.NewMockIssueBackend()
	ib.ListResult = []backend.IssueData{{
		ID:       taskID,
		Title:    "second task",
		Status:   "in_progress",
		Assignee: worker,
	}}

	r := &runner{
		store:          st,
		ib:             ib,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic-1",
		maxConcurrency: 1,
	}

	done, err := r.reconcileOnce(ctx)
	if done {
		t.Fatal("reconcileOnce done = true, want false")
	}
	if err != nil {
		t.Fatalf("reconcileOnce error = %v, want nil during first stopped-worker observation", err)
	}
}

func TestReconcileOnceDetectsStoppedDeterministicWorkerAfterRestart(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	taskID := "EPIC-2"
	worker := workerName("epic-1", taskID)
	createStoppedWorker(t, st, "ws", worker)

	ib := clitest.NewMockIssueBackend()
	ib.ListResult = []backend.IssueData{{
		ID:       taskID,
		Title:    "second task",
		Status:   "in_progress",
		Assignee: worker,
	}}

	r := &runner{
		store:          st,
		ib:             ib,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic-1",
		maxConcurrency: 1,
	}

	if _, err := r.reconcileOnce(ctx); err != nil {
		t.Fatalf("first reconcileOnce error = %v, want grace pass before fatal stall", err)
	}
	_, err := r.reconcileOnce(ctx)
	if !errors.Is(err, errStalledWorker) {
		t.Fatalf("reconcileOnce error = %v, want errStalledWorker", err)
	}
}

func TestAcquireLeadBindLockTimesOutWhenHeld(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	unlock, err := acquireLeadBindLockWithTimeout("ws", "nova", time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("initial acquireLeadBindLockWithTimeout() error = %v", err)
	}
	defer unlock()

	_, err = acquireLeadBindLockWithTimeout("ws", "nova", 10*time.Millisecond, time.Millisecond)
	if err == nil {
		t.Fatal("second acquireLeadBindLockWithTimeout() error = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want timeout message", err)
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

	r := &runner{
		store:          st,
		ib:             ib,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic-1",
		role:           "task",
		maxConcurrency: 1,
	}

	done, err := r.reconcileOnce(ctx)
	if err != nil {
		t.Fatalf("reconcileOnce error = %v", err)
	}
	if done {
		t.Fatal("reconcileOnce done = true, want false")
	}

	newWorker := workerName("epic-1", taskID)
	if _, err := st.Agents().Get(ctx, "ws", newWorker); err != nil {
		t.Fatalf("expected retry to create worker %q: %v", newWorker, err)
	}
	cmds, err := st.AgentCommands().List(ctx, "ws", store.AgentCommandFilter{TargetAgentID: newWorker})
	if err != nil {
		t.Fatalf("list agent commands: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Payload["task_id"] != taskID {
		t.Fatalf("commands = %#v, want one start command for %s", cmds, taskID)
	}
}

func TestSpawnWorkerPinsConfiguredBackend(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	r := &runner{
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

	worker := workerName("epic-1", task.ID)
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
	worker := workerName("epic-1", taskID)
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

	r := &runner{
		store:          st,
		ib:             ib,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic-1",
		role:           "task",
		maxConcurrency: 1,
	}

	done, err := r.reconcileOnce(ctx)
	if err != nil {
		t.Fatalf("reconcileOnce error = %v", err)
	}
	if done {
		t.Fatal("reconcileOnce done = true, want false")
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
	activeWorker := workerName("epic-1", activeTaskID)
	readyWorker := workerName("epic-1", readyTaskID)
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

	r := &runner{
		store:          st,
		ib:             ib,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic-1",
		role:           "task",
		maxConcurrency: 1,
	}

	done, err := r.reconcileOnce(ctx)
	if err != nil {
		t.Fatalf("reconcileOnce error = %v", err)
	}
	if done {
		t.Fatal("reconcileOnce done = true, want false")
	}
	cmds, err := st.AgentCommands().List(ctx, "ws", store.AgentCommandFilter{TargetAgentID: readyWorker})
	if err != nil {
		t.Fatalf("list agent commands: %v", err)
	}
	if len(cmds) != 0 {
		t.Fatalf("commands = %d, want no dispatch while active session consumes cap", len(cmds))
	}
}

func newTestStore(t *testing.T) store.Store {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	return memstore.New()
}

func createStoppedWorker(t *testing.T, st store.Store, workspace, name string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: workspace,
		Name:         name,
		RoleName:     "task",
		Mode:         domain.AgentModeEphemeral,
		DesiredState: domain.AgentDesiredStopped,
	}); err != nil {
		t.Fatalf("create stopped worker: %v", err)
	}
	stopped := domain.AgentStateStopped
	if _, err := st.Agents().Update(ctx, workspace, name, store.AgentUpdate{State: &stopped}); err != nil {
		t.Fatalf("mark worker stopped: %v", err)
	}
}

func createTestLead(t *testing.T, st store.Store, workspace, name, parent, orchestrator string) {
	t.Helper()
	_, err := st.Agents().Create(context.Background(), store.AgentCreate{
		WorkspaceKey:          workspace,
		Name:                  name,
		RoleName:              "lead",
		Parent:                parent,
		OrchestratorSessionID: orchestrator,
	})
	if err != nil {
		t.Fatalf("create lead: %v", err)
	}
}
