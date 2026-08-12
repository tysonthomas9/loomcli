// Package triggerbindings exposes Automation's trigger-binding management HTTP
// surface. Product mutations cross only Automation's public ports; this adapter
// owns request decoding, operator-authority resolution, and the legacy response
// decoration needed by the current Automations UI.
package triggerbindings

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/runhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

const (
	bindingRunScanLimit = 20
	maxBindingBodyBytes = 1 << 20
)

// RunQueries is the read-only compatibility seam used for current run-history
// responses and list health decoration. It deliberately omits run creation and
// every execution lifecycle mutation.
type RunQueries interface {
	Get(ctx context.Context, workspaceKey, runID string) (*domain.DriverRun, error)
	List(ctx context.Context, workspaceKey string, filter store.DriverRunFilter) ([]*domain.DriverRun, error)
}

// ConnectorCompatibility owns the two connector concerns that cannot enter
// Automation's binding model: secret material and binding-scoped egress grants.
// Implementations must make both methods idempotent so interrupted creates and
// deletes can safely be resumed.
type ConnectorCompatibility interface {
	ConfigureBindingSecret(ctx context.Context, workspaceKey, bindingID, sourceKind, secret string) error
	RevokeBindingGrants(ctx context.Context, workspaceKey, bindingID string) (int, error)
}

// Config contains only the public Automation ports and narrow compatibility
// reads needed by this transport.
type Config struct {
	CreateWorkflow       *workflowbinding.Workflow
	Commands             automation.BindingCommands
	Queries              automation.BindingQueries
	ManualDispatch       automation.ManualDispatch
	OperatorAuthority    workflowcataloghttp.OperatorAuthorityResolver
	WorkspaceFromContext func(context.Context) string
	Runs                 RunQueries
	Connectors           ConnectorCompatibility
}

type Module struct {
	createWorkflow       *workflowbinding.Workflow
	commands             automation.BindingCommands
	queries              automation.BindingQueries
	manualDispatch       automation.ManualDispatch
	operatorAuthority    workflowcataloghttp.OperatorAuthorityResolver
	workspaceFromContext func(context.Context) string
	runs                 RunQueries
	connectors           ConnectorCompatibility
	active               bool
}

// New constructs the Automation-backed HTTP adapter. Missing individual ports
// fail closed at request time; route registration itself remains deterministic.
func New(config Config) *Module {
	return &Module{
		createWorkflow: config.CreateWorkflow,
		commands:       config.Commands, queries: config.Queries, manualDispatch: config.ManualDispatch,
		operatorAuthority: config.OperatorAuthority, workspaceFromContext: config.WorkspaceFromContext,
		runs: config.Runs, connectors: config.Connectors, active: true,
	}
}

// NewModule is retained only so older composition continues to compile while
// it migrates to New(Config). It is intentionally inert: a composite Store is
// no longer an authorized trigger-binding management dependency.
func NewModule(store.Store) *Module { return &Module{} }

func (m *Module) Register(mux *http.ServeMux) {
	if m == nil || mux == nil || !m.active {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/trigger-bindings", m.listBindings)
	mux.HandleFunc("POST /api/workspaces/{ws}/trigger-bindings", m.createBinding)
	mux.HandleFunc("PATCH /api/workspaces/{ws}/trigger-bindings/{id}", m.patchBinding)
	mux.HandleFunc("DELETE /api/workspaces/{ws}/trigger-bindings/{id}", m.deleteBinding)
	mux.HandleFunc("POST /api/workspaces/{ws}/trigger-bindings/{id}/enable", m.setEnabled(true))
	mux.HandleFunc("POST /api/workspaces/{ws}/trigger-bindings/{id}/disable", m.setEnabled(false))
	mux.HandleFunc("POST /api/workspaces/{ws}/trigger-bindings/{id}/run", m.runBinding)
	mux.HandleFunc("GET /api/workspaces/{ws}/trigger-bindings/{id}/runs", m.listBindingRuns)
}

type createBindingRequest struct {
	Workflow            string                              `json:"workflow,omitempty"`
	DriverID            string                              `json:"driver_id,omitempty"`
	DriverVersionID     string                              `json:"driver_version_id,omitempty"`
	RouteKey            string                              `json:"route_key"`
	SourceKind          string                              `json:"source_kind,omitempty"`
	Name                string                              `json:"name,omitempty"`
	BindingID           string                              `json:"binding_id,omitempty"`
	Secret              string                              `json:"secret,omitempty"`
	Entrypoint          string                              `json:"entrypoint,omitempty"`
	EventTypePatterns   []string                            `json:"event_type_patterns,omitempty"`
	SubjectKeyTemplate  string                              `json:"subject_key_template,omitempty"`
	ConcurrencyPolicy   automation.BindingConcurrencyPolicy `json:"concurrency_policy,omitempty"`
	ActorFilter         *automation.ActorFilter             `json:"actor_filter,omitempty"`
	RetryMaxAttempts    int                                 `json:"retry_max_attempts,omitempty"`
	RetryBackoffSeconds int                                 `json:"retry_backoff_seconds,omitempty"`
	Enabled             *bool                               `json:"enabled,omitempty"`
	Schedule            string                              `json:"schedule,omitempty"`
	ScheduleTimezone    string                              `json:"schedule_timezone,omitempty"`
	RunInput            json.RawMessage                     `json:"run_input,omitempty"`
	SourceConfigRef     string                              `json:"source_config_ref,omitempty"`
}

func (req createBindingRequest) resolveSourceConfigRef() string {
	if ref := strings.TrimSpace(req.SourceConfigRef); ref != "" {
		return ref
	}
	raw := strings.TrimSpace(string(req.RunInput))
	if raw == "" || raw[0] != '{' {
		return ""
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil || len(object) == 0 {
		return ""
	}
	return raw
}

type bindingWithNextFire struct {
	*automation.Binding
	NextFireAt          *time.Time `json:"next_fire_at,omitempty"`
	LastRunStatus       string     `json:"last_run_status,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures,omitempty"`
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
	workspace, ok := m.canonicalWorkspace(r)
	if !ok {
		handler.RespondError(w, http.StatusBadRequest, "workspace is required")
		return
	}
	if m.queries == nil || m.runs == nil {
		writeAutomationError(w, automation.ErrUnavailable, "list trigger bindings failed")
		return
	}
	bindings, err := m.queries.ListBindings(r.Context(), workspace, automation.BindingFilter{})
	if err != nil {
		writeAutomationError(w, err, "list trigger bindings failed")
		return
	}
	now := time.Now()
	out := make([]bindingWithNextFire, 0, len(bindings))
	for _, binding := range bindings {
		if binding == nil {
			writeAutomationError(w, automation.ErrInvalidPersistedState, "list trigger bindings failed")
			return
		}
		lastStatus, consecutiveFailures := bindingRunHealth(r.Context(), m.runs, workspace, binding.BindingID)
		out = append(out, bindingWithNextFire{
			Binding: binding, NextFireAt: nextAutomationFireFor(binding, now),
			LastRunStatus: lastStatus, ConsecutiveFailures: consecutiveFailures,
		})
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"bindings": out})
}

// DecorateBinding remains a read-only compatibility helper for the existing
// agent DTO adapter. Trigger-binding management no longer uses a Store.
func DecorateBinding(ctx context.Context, st store.Store, workspace string, binding *domain.TriggerBinding, now time.Time) BindingDecorators {
	if st == nil {
		return BindingDecorators{NextFireAt: nextLegacyFireFor(binding, now)}
	}
	lastStatus, consecutiveFailures := bindingRunHealth(ctx, st.DriverRuns(), workspace, bindingID(binding))
	return BindingDecorators{
		NextFireAt: nextLegacyFireFor(binding, now), LastRunStatus: lastStatus,
		ConsecutiveFailures: consecutiveFailures,
	}
}

func bindingID(binding *domain.TriggerBinding) string {
	if binding == nil {
		return ""
	}
	return binding.BindingID
}

func bindingRunHealth(ctx context.Context, runs RunQueries, workspace, bindingID string) (string, int) {
	if runs == nil || strings.TrimSpace(bindingID) == "" {
		return "", 0
	}
	items, err := runs.List(ctx, workspace, store.DriverRunFilter{BindingID: bindingID, Limit: bindingRunScanLimit})
	if err != nil || len(items) == 0 {
		return "", 0
	}
	store.SortDriverRunsNewestFirst(items)
	if len(items) > bindingRunScanLimit {
		items = items[:bindingRunScanLimit]
	}
	lastStatus := string(items[0].Status)
	consecutiveFailures := 0
	for _, run := range items {
		switch run.Status {
		case domain.DriverRunFailed:
			consecutiveFailures++
		case domain.DriverRunQueued, domain.DriverRunRunning, domain.DriverRunSuspendedAwaitingEvent:
			continue
		default:
			return lastStatus, consecutiveFailures
		}
	}
	return lastStatus, consecutiveFailures
}

func nextAutomationFireFor(binding *automation.Binding, now time.Time) *time.Time {
	if binding == nil || !binding.Enabled || binding.SourceKind != automation.SourceKindCron || strings.TrimSpace(binding.Schedule) == "" {
		return nil
	}
	return nextFire(binding.Schedule, binding.ScheduleTimezone, now)
}

func nextLegacyFireFor(binding *domain.TriggerBinding, now time.Time) *time.Time {
	if binding == nil || !binding.Enabled || binding.SourceKind != store.CronSourceKind || strings.TrimSpace(binding.Schedule) == "" {
		return nil
	}
	return nextFire(binding.Schedule, binding.ScheduleTimezone, now)
}

func nextFire(schedule, timezone string, now time.Time) *time.Time {
	next, err := trigger.NextFire(schedule, timezone, now)
	if err != nil {
		return nil
	}
	return &next
}

func (m *Module) createBinding(w http.ResponseWriter, r *http.Request) { //nolint:cyclop,funlen,gocognit // Validation and idempotent ensure semantics intentionally mirror the public wire contract in order.
	workspace, ok := m.canonicalWorkspace(r)
	if !ok {
		handler.RespondError(w, http.StatusBadRequest, "workspace is required")
		return
	}
	auth, ok := m.resolveOperator(w, r, workspace, automation.ActionCreateBinding)
	if !ok {
		return
	}
	if m.createWorkflow == nil || m.queries == nil {
		writeAutomationError(w, automation.ErrUnavailable, "create trigger binding failed")
		return
	}
	var request createBindingRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBindingBodyBytes)).Decode(&request); err != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sourceKind := strings.TrimSpace(request.SourceKind)
	if sourceKind == "" {
		sourceKind = "github"
	}
	routeKey := strings.TrimSpace(request.RouteKey)
	bindingID := strings.TrimSpace(request.BindingID)
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	secret := request.Secret
	if sourceKind == "github" && enabled && strings.TrimSpace(secret) == "" {
		handler.RespondError(w, http.StatusBadRequest, "secret is required to enable a github trigger binding")
		return
	}
	schedule := strings.TrimSpace(request.Schedule)
	if sourceKind == automation.SourceKindCron && schedule == "" {
		handler.RespondError(w, http.StatusBadRequest, "schedule is required for a cron trigger binding")
		return
	}
	switch sourceKind {
	case automation.SourceKindCron:
		if bindingID == "" && routeKey == "" {
			handler.RespondError(w, http.StatusBadRequest, "binding_id is required for a cron trigger binding")
			return
		}
	case automation.SourceKindInternal:
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
		bindingID = "binding-" + strings.ReplaceAll(routeKey, ".", "-")
	}
	if existing, err := m.queries.GetBinding(r.Context(), workspace, bindingID); err == nil {
		if existing == nil {
			writeAutomationError(w, automation.ErrInvalidPersistedState, "get trigger binding failed")
			return
		}
		// Browser/gallery activation uses POST as an idempotent ensure by
		// default. The standalone CLI historically exposed strict create
		// semantics, so its authenticated management request opts into a 409
		// without mutating the existing binding or its secret.
		if r.URL.Query().Get("create_only") == "true" {
			writeAutomationError(w, fmt.Errorf("trigger binding %q already exists: %w", bindingID, automation.ErrConflict), "create trigger binding failed")
			return
		}
		if !m.configureSecret(w, r, workspace, existing.BindingID, existing.SourceKind, secret) {
			return
		}
		handler.WriteJSON(w, http.StatusOK, existing)
		return
	} else if !errors.Is(err, automation.ErrNotFound) {
		writeAutomationError(w, err, "get trigger binding failed")
		return
	}
	driverID := strings.TrimSpace(request.DriverID)
	workflowName := strings.TrimSpace(request.Workflow)
	if driverID == "" && workflowName == "" {
		writeAutomationError(w, fmt.Errorf("one of workflow or driver_id is required: %w", automation.ErrInvalid), "create trigger binding failed")
		return
	}
	binding, err := m.createWorkflow.Create(r.Context(), auth, workflowbinding.CreateRequest{
		WorkspaceKey: workspace,
		Workflow:     workflowName,
		Definition: automation.BindingDefinition{
			BindingID: bindingID, Name: firstNonEmpty(strings.TrimSpace(request.Name), routeKey, bindingID),
			SourceKind: sourceKind, RouteKey: routeKey, EventTypePatterns: request.EventTypePatterns,
			DriverID: driverID, DriverVersionID: strings.TrimSpace(request.DriverVersionID),
			TargetEntrypoint: strings.TrimSpace(request.Entrypoint), SourceConfigRef: request.resolveSourceConfigRef(),
			SubjectKeyTemplate: strings.TrimSpace(request.SubjectKeyTemplate),
			ConcurrencyPolicy:  request.ConcurrencyPolicy, ActorFilter: request.ActorFilter,
			RetryMaxAttempts: request.RetryMaxAttempts, RetryBackoffSeconds: request.RetryBackoffSeconds,
			Enabled: enabled, Schedule: schedule, ScheduleTimezone: strings.TrimSpace(request.ScheduleTimezone),
		},
	})
	if err != nil {
		writeAutomationError(w, err, "create trigger binding failed")
		return
	}
	if binding == nil {
		writeAutomationError(w, automation.ErrInvalidPersistedState, "create trigger binding failed")
		return
	}
	if !m.configureSecret(w, r, workspace, binding.BindingID, binding.SourceKind, secret) {
		return
	}
	handler.WriteJSON(w, http.StatusCreated, binding)
}

func (m *Module) configureSecret(w http.ResponseWriter, r *http.Request, workspace, bindingID, sourceKind, secret string) bool {
	if secret == "" {
		return true
	}
	if m.connectors == nil {
		writeAutomationError(w, automation.ErrUnavailable, "configure trigger binding secret failed")
		return false
	}
	if err := m.connectors.ConfigureBindingSecret(r.Context(), workspace, bindingID, sourceKind, secret); err != nil {
		writeAutomationError(w, err, "configure trigger binding secret failed")
		return false
	}
	return true
}

func (m *Module) setEnabled(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace, ok := m.canonicalWorkspace(r)
		if !ok {
			handler.RespondError(w, http.StatusBadRequest, "workspace is required")
			return
		}
		bindingID := strings.TrimSpace(r.PathValue("id"))
		if bindingID == "" {
			handler.RespondError(w, http.StatusBadRequest, "binding id is required")
			return
		}
		action := automation.ActionDisableBinding
		if enabled {
			action = automation.ActionEnableBinding
		}
		auth, ok := m.resolveOperator(w, r, workspace, action)
		if !ok {
			return
		}
		if m.commands == nil || m.queries == nil {
			writeAutomationError(w, automation.ErrUnavailable, "update trigger binding failed")
			return
		}
		existing, err := m.queries.GetBinding(r.Context(), workspace, bindingID)
		if err != nil {
			writeAutomationError(w, err, "get trigger binding failed")
			return
		}
		if rejectManagedBinding(w, existing) {
			return
		}
		command := automation.BindingCommand{WorkspaceKey: workspace, BindingID: bindingID}
		var binding *automation.Binding
		if enabled {
			binding, err = m.commands.EnableBinding(r.Context(), auth, command)
		} else {
			binding, err = m.commands.DisableBinding(r.Context(), auth, command)
		}
		if err != nil {
			writeAutomationError(w, err, "update trigger binding failed")
			return
		}
		if binding == nil {
			writeAutomationError(w, automation.ErrInvalidPersistedState, "update trigger binding failed")
			return
		}
		handler.WriteJSON(w, http.StatusOK, binding)
	}
}

func (m *Module) runBinding(w http.ResponseWriter, r *http.Request) {
	workspace, ok := m.canonicalWorkspace(r)
	if !ok {
		handler.RespondError(w, http.StatusBadRequest, "workspace is required")
		return
	}
	bindingID := strings.TrimSpace(r.PathValue("id"))
	if bindingID == "" {
		handler.RespondError(w, http.StatusBadRequest, "binding id is required")
		return
	}
	auth, ok := m.resolveOperator(w, r, workspace, automation.ActionDispatchBinding)
	if !ok {
		return
	}
	if m.manualDispatch == nil {
		writeAutomationError(w, automation.ErrUnavailable, "run trigger binding failed")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		var err error
		idempotencyKey, err = newManualIdempotencyKey()
		if err != nil {
			writeAutomationError(w, automation.ErrUnavailable, "run trigger binding failed")
			return
		}
	}
	result, err := m.manualDispatch.DispatchBinding(r.Context(), auth, automation.DispatchBindingCommand{
		WorkspaceKey: workspace, BindingID: bindingID, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		writeAutomationError(w, err, "run trigger binding failed")
		return
	}
	if result == nil || strings.TrimSpace(result.RunID) == "" || len(result.RunSnapshot) == 0 || !json.Valid(result.RunSnapshot) {
		writeAutomationError(w, automation.ErrInvalidPersistedState, "run trigger binding failed")
		return
	}
	handler.WriteJSON(w, http.StatusAccepted, result.RunSnapshot)
}

func newManualIdempotencyKey() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "binding-run-" + hex.EncodeToString(bytes[:]), nil
}

func (m *Module) listBindingRuns(w http.ResponseWriter, r *http.Request) {
	workspace, ok := m.canonicalWorkspace(r)
	if !ok {
		handler.RespondError(w, http.StatusBadRequest, "workspace is required")
		return
	}
	bindingID := strings.TrimSpace(r.PathValue("id"))
	if bindingID == "" {
		handler.RespondError(w, http.StatusBadRequest, "workspace and binding id are required")
		return
	}
	if m.queries == nil || m.runs == nil {
		writeAutomationError(w, automation.ErrUnavailable, "list trigger binding runs failed")
		return
	}
	binding, err := m.queries.GetBinding(r.Context(), workspace, bindingID)
	if err != nil {
		writeAutomationError(w, err, "get trigger binding failed")
		return
	}
	if binding == nil {
		writeAutomationError(w, automation.ErrInvalidPersistedState, "get trigger binding failed")
		return
	}
	limit, ok := runhistory.ParseRunLimit(w, r)
	if !ok {
		return
	}
	runs, err := m.runs.List(r.Context(), workspace, store.DriverRunFilter{BindingID: binding.BindingID, Limit: limit})
	if err != nil {
		writeAutomationError(w, err, "list trigger binding runs failed")
		return
	}
	runs = runhistory.SortAndTrim(runs, limit)
	handler.WriteJSON(w, http.StatusOK, map[string]any{"binding_id": binding.BindingID, "runs": runs})
}

type updateBindingRequest struct {
	Name                *string                              `json:"name,omitempty"`
	SubjectKeyTemplate  *string                              `json:"subject_key_template,omitempty"`
	ConcurrencyPolicy   *automation.BindingConcurrencyPolicy `json:"concurrency_policy,omitempty"`
	ActorFilter         *automation.ActorFilter              `json:"actor_filter,omitempty"`
	ClearActorFilter    bool                                 `json:"clear_actor_filter,omitempty"`
	RetryMaxAttempts    *int                                 `json:"retry_max_attempts,omitempty"`
	RetryBackoffSeconds *int                                 `json:"retry_backoff_seconds,omitempty"`
	EventTypePatterns   *[]string                            `json:"event_type_patterns,omitempty"`
	Schedule            *string                              `json:"schedule,omitempty"`
	ScheduleTimezone    *string                              `json:"schedule_timezone,omitempty"`
}

func (m *Module) patchBinding(w http.ResponseWriter, r *http.Request) {
	workspace, ok := m.canonicalWorkspace(r)
	if !ok {
		handler.RespondError(w, http.StatusBadRequest, "workspace is required")
		return
	}
	bindingID := strings.TrimSpace(r.PathValue("id"))
	if bindingID == "" {
		handler.RespondError(w, http.StatusBadRequest, "binding id is required")
		return
	}
	auth, ok := m.resolveOperator(w, r, workspace, automation.ActionUpdateBinding)
	if !ok {
		return
	}
	if m.commands == nil || m.queries == nil {
		writeAutomationError(w, automation.ErrUnavailable, "update trigger binding failed")
		return
	}
	var request updateBindingRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBindingBodyBytes)).Decode(&request); err != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	existing, err := m.queries.GetBinding(r.Context(), workspace, bindingID)
	if err != nil {
		writeAutomationError(w, err, "get trigger binding failed")
		return
	}
	if rejectManagedBinding(w, existing) {
		return
	}
	patch, ok := buildBindingPatch(w, existing, request)
	if !ok {
		return
	}
	updated, err := m.commands.UpdateBinding(r.Context(), auth, automation.UpdateBindingCommand{
		WorkspaceKey: workspace, BindingID: bindingID, Patch: patch,
	})
	if err != nil {
		writeAutomationError(w, err, "update trigger binding failed")
		return
	}
	if updated == nil {
		writeAutomationError(w, automation.ErrInvalidPersistedState, "update trigger binding failed")
		return
	}
	handler.WriteJSON(w, http.StatusOK, bindingWithNextFire{Binding: updated, NextFireAt: nextAutomationFireFor(updated, time.Now())})
}

func buildBindingPatch(w http.ResponseWriter, existing *automation.Binding, request updateBindingRequest) (automation.BindingPatch, bool) { //nolint:cyclop,funlen // Each branch independently validates one optional public patch field.
	patch := automation.BindingPatch{}
	if existing == nil {
		writeAutomationError(w, automation.ErrInvalidPersistedState, "get trigger binding failed")
		return patch, false
	}
	if request.Name != nil {
		name := strings.TrimSpace(*request.Name)
		if name == "" {
			handler.RespondError(w, http.StatusBadRequest, "name cannot be empty")
			return patch, false
		}
		patch.Name = &name
	}
	if request.SubjectKeyTemplate != nil {
		value := strings.TrimSpace(*request.SubjectKeyTemplate)
		patch.SubjectKeyTemplate = &value
	}
	if request.ConcurrencyPolicy != nil {
		value := automation.BindingConcurrencyPolicy(strings.TrimSpace(string(*request.ConcurrencyPolicy)))
		patch.ConcurrencyPolicy = &value
	}
	if request.ClearActorFilter {
		patch.ClearActorFilter = true
	} else if request.ActorFilter != nil {
		patch.ActorFilter = request.ActorFilter.Clone()
	}
	patch.RetryMaxAttempts = request.RetryMaxAttempts
	patch.RetryBackoffSeconds = request.RetryBackoffSeconds
	if request.EventTypePatterns != nil {
		patterns := append([]string(nil), (*request.EventTypePatterns)...)
		patch.EventTypePatterns = &patterns
	}
	if request.Schedule != nil {
		if existing.SourceKind != automation.SourceKindCron {
			handler.RespondError(w, http.StatusBadRequest, "schedule can only be changed on a cron trigger binding")
			return patch, false
		}
		schedule := strings.TrimSpace(*request.Schedule)
		if err := trigger.ValidateSchedule(schedule); err != nil {
			handler.RespondError(w, http.StatusBadRequest, err.Error())
			return patch, false
		}
		patch.Schedule = &schedule
	}
	if request.ScheduleTimezone != nil {
		if existing.SourceKind != automation.SourceKindCron {
			handler.RespondError(w, http.StatusBadRequest, "schedule_timezone can only be changed on a cron trigger binding")
			return patch, false
		}
		timezone := strings.TrimSpace(*request.ScheduleTimezone)
		if err := trigger.ValidateScheduleTimezone(timezone); err != nil {
			handler.RespondError(w, http.StatusBadRequest, err.Error())
			return patch, false
		}
		patch.ScheduleTimezone = &timezone
	}
	if patch.Name == nil && patch.SubjectKeyTemplate == nil && patch.ConcurrencyPolicy == nil &&
		patch.ActorFilter == nil && !patch.ClearActorFilter && patch.RetryMaxAttempts == nil &&
		patch.RetryBackoffSeconds == nil && patch.EventTypePatterns == nil &&
		patch.Schedule == nil && patch.ScheduleTimezone == nil {
		handler.RespondError(w, http.StatusBadRequest, "no fields to update")
		return patch, false
	}
	return patch, true
}

// deleteBinding is restartable by construction: it resolves both authorities
// before side effects, then disables via Automation, idempotently revokes all
// connector grants, and only then asks Automation to delete the disabled
// binding. A failed revoke or delete leaves a disabled binding that a retry can
// safely resume.
func (m *Module) deleteBinding(w http.ResponseWriter, r *http.Request) { //nolint:funlen // Restartable disable, grant revocation, and delete steps stay visibly ordered.
	workspace, ok := m.canonicalWorkspace(r)
	if !ok {
		handler.RespondError(w, http.StatusBadRequest, "workspace is required")
		return
	}
	bindingID := strings.TrimSpace(r.PathValue("id"))
	if bindingID == "" {
		handler.RespondError(w, http.StatusBadRequest, "binding id is required")
		return
	}
	disableAuth, ok := m.resolveOperator(w, r, workspace, automation.ActionDisableBinding)
	if !ok {
		return
	}
	deleteAuth, ok := m.resolveOperator(w, r, workspace, automation.ActionDeleteBinding)
	if !ok {
		return
	}
	if m.commands == nil || m.queries == nil || m.connectors == nil {
		writeAutomationError(w, automation.ErrUnavailable, "delete trigger binding failed")
		return
	}
	existing, err := m.queries.GetBinding(r.Context(), workspace, bindingID)
	if err != nil {
		// DELETE is convergent: a retry after the final delete committed but its
		// response was lost observes the requested end state and succeeds. Still
		// repeat the idempotent grant cleanup so a pre-existing orphan is repaired
		// instead of being hidden by the absent binding.
		if errors.Is(err, automation.ErrNotFound) {
			revoked, revokeErr := m.connectors.RevokeBindingGrants(r.Context(), workspace, bindingID)
			if revokeErr != nil {
				writeAutomationError(w, revokeErr, "revoke missing trigger binding grants failed")
				return
			}
			handler.WriteJSON(w, http.StatusOK, DeleteBindingResult{
				BindingID: bindingID, Deleted: true, GrantsRevoked: revoked,
			})
			return
		}
		writeAutomationError(w, err, "get trigger binding failed")
		return
	}
	if rejectManagedBinding(w, existing) {
		return
	}
	command := automation.BindingCommand{WorkspaceKey: workspace, BindingID: bindingID}
	if _, err := m.commands.DisableBinding(r.Context(), disableAuth, command); err != nil {
		writeAutomationError(w, err, "disable trigger binding before delete failed")
		return
	}
	revoked, err := m.connectors.RevokeBindingGrants(r.Context(), workspace, bindingID)
	if err != nil {
		writeAutomationError(w, err, "revoke trigger binding grants failed")
		return
	}
	if err := m.commands.DeleteBinding(r.Context(), deleteAuth, command); err != nil {
		writeAutomationError(w, err, "delete trigger binding failed")
		return
	}
	handler.WriteJSON(w, http.StatusOK, DeleteBindingResult{BindingID: bindingID, Deleted: true, GrantsRevoked: revoked})
}

func rejectManagedBinding(w http.ResponseWriter, binding *automation.Binding) bool {
	if binding == nil || strings.TrimSpace(binding.TargetAgentServiceID) == "" {
		return false
	}
	handler.RespondError(w, http.StatusConflict, "managed by agent "+strings.TrimSpace(binding.TargetAgentServiceID))
	return true
}

// DeleteBindingAndRevokeGrants is an inert compatibility bridge for older
// agent handlers. Direct Store deletion is intentionally unavailable; callers
// must migrate to the Automation-backed delete workflow above.
func DeleteBindingAndRevokeGrants(_ context.Context, _ store.Store, _, bindingID string) (DeleteBindingResult, error) {
	return DeleteBindingResult{BindingID: strings.TrimSpace(bindingID)}, automation.ErrUnavailable
}

func (m *Module) canonicalWorkspace(r *http.Request) (string, bool) {
	if m == nil || r == nil || strings.TrimSpace(r.PathValue("ws")) == "" || m.workspaceFromContext == nil {
		return "", false
	}
	workspace := strings.TrimSpace(m.workspaceFromContext(r.Context()))
	return workspace, workspace != ""
}

func (m *Module) resolveOperator(w http.ResponseWriter, r *http.Request, workspace string, action authority.Action) (authority.OperatorAuthority, bool) {
	if m == nil || m.operatorAuthority == nil {
		writeAutomationError(w, automation.ErrUnavailable, "operator authority is unavailable")
		return authority.OperatorAuthority{}, false
	}
	auth, err := m.operatorAuthority.ResolveOperatorAuthority(r, workspace, action)
	if err != nil {
		writeAutomationError(w, err, "operator authority denied")
		return authority.OperatorAuthority{}, false
	}
	return auth, true
}

func writeAutomationError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, workflowcataloghttp.ErrUnauthenticated), errors.Is(err, authority.ErrInvalidOperatorToken),
		errors.Is(err, authority.ErrInvalidPrincipal), errors.Is(err, authority.ErrPrincipalExpired):
		handler.RespondError(w, http.StatusUnauthorized, "authentication required")
	case errors.Is(err, authority.ErrWorkspaceMismatch), errors.Is(err, authority.ErrActionNotAllowed),
		errors.Is(err, authority.ErrAdmissionDenied), errors.Is(err, authority.ErrPrincipalClass):
		handler.RespondError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, automation.ErrNotFound), errors.Is(err, domain.ErrNotFound):
		handler.RespondError(w, http.StatusNotFound, fallback)
	case errors.Is(err, automation.ErrInvalid), errors.Is(err, automation.ErrWrongWorkspace), errors.Is(err, domain.ErrInvalid):
		handler.RespondError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, workflowbinding.ErrInvalidRequest):
		handler.RespondError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, automation.ErrConflict), errors.Is(err, automation.ErrManagedBinding),
		errors.Is(err, automation.ErrBindingEnabled), errors.Is(err, automation.ErrExecutionBusy), errors.Is(err, domain.ErrConflict):
		handler.RespondError(w, http.StatusConflict, err.Error())
	case errors.Is(err, automation.ErrUnavailable), errors.Is(err, workflowbinding.ErrUnavailable):
		handler.RespondError(w, http.StatusServiceUnavailable, fallback)
	default:
		handler.RespondError(w, http.StatusInternalServerError, fallback)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
