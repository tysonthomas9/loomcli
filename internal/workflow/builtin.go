package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const RunParentWorkItemsName = "run-parent-work-items"

type ParentWorkItemsInput struct {
	ParentID       string `json:"parentId"`
	Role           string `json:"role,omitempty"`
	MaxConcurrency int    `json:"maxConcurrency,omitempty"`
}

type BuiltinRunResult struct {
	Run          *domain.WorkflowRun `json:"run"`
	TaskRuns     []*domain.TaskRun   `json:"task_runs,omitempty"`
	Done         bool                `json:"done"`
	ReadyCount   int                 `json:"ready_count"`
	OpenCount    int                 `json:"open_count"`
	BlockedCount int                 `json:"blocked_count"`
}

func EnsureBuiltins(ctx context.Context, st store.Store, workspaceKey string) error {
	if st == nil || st.WorkflowDefinitions() == nil {
		return fmt.Errorf("workflow definition store not configured")
	}
	manifest := json.RawMessage(`{"builtin":true,"runner":"go","kind":"parent-work-items"}`)
	inputSchema := json.RawMessage(`{"type":"object","required":["parentId"],"properties":{"parentId":{"type":"string"},"role":{"type":"string","default":"task"},"maxConcurrency":{"type":"number","default":4}}}`)
	_, err := st.WorkflowDefinitions().Upsert(ctx, store.WorkflowDefinitionUpsert{
		WorkspaceKey:    workspaceKey,
		Name:            RunParentWorkItemsName,
		Version:         "builtin-v1",
		Description:     "Ensure one live TaskRun per ready child under a parent work item.",
		InputSchema:     inputSchema,
		SingletonPolicy: "parent:${input.parentId}",
		SourceRef:       "builtin:" + RunParentWorkItemsName,
		BundleHash:      "builtin:" + RunParentWorkItemsName + ":v1",
		Manifest:        manifest,
		Status:          domain.DefinitionStatusActive,
	})
	return err
}

func CreateOrResumeRun(ctx context.Context, st store.Store, workspaceKey, workflowName string, input json.RawMessage, actor string) (*domain.WorkflowRun, error) {
	if err := EnsureBuiltins(ctx, st, workspaceKey); err != nil {
		return nil, err
	}
	def, err := st.WorkflowDefinitions().Get(ctx, workspaceKey, workflowName)
	if err != nil {
		return nil, err
	}
	key, err := idempotencyKey(workflowName, input)
	if err != nil {
		return nil, err
	}
	run, err := st.WorkflowRuns().CreateOrResume(ctx, store.WorkflowRunCreate{
		WorkspaceKey:    workspaceKey,
		WorkflowName:    workflowName,
		WorkflowVersion: def.Version,
		BundleHash:      def.BundleHash,
		IdempotencyKey:  key,
		Input:           input,
		Status:          domain.WorkflowRunQueued,
		LeaseOwner:      actor,
		StartedAt:       time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  workspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "workflow_started",
		Message:       "workflow run created or resumed",
		Data:          mustJSON(map[string]string{"workflow_name": workflowName}),
	})
	return run, nil
}

func RunBuiltinOnce(ctx context.Context, st store.Store, ib backend.IssueBackend, run *domain.WorkflowRun) (*BuiltinRunResult, error) {
	if run == nil {
		return nil, fmt.Errorf("workflow run required")
	}
	switch run.WorkflowName {
	case RunParentWorkItemsName:
		return runParentWorkItemsOnce(ctx, st, ib, run)
	default:
		return nil, fmt.Errorf("workflow %q is not a built-in workflow", run.WorkflowName)
	}
}

func RunOnce(ctx context.Context, st store.Store, ib backend.IssueBackend, run *domain.WorkflowRun) (*BuiltinRunResult, error) {
	if run == nil {
		return nil, fmt.Errorf("workflow run required")
	}
	if run.WorkflowName == RunParentWorkItemsName {
		return runParentWorkItemsOnce(ctx, st, ib, run)
	}
	if st == nil || st.WorkflowDefinitions() == nil {
		return nil, fmt.Errorf("workflow definition store not configured")
	}
	def, err := st.WorkflowDefinitions().Get(ctx, run.WorkspaceKey, run.WorkflowName)
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Builtin string `json:"builtin"`
	}
	_ = json.Unmarshal(def.Manifest, &manifest)
	switch manifest.Builtin {
	case RunParentWorkItemsName:
		_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
			WorkspaceKey:  run.WorkspaceKey,
			WorkflowRunID: run.RunID,
			Type:          "workflow_ts_reconciled",
			Message:       "code-defined workflow delegated to constrained built-in runner",
			Data:          mustJSON(map[string]string{"builtin": manifest.Builtin, "source_ref": def.SourceRef}),
		})
		return runParentWorkItemsOnce(ctx, st, ib, run)
	case "":
		return nil, fmt.Errorf("code-defined workflow %q has no constrained runner in manifest", run.WorkflowName)
	default:
		return nil, fmt.Errorf("code-defined workflow %q declares unsupported constrained runner %q", run.WorkflowName, manifest.Builtin)
	}
}

//nolint:funlen // The built-in reconcile pass keeps query, ensure, and terminal-state updates in one readable flow.
func runParentWorkItemsOnce(ctx context.Context, st store.Store, ib backend.IssueBackend, run *domain.WorkflowRun) (*BuiltinRunResult, error) {
	if st == nil || st.TaskRuns() == nil || st.RunEvents() == nil {
		return nil, fmt.Errorf("workflow stores not configured")
	}
	if ib == nil {
		return nil, fmt.Errorf("issue backend not configured")
	}
	input, err := decodeParentInput(run.Input)
	if err != nil {
		return nil, err
	}
	ready, err := ib.Ready(ctx, backend.ReadyOpts{ParentID: input.ParentID, Limit: 256})
	if err != nil {
		return nil, fmt.Errorf("ready child query: %w", err)
	}
	blocked, err := ib.Blocked(ctx, backend.BlockedOpts{ParentID: input.ParentID, Limit: 256})
	if err != nil {
		return nil, fmt.Errorf("blocked child query: %w", err)
	}
	all, err := ib.List(ctx, backend.ListOpts{ParentID: input.ParentID, Limit: 10000})
	if err != nil {
		return nil, fmt.Errorf("list child work: %w", err)
	}
	openCount := countOpen(all)
	live, err := st.TaskRuns().List(ctx, run.WorkspaceKey, store.TaskRunFilter{WorkflowRunID: run.RunID, Live: true, Limit: 10000})
	if err != nil {
		return nil, fmt.Errorf("list live task runs: %w", err)
	}
	liveWork := make(map[string]struct{}, len(live))
	for _, tr := range live {
		if tr != nil {
			liveWork[tr.WorkItemID] = struct{}{}
		}
	}
	capacity := input.MaxConcurrency - len(live)
	created, err := ensureReadyTaskRuns(ctx, st, run, input, ready, liveWork, capacity)
	if err != nil {
		return nil, err
	}
	result := &BuiltinRunResult{
		Run:          run,
		TaskRuns:     created,
		Done:         openCount == 0,
		ReadyCount:   len(ready),
		OpenCount:    openCount,
		BlockedCount: len(blocked),
	}
	if result.Done {
		now := time.Now().UTC()
		finishedAt := &now
		status := domain.WorkflowRunCompleted
		data := mustJSON(map[string]any{"status": "completed"})
		updated, err := st.WorkflowRuns().Update(ctx, run.WorkspaceKey, run.RunID, store.WorkflowRunUpdate{
			Status:     &status,
			Result:     &data,
			FinishedAt: &finishedAt,
		})
		if err != nil {
			return nil, fmt.Errorf("complete workflow run: %w", err)
		}
		result.Run = updated
		_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
			WorkspaceKey:  run.WorkspaceKey,
			WorkflowRunID: run.RunID,
			Type:          "workflow_completed",
			Message:       "all child work is terminal",
			Data:          data,
		})
		return result, nil
	}
	status := domain.WorkflowRunWaiting
	wait := "work_item_changed(parent:" + input.ParentID + ") OR task_run_terminal(workflow_run:" + run.RunID + ")"
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
		Message:       "waiting for child work changes",
		Data:          mustJSON(map[string]any{"ready": len(ready), "open": openCount, "blocked": len(blocked)}),
	})
	return result, nil
}

func ensureReadyTaskRuns(ctx context.Context, st store.Store, run *domain.WorkflowRun, input ParentWorkItemsInput, ready []backend.IssueData, liveWork map[string]struct{}, capacity int) ([]*domain.TaskRun, error) {
	created := make([]*domain.TaskRun, 0)
	for _, child := range ready {
		if capacity <= 0 {
			break
		}
		if !shouldEnsureChild(child, liveWork) {
			continue
		}
		tr, err := st.TaskRuns().Ensure(ctx, taskRunEnsure(run, input, child))
		if err != nil {
			return nil, fmt.Errorf("ensure task run for %s: %w", child.ID, err)
		}
		created = append(created, tr)
		capacity--
		_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
			WorkspaceKey:  run.WorkspaceKey,
			WorkflowRunID: run.RunID,
			TaskRunID:     tr.TaskRunID,
			Type:          "task_run_ensured",
			Message:       "ensured child task run",
			Data:          mustJSON(map[string]string{"work_item_id": child.ID, "role": input.Role}),
		})
	}
	return created, nil
}

func shouldEnsureChild(child backend.IssueData, liveWork map[string]struct{}) bool {
	if child.Status == "closed" || child.Status == "deferred" {
		return false
	}
	_, alreadyLive := liveWork[child.ID]
	return !alreadyLive
}

func taskRunEnsure(run *domain.WorkflowRun, input ParentWorkItemsInput, child backend.IssueData) store.TaskRunEnsure {
	return store.TaskRunEnsure{
		WorkspaceKey:   run.WorkspaceKey,
		WorkflowRunID:  run.RunID,
		WorkItemID:     child.ID,
		RoleName:       input.Role,
		IdempotencyKey: "child:" + child.ID + ":role:" + input.Role,
		Reason:         child.Title,
		Metadata: map[string]string{
			"parent_id":     input.ParentID,
			"workflow_name": run.WorkflowName,
		},
	}
}

func decodeParentInput(raw json.RawMessage) (ParentWorkItemsInput, error) {
	var input ParentWorkItemsInput
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &input); err != nil {
			return input, fmt.Errorf("decode workflow input: %w", err)
		}
	}
	if input.ParentID == "" {
		var alt struct {
			ParentID       string `json:"parent_id"`
			Role           string `json:"role"`
			MaxConcurrency int    `json:"max_concurrency"`
		}
		_ = json.Unmarshal(raw, &alt)
		input.ParentID = alt.ParentID
		if input.Role == "" {
			input.Role = alt.Role
		}
		if input.MaxConcurrency == 0 {
			input.MaxConcurrency = alt.MaxConcurrency
		}
	}
	input.ParentID = strings.TrimSpace(input.ParentID)
	input.Role = strings.TrimSpace(input.Role)
	if input.Role == "" {
		input.Role = "task"
	}
	if input.MaxConcurrency <= 0 {
		input.MaxConcurrency = 4
	}
	if input.ParentID == "" {
		return input, fmt.Errorf("workflow input parentId is required")
	}
	return input, nil
}

func idempotencyKey(workflowName string, input json.RawMessage) (string, error) {
	if workflowName != RunParentWorkItemsName {
		return workflowName + ":" + string(input), nil
	}
	decoded, err := decodeParentInput(input)
	if err != nil {
		return "", err
	}
	return "parent:" + decoded.ParentID, nil
}

func countOpen(issues []backend.IssueData) int {
	count := 0
	for _, issue := range issues {
		if issue.Status != "closed" && issue.Status != "deferred" {
			count++
		}
	}
	return count
}

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
