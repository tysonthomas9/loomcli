// Package triggerbindings exposes a webui HTTP surface for creating, listing
// and enabling/disabling trigger bindings. Until now bindings were CLI-only
// (`loom trigger bindings create`), so "turn on code review for this repo" was
// not self-serve. This module mirrors the CLI's binding-create behavior over
// HTTP so the Automations UI can manage event-driven workflows.
package triggerbindings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/runhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

// bindingRunScanLimit bounds the per-binding run scan used to compute failure
// health for the list view. N+1 over bindings is acceptable at local-mode
// scale; the tradeoff is documented on bindingRunHealth.
const bindingRunScanLimit = 20

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
	// PATCH edits name/schedule/timezone; DELETE removes the binding and revokes
	// its connector grants (Decision 6).
	mux.HandleFunc("PATCH /api/workspaces/{ws}/trigger-bindings/{id}", m.patchBinding)
	mux.HandleFunc("DELETE /api/workspaces/{ws}/trigger-bindings/{id}", m.deleteBinding)
	// Enable/disable are modeled as action sub-resources (POST .../enable),
	// which carry no request body.
	mux.HandleFunc("POST /api/workspaces/{ws}/trigger-bindings/{id}/enable", m.setEnabled(true))
	mux.HandleFunc("POST /api/workspaces/{ws}/trigger-bindings/{id}/disable", m.setEnabled(false))
	// Binding-scoped manual run (config-by-reference): creates a DriverRun for the
	// binding's driver, STAMPED with the binding, carrying NO client-supplied
	// run-input. The run reads its own config via loom.binding.config().
	mux.HandleFunc("POST /api/workspaces/{ws}/trigger-bindings/{id}/run", m.runBinding)
	mux.HandleFunc("GET /api/workspaces/{ws}/trigger-bindings/{id}/runs", m.listBindingRuns)
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
	// RunInput is per-binding static run-input the dispatch source merges under
	// each fired run's payload (see trigger.BindingRunInput): e.g. a prompt
	// agent's {"roleName":"docs-assistant","backend":"codex"}. It is stored on
	// the binding's free-form source_config_ref (fleet-db round-trips it
	// untouched — no schema change). SourceConfigRef, when set directly, takes
	// precedence for callers (CLI) that already hold an encoded value.
	RunInput        json.RawMessage `json:"run_input,omitempty"`
	SourceConfigRef string          `json:"source_config_ref,omitempty"`
}

// resolveSourceConfigRef picks the binding's source_config_ref: an explicit
// value wins; otherwise a run_input JSON OBJECT is serialized into it so the
// dispatch source can merge it into fired runs. A run_input that is not a JSON
// object is ignored (nothing to merge), leaving source_config_ref empty.
func (req createBindingRequest) resolveSourceConfigRef() string {
	if ref := strings.TrimSpace(req.SourceConfigRef); ref != "" {
		return ref
	}
	raw := strings.TrimSpace(string(req.RunInput))
	if raw == "" || raw[0] != '{' {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil || len(obj) == 0 {
		return ""
	}
	return raw
}

// bindingWithNextFire decorates a binding with its computed next cron fire
// instant plus run-failure health for the list view. The embedded binding's
// fields marshal at the top level; next_fire_at is populated only for enabled
// schedule-driven bindings, and last_run_status / consecutive_failures drive
// the sidebar failure dot (Decision 7).
type bindingWithNextFire struct {
	*domain.TriggerBinding
	NextFireAt *time.Time `json:"next_fire_at,omitempty"`
	// LastRunStatus is the newest run's status (incl. queued/running) for
	// display; ConsecutiveFailures counts failed runs from newest until the
	// first non-failed terminal run (Decision 7: 1 → amber, 2+ → red "failing").
	LastRunStatus       string `json:"last_run_status,omitempty"`
	ConsecutiveFailures int    `json:"consecutive_failures,omitempty"`
}

type BindingDecorators struct {
	NextFireAt          *time.Time `json:"next_fire_at,omitempty"`
	LastRunStatus       string     `json:"last_run_status,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures,omitempty"`
}

type DeleteBindingResult struct {
	BindingID     string `json:"binding_id"`
	Deleted       bool   `json:"deleted"`
	GrantsRevoked int    `json:"grants_revoked"`
}

func (m *Module) listBindings(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimSpace(r.PathValue("ws"))
	bindings, err := m.store.TriggerBindings().List(r.Context(), ws, store.TriggerBindingFilter{})
	if err != nil {
		handler.WriteDomainError(w, err, "list trigger bindings failed")
		return
	}
	now := time.Now()
	out := make([]bindingWithNextFire, 0, len(bindings))
	for _, b := range bindings {
		decorators := DecorateBinding(r.Context(), m.store, ws, b, now)
		out = append(out, bindingWithNextFire{
			TriggerBinding:      b,
			NextFireAt:          decorators.NextFireAt,
			LastRunStatus:       decorators.LastRunStatus,
			ConsecutiveFailures: decorators.ConsecutiveFailures,
		})
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"bindings": out})
}

func DecorateBinding(ctx context.Context, st store.Store, ws string, b *domain.TriggerBinding, now time.Time) BindingDecorators {
	lastStatus, consecutiveFailures := bindingRunHealth(ctx, st, ws, b)
	return BindingDecorators{
		NextFireAt:          nextFireFor(b, now),
		LastRunStatus:       lastStatus,
		ConsecutiveFailures: consecutiveFailures,
	}
}

// bindingRunHealth computes a binding's failure health from ITS OWN runs.
//
// It lists the runs a trigger-dispatch leg stamped with this binding id
// (BindingID filter, not driver id — bindings that share a driver no longer
// bleed each other's failures), newest-first by the shared run order, scanning
// only the newest bindingRunScanLimit. last_run_status is the newest run's
// status (queued/running included, for display); consecutive_failures counts
// failed runs from newest until the first non-failed TERMINAL run, skipping
// still-in-flight (queued/running/suspended) runs — a pending run is not yet an
// outcome and must not reset or extend the failure streak.
//
// The scan cap is pushed into the store as a Limit: both backends order
// newest-first by StartedAt before limiting, so this fetches only the newest
// bindingRunScanLimit runs rather than the whole history. The client-side sort
// below stays as defense in depth against an unordered backend.
func bindingRunHealth(ctx context.Context, st store.Store, ws string, b *domain.TriggerBinding) (lastStatus string, consecutiveFailures int) {
	// Cheap guard: an unsaved/identifier-less binding has no runs to scan.
	if st == nil || b == nil || strings.TrimSpace(b.BindingID) == "" {
		return "", 0
	}
	runs, err := st.DriverRuns().List(ctx, ws, store.DriverRunFilter{
		BindingID: b.BindingID,
		Limit:     bindingRunScanLimit,
	})
	if err != nil || len(runs) == 0 {
		return "", 0
	}
	store.SortDriverRunsNewestFirst(runs)
	if len(runs) > bindingRunScanLimit {
		runs = runs[:bindingRunScanLimit]
	}
	lastStatus = string(runs[0].Status)
	for _, run := range runs {
		switch run.Status {
		case domain.DriverRunFailed:
			consecutiveFailures++
		case domain.DriverRunQueued, domain.DriverRunRunning, domain.DriverRunSuspendedAwaitingEvent:
			continue // in-flight: not yet an outcome, neither breaks nor extends the streak
		default:
			// completed / needs_review / cancelled — a clean run breaks the streak.
			return lastStatus, consecutiveFailures
		}
	}
	return lastStatus, consecutiveFailures
}

// nextFireFor computes a binding's next cron tick, or nil when it is not an
// enabled schedule-driven binding. A malformed schedule/timezone yields nil
// rather than an error: one bad binding must not fail the whole list.
func nextFireFor(b *domain.TriggerBinding, now time.Time) *time.Time {
	if b == nil || !b.Enabled || b.SourceKind != store.CronSourceKind || strings.TrimSpace(b.Schedule) == "" {
		return nil
	}
	next, err := trigger.NextFire(b.Schedule, b.ScheduleTimezone, now)
	if err != nil {
		return nil
	}
	return &next
}

func (m *Module) createBinding(w http.ResponseWriter, r *http.Request) { //nolint:cyclop,funlen // Request validation is intentionally linear.
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
	// bindings can never share one. External event sources carry a meaningful
	// external route (e.g. github.pull_request.opened) and must supply it. A cron
	// binding (fires by schedule) and an internal-event binding (matches by
	// event_type_patterns, e.g. internal.task.ready) have no external route to
	// own, so route_key is optional for them: the store derives a unique address
	// from the (unique) binding_id (TriggerBindingCreate.WithDerivedRoute). This
	// is what lets several prompt-agent bindings pattern-match the SAME event
	// route without fighting over its 1:1 exact-owner slot.
	switch source {
	case store.CronSourceKind:
		if bindingID == "" && routeKey == "" {
			handler.RespondError(w, http.StatusBadRequest, "binding_id is required for a cron trigger binding")
			return
		}
	case store.InternalSourceKind:
		if bindingID == "" && routeKey == "" {
			handler.RespondError(w, http.StatusBadRequest, "binding_id or route_key is required for an internal trigger binding")
			return
		}
	default:
		if routeKey == "" {
			handler.RespondError(w, http.StatusBadRequest, "route_key is required")
			return
		}
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
		SourceConfigRef:   req.resolveSourceConfigRef(),
	})
	// Plain binding, no computed next_fire_at (list-only for now).
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
		existing, err := m.store.TriggerBindings().Get(r.Context(), ws, id)
		if err != nil {
			handler.WriteDomainError(w, err, "get trigger binding failed")
			return
		}
		if agentID := strings.TrimSpace(existing.TargetAgentServiceID); agentID != "" {
			handler.RespondError(w, http.StatusConflict, "managed by agent "+agentID)
			return
		}
		flag := enabled
		binding, err := m.store.TriggerBindings().Update(r.Context(), ws, id, store.TriggerBindingUpdate{Enabled: &flag})
		if err != nil {
			handler.WriteDomainError(w, err, "update trigger binding failed")
			return
		}
		// Plain binding, no computed next_fire_at (list-only for now).
		handler.WriteJSON(w, http.StatusOK, binding)
	}
}

// runBinding creates a DriverRun for a binding's driver on demand ("Run now"),
// stamping the binding on the run so config resolves BY REFERENCE at run start
// (the binding-config driver op). This is the manual counterpart to a scheduled
// fire, and the reason the frontend Run-now no longer merges the binding's
// run-input into the payload: the run reads its own config from provenance.
//
// The run is stamped two ways so the binding resolves regardless of whether the
// backing store's run-create persists trigger_binding_id yet: (1) TriggerBindingID
// directly (the canonical field), and (2) the binding's route key as SourceRef,
// which the driver-op provenance lookup (lookupParentBindingID) also resolves. It
// runs the binding's PINNED driver version (binding.DriverVersionID — the same
// version a scheduled fire dispatches) so run-now faithfully reproduces a fire.
// It deliberately carries NO run-input: config travels by reference now.
func (m *Module) runBinding(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimSpace(r.PathValue("ws"))
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		handler.RespondError(w, http.StatusBadRequest, "binding id is required")
		return
	}
	binding, err := m.store.TriggerBindings().Get(r.Context(), ws, id)
	if err != nil {
		handler.WriteDomainError(w, err, "get trigger binding failed")
		return
	}
	run, err := driver.CreateDriverRun(r.Context(), m.store, driver.RunOptions{
		WorkspaceKey:     ws,
		DriverID:         binding.DriverID,
		DriverVersionID:  binding.DriverVersionID,
		TriggerBindingID: binding.BindingID,
		SourceKind:       "binding-run",
		SourceRef:        firstNonEmpty(binding.RouteKey, binding.BindingID),
		IdempotencyKey:   strings.TrimSpace(r.Header.Get("Idempotency-Key")),
	})
	if err != nil {
		handler.WriteDomainError(w, err, "run trigger binding failed")
		return
	}
	handler.WriteJSON(w, http.StatusAccepted, run)
}

// listBindingRuns is the agent-owned run history surface. It filters by
// trigger_binding_id only; unattributed runs from bare `loom workflow run`
// calls stay on the workflow-scoped runs endpoint and never appear here.
func (m *Module) listBindingRuns(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimSpace(r.PathValue("ws"))
	id := strings.TrimSpace(r.PathValue("id"))
	if ws == "" || id == "" {
		handler.RespondError(w, http.StatusBadRequest, "workspace and binding id are required")
		return
	}
	binding, err := m.store.TriggerBindings().Get(r.Context(), ws, id)
	if err != nil {
		handler.WriteDomainError(w, err, "get trigger binding failed")
		return
	}
	limit, ok := runhistory.ParseRunLimit(w, r)
	if !ok {
		return
	}
	runs, err := m.store.DriverRuns().List(r.Context(), ws, store.DriverRunFilter{
		BindingID: binding.BindingID,
		Limit:     limit,
	})
	if err != nil {
		handler.WriteDomainError(w, err, "list trigger binding runs failed")
		return
	}
	runs = runhistory.SortAndTrim(runs, limit)
	// Agent-scoped envelope, deliberately NOT driver-rooted (see
	// docs/design/2026-07-07-agent-identity-record.md §4.3): the driver is an
	// implementation detail carried per-run and on the binding record the
	// caller already holds — this surface later becomes /agents/{id}/runs.
	handler.WriteJSON(w, http.StatusOK, map[string]any{
		"binding_id": binding.BindingID,
		"runs":       runs,
	})
}

// updateBindingRequest is the PATCH body: only the operator-editable fields are
// accepted (rename + reschedule). Pointer fields distinguish "absent" (leave
// unchanged) from an explicit value.
type updateBindingRequest struct {
	Name             *string `json:"name,omitempty"`
	Schedule         *string `json:"schedule,omitempty"`
	ScheduleTimezone *string `json:"schedule_timezone,omitempty"`
}

// patchBinding edits a binding's name and/or cron schedule. Schedule and
// timezone changes are rejected on non-cron bindings (400) and validated with
// the same cron/timezone grammar the scheduler enforces.
//
// CronScheduler cache note: the scheduler caches only a per-binding WINDOW start
// (lastTick, keyed ws|bindingID), never a parsed schedule — each sweep re-reads
// binding.Schedule fresh (cron.go sweepBinding → parseCronSchedule). So a
// schedule change self-corrects on the next sweep with no cache invalidation:
// the new schedule's next fire is computed from the existing window start.
func (m *Module) patchBinding(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimSpace(r.PathValue("ws"))
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		handler.RespondError(w, http.StatusBadRequest, "binding id is required")
		return
	}
	var req updateBindingRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBindingBodyBytes)).Decode(&req); err != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Load the binding first: source-kind rules (schedule only on cron) need the
	// stored kind, and a missing binding must 404 before any mutation.
	existing, err := m.store.TriggerBindings().Get(r.Context(), ws, id)
	if err != nil {
		handler.WriteDomainError(w, err, "get trigger binding failed")
		return
	}
	patch, ok := m.buildBindingPatch(w, existing, req)
	if !ok {
		return
	}
	updated, err := m.store.TriggerBindings().Update(r.Context(), ws, id, patch)
	if err != nil {
		handler.WriteDomainError(w, err, "update trigger binding failed")
		return
	}
	handler.WriteJSON(w, http.StatusOK, bindingWithNextFire{TriggerBinding: updated, NextFireAt: nextFireFor(updated, time.Now())})
}

// buildBindingPatch validates the PATCH request against the stored binding and
// assembles the store patch. On a validation failure it writes the error and
// returns ok=false.
func (m *Module) buildBindingPatch(w http.ResponseWriter, existing *domain.TriggerBinding, req updateBindingRequest) (store.TriggerBindingUpdate, bool) {
	patch := store.TriggerBindingUpdate{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			handler.RespondError(w, http.StatusBadRequest, "name cannot be empty")
			return patch, false
		}
		patch.Name = &name
	}
	if req.Schedule != nil {
		if existing.SourceKind != store.CronSourceKind {
			handler.RespondError(w, http.StatusBadRequest, "schedule can only be changed on a cron trigger binding")
			return patch, false
		}
		schedule := strings.TrimSpace(*req.Schedule)
		if err := trigger.ValidateSchedule(schedule); err != nil {
			handler.RespondError(w, http.StatusBadRequest, err.Error())
			return patch, false
		}
		patch.Schedule = &schedule
	}
	if req.ScheduleTimezone != nil {
		if existing.SourceKind != store.CronSourceKind {
			handler.RespondError(w, http.StatusBadRequest, "schedule_timezone can only be changed on a cron trigger binding")
			return patch, false
		}
		tz := strings.TrimSpace(*req.ScheduleTimezone)
		if err := trigger.ValidateScheduleTimezone(tz); err != nil {
			handler.RespondError(w, http.StatusBadRequest, err.Error())
			return patch, false
		}
		patch.ScheduleTimezone = &tz
	}
	if patch.Name == nil && patch.Schedule == nil && patch.ScheduleTimezone == nil {
		handler.RespondError(w, http.StatusBadRequest, "no fields to update: pass name, schedule, or schedule_timezone")
		return patch, false
	}
	return patch, true
}

// deleteBinding removes a binding and revokes its connector grants (Decision 6:
// no orphaned credentials). Ordering is deliberate: delete FIRST, revoke after.
// Delete is the gating action, so a backend that cannot delete (the fleet-db
// server currently returns 405 — see fleetdb.triggerBindingStore.Delete) fails
// with NO side effects — grants stay intact and the binding keeps working —
// rather than leaving a neutered binding with revoked grants. Only once the
// binding is truly gone are its grants revoked, so the SUCCESS path still leaves
// zero live grants. A revoke failure after a successful delete is surfaced (500)
// rather than reported as a clean delete.
func (m *Module) deleteBinding(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimSpace(r.PathValue("ws"))
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		handler.RespondError(w, http.StatusBadRequest, "binding id is required")
		return
	}
	result, err := DeleteBindingAndRevokeGrants(r.Context(), m.store, ws, id)
	if err != nil {
		if result.Deleted {
			handler.RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrInvalid) ||
			errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrAlreadyExists) {
			handler.WriteDomainError(w, err, "delete trigger binding failed")
			return
		}
		handler.RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handler.WriteJSON(w, http.StatusOK, result)
}

func DeleteBindingAndRevokeGrants(ctx context.Context, st store.Store, ws, bindingID string) (DeleteBindingResult, error) {
	result := DeleteBindingResult{BindingID: bindingID}
	if st == nil {
		return result, fmt.Errorf("store is required: %w", domain.ErrInvalid)
	}
	if err := st.TriggerBindings().Delete(ctx, ws, bindingID); err != nil {
		return result, err
	}
	result.Deleted = true
	revoked, err := RevokeBindingGrants(ctx, st, ws, bindingID)
	result.GrantsRevoked = revoked
	if err != nil {
		return result, fmt.Errorf("binding deleted but grant revocation failed: %w", err)
	}
	return result, nil
}

// revokeBindingGrants revokes every active connector grant scoped to the
// binding and returns the count revoked. ListByBinding already excludes
// already-revoked grants; a grant revoked concurrently (ErrGrantRevoked) is
// treated as success. Other revoke errors are joined and returned.
func RevokeBindingGrants(ctx context.Context, st store.Store, ws, bindingID string) (int, error) {
	if st == nil {
		return 0, fmt.Errorf("store is required: %w", domain.ErrInvalid)
	}
	grants, err := st.ConnectorGrants().ListByBinding(ctx, ws, bindingID)
	if err != nil {
		return 0, fmt.Errorf("list connector grants: %w", err)
	}
	revoked := 0
	var errs []error
	for _, g := range grants {
		if g == nil {
			continue
		}
		if err := st.ConnectorGrants().Revoke(ctx, ws, g.GrantID); err != nil {
			if errors.Is(err, domain.ErrGrantRevoked) {
				continue
			}
			errs = append(errs, fmt.Errorf("revoke grant %q: %w", g.GrantID, err))
			continue
		}
		revoked++
	}
	return revoked, errors.Join(errs...)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}
