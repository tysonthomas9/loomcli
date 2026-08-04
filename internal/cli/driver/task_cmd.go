package driver

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	driverClaimReadyWorkspaceKey string
	driverClaimReadyDriverRunID  string
	driverClaimReadyNodeID       string
	driverClaimReadyLeaseID      string
	driverClaimReadyFence        int64
	driverClaimReadyEpicID       string
	driverClaimReadyActor        string
	driverClaimReadyLimit        int
	driverClaimReadyJSON         bool

	driverActiveTaskRunsWorkspaceKey string
	driverActiveTaskRunsDriverRunID  string
	driverActiveTaskRunsNodeID       string
	driverActiveTaskRunsLeaseID      string
	driverActiveTaskRunsFence        int64
	driverActiveTaskRunsEpicID       string
	driverActiveTaskRunsLimit        int
	driverActiveTaskRunsJSON         bool

	driverCompleteTaskWorkspaceKey string
	driverCompleteTaskDriverRunID  string
	driverCompleteTaskNodeID       string
	driverCompleteTaskLeaseID      string
	driverCompleteTaskFence        int64
	driverCompleteTaskTaskID       string
	driverCompleteTaskTaskRunID    string
	driverCompleteTaskCompletionID string
	driverCompleteTaskLeaseToken   string
	driverCompleteTaskArtifactIDs  []string
	driverCompleteTaskLogsRef      string
	driverCompleteTaskArtifactsRef string
	driverCompleteTaskReason       string
	driverCompleteTaskJSON         bool

	driverReleaseTaskWorkspaceKey string
	driverReleaseTaskDriverRunID  string
	driverReleaseTaskNodeID       string
	driverReleaseTaskLeaseID      string
	driverReleaseTaskFence        int64
	driverReleaseTaskTaskID       string
	driverReleaseTaskActor        string
	driverReleaseTaskJSON         bool

	driverRecoverStaleWorkspaceKey  string
	driverRecoverStaleDriverRunID   string
	driverRecoverStaleBefore        string
	driverRecoverStaleMaxAgeSeconds int64
	driverRecoverStaleErrorClass    string
	driverRecoverStaleErrorMessage  string
	driverRecoverStaleJSON          bool
)

var driverClaimReadyCmd = &cobra.Command{
	Use:    "claim-ready",
	Short:  "Claim one ready task for a driver runtime",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDriverClaimReady,
}

var driverActiveTaskRunsCmd = &cobra.Command{
	Use:    "active-task-runs",
	Short:  "List active child task runs for a driver runtime",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDriverActiveTaskRuns,
}

var driverCompleteTaskCmd = &cobra.Command{
	Use:    "complete-task",
	Short:  "Complete a claimed FleetDB task for a driver runtime",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDriverCompleteTask,
}

var driverReleaseTaskCmd = &cobra.Command{
	Use:    "release-task",
	Short:  "Release a claimed FleetDB task for a driver runtime",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDriverReleaseTask,
}

var driverRecoverStaleTasksCmd = &cobra.Command{
	Use:    "recover-stale-tasks [driver-run-id]",
	Short:  "Fail stale child task runs and release their task claims",
	Hidden: true,
	Args:   cobra.MaximumNArgs(1),
	RunE:   runDriverRecoverStaleTasks,
}

func bindDriverClaimReadyFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverClaimReadyWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	cmd.Flags().StringVar(&driverClaimReadyDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	cmd.Flags().StringVar(&driverClaimReadyNodeID, "node-id", "", "Parent DriverRun node ID")
	cmd.Flags().StringVar(&driverClaimReadyLeaseID, "lease-id", "", "Parent DriverRun lease ID")
	cmd.Flags().Int64Var(&driverClaimReadyFence, "fencing-token", 0, "Parent DriverRun fencing token")
	cmd.Flags().StringVar(&driverClaimReadyEpicID, "epic-id", "", "Epic ID to scope ready tasks (default: parent DriverRun epic)")
	cmd.Flags().StringVar(&driverClaimReadyActor, "actor", "", "Claim actor (default: driver-run:<driver-run-id>)")
	cmd.Flags().IntVar(&driverClaimReadyLimit, "limit", 100, "Maximum ready tasks to inspect")
	cmd.Flags().BoolVar(&driverClaimReadyJSON, "json", false, "JSON output")
}

func bindDriverActiveTaskRunsFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverActiveTaskRunsWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	cmd.Flags().StringVar(&driverActiveTaskRunsDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	cmd.Flags().StringVar(&driverActiveTaskRunsNodeID, "node-id", "", "Parent DriverRun node ID")
	cmd.Flags().StringVar(&driverActiveTaskRunsLeaseID, "lease-id", "", "Parent DriverRun lease ID")
	cmd.Flags().Int64Var(&driverActiveTaskRunsFence, "fencing-token", 0, "Parent DriverRun fencing token")
	cmd.Flags().StringVar(&driverActiveTaskRunsEpicID, "epic-id", "", "Epic ID metadata (default: parent DriverRun epic)")
	cmd.Flags().IntVar(&driverActiveTaskRunsLimit, "limit", 100, "Maximum active task runs to return")
	cmd.Flags().BoolVar(&driverActiveTaskRunsJSON, "json", false, "JSON output")
}

func bindDriverCompleteTaskFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverCompleteTaskWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	cmd.Flags().StringVar(&driverCompleteTaskDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	cmd.Flags().StringVar(&driverCompleteTaskNodeID, "node-id", "", "Parent DriverRun node ID")
	cmd.Flags().StringVar(&driverCompleteTaskLeaseID, "lease-id", "", "Parent DriverRun lease ID")
	cmd.Flags().Int64Var(&driverCompleteTaskFence, "fencing-token", 0, "Parent DriverRun fencing token")
	cmd.Flags().StringVar(&driverCompleteTaskTaskID, "task-id", "", "FleetDB task ID")
	cmd.Flags().StringVar(&driverCompleteTaskTaskRunID, "task-run-id", "", "TaskRun ID to complete through FleetDB CompleteRun")
	cmd.Flags().StringVar(&driverCompleteTaskCompletionID, "completion-id", "", "Completion idempotency key (default: complete-<task-run-id>)")
	cmd.Flags().StringVar(&driverCompleteTaskLeaseToken, "lease-token", "", "TaskRun lease token (default: LOOM_TASK_RUN_LEASE_TOKEN or LOOM_RUNNER_LEASE_TOKEN)")
	cmd.Flags().StringArrayVar(&driverCompleteTaskArtifactIDs, "artifact-id", nil, "Required artifact ID; may be repeated")
	cmd.Flags().StringVar(&driverCompleteTaskLogsRef, "logs-ref", "", "Logs artifact/ref for CompleteRun")
	cmd.Flags().StringVar(&driverCompleteTaskArtifactsRef, "artifacts-ref", "", "Artifact bundle/ref for CompleteRun")
	cmd.Flags().StringVar(&driverCompleteTaskReason, "reason", "", "Completion reason")
	cmd.Flags().BoolVar(&driverCompleteTaskJSON, "json", false, "JSON output")
}

func bindDriverReleaseTaskFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverReleaseTaskWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	cmd.Flags().StringVar(&driverReleaseTaskDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	cmd.Flags().StringVar(&driverReleaseTaskNodeID, "node-id", "", "Parent DriverRun node ID")
	cmd.Flags().StringVar(&driverReleaseTaskLeaseID, "lease-id", "", "Parent DriverRun lease ID")
	cmd.Flags().Int64Var(&driverReleaseTaskFence, "fencing-token", 0, "Parent DriverRun fencing token")
	cmd.Flags().StringVar(&driverReleaseTaskTaskID, "task-id", "", "FleetDB task ID")
	cmd.Flags().StringVar(&driverReleaseTaskActor, "actor", "", "Release actor (default: driver-run:<driver-run-id>)")
	cmd.Flags().BoolVar(&driverReleaseTaskJSON, "json", false, "JSON output")
	_ = cmd.MarkFlagRequired("task-id")
}

func bindDriverRecoverStaleTasksFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverRecoverStaleWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	cmd.Flags().StringVar(&driverRecoverStaleDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID or positional argument)")
	cmd.Flags().StringVar(&driverRecoverStaleBefore, "stale-before", "", "Recover task runs last observed before this RFC3339 timestamp")
	cmd.Flags().Int64Var(&driverRecoverStaleMaxAgeSeconds, "max-age-seconds", 300, "Recover task runs last observed more than this many seconds ago")
	cmd.Flags().StringVar(&driverRecoverStaleErrorClass, "error-class", "stale_task_run", "Error class recorded on recovered task runs")
	cmd.Flags().StringVar(&driverRecoverStaleErrorMessage, "error-message", "task run heartbeat is stale", "Error message recorded on recovered task runs")
	cmd.Flags().BoolVar(&driverRecoverStaleJSON, "json", false, "JSON output")
}

func runDriverClaimReady(_ *cobra.Command, _ []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, parent, err := resolveRunningDriverRun(ctx, h, driverClaimReadyWorkspaceKey, driverClaimReadyDriverRunID, driverClaimReadyNodeID, driverClaimReadyLeaseID, driverClaimReadyFence)
		if err != nil {
			return err
		}
		epicID := firstNonEmpty(driverClaimReadyEpicID, parent.EpicID, driverRunPayloadEpicID(parent.Payload))
		actor := firstNonEmpty(driverClaimReadyActor, driverRunActor(parent.RunID))
		issueBackend, err := newDriverIssueBackend(h, ws, actor)
		if err != nil {
			return err
		}
		claimed, err := driverpkg.ClaimReadyTask(ctx, issueBackend, driverpkg.TaskClaimOptions{
			EpicID: epicID,
			Actor:  actor,
			Limit:  driverClaimReadyLimit,
		})
		if err != nil {
			return fmt.Errorf("claim ready task: %w", err)
		}
		if driverClaimReadyJSON {
			return cmdstore.WriteJSON(claimed)
		}
		if claimed == nil {
			fmt.Println("No ready task claimed")
			return nil
		}
		fmt.Printf("Claimed task %s for %s\n", claimed.ID, actor)
		return nil
	})
}

func runDriverActiveTaskRuns(_ *cobra.Command, _ []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, parent, err := resolveRunningDriverRun(ctx, h, driverActiveTaskRunsWorkspaceKey, driverActiveTaskRunsDriverRunID, driverActiveTaskRunsNodeID, driverActiveTaskRunsLeaseID, driverActiveTaskRunsFence)
		if err != nil {
			return err
		}
		epicID := firstNonEmpty(driverActiveTaskRunsEpicID, parent.EpicID, driverRunPayloadEpicID(parent.Payload))
		active, err := driverpkg.ListActiveTaskRuns(ctx, h.Store, driverpkg.ActiveTaskRunsOptions{
			WorkspaceKey: ws,
			DriverRunID:  parent.RunID,
			EpicID:       epicID,
			Limit:        driverActiveTaskRunsLimit,
		})
		if err != nil {
			return fmt.Errorf("list active task runs: %w", err)
		}
		if driverActiveTaskRunsJSON {
			return cmdstore.WriteJSON(active)
		}
		fmt.Printf("Driver run %s has %d active child task run(s)\n", active.DriverRunID, active.ActiveCount)
		return nil
	})
}

func runDriverCompleteTask(_ *cobra.Command, _ []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, _, err := resolveRunningDriverRun(ctx, h, driverCompleteTaskWorkspaceKey, driverCompleteTaskDriverRunID, driverCompleteTaskNodeID, driverCompleteTaskLeaseID, driverCompleteTaskFence)
		if err != nil {
			return err
		}
		taskRunID := strings.TrimSpace(driverCompleteTaskTaskRunID)
		if taskRunID == "" {
			return fmt.Errorf("--task-run-id is required for fenced driver completion: %w", domain.ErrInvalid)
		}
		result, err := completeDriverTaskRun(ctx, h.Store.TaskRuns(), ws, taskRunID, driverTaskRunCompletionOptions{
			TaskID:       driverCompleteTaskTaskID,
			CompletionID: driverCompleteTaskCompletionID,
			LeaseToken:   resolveDriverCompleteTaskLeaseToken(),
			ArtifactIDs:  driverCompleteTaskArtifactIDs,
			LogsRef:      driverCompleteTaskLogsRef,
			ArtifactsRef: driverCompleteTaskArtifactsRef,
			Reason:       driverCompleteTaskReason,
		})
		if err != nil {
			return fmt.Errorf("complete task run: %w", err)
		}
		if driverCompleteTaskJSON {
			return cmdstore.WriteJSON(result)
		}
		fmt.Printf("Completed task %s\n", result.ID)
		return nil
	})
}

type driverTaskRunCompletionOptions = driverpkg.DriverTaskRunCompletionOptions

func completeDriverTaskRun(ctx context.Context, taskRuns store.TaskRunStore, ws, taskRunID string, opts driverTaskRunCompletionOptions) (*driverpkg.TaskMutationResult, error) {
	return driverpkg.CompleteDriverTaskRun(ctx, taskRuns, ws, taskRunID, opts)
}

func resolveDriverCompleteTaskLeaseToken() string {
	return firstNonEmpty(driverCompleteTaskLeaseToken, os.Getenv("LOOM_TASK_RUN_LEASE_TOKEN"), os.Getenv("LOOM_RUNNER_LEASE_TOKEN"))
}

func runDriverReleaseTask(_ *cobra.Command, _ []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, parent, err := resolveRunningDriverRun(ctx, h, driverReleaseTaskWorkspaceKey, driverReleaseTaskDriverRunID, driverReleaseTaskNodeID, driverReleaseTaskLeaseID, driverReleaseTaskFence)
		if err != nil {
			return err
		}
		actor := firstNonEmpty(driverReleaseTaskActor, driverRunActor(parent.RunID))
		issueBackend, err := newDriverIssueBackend(h, ws, actor)
		if err != nil {
			return err
		}
		result, err := driverpkg.ReleaseTask(ctx, issueBackend, driverpkg.TaskReleaseOptions{
			TaskID: driverReleaseTaskTaskID,
			Actor:  actor,
		})
		if err != nil {
			return fmt.Errorf("release task: %w", err)
		}
		if driverReleaseTaskJSON {
			return cmdstore.WriteJSON(result)
		}
		fmt.Printf("Released task %s\n", result.ID)
		return nil
	})
}

func runDriverRecoverStaleTasks(_ *cobra.Command, args []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, err := resolveDriverWorkspace(ctx, h, driverRecoverStaleWorkspaceKey)
		if err != nil {
			return err
		}
		runID := resolveDriverRunID(driverRecoverStaleDriverRunID)
		if len(args) > 0 {
			runID = firstNonEmpty(args[0], runID)
		}
		if runID == "" {
			return fmt.Errorf("driver-run-id required: %w", domain.ErrInvalid)
		}
		staleBefore, err := parseDriverRecoverStaleBefore(driverRecoverStaleBefore)
		if err != nil {
			return err
		}
		result, err := h.Store.DriverRuns().RecoverStaleTaskRuns(ctx, ws, runID, store.StaleTaskRunRecovery{
			StaleBefore:   staleBefore,
			MaxAgeSeconds: driverRecoverStaleMaxAgeSeconds,
			ErrorClass:    driverRecoverStaleErrorClass,
			ErrorMessage:  driverRecoverStaleErrorMessage,
		})
		if err != nil {
			return fmt.Errorf("recover stale task runs: %w", err)
		}
		if driverRecoverStaleJSON {
			return cmdstore.WriteJSON(result)
		}
		fmt.Printf("Recovered %d stale task run(s) for driver run %s\n", result.Recovered, result.DriverRunID)
		if result.Released > 0 {
			fmt.Printf("Released %d task claim(s)\n", result.Released)
		}
		if result.SkippedFresh > 0 || result.SkippedActorMismatch > 0 || result.SkippedIssueNotFound > 0 {
			fmt.Printf("Skipped fresh=%d actor_mismatch=%d issue_not_found=%d\n", result.SkippedFresh, result.SkippedActorMismatch, result.SkippedIssueNotFound)
		}
		return nil
	})
}

func parseDriverRecoverStaleBefore(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse --stale-before as RFC3339: %w", err)
	}
	return parsed.UTC(), nil
}
