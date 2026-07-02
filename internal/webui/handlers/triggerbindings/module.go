// Package triggerbindings exposes a webui HTTP surface for creating, listing
// and enabling/disabling trigger bindings. Until now bindings were CLI-only
// (`loom trigger bindings create`), so "turn on code review for this repo" was
// not self-serve. This module mirrors the CLI's binding-create behavior over
// HTTP so the Automations UI can manage event-driven workflows.
package triggerbindings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

const maxBindingBodyBytes = 1 << 20

type Module struct {
	store store.Store
}

func NewModule(st store.Store) *Module {
	return &Module{store: st}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.store == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/trigger-bindings", m.listBindings)
	mux.HandleFunc("POST /api/workspaces/{ws}/trigger-bindings", m.createBinding)
	// Enable/disable are modeled as action sub-resources (POST .../enable),
	// which carry no request body.
	mux.HandleFunc("POST /api/workspaces/{ws}/trigger-bindings/{id}/enable", m.setEnabled(true))
	mux.HandleFunc("POST /api/workspaces/{ws}/trigger-bindings/{id}/disable", m.setEnabled(false))
}

type createBindingRequest struct {
	// One of Workflow (builtin/registered workflow name) or DriverID is required.
	Workflow          string   `json:"workflow,omitempty"`
	DriverID          string   `json:"driver_id,omitempty"`
	DriverVersionID   string   `json:"driver_version_id,omitempty"`
	RouteKey          string   `json:"route_key"`
	SourceKind        string   `json:"source_kind,omitempty"`
	Name              string   `json:"name,omitempty"`
	BindingID         string   `json:"binding_id,omitempty"`
	Secret            string   `json:"secret,omitempty"`
	Entrypoint        string   `json:"entrypoint,omitempty"`
	EventTypePatterns []string `json:"event_type_patterns,omitempty"`
	Enabled           *bool    `json:"enabled,omitempty"`
	// Schedule is a 5-field cron expression, required when SourceKind is "cron"
	// (the store + CronScheduler support it; this HTTP surface previously dropped it).
	Schedule         string `json:"schedule,omitempty"`
	ScheduleTimezone string `json:"schedule_timezone,omitempty"`
}

func (m *Module) listBindings(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimSpace(r.PathValue("ws"))
	bindings, err := m.store.TriggerBindings().List(r.Context(), ws, store.TriggerBindingFilter{})
	if err != nil {
		handler.WriteDomainError(w, err, "list trigger bindings failed")
		return
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"bindings": bindings})
}

func (m *Module) createBinding(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimSpace(r.PathValue("ws"))
	if ws == "" {
		handler.RespondError(w, http.StatusBadRequest, "workspace is required")
		return
	}
	var req createBindingRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBindingBodyBytes)).Decode(&req); err != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	source := strings.TrimSpace(req.SourceKind)
	if source == "" {
		source = "github"
	}
	routeKey := strings.TrimSpace(req.RouteKey)
	bindingID := strings.TrimSpace(req.BindingID)
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	// An enabled github binding must carry a signing secret, else inbound
	// webhook HMAC verification always fails and the binding never fires.
	if source == "github" && enabled && strings.TrimSpace(req.Secret) == "" {
		handler.RespondError(w, http.StatusBadRequest, "secret is required to enable a github trigger binding")
		return
	}
	// A cron binding needs a schedule, else the scheduler's cron parse fails and
	// the binding never fires. Lets the UI stand up a scheduled S1/S2 agent.
	schedule := strings.TrimSpace(req.Schedule)
	if source == store.CronSourceKind && schedule == "" {
		handler.RespondError(w, http.StatusBadRequest, "schedule is required for a cron trigger binding")
		return
	}

	// route_key is a binding's unique routing ADDRESS: the scheduler stamps it on
	// each cron.tick and the router resolves it 1:1 (GetByRouteKey), so two
	// bindings can never share one. Event sources carry a meaningful external
	// route (e.g. github.pull_request.opened) and must supply it. A cron binding
	// has no external route — it fires by schedule — so route_key is optional: the
	// store derives it from the (unique) binding_id (TriggerBindingCreate.
	// WithDerivedRoute), so here a cron binding only needs a binding_id.
	if source == store.CronSourceKind {
		if bindingID == "" && routeKey == "" {
			handler.RespondError(w, http.StatusBadRequest, "binding_id is required for a cron trigger binding")
			return
		}
	} else if routeKey == "" {
		handler.RespondError(w, http.StatusBadRequest, "route_key is required")
		return
	}
	if bindingID == "" {
		bindingID = store.DefaultBindingID(routeKey)
	}

	// Fast idempotent path: an already-provisioned binding is returned untouched
	// (200), so re-activating the same template is safe — and the builtin
	// self-heal in resolveBindingDriver below is skipped. Changing an existing
	// binding's schedule is an update (enable/disable sub-resources), not a
	// re-create.
	fetch := func() (*domain.TriggerBinding, bool) {
		b, err := m.store.TriggerBindings().Get(r.Context(), ws, bindingID)
		return b, err == nil && b != nil
	}
	if handler.WriteExistingIfFound(w, fetch) {
		return
	}

	driverID, versionID, err := m.resolveBindingDriver(r.Context(), ws, req)
	if err != nil {
		handler.WriteDomainError(w, err, err.Error())
		return
	}

	binding, err := m.store.TriggerBindings().Create(r.Context(), store.TriggerBindingCreate{
		WorkspaceKey:      ws,
		BindingID:         bindingID,
		Name:              firstNonEmpty(strings.TrimSpace(req.Name), routeKey, bindingID),
		SourceKind:        source,
		RouteKey:          routeKey,
		EventTypePatterns: req.EventTypePatterns,
		DriverID:          driverID,
		DriverVersionID:   versionID,
		TargetEntrypoint:  strings.TrimSpace(req.Entrypoint),
		WebhookSecret:     req.Secret,
		Enabled:           enabled,
		Schedule:          schedule,
		ScheduleTimezone:  strings.TrimSpace(req.ScheduleTimezone),
	})
	handler.WriteCreatedOrExisting(w, binding, err, fetch, "create trigger binding failed")
}

// resolveBindingDriver turns a workflow name (or explicit driver id) into a
// (driverID, versionID) pair, self-healing builtin workflows on demand the same
// way the workflow-run path does.
func (m *Module) resolveBindingDriver(ctx context.Context, ws string, req createBindingRequest) (string, string, error) {
	driverID := strings.TrimSpace(req.DriverID)
	workflow := strings.TrimSpace(req.Workflow)
	if driverID == "" && workflow == "" {
		return "", "", fmt.Errorf("one of workflow or driver_id is required: %w", domain.ErrInvalid)
	}
	// Resolve to the driver record in one fetch: Get by explicit id, else
	// self-heal the builtin and resolve by workflow name (ResolveDriver returns
	// the record, so no second Get is needed for ActiveVersionID below).
	var driver *domain.Driver
	var err error
	if driverID != "" {
		driver, err = m.store.Drivers().Get(ctx, ws, driverID)
	} else {
		driver, err = workflowdefs.EnsureAndResolveDriver(ctx, m.store, ws, workflow)
	}
	if err != nil {
		return "", "", err
	}
	versionID := strings.TrimSpace(req.DriverVersionID)
	if versionID == "" {
		versionID = strings.TrimSpace(driver.ActiveVersionID)
	}
	if versionID == "" {
		return "", "", fmt.Errorf("driver %q has no active version; activate one first: %w", driver.DriverID, domain.ErrInvalid)
	}
	return driver.DriverID, versionID, nil
}

func (m *Module) setEnabled(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws := strings.TrimSpace(r.PathValue("ws"))
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			handler.RespondError(w, http.StatusBadRequest, "binding id is required")
			return
		}
		flag := enabled
		binding, err := m.store.TriggerBindings().Update(r.Context(), ws, id, store.TriggerBindingUpdate{Enabled: &flag})
		if err != nil {
			handler.WriteDomainError(w, err, "update trigger binding failed")
			return
		}
		handler.WriteJSON(w, http.StatusOK, binding)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}
