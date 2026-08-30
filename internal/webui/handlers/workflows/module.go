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
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

const maxRunPayloadBytes = 4 << 20

type Module struct {
	store store.Store
}

func NewModule(st store.Store) *Module {
	return &Module{store: st}
}

func (m *Module) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/workspaces/{ws}/workflows/{name}/versions", m.createWorkflowVersion)
	mux.HandleFunc("POST /api/workspaces/{ws}/workflows/{name}", m.createWorkflowRun)
	mux.HandleFunc("GET /api/workspaces/{ws}/runs/{runId}", m.getRun)
	mux.HandleFunc("GET /api/workspaces/{ws}/runs/{runId}/events", m.getRunEvents)
	mux.HandleFunc("GET /api/workspaces/{ws}/runs/{runId}/stream", m.streamRunEvents)
}

type createWorkflowVersionRequest struct {
	Files      map[string]string `json:"files"`
	Entrypoint string            `json:"entrypoint,omitempty"`
	Activate   *bool             `json:"activate,omitempty"`
}

type workflowVersionInput struct {
	entrypoint string
	files      map[string]string
	activate   bool
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
	in.files = files
	in.activate = true
	if req.Activate != nil {
		in.activate = *req.Activate
	}
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
		Activate:     in.activate,
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
	if isBuiltinWorkflowName(name) {
		if err := workflowdefs.EnsureBuiltinWorkflow(ctx, m.store, ws, name); err != nil {
			return "", err
		}
	}
	driverID, err := workflowdefs.ResolveDriverID(ctx, m.store, ws, name)
	if err == nil {
		return driverID, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return "", err
	}
	if err := workflowdefs.EnsureBuiltinWorkflow(ctx, m.store, ws, name); err != nil {
		return "", err
	}
	return workflowdefs.ResolveDriverID(ctx, m.store, ws, name)
}

func isBuiltinWorkflowName(name string) bool {
	for _, builtin := range workflowdefs.BuiltinWorkflowNames() {
		if builtin == name {
			return true
		}
	}
	return false
}

func (m *Module) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := m.store.DriverRuns().Get(r.Context(), r.PathValue("ws"), r.PathValue("runId"))
	if err != nil {
		writeDomainError(w, err, "run not found")
		return
	}
	handler.WriteJSON(w, http.StatusOK, run)
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
	sw, err := realtime.NewWriter(w)
	if err != nil {
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
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	// Commit the response now so clients observe the stream as open even when
	// the first page is empty — a completed run never produces another event,
	// and the old loop flushed every fetched page, empty ones included.
	_ = http.NewResponseController(w).Flush()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		page, err := reader.Events(r.Context(), ws, runID, after, 100)
		if err != nil {
			_ = writeSSE(sw, "error", map[string]string{"error": err.Error()})
			return
		}
		for _, event := range page.Events {
			if err := writeSSE(sw, "event", event); err != nil {
				return
			}
		}
		if page.Cursor != "" {
			after = page.Cursor
		}
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

func writeSSE(sw *realtime.Writer, event string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return sw.WriteEventNoID(event, string(data))
}

func writeError(w http.ResponseWriter, status int, message string) {
	handler.WriteJSON(w, status, map[string]string{"error": message})
}

func writeDomainError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, fallback)
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrDriverRunEventsUnavailable):
		writeError(w, http.StatusNotImplemented, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, fallback)
	}
}
