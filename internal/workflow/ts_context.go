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

type tsContextRequest struct {
	ID              string                   `json:"id"`
	SourcePath      string                   `json:"sourcePath"`
	Input           map[string]any           `json:"input"`
	Env             map[string]string        `json:"env,omitempty"`
	Request         tsContextRequestMetadata `json:"request"`
	ReadyChildren   []backend.IssueData      `json:"readyChildren,omitempty"`
	BlockedChildren []backend.IssueData      `json:"blockedChildren,omitempty"`
	ChildWorkItems  []backend.IssueData      `json:"childWorkItems,omitempty"`
}

type tsContextRequestMetadata struct {
	WorkspaceKey    string `json:"workspaceKey"`
	WorkflowName    string `json:"workflowName"`
	WorkflowVersion string `json:"workflowVersion"`
	Actor           string `json:"actor,omitempty"`
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
	request, parentID, err := buildTSContextRequest(ctx, ib, run, def, sourcePath)
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

func buildTSContextRequest(ctx context.Context, ib backend.IssueBackend, run *domain.WorkflowRun, def *domain.WorkflowDefinition, sourcePath string) (tsContextRequest, string, error) {
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
		Env:        workflowEnvBindings(def),
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
		case "tools.call":
			appendToolCallEvent(ctx, st, run, op.Params)
		case "artifacts.record":
			if err := applyRecordArtifactOperation(ctx, st, run, op.Params); err != nil {
				return nil, err
			}
		case "agents.session":
			if err := applyInitializeAgentSessionOperation(ctx, st, run, op.Params); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported WorkflowContext operation %q", op.Type)
		}
	}
	return taskRuns, nil
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

func applyRecordArtifactOperation(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) error {
	if st.Artifacts() == nil {
		return fmt.Errorf("artifact store not configured")
	}
	uri := firstString(params, "uri")
	if uri == "" {
		return fmt.Errorf("artifacts.record requires uri")
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
		Metadata:     stringMap(params["metadata"]),
	})
	if err != nil {
		return fmt.Errorf("record workflow artifact %s: %w", artifactID, err)
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
	return metadata
}

func appendAgentSessionInitializedEvent(ctx context.Context, st store.Store, run *domain.WorkflowRun, session *domain.AgentSession) {
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "agent_session_initialized",
		Message:       "workflow agent session initialized",
		Data: mustJSON(map[string]string{
			"session_id": session.SessionID,
			"agent_id":   session.AgentID,
			"kind":       string(session.Kind),
			"task_id":    session.TaskID,
			"phase":      session.Phase,
		}),
	})
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

func generatedArtifactID(runID, artifactType, uri string) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + artifactType + "\x00" + uri))
	return "artifact:" + runID + ":" + hex.EncodeToString(sum[:])[:12]
}

func generatedSessionID(runID, agentID, harnessName, sessionName, taskID string) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + agentID + "\x00" + harnessName + "\x00" + sessionName + "\x00" + taskID))
	return "session:" + runID + ":" + hex.EncodeToString(sum[:])[:12]
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
