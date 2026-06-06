package driver

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	driverPublishName string
	driverPublishJSON bool

	driverRunEpic           string
	driverRunID             string
	driverRunIdempotencyKey string
	driverRunEntrypoint     string
	driverRunInput          []string
	driverRunJSON           bool

	driverExecTaskWorkspaceKey       string
	driverExecTaskDriverRunID        string
	driverExecTaskDriverStepID       string
	driverExecTaskTaskRunID          string
	driverExecTaskTaskID             string
	driverExecTaskWorkerProfileID    string
	driverExecTaskProviderProfile    string
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

	driverClaimReadyWorkspaceKey string
	driverClaimReadyDriverRunID  string
	driverClaimReadyNodeID       string
	driverClaimReadyLeaseID      string
	driverClaimReadyFence        int64
	driverClaimReadyEpicID       string
	driverClaimReadyActor        string
	driverClaimReadyLimit        int
	driverClaimReadyJSON         bool

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
	driverCompleteTaskSession      string
	driverCompleteTaskActor        string
	driverCompleteTaskLegacyClose  bool
	driverCompleteTaskForce        bool
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

var driverCmd = &cobra.Command{
	Use:     "driver",
	Short:   "Publish and run dynamic Loom drivers",
	GroupID: "workspace",
}

var driverPublishCmd = &cobra.Command{
	Use:   "publish <.loom/workflows/name.ts>",
	Short: "Publish a TypeScript workflow as an immutable DriverVersion",
	Long: `Publish a TypeScript workflow as an immutable DriverVersion.

The source must live under .loom/workflows and export a named run entrypoint.
The publisher writes a deterministic Flue-compatible bundle under
.loom/drivers/<driver>/<version> and records the Driver/DriverVersion in the
active workspace.`,
	Args: cobra.ExactArgs(1),
	RunE: runDriverPublish,
}

var driverRunCmd = &cobra.Command{
	Use:   "run <driver_id>",
	Short: "Record a queued DriverRun for a published driver",
	Long: `Record a queued DriverRun for a published driver.

The run is pinned to the driver's active DriverVersion. Driver execution is
handled by a later runtime/executor slice; this command records durable work
without claiming or running it synchronously.`,
	Args: cobra.ExactArgs(1),
	RunE: runDriverRun,
}

var driverExecTaskCmd = &cobra.Command{
	Use:    "exec-task",
	Short:  "Execute one child task run for a driver runtime",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDriverExecTask,
}

var driverClaimReadyCmd = &cobra.Command{
	Use:    "claim-ready",
	Short:  "Claim one ready task for a driver runtime",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDriverClaimReady,
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

func init() {
	driverPublishCmd.Flags().StringVar(&driverPublishName, "name", "", "Driver name (default: source filename without .ts)")
	driverPublishCmd.Flags().BoolVar(&driverPublishJSON, "json", false, "JSON output")

	driverRunCmd.Flags().StringVar(&driverRunEpic, "epic", "", "Epic ID to pass as input.epicId")
	driverRunCmd.Flags().StringVar(&driverRunID, "run-id", "", "Run ID (default: generated)")
	driverRunCmd.Flags().StringVar(&driverRunIdempotencyKey, "idempotency-key", "", "DriverRun admission idempotency key")
	driverRunCmd.Flags().StringVar(&driverRunEntrypoint, "entrypoint", driverpkg.EntrypointRun, "Driver entrypoint")
	driverRunCmd.Flags().StringArrayVar(&driverRunInput, "input", nil, "Input key=value (repeatable)")
	driverRunCmd.Flags().BoolVar(&driverRunJSON, "json", false, "JSON output")
	_ = driverRunCmd.MarkFlagRequired("epic")

	driverExecTaskCmd.Flags().StringVar(&driverExecTaskWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	driverExecTaskCmd.Flags().StringVar(&driverExecTaskDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	driverExecTaskCmd.Flags().StringVar(&driverExecTaskDriverStepID, "driver-step-id", "", "DriverStep ID (default: LOOM_DRIVER_STEP_ID)")
	driverExecTaskCmd.Flags().StringVar(&driverExecTaskTaskRunID, "task-run-id", "", "TaskRun ID (default: generated)")
	driverExecTaskCmd.Flags().StringVar(&driverExecTaskTaskID, "task-id", "", "FleetDB task ID")
	driverExecTaskCmd.Flags().StringVar(&driverExecTaskWorkerProfileID, "worker-profile-id", "", "Worker profile ID")
	driverExecTaskCmd.Flags().StringVar(&driverExecTaskProviderProfile, "provider-profile", "", "Provider profile requested by the driver")
	driverExecTaskCmd.Flags().StringVar(&driverExecTaskNodeID, "node-id", "", "Executor node ID")
	driverExecTaskCmd.Flags().StringVar(&driverExecTaskRunnerID, "runner-id", "", "Runner ID")
	driverExecTaskCmd.Flags().StringVar(&driverExecTaskLeaseID, "lease-id", "", "Parent DriverRun lease ID")
	driverExecTaskCmd.Flags().Int64Var(&driverExecTaskFencingToken, "fencing-token", 0, "Parent DriverRun fencing token")
	driverExecTaskCmd.Flags().StringSliceVar(&driverExecTaskSupportedProviders, "supported-provider", nil, "Supported task provider (repeatable)")
	driverExecTaskCmd.Flags().StringSliceVar(&driverExecTaskCapabilities, "capability", nil, "Runner capability (repeatable)")
	driverExecTaskCmd.Flags().StringVar(&driverExecTaskSandboxProvider, "sandbox-provider", "", "Sandbox provider")
	driverExecTaskCmd.Flags().StringVar(&driverExecTaskSandboxID, "sandbox-id", "", "Sandbox ID")
	driverExecTaskCmd.Flags().StringVar(&driverExecTaskSandboxCWD, "sandbox-cwd", "", "Sandbox working directory")
	driverExecTaskCmd.Flags().StringVar(&driverExecTaskSandboxRepoRef, "sandbox-repo-ref", "", "Sandbox repository ref")
	driverExecTaskCmd.Flags().BoolVar(&driverExecTaskDeferCompletion, "defer-completion", false, "Return execution result but leave successful TaskRun running for CompleteRun")
	driverExecTaskCmd.Flags().BoolVar(&driverExecTaskJSON, "json", false, "JSON output")
	_ = driverExecTaskCmd.MarkFlagRequired("task-id")

	driverClaimReadyCmd.Flags().StringVar(&driverClaimReadyWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	driverClaimReadyCmd.Flags().StringVar(&driverClaimReadyDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	driverClaimReadyCmd.Flags().StringVar(&driverClaimReadyNodeID, "node-id", "", "Parent DriverRun node ID")
	driverClaimReadyCmd.Flags().StringVar(&driverClaimReadyLeaseID, "lease-id", "", "Parent DriverRun lease ID")
	driverClaimReadyCmd.Flags().Int64Var(&driverClaimReadyFence, "fencing-token", 0, "Parent DriverRun fencing token")
	driverClaimReadyCmd.Flags().StringVar(&driverClaimReadyEpicID, "epic-id", "", "Epic ID to scope ready tasks (default: parent DriverRun epic)")
	driverClaimReadyCmd.Flags().StringVar(&driverClaimReadyActor, "actor", "", "Claim actor (default: driver-run:<driver-run-id>)")
	driverClaimReadyCmd.Flags().IntVar(&driverClaimReadyLimit, "limit", 100, "Maximum ready tasks to inspect")
	driverClaimReadyCmd.Flags().BoolVar(&driverClaimReadyJSON, "json", false, "JSON output")

	driverCompleteTaskCmd.Flags().StringVar(&driverCompleteTaskWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	driverCompleteTaskCmd.Flags().StringVar(&driverCompleteTaskDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	driverCompleteTaskCmd.Flags().StringVar(&driverCompleteTaskNodeID, "node-id", "", "Parent DriverRun node ID")
	driverCompleteTaskCmd.Flags().StringVar(&driverCompleteTaskLeaseID, "lease-id", "", "Parent DriverRun lease ID")
	driverCompleteTaskCmd.Flags().Int64Var(&driverCompleteTaskFence, "fencing-token", 0, "Parent DriverRun fencing token")
	driverCompleteTaskCmd.Flags().StringVar(&driverCompleteTaskTaskID, "task-id", "", "FleetDB task ID")
	driverCompleteTaskCmd.Flags().StringVar(&driverCompleteTaskTaskRunID, "task-run-id", "", "TaskRun ID to complete through FleetDB CompleteRun")
	driverCompleteTaskCmd.Flags().StringVar(&driverCompleteTaskCompletionID, "completion-id", "", "Completion idempotency key (default: complete-<task-run-id>)")
	driverCompleteTaskCmd.Flags().StringVar(&driverCompleteTaskLeaseToken, "lease-token", "", "TaskRun lease token (default: LOOM_TASK_RUN_LEASE_TOKEN or LOOM_RUNNER_LEASE_TOKEN)")
	driverCompleteTaskCmd.Flags().StringArrayVar(&driverCompleteTaskArtifactIDs, "artifact-id", nil, "Required artifact ID; may be repeated")
	driverCompleteTaskCmd.Flags().StringVar(&driverCompleteTaskLogsRef, "logs-ref", "", "Logs artifact/ref for CompleteRun")
	driverCompleteTaskCmd.Flags().StringVar(&driverCompleteTaskArtifactsRef, "artifacts-ref", "", "Artifact bundle/ref for CompleteRun")
	driverCompleteTaskCmd.Flags().StringVar(&driverCompleteTaskReason, "reason", "", "Completion reason")
	driverCompleteTaskCmd.Flags().StringVar(&driverCompleteTaskSession, "session", "", "Session identifier associated with completion")
	driverCompleteTaskCmd.Flags().StringVar(&driverCompleteTaskActor, "actor", "", "Completion actor (default: driver-run:<driver-run-id>)")
	driverCompleteTaskCmd.Flags().BoolVar(&driverCompleteTaskLegacyClose, "legacy-task-close", false, "Use legacy direct task close without a child TaskRun")
	driverCompleteTaskCmd.Flags().BoolVar(&driverCompleteTaskForce, "force", false, "Force completion when backend permits it")
	driverCompleteTaskCmd.Flags().BoolVar(&driverCompleteTaskJSON, "json", false, "JSON output")

	driverReleaseTaskCmd.Flags().StringVar(&driverReleaseTaskWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	driverReleaseTaskCmd.Flags().StringVar(&driverReleaseTaskDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	driverReleaseTaskCmd.Flags().StringVar(&driverReleaseTaskNodeID, "node-id", "", "Parent DriverRun node ID")
	driverReleaseTaskCmd.Flags().StringVar(&driverReleaseTaskLeaseID, "lease-id", "", "Parent DriverRun lease ID")
	driverReleaseTaskCmd.Flags().Int64Var(&driverReleaseTaskFence, "fencing-token", 0, "Parent DriverRun fencing token")
	driverReleaseTaskCmd.Flags().StringVar(&driverReleaseTaskTaskID, "task-id", "", "FleetDB task ID")
	driverReleaseTaskCmd.Flags().StringVar(&driverReleaseTaskActor, "actor", "", "Release actor (default: driver-run:<driver-run-id>)")
	driverReleaseTaskCmd.Flags().BoolVar(&driverReleaseTaskJSON, "json", false, "JSON output")
	_ = driverReleaseTaskCmd.MarkFlagRequired("task-id")

	driverRecoverStaleTasksCmd.Flags().StringVar(&driverRecoverStaleWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	driverRecoverStaleTasksCmd.Flags().StringVar(&driverRecoverStaleDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID or positional argument)")
	driverRecoverStaleTasksCmd.Flags().StringVar(&driverRecoverStaleBefore, "stale-before", "", "Recover task runs last observed before this RFC3339 timestamp")
	driverRecoverStaleTasksCmd.Flags().Int64Var(&driverRecoverStaleMaxAgeSeconds, "max-age-seconds", 300, "Recover task runs last observed more than this many seconds ago")
	driverRecoverStaleTasksCmd.Flags().StringVar(&driverRecoverStaleErrorClass, "error-class", "stale_task_run", "Error class recorded on recovered task runs")
	driverRecoverStaleTasksCmd.Flags().StringVar(&driverRecoverStaleErrorMessage, "error-message", "task run heartbeat is stale", "Error message recorded on recovered task runs")
	driverRecoverStaleTasksCmd.Flags().BoolVar(&driverRecoverStaleJSON, "json", false, "JSON output")

	driverCmd.AddCommand(driverPublishCmd, driverRunCmd, driverExecTaskCmd, driverClaimReadyCmd, driverCompleteTaskCmd, driverReleaseTaskCmd, driverRecoverStaleTasksCmd)
	cli.RegisterCommand(driverCmd)
}

func runDriverPublish(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		workDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve work dir: %w", err)
		}
		result, err := driverpkg.PublishWorkflow(ctx, h.Store, driverpkg.PublishOptions{
			WorkspaceKey: ws,
			WorkDir:      workDir,
			SourcePath:   args[0],
			DriverName:   driverPublishName,
			CreatedBy:    publishActor(),
		})
		if driverPublishJSON && result != nil {
			if writeErr := cmdstore.WriteJSON(result); writeErr != nil && err == nil {
				err = writeErr
			}
		}
		if err != nil {
			if result != nil && result.Version != nil && !driverPublishJSON {
				fmt.Fprintf(os.Stderr, "Recorded failed driver version %s/%s (%s)\n", ws, result.Version.VersionID, result.Version.ValidationStatus)
			}
			return fmt.Errorf("publish driver: %w", err)
		}
		if !driverPublishJSON {
			fmt.Printf("Published driver %s version %s\n", result.Driver.DriverID, result.Version.VersionID)
			fmt.Printf("Source: %s %s\n", result.Version.SourceRef, result.Version.SourceDigest)
			fmt.Printf("Bundle: %s %s\n", result.Version.BundleRef, result.Version.BundleDigest)
		}
		return nil
	})
}

func runDriverRun(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		input, err := parseDriverRunInput(driverRunInput)
		if err != nil {
			return err
		}
		run, err := driverpkg.CreateDriverRun(ctx, h.Store, driverpkg.RunOptions{
			WorkspaceKey:   ws,
			DriverID:       args[0],
			EpicID:         driverRunEpic,
			RunID:          driverRunID,
			IdempotencyKey: driverRunIdempotencyKey,
			Entrypoint:     driverRunEntrypoint,
			Input:          input,
		})
		if err != nil {
			return fmt.Errorf("create driver run: %w", err)
		}
		if driverRunJSON {
			return cmdstore.WriteJSON(run)
		}
		fmt.Printf("Recorded driver run %s (%s)\n", run.RunID, run.Status)
		fmt.Printf("Driver: %s version %s\n", run.DriverID, run.DriverVersionID)
		fmt.Printf("Epic: %s\n", run.EpicID)
		fmt.Println("Execution pending: start a driver executor/runtime to claim queued runs.")
		return nil
	})
}

func runDriverExecTask(_ *cobra.Command, _ []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws := firstNonEmpty(driverExecTaskWorkspaceKey, os.Getenv("LOOM_DRIVER_WORKSPACE"))
		if ws == "" {
			resolved, err := cmdstore.ActiveWorkspace(ctx, h.Store)
			if err != nil {
				return err
			}
			ws = resolved
		}
		driverRunID := firstNonEmpty(driverExecTaskDriverRunID, os.Getenv("LOOM_DRIVER_RUN_ID"))
		driverStepID := firstNonEmpty(driverExecTaskDriverStepID, os.Getenv("LOOM_DRIVER_STEP_ID"))
		nodeID := firstNonEmpty(driverExecTaskNodeID, os.Getenv("LOOM_DRIVER_NODE_ID"))
		leaseID := firstNonEmpty(driverExecTaskLeaseID, os.Getenv("LOOM_DRIVER_LEASE_ID"))
		fencingToken, err := resolveDriverRunFencingToken(driverExecTaskFencingToken)
		if err != nil {
			return err
		}
		outcome, err := driverpkg.RequestTaskRunWithResult(ctx, h.Store, driverpkg.TaskRunRequestOptions{
			WorkspaceKey:       ws,
			DriverRunID:        driverRunID,
			DriverStepID:       driverStepID,
			TaskRunID:          driverExecTaskTaskRunID,
			TaskID:             driverExecTaskTaskID,
			WorkerProfileID:    driverExecTaskWorkerProfileID,
			ProviderProfile:    driverExecTaskProviderProfile,
			ParentNodeID:       nodeID,
			ParentLeaseID:      leaseID,
			ParentFence:        fencingToken,
			NodeID:             nodeID,
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
		result := driverpkg.TaskRunResultFromOutcome(outcome)
		if driverExecTaskJSON {
			return cmdstore.WriteJSON(result)
		}
		fmt.Printf("Task run %s %s for task %s\n", result.ID, result.Status, result.TaskID)
		if result.ErrorMessage != "" {
			fmt.Println(result.ErrorMessage)
		}
		return nil
	})
}

func runDriverClaimReady(_ *cobra.Command, _ []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, parent, err := resolveRunningDriverRun(ctx, h, driverClaimReadyWorkspaceKey, driverClaimReadyDriverRunID, driverClaimReadyNodeID, driverClaimReadyLeaseID, driverClaimReadyFence)
		if err != nil {
			return err
		}
		epicID := firstNonEmpty(driverClaimReadyEpicID, parent.EpicID, parent.Input["epicId"])
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

func runDriverCompleteTask(_ *cobra.Command, _ []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, parent, err := resolveRunningDriverRun(ctx, h, driverCompleteTaskWorkspaceKey, driverCompleteTaskDriverRunID, driverCompleteTaskNodeID, driverCompleteTaskLeaseID, driverCompleteTaskFence)
		if err != nil {
			return err
		}
		taskRunID := strings.TrimSpace(driverCompleteTaskTaskRunID)
		if taskRunID != "" {
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
		}
		if !driverCompleteTaskLegacyClose {
			return fmt.Errorf("--task-run-id is required for fenced driver completion: %w", domain.ErrInvalid)
		}
		if strings.TrimSpace(driverCompleteTaskTaskID) == "" {
			return fmt.Errorf("--task-id is required when --task-run-id is not provided: %w", domain.ErrInvalid)
		}
		actor := firstNonEmpty(driverCompleteTaskActor, driverRunActor(parent.RunID))
		issueBackend, err := newDriverIssueBackend(h, ws, actor)
		if err != nil {
			return err
		}
		result, err := driverpkg.CompleteTask(ctx, issueBackend, driverpkg.TaskCompleteOptions{
			TaskID:  driverCompleteTaskTaskID,
			Reason:  driverCompleteTaskReason,
			Session: driverCompleteTaskSession,
			Force:   driverCompleteTaskForce,
		})
		if err != nil {
			return fmt.Errorf("complete task: %w", err)
		}
		if driverCompleteTaskJSON {
			return cmdstore.WriteJSON(result)
		}
		fmt.Printf("Completed task %s\n", result.ID)
		return nil
	})
}

type driverTaskRunCompletionOptions struct {
	TaskID       string
	CompletionID string
	LeaseToken   string
	ArtifactIDs  []string
	LogsRef      string
	ArtifactsRef string
	Reason       string
}

func completeDriverTaskRun(ctx context.Context, taskRuns store.TaskRunStore, ws, taskRunID string, opts driverTaskRunCompletionOptions) (*driverpkg.TaskMutationResult, error) {
	taskRun, err := taskRuns.Get(ctx, ws, taskRunID)
	if err != nil {
		return nil, fmt.Errorf("get task run: %w", err)
	}
	if opts.TaskID != "" && taskRun.TaskID != opts.TaskID {
		return nil, fmt.Errorf("task run %q belongs to task %q, not %q: %w", taskRunID, taskRun.TaskID, opts.TaskID, domain.ErrInvalid)
	}
	completionID := strings.TrimSpace(opts.CompletionID)
	if completionID == "" {
		completionID = "complete-" + taskRunID
	}
	reason := strings.TrimSpace(opts.Reason)
	if reason == "" {
		reason = "completed by driver"
	}
	completed, err := taskRuns.Complete(ctx, ws, taskRunID, store.TaskRunComplete{
		CompletionID:        completionID,
		NodeID:              taskRun.NodeID,
		LeaseID:             taskRun.LeaseID,
		LeaseToken:          opts.LeaseToken,
		FencingToken:        taskRun.FencingToken,
		Status:              domain.TaskRunCompleted,
		LogsRef:             opts.LogsRef,
		ArtifactsRef:        opts.ArtifactsRef,
		RequiredArtifactIDs: opts.ArtifactIDs,
		RequireArtifacts:    len(opts.ArtifactIDs) > 0,
		CloseTask:           true,
		CloseReason:         reason,
	})
	if err != nil {
		return nil, err
	}
	return &driverpkg.TaskMutationResult{ID: completed.TaskID, Status: string(completed.Status), Reason: reason}, nil
}

func resolveDriverCompleteTaskLeaseToken() string {
	return firstNonEmpty(driverCompleteTaskLeaseToken, os.Getenv("LOOM_TASK_RUN_LEASE_TOKEN"), os.Getenv("LOOM_RUNNER_LEASE_TOKEN"))
}

func currentWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
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
		ws := firstNonEmpty(driverRecoverStaleWorkspaceKey, os.Getenv("LOOM_DRIVER_WORKSPACE"))
		if ws == "" {
			resolved, err := cmdstore.ActiveWorkspace(ctx, h.Store)
			if err != nil {
				return err
			}
			ws = resolved
		}
		runID := firstNonEmpty(driverRecoverStaleDriverRunID, os.Getenv("LOOM_DRIVER_RUN_ID"))
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

func resolveRunningDriverRun(ctx context.Context, h *bootstrap.StoreHandle, workspaceKey, driverRunID, nodeID, leaseID string, fencingToken int64) (string, *domain.DriverRun, error) {
	ws := firstNonEmpty(workspaceKey, os.Getenv("LOOM_DRIVER_WORKSPACE"))
	if ws == "" {
		resolved, err := cmdstore.ActiveWorkspace(ctx, h.Store)
		if err != nil {
			return "", nil, err
		}
		ws = resolved
	}
	runID := firstNonEmpty(driverRunID, os.Getenv("LOOM_DRIVER_RUN_ID"))
	if runID == "" {
		return "", nil, fmt.Errorf("driver-run-id required: %w", domain.ErrInvalid)
	}
	parent, err := h.Store.DriverRuns().Get(ctx, ws, runID)
	if err != nil {
		return "", nil, fmt.Errorf("get parent driver run: %w", err)
	}
	if parent.Status != domain.DriverRunRunning {
		return "", nil, fmt.Errorf("driver run %q is %s, want running: %w", runID, parent.Status, domain.ErrInvalidTransition)
	}
	if parent.LeaseID != "" || parent.FencingToken != 0 {
		ownerNode := firstNonEmpty(nodeID, os.Getenv("LOOM_DRIVER_NODE_ID"))
		ownerLease := firstNonEmpty(leaseID, os.Getenv("LOOM_DRIVER_LEASE_ID"))
		ownerFence, err := resolveDriverRunFencingToken(fencingToken)
		if err != nil {
			return "", nil, err
		}
		if ownerNode == "" || ownerLease == "" || ownerFence == 0 {
			return "", nil, fmt.Errorf("driver run %q owner credentials required: %w", runID, domain.ErrNotOwner)
		}
		parent, err = h.Store.DriverRuns().Heartbeat(ctx, ws, runID, ownerNode, ownerLease, ownerFence)
		if err != nil {
			return "", nil, fmt.Errorf("verify driver run owner: %w", err)
		}
	}
	return ws, parent, nil
}

func resolveDriverRunFencingToken(flagValue int64) (int64, error) {
	if flagValue != 0 {
		return flagValue, nil
	}
	raw := strings.TrimSpace(os.Getenv("LOOM_DRIVER_FENCING_TOKEN"))
	if raw == "" {
		return 0, nil
	}
	token, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || token <= 0 {
		if err == nil {
			err = domain.ErrInvalid
		}
		return 0, fmt.Errorf("parse LOOM_DRIVER_FENCING_TOKEN: %w", err)
	}
	return token, nil
}

func newDriverIssueBackend(h *bootstrap.StoreHandle, ws, actor string) (*fleet.FleetBackend, error) {
	issueBackend, err := fleet.New(fleet.Config{
		BaseURL:     h.URL(),
		WorkspaceID: ws,
		APIKey:      os.Getenv(bootstrap.EnvFleetDBAPIKey),
		Actor:       actor,
	})
	if err != nil {
		return nil, fmt.Errorf("create fleet-db issue backend: %w", err)
	}
	return issueBackend, nil
}

func driverRunActor(runID string) string {
	return "driver-run:" + runID
}

func parseDriverRunInput(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("input must be key=value: %q", value)
		}
		out[key] = val
	}
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func publishActor() string {
	if actor := os.Getenv("LOOM_FLEET_ACTOR"); actor != "" {
		return actor
	}
	if actor := os.Getenv("USER"); actor != "" {
		return actor
	}
	return "loom-cli"
}
