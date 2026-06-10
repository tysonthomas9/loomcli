package workflowruns

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/workflows"
	"github.com/tysonthomas9/loomcli/internal/workflows/platform"
)

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func respondError(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}

func respondStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		respondError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrConflict):
		respondError(w, http.StatusConflict, err.Error())
	case errors.Is(err, domain.ErrInvalid):
		respondError(w, http.StatusBadRequest, err.Error())
	default:
		respondError(w, http.StatusBadGateway, err.Error())
	}
}

func (m *Module) handleListRuns(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	runs, err := m.store.DriverRuns().List(r.Context(), ws, platform.DriverRunFilter{
		DriverID: q.Get("driver"),
		EpicID:   q.Get("epic"),
		Status:   platform.DriverRunStatus(q.Get("status")),
		Limit:    limit,
	})
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"runs": runs, "count": len(runs)})
}

func (m *Module) handleGetRun(w http.ResponseWriter, r *http.Request) {
	run, err := m.store.DriverRuns().Get(r.Context(), r.PathValue("ws"), r.PathValue("id"))
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, run)
}

func (m *Module) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	after := q.Get("after")
	if after == "" {
		after = "0"
	}
	events, cursor, err := m.store.DriverRuns().Events(r.Context(), r.PathValue("ws"), r.PathValue("id"), after, limit)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"events": events, "cursor": cursor})
}

// handleTailRun streams run state over SSE until the run is terminal:
// one `event: run` frame per status change and one `event: run_event`
// frame per new lifecycle event, sourced by polling fleet-db.
func (m *Module) handleTailRun(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		respondError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ws, runID := r.PathValue("ws"), r.PathValue("id")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	writeFrame := func(event string, v any) {
		data, err := json.Marshal(v)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
	}

	cursor := "0"
	var lastStatus platform.DriverRunStatus
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		run, err := m.store.DriverRuns().Get(r.Context(), ws, runID)
		if err != nil {
			writeFrame("error", map[string]string{"error": err.Error()})
			return
		}
		if run.Status != lastStatus {
			lastStatus = run.Status
			writeFrame("run", run)
		}
		events, next, err := m.store.DriverRuns().Events(r.Context(), ws, runID, cursor, 500)
		if err == nil {
			cursor = next
			for _, e := range events {
				writeFrame("run_event", e)
			}
		}
		if run.Status.Terminal() {
			writeFrame("done", map[string]string{"status": string(run.Status)})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

type runEpicRequest struct {
	Driver string `json:"driver,omitempty"`
}

// handleRunEpic is the "Run Epic" admission endpoint. It returns 409
// with the active run when fleet-db's one_active_per_epic invariant
// absorbs the request, and 503 when no driver version is registered
// (no execution plane has ever attached).
func (m *Module) handleRunEpic(w http.ResponseWriter, r *http.Request) {
	ws, epicID := r.PathValue("ws"), r.PathValue("epic")
	var req runEpicRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req) // empty body = defaults
	}
	driverName := req.Driver
	if driverName == "" {
		driverName = "epic-runner"
	}

	driver, err := m.store.Drivers().Get(r.Context(), ws, driverName)
	if err != nil || driver.ActiveVersionID == "" {
		respondError(w, http.StatusServiceUnavailable,
			fmt.Sprintf("driver %q has no registered version — no execution plane connected (start `loom workflow dev`)", driverName))
		return
	}

	requestedID := fmt.Sprintf("run-%s-%d", workflows.SanitizeID(epicID), time.Now().UnixNano())
	run, err := m.store.DriverRuns().Create(r.Context(), ws, platform.DriverRunCreate{
		RunID:           requestedID,
		DriverID:        driver.DriverID,
		DriverVersionID: driver.ActiveVersionID,
		EpicID:          epicID,
		SourceKind:      "ui",
	})
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if run.RunID != requestedID {
		respondJSON(w, http.StatusConflict, map[string]any{
			"error":      "epic already has an active run",
			"active_run": run,
		})
		return
	}
	respondJSON(w, http.StatusCreated, run)
}
