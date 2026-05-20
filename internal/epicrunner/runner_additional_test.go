package epicrunner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestRunnerValidationAndFormattingBranches(t *testing.T) {
	ctx := context.Background()
	if ErrorKindOf(validateRunnerConfig(ctx, RunnerConfig{})) != ErrorKindUnavailable {
		t.Fatal("nil store should be unavailable")
	}
	if ErrorKindOf(validateRunnerConfig(ctx, RunnerConfig{Store: newTestStore(t)})) != ErrorKindUnavailable {
		t.Fatal("nil issue backend should be unavailable")
	}
	ib := clitest.NewMockIssueBackend()
	if ErrorKindOf(validateRunnerConfig(ctx, RunnerConfig{Store: newTestStore(t), IssueBackend: ib})) != ErrorKindValidation {
		t.Fatal("missing workspace should be validation")
	}
	if ErrorKindOf(validateRunnerConfig(ctx, RunnerConfig{Store: newTestStore(t), IssueBackend: ib, WorkspaceKey: "ws"})) != ErrorKindValidation {
		t.Fatal("missing epic should be validation")
	}

	st := &noCommandStore{Store: newTestStore(t)}
	err := validateRunnerConfig(ctx, RunnerConfig{
		Store:               st,
		IssueBackend:        ib,
		WorkspaceKey:        "ws",
		EpicID:              "EPIC-1",
		RequireCommandStore: true,
	})
	if ErrorKindOf(err) != ErrorKindUnavailable || !strings.Contains(err.Error(), "agent command store") {
		t.Fatalf("RequireCommandStore err = %v", err)
	}

	var out bytes.Buffer
	r := &Runner{
		workspace:      "ws",
		parent:         "EPIC-1",
		role:           "task",
		prefix:         "epic",
		maxConcurrency: 2,
		out:            &out,
	}
	r.PrintHeader()
	if !strings.Contains(out.String(), "orchestrator:     (none") {
		t.Fatalf("header without orchestrator = %q", out.String())
	}
	r.targetNodeID = "node-1"
	out.Reset()
	r.PrintHeader()
	if !strings.Contains(out.String(), "target node:      node-1") {
		t.Fatalf("header with target node = %q", out.String())
	}

	tasks := []backend.IssueData{
		{Title: "no id"},
		{ID: "T-1", Title: "one"},
		{ID: "T-2"},
		{ID: "T-3"},
		{ID: "T-4"},
		{ID: "T-5"},
		{ID: "T-6"},
	}
	summary := blockedTaskSummary(tasks)
	if strings.Contains(summary, "no id") || !strings.Contains(summary, "T-1 (one)") || !strings.Contains(summary, "+2 more") {
		t.Fatalf("blocked summary = %q", summary)
	}
	if got := WorkerName(strings.Repeat("!", 120), ""); !strings.HasPrefix(got, "task-") {
		t.Fatalf("WorkerName empty sanitized prefix = %q", got)
	}
	if got := WorkerName(strings.Repeat("x", 120), "TASK"); len(got) > 63 {
		t.Fatalf("WorkerName long prefix length = %d", len(got))
	}
}

func TestNewRunnerSelectsTargetNodeAndRunLoopExitBranches(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createLead(t, st, "ws", "nova", "", "orch-1")
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey: "ws",
		NodeID:       "node-1",
		DrainState:   domain.NodeDrainActive,
		TTL:          time.Minute,
	}); err != nil {
		t.Fatalf("create active node: %v", err)
	}
	ib := clitest.NewMockIssueBackend()
	ib.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{ID: "EPIC-1", IssueType: "epic"}}

	r, result, err := NewRunner(ctx, RunnerConfig{
		Store:                 st,
		IssueBackend:          ib,
		WorkspaceKey:          "ws",
		EpicID:                "EPIC-1",
		LeadName:              "nova",
		OrchestratorSessionID: "orch-1",
		MutateLead:            true,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if r.targetNodeID != "node-1" || result == nil || result.LeadName != "nova" {
		t.Fatalf("runner target=%q result=%+v", r.targetNodeID, result)
	}

	var out bytes.Buffer
	r = &Runner{
		store:          newTestStore(t),
		ib:             clitest.NewMockIssueBackend(),
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic",
		maxConcurrency: 1,
		interval:       time.Millisecond,
		out:            &out,
	}
	if err := r.RunLoop(ctx); err != nil {
		t.Fatalf("RunLoop drained: %v", err)
	}
	if !strings.Contains(out.String(), "drained") {
		t.Fatalf("drained output = %q", out.String())
	}

	blockedIB := clitest.NewMockIssueBackend()
	blockedIB.BlockedResult = []backend.IssueData{{ID: "TASK-1", Status: "open"}}
	blockedIB.ListResult = []backend.IssueData{{ID: "TASK-1", Status: "open"}}
	r = &Runner{
		store:          newTestStore(t),
		ib:             blockedIB,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic",
		maxConcurrency: 1,
		interval:       time.Millisecond,
		out:            &bytes.Buffer{},
	}
	if err := r.RunLoop(ctx); err != nil {
		t.Fatalf("RunLoop blocked: %v", err)
	}

	activeStore := newTestStore(t)
	activeTask := backend.IssueData{ID: "TASK-ACTIVE", Status: "in_progress"}
	activeWorker := WorkerName("epic", activeTask.ID)
	activeTask.Assignee = activeWorker
	if _, err := activeStore.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws",
		Name:         activeWorker,
		RoleName:     "task",
		Parent:       "EPIC-1",
		Mode:         domain.AgentModeEphemeral,
	}); err != nil {
		t.Fatalf("create active worker: %v", err)
	}
	if _, err := activeStore.Agents().Update(ctx, "ws", activeWorker, store.AgentUpdate{State: statePtr(domain.AgentStateActive)}); err != nil {
		t.Fatalf("activate worker: %v", err)
	}
	activeIB := clitest.NewMockIssueBackend()
	activeIB.ListResult = []backend.IssueData{activeTask}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	r = &Runner{
		store:          activeStore,
		ib:             activeIB,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic",
		maxConcurrency: 1,
		interval:       time.Millisecond,
		out:            &bytes.Buffer{},
	}
	if err := r.RunLoop(canceled); err != nil {
		t.Fatalf("RunLoop canceled: %v", err)
	}

	stalledStore := newTestStore(t)
	stalledTask := backend.IssueData{ID: "TASK-STALLED", Title: "stalled", Status: "in_progress"}
	stalledWorker := WorkerName("epic", stalledTask.ID)
	stalledTask.Assignee = stalledWorker
	if _, err := stalledStore.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws",
		Name:         stalledWorker,
		RoleName:     "task",
		Parent:       "EPIC-1",
		Mode:         domain.AgentModeEphemeral,
	}); err != nil {
		t.Fatalf("create stopped worker: %v", err)
	}
	stalledIB := clitest.NewMockIssueBackend()
	stalledIB.ListResult = []backend.IssueData{stalledTask}
	r = &Runner{
		store:          stalledStore,
		ib:             stalledIB,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic",
		maxConcurrency: 1,
		interval:       time.Millisecond,
		out:            &bytes.Buffer{},
		errOut:         &bytes.Buffer{},
	}
	if err := r.RunLoop(ctx); !errors.Is(err, ErrStalledWorker) {
		t.Fatalf("RunLoop stalled err = %v", err)
	}
}

func TestDispatchReadyTasksSkipBranches(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	activeTask := "T-ACTIVE"
	activeName := WorkerName("epic", activeTask)
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws",
		Name:         activeName,
		RoleName:     "task",
		Parent:       "EPIC-1",
		Mode:         domain.AgentModeEphemeral,
	}); err != nil {
		t.Fatalf("create active worker: %v", err)
	}
	if _, err := st.Agents().Update(ctx, "ws", activeName, store.AgentUpdate{State: statePtr(domain.AgentStateActive)}); err != nil {
		t.Fatalf("activate worker: %v", err)
	}

	var out bytes.Buffer
	r := &Runner{
		store:          st,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic",
		role:           "task",
		maxConcurrency: 2,
		dryRun:         true,
		out:            &out,
	}
	dispatched, failures := r.dispatchReadyTasks(ctx, []backend.IssueData{
		{ID: "T-INPROGRESS", Status: "in_progress"},
		{ID: "T-CLOSED", Status: "closed"},
		{ID: "T-DEFERRED", Status: "deferred"},
		{ID: activeTask, Status: "open"},
		{ID: "T-DISPATCH", Title: "dispatch me", Status: "open"},
		{ID: "T-SKIPPED-BY-SLOTS", Status: "open"},
	}, 1)
	if len(failures) != 0 {
		t.Fatalf("failures = %#v", failures)
	}
	if len(dispatched) != 1 || dispatched[0].TaskID != "T-DISPATCH" {
		t.Fatalf("dispatched = %#v", dispatched)
	}
	if !strings.Contains(out.String(), "DRY-RUN would spawn") {
		t.Fatalf("dispatch output = %q", out.String())
	}

	if active, err := r.workerActiveForTask(ctx, "", "T-EMPTY"); err != nil || active {
		t.Fatalf("empty worker active=%t err=%v", active, err)
	}
}

func TestSpawnWorkerCreateExistingAndCommandBranches(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	r := &Runner{
		store:                 st,
		workspace:             "ws",
		parent:                "EPIC-1",
		prefix:                "epic",
		role:                  "task",
		backend:               "codex",
		orchestratorSessionID: "orch-1",
		targetNodeID:          "node-1",
	}
	task := backend.IssueData{ID: "TASK-1", Title: "Build it", Status: "open"}
	name := WorkerName("epic", "TASK-1")
	if err := r.spawnWorker(ctx, task); err != nil {
		t.Fatalf("spawnWorker: %v", err)
	}
	agent, err := st.Agents().Get(ctx, "ws", name)
	if err != nil {
		t.Fatalf("get spawned worker: %v", err)
	}
	if agent.Mode != domain.AgentModeEphemeral || agent.DesiredState != domain.AgentDesiredStopped {
		t.Fatalf("spawned agent = %+v", agent)
	}
	cmds, err := st.AgentCommands().List(ctx, "ws", store.AgentCommandFilter{TargetAgentID: name})
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(cmds) != 1 || cmds[0].TargetNodeID != "node-1" || cmds[0].Payload["parent_session_id"] != "orch-1" {
		t.Fatalf("queued commands = %+v", cmds)
	}

	if _, err := r.createOrLoadWorkerAgent(ctx, name); err != nil {
		t.Fatalf("createOrLoad existing valid worker: %v", err)
	}
	badName := WorkerName("epic", "TASK-BAD")
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws",
		Name:         badName,
		RoleName:     "task",
		Parent:       "OTHER",
		Mode:         domain.AgentModeService,
	}); err != nil {
		t.Fatalf("create conflicting worker: %v", err)
	}
	if _, err := r.createOrLoadWorkerAgent(ctx, badName); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("conflicting existing worker err = %v", err)
	}

	var out bytes.Buffer
	noCommand := &Runner{
		store:     &noCommandStore{Store: st},
		workspace: "ws",
		parent:    "EPIC-1",
		prefix:    "epic",
		role:      "task",
		out:       &out,
	}
	if err := noCommand.spawnWorker(ctx, backend.IssueData{ID: "TASK-2", Title: "No command"}); err != nil {
		t.Fatalf("spawnWorker no command store: %v", err)
	}
	if !strings.Contains(out.String(), "no command channel") {
		t.Fatalf("no-command output = %q", out.String())
	}
}

func TestReconcileOnceBlockedDrainedAndDispatchFailureBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("drained", func(t *testing.T) {
		var out bytes.Buffer
		r := &Runner{
			store:          newTestStore(t),
			ib:             clitest.NewMockIssueBackend(),
			workspace:      "ws",
			parent:         "EPIC-1",
			prefix:         "epic",
			maxConcurrency: 1,
			out:            &out,
		}
		result, err := r.ReconcileOnce(ctx)
		if err != nil {
			t.Fatalf("ReconcileOnce drained: %v", err)
		}
		if !result.Done || result.ActiveWorkers != 0 {
			t.Fatalf("drained result = %+v", result)
		}
	})

	t.Run("blocked", func(t *testing.T) {
		ib := clitest.NewMockIssueBackend()
		ib.BlockedResult = []backend.IssueData{{ID: "TASK-1", Title: "blocked", Status: "open"}}
		ib.ListResult = []backend.IssueData{{ID: "TASK-1", Title: "blocked", Status: "open"}}
		var out bytes.Buffer
		r := &Runner{
			store:          newTestStore(t),
			ib:             ib,
			workspace:      "ws",
			parent:         "EPIC-1",
			prefix:         "epic",
			maxConcurrency: 1,
			out:            &out,
		}
		result, err := r.ReconcileOnce(ctx)
		if err != nil {
			t.Fatalf("ReconcileOnce blocked: %v", err)
		}
		if !result.Blocked || !strings.Contains(out.String(), "blocked with 1 child") {
			t.Fatalf("blocked result=%+v output=%q", result, out.String())
		}
	})

	t.Run("dispatch failure", func(t *testing.T) {
		st := newTestStore(t)
		task := backend.IssueData{ID: "TASK-1", Title: "spawn me", Status: "open"}
		worker := WorkerName("epic", task.ID)
		if _, err := st.Agents().Create(ctx, store.AgentCreate{
			WorkspaceKey: "ws",
			Name:         worker,
			RoleName:     "task",
			Parent:       "OTHER",
			Mode:         domain.AgentModeService,
		}); err != nil {
			t.Fatalf("create conflicting worker: %v", err)
		}

		ib := clitest.NewMockIssueBackend()
		ib.ReadyResult = []backend.IssueData{task}
		ib.ListResult = []backend.IssueData{task}
		r := &Runner{
			store:               st,
			ib:                  ib,
			workspace:           "ws",
			parent:              "EPIC-1",
			prefix:              "epic",
			maxConcurrency:      1,
			failOnDispatchError: true,
			out:                 &bytes.Buffer{},
			errOut:              &bytes.Buffer{},
		}
		result, err := r.ReconcileOnce(ctx)
		if ErrorKindOf(err) != ErrorKindInternal || result.DispatchedCount != 0 {
			t.Fatalf("dispatch failure result=%+v err=%v", result, err)
		}
	})
}

func TestRunnerWorkerLivenessAndStalledBranches(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	worker := WorkerName("epic", "TASK-1")
	r := &Runner{
		store:            st,
		workspace:        "ws",
		parent:           "EPIC-1",
		prefix:           "epic",
		maxConcurrency:   2,
		stalledTaskTicks: make(map[string]int),
	}

	if _, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws",
		TargetAgentID: worker,
		Type:          "start",
		Payload:       map[string]string{"task_id": "TASK-1"},
	}); err != nil {
		t.Fatalf("create command: %v", err)
	}
	if active, err := r.workerActiveForTask(ctx, worker, "TASK-1"); err != nil || !active {
		t.Fatalf("workerActiveForTask command active=%t err=%v", active, err)
	}

	openChildren := []backend.IssueData{{ID: "TASK-1", Title: "one", Status: "in_progress", Assignee: worker}}
	if stalled := r.stalledTasks(ctx, openChildren); len(stalled) != 0 {
		t.Fatalf("stalled with live command = %+v", stalled)
	}

	st = newTestStore(t)
	r.store = st
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws",
		Name:         worker,
		RoleName:     "task",
		Parent:       "EPIC-1",
		Mode:         domain.AgentModeEphemeral,
	}); err != nil {
		t.Fatalf("create stopped worker: %v", err)
	}
	if stalled := r.stalledTasks(ctx, openChildren); len(stalled) != 0 {
		t.Fatalf("first stalled tick = %+v", stalled)
	}
	stalled := r.stalledTasks(ctx, openChildren)
	if len(stalled) != 1 || !strings.Contains(stalled[0], "TASK-1") {
		t.Fatalf("second stalled tick = %+v", stalled)
	}
	r.stalledTasks(ctx, []backend.IssueData{{ID: "OTHER", Status: "open"}})
	if _, ok := r.stalledTaskTicks["TASK-1"]; ok {
		t.Fatalf("stalled tick was not pruned: %+v", r.stalledTaskTicks)
	}
}

func TestEnsureLocalWorkerWorktreesAdditionalBranches(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	agent := domain.Agent{WorkspaceKey: "ws", Name: "worker-a"}

	t.Run("no local path is a no-op", func(t *testing.T) {
		st := newTestStore(t)
		if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
			Workspaces: map[string]bootstrap.WorkspaceLocalState{"ws": {}},
		}); err != nil {
			t.Fatalf("save state cache: %v", err)
		}
		r := &Runner{store: st}
		if err := r.ensureLocalWorkerWorktrees(ctx, agent); err != nil {
			t.Fatalf("ensureLocalWorkerWorktrees no path: %v", err)
		}
	})

	t.Run("local path with no repos reports workspace context", func(t *testing.T) {
		st := newTestStore(t)
		if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
			Workspaces: map[string]bootstrap.WorkspaceLocalState{"ws": {Path: t.TempDir()}},
		}); err != nil {
			t.Fatalf("save state cache: %v", err)
		}
		r := &Runner{store: st}
		err := r.ensureLocalWorkerWorktrees(ctx, agent)
		if err == nil || !strings.Contains(err.Error(), "workspace ws has no repos") {
			t.Fatalf("ensureLocalWorkerWorktrees no repos err = %v", err)
		}
	})

	t.Run("creates worktree and remembers local agent path", func(t *testing.T) {
		st := newTestStore(t)
		workspaceDir := t.TempDir()
		repoDir := filepath.Join(workspaceDir, "api")
		initRunnerGitRepo(t, repoDir)
		if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "ws", Name: "api"}); err != nil {
			t.Fatalf("create repo: %v", err)
		}
		if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
			Workspaces: map[string]bootstrap.WorkspaceLocalState{
				"ws": {
					Path:  workspaceDir,
					Repos: map[string]string{"api": repoDir},
				},
			},
		}); err != nil {
			t.Fatalf("save state cache: %v", err)
		}

		r := &Runner{store: st}
		if err := r.ensureLocalWorkerWorktrees(ctx, agent); err != nil {
			t.Fatalf("ensureLocalWorkerWorktrees create: %v", err)
		}

		want := filepath.Join(workspaceDir, "worktrees", "api", "worker-a")
		if _, err := os.Stat(filepath.Join(want, ".git")); err != nil {
			t.Fatalf("worktree .git missing: %v", err)
		}
		sc, err := bootstrap.LoadStateCache()
		if err != nil {
			t.Fatalf("load state cache: %v", err)
		}
		if got := sc.Workspaces["ws"].Agents["worker-a"].Worktree; got != want {
			t.Fatalf("remembered worktree = %q, want %q", got, want)
		}
	})
}

func TestSelectTargetNodeIDAdditionalBranches(t *testing.T) {
	ctx := context.Background()
	noNodeStore := &noNodesStore{Store: newTestStore(t)}
	if got, err := SelectTargetNodeID(ctx, noNodeStore, "ws"); err != nil || got != "" {
		t.Fatalf("SelectTargetNodeID no node store got=%q err=%v", got, err)
	}

	st := newTestStore(t)
	if _, err := SelectTargetNodeID(ctx, st, "ws"); ErrorKindOf(err) != ErrorKindUnavailable {
		t.Fatalf("SelectTargetNodeID no active err = %v", err)
	}
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{WorkspaceKey: "ws", NodeID: "draining", DrainState: domain.NodeDrainDraining, TTL: time.Minute}); err != nil {
		t.Fatalf("create draining node: %v", err)
	}
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{WorkspaceKey: "ws", NodeID: "active-1", DrainState: domain.NodeDrainActive, TTL: time.Minute}); err != nil {
		t.Fatalf("create active node: %v", err)
	}
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{WorkspaceKey: "ws", NodeID: "active-2", DrainState: domain.NodeDrainActive, TTL: time.Minute}); err != nil {
		t.Fatalf("create second active node: %v", err)
	}
	if _, err := SelectTargetNodeID(ctx, st, "ws"); ErrorKindOf(err) != ErrorKindConflict {
		t.Fatalf("SelectTargetNodeID multiple active err = %v", err)
	}
}

func TestLoadReconcileSnapshotBackendErrorBranches(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name string
		ib   *clitest.MockIssueBackend
	}{
		{name: "ready", ib: &clitest.MockIssueBackend{ReadyErr: errors.New("ready failed")}},
		{name: "blocked", ib: &clitest.MockIssueBackend{BlockedErr: errors.New("blocked failed")}},
		{name: "list", ib: &clitest.MockIssueBackend{ListErr: errors.New("list failed")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := &Runner{store: newTestStore(t), ib: tt.ib, workspace: "ws", parent: "EPIC-1"}
			if _, err := r.loadReconcileSnapshot(ctx); ErrorKindOf(err) != ErrorKindInternal {
				t.Fatalf("loadReconcileSnapshot err = %v", err)
			}
		})
	}
}

type noCommandStore struct {
	store.Store
}

func (s *noCommandStore) AgentCommands() store.AgentCommandStore { return nil }

type noNodesStore struct {
	store.Store
}

func (s *noNodesStore) Nodes() store.NodeStore { return nil }

func statePtr(s domain.AgentState) *domain.AgentState { return &s }

func initRunnerGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runnerGit(t, dir, "init")
	runnerGit(t, dir, "config", "user.name", "Runner Test")
	runnerGit(t, dir, "config", "user.email", "runner@example.test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runnerGit(t, dir, "add", "README.md")
	runnerGit(t, dir, "commit", "-m", "init")
}

func runnerGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // tests pass fixed git arguments.
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}
