package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
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
