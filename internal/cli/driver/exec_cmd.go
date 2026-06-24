package driver

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

var (
	driverExecTaskWorkspaceKey       string
	driverExecTaskDriverRunID        string
	driverExecTaskDriverStepID       string
	driverExecTaskTaskRunID          string
	driverExecTaskTaskID             string
	driverExecTaskWorkerProfileID    string
	driverExecTaskProviderProfile    string
	driverExecTaskParentSessionID    string
	driverExecTaskNodeID             string
	driverExecTaskRunnerID           string
	driverExecTaskLeaseID            string
	driverExecTaskFencingToken       int64
	driverExecTaskSupportedProviders []string
	driverExecTaskCapabilities       []string
	driverExecTaskSandboxProvider    string
	driverExecTaskSandboxID          string
	driverExecTaskSandboxCWD         string
	driverExecTaskSandboxRepoRef     string
	driverExecTaskDeferCompletion    bool
	driverExecTaskJSON               bool

	driverWorkTaskWorkspaceKey       string
	driverWorkTaskTaskRunID          string
	driverWorkTaskNodeID             string
	driverWorkTaskRunnerID           string
	driverWorkTaskLeaseID            string
	driverWorkTaskLeaseToken         string
	driverWorkTaskSupportedProviders []string
	driverWorkTaskCapabilities       []string
	driverWorkTaskWorkerProfileIDs   []string
	driverWorkTaskRunnerProvider     string
	driverWorkTaskRunnerProcessRef   string
	driverWorkTaskSandboxProvider    string
	driverWorkTaskSandboxID          string
	driverWorkTaskSandboxCWD         string
	driverWorkTaskSandboxRepoRef     string
	driverWorkTaskSandboxImage       string
	driverWorkTaskDeferCompletion    bool
	driverWorkTaskJSON               bool
)

var driverExecTaskCmd = &cobra.Command{
	Use:    "exec-task",
	Short:  "Execute one child task run for a driver runtime",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDriverExecTask,
}

var driverWorkTaskRunCmd = &cobra.Command{
	Use:    "work-task-run",
	Short:  "Claim and execute one queued task run for a worker runtime",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDriverWorkTaskRun,
}

func bindDriverExecTaskFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverExecTaskWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	cmd.Flags().StringVar(&driverExecTaskDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	cmd.Flags().StringVar(&driverExecTaskDriverStepID, "driver-step-id", "", "DriverStep ID (default: LOOM_DRIVER_STEP_ID)")
	cmd.Flags().StringVar(&driverExecTaskTaskRunID, "task-run-id", "", "TaskRun ID (default: generated)")
	cmd.Flags().StringVar(&driverExecTaskTaskID, "task-id", "", "FleetDB task ID")
	cmd.Flags().StringVar(&driverExecTaskWorkerProfileID, "worker-profile-id", "", "Worker profile ID")
	cmd.Flags().StringVar(&driverExecTaskProviderProfile, "provider-profile", "", "Provider profile requested by the driver")
	cmd.Flags().StringVar(&driverExecTaskParentSessionID, "parent-session-id", "", "Parent AgentSession ID")
	cmd.Flags().StringVar(&driverExecTaskNodeID, "node-id", "", "Executor node ID")
	cmd.Flags().StringVar(&driverExecTaskRunnerID, "runner-id", "", "Runner ID")
	cmd.Flags().StringVar(&driverExecTaskLeaseID, "lease-id", "", "Parent DriverRun lease ID")
	cmd.Flags().Int64Var(&driverExecTaskFencingToken, "fencing-token", 0, "Parent DriverRun fencing token")
	cmd.Flags().StringSliceVar(&driverExecTaskSupportedProviders, "supported-provider", nil, "Supported task provider (repeatable)")
	cmd.Flags().StringSliceVar(&driverExecTaskCapabilities, "capability", nil, "Runner capability (repeatable)")
	cmd.Flags().StringVar(&driverExecTaskSandboxProvider, "sandbox-provider", "", "Sandbox provider")
	cmd.Flags().StringVar(&driverExecTaskSandboxID, "sandbox-id", "", "Sandbox ID")
	cmd.Flags().StringVar(&driverExecTaskSandboxCWD, "sandbox-cwd", "", "Sandbox working directory")
	cmd.Flags().StringVar(&driverExecTaskSandboxRepoRef, "sandbox-repo-ref", "", "Sandbox repository ref")
	cmd.Flags().BoolVar(&driverExecTaskDeferCompletion, "defer-completion", false, "Return execution result but leave successful TaskRun running for CompleteRun")
	cmd.Flags().BoolVar(&driverExecTaskJSON, "json", false, "JSON output")
	_ = cmd.MarkFlagRequired("task-id")
}

func bindDriverWorkTaskRunFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverWorkTaskWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_WORKER_WORKSPACE, LOOM_DRIVER_WORKSPACE, or active workspace)")
	cmd.Flags().StringVar(&driverWorkTaskTaskRunID, "task-run-id", "", "Specific queued TaskRun ID to claim")
	cmd.Flags().StringVar(&driverWorkTaskNodeID, "node-id", "", "Worker node ID (default: LOOM_WORKER_NODE_ID, LOOM_DRIVER_NODE_ID, or host name)")
	cmd.Flags().StringVar(&driverWorkTaskRunnerID, "runner-id", "", "Worker runner ID (default: LOOM_WORKER_RUNNER_ID or LOOM_DRIVER_RUNNER_ID)")
	cmd.Flags().StringVar(&driverWorkTaskLeaseID, "lease-id", "", "TaskRun lease ID (default: generated per claim)")
	cmd.Flags().StringVar(&driverWorkTaskLeaseToken, "lease-token", "", "TaskRun lease token (default: generated per claim)")
	cmd.Flags().StringSliceVar(&driverWorkTaskSupportedProviders, "supported-provider", nil, "Supported task provider (repeatable)")
	cmd.Flags().StringSliceVar(&driverWorkTaskCapabilities, "capability", nil, "Worker capability (repeatable)")
	cmd.Flags().StringSliceVar(&driverWorkTaskWorkerProfileIDs, "worker-profile-id", nil, "Worker profile ID this worker may claim (repeatable)")
	cmd.Flags().StringVar(&driverWorkTaskRunnerProvider, "runner-provider", "", "Runner placement provider")
	cmd.Flags().StringVar(&driverWorkTaskRunnerProcessRef, "runner-process-ref", "", "Runner process reference")
	cmd.Flags().StringVar(&driverWorkTaskSandboxProvider, "sandbox-provider", "", "Sandbox provider")
	cmd.Flags().StringVar(&driverWorkTaskSandboxID, "sandbox-id", "", "Sandbox ID")
	cmd.Flags().StringVar(&driverWorkTaskSandboxCWD, "sandbox-cwd", "", "Sandbox working directory")
	cmd.Flags().StringVar(&driverWorkTaskSandboxRepoRef, "sandbox-repo-ref", "", "Sandbox repository ref")
	cmd.Flags().StringVar(&driverWorkTaskSandboxImage, "sandbox-image", "", "Sandbox image or snapshot")
	cmd.Flags().BoolVar(&driverWorkTaskDeferCompletion, "defer-completion", false, "Return execution result but leave successful TaskRun running for CompleteRun")
	cmd.Flags().BoolVar(&driverWorkTaskJSON, "json", false, "JSON output")
}

func runDriverExecTask(_ *cobra.Command, _ []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		rc, err := resolveDriverRunContext(ctx, h, driverExecTaskWorkspaceKey, driverExecTaskDriverRunID, driverExecTaskNodeID, driverExecTaskLeaseID, driverExecTaskFencingToken)
		if err != nil {
			return err
		}
		fencingToken, err := rc.FencingToken()
		if err != nil {
			return err
		}
		driverStepID := firstNonEmpty(driverExecTaskDriverStepID, os.Getenv("LOOM_DRIVER_STEP_ID"))
		outcome, err := driverpkg.RequestTaskRunWithResult(ctx, h.Store, driverpkg.TaskRunRequestOptions{
			WorkspaceKey:       rc.WorkspaceKey,
			DriverRunID:        rc.DriverRunID,
			DriverStepID:       driverStepID,
			TaskRunID:          driverExecTaskTaskRunID,
			TaskID:             driverExecTaskTaskID,
			WorkerProfileID:    driverExecTaskWorkerProfileID,
			ProviderProfile:    driverExecTaskProviderProfile,
			ParentSessionID:    firstNonEmpty(driverExecTaskParentSessionID, os.Getenv("LOOM_PARENT_SESSION_ID"), os.Getenv("LOOM_TASK_RUN_PARENT_SESSION_ID")),
			ParentNodeID:       rc.NodeID,
			ParentLeaseID:      rc.LeaseID,
			ParentFence:        fencingToken,
			NodeID:             rc.NodeID,
			RunnerID:           driverExecTaskRunnerID,
			LeaseToken:         firstNonEmpty(os.Getenv("LOOM_TASK_RUN_LEASE_TOKEN"), os.Getenv("LOOM_RUNNER_LEASE_TOKEN")),
			SupportedProviders: driverExecTaskSupportedProviders,
			Capabilities:       driverExecTaskCapabilities,
			SandboxPlacement: domain.TaskRunPlacement{
				Provider:  driverExecTaskSandboxProvider,
				SandboxID: driverExecTaskSandboxID,
				CWD:       driverExecTaskSandboxCWD,
				RepoRef:   driverExecTaskSandboxRepoRef,
			},
			DeferCompletion: driverExecTaskDeferCompletion,
		}, driverpkg.HostBridgeTaskExecutor{
			Store:        h.Store,
			WorktreePath: currentWorkingDir(),
		})
		if err != nil {
			return fmt.Errorf("exec task: %w", err)
		}
		return writeTaskRunOutcome(outcome, driverExecTaskJSON)
	})
}

func writeTaskRunOutcome(outcome *driverpkg.TaskRunRequestOutcome, asJSON bool) error {
	result := driverpkg.TaskRunResultFromOutcome(outcome)
	if asJSON {
		return cmdstore.WriteJSON(result)
	}
	fmt.Printf("Task run %s %s for task %s\n", result.ID, result.Status, result.TaskID)
	if result.ErrorMessage != "" {
		fmt.Println(result.ErrorMessage)
	}
	return nil
}

func runDriverWorkTaskRun(_ *cobra.Command, _ []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, err := resolveWorkerWorkspace(ctx, h, driverWorkTaskWorkspaceKey)
		if err != nil {
			return err
		}
		nodeID := firstNonEmpty(driverWorkTaskNodeID, os.Getenv("LOOM_WORKER_NODE_ID"), os.Getenv("LOOM_DRIVER_NODE_ID"), defaultWorkerNodeID())
		if nodeID == "" {
			return fmt.Errorf("worker node id required: %w", domain.ErrInvalid)
		}
		runnerID := firstNonEmpty(driverWorkTaskRunnerID, os.Getenv("LOOM_WORKER_RUNNER_ID"), os.Getenv("LOOM_DRIVER_RUNNER_ID"))
		outcome, err := driverpkg.ClaimAndExecuteTaskRunWithResult(ctx, h.Store, driverpkg.TaskRunWorkerOptions{
			WorkspaceKey:       ws,
			TaskRunID:          driverWorkTaskTaskRunID,
			NodeID:             nodeID,
			RunnerID:           runnerID,
			LeaseID:            driverWorkTaskLeaseID,
			LeaseToken:         firstNonEmpty(driverWorkTaskLeaseToken, os.Getenv("LOOM_TASK_RUN_LEASE_TOKEN"), os.Getenv("LOOM_RUNNER_LEASE_TOKEN")),
			SupportedProviders: driverWorkTaskSupportedProviders,
			Capabilities:       driverWorkTaskCapabilities,
			WorkerProfileIDs:   driverWorkTaskWorkerProfileIDs,
			RunnerPlacement: domain.TaskRunPlacement{
				Provider:   driverWorkTaskRunnerProvider,
				NodeID:     nodeID,
				RunnerID:   runnerID,
				ProcessRef: driverWorkTaskRunnerProcessRef,
			},
			SandboxPlacement: domain.TaskRunPlacement{
				Provider:        driverWorkTaskSandboxProvider,
				SandboxID:       driverWorkTaskSandboxID,
				CWD:             driverWorkTaskSandboxCWD,
				RepoRef:         driverWorkTaskSandboxRepoRef,
				ImageOrSnapshot: driverWorkTaskSandboxImage,
			},
			DeferCompletion: driverWorkTaskDeferCompletion,
		}, driverpkg.HostBridgeTaskExecutor{
			Store:        h.Store,
			WorktreePath: currentWorkingDir(),
		})
		if err != nil {
			return fmt.Errorf("work task run: %w", err)
		}
		return writeTaskRunOutcome(outcome, driverWorkTaskJSON)
	})
}

func currentWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func defaultWorkerNodeID() string {
	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return ""
	}
	return "worker-" + hostname
}
