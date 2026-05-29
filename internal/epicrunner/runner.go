package epicrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
	workflowpkg "github.com/tysonthomas9/loomcli/internal/workflow"
)

const stalledWorkerFatalConsecutiveTicks = 2

// ErrStalledWorker reports an in-progress task assigned to a stopped worker.
var ErrStalledWorker = errors.New("task worker stopped before completing its task")

// Runner is one epic-run reconciliation loop.
type Runner struct {
	store                 store.Store
	ib                    backend.IssueBackend
	workspace             string
	parent                string
	leadName              string
	role                  string
	backend               string
	prefix                string
	maxConcurrency        int
	interval              time.Duration
	orchestratorSessionID string
	targetNodeID          string
	workflowRunID         string
	dryRun                bool
	failOnDispatchError   bool
	prepareWorktrees      bool
	stalledTaskTicks      map[string]int
	out                   io.Writer
	errOut                io.Writer
}

// NewRunner validates prerequisites, optionally binds the lead to the epic,
// and returns a runner ready to reconcile work.
//
//nolint:funlen // Runner construction performs ordered preflight, binding, and optional mutation in one place.
func NewRunner(ctx context.Context, cfg RunnerConfig) (*Runner, *StartResult, error) {
	cfg.normalize()
	if err := validateRunnerConfig(ctx, cfg); err != nil {
		return nil, nil, err
	}

	nodeID := cfg.TargetNodeID
	if nodeID == "" && cfg.Store.AgentCommands() != nil && !cfg.DryRun {
		var err error
		nodeID, err = SelectTargetNodeID(ctx, cfg.Store, cfg.WorkspaceKey)
		if err != nil {
			return nil, nil, err
		}
	}

	result, err := Start(ctx, cfg.Store, StartInput{
		WorkspaceKey:          cfg.WorkspaceKey,
		EpicID:                cfg.EpicID,
		LeadName:              cfg.LeadName,
		OrchestratorSessionID: cfg.OrchestratorSessionID,
		Mutate:                cfg.MutateLead,
	})
	if err != nil {
		return nil, nil, err
	}
	if result != nil {
		cfg.LeadName = result.LeadName
		cfg.OrchestratorSessionID = result.OrchestratorSessionID
	}
	workflowRunID, err := ensureEpicWorkflowRun(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}

	r := &Runner{
		store:                 cfg.Store,
		ib:                    cfg.IssueBackend,
		workspace:             cfg.WorkspaceKey,
		parent:                cfg.EpicID,
		leadName:              cfg.LeadName,
		role:                  cfg.Role,
		backend:               cfg.Backend,
		prefix:                cfg.WorkerPrefix,
		maxConcurrency:        cfg.MaxConcurrency,
		interval:              cfg.Interval,
		orchestratorSessionID: cfg.OrchestratorSessionID,
		targetNodeID:          nodeID,
		workflowRunID:         workflowRunID,
		dryRun:                cfg.DryRun,
		failOnDispatchError:   cfg.FailOnDispatchError,
		prepareWorktrees:      cfg.PrepareWorktrees,
		stalledTaskTicks:      make(map[string]int),
		out:                   cfg.Out,
		errOut:                cfg.ErrOut,
	}
	return r, result, nil
}

// PrintHeader writes the runner configuration to Out.
func (r *Runner) PrintHeader() {
	r.ensureWriters()
	r.writeln("Epic runner")
	r.writef("  workspace:        %s\n", r.workspace)
	r.writef("  parent epic:      %s\n", r.parent)
	if r.leadName != "" {
		r.writef("  lead agent:       %s\n", r.leadName)
	}
	r.writef("  role:             %s\n", r.role)
	if r.backend != "" {
		r.writef("  backend:          %s\n", r.backend)
	}
	r.writef("  worker prefix:    %s\n", r.prefix)
	r.writef("  max concurrency:  %d\n", r.maxConcurrency)
	r.writef("  interval:         %s\n", r.interval)
	if r.orchestratorSessionID != "" {
		r.writef("  orchestrator:     %s\n", r.orchestratorSessionID)
	} else {
		r.writeln("  orchestrator:     (none - workers will be unattached)")
	}
	if r.workflowRunID != "" {
		r.writef("  workflow run:     %s\n", r.workflowRunID)
	}
	if r.targetNodeID != "" {
		r.writef("  target node:      %s\n", r.targetNodeID)
	}
	if r.dryRun {
		r.writeln("  dry-run:          true")
	}
	r.writeln()
}

// RunLoop reconciles the epic until it is drained or the context is cancelled.
func (r *Runner) RunLoop(ctx context.Context) error {
	r.ensureWriters()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		result, err := r.ReconcileOnce(ctx)
		if err != nil {
			if errors.Is(err, ErrStalledWorker) {
				return err
			}
			r.errorf("[epic-run] reconcile error: %v (continuing)\n", err)
		}
		if result.Done {
			r.writef("[epic-run] epic %s drained, exiting\n", r.parent)
			return nil
		}
		if result.Blocked {
			return nil
		}

		select {
		case <-ctx.Done():
			r.writeln("[epic-run] interrupted; worker state remains in fleet-db and workers continue independently")
			return nil
		case <-ticker.C:
		}
	}
}

type reconcileSnapshot struct {
	ready        []backend.IssueData
	blocked      []backend.IssueData
	openChildren []backend.IssueData
}

// DispatchedTask describes one worker start enqueued by a reconcile pass.
type DispatchedTask struct {
	TaskID    string `json:"task_id"`
	Title     string `json:"title,omitempty"`
	AgentName string `json:"agent_name"`
}

// ReconcileResult summarizes a single reconcile pass.
type ReconcileResult struct {
	Done              bool             `json:"done"`
	Blocked           bool             `json:"blocked,omitempty"`
	ReadyCount        int              `json:"ready_count"`
	BlockedCount      int              `json:"blocked_count"`
	OpenChildrenCount int              `json:"open_children_count"`
	ActiveWorkers     int              `json:"active_workers"`
	DispatchedCount   int              `json:"dispatched_count"`
	MaxConcurrency    int              `json:"max_concurrency"`
	Dispatched        []DispatchedTask `json:"dispatched,omitempty"`
}

// ReconcileOnce performs one pass: fetch ready and blocked children, start
// workers for ready tasks up to the concurrency cap, and report whether the
// epic is fully drained.
func (r *Runner) ReconcileOnce(ctx context.Context) (ReconcileResult, error) {
	r.ensureWriters()
	snapshot, err := r.loadReconcileSnapshot(ctx)
	if err != nil {
		return ReconcileResult{}, err
	}
	if err := r.reconcileTaskRuns(ctx, snapshot.openChildren); err != nil {
		return ReconcileResult{}, err
	}
	result := ReconcileResult{
		ReadyCount:        len(snapshot.ready),
		BlockedCount:      len(snapshot.blocked),
		OpenChildrenCount: len(snapshot.openChildren),
		MaxConcurrency:    r.maxConcurrency,
	}
	if drained, activeWorkers := r.drainState(ctx, snapshot.openChildren); drained {
		result.Done = true
		result.ActiveWorkers = activeWorkers
		return result, nil
	} else if len(snapshot.openChildren) == 0 {
		result.ActiveWorkers = activeWorkers
		r.writef("[epic-run] child work closed; waiting for %d active worker(s) to stop\n", activeWorkers)
		return result, nil
	}

	activeWorkers := r.countActiveWorkers(ctx, snapshot.openChildren)
	result.ActiveWorkers = activeWorkers
	if stalled := r.stalledTasks(ctx, snapshot.openChildren); len(stalled) > 0 {
		return result, fmt.Errorf("%w: %s", ErrStalledWorker, strings.Join(stalled, "; "))
	}
	if len(snapshot.ready) == 0 && len(snapshot.blocked) > 0 && activeWorkers == 0 {
		result.Blocked = true
		r.writef("[epic-run] epic %s blocked with %d child task(s): %s\n",
			r.parent, len(snapshot.blocked), blockedTaskSummary(snapshot.blocked))
		return result, nil
	}
	slots := r.maxConcurrency - activeWorkers
	if slots <= 0 {
		r.writef("[epic-run] %d ready, %d blocked, %d active workers (at cap)\n", len(snapshot.ready), len(snapshot.blocked), activeWorkers)
		return result, nil
	}

	dispatched, failures := r.dispatchReadyTasks(ctx, snapshot.ready, slots)
	result.Dispatched = dispatched
	result.DispatchedCount = len(dispatched)
	r.writef("[epic-run] %d ready, %d blocked, dispatched %d (active %d/%d)\n",
		len(snapshot.ready), len(snapshot.blocked), len(dispatched), activeWorkers+len(dispatched), r.maxConcurrency)
	if len(failures) > 0 && r.failOnDispatchError {
		return result, runError(ErrorKindInternal, fmt.Sprintf("dispatch failed: %s", strings.Join(failures, "; ")), nil)
	}
	return result, nil
}

func blockedTaskSummary(tasks []backend.IssueData) string {
	const maxTasks = 5
	parts := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if task.ID == "" {
			continue
		}
		label := task.ID
		if strings.TrimSpace(task.Title) != "" {
			label += " (" + task.Title + ")"
		}
		parts = append(parts, label)
		if len(parts) == maxTasks {
			break
		}
	}
	if len(tasks) > maxTasks {
		parts = append(parts, fmt.Sprintf("+%d more", len(tasks)-maxTasks))
	}
	return strings.Join(parts, ", ")
}

func (r *Runner) loadReconcileSnapshot(ctx context.Context) (*reconcileSnapshot, error) {
	ready, err := r.ib.Ready(ctx, backend.ReadyOpts{ParentID: r.parent, Limit: 256})
	if err != nil {
		return nil, backendRunError(ErrorKindInternal, "ready query", err)
	}
	blocked, err := r.ib.Blocked(ctx, backend.BlockedOpts{ParentID: r.parent, Limit: 256})
	if err != nil {
		return nil, backendRunError(ErrorKindInternal, "blocked query", err)
	}

	openChildren, err := r.openChildren(ctx)
	if err != nil {
		return nil, err
	}
	return &reconcileSnapshot{ready: ready, blocked: blocked, openChildren: openChildren}, nil
}

func (r *Runner) ensureTaskRun(ctx context.Context, task backend.IssueData) (*domain.TaskRun, error) {
	if r.dryRun || r.workflowRunID == "" {
		return nil, nil
	}
	run, err := r.store.TaskRuns().Ensure(ctx, store.TaskRunEnsure{
		WorkspaceKey:    r.workspace,
		WorkflowRunID:   r.workflowRunID,
		WorkItemID:      task.ID,
		RoleName:        r.role,
		IdempotencyKey:  "child:" + task.ID + ":role:" + r.role,
		ParentSessionID: r.orchestratorSessionID,
		Reason:          task.Title,
		Metadata: map[string]string{
			"parent_id":     r.parent,
			"workflow_name": workflowpkg.RunParentWorkItemsName,
		},
	})
	if err != nil {
		return nil, err
	}
	_, _ = r.store.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  r.workspace,
		WorkflowRunID: r.workflowRunID,
		TaskRunID:     run.TaskRunID,
		Type:          "task_run_ensured",
		Message:       "ensured epic child task run",
		Data:          mustJSON(map[string]string{"work_item_id": task.ID, "role": r.role}),
	})
	return run, nil
}

func (r *Runner) reconcileTaskRuns(ctx context.Context, openChildren []backend.IssueData) error {
	if r.dryRun || r.workflowRunID == "" || r.store.TaskRuns() == nil {
		return nil
	}
	runs, err := r.store.TaskRuns().List(ctx, r.workspace, store.TaskRunFilter{WorkflowRunID: r.workflowRunID, Live: true, Limit: 10000})
	if err != nil {
		return fmt.Errorf("list live task runs: %w", err)
	}
	openByID := make(map[string]backend.IssueData, len(openChildren))
	for _, child := range openChildren {
		openByID[child.ID] = child
	}
	for _, run := range runs {
		if run == nil {
			continue
		}
		child, open := openByID[run.WorkItemID]
		if !open {
			status := domain.TaskRunPassed
			now := time.Now().UTC()
			finishedAt := &now
			if _, err := r.store.TaskRuns().Update(ctx, r.workspace, run.TaskRunID, store.TaskRunUpdate{Status: &status, FinishedAt: &finishedAt}); err != nil {
				return err
			}
			_, _ = r.store.RunEvents().Append(ctx, store.RunEventAppend{
				WorkspaceKey:  r.workspace,
				WorkflowRunID: r.workflowRunID,
				TaskRunID:     run.TaskRunID,
				Type:          "task_run_completed",
				Message:       "child work item is terminal",
				Data:          mustJSON(map[string]string{"work_item_id": run.WorkItemID}),
			})
			continue
		}
		if child.Status == "in_progress" && child.Assignee != "" && run.ClaimActor == "" {
			status := domain.TaskRunClaimed
			claimActor := child.Assignee
			if _, err := r.store.TaskRuns().Update(ctx, r.workspace, run.TaskRunID, store.TaskRunUpdate{Status: &status, ClaimActor: &claimActor}); err != nil {
				return err
			}
		}
		if sessionID, ok, err := r.liveTaskSessionID(ctx, run.AgentID, run.WorkItemID); err != nil {
			return err
		} else if ok {
			status := domain.TaskRunRunning
			_, _ = r.store.TaskRuns().Update(ctx, r.workspace, run.TaskRunID, store.TaskRunUpdate{Status: &status, SessionID: &sessionID})
		}
	}
	return nil
}

func (r *Runner) failTaskRun(ctx context.Context, taskRun *domain.TaskRun, cause error) {
	if taskRun == nil || r.store.TaskRuns() == nil {
		return
	}
	status := domain.TaskRunFailed
	now := time.Now().UTC()
	finishedAt := &now
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	_, _ = r.store.TaskRuns().Update(ctx, r.workspace, taskRun.TaskRunID, store.TaskRunUpdate{
		Status:       &status,
		FinishedAt:   &finishedAt,
		ErrorMessage: &message,
	})
	if r.store.RunEvents() != nil {
		_, _ = r.store.RunEvents().Append(ctx, store.RunEventAppend{
			WorkspaceKey:  r.workspace,
			WorkflowRunID: r.workflowRunID,
			TaskRunID:     taskRun.TaskRunID,
			Type:          "task_run_failed",
			Message:       "epic runner failed to dispatch task run",
			Data:          mustJSON(map[string]string{"work_item_id": taskRun.WorkItemID, "error": message}),
		})
	}
}

func (r *Runner) drainState(ctx context.Context, openChildren []backend.IssueData) (drained bool, activeWorkers int) {
	if len(openChildren) > 0 {
		return false, 0
	}
	activeWorkers = r.countActiveWorkers(ctx, openChildren)
	return activeWorkers == 0, activeWorkers
}

func (r *Runner) dispatchReadyTasks(ctx context.Context, ready []backend.IssueData, slots int) ([]DispatchedTask, []string) {
	dispatched := make([]DispatchedTask, 0, slots)
	var failures []string
	for _, task := range ready {
		if len(dispatched) >= slots {
			break
		}
		if task.Status == "in_progress" {
			continue
		}
		if task.Status == "closed" || task.Status == "deferred" {
			continue
		}
		name := WorkerName(r.prefix, task.ID)
		taskRun, err := r.ensureTaskRun(ctx, task)
		if err != nil {
			msg := fmt.Sprintf("ensure task run for %s failed: %v", task.ID, err)
			r.errorf("[epic-run] %s\n", msg)
			failures = append(failures, msg)
			continue
		}
		active, err := r.workerActiveForTask(ctx, name, task.ID)
		if err != nil {
			msg := fmt.Sprintf("active worker check for %s failed: %v", task.ID, err)
			r.errorf("[epic-run] %s\n", msg)
			failures = append(failures, msg)
			continue
		}
		if active {
			continue
		}
		if err := r.spawnWorker(ctx, task, taskRun); err != nil {
			msg := fmt.Sprintf("spawn for %s failed: %v", task.ID, err)
			r.errorf("[epic-run] %s\n", msg)
			failures = append(failures, msg)
			r.failTaskRun(ctx, taskRun, err)
			continue
		}
		dispatched = append(dispatched, DispatchedTask{
			TaskID:    task.ID,
			Title:     task.Title,
			AgentName: name,
		})
	}
	return dispatched, failures
}

func (r *Runner) openChildren(ctx context.Context) ([]backend.IssueData, error) {
	issues, err := r.ib.List(ctx, backend.ListOpts{ParentID: r.parent, Limit: 10000})
	if err != nil {
		return nil, backendRunError(ErrorKindInternal, "list child work", err)
	}
	open := make([]backend.IssueData, 0, len(issues))
	for _, t := range issues {
		if t.Status != "closed" && t.Status != "deferred" {
			open = append(open, t)
		}
	}
	return open, nil
}

func (r *Runner) countActiveWorkers(ctx context.Context, openChildren []backend.IssueData) int {
	active := make(map[string]struct{})
	for _, task := range openChildren {
		name := WorkerName(r.prefix, task.ID)
		ok, err := r.workerActiveForTask(ctx, name, task.ID)
		if err != nil {
			return r.maxConcurrency
		}
		if ok {
			active[name] = struct{}{}
		}
	}

	agents, err := r.store.Agents().List(ctx, r.workspace)
	if err != nil {
		return r.maxConcurrency
	}
	for _, agent := range agents {
		if agent == nil || agent.Parent != r.parent || agent.Mode != domain.AgentModeEphemeral {
			continue
		}
		if _, counted := active[agent.Name]; counted {
			continue
		}
		ok, err := r.workerActiveForTask(ctx, agent.Name, "")
		if err != nil {
			return r.maxConcurrency
		}
		if ok {
			active[agent.Name] = struct{}{}
		}
	}
	return len(active)
}

func (r *Runner) workerActiveForTask(ctx context.Context, name, taskID string) (bool, error) {
	if name == "" {
		return false, nil
	}
	liveCommand, err := r.hasLiveStartCommand(ctx, name, taskID)
	if err != nil || liveCommand {
		return liveCommand, err
	}
	liveSession, err := r.hasLiveTaskSession(ctx, name, taskID)
	if err != nil || liveSession {
		return liveSession, err
	}
	a, err := r.store.Agents().Get(ctx, r.workspace, name)
	if errors.Is(err, domain.ErrNotFound) || a == nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return a.Mode == domain.AgentModeEphemeral && a.Parent == r.parent && a.State == domain.AgentStateActive, nil
}

func (r *Runner) hasLiveStartCommand(ctx context.Context, name, taskID string) (bool, error) {
	if r.store.AgentCommands() == nil {
		return false, nil
	}
	cmds, err := r.store.AgentCommands().List(ctx, r.workspace, store.AgentCommandFilter{
		TargetAgentID: name,
		Limit:         100,
	})
	if err != nil {
		return false, err
	}
	for _, cmd := range cmds {
		if cmd == nil || cmd.Type != "start" || !liveAgentCommandStatus(cmd.Status) {
			continue
		}
		if taskID != "" && cmd.Payload["task_id"] != taskID {
			continue
		}
		return true, nil
	}
	return false, nil
}

func (r *Runner) hasLiveTaskSession(ctx context.Context, name, taskID string) (bool, error) {
	if r.store.AgentSessions() == nil {
		return false, nil
	}
	filter := store.AgentSessionFilter{AgentID: name, Limit: 100}
	if taskID != "" {
		filter.TaskID = taskID
	}
	sessions, err := r.store.AgentSessions().List(ctx, r.workspace, filter)
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		if session == nil || session.Kind != domain.AgentSessionKindTask || !liveAgentSessionStatus(session.Status) {
			continue
		}
		if taskID != "" && session.TaskID != taskID {
			continue
		}
		return true, nil
	}
	return false, nil
}

func (r *Runner) liveTaskSessionID(ctx context.Context, name, taskID string) (string, bool, error) {
	if r.store.AgentSessions() == nil || name == "" {
		return "", false, nil
	}
	filter := store.AgentSessionFilter{AgentID: name, Limit: 100}
	if taskID != "" {
		filter.TaskID = taskID
	}
	sessions, err := r.store.AgentSessions().List(ctx, r.workspace, filter)
	if err != nil {
		return "", false, err
	}
	for _, session := range sessions {
		if session == nil || session.Kind != domain.AgentSessionKindTask || !liveAgentSessionStatus(session.Status) {
			continue
		}
		if taskID != "" && session.TaskID != taskID {
			continue
		}
		return session.SessionID, true, nil
	}
	return "", false, nil
}

func liveAgentCommandStatus(status domain.AgentCommandStatus) bool {
	switch status {
	case "", domain.AgentCommandQueued, domain.AgentCommandAcked, domain.AgentCommandRunning:
		return true
	default:
		return false
	}
}

func liveAgentSessionStatus(status domain.AgentSessionStatus) bool {
	switch status {
	case domain.AgentSessionQueued, domain.AgentSessionLeased, domain.AgentSessionStarting, domain.AgentSessionRunning:
		return true
	default:
		return false
	}
}

func (r *Runner) stalledTasks(ctx context.Context, openChildren []backend.IssueData) []string {
	var stalled []string
	if r.stalledTaskTicks == nil {
		r.stalledTaskTicks = make(map[string]int)
	}
	observed := make(map[string]struct{})
	for _, task := range openChildren {
		if task.Status != "in_progress" {
			continue
		}
		name := WorkerName(r.prefix, task.ID)
		if task.Assignee != name {
			continue
		}
		active, err := r.workerActiveForTask(ctx, name, task.ID)
		if err != nil || active {
			continue
		}
		observed[task.ID] = struct{}{}
		r.stalledTaskTicks[task.ID]++
		if r.stalledTaskTicks[task.ID] >= stalledWorkerFatalConsecutiveTicks {
			stalled = append(stalled, fmt.Sprintf("%s (%s) assigned to stopped worker %s", task.ID, task.Title, name))
		}
	}
	for taskID := range r.stalledTaskTicks {
		if _, ok := observed[taskID]; !ok {
			delete(r.stalledTaskTicks, taskID)
		}
	}
	return stalled
}

func (r *Runner) spawnWorker(ctx context.Context, task backend.IssueData, taskRun *domain.TaskRun) error {
	r.ensureWriters()
	name := WorkerName(r.prefix, task.ID)
	if r.dryRun {
		r.writef("[epic-run] DRY-RUN would spawn: %s for task %s (%s)\n", name, task.ID, task.Title)
		return nil
	}

	agent, err := r.createOrLoadWorkerAgent(ctx, name)
	if err != nil {
		return err
	}
	if r.prepareWorktrees {
		if err := r.ensureLocalWorkerWorktrees(ctx, *agent); err != nil {
			return err
		}
	}
	if taskRun != nil {
		status := domain.TaskRunStarting
		agentID := agent.Name
		claimActor := agent.Name
		parent := r.orchestratorSessionID
		_, _ = r.store.TaskRuns().Update(ctx, r.workspace, taskRun.TaskRunID, store.TaskRunUpdate{
			Status:          &status,
			AgentID:         &agentID,
			ClaimActor:      &claimActor,
			ParentSessionID: &parent,
		})
	}
	commandID, err := r.enqueueWorkerStart(ctx, name, task, taskRun)
	if err != nil {
		return err
	}
	if taskRun != nil && commandID != "" {
		status := domain.TaskRunStarting
		_, _ = r.store.TaskRuns().Update(ctx, r.workspace, taskRun.TaskRunID, store.TaskRunUpdate{
			Status:    &status,
			CommandID: &commandID,
		})
		_, _ = r.store.RunEvents().Append(ctx, store.RunEventAppend{
			WorkspaceKey:  r.workspace,
			WorkflowRunID: r.workflowRunID,
			TaskRunID:     taskRun.TaskRunID,
			Type:          "task_run_dispatched",
			Message:       "derived daemon start command from task run",
			Data:          mustJSON(map[string]string{"agent_id": name, "command_id": commandID, "work_item_id": task.ID}),
		})
	}
	r.writef("[epic-run] spawned %s -> %s (%s)\n", name, task.ID, task.Title)
	return nil
}

func (r *Runner) createOrLoadWorkerAgent(ctx context.Context, name string) (*domain.Agent, error) {
	mode := domain.AgentModeEphemeral
	desired := domain.AgentDesiredStopped
	if r.store.AgentCommands() == nil {
		desired = domain.AgentDesiredRunning
	}
	agent, err := r.store.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: r.workspace,
		Name:         name,
		RoleName:     r.role,
		Auto:         true,
		Backend:      r.backend,
		Parent:       r.parent,
		Mode:         mode,
		DesiredState: desired,
	})
	if err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		return nil, fmt.Errorf("create agent %s: %w", name, err)
	}
	if errors.Is(err, domain.ErrAlreadyExists) {
		agent, err = r.store.Agents().Get(ctx, r.workspace, name)
		if err != nil {
			return nil, fmt.Errorf("get existing agent %s: %w", name, err)
		}
		if agent.Mode != domain.AgentModeEphemeral || agent.Parent != r.parent {
			return nil, fmt.Errorf("worker name %s already exists for role %q parent %q mode %q", name, agent.RoleName, agent.Parent, agent.Mode)
		}
	}
	if agent == nil {
		return nil, fmt.Errorf("create agent %s returned nil", name)
	}
	return agent, nil
}

func (r *Runner) enqueueWorkerStart(ctx context.Context, name string, task backend.IssueData, taskRun *domain.TaskRun) (string, error) {
	if r.store.AgentCommands() == nil {
		r.writef("[epic-run] spawned %s for %s (%s) - no command channel, agent will pick task via Ready())\n", name, task.ID, task.Title)
		return "", nil
	}

	payload := map[string]string{"task_id": task.ID}
	if r.orchestratorSessionID != "" {
		payload["parent_session_id"] = r.orchestratorSessionID
	}
	if r.workflowRunID != "" {
		payload["workflow_run_id"] = r.workflowRunID
	}
	if taskRun != nil {
		payload["task_run_id"] = taskRun.TaskRunID
	}
	cmd, err := r.store.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  r.workspace,
		TargetAgentID: name,
		TargetNodeID:  r.targetNodeID,
		Type:          "start",
		Payload:       payload,
	})
	if err != nil {
		return "", fmt.Errorf("enqueue start command for %s: %w", name, err)
	}
	return cmd.CommandID, nil
}

func (r *Runner) ensureLocalWorkerWorktrees(ctx context.Context, agent domain.Agent) error {
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		return fmt.Errorf("load local workspace state: %w", err)
	}
	local := sc.Workspaces[agent.WorkspaceKey]
	if local.Path == "" {
		return nil
	}
	repos, err := r.store.Repos().List(ctx, agent.WorkspaceKey)
	if err != nil {
		return fmt.Errorf("list workspace repos: %w", err)
	}
	localRepos := make([]localworkspace.Repo, 0, len(repos))
	for _, repo := range repos {
		if repo == nil {
			continue
		}
		localRepos = append(localRepos, localworkspace.Repo{
			Name:          repo.Name,
			Path:          localworkspace.RepoPath(local, repo.Name),
			Remote:        repo.Remote,
			DefaultBranch: repo.DefaultBranch,
			Groups:        append([]string(nil), repo.Groups...),
		})
	}
	selected, err := localworkspace.SelectAgentRepos(localRepos, agent)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		return fmt.Errorf("workspace %s has no repos for agent %q", agent.WorkspaceKey, agent.Name)
	}

	created := make(map[string]string, len(selected))
	for _, repo := range selected {
		target := localworkspace.AgentWorktreePath(local.Path, repo.Name, agent.Name)
		if err := localworkspace.EnsureGitWorktreeFromBranch(repo.Path, target, agent.Name, repo.Remote, repo.DefaultBranch); err != nil {
			return fmt.Errorf("create worktree for repo %q: %w", repo.Name, err)
		}
		created[repo.Name] = target
	}
	if err := localworkspace.RememberAgentWorktree(agent.WorkspaceKey, agent.Name, localworkspace.FirstWorktreePath(created)); err != nil {
		return fmt.Errorf("remember agent worktree: %w", err)
	}
	return nil
}

func (r *Runner) ensureWriters() {
	if r.out == nil {
		r.out = io.Discard
	}
	if r.errOut == nil {
		r.errOut = io.Discard
	}
}

func (r *Runner) writef(format string, args ...any) {
	r.ensureWriters()
	_, _ = fmt.Fprintf(r.out, format, args...)
}

func (r *Runner) writeln(args ...any) {
	r.ensureWriters()
	_, _ = fmt.Fprintln(r.out, args...)
}

func (r *Runner) errorf(format string, args ...any) {
	r.ensureWriters()
	_, _ = fmt.Fprintf(r.errOut, format, args...)
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}

// WorkerName derives a deterministic agent name from prefix + task ID.
func WorkerName(prefix, taskID string) string {
	hashBytes := sha256.Sum256([]byte(taskID))
	hash := hex.EncodeToString(hashBytes[:])[:8]
	base := SanitizePrefix(prefix + "-" + taskID)
	const maxNameLen = 63
	suffix := "-" + hash
	if len(base)+len(suffix) > maxNameLen {
		base = strings.TrimRight(base[:maxNameLen-len(suffix)], "-")
	}
	if base == "" {
		base = "task"
	}
	return base + suffix
}

// SanitizePrefix lowercases and strips characters that are invalid in agent names.
func SanitizePrefix(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}

// SelectTargetNodeID returns the single active local daemon node for workspace.
func SelectTargetNodeID(ctx context.Context, st store.Store, workspace string) (string, error) {
	if st.Nodes() == nil {
		return "", nil
	}
	nodes, err := st.Nodes().List(ctx, workspace)
	if err != nil {
		return "", runError(ErrorKindInternal, "list daemon nodes", err)
	}
	now := time.Now().UTC()
	active := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if node.DrainState != domain.NodeDrainActive {
			continue
		}
		if !node.ExpiresAt.IsZero() && node.ExpiresAt.Before(now) {
			continue
		}
		active = append(active, node.NodeID)
	}
	switch len(active) {
	case 0:
		return "", runError(ErrorKindUnavailable, "no active daemon node registered for this workspace; start `loom daemon` before running an epic", nil)
	case 1:
		return active[0], nil
	default:
		return "", runError(ErrorKindConflict, fmt.Sprintf("multiple active daemon nodes found (%s); route the request to one node before running an epic", strings.Join(active, ", ")), nil)
	}
}
