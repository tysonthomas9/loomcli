package leadapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
	"github.com/tysonthomas9/loomcli/internal/workflows"
)

type epicRunDispatchParams struct {
	EpicID         string `json:"epicId"`
	MaxConcurrency *int   `json:"maxConcurrency,omitempty"`
	Runner         string `json:"runner,omitempty"`
}

type epicRunDispatchView struct {
	RunID    string                 `json:"runId"`
	Workflow string                 `json:"workflow"`
	EpicID   string                 `json:"epicId"`
	Status   domain.DriverRunStatus `json:"status"`
}

type epicRunDispatchResponse struct {
	Success bool                `json:"success"`
	Data    epicRunDispatchView `json:"data"`
}

type runStatusView struct {
	RunID      string                 `json:"runId"`
	EpicID     string                 `json:"epicId,omitempty"`
	Status     domain.DriverRunStatus `json:"status"`
	Terminal   bool                   `json:"terminal"`
	Summary    string                 `json:"summary,omitempty"`
	ErrorClass string                 `json:"errorClass,omitempty"`
	StartedAt  *time.Time             `json:"startedAt,omitempty"`
	FinishedAt *time.Time             `json:"finishedAt,omitempty"`
}

type runStatusResponse struct {
	Success bool          `json:"success"`
	Data    runStatusView `json:"data"`
}

func (m *Module) epicRunDispatch(w http.ResponseWriter, r *http.Request, id occupantIdentity) {
	params, err := decodeEpicRunParams(r.Body)
	if err != nil {
		writeDispatchError(w, err)
		return
	}
	ws := id.claims.WorkspaceKey
	plan, err := m.planEpicRun(r.Context(), ws, id, params)
	if err != nil {
		writeDispatchError(w, err)
		return
	}
	run, err := m.createEpicRun(r.Context(), ws, id, plan)
	if err != nil {
		writeDispatchError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, epicRunDispatchResponse{Success: true, Data: epicRunDispatchView{
		RunID: run.RunID, Workflow: workflows.BuiltinEpicRunnerWorkflowName,
		EpicID: plan.epicID, Status: run.Status,
	}})
}

func (m *Module) epicRunStatus(w http.ResponseWriter, r *http.Request, id occupantIdentity) {
	runID := strings.TrimSpace(r.PathValue("runId"))
	run, err := m.store.DriverRuns().Get(r.Context(), id.claims.WorkspaceKey, runID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		writeDispatchError(w, err)
		return
	}
	if err != nil || !occupantOwnsRun(run, id.claims) {
		writeRunNotFound(w)
		return
	}
	writeJSON(w, http.StatusOK, runStatusResponse{Success: true, Data: newRunStatusView(run)})
}

func writeRunNotFound(w http.ResponseWriter) {
	writeDataError(w, http.StatusNotFound, "not_found", "driver run not found")
}

func occupantOwnsRun(run *domain.DriverRun, claims *leadtoken.OccupantClaims) bool {
	return run != nil &&
		run.WorkspaceKey == claims.WorkspaceKey &&
		run.SourceKind == occupantRunSourceKind &&
		run.SourceRef == leadtoken.OccupantActor(claims.PlacementID)
}

func newRunStatusView(run *domain.DriverRun) runStatusView {
	return runStatusView{
		RunID: run.RunID, EpicID: run.EpicID, Status: run.Status,
		Terminal: run.Status.IsTerminal(), Summary: run.Summary,
		ErrorClass: run.ErrorClass, StartedAt: timePtr(run.StartedAt),
		FinishedAt: cloneTimePtr(run.FinishedAt),
	}
}

func decodeEpicRunParams(body io.Reader) (epicRunDispatchParams, error) {
	var params epicRunDispatchParams
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&params); err != nil {
		return params, epicRunBodyError(err, "decode epic-run dispatch params: "+err.Error())
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return params, epicRunBodyError(err, "epic-run dispatch body must be a single JSON object")
	}
	return params, nil
}

func epicRunBodyError(err error, invalidMessage string) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return newStatusError(http.StatusRequestEntityTooLarge, "too_large",
			"epic-run dispatch body exceeds 1MB", false)
	}
	return newStatusError(http.StatusBadRequest, "invalid", invalidMessage, false)
}

func writeDispatchError(w http.ResponseWriter, err error) {
	var statusErr *opStatusError
	if errors.As(err, &statusErr) {
		writeDataError(w, statusErr.status, statusErr.code, statusErr.message)
		return
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeDataError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, domain.ErrInvalid):
		slog.Warn("occupant epic-run dispatch invalid", "err", err)
		writeDataError(w, http.StatusBadRequest, "invalid", "invalid epic-run request")
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrAlreadyExists):
		slog.Warn("occupant epic-run dispatch conflict", "err", err)
		writeDataError(w, http.StatusConflict, "conflict", "epic-run conflict")
	default:
		slog.Error("occupant epic-run dispatch failed", "err", err)
		writeDataError(w, http.StatusInternalServerError, "internal", "epic-run dispatch failed")
	}
}
