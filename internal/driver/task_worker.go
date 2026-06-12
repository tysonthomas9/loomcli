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
	if err := (&Executor{
		Store:             w.Store,
		NodeID:            nodeID,
		HeartbeatInterval: w.HeartbeatInterval,
	}).ensureNode(ctx, ws, nodeID); err != nil {
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
