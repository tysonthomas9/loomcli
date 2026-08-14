// Package agentservices exposes read-only durable background-agent identity
// and run-history projections for the WebUI.
package agentservices

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

const (
	defaultRunsLimit = 20
	maxRunsLimit     = 1000
	healthScanLimit  = 100
)

type Module struct {
	store store.Store
}

func NewModule(st store.Store) *Module { return &Module{store: st} }

func (m *Module) Register(mux *http.ServeMux) {
	if m.store == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/agent-services", m.listAgentServices)
	mux.HandleFunc("GET /api/workspaces/{ws}/agent-services/{id}/runs", m.listAgentServiceRuns)
}

type agentServiceDTO struct {
	ID                  string                   `json:"id"`
	Name                string                   `json:"name"`
	Kind                string                   `json:"kind"`
	Enabled             bool                     `json:"enabled"`
	Behavior            agentServiceBehaviorDTO  `json:"behavior"`
	Bindings            []agentServiceBindingDTO `json:"bindings"`
	NextFireAt          *time.Time               `json:"nextFireAt"`
	LastRunStatus       string                   `json:"lastRunStatus"`
	ConsecutiveFailures int                      `json:"consecutiveFailures"`
	Errors              []string                 `json:"errors"`
	CreatedAt           time.Time                `json:"createdAt"`
	UpdatedAt           time.Time                `json:"updatedAt"`
}

type agentServiceBehaviorDTO struct {
	RoleName        string `json:"roleName,omitempty"`
	DriverID        string `json:"driverId,omitempty"`
	DriverVersionID string `json:"driverVersionId,omitempty"`
}

type agentServiceBindingDTO struct {
	ID         string `json:"id"`
	SourceKind string `json:"sourceKind"`
	Schedule   string `json:"schedule"`
	Enabled    bool   `json:"enabled"`
	RouteKey   string `json:"routeKey"`
}

type driverRunDTO struct {
	WorkspaceKey        string                 `json:"workspaceKey"`
	RunID               string                 `json:"runId"`
	DriverID            string                 `json:"driverId"`
	DriverVersionID     string                 `json:"driverVersionId"`
	Entrypoint          string                 `json:"entrypoint,omitempty"`
	SourceKind          string                 `json:"sourceKind,omitempty"`
	SourceRef           string                 `json:"sourceRef,omitempty"`
	EpicID              string                 `json:"epicId,omitempty"`
	TriggerBindingID    string                 `json:"triggerBindingId,omitempty"`
	AgentServiceID      string                 `json:"agentServiceId,omitempty"`
	Status              domain.DriverRunStatus `json:"status"`
	NodeID              string                 `json:"nodeId,omitempty"`
	LeaseID             string                 `json:"leaseId,omitempty"`
	FencingToken        int64                  `json:"fencingToken,omitempty"`
	IdempotencyKey      string                 `json:"idempotencyKey,omitempty"`
	Payload             json.RawMessage        `json:"payload,omitempty"`
	Output              map[string]string      `json:"output,omitempty"`
	Summary             string                 `json:"summary,omitempty"`
	ErrorClass          string                 `json:"errorClass,omitempty"`
	StartedAt           time.Time              `json:"startedAt,omitempty"`
	LastHeartbeat       time.Time              `json:"lastHeartbeat,omitempty"`
	FinishedAt          *time.Time             `json:"finishedAt,omitempty"`
	ParentRunID         string                 `json:"parentRunId,omitempty"`
	SuspendedAt         *time.Time             `json:"suspendedAt,omitempty"`
	CancelRequestedAt   *time.Time             `json:"cancelRequestedAt,omitempty"`
	CancelRequestReason string                 `json:"cancelRequestedReason,omitempty"`
	ResumeSourceEventID string                 `json:"resumeSourceEventId,omitempty"`
	CreatedAt           time.Time              `json:"createdAt"`
	UpdatedAt           time.Time              `json:"updatedAt"`
}

func (m *Module) listAgentServices(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimSpace(r.PathValue("ws"))
	if ws == "" {
		handler.RespondError(w, http.StatusBadRequest, "workspace is required")
		return
	}
	services, err := m.store.AgentServices().List(r.Context(), ws, store.AgentServiceFilter{})
	if err != nil {
		writeStoreError(w, err, "list agent services failed")
		return
	}
	items := make([]agentServiceDTO, 0, len(services))
	for _, svc := range services {
		if svc == nil || svc.DeletedAt != nil {
			continue
		}
		items = append(items, m.decorateAgentService(r, ws, svc))
	}
	handler.WriteJSON(w, http.StatusOK, dto.NewListResponse(items, len(items)))
}

func (m *Module) decorateAgentService(r *http.Request, ws string, svc *domain.AgentService) agentServiceDTO {
	out := agentServiceDTO{
		ID: svc.ServiceID, Name: svc.Name, Kind: deriveAgentServiceKind(svc),
		Enabled:  svc.DesiredState == domain.AgentServiceDesiredRunning,
		Behavior: agentServiceBehaviorDTO{RoleName: svc.RoleName, DriverID: svc.DriverID, DriverVersionID: svc.DriverVersionID},
		Bindings: []agentServiceBindingDTO{}, Errors: []string{}, CreatedAt: svc.CreatedAt, UpdatedAt: svc.UpdatedAt,
	}
	bindings, err := m.store.TriggerBindings().List(r.Context(), ws, store.TriggerBindingFilter{TargetAgentServiceID: svc.ServiceID})
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("binding health unavailable: %v", err))
	} else {
		decorateAgentServiceBindings(&out, bindings, time.Now())
	}
	runs, err := m.store.DriverRuns().List(r.Context(), ws, store.DriverRunFilter{AgentServiceID: svc.ServiceID, Limit: healthScanLimit})
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("run health unavailable: %v", err))
	} else {
		out.LastRunStatus, out.ConsecutiveFailures = driverRunHealth(runs)
	}
	return out
}

func decorateAgentServiceBindings(out *agentServiceDTO, bindings []*domain.TriggerBinding, now time.Time) {
	for _, binding := range bindings {
		if binding == nil {
			out.Errors = append(out.Errors, "binding health unavailable: empty binding record")
			continue
		}
		out.Bindings = append(out.Bindings, agentServiceBindingDTO{
			ID: binding.BindingID, SourceKind: binding.SourceKind, Schedule: binding.Schedule,
			Enabled: binding.Enabled, RouteKey: binding.RouteKey,
		})
		if !binding.Enabled || binding.SourceKind != trigger.CronSourceKind || strings.TrimSpace(binding.Schedule) == "" {
			continue
		}
		next, err := trigger.NextFire(binding.Schedule, binding.ScheduleTimezone, now)
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("binding %s next fire unavailable: %v", binding.BindingID, err))
			continue
		}
		if out.NextFireAt == nil || next.Before(*out.NextFireAt) {
			out.NextFireAt = &next
		}
	}
}

func deriveAgentServiceKind(svc *domain.AgentService) string {
	if svc != nil && (strings.TrimSpace(svc.DriverID) != "" || strings.TrimSpace(svc.DriverVersionID) != "") {
		return "scripted"
	}
	if svc != nil && strings.TrimSpace(svc.RoleName) != "" {
		return "prompt"
	}
	return "unknown"
}

func driverRunHealth(runs []*domain.DriverRun) (string, int) {
	sortDriverRunsNewestFirst(runs)
	if len(runs) == 0 || runs[0] == nil {
		return "", 0
	}
	lastStatus := string(runs[0].Status)
	failures := 0
	for _, run := range runs {
		if run == nil {
			continue
		}
		switch run.Status {
		case domain.DriverRunFailed:
			failures++
		case domain.DriverRunQueued, domain.DriverRunRunning, domain.DriverRunSuspendedAwaitingEvent:
			continue
		default:
			return lastStatus, failures
		}
	}
	return lastStatus, failures
}

func (m *Module) listAgentServiceRuns(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimSpace(r.PathValue("ws"))
	serviceID := strings.TrimSpace(r.PathValue("id"))
	if ws == "" || serviceID == "" {
		handler.RespondError(w, http.StatusBadRequest, "workspace and agent service id are required")
		return
	}
	if _, err := m.store.AgentServices().Get(r.Context(), ws, serviceID); err != nil {
		writeStoreError(w, err, "agent service not found")
		return
	}
	limit, err := parseRunsLimit(r)
	if err != nil {
		handler.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}
	runs, err := m.store.DriverRuns().List(r.Context(), ws, store.DriverRunFilter{AgentServiceID: serviceID, Limit: limit})
	if err != nil {
		writeStoreError(w, err, "list agent service runs failed")
		return
	}
	sortDriverRunsNewestFirst(runs)
	if len(runs) > limit {
		runs = runs[:limit]
	}
	items := make([]driverRunDTO, 0, len(runs))
	for _, run := range runs {
		if run != nil {
			items = append(items, newDriverRunDTO(run))
		}
	}
	handler.WriteJSON(w, http.StatusOK, dto.NewListResponse(items, len(items)))
}

func parseRunsLimit(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return defaultRunsLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maxRunsLimit {
		return 0, fmt.Errorf("invalid limit: must be 1-%d", maxRunsLimit)
	}
	return limit, nil
}

func sortDriverRunsNewestFirst(runs []*domain.DriverRun) {
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i] == nil {
			return false
		}
		if runs[j] == nil {
			return true
		}
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].RunID > runs[j].RunID
		}
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
}

func newDriverRunDTO(run *domain.DriverRun) driverRunDTO {
	return driverRunDTO{
		WorkspaceKey: run.WorkspaceKey, RunID: run.RunID, DriverID: run.DriverID, DriverVersionID: run.DriverVersionID,
		Entrypoint: run.Entrypoint, SourceKind: run.SourceKind, SourceRef: run.SourceRef, EpicID: run.EpicID,
		TriggerBindingID: run.TriggerBindingID, AgentServiceID: run.AgentServiceID, Status: run.Status,
		NodeID: run.NodeID, LeaseID: run.LeaseID, FencingToken: run.FencingToken, IdempotencyKey: run.IdempotencyKey,
		Payload: run.Payload, Output: run.Output, Summary: run.Summary, ErrorClass: run.ErrorClass,
		StartedAt: run.StartedAt, LastHeartbeat: run.LastHeartbeat, FinishedAt: run.FinishedAt,
		ParentRunID: run.ParentRunID, SuspendedAt: run.SuspendedAt, CancelRequestedAt: run.CancelRequestedAt,
		CancelRequestReason: run.CancelRequestedReason, ResumeSourceEventID: run.ResumeSourceEventID,
		CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}
}

func writeStoreError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		handler.RespondError(w, http.StatusNotFound, fallback)
	case errors.Is(err, domain.ErrInvalid):
		handler.RespondError(w, http.StatusBadRequest, err.Error())
	default:
		handler.RespondError(w, http.StatusInternalServerError, fallback)
	}
}
