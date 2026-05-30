package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	workflowpkg "github.com/tysonthomas9/loomcli/internal/workflow"
)

type runRequest struct {
	Input json.RawMessage `json:"input,omitempty"`
	Once  *bool           `json:"once,omitempty"`
	Wait  bool            `json:"wait,omitempty"`
}

type runResponse struct {
	Run     *domain.WorkflowRun           `json:"run"`
	Builtin *workflowpkg.BuiltinRunResult `json:"builtin,omitempty"`
}

type triggerResponse struct {
	Event string        `json:"event"`
	Runs  []runResponse `json:"runs"`
}

type runListItem struct {
	Run      *domain.WorkflowRun `json:"run"`
	TaskRuns []*domain.TaskRun   `json:"task_runs,omitempty"`
}

func HandleList(st store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws := r.PathValue("ws")
		if err := workflowpkg.EnsureBuiltins(r.Context(), st, ws); err != nil {
			handler.HandleServiceError(w, service.ErrInternal("ensure built-in workflows", err))
			return
		}
		defs, err := st.WorkflowDefinitions().List(r.Context(), ws, store.WorkflowDefinitionFilter{Status: domain.DefinitionStatusActive})
		if err != nil {
			handler.HandleServiceError(w, storeError("list workflow definitions", err))
			return
		}
		handler.WriteJSON(w, http.StatusOK, dto.NewListResponse(defs, len(defs)))
	}
}

func HandleListRuns(st store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter, workItemID, err := parseWorkflowRunListFilter(r)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		ws := r.PathValue("ws")
		items, err := listWorkflowRunItems(r.Context(), st, ws, filter, workItemID)
		if err != nil {
			handler.HandleServiceError(w, storeError("list workflow runs", err))
			return
		}
		handler.WriteJSON(w, http.StatusOK, dto.NewListResponse(items, len(items)))
	}
}

func HandleRun(st store.Store, issueBackendFn func(context.Context) backend.IssueBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := readRunRequest(w, r)
		if !ok {
			return
		}
		ws := r.PathValue("ws")
		resp, err := runWorkflowRequest(r.Context(), st, issueBackendFn, ws, r.PathValue("name"), req, actorName(r))
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusCreated, resp)
	}
}

func HandleRunRouteBinding(st store.Store, issueBackendFn func(context.Context) backend.IssueBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := readRunRequest(w, r)
		if !ok {
			return
		}
		ws := r.PathValue("ws")
		route, err := resolveWorkflowRouteBinding(r.Context(), st, ws, r.Method, "/"+r.PathValue("route"))
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		resp, err := runWorkflowRequest(r.Context(), st, issueBackendFn, ws, route.DefinitionName, req, actorName(r))
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusCreated, resp)
	}
}

func HandleTriggerBinding(st store.Store, issueBackendFn func(context.Context) backend.IssueBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := readRunRequest(w, r)
		if !ok {
			return
		}
		ws := r.PathValue("ws")
		eventType := r.PathValue("event")
		triggers, err := resolveWorkflowTriggerBindings(r.Context(), st, ws, eventType, req.Input)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		resp := triggerResponse{Event: eventType, Runs: make([]runResponse, 0, len(triggers))}
		for _, trigger := range triggers {
			run, err := runWorkflowRequest(r.Context(), st, issueBackendFn, ws, trigger.WorkflowName, req, actorName(r))
			if err != nil {
				handler.HandleServiceError(w, err)
				return
			}
			resp.Runs = append(resp.Runs, run)
		}
		handler.WriteJSON(w, http.StatusCreated, resp)
	}
}

func HandleShow(st store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		run, err := st.WorkflowRuns().Get(r.Context(), r.PathValue("ws"), r.PathValue("runID"))
		if err != nil {
			handler.HandleServiceError(w, storeError("get workflow run", err))
			return
		}
		handler.WriteJSON(w, http.StatusOK, run)
	}
}

func HandleEvents(st store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		events, err := st.RunEvents().List(r.Context(), r.PathValue("ws"), store.RunEventFilter{WorkflowRunID: r.PathValue("runID")})
		if err != nil {
			handler.HandleServiceError(w, storeError("list workflow events", err))
			return
		}
		handler.WriteJSON(w, http.StatusOK, dto.NewListResponse(events, len(events)))
	}
}

func HandleCancel(st store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		finishedAt := &now
		status := domain.WorkflowRunCancelled
		run, err := st.WorkflowRuns().Update(r.Context(), r.PathValue("ws"), r.PathValue("runID"), store.WorkflowRunUpdate{
			Status:     &status,
			FinishedAt: &finishedAt,
		})
		if err != nil {
			handler.HandleServiceError(w, storeError("cancel workflow run", err))
			return
		}
		_, _ = st.RunEvents().Append(r.Context(), store.RunEventAppend{
			WorkspaceKey:  run.WorkspaceKey,
			WorkflowRunID: run.RunID,
			Type:          "workflow_cancelled",
			Message:       "workflow run cancelled",
			Data:          mustJSON(map[string]string{"actor": actorName(r)}),
		})
		handler.WriteJSON(w, http.StatusOK, run)
	}
}

func readRunRequest(w http.ResponseWriter, r *http.Request) (runRequest, bool) {
	req := runRequest{Input: json.RawMessage(`{}`)}
	if r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0 {
		if err := handler.ReadJSON(w, r, &req); err != nil {
			handler.HandleServiceError(w, err)
			return req, false
		}
	}
	if len(req.Input) == 0 {
		req.Input = json.RawMessage(`{}`)
	}
	var tmp any
	if err := json.Unmarshal(req.Input, &tmp); err != nil {
		handler.HandleServiceError(w, service.ErrValidation("input must be valid JSON"))
		return req, false
	}
	return req, true
}

func (r runRequest) runOnce() bool {
	return r.Once == nil || *r.Once
}

func runWorkflowRequest(ctx context.Context, st store.Store, issueBackendFn func(context.Context) backend.IssueBackend, ws, workflowName string, req runRequest, actor string) (runResponse, error) {
	run, err := workflowpkg.CreateOrResumeRun(ctx, st, ws, workflowName, req.Input, actor)
	if err != nil {
		return runResponse{}, storeError("create workflow run", err)
	}
	var result *workflowpkg.BuiltinRunResult
	if req.runOnce() {
		if issueBackendFn == nil {
			return runResponse{}, service.ErrUnavailable("issue backend not configured")
		}
		ib := issueBackendFn(ctx)
		if ib == nil {
			return runResponse{}, service.ErrUnavailable("issue backend not available")
		}
		result, err = workflowpkg.RunOnce(ctx, st, ib, run)
		if err != nil {
			return runResponse{}, storeError("run workflow", err)
		}
		run = result.Run
	}
	if req.Wait {
		run, err = waitWorkflow(ctx, st, ws, run.RunID)
		if err != nil {
			return runResponse{}, storeError("wait workflow run", err)
		}
	}
	return runResponse{Run: run, Builtin: result}, nil
}

func parseWorkflowRunListFilter(r *http.Request) (store.WorkflowRunFilter, string, error) {
	opts, err := handler.ParseListOpts(r)
	if err != nil {
		return store.WorkflowRunFilter{}, "", err
	}
	status, err := parseWorkflowRunStatus(opts.Status)
	if err != nil {
		return store.WorkflowRunFilter{}, "", err
	}
	return store.WorkflowRunFilter{
		WorkflowName: strings.TrimSpace(r.URL.Query().Get("workflow_name")),
		Status:       status,
		Limit:        opts.Limit,
	}, strings.TrimSpace(r.URL.Query().Get("work_item_id")), nil
}

func parseWorkflowRunStatus(raw string) (domain.WorkflowRunStatus, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	status := domain.WorkflowRunStatus(raw)
	switch status {
	case domain.WorkflowRunQueued, domain.WorkflowRunRunning, domain.WorkflowRunWaiting, domain.WorkflowRunCompleted, domain.WorkflowRunFailed, domain.WorkflowRunCancelled:
		return status, nil
	default:
		return "", service.ErrValidation("invalid workflow run status")
	}
}

func listWorkflowRunItems(ctx context.Context, st store.Store, ws string, filter store.WorkflowRunFilter, workItemID string) ([]runListItem, error) {
	if workItemID == "" {
		runs, err := st.WorkflowRuns().List(ctx, ws, filter)
		if err != nil {
			return nil, err
		}
		items := make([]runListItem, 0, len(runs))
		for _, run := range runs {
			items = append(items, runListItem{Run: run})
		}
		return items, nil
	}

	taskRuns, err := st.TaskRuns().List(ctx, ws, store.TaskRunFilter{WorkItemID: workItemID, Limit: 10000})
	if err != nil {
		return nil, err
	}
	taskRunsByWorkflowRun := make(map[string][]*domain.TaskRun)
	for _, taskRun := range taskRuns {
		if taskRun == nil || taskRun.WorkflowRunID == "" {
			continue
		}
		taskRunsByWorkflowRun[taskRun.WorkflowRunID] = append(taskRunsByWorkflowRun[taskRun.WorkflowRunID], taskRun)
	}

	items := make([]runListItem, 0, len(taskRunsByWorkflowRun))
	for runID, relatedTaskRuns := range taskRunsByWorkflowRun {
		run, err := st.WorkflowRuns().Get(ctx, ws, runID)
		if err != nil {
			return nil, err
		}
		if filter.WorkflowName != "" && run.WorkflowName != filter.WorkflowName {
			continue
		}
		if filter.Status != "" && run.Status != filter.Status {
			continue
		}
		items = append(items, runListItem{Run: run, TaskRuns: relatedTaskRuns})
	}
	sort.Slice(items, func(i, j int) bool {
		left, right := items[i].Run, items[j].Run
		if left == nil || right == nil {
			return right == nil
		}
		return left.CreatedAt.After(right.CreatedAt)
	})
	if filter.Limit > 0 && len(items) > filter.Limit {
		items = items[:filter.Limit]
	}
	return items, nil
}

func resolveWorkflowRouteBinding(ctx context.Context, st store.Store, ws, method, routePath string) (*domain.RouteBinding, error) {
	if st == nil || st.RouteBindings() == nil {
		return nil, service.ErrUnavailable("route binding store not configured")
	}
	routes, err := st.RouteBindings().List(ctx, ws, store.RouteBindingFilter{Status: domain.DefinitionStatusActive, Limit: 10000})
	if err != nil {
		return nil, storeError("list route bindings", err)
	}
	wantPath := normalizeRoutePath(routePath)
	wantMethod := strings.ToUpper(strings.TrimSpace(method))
	if wantMethod == "" {
		wantMethod = http.MethodPost
	}
	for _, route := range routes {
		if route == nil || route.DefinitionType != domain.DefinitionTypeWorkflow {
			continue
		}
		routeMethod := strings.ToUpper(strings.TrimSpace(route.Method))
		if routeMethod == "" {
			routeMethod = http.MethodPost
		}
		if routeMethod != wantMethod || normalizeRoutePath(route.Path) != wantPath {
			continue
		}
		if strings.TrimSpace(route.AuthPolicy) != "workspace" {
			return nil, service.ErrForbidden("workflow route binding auth policy is not supported")
		}
		return route, nil
	}
	return nil, service.ErrNotFound("workflow route binding not found")
}

func resolveWorkflowTriggerBindings(ctx context.Context, st store.Store, ws, eventType string, input json.RawMessage) ([]*domain.TriggerBinding, error) {
	if st == nil || st.TriggerBindings() == nil {
		return nil, service.ErrUnavailable("trigger binding store not configured")
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return nil, service.ErrValidation("trigger event is required")
	}
	triggers, err := st.TriggerBindings().List(ctx, ws, store.TriggerBindingFilter{
		EventType: eventType,
		Status:    domain.DefinitionStatusActive,
		Limit:     10000,
	})
	if err != nil {
		return nil, storeError("list trigger bindings", err)
	}
	out := make([]*domain.TriggerBinding, 0, len(triggers))
	for _, trigger := range triggers {
		if trigger == nil || !triggerFilterMatches(trigger.Filter, input) {
			continue
		}
		out = append(out, trigger)
	}
	if len(out) == 0 {
		return nil, service.ErrNotFound("workflow trigger binding not found")
	}
	return out, nil
}

func triggerFilterMatches(filter json.RawMessage, input json.RawMessage) bool {
	var required map[string]string
	if len(filter) > 0 {
		_ = json.Unmarshal(filter, &required)
	}
	if len(required) == 0 {
		return true
	}
	var payload map[string]any
	if len(input) > 0 {
		_ = json.Unmarshal(input, &payload)
	}
	if len(payload) == 0 {
		return false
	}
	for key, want := range required {
		if strings.TrimSpace(want) == "" {
			continue
		}
		if strings.TrimSpace(fmt.Sprint(payload[key])) != want {
			return false
		}
	}
	return true
}

func normalizeRoutePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return "/"
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	clean := path.Clean(value)
	if clean == "." {
		return "/"
	}
	return clean
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

func storeError(msg string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return service.ErrNotFound(msg)
	case errors.Is(err, domain.ErrInvalid):
		return service.ErrValidation(msg)
	case errors.Is(err, domain.ErrConflict):
		return service.ErrConflict(msg)
	default:
		return service.ErrInternal(msg, err)
	}
}

func actorName(r *http.Request) string {
	if r != nil {
		if actor := strings.TrimSpace(r.Header.Get("X-Actor")); actor != "" {
			return actor
		}
	}
	if actor := strings.TrimSpace(os.Getenv("LOOM_ACTOR")); actor != "" {
		return actor
	}
	if actor := strings.TrimSpace(os.Getenv("USER")); actor != "" {
		return actor
	}
	return "webui"
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	return data
}
