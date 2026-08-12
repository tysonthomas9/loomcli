package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/runhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

const maxRunPayloadBytes = 4 << 20

const (
	defaultRunsLimit = runhistory.DefaultRunsLimit
	maxRunsLimit     = runhistory.MaxRunsLimit
)

type Module struct {
	store store.Store
}

func NewModule(st store.Store) *Module {
	return &Module{store: st}
}

func (m *Module) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workspaces/{ws}/workflows", m.listWorkflows)
	mux.HandleFunc("POST /api/workspaces/{ws}/workflows/{name}/versions", m.createWorkflowVersion)
	// Builtin source/build/run behavior remains in this compatibility module.
	// Registered-driver reads and version lifecycle commands are owned by the
	// Workflow Catalog capability module and registered separately by app/serve.
	mux.HandleFunc("GET /api/workspaces/{ws}/workflows/{name}/source", m.getWorkflowSource)
	// Run history for a workflow: a thin, read-only view over DriverRunStore.List
	// so the UI can show past/active runs (Phase 1). Unlike the run/version
	// mutation paths it must not self-heal a driver, so it uses ResolveDriver.
	mux.HandleFunc("GET /api/workspaces/{ws}/workflows/{name}/runs", m.listWorkflowRuns)
	mux.HandleFunc("POST /api/workspaces/{ws}/workflows/{name}", m.createWorkflowRun)
	mux.HandleFunc("GET /api/workspaces/{ws}/runs/{runId}", m.getRun)
	mux.HandleFunc("GET /api/workspaces/{ws}/runs/{runId}/events", m.getRunEvents)
	mux.HandleFunc("GET /api/workspaces/{ws}/runs/{runId}/stream", m.streamRunEvents)
}

type workflowSummary struct {
	Name    string `json:"name"`
	Builtin bool   `json:"builtin"`
}

// listWorkflows returns the workflows that can be started or bound to a
// trigger. Today that is the built-in catalog (epic-runner, github-review-agent,
// …); exposing it de-hardcodes the frontend, which previously only knew the
// epic-runner name.
func (m *Module) listWorkflows(w http.ResponseWriter, r *http.Request) {
	names := workflowdefs.BuiltinWorkflowNames()
	out := make([]workflowSummary, 0, len(names))
	for _, name := range names {
		out = append(out, workflowSummary{Name: name, Builtin: true})
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"workflows": out})
}

// getWorkflowSource returns a builtin workflow's TS source files so the UI can
// show/seed an editor. Only builtins are available: custom driver versions
// persist a bundle + digest, not source text, so a non-builtin name is a 404
// rather than a fake empty editor.
func (m *Module) getWorkflowSource(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		handler.RespondError(w, http.StatusBadRequest, "workflow name is required")
		return
	}
	spec, ok := workflowdefs.BuiltinWorkflow(name)
	if !ok {
		handler.RespondError(w, http.StatusNotFound, "no builtin source available for workflow "+name)
		return
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{
		"name":       name,
		"builtin":    true,
		"entrypoint": spec.Entrypoint,
		"files":      spec.Files,
	})
}

// listWorkflowRuns returns a workflow's run history, newest first, over
// DriverRunStore.List. It resolves the workflow with ResolveDriver (never
// EnsureAndResolveDriver): listing runs is a read and must not self-heal or
// register a driver as a side effect, so an unregistered workflow is a 404.
func (m *Module) listWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimSpace(r.PathValue("ws"))
	name := strings.TrimSpace(r.PathValue("name"))
	if ws == "" || name == "" {
		writeError(w, http.StatusBadRequest, "workspace and workflow name are required")
		return
	}
	status, ok := parseRunStatusFilter(w, r)
	if !ok {
		return
	}
	limit, ok := runhistory.ParseRunLimit(w, r)
	if !ok {
		return
	}
	drv, err := workflowdefs.ResolveDriver(r.Context(), m.store, ws, name)
	if err != nil {
		writeDomainError(w, err, "resolve workflow driver failed")
		return
	}
	// Both backends now order newest-first by StartedAt BEFORE applying the
	// limit (fleet-db server-side; memstore in store.DriverRuns().List), so the
	// limit can be pushed down — it returns the newest-by-StartedAt window
	// rather than dropping runs that belong in it. The client-side sort/truncate
	// below stays as defense in depth against an unordered backend.
	runs, err := m.store.DriverRuns().List(r.Context(), ws, store.DriverRunFilter{
		DriverID: drv.DriverID,
		Status:   status,
		Limit:    limit,
	})
	if err != nil {
		writeDomainError(w, err, "list workflow runs failed")
		return
	}
	runs = runhistory.SortAndTrim(runs, limit)
	handler.WriteJSON(w, http.StatusOK, map[string]any{
		"driver_id":         drv.DriverID,
		"active_version_id": drv.ActiveVersionID,
		"runs":              runs,
	})
}

// parseRunStatusFilter reads the optional ?status= filter and validates it
// against the known DriverRunStatus values. On an unknown status it writes a
// 400 and returns ok=false; an empty filter is allowed (no status constraint).
func parseRunStatusFilter(w http.ResponseWriter, r *http.Request) (domain.DriverRunStatus, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("status"))
	if raw == "" {
		return "", true
	}
	status := domain.DriverRunStatus(raw)
	if !isKnownRunStatus(status) {
		writeError(w, http.StatusBadRequest, "invalid status: "+raw)
		return "", false
	}
	return status, true
}

func isKnownRunStatus(s domain.DriverRunStatus) bool {
	switch s {
	case domain.DriverRunQueued, domain.DriverRunRunning, domain.DriverRunCompleted,
		domain.DriverRunFailed, domain.DriverRunNeedsReview, domain.DriverRunCancelled,
		domain.DriverRunSuspendedAwaitingEvent:
		return true
	default:
		return false
	}
}

type createWorkflowVersionRequest struct {
	Files      map[string]string `json:"files"`
	Entrypoint string            `json:"entrypoint,omitempty"`
	Activate   *bool             `json:"activate,omitempty"`
}

type workflowVersionInput struct {
	entrypoint string
	files      map[string]string
}

// parseCreateWorkflowVersionRequest decodes and validates the request body
// for createWorkflowVersion. On failure it writes the HTTP error response
// itself and returns ok=false.
func parseCreateWorkflowVersionRequest(w http.ResponseWriter, r *http.Request, name string) (workflowVersionInput, bool) {
	var in workflowVersionInput
	var req createWorkflowVersionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRunPayloadBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return in, false
	}
	if len(req.Files) == 0 {
		writeError(w, http.StatusBadRequest, "files is required")
		return in, false
	}
	in.entrypoint = strings.TrimSpace(req.Entrypoint)
	if in.entrypoint == "" {
		in.entrypoint = filepath.ToSlash(filepath.Join("workflows", name+".ts"))
	}
	if err := workflowdefs.ValidateWorkflowEntrypoint(name, in.entrypoint); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return in, false
	}
	files, err := workflowdefs.ValidateWorkflowFiles(req.Files)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return in, false
	}
	if _, ok := files[in.entrypoint]; !ok {
		writeError(w, http.StatusBadRequest, "entrypoint file is missing")
		return in, false
	}
	if req.Activate != nil && *req.Activate {
		writeError(w, http.StatusBadRequest, "activate=true is not supported; approve and activate the version through the workflow catalog lifecycle API")
		return in, false
	}
	in.files = files
	return in, true
}

func (m *Module) createWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "workflow name is required")
		return
	}
	in, ok := parseCreateWorkflowVersionRequest(w, r, name)
	if !ok {
		return
	}
	result, buildOutput, err := workflowdefs.BuildAndRegister(r.Context(), m.store, workflowdefs.BuildAndRegisterOptions{
		WorkspaceKey: ws,
		Name:         name,
		Entrypoint:   in.entrypoint,
		Files:        in.files,
		// HTTP submission only builds and registers. Approval and activation
		// cross the Workflow Catalog lifecycle command boundary explicitly.
		Activate: false,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	handler.WriteJSON(w, http.StatusCreated, map[string]any{
		"driver":            result.Driver,
		"version":           result.Version,
		"bundle":            result.Bundle,
		"created_driver":    result.CreatedDriver,
		"created_version":   result.CreatedVersion,
		"reused_version":    result.ReusedVersion,
		"activated":         result.Activated,
		"build_diagnostics": buildOutput,
	})
}

func (m *Module) createWorkflowRun(w http.ResponseWriter, r *http.Request) {
	payload, err := readRawJSONBody(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ws := r.PathValue("ws")
	name := strings.TrimSpace(r.PathValue("name"))
	driverID, err := m.resolveWorkflowDriverID(r.Context(), ws, name)
	if err != nil {
		slog.Error("createWorkflowRun: resolveWorkflowDriverID failed", "ws", ws, "workflow", name, "err", err.Error())
		// A genuine not-found is the ONLY case that is a 404 "workflow not found".
		// Any other cause (builtin self-heal failure, a fleet-db rejection, a
		// bundling error) must surface its real status/message instead of being
		// collapsed into a misleading generic 500 "workflow not found", which
		// hides the true failure in the serve log alone.
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "workflow not found")
			return
		}
		writeDomainError(w, err, err.Error())
		return
	}
	// Fail-closed BEFORE creating the run: when the epic-runner routes child
	// task runs to the local task runner, the resolved backend CLI must be
	// installed and authenticated, else the run would fail deep in the worker.
	if err := m.preflightRunnerForRun(r.Context(), ws, name, payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	run, err := driver.CreateDriverRun(r.Context(), m.store, driver.RunOptions{
		WorkspaceKey:   ws,
		DriverID:       driverID,
		EpicID:         driver.DriverRunPayloadEpicID(payload),
		IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")),
		SourceKind:     "api",
		SourceRef:      r.URL.Path,
		Payload:        payload,
	})
	if err != nil {
		writeDomainError(w, err, "create workflow run failed")
		return
	}
	handler.WriteJSON(w, http.StatusAccepted, run)
}

func (m *Module) resolveWorkflowDriverID(ctx context.Context, ws, name string) (string, error) {
	drv, err := workflowdefs.EnsureAndResolveDriver(ctx, m.store, ws, name)
	if err != nil {
		return "", err
	}
	return drv.DriverID, nil
}

func (m *Module) getRun(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	run, err := m.store.DriverRuns().Get(r.Context(), ws, r.PathValue("runId"))
	if err != nil {
		writeDomainError(w, err, "run not found")
		return
	}
	steps, err := m.runStepSummaries(r.Context(), ws, run.RunID)
	if err != nil {
		writeDomainError(w, err, "run steps unavailable")
		return
	}
	handler.WriteJSON(w, http.StatusOK, runDetailResponse{DriverRun: run, Steps: steps})
}

type runDetailResponse struct {
	*domain.DriverRun
	Steps []runStepSummary `json:"steps,omitempty"`
}

type runStepSummary struct {
	ID        string                  `json:"id"`
	StepKind  string                  `json:"step_kind"`
	TaskRunID string                  `json:"task_run_id,omitempty"`
	TaskID    string                  `json:"task_id,omitempty"`
	Status    domain.DriverStepStatus `json:"status"`
}

func (m *Module) runStepSummaries(ctx context.Context, ws, runID string) ([]runStepSummary, error) {
	steps, err := m.store.DriverSteps().ListForRun(ctx, ws, runID, store.DriverStepFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]runStepSummary, 0, len(steps))
	for _, step := range steps {
		if step == nil {
			continue
		}
		summary := runStepSummary{
			ID:        step.StepID,
			StepKind:  step.StepKind,
			TaskRunID: step.TaskRunID,
			Status:    step.Status,
		}
		if step.TaskRunID != "" {
			// Task IDs are best-effort enrichment: older or partial stores may have
			// the step before the task-run row is readable, but the step link itself
			// should still reach the UI.
			if taskRun, err := m.store.TaskRuns().Get(ctx, ws, step.TaskRunID); err == nil && taskRun != nil {
				summary.TaskID = taskRun.TaskID
			}
		}
		out = append(out, summary)
	}
	return out, nil
}

func (m *Module) getRunEvents(w http.ResponseWriter, r *http.Request) {
	page, err := m.loadRunEvents(r.Context(), r, 100)
	if err != nil {
		writeDomainError(w, err, "run events unavailable")
		return
	}
	handler.WriteJSON(w, http.StatusOK, page)
}

func (m *Module) streamRunEvents(w http.ResponseWriter, r *http.Request) {
	reader, ok := m.store.DriverRuns().(store.DriverRunEventsReader)
	if !ok {
		writeError(w, http.StatusNotImplemented, "run event stream is unavailable for this store")
		return
	}
	ws := r.PathValue("ws")
	runID := r.PathValue("runId")
	if _, err := m.store.DriverRuns().Get(r.Context(), ws, runID); err != nil {
		writeDomainError(w, err, "run not found")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is unavailable")
		return
	}
	after := strings.TrimSpace(r.URL.Query().Get("after"))
	if after == "" {
		after = "0"
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		page, err := reader.Events(r.Context(), ws, runID, after, 100)
		if err != nil {
			writeSSE(w, "error", map[string]string{"error": err.Error()})
			flusher.Flush()
			return
		}
		for _, event := range page.Events {
			writeSSE(w, "event", event)
		}
		if page.Cursor != "" {
			after = page.Cursor
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Module) loadRunEvents(ctx context.Context, r *http.Request, defaultLimit int) (*domain.PlatformEventsPage, error) {
	reader, ok := m.store.DriverRuns().(store.DriverRunEventsReader)
	if !ok {
		return nil, store.ErrDriverRunEventsUnavailable
	}
	ws := r.PathValue("ws")
	runID := r.PathValue("runId")
	if _, err := m.store.DriverRuns().Get(ctx, ws, runID); err != nil {
		return nil, err
	}
	limit := defaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 1000 {
			return nil, fmt.Errorf("invalid limit: %w", domain.ErrInvalid)
		}
		limit = parsed
	}
	return reader.Events(ctx, ws, runID, strings.TrimSpace(r.URL.Query().Get("after")), limit)
}

func readRawJSONBody(w http.ResponseWriter, r *http.Request) (json.RawMessage, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRunPayloadBytes))
	if err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("payload must be valid JSON")
	}
	out := make(json.RawMessage, len(body))
	copy(out, body)
	return out, nil
}

func writeSSE(w io.Writer, event string, value any) {
	data, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	handler.WriteJSON(w, status, map[string]string{"error": message})
}

// writeDomainError handles this package's one extra sentinel (event reads on a
// store without event support → 501), then defers to the shared domain mapper.
func writeDomainError(w http.ResponseWriter, err error, fallback string) {
	if errors.Is(err, store.ErrDriverRunEventsUnavailable) {
		writeError(w, http.StatusNotImplemented, err.Error())
		return
	}
	handler.WriteDomainError(w, err, fallback)
}
