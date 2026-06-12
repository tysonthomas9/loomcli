package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var ErrNoQueuedTaskRun = errors.New("task worker: no queued task run")

type TaskWorker struct {
	Store              store.Store
	WorkspaceKey       string
	TaskRunID          string
	WorkDir            string
	NodeID             string
	RunnerID           string
	LeaseID            string
	LeaseToken         string
	SupportedProviders []string
	Capabilities       []string
	WorkerProfileIDs   []string
	RunnerPlacement    domain.TaskRunPlacement
	SandboxPlacement   domain.TaskRunPlacement
	HeartbeatInterval  time.Duration
	MaxAttempts        int
	Executor           TaskExecutor
	// Now is a clock seam for tests; nil uses time.Now.
	Now func() time.Time
}

func (w *TaskWorker) RunOnce(ctx context.Context) (*TaskRunRequestOutcome, error) {
	if w == nil || w.Store == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	workDir, err := (&Executor{WorkDir: w.WorkDir}).resolveWorkDir()
	if err != nil {
		return nil, err
	}
	if ws := strings.TrimSpace(w.WorkspaceKey); ws != "" {
		return w.runOnceInWorkspace(ctx, ws, workDir)
	}
	workspaces, err := w.Store.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces for task worker: %w", err)
	}
	for _, ws := range workspaces {
		if ws == nil {
			continue
		}
		outcome, err := w.runOnceInWorkspace(ctx, ws.Key, workDir)
		if err == nil {
			return outcome, nil
		}
		if !errors.Is(err, ErrNoQueuedTaskRun) {
			return nil, err
		}
	}
	return nil, ErrNoQueuedTaskRun
}

func (w *TaskWorker) runOnceInWorkspace(ctx context.Context, ws, workDir string) (*TaskRunRequestOutcome, error) {
	nodeID := w.nodeID()
	if nodeID == "" {
		return nil, fmt.Errorf("worker node id required: %w", domain.ErrInvalid)
	}
	if err := w.ensureNode(ctx, ws, nodeID); err != nil {
		return nil, err
	}
	executor := w.Executor
	if executor == nil {
		executor = HostBridgeTaskExecutor{
			Store:        w.Store,
			WorktreePath: workDir,
		}
	}
	outcome, err := ClaimAndExecuteTaskRunWithResult(ctx, w.Store, TaskRunWorkerOptions{
		WorkspaceKey:       ws,
		TaskRunID:          w.TaskRunID,
		NodeID:             nodeID,
		RunnerID:           w.RunnerID,
		LeaseID:            w.LeaseID,
		LeaseToken:         w.LeaseToken,
		SupportedProviders: w.SupportedProviders,
		Capabilities:       w.Capabilities,
		WorkerProfileIDs:   w.WorkerProfileIDs,
		RunnerPlacement:    w.runnerPlacement(nodeID),
		SandboxPlacement:   w.SandboxPlacement,
		HeartbeatInterval:  w.HeartbeatInterval,
		CloseTaskOnSuccess: true,
		MaxAttempts:        w.maxAttempts(),
		Now:                w.Now,
	}, executor)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, ErrNoQueuedTaskRun
		}
		return nil, err
	}
	if err := w.updateLinkedDriverStep(ctx, outcome.Run); err != nil {
		return outcome, err
	}
	return outcome, nil
}

func (w *TaskWorker) updateLinkedDriverStep(ctx context.Context, run *domain.TaskRun) error {
	if run == nil || run.DriverRunID == "" || run.DriverStepID == "" {
		return nil
	}
	parent, err := w.Store.DriverRuns().Get(ctx, run.WorkspaceKey, run.DriverRunID)
	if err != nil {
		return fmt.Errorf("get parent driver run for task step update: %w", err)
	}
	if parent.Status != domain.DriverRunRunning {
		return nil
	}
	status := driverStepStatusForTaskRun(run.Status)
	outputRef := firstNonEmpty(run.ArtifactsRef, run.LogsRef)
	_, err = w.Store.DriverSteps().Update(ctx, run.WorkspaceKey, run.DriverStepID, store.DriverStepUpdate{
		Status:       &status,
		TaskRunID:    &run.TaskRunID,
		OutputRef:    &outputRef,
		NodeID:       parent.NodeID,
		LeaseID:      parent.LeaseID,
		FencingToken: parent.FencingToken,
	})
	if err != nil {
		return fmt.Errorf("update linked driver step from task worker: %w", err)
	}
	return nil
}

func (w *TaskWorker) nodeID() string {
	if id := strings.TrimSpace(w.NodeID); id != "" {
		return id
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "local"
	}
	return fmt.Sprintf("loom-task-worker-%s-%d", host, os.Getpid())
}

func (w *TaskWorker) runnerPlacement(nodeID string) domain.TaskRunPlacement {
	placement := w.RunnerPlacement
	if placement.Provider == "" {
		placement.Provider = "loom-serve"
	}
	if placement.NodeID == "" {
		placement.NodeID = nodeID
	}
	if placement.RunnerID == "" {
		placement.RunnerID = w.RunnerID
	}
	return placement
}

func (w *TaskWorker) maxAttempts() int {
	if w.MaxAttempts < 1 {
		return 2
	}
	return w.MaxAttempts
}

func (w *TaskWorker) ensureNode(ctx context.Context, ws, nodeID string) error {
	ttl := (&Executor{HeartbeatInterval: w.HeartbeatInterval}).nodeTTL()
	ownerActor := executorOwnerActor()
	runtimeProvider := domain.RuntimeProviderLocal
	labels := []string{"loom-driver-executor", "loom-task-worker"}
	capabilities := w.nodeCapabilities()
	toolInventory := []string{"loom-driver", "loom-task-worker"}
	drainState := domain.NodeDrainActive
	_, err := w.Store.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    ws,
		NodeID:          nodeID,
		OwnerActor:      ownerActor,
		RuntimeProvider: runtimeProvider,
		Labels:          labels,
		Capabilities:    capabilities,
		ToolInventory:   toolInventory,
		DrainState:      drainState,
		TTL:             ttl,
	})
	if err == nil {
		return nil
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("register task worker node: %w", err)
	}
	if _, hbErr := w.Store.Nodes().Heartbeat(ctx, ws, nodeID, ttl); hbErr != nil {
		return fmt.Errorf("heartbeat task worker node: %w", hbErr)
	}
	if existing, getErr := w.Store.Nodes().Get(ctx, ws, nodeID); getErr == nil {
		labels = mergeNodeStringSet(existing.Labels, labels)
		capabilities = mergeNodeStringSet(existing.Capabilities, capabilities)
		toolInventory = mergeNodeStringSet(existing.ToolInventory, toolInventory)
	}
	if _, updateErr := w.Store.Nodes().Update(ctx, ws, nodeID, store.NodeUpdate{
		OwnerActor:      &ownerActor,
		RuntimeProvider: &runtimeProvider,
		Labels:          &labels,
		Capabilities:    &capabilities,
		ToolInventory:   &toolInventory,
		DrainState:      &drainState,
	}); updateErr != nil {
		return fmt.Errorf("refresh task worker node: %w", updateErr)
	}
	return nil
}

func (w *TaskWorker) nodeCapabilities() []string {
	values := []string{"driver-runner", "task-runner", "flue-local"}
	values = append(values, w.SupportedProviders...)
	values = append(values, w.Capabilities...)
	values = append(values, w.SandboxPlacement.Provider)
	return normalizeStringList(values)
}
