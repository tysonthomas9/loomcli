package workflow

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

//go:embed ts_context_runner.js
var tsContextRunnerSource string

type tsContextRequest struct {
	ID              string              `json:"id"`
	SourcePath      string              `json:"sourcePath"`
	Input           map[string]any      `json:"input"`
	Env             map[string]string   `json:"env,omitempty"`
	ReadyChildren   []backend.IssueData `json:"readyChildren,omitempty"`
	BlockedChildren []backend.IssueData `json:"blockedChildren,omitempty"`
	ChildWorkItems  []backend.IssueData `json:"childWorkItems,omitempty"`
}

type tsContextResponse struct {
	Result     json.RawMessage       `json:"result"`
	Logs       []tsWorkflowLog       `json:"logs"`
	Operations []tsWorkflowOperation `json:"operations"`
}

type tsWorkflowLog struct {
	Level      string         `json:"level"`
	Message    string         `json:"message"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type tsWorkflowOperation struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
}

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
	request, parentID, err := buildTSContextRequest(ctx, ib, run, sourcePath)
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
	taskRuns, err := applyTSOperations(ctx, st, run, parentID, response.Operations)
	if err != nil {
		return nil, err
	}
	taskRuns, dispatched, err := dispatchTSContextTaskRuns(ctx, st, run, parentID, taskRuns)
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
	return finishTSContextRun(ctx, st, run, response.Result, result, parentID)
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

func finishTSContextRun(ctx context.Context, st store.Store, run *domain.WorkflowRun, runResult json.RawMessage, result *BuiltinRunResult, parentID string) (*BuiltinRunResult, error) {
	if len(result.TaskRuns) > 0 || result.OpenCount > 0 {
		status := domain.WorkflowRunWaiting
		wait := "workflow_context_task_runs(workflow_run:" + run.RunID + ")"
		if parentID != "" {
			wait = "work_item_changed(parent:" + parentID + ") OR task_run_terminal(workflow_run:" + run.RunID + ")"
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
			Data:          mustJSON(map[string]any{"ensured": len(result.TaskRuns), "open": result.OpenCount, "blocked": result.BlockedCount}),
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

func buildTSContextRequest(ctx context.Context, ib backend.IssueBackend, run *domain.WorkflowRun, sourcePath string) (tsContextRequest, string, error) {
	inputMap := map[string]any{}
	if len(run.Input) > 0 {
		if err := json.Unmarshal(run.Input, &inputMap); err != nil {
			return tsContextRequest{}, "", fmt.Errorf("decode workflow input: %w", err)
		}
	}
	parentID := firstString(inputMap, "parentId", "parent_id")
	request := tsContextRequest{
		ID:         run.RunID,
		SourcePath: sourcePath,
		Input:      inputMap,
		Env:        map[string]string{},
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

func applyTSOperations(ctx context.Context, st store.Store, run *domain.WorkflowRun, parentID string, operations []tsWorkflowOperation) ([]*domain.TaskRun, error) {
	taskRuns := make([]*domain.TaskRun, 0)
	for _, op := range operations {
		switch op.Type {
		case "taskRuns.ensure":
			taskRun, err := applyEnsureTaskRunOperation(ctx, st, run, parentID, op.Params)
			if err != nil {
				return nil, err
			}
			taskRuns = append(taskRuns, taskRun)
		default:
			return nil, fmt.Errorf("unsupported WorkflowContext operation %q", op.Type)
		}
	}
	return taskRuns, nil
}

func applyEnsureTaskRunOperation(ctx context.Context, st store.Store, run *domain.WorkflowRun, parentID string, params map[string]any) (*domain.TaskRun, error) {
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
