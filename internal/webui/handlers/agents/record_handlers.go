package agents

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	rolehandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/roles"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/runhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/triggerbindings"
	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

const (
	agentRecordKindPrompt     = "prompt"
	agentRecordKindScripted   = "scripted"
	agentRecordKindBinding    = "binding"
	agentRecordKindSupervised = "supervised"
	agentArchiveMetadataKey   = "archived_at"
)

type supervisedAgentDTO struct {
	*domain.Agent
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

type agentRecordDTO struct {
	ID                  string             `json:"id"`
	Name                string             `json:"name"`
	Kind                string             `json:"kind"`
	Enabled             bool               `json:"enabled"`
	Behavior            agentBehaviorDTO   `json:"behavior"`
	BudgetPolicy        string             `json:"budget_policy,omitempty"`
	WorkspaceKey        string             `json:"workspace_key"`
	Bindings            []recordBindingDTO `json:"bindings,omitempty"`
	LastRunStatus       string             `json:"last_run_status,omitempty"`
	ConsecutiveFailures int                `json:"consecutive_failures,omitempty"`
	NextFireAt          *time.Time         `json:"next_fire_at,omitempty"`
	Metadata            map[string]string  `json:"metadata,omitempty"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
}

type agentBehaviorDTO struct {
	RoleName        string `json:"role_name,omitempty"`
	DriverID        string `json:"driver_id,omitempty"`
	DriverVersionID string `json:"driver_version_id,omitempty"`
}

type recordBindingDTO struct {
	*domain.TriggerBinding
	NextFireAt          *time.Time `json:"next_fire_at,omitempty"`
	LastRunStatus       string     `json:"last_run_status,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures,omitempty"`
}

type legacyBindingAgentDTO struct {
	*domain.TriggerBinding
	ID                  string     `json:"id"`
	Kind                string     `json:"kind"`
	NextFireAt          *time.Time `json:"next_fire_at,omitempty"`
	LastRunStatus       string     `json:"last_run_status,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures,omitempty"`
}

type createAgentKindProbe struct {
	Kind string `json:"kind"`
}

type createPromptAgentRequest struct {
	Kind         string                    `json:"kind"`
	Name         string                    `json:"name"`
	Backend      string                    `json:"backend,omitempty"`
	Behavior     promptAgentBehaviorCreate `json:"behavior"`
	Trigger      promptAgentTriggerRequest `json:"trigger,omitempty"`
	Grants       []promptAgentGrantRequest `json:"grants,omitempty"`
	Enabled      *bool                     `json:"enabled,omitempty"`
	BudgetPolicy string                    `json:"budget_policy,omitempty"`
}

type promptAgentBehaviorCreate struct {
	RoleName   string                 `json:"role_name"`
	RoleCreate *promptRoleCreateInput `json:"role_create,omitempty"`
}

type promptRoleCreateInput struct {
	Prompt         string   `json:"prompt,omitempty"`
	PromptFilename string   `json:"prompt_filename,omitempty"`
	Description    string   `json:"description,omitempty"`
	TaskFilter     string   `json:"task_filter,omitempty"`
	Model          string   `json:"model,omitempty"`
	Backend        string   `json:"backend,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	ReadOnly       bool     `json:"read_only,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	DeniedTools    []string `json:"denied_tools,omitempty"`
	Skills         []string `json:"skills,omitempty"`
}

type promptAgentTriggerRequest struct {
	SourceKind        string   `json:"source_kind,omitempty"`
	RouteKey          string   `json:"route_key,omitempty"`
	BindingID         string   `json:"binding_id,omitempty"`
	EventTypePatterns []string `json:"event_type_patterns,omitempty"`
	Schedule          string   `json:"schedule,omitempty"`
	ScheduleTimezone  string   `json:"schedule_timezone,omitempty"`
	Entrypoint        string   `json:"entrypoint,omitempty"`
}

type promptAgentGrantRequest struct {
	ConnectorID     string `json:"connector_id"`
	Action          string `json:"action"`
	ResourcePattern string `json:"resource_pattern"`
	GrantID         string `json:"grant_id,omitempty"`
}

type patchAgentRecordRequest struct {
	Name         *string                   `json:"name,omitempty"`
	Behavior     *patchAgentBehaviorRecord `json:"behavior,omitempty"`
	BudgetPolicy *string                   `json:"budget_policy,omitempty"`
}

type patchAgentBehaviorRecord struct {
	RoleName *string `json:"role_name,omitempty"`
}

func (m *Module) listAgents(w http.ResponseWriter, r *http.Request) { //nolint:funlen // Unified response merges three agent representations.
	if m.store == nil {
		if m.agentSvc == nil {
			handler.HandleServiceError(w, service.ErrUnavailable("fleet-db store not configured"))
			return
		}
		HandleList(m.agentSvc)(w, r)
		return
	}
	ws := requestWorkspaceID(r)
	now := time.Now()
	items := []any{}

	supervised, err := m.store.Agents().List(r.Context(), ws)
	if err != nil {
		handler.WriteDomainError(w, err, "list supervised agents failed")
		return
	}
	for _, a := range supervised {
		if a == nil {
			continue
		}
		items = append(items, supervisedAgentDTO{Agent: a, ID: a.Name, Kind: agentRecordKindSupervised})
	}

	// Archived records are excluded server-side via deleted_at (Wave B);
	// ?include=archived opts in, which requires IncludeDeleted on the store
	// filter or fleet-db never even returns them to filter here.
	includeArchived := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include")), "archived")
	records, err := m.store.AgentServices().List(r.Context(), ws, store.AgentServiceFilter{IncludeDeleted: includeArchived})
	if err != nil {
		handler.WriteDomainError(w, err, "list agent records failed")
		return
	}
	for _, record := range records {
		if record == nil {
			continue
		}
		if isAgentRecordArchived(record) && !includeArchived {
			continue
		}
		dto, err := m.agentRecordDTO(r.Context(), ws, record, now)
		if err != nil {
			handler.WriteDomainError(w, err, "decorate agent record failed")
			return
		}
		items = append(items, dto)
	}

	bindings, err := m.store.TriggerBindings().List(r.Context(), ws, store.TriggerBindingFilter{})
	if err != nil {
		handler.WriteDomainError(w, err, "list trigger bindings failed")
		return
	}
	for _, b := range bindings {
		if b == nil || strings.TrimSpace(b.TargetAgentServiceID) != "" {
			continue
		}
		items = append(items, legacyBindingDTO(r.Context(), m.store, ws, b, now))
	}

	handler.WriteJSON(w, http.StatusOK, dto.NewListResponse(items, len(items)))
}

func (m *Module) createAgent(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, handler.MaxRequestBody))
	if err != nil {
		handler.HandleServiceError(w, service.ErrPayloadTooLarge("request body too large (max 1MB)"))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))

	var probe createAgentKindProbe
	if err := json.Unmarshal(body, &probe); err != nil {
		m.createSupervisedAgent(w, r)
		return
	}
	switch strings.TrimSpace(probe.Kind) {
	case "", agentRecordKindSupervised:
		m.createSupervisedAgent(w, r)
	case agentRecordKindPrompt:
		m.createPromptAgent(w, r, body)
	default:
		handler.RespondError(w, http.StatusBadRequest, "unsupported agent kind: "+probe.Kind)
	}
}

func (m *Module) createSupervisedAgent(w http.ResponseWriter, r *http.Request) {
	if m.agentSvc == nil {
		handler.HandleServiceError(w, service.ErrUnavailable("agent service not configured"))
		return
	}
	HandleCreate(m.agentSvc, m.hub)(w, r)
}

func (m *Module) createPromptAgent(w http.ResponseWriter, r *http.Request, body []byte) { //nolint:funlen // Transaction and compensation stay visibly ordered.
	if m.store == nil {
		handler.HandleServiceError(w, service.ErrUnavailable("fleet-db store not configured"))
		return
	}
	ws := requestWorkspaceID(r)
	var req createPromptAgentRequest
	if err := json.Unmarshal(body, &req); err != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	roleName := strings.TrimSpace(req.Behavior.RoleName)
	if roleName == "" {
		handler.RespondError(w, http.StatusBadRequest, "behavior.role_name is required")
		return
	}
	if req.Behavior.RoleCreate != nil {
		if _, _, err := rolehandlers.EnsureRole(r.Context(), m.store, ws, rolehandlers.EnsureRoleRequest{
			Name:           roleName,
			Description:    req.Behavior.RoleCreate.Description,
			Prompt:         req.Behavior.RoleCreate.Prompt,
			PromptFilename: req.Behavior.RoleCreate.PromptFilename,
			Model:          req.Behavior.RoleCreate.Model,
			TaskFilter:     req.Behavior.RoleCreate.TaskFilter,
			Backend:        req.Behavior.RoleCreate.Backend,
			Effort:         req.Behavior.RoleCreate.Effort,
			ReadOnly:       req.Behavior.RoleCreate.ReadOnly,
			AllowedTools:   req.Behavior.RoleCreate.AllowedTools,
			DeniedTools:    req.Behavior.RoleCreate.DeniedTools,
			Skills:         req.Behavior.RoleCreate.Skills,
		}); err != nil {
			handler.WriteDomainError(w, err, "ensure role failed")
			return
		}
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	sourceKind := strings.TrimSpace(req.Trigger.SourceKind)
	if sourceKind == "" {
		sourceKind = store.InternalSourceKind
	}
	if sourceKind != store.InternalSourceKind && sourceKind != store.CronSourceKind {
		handler.RespondError(w, http.StatusBadRequest, "prompt agents support internal or cron triggers")
		return
	}
	desired := domain.AgentServiceDesiredPaused
	if enabled {
		desired = domain.AgentServiceDesiredRunning
	}

	agentID, err := createUniqueAgentRecord(r.Context(), m.store, ws, req.Name, roleName, store.AgentServiceCreate{
		WorkspaceKey: ws,
		Name:         firstNonEmpty(strings.TrimSpace(req.Name), roleName),
		Kind:         agentServiceKindForSource(sourceKind),
		DesiredState: desired,
		RoleName:     roleName,
		BudgetPolicy: strings.TrimSpace(req.BudgetPolicy),
	})
	if err != nil {
		handler.WriteDomainError(w, err, "create agent record failed")
		return
	}

	binding, err := m.createPromptAgentBinding(r.Context(), ws, agentID, req, roleName, enabled, sourceKind)
	if err != nil {
		// Compensation failures are non-fatal by design (§5: an orphan
		// "unconfigured" agent is legal and deletable) but must never be
		// silent — the serve log is the only trace an operator gets.
		if compErr := m.store.AgentServices().Delete(context.WithoutCancel(r.Context()), ws, agentID); compErr != nil {
			slog.Warn("prompt agent create: compensation failed, orphan agent record left behind",
				"workspace", ws, "agent_id", agentID, "err", compErr)
		}
		handler.WriteDomainError(w, err, "create prompt agent binding failed")
		return
	}

	if err := m.provisionPromptAgentGrants(r.Context(), ws, binding.BindingID, req.Grants); err != nil {
		if _, compErr := triggerbindings.DeleteBindingAndRevokeGrants(context.WithoutCancel(r.Context()), m.store, ws, binding.BindingID); compErr != nil {
			slog.Warn("prompt agent create: binding compensation failed after grant error",
				"workspace", ws, "binding_id", binding.BindingID, "err", compErr)
		}
		if compErr := m.store.AgentServices().Delete(context.WithoutCancel(r.Context()), ws, agentID); compErr != nil {
			slog.Warn("prompt agent create: record compensation failed after grant error, orphan agent record left behind",
				"workspace", ws, "agent_id", agentID, "err", compErr)
		}
		handler.WriteDomainError(w, err, "provision connector grants failed")
		return
	}

	record, err := m.store.AgentServices().Get(r.Context(), ws, agentID)
	if err != nil {
		handler.WriteDomainError(w, err, "get created agent record failed")
		return
	}
	out, err := m.agentRecordDTO(r.Context(), ws, record, time.Now())
	if err != nil {
		handler.WriteDomainError(w, err, "decorate created agent record failed")
		return
	}
	broadcastAgentRefresh(m.hub, ws, out.ID, r.Header.Get("X-Actor"))
	handler.WriteJSON(w, http.StatusCreated, out)
}

func createUniqueAgentRecord(ctx context.Context, st store.Store, ws, name, roleName string, base store.AgentServiceCreate) (string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		id, err := mintAgentRecordID(firstNonEmpty(name, roleName))
		if err != nil {
			return "", err
		}
		base.ServiceID = id
		if _, err := st.AgentServices().Create(ctx, base); err != nil {
			if errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrConflict) {
				continue
			}
			return "", err
		}
		return id, nil
	}
	return "", fmt.Errorf("mint unique agent id in workspace %q: %w", ws, domain.ErrAlreadyExists)
}

func (m *Module) createPromptAgentBinding(ctx context.Context, ws, agentID string, req createPromptAgentRequest, roleName string, enabled bool, sourceKind string) (*domain.TriggerBinding, error) {
	driver, err := m.store.Drivers().Get(ctx, ws, workflowdefs.BuiltinPromptAgentWorkflowName)
	if err != nil || strings.TrimSpace(driver.ActiveVersionID) == "" {
		driver, err = workflowdefs.EnsureAndResolveDriver(ctx, m.store, ws, workflowdefs.BuiltinPromptAgentWorkflowName)
		if err != nil {
			return nil, err
		}
	}
	versionID := strings.TrimSpace(driver.ActiveVersionID)
	if versionID == "" {
		return nil, fmt.Errorf("driver %q has no active version: %w", driver.DriverID, domain.ErrInvalid)
	}
	bindingID := strings.TrimSpace(req.Trigger.BindingID)
	if bindingID == "" {
		bindingID = agentID + "-1"
	}
	eventPatterns := append([]string(nil), req.Trigger.EventTypePatterns...)
	if sourceKind == store.InternalSourceKind && len(eventPatterns) == 0 {
		eventPatterns = []string{"internal.task.ready"}
	}
	if sourceKind == store.CronSourceKind && strings.TrimSpace(req.Trigger.Schedule) == "" {
		return nil, fmt.Errorf("schedule is required for a cron prompt agent: %w", domain.ErrInvalid)
	}
	sourceConfigRef, err := promptAgentSourceConfigRef(roleName, req.Backend)
	if err != nil {
		return nil, err
	}
	return m.store.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey:         ws,
		BindingID:            bindingID,
		Name:                 firstNonEmpty(strings.TrimSpace(req.Name), roleName, bindingID),
		SourceKind:           sourceKind,
		RouteKey:             strings.TrimSpace(req.Trigger.RouteKey),
		EventTypePatterns:    eventPatterns,
		DriverID:             driver.DriverID,
		DriverVersionID:      versionID,
		TargetEntrypoint:     firstNonEmpty(strings.TrimSpace(req.Trigger.Entrypoint), "run"),
		TargetAgentServiceID: agentID,
		Enabled:              enabled,
		Schedule:             strings.TrimSpace(req.Trigger.Schedule),
		ScheduleTimezone:     strings.TrimSpace(req.Trigger.ScheduleTimezone),
		SourceConfigRef:      sourceConfigRef,
		ConcurrencyPolicy:    domain.TriggerBindingConcurrencyOneActivePerEpic,
	})
}

func (m *Module) provisionPromptAgentGrants(ctx context.Context, ws, bindingID string, grants []promptAgentGrantRequest) error {
	for _, grant := range grants {
		connectorID := strings.TrimSpace(grant.ConnectorID)
		action := strings.TrimSpace(grant.Action)
		resourcePattern := strings.TrimSpace(grant.ResourcePattern)
		if connectorID == "" || action == "" || resourcePattern == "" {
			return fmt.Errorf("connector_id, action and resource_pattern are required for grants: %w", domain.ErrInvalid)
		}
		grantID := strings.TrimSpace(grant.GrantID)
		if grantID == "" {
			grantID = "grant-" + bindingID + "-" + strings.ReplaceAll(action, ".", "-")
		}
		if _, err := m.store.ConnectorGrants().Create(ctx, store.ConnectorGrantCreate{
			WorkspaceKey:    ws,
			GrantID:         grantID,
			ConnectorID:     connectorID,
			BindingID:       bindingID,
			Action:          action,
			ResourcePattern: resourcePattern,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) getAgent(w http.ResponseWriter, r *http.Request) {
	if m.store == nil {
		handler.HandleServiceError(w, service.ErrUnavailable("fleet-db store not configured"))
		return
	}
	ws := requestWorkspaceID(r)
	id := agentRouteValue(r, "name", "idOrName")
	if supervised, ok, err := m.supervisedByName(r.Context(), ws, id); err != nil {
		handler.WriteDomainError(w, err, "get supervised agent failed")
		return
	} else if ok {
		handler.WriteJSON(w, http.StatusOK, supervisedAgentDTO{Agent: supervised, ID: supervised.Name, Kind: agentRecordKindSupervised})
		return
	}
	record, err := m.store.AgentServices().Get(r.Context(), ws, id)
	if err != nil {
		handler.WriteDomainError(w, err, "get agent record failed")
		return
	}
	out, err := m.agentRecordDTO(r.Context(), ws, record, time.Now())
	if err != nil {
		handler.WriteDomainError(w, err, "decorate agent record failed")
		return
	}
	handler.WriteJSON(w, http.StatusOK, out)
}

func (m *Module) patchAgent(w http.ResponseWriter, r *http.Request) { //nolint:funlen // Kind routing and record patching share one endpoint.
	if m.store == nil {
		handler.HandleServiceError(w, service.ErrUnavailable("fleet-db store not configured"))
		return
	}
	ws := requestWorkspaceID(r)
	id := agentRouteValue(r, "name", "idOrName")
	if _, ok, err := m.supervisedByName(r.Context(), ws, id); err != nil {
		handler.WriteDomainError(w, err, "get supervised agent failed")
		return
	} else if ok {
		if m.agentSvc == nil {
			handler.HandleServiceError(w, service.ErrUnavailable("agent service not configured"))
			return
		}
		HandleUpdate(m.agentSvc, m.hub)(w, r)
		return
	}

	var req patchAgentRecordRequest
	if err := handler.ReadJSON(w, r, &req); err != nil {
		handler.HandleServiceError(w, err)
		return
	}
	patch := store.AgentServiceUpdate{}
	var newRoleName string
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			handler.RespondError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		patch.Name = &name
	}
	if req.BudgetPolicy != nil {
		budgetPolicy := strings.TrimSpace(*req.BudgetPolicy)
		patch.BudgetPolicy = &budgetPolicy
	}
	if req.Behavior != nil && req.Behavior.RoleName != nil {
		roleName := strings.TrimSpace(*req.Behavior.RoleName)
		if roleName == "" {
			handler.RespondError(w, http.StatusBadRequest, "behavior.role_name cannot be empty")
			return
		}
		newRoleName = roleName
		patch.RoleName = &roleName
	}
	if patch.Name == nil && patch.BudgetPolicy == nil && patch.RoleName == nil {
		handler.RespondError(w, http.StatusBadRequest, "no fields to update")
		return
	}
	record, err := m.store.AgentServices().Update(r.Context(), ws, id, patch)
	if err != nil {
		handler.WriteDomainError(w, err, "update agent record failed")
		return
	}
	if newRoleName != "" {
		if err := m.updateAttachedBindingRole(r.Context(), ws, record.ServiceID, newRoleName); err != nil {
			handler.WriteDomainError(w, err, "update attached binding role failed")
			return
		}
	}
	out, err := m.agentRecordDTO(r.Context(), ws, record, time.Now())
	if err != nil {
		handler.WriteDomainError(w, err, "decorate agent record failed")
		return
	}
	broadcastAgentRefresh(m.hub, ws, out.ID, r.Header.Get("X-Actor"))
	handler.WriteJSON(w, http.StatusOK, out)
}

func (m *Module) deleteAgent(w http.ResponseWriter, r *http.Request) { //nolint:funlen // Lifecycle deletion keeps binding cleanup and archival ordered.
	if m.store == nil {
		handler.HandleServiceError(w, service.ErrUnavailable("fleet-db store not configured"))
		return
	}
	ws := requestWorkspaceID(r)
	id := agentRouteValue(r, "name", "idOrName")
	if _, ok, err := m.supervisedByName(r.Context(), ws, id); err != nil {
		handler.WriteDomainError(w, err, "get supervised agent failed")
		return
	} else if ok {
		if m.agentSvc == nil {
			handler.HandleServiceError(w, service.ErrUnavailable("agent service not configured"))
			return
		}
		HandleDelete(m.agentSvc, m.hub)(w, r)
		return
	}

	record, err := m.store.AgentServices().Get(r.Context(), ws, id)
	if err != nil {
		handler.WriteDomainError(w, err, "get agent record failed")
		return
	}
	bindings, err := m.store.TriggerBindings().List(r.Context(), ws, store.TriggerBindingFilter{TargetAgentServiceID: record.ServiceID})
	if err != nil {
		handler.WriteDomainError(w, err, "list attached bindings failed")
		return
	}
	bindingsDeleted := 0
	grantsRevoked := 0
	for _, b := range bindings {
		if b == nil {
			continue
		}
		result, err := triggerbindings.DeleteBindingAndRevokeGrants(r.Context(), m.store, ws, b.BindingID)
		if err != nil {
			handler.WriteDomainError(w, err, "delete attached binding failed")
			return
		}
		if result.Deleted {
			bindingsDeleted++
		}
		grantsRevoked += result.GrantsRevoked
	}
	// Archive via Wave B's deleted_at (fleet-db DELETE archives, never erases;
	// the record stays GET-able for run attribution). The Wave-A metadata
	// marker is superseded — deleted_at is the single archive signal, so every
	// fleet-db consumer sees the same lifecycle state. desired_state is parked
	// stopped first so a later un-archive (ops) doesn't resurrect a running
	// agent by surprise.
	stopped := domain.AgentServiceDesiredStopped
	if _, err := m.store.AgentServices().Update(r.Context(), ws, record.ServiceID, store.AgentServiceUpdate{
		DesiredState: &stopped,
	}); err != nil {
		handler.WriteDomainError(w, err, "park agent record failed")
		return
	}
	if err := m.store.AgentServices().Delete(r.Context(), ws, record.ServiceID); err != nil {
		handler.WriteDomainError(w, err, "archive agent record failed")
		return
	}
	archived, err := m.store.AgentServices().Get(r.Context(), ws, record.ServiceID)
	if err != nil {
		handler.WriteDomainError(w, err, "get archived agent record failed")
		return
	}
	out, err := m.agentRecordDTO(r.Context(), ws, archived, time.Now())
	if err != nil {
		handler.WriteDomainError(w, err, "decorate archived agent record failed")
		return
	}
	broadcastAgentRefresh(m.hub, ws, out.ID, r.Header.Get("X-Actor"))
	handler.WriteJSON(w, http.StatusOK, map[string]any{
		"agent":            out,
		"archived":         true,
		"bindings_deleted": bindingsDeleted,
		"grants_revoked":   grantsRevoked,
	})
}

func (m *Module) setRecordEnabled(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.store == nil {
			handler.HandleServiceError(w, service.ErrUnavailable("fleet-db store not configured"))
			return
		}
		ws := requestWorkspaceID(r)
		id := agentRouteValue(r, "id", "name")
		if _, ok, err := m.supervisedByName(r.Context(), ws, id); err != nil {
			handler.WriteDomainError(w, err, "get supervised agent failed")
			return
		} else if ok {
			handler.RespondError(w, http.StatusBadRequest, "supervised agents use start/stop lifecycle actions")
			return
		}
		record, err := m.store.AgentServices().Get(r.Context(), ws, id)
		if err != nil {
			handler.WriteDomainError(w, err, "get agent record failed")
			return
		}
		if isAgentRecordArchived(record) {
			handler.RespondError(w, http.StatusConflict, "agent is archived")
			return
		}
		desired := domain.AgentServiceDesiredPaused
		if enabled {
			desired = domain.AgentServiceDesiredRunning
		}
		updated, err := m.store.AgentServices().Update(r.Context(), ws, record.ServiceID, store.AgentServiceUpdate{DesiredState: &desired})
		if err != nil {
			handler.WriteDomainError(w, err, "update agent record failed")
			return
		}
		if err := m.setAttachedBindingsEnabled(r.Context(), ws, record.ServiceID, enabled); err != nil {
			handler.WriteDomainError(w, err, "update attached bindings failed")
			return
		}
		out, err := m.agentRecordDTO(r.Context(), ws, updated, time.Now())
		if err != nil {
			handler.WriteDomainError(w, err, "decorate agent record failed")
			return
		}
		broadcastAgentRefresh(m.hub, ws, out.ID, r.Header.Get("X-Actor"))
		handler.WriteJSON(w, http.StatusOK, out)
	}
}

func (m *Module) listAgentRuns(w http.ResponseWriter, r *http.Request) {
	if m.store == nil {
		handler.HandleServiceError(w, service.ErrUnavailable("fleet-db store not configured"))
		return
	}
	ws := requestWorkspaceID(r)
	id := agentRouteValue(r, "id", "name")
	if _, ok, err := m.supervisedByName(r.Context(), ws, id); err != nil {
		handler.WriteDomainError(w, err, "get supervised agent failed")
		return
	} else if ok {
		handler.RespondError(w, http.StatusBadRequest, "agent runs are available for record agents")
		return
	}
	record, err := m.store.AgentServices().Get(r.Context(), ws, id)
	if err != nil {
		handler.WriteDomainError(w, err, "get agent record failed")
		return
	}
	limit, ok := runhistory.ParseRunLimit(w, r)
	if !ok {
		return
	}
	runs, err := m.runsForAgent(r.Context(), ws, record.ServiceID, limit)
	if err != nil {
		handler.WriteDomainError(w, err, "list agent runs failed")
		return
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{
		"agent_id": record.ServiceID,
		"runs":     runs,
	})
}

func (m *Module) runsForAgent(ctx context.Context, ws, agentID string, limit int) ([]*domain.DriverRun, error) {
	runs, err := m.store.DriverRuns().List(ctx, ws, store.DriverRunFilter{
		AgentServiceID: agentID,
		Limit:          limit,
	})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(runs))
	for _, run := range runs {
		if run != nil {
			seen[run.RunID] = struct{}{}
		}
	}

	// Compatibility for runs created before fleet-db stamped agent_service_id:
	// while their binding remains attached, retain the historical binding join.
	bindings, err := m.store.TriggerBindings().List(ctx, ws, store.TriggerBindingFilter{TargetAgentServiceID: agentID})
	if err != nil {
		return nil, err
	}
	for _, b := range bindings {
		if b == nil {
			continue
		}
		bindingRuns, err := m.store.DriverRuns().List(ctx, ws, store.DriverRunFilter{
			BindingID: b.BindingID,
			Limit:     limit,
		})
		if err != nil {
			return nil, err
		}
		for _, run := range bindingRuns {
			if run == nil {
				continue
			}
			if _, ok := seen[run.RunID]; ok {
				continue
			}
			seen[run.RunID] = struct{}{}
			runs = append(runs, run)
		}
	}
	runs = runhistory.SortAndTrim(runs, limit)
	return runs, nil
}

func (m *Module) supervisedByName(ctx context.Context, ws, name string) (*domain.Agent, bool, error) {
	if m.store == nil || strings.TrimSpace(name) == "" {
		return nil, false, nil
	}
	agent, err := m.store.Agents().Get(ctx, ws, name)
	if err == nil && agent != nil {
		return agent, true, nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return nil, false, nil
	}
	return nil, false, err
}

func (m *Module) agentRecordDTO(ctx context.Context, ws string, record *domain.AgentService, now time.Time) (agentRecordDTO, error) {
	out := agentRecordDTO{
		ID:           record.ServiceID,
		Name:         record.Name,
		Kind:         deriveAgentRecordKind(record),
		Enabled:      record.DesiredState == domain.AgentServiceDesiredRunning,
		Behavior:     agentBehaviorDTO{RoleName: record.RoleName},
		BudgetPolicy: record.BudgetPolicy,
		WorkspaceKey: record.WorkspaceKey,
		Metadata:     cloneStringMap(record.Metadata),
		CreatedAt:    record.CreatedAt,
		UpdatedAt:    record.UpdatedAt,
	}
	bindings, err := m.store.TriggerBindings().List(ctx, ws, store.TriggerBindingFilter{TargetAgentServiceID: record.ServiceID})
	if err != nil {
		return out, err
	}
	decorators := make([]triggerbindings.BindingDecorators, 0, len(bindings))
	out.Bindings = make([]recordBindingDTO, 0, len(bindings))
	for _, b := range bindings {
		if b == nil {
			continue
		}
		dec := triggerbindings.DecorateBinding(ctx, m.store, ws, b, now)
		decorators = append(decorators, dec)
		out.Bindings = append(out.Bindings, recordBindingDTO{
			TriggerBinding:      b,
			NextFireAt:          dec.NextFireAt,
			LastRunStatus:       dec.LastRunStatus,
			ConsecutiveFailures: dec.ConsecutiveFailures,
		})
	}
	out.LastRunStatus, out.ConsecutiveFailures, out.NextFireAt = aggregateBindingDecorators(decorators)
	return out, nil
}

func legacyBindingDTO(ctx context.Context, st store.Store, ws string, b *domain.TriggerBinding, now time.Time) legacyBindingAgentDTO {
	dec := triggerbindings.DecorateBinding(ctx, st, ws, b, now)
	return legacyBindingAgentDTO{
		TriggerBinding:      b,
		ID:                  b.BindingID,
		Kind:                agentRecordKindBinding,
		NextFireAt:          dec.NextFireAt,
		LastRunStatus:       dec.LastRunStatus,
		ConsecutiveFailures: dec.ConsecutiveFailures,
	}
}

func aggregateBindingDecorators(decorators []triggerbindings.BindingDecorators) (string, int, *time.Time) {
	lastStatus := ""
	lastRank := -1
	failures := 0
	var next *time.Time
	for _, dec := range decorators {
		if dec.ConsecutiveFailures > failures {
			failures = dec.ConsecutiveFailures
		}
		if dec.LastRunStatus != "" {
			if rank := runStatusRank(dec.LastRunStatus); rank > lastRank {
				lastRank = rank
				lastStatus = dec.LastRunStatus
			}
		}
		if dec.NextFireAt != nil && (next == nil || dec.NextFireAt.Before(*next)) {
			t := *dec.NextFireAt
			next = &t
		}
	}
	return lastStatus, failures, next
}

func runStatusRank(status string) int {
	switch domain.DriverRunStatus(status) {
	case domain.DriverRunFailed:
		return 50
	case domain.DriverRunNeedsReview, domain.DriverRunCancelled:
		return 40
	case domain.DriverRunRunning, domain.DriverRunQueued, domain.DriverRunSuspendedAwaitingEvent:
		return 30
	case domain.DriverRunCompleted:
		return 10
	default:
		return 0
	}
}

func deriveAgentRecordKind(record *domain.AgentService) string {
	if strings.TrimSpace(record.RoleName) != "" {
		return agentRecordKindPrompt
	}
	return agentRecordKindScripted
}

func isAgentRecordArchived(record *domain.AgentService) bool {
	if record == nil {
		return false
	}
	// deleted_at is the archive signal (Wave B); the metadata marker survives
	// only for records archived before the switch.
	return record.DeletedAt != nil || strings.TrimSpace(record.Metadata[agentArchiveMetadataKey]) != ""
}

func agentServiceKindForSource(sourceKind string) domain.AgentServiceKind {
	if sourceKind == store.CronSourceKind {
		return domain.AgentServiceKindCron
	}
	return domain.AgentServiceKindEvent
}

func mintAgentRecordID(name string) (string, error) {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	return "agt-" + slugForAgentID(name) + "-" + hex.EncodeToString(suffix), nil
}

func slugForAgentID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 32 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "agent"
	}
	if len(slug) > 32 {
		slug = strings.Trim(slug[:32], "-")
	}
	return slug
}

func promptAgentSourceConfigRef(roleName, backend string) (string, error) {
	runInput := map[string]string{"roleName": roleName}
	if backend = strings.TrimSpace(backend); backend != "" {
		runInput["backend"] = backend
	}
	data, err := json.Marshal(runInput)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (m *Module) updateAttachedBindingRole(ctx context.Context, ws, agentID, roleName string) error {
	bindings, err := m.store.TriggerBindings().List(ctx, ws, store.TriggerBindingFilter{TargetAgentServiceID: agentID})
	if err != nil {
		return err
	}
	for _, b := range bindings {
		if b == nil {
			continue
		}
		ref := sourceConfigRefWithRole(b.SourceConfigRef, roleName)
		if _, err := m.store.TriggerBindings().Update(ctx, ws, b.BindingID, store.TriggerBindingUpdate{SourceConfigRef: &ref}); err != nil {
			return err
		}
	}
	return nil
}

func sourceConfigRefWithRole(ref, roleName string) string {
	obj := map[string]json.RawMessage{}
	if strings.TrimSpace(ref) != "" {
		_ = json.Unmarshal([]byte(ref), &obj)
	}
	raw, _ := json.Marshal(roleName)
	obj["roleName"] = raw
	data, err := json.Marshal(obj)
	if err != nil {
		return `{"roleName":` + strconvQuote(roleName) + `}`
	}
	return string(data)
}

func (m *Module) setAttachedBindingsEnabled(ctx context.Context, ws, agentID string, enabled bool) error {
	bindings, err := m.store.TriggerBindings().List(ctx, ws, store.TriggerBindingFilter{TargetAgentServiceID: agentID})
	if err != nil {
		return err
	}
	for _, b := range bindings {
		if b == nil {
			continue
		}
		flag := enabled
		if _, err := m.store.TriggerBindings().Update(ctx, ws, b.BindingID, store.TriggerBindingUpdate{Enabled: &flag}); err != nil {
			return err
		}
	}
	return nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func agentRouteValue(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(r.PathValue(name)); value != "" {
			return value
		}
	}
	return ""
}

func strconvQuote(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(data)
}
