package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

//go:embed ts_context_runner.js
var tsContextRunnerSource string

func runTypeScriptContextOnce(ctx context.Context, st store.Store, ib backend.IssueBackend, run *domain.WorkflowRun, def *domain.WorkflowDefinition) (*BuiltinRunResult, error) {
	if st == nil || st.TaskRuns() == nil || st.RunEvents() == nil || st.WorkflowRuns() == nil {
		return nil, fmt.Errorf("workflow stores not configured")
	}
	if def == nil {
		return nil, fmt.Errorf("workflow definition required")
	}
	sourcePath := strings.TrimSpace(def.SourceRef)
	if sourcePath == "" {
		return nil, fmt.Errorf("workflow %q has no TypeScript source_ref", run.WorkflowName)
	}
	request, parentID, err := buildTSContextRequest(ctx, st, ib, run, def, sourcePath)
	if err != nil {
		return nil, err
	}
	response, err := executeTSContext(ctx, request)
	if err != nil {
		return nil, err
	}
	for _, log := range response.Logs {
		appendTSLogEvent(ctx, st, run, log)
	}
	applied, err := applyTSOperations(ctx, st, ib, run, parentID, response.Operations)
	if err != nil {
		return nil, err
	}
	if applied.Cancelled {
		return tsContextCancelledResult(ctx, st, run, applied.Run, request), nil
	}
	taskRuns, dispatched, err := dispatchTSContextTaskRuns(ctx, st, run, parentID, applied.TaskRuns)
	if err != nil {
		return nil, err
	}
	result := &BuiltinRunResult{
		Run:             run,
		TaskRuns:        taskRuns,
		ReadyCount:      len(request.ReadyChildren),
		OpenCount:       countOpen(request.ChildWorkItems),
		BlockedCount:    len(request.BlockedChildren),
		DispatchedCount: dispatched,
	}
	result.Done = len(taskRuns) == 0 && result.OpenCount == 0
	return finishTSContextRun(ctx, st, run, response.Result, result, parentID, applied.WaitCondition)
}

func tsContextCancelledResult(ctx context.Context, st store.Store, original, cancelled *domain.WorkflowRun, request tsContextRequest) *BuiltinRunResult {
	if cancelled == nil {
		cancelled, _ = st.WorkflowRuns().Get(ctx, original.WorkspaceKey, original.RunID)
	}
	return &BuiltinRunResult{
		Run:          cancelled,
		Done:         true,
		ReadyCount:   len(request.ReadyChildren),
		OpenCount:    countOpen(request.ChildWorkItems),
		BlockedCount: len(request.BlockedChildren),
	}
}

func dispatchTSContextTaskRuns(ctx context.Context, st store.Store, run *domain.WorkflowRun, parentID string, taskRuns []*domain.TaskRun) ([]*domain.TaskRun, int, error) {
	dispatched := 0
	for i, taskRun := range taskRuns {
		input := ParentWorkItemsInput{ParentID: parentID, Role: taskRun.RoleName}
		if input.Role == "" {
			input.Role = "task"
		}
		updated, didDispatch, err := dispatchTaskRun(ctx, st, run, input, taskRun)
		if err != nil {
			return nil, dispatched, err
		}
		taskRuns[i] = updated
		if didDispatch {
			dispatched++
		}
	}
	return taskRuns, dispatched, nil
}

func finishTSContextRun(ctx context.Context, st store.Store, run *domain.WorkflowRun, runResult json.RawMessage, result *BuiltinRunResult, parentID, explicitWaitCondition string) (*BuiltinRunResult, error) {
	if explicitWaitCondition != "" || len(result.TaskRuns) > 0 || result.OpenCount > 0 {
		status := domain.WorkflowRunWaiting
		wait := explicitWaitCondition
		if wait == "" {
			wait = "workflow_context_task_runs(workflow_run:" + run.RunID + ")"
			if parentID != "" {
				wait = "work_item_changed(parent:" + parentID + ") OR task_run_terminal(workflow_run:" + run.RunID + ")"
			}
		}
		updated, err := st.WorkflowRuns().Update(ctx, run.WorkspaceKey, run.RunID, store.WorkflowRunUpdate{
			Status:        &status,
			WaitCondition: &wait,
		})
		if err != nil {
			return nil, fmt.Errorf("wait workflow run: %w", err)
		}
		result.Run = updated
		_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
			WorkspaceKey:  run.WorkspaceKey,
			WorkflowRunID: run.RunID,
			Type:          "workflow_waiting",
			Message:       "TypeScript WorkflowContext run is waiting for child work",
			Data:          mustJSON(map[string]any{"ensured": len(result.TaskRuns), "open": result.OpenCount, "blocked": result.BlockedCount, "wait_condition": wait}),
		})
		return result, nil
	}
	now := time.Now().UTC()
	finishedAt := &now
	status := domain.WorkflowRunCompleted
	if len(runResult) == 0 {
		runResult = json.RawMessage(`null`)
	}
	updated, err := st.WorkflowRuns().Update(ctx, run.WorkspaceKey, run.RunID, store.WorkflowRunUpdate{
		Status:     &status,
		Result:     &runResult,
		FinishedAt: &finishedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("complete workflow run: %w", err)
	}
	result.Run = updated
	result.Done = true
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "workflow_completed",
		Message:       "TypeScript WorkflowContext run completed",
		Data:          runResult,
	})
	return result, nil
}

func buildTSContextRequest(ctx context.Context, st store.Store, ib backend.IssueBackend, run *domain.WorkflowRun, def *domain.WorkflowDefinition, sourcePath string) (tsContextRequest, string, error) {
	workflowState, taskRuns, taskClaims, err := tsContextControlState(ctx, st, run)
	if err != nil {
		return tsContextRequest{}, "", err
	}
	inputMap := map[string]any{}
	if len(run.Input) > 0 {
		if err := json.Unmarshal(run.Input, &inputMap); err != nil {
			return tsContextRequest{}, "", fmt.Errorf("decode workflow input: %w", err)
		}
	}
	parentID := firstString(inputMap, "parentId", "parent_id")
	runtimeProfile := tsContextRuntimeProfileForDefinition(ctx, st, def)
	request := tsContextRequest{
		ID:             run.RunID,
		SourcePath:     sourcePath,
		Input:          inputMap,
		Env:            workflowEnvBindings(def),
		Workspace:      tsContextWorkspaceForRun(ctx, st, run, def, runtimeProfile),
		Workflow:       workflowState,
		RuntimeProfile: runtimeProfile,
		RuntimeRoot:    tsContextRuntimeWorkspaceRoot(runtimeProfile),
		TaskRuns:       taskRuns,
		TaskClaims:     taskClaims,
		Request: tsContextRequestMetadata{
			WorkspaceKey:    run.WorkspaceKey,
			WorkflowName:    run.WorkflowName,
			WorkflowVersion: run.WorkflowVersion,
			Actor:           run.LeaseOwner,
		},
	}
	if ib == nil || parentID == "" {
		return request, parentID, nil
	}
	ready, err := ib.Ready(ctx, backend.ReadyOpts{ParentID: parentID, Limit: 256})
	if err != nil {
		return request, parentID, fmt.Errorf("ready child query: %w", err)
	}
	blocked, err := ib.Blocked(ctx, backend.BlockedOpts{ParentID: parentID, Limit: 256})
	if err != nil {
		return request, parentID, fmt.Errorf("blocked child query: %w", err)
	}
	all, err := ib.List(ctx, backend.ListOpts{ParentID: parentID, Limit: 10000})
	if err != nil {
		return request, parentID, fmt.Errorf("list child work: %w", err)
	}
	request.ReadyChildren = ready
	request.BlockedChildren = blocked
	request.ChildWorkItems = all
	return request, parentID, nil
}

func tsContextControlState(ctx context.Context, st store.Store, run *domain.WorkflowRun) (tsContextWorkflowState, []*domain.TaskRun, []tsContextTaskClaim, error) {
	currentRun := run
	if loaded, err := st.WorkflowRuns().Get(ctx, run.WorkspaceKey, run.RunID); err == nil {
		currentRun = loaded
	}
	taskRuns, err := st.TaskRuns().List(ctx, run.WorkspaceKey, store.TaskRunFilter{WorkflowRunID: run.RunID, Limit: 10000})
	if err != nil {
		return tsContextWorkflowState{}, nil, nil, fmt.Errorf("list workflow task runs: %w", err)
	}
	return tsContextWorkflowState{
		Status:          string(currentRun.Status),
		WaitCondition:   currentRun.WaitCondition,
		CancelRequested: currentRun.Status == domain.WorkflowRunCancelled,
	}, taskRuns, taskClaimProjections(taskRuns), nil
}

func taskClaimProjections(taskRuns []*domain.TaskRun) []tsContextTaskClaim {
	out := make([]tsContextTaskClaim, 0, len(taskRuns))
	for _, taskRun := range taskRuns {
		if taskRun == nil || (taskRun.ClaimActor == "" && taskRun.AgentID == "" && taskRun.SessionID == "") {
			continue
		}
		out = append(out, tsContextTaskClaim{
			TaskRunID:    taskRun.TaskRunID,
			WorkItemID:   taskRun.WorkItemID,
			ClaimActor:   taskRun.ClaimActor,
			ClaimEventID: taskRun.ClaimEventID,
			Status:       string(taskRun.Status),
			AgentID:      taskRun.AgentID,
			SessionID:    taskRun.SessionID,
			Active:       domain.TaskRunStatusLive(taskRun.Status),
		})
	}
	return out
}

func workflowEnvBindings(def *domain.WorkflowDefinition) map[string]string {
	out := map[string]string{}
	if def == nil || len(def.Manifest) == 0 {
		return out
	}
	var manifest struct {
		Env []string `json:"env"`
	}
	if err := json.Unmarshal(def.Manifest, &manifest); err != nil {
		return out
	}
	for _, name := range manifest.Env {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if value, ok := os.LookupEnv(name); ok {
			out[name] = value
		}
	}
	return out
}

func executeTSContext(ctx context.Context, request tsContextRequest) (tsContextResponse, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return tsContextResponse{}, err
	}
	cmd := exec.CommandContext(ctx, "node", "--no-warnings", "-e", tsContextRunnerSource) //nolint:gosec // Runner source is embedded and request is passed over stdin.
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return tsContextResponse{}, fmt.Errorf("execute TypeScript WorkflowContext runner: %w\n%s", err, stderr.String())
	}
	var response tsContextResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return tsContextResponse{}, fmt.Errorf("decode TypeScript WorkflowContext response: %w\n%s", err, stdout.String())
	}
	return response, nil
}

func appendTSLogEvent(ctx context.Context, st store.Store, run *domain.WorkflowRun, log tsWorkflowLog) {
	level := strings.TrimSpace(log.Level)
	if level == "" {
		level = "info"
	}
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "workflow_log",
		Message:       log.Message,
		Data:          mustJSON(map[string]any{"level": level, "attributes": log.Attributes}),
	})
}

func applyTSOperations(ctx context.Context, st store.Store, ib backend.IssueBackend, run *domain.WorkflowRun, parentID string, operations []tsWorkflowOperation) (tsAppliedOperations, error) {
	applied := tsAppliedOperations{TaskRuns: make([]*domain.TaskRun, 0)}
	for _, op := range operations {
		if err := applyTSOperation(ctx, st, ib, run, parentID, &applied, op); err != nil {
			return tsAppliedOperations{}, err
		}
	}
	return applied, nil
}

//nolint:cyclop,funlen // This switch is the explicit WorkflowContext operation admission allowlist.
func applyTSOperation(ctx context.Context, st store.Store, ib backend.IssueBackend, run *domain.WorkflowRun, parentID string, applied *tsAppliedOperations, op tsWorkflowOperation) error {
	switch op.Type {
	case "runtime.profile", "runtime.workspace", "runtime.skills":
		appendRuntimeReadEvent(ctx, st, run, op)
	case "runtime.workspace.materialize":
		return appendRuntimeWorkspaceLifecycleEvent(ctx, st, run, op.Params, "runtime_workspace_materialize_requested", "workflow runtime workspace materialization requested")
	case "runtime.workspace.cleanup":
		return appendRuntimeWorkspaceLifecycleEvent(ctx, st, run, op.Params, "runtime_workspace_cleanup_requested", "workflow runtime workspace cleanup requested")
	case "runtime.workspace.files.write":
		return applyRuntimeWorkspaceFileOperation(ctx, st, run, op.Params, "runtime_workspace_file_written", "workflow runtime workspace file written")
	case "runtime.workspace.files.read":
		return applyRuntimeWorkspaceFileOperation(ctx, st, run, op.Params, "runtime_workspace_file_read", "workflow runtime workspace file read")
	case "workItems.get":
		appendWorkItemReadEvent(ctx, st, run, op.Params)
	case "workItems.comment":
		return applyWorkItemCommentOperation(ctx, st, ib, run, op.Params)
	case "taskRuns.ensure":
		taskRun, err := applyEnsureTaskRunOperation(ctx, st, ib, run, parentID, op.Params)
		if err != nil {
			return err
		}
		applied.TaskRuns = append(applied.TaskRuns, taskRun)
	case "taskRuns.wait":
		setAppliedWaitCondition(applied, applyTaskRunsWaitOperation(ctx, st, run, op.Params))
	case "taskClaims.wait":
		setAppliedWaitCondition(applied, applyTaskClaimsWaitOperation(ctx, st, run, op.Params))
	case "workflow.waitUntil":
		waitCondition, err := applyWorkflowWaitUntilOperation(ctx, st, run, op.Params)
		if err != nil {
			return err
		}
		setAppliedWaitCondition(applied, waitCondition)
	case "workflow.cancel":
		cancelled, err := applyWorkflowCancelOperation(ctx, st, run, op.Params)
		if err != nil {
			return err
		}
		applied.Cancelled = true
		applied.Run = cancelled
	case "tools.call":
		appendToolCallEvent(ctx, st, run, op.Params)
	case "artifacts.record":
		return applyRecordArtifactOperation(ctx, st, run, op.Params)
	case "files.write":
		return applyStageFileOperation(ctx, st, run, op.Params)
	case "files.read":
		appendFileReadEvent(ctx, st, run, op.Params)
	case "shell.run":
		return appendControllerShellRunEvent(ctx, st, run, op.Params)
	case "agents.session":
		return applyInitializeAgentSessionOperation(ctx, st, run, op.Params)
	case "agents.session.operation":
		return appendAgentSessionOperationEvent(ctx, st, run, op.Params)
	case "agents.dispatch":
		return appendAgentDispatchAdmittedEvent(ctx, st, run, op.Params)
	default:
		return fmt.Errorf("unsupported WorkflowContext operation %q", op.Type)
	}
	return nil
}

func setAppliedWaitCondition(applied *tsAppliedOperations, waitCondition string) {
	if applied.WaitCondition == "" {
		applied.WaitCondition = waitCondition
	}
}

func appendToolCallEvent(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) {
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "tool_call",
		Message:       "typed workflow tool executed",
		Data:          mustJSON(params),
	})
}

func applyTaskRunsWaitOperation(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) string {
	liveCount := firstInt64(params, "liveCount", "live_count")
	data := copyAnyMap(params)
	data["workflow_run_id"] = run.RunID
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "task_runs_observed",
		Message:       "workflow task runs observed from TypeScript WorkflowContext",
		Data:          mustJSON(data),
	})
	if liveCount <= 0 && !firstBool(params, "wait") {
		return ""
	}
	condition := firstString(params, "condition")
	if condition == "" {
		condition = "task_run_terminal(workflow_run:" + run.RunID + ")"
	}
	return condition
}

func applyTaskClaimsWaitOperation(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) string {
	activeCount := firstInt64(params, "activeCount", "active_count")
	data := copyAnyMap(params)
	data["workflow_run_id"] = run.RunID
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "task_claims_observed",
		Message:       "workflow task claims observed from TypeScript WorkflowContext",
		Data:          mustJSON(data),
	})
	if activeCount <= 0 && !firstBool(params, "wait") {
		return ""
	}
	condition := firstString(params, "condition")
	if condition == "" {
		condition = "task_claim_changed(workflow_run:" + run.RunID + ")"
	}
	return condition
}

func applyWorkflowWaitUntilOperation(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) (string, error) {
	condition := firstString(params, "condition", "waitCondition", "wait_condition")
	if condition == "" {
		return "", fmt.Errorf("workflow.waitUntil requires condition")
	}
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "workflow_wait_requested",
		Message:       "TypeScript WorkflowContext requested workflow wait",
		Data:          mustJSON(params),
	})
	return condition, nil
}

func applyWorkflowCancelOperation(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) (*domain.WorkflowRun, error) {
	now := time.Now().UTC()
	finishedAt := &now
	status := domain.WorkflowRunCancelled
	updated, err := st.WorkflowRuns().Update(ctx, run.WorkspaceKey, run.RunID, store.WorkflowRunUpdate{
		Status:     &status,
		FinishedAt: &finishedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("cancel workflow run: %w", err)
	}
	data := copyAnyMap(params)
	data["workflow_run_id"] = run.RunID
	data["source"] = "workflow_context"
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "workflow_cancelled",
		Message:       "TypeScript WorkflowContext cancelled workflow run",
		Data:          mustJSON(data),
	})
	return updated, nil
}

func appendAgentDispatchAdmittedEvent(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) error {
	agentID := firstString(params, "agentId", "agent_id", "agent", "name")
	if agentID == "" {
		return fmt.Errorf("agents.dispatch requires agentId")
	}
	data := agentDispatchAdmittedEventData(run, params, agentID)
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "agent_dispatch_admitted",
		Message:       "workflow agent dispatch admitted",
		Data:          mustJSON(data),
	})
	return nil
}

func agentDispatchAdmittedEventData(run *domain.WorkflowRun, params map[string]any, agentID string) map[string]any {
	dispatchID := firstString(params, "dispatchId", "dispatch_id")
	if dispatchID == "" {
		dispatchID = "dispatch:" + run.RunID + ":" + agentID
	}
	operationID := firstString(params, "operationId", "operation_id")
	if operationID == "" {
		operationID = "op:" + dispatchID
	}
	status := firstString(params, "status")
	if status == "" {
		status = "admitted"
	}
	taskID := firstString(params, "taskId", "task_id", "workItemId", "work_item_id")
	taskRunID := firstString(params, "taskRunId", "task_run_id")
	sessionID := firstString(params, "sessionId", "session_id")
	data := copyAnyMap(params)
	data["agent_id"] = agentID
	data["dispatch_id"] = dispatchID
	data["dispatchId"] = dispatchID
	data["operation_id"] = operationID
	data["operationId"] = operationID
	data["status"] = status
	data["workflow_run_id"] = run.RunID
	data["source"] = "workflow_context"
	if taskID != "" {
		data["task_id"] = taskID
		data["work_item_id"] = taskID
	}
	if taskRunID != "" {
		data["task_run_id"] = taskRunID
	}
	if sessionID != "" {
		data["session_id"] = sessionID
	}
	if _, ok := data["correlation"]; !ok {
		data["correlation"] = map[string]any{
			"workflowRunId": run.RunID,
			"agentId":       agentID,
			"dispatchId":    dispatchID,
			"operationId":   operationID,
			"taskRunId":     taskRunID,
			"workItemId":    taskID,
			"sessionId":     sessionID,
		}
	}
	return data
}

func applyRecordArtifactOperation(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) error {
	artifact, err := createWorkflowArtifact(ctx, st, run, params, "artifacts.record requires uri")
	if err != nil {
		return err
	}
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "artifact_recorded",
		Message:       "workflow artifact recorded",
		Data: mustJSON(map[string]string{
			"artifact_id": artifact.ArtifactID,
			"type":        artifact.Type,
			"uri":         artifact.URI,
			"task_id":     artifact.TaskID,
		}),
	})
	return nil
}

func applyStageFileOperation(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) error {
	artifact, err := createWorkflowArtifact(ctx, st, run, params, "files.write requires uri")
	if err != nil {
		return err
	}
	data := copyAnyMap(params)
	data["artifact_id"] = artifact.ArtifactID
	data["workflow_run_id"] = run.RunID
	data["visibility"] = "controller"
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "workflow_file_staged",
		Message:       "workflow controller staged file",
		Data:          mustJSON(data),
	})
	return nil
}

func appendFileReadEvent(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) {
	data := copyAnyMap(params)
	data["workflow_run_id"] = run.RunID
	data["visibility"] = "controller"
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "workflow_file_read",
		Message:       "workflow controller read staged file",
		Data:          mustJSON(data),
	})
}

func appendControllerShellRunEvent(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) error {
	command := firstString(params, "command")
	if command == "" {
		return fmt.Errorf("shell.run requires command")
	}
	data := copyAnyMap(params)
	data["command"] = command
	data["workflow_run_id"] = run.RunID
	data["visibility"] = "controller"
	if _, ok := data["status"]; !ok {
		data["status"] = "completed"
	}
	message := "workflow controller shell run admitted"
	if firstString(data, "status") == "completed" {
		message = "workflow controller shell run completed"
	}
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "workflow_shell_run",
		Message:       message,
		Data:          mustJSON(data),
	})
	return nil
}

func createWorkflowArtifact(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any, missingURI string) (*domain.Artifact, error) {
	if st.Artifacts() == nil {
		return nil, fmt.Errorf("artifact store not configured")
	}
	uri := firstString(params, "uri")
	if uri == "" {
		return nil, errors.New(missingURI)
	}
	artifactType := firstString(params, "type", "kind")
	if artifactType == "" {
		artifactType = "workflow"
	}
	artifactID := firstString(params, "artifactId", "artifact_id", "id")
	if artifactID == "" {
		artifactID = generatedArtifactID(run.RunID, artifactType, uri)
	}
	taskID := firstString(params, "taskId", "task_id", "workItemId", "work_item_id")
	metadata := stringMap(params["metadata"])
	metadata["workflow_run_id"] = run.RunID
	metadata["workflow_name"] = run.WorkflowName
	if run.WorkflowVersion != "" {
		metadata["workflow_version"] = run.WorkflowVersion
	}
	artifact, err := st.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey: run.WorkspaceKey,
		ArtifactID:   artifactID,
		TaskID:       taskID,
		Type:         artifactType,
		URI:          uri,
		Summary:      firstString(params, "summary"),
		MIMEType:     firstString(params, "mimeType", "mime_type"),
		SizeBytes:    firstInt64(params, "sizeBytes", "size_bytes"),
		Checksum:     firstString(params, "checksum"),
		Metadata:     metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("record workflow artifact %s: %w", artifactID, err)
	}
	return artifact, nil
}

func applyInitializeAgentSessionOperation(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) error {
	if st.AgentSessions() == nil {
		return fmt.Errorf("agent session store not configured")
	}
	create, err := agentSessionCreateForWorkflow(run, params)
	if err != nil {
		return err
	}
	session, err := st.AgentSessions().Create(ctx, create)
	if err != nil {
		if !errors.Is(err, domain.ErrAlreadyExists) {
			return fmt.Errorf("initialize workflow agent session %s: %w", create.SessionID, err)
		}
		session, err = st.AgentSessions().Get(ctx, run.WorkspaceKey, create.SessionID)
		if err != nil {
			return fmt.Errorf("load existing workflow agent session %s: %w", create.SessionID, err)
		}
	}
	appendAgentSessionInitializedEvent(ctx, st, run, session)
	return nil
}

func agentSessionCreateForWorkflow(run *domain.WorkflowRun, params map[string]any) (store.AgentSessionCreate, error) {
	agentID := firstString(params, "agentId", "agent_id", "agent", "name")
	if agentID == "" {
		return store.AgentSessionCreate{}, fmt.Errorf("agents.session requires agentId")
	}
	kind, err := agentSessionKind(firstString(params, "kind"))
	if err != nil {
		return store.AgentSessionCreate{}, err
	}
	status, err := agentSessionStatus(firstString(params, "status"))
	if err != nil {
		return store.AgentSessionCreate{}, err
	}
	taskID := firstString(params, "taskId", "task_id", "workItemId", "work_item_id")
	harnessName := firstString(params, "harness", "harnessName", "harness_name")
	sessionName := firstString(params, "sessionName", "session_name")
	sessionID := firstString(params, "sessionId", "session_id", "id")
	if sessionID == "" {
		sessionID = generatedSessionID(run.RunID, agentID, harnessName, sessionName, taskID)
	}
	phase := firstString(params, "phase")
	if phase == "" {
		phase = "initialized"
	}
	return store.AgentSessionCreate{
		WorkspaceKey:    run.WorkspaceKey,
		SessionID:       sessionID,
		AgentID:         agentID,
		NodeID:          firstString(params, "nodeId", "node_id"),
		Kind:            kind,
		TaskID:          taskID,
		TerminalID:      firstString(params, "terminalId", "terminal_id"),
		ParentSessionID: firstString(params, "parentSessionId", "parent_session_id"),
		Status:          status,
		Phase:           phase,
		Attempt:         int(firstInt64(params, "attempt")),
		Metadata:        agentSessionMetadataForWorkflow(run, params, harnessName, sessionName),
	}, nil
}

func agentSessionMetadataForWorkflow(run *domain.WorkflowRun, params map[string]any, harnessName, sessionName string) map[string]string {
	metadata := stringMap(params["metadata"])
	metadata["workflow_run_id"] = run.RunID
	metadata["workflow_name"] = run.WorkflowName
	if run.WorkflowVersion != "" {
		metadata["workflow_version"] = run.WorkflowVersion
	}
	if harnessName != "" {
		metadata["harness"] = harnessName
	}
	if sessionName != "" {
		metadata["session_name"] = sessionName
	}
	if model := firstString(params, "model"); model != "" {
		metadata["model"] = model
	}
	if backendName := firstString(params, "backend"); backendName != "" {
		metadata["backend"] = backendName
	}
	if profileName := firstString(params, "profileName", "profile_name"); profileName != "" {
		metadata["profile_name"] = profileName
		if _, ok := metadata["source_agent_profile"]; !ok {
			metadata["source_agent_profile"] = profileName
		}
	}
	return metadata
}

func appendAgentSessionInitializedEvent(ctx context.Context, st store.Store, run *domain.WorkflowRun, session *domain.AgentSession) {
	data := map[string]string{
		"session_id": session.SessionID,
		"agent_id":   session.AgentID,
		"kind":       string(session.Kind),
		"task_id":    session.TaskID,
		"phase":      session.Phase,
	}
	if session.Metadata != nil {
		if profileName := session.Metadata["profile_name"]; profileName != "" {
			data["profile_name"] = profileName
		}
		if sourceProfile := session.Metadata["source_agent_profile"]; sourceProfile != "" {
			data["source_agent_profile"] = sourceProfile
		}
	}
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "agent_session_initialized",
		Message:       "workflow agent session initialized",
		Data:          mustJSON(data),
	})
}

func appendAgentSessionOperationEvent(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) error {
	operation := firstString(params, "operation", "type")
	if !validAgentSessionOperation(operation) {
		return fmt.Errorf("unsupported agents.session operation %q", operation)
	}
	agentID := firstString(params, "agentId", "agent_id", "agent", "name")
	if agentID == "" {
		return fmt.Errorf("agents.session.%s requires agentId", operation)
	}
	taskID := firstString(params, "taskId", "task_id", "workItemId", "work_item_id")
	harnessName := firstString(params, "harness", "harnessName", "harness_name")
	sessionName := firstString(params, "sessionName", "session_name")
	sessionID := firstString(params, "sessionId", "session_id", "id")
	if sessionID == "" {
		sessionID = generatedSessionID(run.RunID, agentID, harnessName, sessionName, taskID)
	}
	data := copyAnyMap(params)
	data["agent_id"] = agentID
	data["session_id"] = sessionID
	data["operation"] = operation
	data["visibility"] = agentSessionOperationVisibility(operation)
	operationID := firstString(data, "operationId", "operation_id")
	if operationID == "" {
		operationID = generatedAgentSessionOperationID(run.RunID, sessionID, operation, data)
	}
	data["operation_id"] = operationID
	if _, ok := data["status"]; !ok {
		data["status"] = "admitted"
	}
	if taskID != "" {
		data["task_id"] = taskID
	}
	message := "workflow agent session operation admitted"
	if firstString(data, "status") == "completed" {
		message = "workflow agent session operation completed"
	}
	upsertAgentSessionOperation(ctx, st, run, data)
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "agent_session_operation",
		Message:       message,
		Data:          mustJSON(data),
	})
	return appendAgentSessionToolCallEvents(ctx, st, run, data)
}

func upsertAgentSessionOperation(ctx context.Context, st store.Store, run *domain.WorkflowRun, data map[string]any) {
	if st == nil || st.AgentSessionOperations() == nil || run == nil {
		return
	}
	result, _ := rawMessageFromFirst(data, "result", "data")
	input, _ := rawMessageFromFirst(data, "input")
	usage, _ := rawMessageFromFirst(data, "usage")
	toolCalls, _ := rawMessageFromFirst(data, "toolCalls", "tool_calls")
	startedAt := firstTime(data, "startedAt", "started_at")
	completedAt := firstTimePtr(data, "completedAt", "completed_at")
	_, _ = st.AgentSessionOperations().Upsert(ctx, store.AgentSessionOperationUpsert{
		WorkspaceKey:      run.WorkspaceKey,
		OperationID:       firstString(data, "operation_id", "operationId"),
		SessionID:         firstString(data, "session_id", "sessionId"),
		AgentID:           firstString(data, "agent_id", "agentId"),
		WorkflowRunID:     run.RunID,
		TaskRunID:         firstString(data, "taskRunId", "task_run_id"),
		TaskID:            firstString(data, "task_id", "taskId", "workItemId", "work_item_id"),
		Kind:              firstString(data, "operation"),
		Status:            domain.AgentSessionOperationStatus(firstNonEmptyString(firstString(data, "status"), string(domain.AgentSessionOperationAdmitted))),
		Model:             firstString(data, "model"),
		Provider:          firstString(data, "provider"),
		ProviderModel:     firstString(data, "providerModel", "provider_model"),
		ProviderSessionID: firstString(data, "providerSessionId", "provider_session_id"),
		PromptHash:        firstString(data, "promptHash", "prompt_hash"),
		Text:              firstNonEmptyString(firstString(data, "text", "response", "message"), resultText(result)),
		Input:             input,
		Result:            result,
		Usage:             usage,
		ToolCalls:         toolCalls,
		ErrorClass:        firstString(data, "errorClass", "error_class"),
		ErrorMessage:      firstString(data, "errorMessage", "error_message"),
		StartedAt:         startedAt,
		CompletedAt:       completedAt,
		DurationMS:        firstInt64(data, "durationMs", "duration_ms"),
		Metadata:          stringMapFromFirst(data, "metadata"),
	})
}

func appendAgentSessionToolCallEvents(ctx context.Context, st store.Store, run *domain.WorkflowRun, operationData map[string]any) error {
	calls, found, err := firstMapSlice(operationData, "toolCalls", "tool_calls")
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	operationID := firstString(operationData, "operation_id", "operationId")
	for i, call := range calls {
		data := copyAnyMap(call)
		data["workflow_run_id"] = run.RunID
		data["agent_id"] = firstString(operationData, "agent_id", "agentId")
		data["session_id"] = firstString(operationData, "session_id", "sessionId")
		data["operation"] = firstString(operationData, "operation")
		if taskRunID := firstString(operationData, "taskRunId", "task_run_id"); taskRunID != "" {
			data["task_run_id"] = taskRunID
		}
		if taskID := firstString(operationData, "taskId", "task_id", "workItemId", "work_item_id"); taskID != "" {
			data["task_id"] = taskID
		}
		if operationID != "" {
			data["operation_id"] = operationID
		}
		callID := firstString(data, "call_id", "callId", "id")
		if callID == "" {
			callID = fmt.Sprintf("%s:tool:%d", firstNonEmptyString(operationID, "agent_session_operation"), i+1)
		}
		data["call_id"] = callID
		if name := firstString(data, "name", "toolName", "tool_name", "tool"); name != "" {
			data["name"] = name
			data["tool_name"] = name
		}
		status := firstString(data, "status")
		if status == "" {
			status = "completed"
			if _, ok := firstValue(data, "error", "failure"); ok {
				status = "failed"
			}
			data["status"] = status
		}
		if authorization := firstString(data, "authorization_status", "authorizationStatus"); authorization != "" {
			data["authorization_status"] = authorization
		} else {
			data["authorization_status"] = "authorized"
		}
		copyFirstValue(data, "idempotency_key", "idempotencyKey", "idempotency_key")
		copyFirstValue(data, "tool_version", "toolVersion", "tool_version", "version")
		copyFirstValue(data, "source_hash", "sourceHash", "source_hash")
		copyFirstValue(data, "provider_call_id", "providerCallId", "provider_call_id")
		copyFirstValue(data, "started_at", "startedAt", "started_at")
		copyFirstValue(data, "completed_at", "completedAt", "completed_at")
		copyFirstValue(data, "duration_ms", "durationMs", "duration_ms")
		copyFirstValue(data, "read_only", "readOnly", "read_only")
		copyFirstValue(data, "redacted", "redacted")
		copyFirstValue(data, "handler", "handler")
		copyFirstValue(data, "runtime", "runtime")
		copyFirstValue(data, "timeout", "timeout")
		copyFirstValue(data, "cancellable", "cancellable")
		upsertAgentSessionToolCall(ctx, st, run, data)
		_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
			WorkspaceKey:  run.WorkspaceKey,
			WorkflowRunID: run.RunID,
			Type:          "agent_session_tool_call",
			Message:       "workflow agent session tool call recorded",
			Data:          mustJSON(data),
		})
	}
	return nil
}

func upsertAgentSessionToolCall(ctx context.Context, st store.Store, run *domain.WorkflowRun, data map[string]any) {
	if st == nil || st.AgentSessionToolCalls() == nil || run == nil {
		return
	}
	args, _ := rawMessageFromFirst(data, "args", "arguments", "input")
	result, _ := rawMessageFromFirst(data, "result", "output")
	_, _ = st.AgentSessionToolCalls().Upsert(ctx, store.AgentSessionToolCallUpsert{
		WorkspaceKey:        run.WorkspaceKey,
		CallID:              firstString(data, "call_id", "callId", "id"),
		ProviderCallID:      firstString(data, "provider_call_id", "providerCallId"),
		OperationID:         firstString(data, "operation_id", "operationId"),
		SessionID:           firstString(data, "session_id", "sessionId"),
		AgentID:             firstString(data, "agent_id", "agentId"),
		WorkflowRunID:       run.RunID,
		TaskRunID:           firstString(data, "taskRunId", "task_run_id"),
		TaskID:              firstString(data, "task_id", "taskId", "workItemId", "work_item_id"),
		Name:                firstString(data, "name", "toolName", "tool_name", "tool"),
		Status:              firstNonEmptyString(firstString(data, "status"), "completed"),
		AuthorizationStatus: firstString(data, "authorization_status", "authorizationStatus"),
		IdempotencyKey:      firstString(data, "idempotency_key", "idempotencyKey"),
		ToolVersion:         firstString(data, "tool_version", "toolVersion", "version"),
		SourceHash:          firstString(data, "source_hash", "sourceHash"),
		Handler:             firstString(data, "handler"),
		Runtime:             firstString(data, "runtime"),
		Timeout:             firstString(data, "timeout"),
		Cancellable:         firstBool(data, "cancellable"),
		ReadOnly:            firstBool(data, "read_only", "readOnly"),
		Redacted:            firstBool(data, "redacted"),
		Args:                args,
		Result:              result,
		ErrorClass:          firstString(data, "error_class", "errorClass"),
		ErrorMessage:        firstString(data, "error_message", "errorMessage", "error"),
		StartedAt:           firstTime(data, "startedAt", "started_at"),
		CompletedAt:         firstTimePtr(data, "completedAt", "completed_at"),
		DurationMS:          firstInt64(data, "durationMs", "duration_ms"),
		Metadata:            stringMapFromFirst(data, "metadata"),
	})
}

func validAgentSessionOperation(operation string) bool {
	switch operation {
	case "prompt", "skill", "task", "shell", "compact":
		return true
	default:
		return false
	}
}

func agentSessionOperationVisibility(operation string) string {
	if operation == "compact" {
		return "controller"
	}
	return "model"
}

func applyEnsureTaskRunOperation(ctx context.Context, st store.Store, ib backend.IssueBackend, run *domain.WorkflowRun, parentID string, params map[string]any) (*domain.TaskRun, error) {
	workItemID := firstString(params, "workItemId", "work_item_id", "id")
	if workItemID == "" {
		return nil, fmt.Errorf("taskRuns.ensure requires workItemId")
	}
	role := firstString(params, "role", "roleName", "role_name")
	if role == "" {
		role = "task"
	}
	reason := firstString(params, "reason", "title")
	metadata := stringMap(params["metadata"])
	if parentID != "" {
		metadata["parent_id"] = parentID
	}
	metadata["workflow_name"] = run.WorkflowName
	if sourceRepo := firstString(params, "sourceRepo", "source_repo", "repo"); sourceRepo != "" {
		metadata["source_repo"] = sourceRepo
	}
	if metadata["source_repo"] == "" && ib != nil {
		if detail, err := ib.Get(ctx, workItemID); err == nil && detail != nil && strings.TrimSpace(detail.SourceRepo) != "" {
			metadata["source_repo"] = strings.TrimSpace(detail.SourceRepo)
		}
	}
	taskRun, err := st.TaskRuns().Ensure(ctx, store.TaskRunEnsure{
		WorkspaceKey:   run.WorkspaceKey,
		WorkflowRunID:  run.RunID,
		WorkItemID:     workItemID,
		RoleName:       role,
		IdempotencyKey: "ctx:" + workItemID + ":role:" + role,
		Reason:         reason,
		Metadata:       metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("ensure task run for %s: %w", workItemID, err)
	}
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		TaskRunID:     taskRun.TaskRunID,
		Type:          "task_run_ensured",
		Message:       "ensured child task run from TypeScript WorkflowContext",
		Data:          mustJSON(map[string]string{"work_item_id": workItemID, "role": role}),
	})
	return taskRun, nil
}

func generatedArtifactID(runID, artifactType, uri string) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + artifactType + "\x00" + uri))
	return "artifact:" + runID + ":" + hex.EncodeToString(sum[:])[:12]
}

func generatedSessionID(runID, agentID, harnessName, sessionName, taskID string) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + agentID + "\x00" + harnessName + "\x00" + sessionName + "\x00" + taskID))
	return "session:" + runID + ":" + hex.EncodeToString(sum[:])[:12]
}

func generatedAgentSessionOperationID(runID, sessionID, operation string, data map[string]any) string {
	seed := runID + "\x00" + sessionID + "\x00" + operation + "\x00" + string(mustJSON(data))
	sum := sha256.Sum256([]byte(seed))
	return "op:" + runID + ":" + hex.EncodeToString(sum[:])[:12]
}

func agentSessionKind(raw string) (domain.AgentSessionKind, error) {
	switch strings.TrimSpace(raw) {
	case "":
		return domain.AgentSessionKindTask, nil
	case string(domain.AgentSessionKindTask):
		return domain.AgentSessionKindTask, nil
	case string(domain.AgentSessionKindOrchestration):
		return domain.AgentSessionKindOrchestration, nil
	case string(domain.AgentSessionKindTerminal):
		return domain.AgentSessionKindTerminal, nil
	case string(domain.AgentSessionKindMaintenance):
		return domain.AgentSessionKindMaintenance, nil
	case string(domain.AgentSessionKindAdHoc), "ad-hoc", "adhoc":
		return domain.AgentSessionKindAdHoc, nil
	default:
		return "", fmt.Errorf("unsupported agents.session kind %q", raw)
	}
}

func agentSessionStatus(raw string) (domain.AgentSessionStatus, error) {
	switch strings.TrimSpace(raw) {
	case "":
		return domain.AgentSessionRunning, nil
	case string(domain.AgentSessionQueued):
		return domain.AgentSessionQueued, nil
	case string(domain.AgentSessionLeased):
		return domain.AgentSessionLeased, nil
	case string(domain.AgentSessionStarting):
		return domain.AgentSessionStarting, nil
	case string(domain.AgentSessionRunning):
		return domain.AgentSessionRunning, nil
	case string(domain.AgentSessionIdle):
		return domain.AgentSessionIdle, nil
	case string(domain.AgentSessionYielded):
		return domain.AgentSessionYielded, nil
	case string(domain.AgentSessionCompleted):
		return domain.AgentSessionCompleted, nil
	case string(domain.AgentSessionFailed):
		return domain.AgentSessionFailed, nil
	case string(domain.AgentSessionCancelled):
		return domain.AgentSessionCancelled, nil
	case string(domain.AgentSessionExpired):
		return domain.AgentSessionExpired, nil
	default:
		return "", fmt.Errorf("unsupported agents.session status %q", raw)
	}
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if values == nil {
			continue
		}
		switch v := values[key].(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case fmt.Stringer:
			if s := strings.TrimSpace(v.String()); s != "" {
				return s
			}
		}
	}
	return ""
}

func firstValue(values map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if values == nil {
			continue
		}
		value, ok := values[key]
		if !ok || value == nil {
			continue
		}
		return value, true
	}
	return nil, false
}

func copyFirstValue(values map[string]any, target string, keys ...string) {
	if _, ok := values[target]; ok {
		return
	}
	if value, ok := firstValue(values, keys...); ok {
		values[target] = value
	}
}

func firstMapSlice(values map[string]any, keys ...string) ([]map[string]any, bool, error) {
	value, ok := firstValue(values, keys...)
	if !ok {
		return nil, false, nil
	}
	switch raw := value.(type) {
	case []map[string]any:
		return raw, true, nil
	case []any:
		out := make([]map[string]any, 0, len(raw))
		for i, item := range raw {
			mapped, ok := item.(map[string]any)
			if !ok {
				return nil, true, fmt.Errorf("agent session tool call %d must be an object", i+1)
			}
			out = append(out, mapped)
		}
		return out, true, nil
	default:
		return nil, true, fmt.Errorf("agent session tool calls must be an array")
	}
}

func firstInt64(values map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if values == nil {
			continue
		}
		switch v := values[key].(type) {
		case int:
			return int64(v)
		case int64:
			return v
		case float64:
			return int64(v)
		case json.Number:
			n, _ := v.Int64()
			return n
		}
	}
	return 0
}

func firstTime(values map[string]any, keys ...string) time.Time {
	if raw := firstString(values, keys...); raw != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return parsed
		}
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func firstTimePtr(values map[string]any, keys ...string) *time.Time {
	parsed := firstTime(values, keys...)
	if parsed.IsZero() {
		return nil
	}
	return &parsed
}

func firstBool(values map[string]any, keys ...string) bool {
	for _, key := range keys {
		if values == nil {
			continue
		}
		switch v := values[key].(type) {
		case bool:
			return v
		case string:
			return strings.EqualFold(strings.TrimSpace(v), "true")
		}
	}
	return false
}

func stringMap(value any) map[string]string {
	out := map[string]string{}
	raw, ok := value.(map[string]any)
	if !ok {
		return out
	}
	for key, value := range raw {
		if s, ok := value.(string); ok && strings.TrimSpace(key) != "" {
			out[key] = s
		}
	}
	return out
}

func stringMapFromFirst(values map[string]any, keys ...string) map[string]string {
	value, ok := firstValue(values, keys...)
	if !ok {
		return nil
	}
	out := stringMap(value)
	if len(out) == 0 {
		return nil
	}
	return out
}

func rawMessageFromFirst(values map[string]any, keys ...string) (json.RawMessage, bool) {
	value, ok := firstValue(values, keys...)
	if !ok {
		return nil, false
	}
	return mustJSON(value), true
}

func resultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return firstString(payload, "text", "response", "message")
}

func copyAnyMap(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}
