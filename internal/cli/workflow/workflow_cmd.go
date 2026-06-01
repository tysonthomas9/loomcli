package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	workflowpkg "github.com/tysonthomas9/loomcli/internal/workflow"
)

var (
	workflowJSON                  bool
	workflowRunInput              string
	workflowRunPayload            string
	workflowRunWait               bool
	workflowRunOnce               bool
	workflowArtifactsJSON         bool
	workflowArtifactsType         string
	workflowLogsJSON              bool
	workflowSessionsJSON          bool
	workflowOperationsJSON        bool
	workflowOperationCancelJSON   bool
	workflowOperationCancelReason string
	workflowToolCallsJSON         bool
	workflowTasksJSON             bool
	workflowShowJSON              bool
	workflowListJSON              bool
	workflowCancelJSON            bool
	workflowRouteAuth             string
	workflowRouteJSON             bool
	workflowTriggerJSON           bool
	workflowTriggerFilter         string

	workflowWithActiveWorkspace = cmdstore.WithActiveWorkspace
	workflowWriteJSON           = cmdstore.WriteJSON
)

var workflowCmd = &cobra.Command{
	Use:     "workflow",
	Short:   "Run and inspect workflow definitions",
	GroupID: "workspace",
}

var workflowListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workflow definitions",
	Args:  cobra.NoArgs,
	RunE:  runWorkflowList,
}

var workflowRunCmd = &cobra.Command{
	Use:   "run <NAME>",
	Short: "Create or resume a workflow run",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowRun,
}

var workflowShowCmd = &cobra.Command{
	Use:   "show <RUN_ID>",
	Short: "Show a workflow run",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowShow,
}

var workflowLogsCmd = &cobra.Command{
	Use:   "logs <RUN_ID>",
	Short: "Show workflow run events",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowLogs,
}

var workflowTasksCmd = &cobra.Command{
	Use:   "tasks <RUN_ID>",
	Short: "Show workflow task runs",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowTasks,
}

var workflowArtifactsCmd = &cobra.Command{
	Use:   "artifacts <RUN_ID>",
	Short: "Show workflow run artifacts",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowArtifacts,
}

var workflowSessionsCmd = &cobra.Command{
	Use:   "sessions <RUN_ID>",
	Short: "Show workflow-linked agent sessions",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowSessions,
}

var workflowOperationsCmd = &cobra.Command{
	Use:   "operations <RUN_ID>",
	Short: "Show workflow-linked agent session operations",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowOperations,
}

var workflowOperationCancelCmd = &cobra.Command{
	Use:   "operation-cancel <OPERATION_ID>",
	Short: "Cancel a durable agent session operation",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowOperationCancel,
}

var workflowToolCallsCmd = &cobra.Command{
	Use:   "tool-calls <RUN_ID>",
	Short: "Show workflow-linked agent session tool calls",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowToolCalls,
}

var workflowCancelCmd = &cobra.Command{
	Use:   "cancel <RUN_ID>",
	Short: "Cancel a live workflow run",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowCancel,
}

var workflowRouteCmd = &cobra.Command{
	Use:   "route",
	Short: "Manage durable workflow route bindings",
}

var workflowRouteBindCmd = &cobra.Command{
	Use:   "bind <WORKFLOW> <PATH>",
	Short: "Create or update a durable POST route binding for a workflow",
	Args:  cobra.ExactArgs(2),
	RunE:  runWorkflowRouteBind,
}

var workflowRouteListCmd = &cobra.Command{
	Use:   "list [WORKFLOW]",
	Short: "List active durable workflow route bindings",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkflowRouteList,
}

var workflowRouteRemoveCmd = &cobra.Command{
	Use:   "remove <WORKFLOW> <PATH>",
	Short: "Disable a durable workflow route binding",
	Args:  cobra.ExactArgs(2),
	RunE:  runWorkflowRouteRemove,
}

var workflowTriggerCmd = &cobra.Command{
	Use:   "trigger",
	Short: "Manage durable workflow trigger bindings",
}

var workflowTriggerBindCmd = &cobra.Command{
	Use:   "bind <WORKFLOW> <EVENT>",
	Short: "Create or update a durable trigger binding for a workflow",
	Args:  cobra.ExactArgs(2),
	RunE:  runWorkflowTriggerBind,
}

var workflowTriggerListCmd = &cobra.Command{
	Use:   "list [WORKFLOW]",
	Short: "List active durable workflow trigger bindings",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkflowTriggerList,
}

var workflowTriggerRemoveCmd = &cobra.Command{
	Use:   "remove <WORKFLOW> <EVENT>",
	Short: "Disable a durable workflow trigger binding",
	Args:  cobra.ExactArgs(2),
	RunE:  runWorkflowTriggerRemove,
}

func init() {
	workflowListCmd.Flags().BoolVar(&workflowListJSON, "json", false, "JSON output")
	workflowRunCmd.Flags().StringVar(&workflowRunInput, "input", "{}", "Workflow input JSON")
	workflowRunCmd.Flags().StringVar(&workflowRunPayload, "payload", "", "Workflow input JSON (alias for --input)")
	workflowRunCmd.Flags().BoolVar(&workflowRunWait, "wait", false, "Poll until the workflow reaches a terminal state")
	workflowRunCmd.Flags().BoolVar(&workflowRunOnce, "once", true, "Run one reconcile pass for built-in workflows")
	workflowRunCmd.Flags().BoolVar(&workflowJSON, "json", false, "JSON output")
	workflowShowCmd.Flags().BoolVar(&workflowShowJSON, "json", false, "JSON output")
	workflowLogsCmd.Flags().BoolVar(&workflowLogsJSON, "json", false, "JSON output")
	workflowTasksCmd.Flags().BoolVar(&workflowTasksJSON, "json", false, "JSON output")
	workflowArtifactsCmd.Flags().BoolVar(&workflowArtifactsJSON, "json", false, "JSON output")
	workflowArtifactsCmd.Flags().StringVar(&workflowArtifactsType, "type", "", "Filter artifacts by type")
	workflowSessionsCmd.Flags().BoolVar(&workflowSessionsJSON, "json", false, "JSON output")
	workflowOperationsCmd.Flags().BoolVar(&workflowOperationsJSON, "json", false, "JSON output")
	workflowOperationCancelCmd.Flags().BoolVar(&workflowOperationCancelJSON, "json", false, "JSON output")
	workflowOperationCancelCmd.Flags().StringVar(&workflowOperationCancelReason, "reason", "", "Cancellation reason")
	workflowToolCallsCmd.Flags().BoolVar(&workflowToolCallsJSON, "json", false, "JSON output")
	workflowCancelCmd.Flags().BoolVar(&workflowCancelJSON, "json", false, "JSON output")
	workflowRouteBindCmd.Flags().StringVar(&workflowRouteAuth, "auth", "workspace", "Route auth policy")
	workflowRouteBindCmd.Flags().BoolVar(&workflowRouteJSON, "json", false, "JSON output")
	workflowRouteListCmd.Flags().BoolVar(&workflowRouteJSON, "json", false, "JSON output")
	workflowRouteRemoveCmd.Flags().BoolVar(&workflowRouteJSON, "json", false, "JSON output")
	workflowTriggerBindCmd.Flags().StringVar(&workflowTriggerFilter, "filter", "{}", "Trigger filter JSON object")
	workflowTriggerBindCmd.Flags().BoolVar(&workflowTriggerJSON, "json", false, "JSON output")
	workflowTriggerListCmd.Flags().BoolVar(&workflowTriggerJSON, "json", false, "JSON output")
	workflowTriggerRemoveCmd.Flags().BoolVar(&workflowTriggerJSON, "json", false, "JSON output")

	workflowRouteCmd.AddCommand(workflowRouteBindCmd, workflowRouteListCmd, workflowRouteRemoveCmd)
	workflowTriggerCmd.AddCommand(workflowTriggerBindCmd, workflowTriggerListCmd, workflowTriggerRemoveCmd)
	workflowCmd.AddCommand(workflowListCmd, workflowRunCmd, workflowShowCmd, workflowLogsCmd, workflowTasksCmd, workflowArtifactsCmd, workflowSessionsCmd, workflowOperationsCmd, workflowOperationCancelCmd, workflowToolCallsCmd, workflowCancelCmd, workflowRouteCmd, workflowTriggerCmd)
	cli.RegisterCommand(workflowCmd)
}

func runWorkflowList(_ *cobra.Command, _ []string) error {
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if err := workflowpkg.EnsureBuiltins(ctx, h.Store, ws); err != nil {
			return err
		}
		defs, err := h.Store.WorkflowDefinitions().List(ctx, ws, store.WorkflowDefinitionFilter{Status: domain.DefinitionStatusActive})
		if err != nil {
			return fmt.Errorf("list workflows: %w", err)
		}
		if workflowListJSON {
			return workflowWriteJSON(defs)
		}
		if len(defs) == 0 {
			fmt.Printf("No workflow definitions in workspace %s\n", ws)
			return nil
		}
		for _, def := range defs {
			fmt.Printf("%-28s %-12s %s\n", def.Name, def.Version, def.Description)
		}
		return nil
	})
}

func runWorkflowRun(_ *cobra.Command, args []string) error {
	input, err := parseWorkflowPayload(workflowRunInput, workflowRunPayload)
	if err != nil {
		return err
	}
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		ib := cli.DefaultIssueBackend()
		if ib == nil {
			return errors.New("no issue backend available")
		}
		run, err := workflowpkg.CreateOrResumeRun(ctx, h.Store, ws, args[0], input, actorName())
		if err != nil {
			return fmt.Errorf("create workflow run: %w", err)
		}
		var result *workflowpkg.BuiltinRunResult
		if workflowRunOnce {
			result, err = workflowpkg.RunOnce(ctx, h.Store, ib, run)
			if err != nil {
				return fmt.Errorf("run workflow: %w", err)
			}
			run = result.Run
		}
		if workflowRunWait {
			run, err = waitWorkflow(ctx, h.Store, ws, run.RunID)
			if err != nil {
				return err
			}
		}
		if workflowJSON {
			if result != nil {
				return workflowWriteJSON(result)
			}
			return workflowWriteJSON(run)
		}
		fmt.Printf("Workflow run %s %s (%s)\n", run.RunID, run.Status, run.WorkflowName)
		if result != nil {
			fmt.Printf("ready=%d open=%d blocked=%d ensured=%d\n", result.ReadyCount, result.OpenCount, result.BlockedCount, len(result.TaskRuns))
		}
		return nil
	})
}

func runWorkflowShow(_ *cobra.Command, args []string) error {
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		run, err := h.Store.WorkflowRuns().Get(ctx, ws, args[0])
		if err != nil {
			return fmt.Errorf("get workflow run: %w", err)
		}
		if workflowShowJSON {
			return workflowWriteJSON(run)
		}
		fmt.Printf("Run:        %s\n", run.RunID)
		fmt.Printf("Workflow:   %s@%s\n", run.WorkflowName, run.WorkflowVersion)
		fmt.Printf("Status:     %s\n", run.Status)
		if run.IdempotencyKey != "" {
			fmt.Printf("Singleton:  %s\n", run.IdempotencyKey)
		}
		if run.WaitCondition != "" {
			fmt.Printf("Waiting:    %s\n", run.WaitCondition)
		}
		if run.ErrorMessage != "" {
			fmt.Printf("Error:      %s\n", run.ErrorMessage)
		}
		return nil
	})
}

func runWorkflowLogs(_ *cobra.Command, args []string) error {
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		events, err := h.Store.RunEvents().List(ctx, ws, store.RunEventFilter{WorkflowRunID: args[0]})
		if err != nil {
			return fmt.Errorf("list workflow events: %w", err)
		}
		if workflowLogsJSON {
			return workflowWriteJSON(events)
		}
		for _, ev := range events {
			fmt.Printf("%03d %-24s %s\n", ev.EventIndex, ev.Type, ev.Message)
		}
		return nil
	})
}

func runWorkflowTasks(_ *cobra.Command, args []string) error {
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		taskRuns, err := h.Store.TaskRuns().List(ctx, ws, store.TaskRunFilter{WorkflowRunID: args[0], Limit: 10000})
		if err != nil {
			return fmt.Errorf("list workflow task runs: %w", err)
		}
		if workflowTasksJSON {
			return workflowWriteJSON(taskRuns)
		}
		for _, taskRun := range taskRuns {
			fmt.Printf("%-26s %-12s %-12s %s\n", taskRun.TaskRunID, taskRun.Status, taskRun.RoleName, taskRun.WorkItemID)
		}
		return nil
	})
}

func runWorkflowArtifacts(_ *cobra.Command, args []string) error {
	runID := strings.TrimSpace(args[0])
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if _, err := h.Store.WorkflowRuns().Get(ctx, ws, runID); err != nil {
			return fmt.Errorf("get workflow run: %w", err)
		}
		artifacts, err := workflowRunArtifacts(ctx, h.Store, ws, runID, workflowArtifactsType)
		if err != nil {
			return err
		}
		if workflowArtifactsJSON {
			return workflowWriteJSON(artifacts)
		}
		for _, artifact := range artifacts {
			fmt.Printf("%-30s %-18s %-24s %s\n", artifact.ArtifactID, artifact.Type, artifact.TaskID, artifact.URI)
		}
		return nil
	})
}

func workflowRunArtifacts(ctx context.Context, st store.Store, ws, runID, typ string) ([]*domain.Artifact, error) {
	taskIDs, sessionIDs, err := workflowRunTaskLinks(ctx, st, ws, runID)
	if err != nil {
		return nil, err
	}
	artifacts, err := st.Artifacts().List(ctx, ws, store.ArtifactFilter{Type: strings.TrimSpace(typ), Limit: 10000})
	if err != nil {
		return nil, fmt.Errorf("list workflow artifacts: %w", err)
	}
	out := make([]*domain.Artifact, 0, len(artifacts))
	seen := map[string]struct{}{}
	generatedPrefix := "artifact:" + runID + ":"
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		if artifact.Metadata["workflow_run_id"] == runID || strings.HasPrefix(artifact.ArtifactID, generatedPrefix) {
			appendUniqueArtifact(&out, seen, artifact)
			continue
		}
		if _, ok := taskIDs[artifact.TaskID]; ok && artifact.TaskID != "" {
			appendUniqueArtifact(&out, seen, artifact)
			continue
		}
		if _, ok := sessionIDs[artifact.SessionID]; ok && artifact.SessionID != "" {
			appendUniqueArtifact(&out, seen, artifact)
		}
	}
	return out, nil
}

func runWorkflowSessions(_ *cobra.Command, args []string) error {
	runID := strings.TrimSpace(args[0])
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if _, err := h.Store.WorkflowRuns().Get(ctx, ws, runID); err != nil {
			return fmt.Errorf("get workflow run: %w", err)
		}
		sessions, err := workflowRunSessions(ctx, h.Store, ws, runID)
		if err != nil {
			return err
		}
		if workflowSessionsJSON {
			return workflowWriteJSON(sessions)
		}
		for _, session := range sessions {
			fmt.Printf("%-30s %-18s %-12s %s\n", session.SessionID, session.AgentID, session.Status, session.TaskID)
		}
		return nil
	})
}

func workflowRunSessions(ctx context.Context, st store.Store, ws, runID string) ([]*domain.AgentSession, error) {
	taskIDs, sessionIDs, err := workflowRunTaskLinks(ctx, st, ws, runID)
	if err != nil {
		return nil, err
	}
	sessions, err := st.AgentSessions().List(ctx, ws, store.AgentSessionFilter{Limit: 10000})
	if err != nil {
		return nil, fmt.Errorf("list workflow agent sessions: %w", err)
	}
	out := make([]*domain.AgentSession, 0, len(sessions))
	seen := map[string]struct{}{}
	generatedPrefix := "session:" + runID + ":"
	for _, session := range sessions {
		if session == nil {
			continue
		}
		if session.Metadata["workflow_run_id"] == runID || strings.HasPrefix(session.SessionID, generatedPrefix) {
			appendUniqueSession(&out, seen, session)
			continue
		}
		if _, ok := taskIDs[session.TaskID]; ok && session.TaskID != "" {
			appendUniqueSession(&out, seen, session)
			continue
		}
		if _, ok := sessionIDs[session.SessionID]; ok && session.SessionID != "" {
			appendUniqueSession(&out, seen, session)
		}
	}
	return out, nil
}

func runWorkflowOperations(_ *cobra.Command, args []string) error {
	runID := strings.TrimSpace(args[0])
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if _, err := h.Store.WorkflowRuns().Get(ctx, ws, runID); err != nil {
			return fmt.Errorf("get workflow run: %w", err)
		}
		operations, err := workflowRunOperations(ctx, h.Store, ws, runID)
		if err != nil {
			return err
		}
		if workflowOperationsJSON {
			return workflowWriteJSON(operations)
		}
		for _, operation := range operations {
			fmt.Printf("%-30s %-18s %-12s %-12s %s\n", operation.OperationID, operation.SessionID, operation.Kind, operation.Status, operation.TaskID)
		}
		return nil
	})
}

func workflowRunOperations(ctx context.Context, st store.Store, ws, runID string) ([]*domain.AgentSessionOperation, error) {
	taskIDs, sessionIDs, err := workflowRunTaskLinks(ctx, st, ws, runID)
	if err != nil {
		return nil, err
	}
	operations, err := st.AgentSessionOperations().List(ctx, ws, store.AgentSessionOperationFilter{Limit: 10000})
	if err != nil {
		return nil, fmt.Errorf("list workflow agent session operations: %w", err)
	}
	out := make([]*domain.AgentSessionOperation, 0, len(operations))
	seen := map[string]struct{}{}
	generatedPrefix := "op:" + runID + ":"
	for _, operation := range operations {
		if operation == nil {
			continue
		}
		if operation.WorkflowRunID == runID || strings.HasPrefix(operation.OperationID, generatedPrefix) {
			appendUniqueOperation(&out, seen, operation)
			continue
		}
		if _, ok := taskIDs[operation.TaskID]; ok && operation.TaskID != "" {
			appendUniqueOperation(&out, seen, operation)
			continue
		}
		if _, ok := sessionIDs[operation.SessionID]; ok && operation.SessionID != "" {
			appendUniqueOperation(&out, seen, operation)
		}
	}
	return out, nil
}

func runWorkflowOperationCancel(_ *cobra.Command, args []string) error {
	operationID := strings.TrimSpace(args[0])
	reason := strings.TrimSpace(workflowOperationCancelReason)
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		now := time.Now().UTC()
		cancel := store.AgentSessionOperationCancel{
			ErrorClass:   "cancelled",
			ErrorMessage: firstNonEmptyString(reason, "agent session operation cancelled"),
			CompletedAt:  now,
			Metadata:     map[string]string{"cancelled_by": actorName()},
		}
		if reason != "" {
			cancel.Metadata["cancel_reason"] = reason
		}
		operation, err := h.Store.AgentSessionOperations().Cancel(ctx, ws, operationID, cancel)
		if err != nil {
			return fmt.Errorf("cancel agent session operation: %w", err)
		}
		if err := cancelOperationToolCalls(ctx, h.Store, ws, operationID, reason, now); err != nil {
			return err
		}
		if operation.WorkflowRunID != "" {
			data := map[string]string{"actor": actorName(), "operation_id": operation.OperationID}
			if reason != "" {
				data["reason"] = reason
			}
			encoded, err := json.Marshal(data)
			if err != nil {
				return fmt.Errorf("encode operation cancellation event: %w", err)
			}
			_, _ = h.Store.RunEvents().Append(ctx, store.RunEventAppend{
				WorkspaceKey:  ws,
				WorkflowRunID: operation.WorkflowRunID,
				TaskRunID:     operation.TaskRunID,
				Type:          "agent_session_operation_cancelled",
				Message:       "agent session operation cancelled",
				Data:          encoded,
			})
		}
		if workflowOperationCancelJSON {
			return workflowWriteJSON(operation)
		}
		fmt.Printf("Cancelled agent session operation %s\n", operation.OperationID)
		return nil
	})
}

func runWorkflowToolCalls(_ *cobra.Command, args []string) error {
	runID := strings.TrimSpace(args[0])
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if _, err := h.Store.WorkflowRuns().Get(ctx, ws, runID); err != nil {
			return fmt.Errorf("get workflow run: %w", err)
		}
		calls, err := workflowRunToolCalls(ctx, h.Store, ws, runID)
		if err != nil {
			return err
		}
		if workflowToolCallsJSON {
			return workflowWriteJSON(calls)
		}
		for _, call := range calls {
			fmt.Printf("%-30s %-24s %-18s %-12s %s\n", call.CallID, call.OperationID, call.Name, call.Status, call.SessionID)
		}
		return nil
	})
}

func workflowRunToolCalls(ctx context.Context, st store.Store, ws, runID string) ([]*domain.AgentSessionToolCall, error) {
	taskIDs, sessionIDs, err := workflowRunTaskLinks(ctx, st, ws, runID)
	if err != nil {
		return nil, err
	}
	calls, err := st.AgentSessionToolCalls().List(ctx, ws, store.AgentSessionToolCallFilter{Limit: 10000})
	if err != nil {
		return nil, fmt.Errorf("list workflow agent session tool calls: %w", err)
	}
	out := make([]*domain.AgentSessionToolCall, 0, len(calls))
	seen := map[string]struct{}{}
	for _, call := range calls {
		if call == nil {
			continue
		}
		if call.WorkflowRunID == runID {
			appendUniqueToolCall(&out, seen, call)
			continue
		}
		if _, ok := taskIDs[call.TaskID]; ok && call.TaskID != "" {
			appendUniqueToolCall(&out, seen, call)
			continue
		}
		if _, ok := sessionIDs[call.SessionID]; ok && call.SessionID != "" {
			appendUniqueToolCall(&out, seen, call)
		}
	}
	return out, nil
}

func cancelOperationToolCalls(ctx context.Context, st store.Store, ws, operationID, reason string, completedAt time.Time) error {
	if st.AgentSessionToolCalls() == nil {
		return nil
	}
	calls, err := st.AgentSessionToolCalls().List(ctx, ws, store.AgentSessionToolCallFilter{OperationID: operationID, Limit: 10000})
	if err != nil {
		return fmt.Errorf("list agent session tool calls for operation %s: %w", operationID, err)
	}
	for _, call := range calls {
		if call == nil || terminalToolCallStatus(call.Status) {
			continue
		}
		if _, err := st.AgentSessionToolCalls().Upsert(ctx, store.AgentSessionToolCallUpsert{
			WorkspaceKey:        ws,
			CallID:              call.CallID,
			ProviderCallID:      call.ProviderCallID,
			OperationID:         call.OperationID,
			SessionID:           call.SessionID,
			AgentID:             call.AgentID,
			WorkflowRunID:       call.WorkflowRunID,
			TaskRunID:           call.TaskRunID,
			TaskID:              call.TaskID,
			Name:                call.Name,
			Status:              "cancelled",
			AuthorizationStatus: call.AuthorizationStatus,
			IdempotencyKey:      call.IdempotencyKey,
			ToolVersion:         call.ToolVersion,
			SourceHash:          call.SourceHash,
			Handler:             call.Handler,
			Runtime:             call.Runtime,
			Timeout:             call.Timeout,
			Cancellable:         call.Cancellable,
			ReadOnly:            call.ReadOnly,
			Redacted:            call.Redacted,
			Args:                call.Args,
			Result:              call.Result,
			ErrorClass:          firstNonEmptyString(call.ErrorClass, "cancelled"),
			ErrorMessage:        firstNonEmptyString(reason, call.ErrorMessage, "agent session operation cancelled"),
			StartedAt:           call.StartedAt,
			CompletedAt:         &completedAt,
			DurationMS:          call.DurationMS,
			Metadata:            call.Metadata,
		}); err != nil {
			return fmt.Errorf("cancel agent session tool call %s: %w", call.CallID, err)
		}
	}
	return nil
}

func terminalToolCallStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func workflowRunTaskLinks(ctx context.Context, st store.Store, ws, runID string) (map[string]struct{}, map[string]struct{}, error) {
	taskRuns, err := st.TaskRuns().List(ctx, ws, store.TaskRunFilter{WorkflowRunID: runID, Limit: 10000})
	if err != nil {
		return nil, nil, fmt.Errorf("list workflow task runs: %w", err)
	}
	taskIDs := make(map[string]struct{}, len(taskRuns))
	sessionIDs := make(map[string]struct{}, len(taskRuns))
	for _, taskRun := range taskRuns {
		if taskRun == nil {
			continue
		}
		if strings.TrimSpace(taskRun.WorkItemID) != "" {
			taskIDs[taskRun.WorkItemID] = struct{}{}
		}
		if strings.TrimSpace(taskRun.SessionID) != "" {
			sessionIDs[taskRun.SessionID] = struct{}{}
		}
	}
	return taskIDs, sessionIDs, nil
}

func appendUniqueArtifact(out *[]*domain.Artifact, seen map[string]struct{}, artifact *domain.Artifact) {
	if _, ok := seen[artifact.ArtifactID]; ok {
		return
	}
	seen[artifact.ArtifactID] = struct{}{}
	*out = append(*out, artifact)
}

func appendUniqueSession(out *[]*domain.AgentSession, seen map[string]struct{}, session *domain.AgentSession) {
	if _, ok := seen[session.SessionID]; ok {
		return
	}
	seen[session.SessionID] = struct{}{}
	*out = append(*out, session)
}

func appendUniqueOperation(out *[]*domain.AgentSessionOperation, seen map[string]struct{}, operation *domain.AgentSessionOperation) {
	if _, ok := seen[operation.OperationID]; ok {
		return
	}
	seen[operation.OperationID] = struct{}{}
	*out = append(*out, operation)
}

func appendUniqueToolCall(out *[]*domain.AgentSessionToolCall, seen map[string]struct{}, call *domain.AgentSessionToolCall) {
	if _, ok := seen[call.CallID]; ok {
		return
	}
	seen[call.CallID] = struct{}{}
	*out = append(*out, call)
}

func runWorkflowRouteBind(_ *cobra.Command, args []string) error {
	workflowName := strings.TrimSpace(args[0])
	routePath := workflowRoutePath(args[1])
	auth := strings.TrimSpace(workflowRouteAuth)
	if auth == "" {
		auth = "workspace"
	}
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if _, err := h.Store.WorkflowDefinitions().Get(ctx, ws, workflowName); err != nil {
			return fmt.Errorf("get workflow definition: %w", err)
		}
		if err := defspkg.ValidateWorkflowRouteBindingCollision(ctx, h.Store, ws, defspkg.WorkflowModule{
			Name:      workflowName,
			RoutePath: routePath,
		}); err != nil {
			return err
		}
		binding, err := h.Store.RouteBindings().Upsert(ctx, store.RouteBindingUpsert{
			WorkspaceKey:   ws,
			BindingID:      defspkg.RouteBindingID(workflowName, "POST", routePath),
			DefinitionName: workflowName,
			DefinitionType: domain.DefinitionTypeWorkflow,
			Path:           routePath,
			Method:         "POST",
			AuthPolicy:     auth,
			Status:         domain.DefinitionStatusActive,
		})
		if err != nil {
			return fmt.Errorf("upsert route binding: %w", err)
		}
		if workflowRouteJSON {
			return workflowWriteJSON(binding)
		}
		fmt.Printf("Bound workflow %s route POST %s auth=%s\n", workflowName, routePath, auth)
		return nil
	})
}

func runWorkflowRouteList(_ *cobra.Command, args []string) error {
	workflowName := ""
	if len(args) > 0 {
		workflowName = strings.TrimSpace(args[0])
	}
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		routes, err := h.Store.RouteBindings().List(ctx, ws, store.RouteBindingFilter{
			DefinitionName: workflowName,
			Status:         domain.DefinitionStatusActive,
			Limit:          10000,
		})
		if err != nil {
			return fmt.Errorf("list route bindings: %w", err)
		}
		if workflowRouteJSON {
			return workflowWriteJSON(routes)
		}
		if len(routes) == 0 {
			fmt.Println("No active workflow route bindings")
			return nil
		}
		for _, route := range routes {
			fmt.Printf("%-28s %-6s %-36s auth=%s\n", route.DefinitionName, route.Method, route.Path, route.AuthPolicy)
		}
		return nil
	})
}

func runWorkflowRouteRemove(_ *cobra.Command, args []string) error {
	workflowName := strings.TrimSpace(args[0])
	routePath := workflowRoutePath(args[1])
	bindingID := defspkg.RouteBindingID(workflowName, "POST", routePath)
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		existing, err := h.Store.RouteBindings().Get(ctx, ws, bindingID)
		if err != nil {
			return fmt.Errorf("get route binding: %w", err)
		}
		binding, err := h.Store.RouteBindings().Upsert(ctx, store.RouteBindingUpsert{
			WorkspaceKey:   ws,
			BindingID:      existing.BindingID,
			DefinitionName: existing.DefinitionName,
			DefinitionType: existing.DefinitionType,
			Path:           existing.Path,
			Method:         existing.Method,
			AuthPolicy:     existing.AuthPolicy,
			Status:         domain.DefinitionStatusDisabled,
		})
		if err != nil {
			return fmt.Errorf("disable route binding: %w", err)
		}
		if workflowRouteJSON {
			return workflowWriteJSON(binding)
		}
		fmt.Printf("Disabled workflow %s route POST %s\n", workflowName, routePath)
		return nil
	})
}

func runWorkflowTriggerBind(_ *cobra.Command, args []string) error {
	workflowName := strings.TrimSpace(args[0])
	eventType := strings.TrimSpace(args[1])
	filter, rawFilter, err := parseWorkflowTriggerFilter(workflowTriggerFilter)
	if err != nil {
		return err
	}
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if _, err := h.Store.WorkflowDefinitions().Get(ctx, ws, workflowName); err != nil {
			return fmt.Errorf("get workflow definition: %w", err)
		}
		if err := defspkg.ValidateWorkflowTriggerBindingCollision(ctx, h.Store, ws, defspkg.WorkflowModule{
			Name:          workflowName,
			TriggerEvent:  eventType,
			TriggerFilter: filter,
		}); err != nil {
			return err
		}
		binding, err := h.Store.TriggerBindings().Upsert(ctx, store.TriggerBindingUpsert{
			WorkspaceKey: ws,
			BindingID:    "workflow:" + workflowName + ":" + eventType,
			WorkflowName: workflowName,
			EventType:    eventType,
			Filter:       rawFilter,
			Status:       domain.DefinitionStatusActive,
		})
		if err != nil {
			return fmt.Errorf("upsert trigger binding: %w", err)
		}
		if workflowTriggerJSON {
			return workflowWriteJSON(binding)
		}
		fmt.Printf("Bound workflow %s trigger %s\n", workflowName, eventType)
		return nil
	})
}

func runWorkflowTriggerList(_ *cobra.Command, args []string) error {
	workflowName := ""
	if len(args) > 0 {
		workflowName = strings.TrimSpace(args[0])
	}
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		triggers, err := h.Store.TriggerBindings().List(ctx, ws, store.TriggerBindingFilter{
			WorkflowName: workflowName,
			Status:       domain.DefinitionStatusActive,
			Limit:        10000,
		})
		if err != nil {
			return fmt.Errorf("list trigger bindings: %w", err)
		}
		if workflowTriggerJSON {
			return workflowWriteJSON(triggers)
		}
		if len(triggers) == 0 {
			fmt.Println("No active workflow trigger bindings")
			return nil
		}
		for _, trigger := range triggers {
			fmt.Printf("%-28s %-36s filter=%s\n", trigger.WorkflowName, trigger.EventType, string(trigger.Filter))
		}
		return nil
	})
}

func runWorkflowTriggerRemove(_ *cobra.Command, args []string) error {
	workflowName := strings.TrimSpace(args[0])
	eventType := strings.TrimSpace(args[1])
	bindingID := "workflow:" + workflowName + ":" + eventType
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		existing, err := h.Store.TriggerBindings().Get(ctx, ws, bindingID)
		if err != nil {
			return fmt.Errorf("get trigger binding: %w", err)
		}
		binding, err := h.Store.TriggerBindings().Upsert(ctx, store.TriggerBindingUpsert{
			WorkspaceKey: ws,
			BindingID:    existing.BindingID,
			WorkflowName: existing.WorkflowName,
			EventType:    existing.EventType,
			Filter:       existing.Filter,
			Status:       domain.DefinitionStatusDisabled,
		})
		if err != nil {
			return fmt.Errorf("disable trigger binding: %w", err)
		}
		if workflowTriggerJSON {
			return workflowWriteJSON(binding)
		}
		fmt.Printf("Disabled workflow %s trigger %s\n", workflowName, eventType)
		return nil
	})
}

func workflowRoutePath(routePath string) string {
	routePath = "/" + strings.Trim(strings.TrimSpace(routePath), "/")
	if routePath == "/" {
		return "/"
	}
	return routePath
}

func parseWorkflowTriggerFilter(raw string) (map[string]string, json.RawMessage, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "{}"
	}
	var generic map[string]any
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return nil, nil, fmt.Errorf("--filter must be a JSON object: %w", err)
	}
	filter := make(map[string]string, len(generic))
	for key, value := range generic {
		key = strings.TrimSpace(key)
		if key == "" || value == nil {
			continue
		}
		filter[key] = fmt.Sprint(value)
	}
	encoded, err := json.Marshal(filter)
	if err != nil {
		return nil, nil, err
	}
	return filter, json.RawMessage(encoded), nil
}

func runWorkflowCancel(_ *cobra.Command, args []string) error {
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		run, err := workflowpkg.CancelRun(ctx, h.Store, ws, args[0], nil)
		if err != nil {
			return err
		}
		if workflowCancelJSON {
			return workflowWriteJSON(run)
		}
		fmt.Printf("Cancelled workflow run %s\n", run.RunID)
		return nil
	})
}

func parseInput(s string) (json.RawMessage, error) {
	return parseInputFlag(s, "--input")
}

func parseWorkflowPayload(input, payload string) (json.RawMessage, error) {
	if strings.TrimSpace(payload) == "" {
		return parseInput(input)
	}
	if trimmedInput := strings.TrimSpace(input); trimmedInput != "" && trimmedInput != "{}" {
		return nil, errors.New("--input and --payload cannot both be set")
	}
	return parseInputFlag(payload, "--payload")
}

func parseInputFlag(s, flagName string) (json.RawMessage, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "{}"
	}
	var tmp any
	if err := json.Unmarshal([]byte(s), &tmp); err != nil {
		return nil, fmt.Errorf("%s must be valid JSON: %w", flagName, err)
	}
	return json.RawMessage(s), nil
}

func waitWorkflow(ctx context.Context, st store.Store, ws, runID string) (*domain.WorkflowRun, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		run, err := st.WorkflowRuns().Get(ctx, ws, runID)
		if err != nil {
			return nil, err
		}
		if !domain.WorkflowRunStatusLive(run.Status) {
			return run, nil
		}
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-ticker.C:
		}
	}
}

func actorName() string {
	if actor := strings.TrimSpace(os.Getenv("LOOM_ACTOR")); actor != "" {
		return actor
	}
	if actor := strings.TrimSpace(os.Getenv("USER")); actor != "" {
		return actor
	}
	return "loom"
}
