package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

func (m *Module) listAgents(w http.ResponseWriter, r *http.Request) { //nolint:cyclop,funlen // Unified response must merge and collision-check three agent representations.
	if m.store == nil {
		if m.agentSvc == nil {
			handler.HandleServiceError(w, service.ErrUnavailable("fleet-db store not configured"))
			return
		}
		HandleList(m.agentSvc)(w, r)
		return
	}
	ws, ok := m.requireCanonicalWorkspace(w, r)
	if !ok {
		return
	}
	now := time.Now()
	items := []any{}

	supervised, err := m.store.Agents().List(r.Context(), ws)
	if err != nil {
		handler.WriteDomainError(w, err, "list supervised agents failed")
		return
	}
	supervisedIDs := make(map[string]struct{}, len(supervised))
	for _, a := range supervised {
		if a == nil {
			continue
		}
		supervisedIDs[a.Name] = struct{}{}
		items = append(items, newSupervisedAgentDTO(a))
	}

	// Load archived records for identity-collision detection even when the caller
	// does not request them in the response. GET-by-id and create already reserve
	// those durable identities, so allowing a legacy binding with the same id to
	// appear in the normal list would make the collection disagree with item
	// routing. ?include=archived controls display only.
	includeArchived := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include")), "archived")
	records, err := m.store.AgentServices().List(r.Context(), ws, store.AgentServiceFilter{IncludeDeleted: true})
	if err != nil {
		handler.WriteDomainError(w, err, "list agent records failed")
		return
	}
	recordIDs := make(map[string]struct{}, len(records))
	for _, record := range records {
		if record == nil {
			continue
		}
		recordIDs[record.ServiceID] = struct{}{}
		if _, collision := supervisedIDs[record.ServiceID]; collision {
			handler.WriteDomainError(w, agentIdentifierCollisionError(record.ServiceID, "a supervised agent", "an agent record"), "list agents failed")
			return
		}
		if isAgentRecordArchived(record) && !includeArchived {
			continue
		}
		dto, err := m.agentRecordDTO(r.Context(), ws, record, now)
		if err != nil {
			writeBindingError(w, err, "decorate agent record failed")
			return
		}
		items = append(items, dto)
	}

	if m.bindings == nil {
		writeBindingError(w, automation.ErrUnavailable, "list trigger bindings failed")
		return
	}
	bindings, err := m.bindings.ListBindings(r.Context(), ws, automation.BindingFilter{})
	if err != nil {
		writeBindingError(w, err, "list trigger bindings failed")
		return
	}
	for _, b := range bindings {
		if b == nil || strings.TrimSpace(b.TargetAgentServiceID) != "" {
			continue
		}
		if _, collision := supervisedIDs[b.BindingID]; collision {
			handler.WriteDomainError(w, agentIdentifierCollisionError(b.BindingID, "a supervised agent", "a legacy binding agent"), "list agents failed")
			return
		}
		if _, collision := recordIDs[b.BindingID]; collision {
			handler.WriteDomainError(w, agentIdentifierCollisionError(b.BindingID, "an agent record", "a legacy binding agent"), "list agents failed")
			return
		}
		items = append(items, legacyBindingDTO(r.Context(), m.store, ws, b, now))
	}

	handler.WriteJSON(w, http.StatusOK, dto.NewListResponse(items, len(items)))
}

func (m *Module) createAgent(w http.ResponseWriter, r *http.Request) {
	ws, ok := m.requireCanonicalWorkspace(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, handler.MaxRequestBody))
	if err != nil {
		handler.HandleServiceError(w, service.ErrPayloadTooLarge("request body too large (max 1MB)"))
		return
	}
	resetAgentCreateBody(r, body)

	var probe createAgentKindProbe
	if err := json.Unmarshal(body, &probe); err != nil {
		m.createSupervisedAgent(w, r)
		return
	}
	switch strings.ToLower(strings.TrimSpace(probe.Kind)) {
	case "", string(domain.RoleKindInteractive), string(domain.RoleKindWorker):
		if m.rejectSupervisedIdentityCollision(w, r.Context(), ws, probe.Name) {
			return
		}
		m.createSupervisedAgent(w, r)
	case agentRecordKindSupervised:
		if m.rejectSupervisedIdentityCollision(w, r.Context(), ws, probe.Name) {
			return
		}
		// Unified agent records use kind="supervised" as their representation
		// discriminator, while the legacy assignment create contract uses the
		// same field for role kind (interactive/worker). Remove the record
		// discriminator before delegating so AgentCreateInput can infer the role
		// kind instead of rejecting "supervised" as an invalid role kind.
		normalized, err := withoutJSONField(body, "kind")
		if err != nil {
			handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		resetAgentCreateBody(r, normalized)
		m.createSupervisedAgent(w, r)
	case agentRecordKindPrompt:
		m.createPromptAgent(w, r, body)
	default:
		handler.RespondError(w, http.StatusBadRequest, "unsupported agent kind: "+probe.Kind)
	}
}

func (m *Module) rejectSupervisedIdentityCollision(w http.ResponseWriter, ctx context.Context, ws, name string) bool {
	if m.store == nil || strings.TrimSpace(name) == "" {
		return false
	}
	if m.bindings == nil {
		writeBindingError(w, automation.ErrUnavailable, "check agent identifier failed")
		return true
	}
	// The supervised service lowercases names before persistence. Probe the
	// cross-kind namespaces with that prospective stored identity, while leaving
	// durable record and binding identifiers themselves case-sensitive.
	id := strings.ToLower(strings.TrimSpace(name))
	if _, err := m.store.AgentServices().Get(ctx, ws, id); err == nil {
		handler.RespondError(w, http.StatusConflict, "agent identifier is already used by an agent record")
		return true
	} else if !errors.Is(err, domain.ErrNotFound) {
		handler.WriteDomainError(w, err, "check agent identifier failed")
		return true
	}
	if _, ok, err := m.unattachedBindingByID(ctx, ws, id); err != nil {
		writeBindingError(w, err, "check agent identifier failed")
		return true
	} else if ok {
		handler.RespondError(w, http.StatusConflict, "agent identifier is already used by a legacy binding agent")
		return true
	}
	return false
}

func agentIdentifierCollisionError(id, first, second string) error {
	return fmt.Errorf("agent identifier %q resolves to both %s and %s: %w", id, first, second, domain.ErrConflict)
}

func createUniqueAgentRecord(
	ctx context.Context,
	st store.Store,
	ws, name, roleName string,
	base store.AgentServiceCreate,
) (*domain.AgentService, error) {
	for attempt := 0; attempt < 5; attempt++ {
		id, err := mintAgentRecordID(firstNonEmpty(name, roleName))
		if err != nil {
			return nil, err
		}
		if _, err := st.Agents().Get(ctx, ws, id); err == nil {
			continue
		} else if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		base.ServiceID = id
		created, err := st.AgentServices().Create(ctx, base)
		if err != nil {
			if errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrConflict) {
				continue
			}
			return nil, err
		}
		if created == nil {
			return nil, errors.New("create agent record returned no record")
		}
		return created, nil
	}
	return nil, fmt.Errorf("mint unique agent id in workspace %q: %w", ws, domain.ErrAlreadyExists)
}

func (m *Module) createSupervisedAgent(w http.ResponseWriter, r *http.Request) {
	if m.agentSvc == nil {
		handler.HandleServiceError(w, service.ErrUnavailable("agent service not configured"))
		return
	}
	HandleCreate(m.agentSvc, m.hub)(w, r)
}

type promptAgentCreatePlan struct {
	request    createPromptAgentRequest
	roleName   string
	sourceKind string
	enabled    bool
	desired    domain.AgentServiceDesiredState
}

func parsePromptAgentCreatePlan(body []byte) (promptAgentCreatePlan, error) {
	var req createPromptAgentRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return promptAgentCreatePlan{}, errors.New("invalid JSON body")
	}
	roleName := strings.TrimSpace(req.Behavior.RoleName)
	if roleName == "" {
		return promptAgentCreatePlan{}, errors.New("behavior.role_name is required")
	}
	enabled := req.Enabled == nil || *req.Enabled
	sourceKind := strings.TrimSpace(req.Trigger.SourceKind)
	if sourceKind == "" {
		sourceKind = store.InternalSourceKind
	}
	if sourceKind != store.InternalSourceKind && sourceKind != store.CronSourceKind {
		return promptAgentCreatePlan{}, errors.New("prompt agents support internal or cron triggers")
	}
	if sourceKind == store.CronSourceKind && strings.TrimSpace(req.Trigger.Schedule) == "" {
		return promptAgentCreatePlan{}, errors.New("schedule is required for a cron prompt agent")
	}
	desired := domain.AgentServiceDesiredPaused
	if enabled {
		desired = domain.AgentServiceDesiredRunning
	}
	return promptAgentCreatePlan{
		request:    req,
		roleName:   roleName,
		sourceKind: sourceKind,
		enabled:    enabled,
		desired:    desired,
	}, nil
}

func (m *Module) compensatePromptAgentBindingFailure(
	ctx context.Context,
	ws string,
	agentID string,
) {
	if err := m.store.AgentServices().Delete(context.WithoutCancel(ctx), ws, agentID); err != nil {
		slog.Warn("prompt agent create: compensation failed, orphan agent record left behind",
			"workspace", ws, "agent_id", agentID, "err", err)
	}
}

func (m *Module) compensatePromptAgentGrantFailure(
	ctx context.Context,
	ws string,
	bindingID string,
	agentID string,
	authorities promptAgentCreateAuthorities,
) {
	compensationCtx := context.WithoutCancel(ctx)
	if _, err := m.deleteManagedBinding(
		compensationCtx, ws, bindingID, agentID, authorities.disableBinding, authorities.deleteBinding,
	); err != nil {
		slog.Warn("prompt agent create: binding compensation failed after grant error",
			"workspace", ws, "binding_id", bindingID, "err", err)
	}
	if err := m.store.AgentServices().Delete(compensationCtx, ws, agentID); err != nil {
		slog.Warn("prompt agent create: record compensation failed after grant error, orphan agent record left behind",
			"workspace", ws, "agent_id", agentID, "err", err)
	}
}

func (m *Module) resolvePromptAgentDriverForCreate(
	w http.ResponseWriter,
	r *http.Request,
	ws string,
) (*domain.Driver, bool) {
	driverRecord, err := m.resolvePromptAgentDriver(r.Context(), ws)
	if err == nil {
		return driverRecord, true
	}
	if errors.Is(err, workflowdefs.ErrBuildToolchainUnavailable) {
		slog.Warn("prompt agent create: workflow build toolchain unavailable", "workspace", ws, "err", err)
		handler.RespondError(w, http.StatusServiceUnavailable,
			"prompt-agent workflow build toolchain is unavailable; configure the Loom SDK and Flue build runtime/CLI, including the target-platform Rolldown native binding")
		return nil, false
	}
	handler.WriteDomainError(w, err, "resolve prompt-agent driver failed")
	return nil, false
}

func (m *Module) createPromptAgent(w http.ResponseWriter, r *http.Request, body []byte) { //nolint:funlen // Transaction and compensation stay visibly ordered.
	if m.store == nil {
		handler.HandleServiceError(w, service.ErrUnavailable("fleet-db store not configured"))
		return
	}
	ws, ok := m.requireCanonicalWorkspace(w, r)
	if !ok {
		return
	}
	plan, err := parsePromptAgentCreatePlan(body)
	if err != nil {
		handler.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}
	req := plan.request
	authorities, ok := m.resolvePromptAgentCreateAuthorities(w, r, ws, len(req.Grants) > 0)
	if !ok {
		return
	}
	// Resolve and, for the embedded prompt-agent, materialize the executable
	// driver before mutating role/service state. Some deployment profiles are
	// intentionally read/run-only and omit the Flue build toolchain; those must
	// fail atomically and advertise temporary capability unavailability rather
	// than leaving a role behind and returning an opaque 500 after compensation.
	driverRecord, ok := m.resolvePromptAgentDriverForCreate(w, r, ws)
	if !ok {
		return
	}
	roleResolution, ok := m.resolvePromptAgentRoleForCreate(w, r, ws, plan)
	if !ok {
		return
	}

	agentMetadata := map[string]string{}
	if backend := strings.TrimSpace(req.Backend); backend != "" {
		agentMetadata["backend"] = backend
	}
	record, err := createUniqueAgentRecord(r.Context(), m.store, ws, req.Name, plan.roleName, store.AgentServiceCreate{
		WorkspaceKey: ws,
		Name:         firstNonEmpty(strings.TrimSpace(req.Name), plan.roleName),
		Kind:         agentServiceKindForSource(plan.sourceKind),
		DesiredState: plan.desired,
		RoleName:     plan.roleName,
		BudgetPolicy: strings.TrimSpace(req.BudgetPolicy),
		Metadata:     agentMetadata,
	})
	if err != nil {
		roleResolution.compensate(r.Context(), m, ws)
		handler.WriteDomainError(w, err, "create agent record failed")
		return
	}
	agentID := record.ServiceID

	binding, err := m.createPromptAgentBinding(
		r.Context(), authorities.createBinding, ws, agentID, driverRecord, req, plan.roleName, plan.enabled, plan.sourceKind,
	)
	if err != nil {
		// Compensation failures are non-fatal by design (§5: an orphan
		// "unconfigured" agent is legal and deletable) but must never be
		// silent — the serve log is the only trace an operator gets.
		m.compensatePromptAgentBindingFailure(r.Context(), ws, agentID)
		roleResolution.compensate(r.Context(), m, ws)
		writeBindingError(w, err, "create prompt agent binding failed")
		return
	}

	if err := m.provisionPromptAgentGrants(r.Context(), ws, binding.BindingID, req.Grants); err != nil {
		m.compensatePromptAgentGrantFailure(r.Context(), ws, binding.BindingID, agentID, authorities)
		roleResolution.compensate(r.Context(), m, ws)
		handler.WriteDomainError(w, err, "provision connector grants failed")
		return
	}

	out := m.createdPromptAgentResponse(r.Context(), ws, record, binding, time.Now())
	broadcastAgentRefresh(m.hub, ws, out.ID, r.Header.Get("X-Actor"))
	handler.WriteJSON(w, http.StatusCreated, out)
}

func (m *Module) resolvePromptAgentDriver(ctx context.Context, ws string) (*domain.Driver, error) {
	// EnsureBuiltinWorkflow's reuse check is intentionally more than a row
	// lookup: it verifies that the active version still has a staged manifest
	// and executable bundle. The check is a cheap stat-only fast path for a
	// healthy active version, and self-heals (or returns the typed build-toolchain
	// error) when durable FleetDB state outlives its local bundle directory.
	return workflowdefs.EnsureAndResolveDriver(ctx, m.store, ws, workflowdefs.BuiltinPromptAgentWorkflowName)
}

func (m *Module) createPromptAgentBinding(
	ctx context.Context,
	auth authority.OperatorAuthority,
	ws, agentID string,
	driver *domain.Driver,
	req createPromptAgentRequest,
	roleName string,
	enabled bool,
	sourceKind string,
) (*domain.TriggerBinding, error) {
	if m.bindings == nil {
		return nil, automation.ErrUnavailable
	}
	if driver == nil {
		return nil, fmt.Errorf("prompt-agent driver is required: %w", domain.ErrInvalid)
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
	return m.bindings.CreateManagedBinding(ctx, auth, automation.CreateManagedBindingCommand{
		WorkspaceKey: ws, AgentServiceID: agentID,
		Definition: automation.BindingDefinition{
			BindingID: bindingID, Name: firstNonEmpty(strings.TrimSpace(req.Name), roleName, bindingID),
			SourceKind: sourceKind, RouteKey: strings.TrimSpace(req.Trigger.RouteKey), EventTypePatterns: eventPatterns,
			DriverID: driver.DriverID, DriverVersionID: versionID,
			TargetEntrypoint:     firstNonEmpty(strings.TrimSpace(req.Trigger.Entrypoint), "run"),
			TargetAgentServiceID: agentID, Enabled: enabled,
			Schedule: strings.TrimSpace(req.Trigger.Schedule), ScheduleTimezone: strings.TrimSpace(req.Trigger.ScheduleTimezone),
			SourceConfigRef: sourceConfigRef, ConcurrencyPolicy: automation.ConcurrencyOneActivePerEpic,
		},
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
	ws, ok := m.requireCanonicalWorkspace(w, r)
	if !ok {
		return
	}
	id := agentRouteValue(r, "name", "idOrName")
	if supervised, ok, err := m.supervisedByName(r.Context(), ws, id); err != nil {
		writeBindingError(w, err, "get supervised agent failed")
		return
	} else if ok {
		handler.WriteJSON(w, http.StatusOK, newSupervisedAgentDTO(supervised))
		return
	}
	record, err := m.store.AgentServices().Get(r.Context(), ws, id)
	if errors.Is(err, domain.ErrNotFound) {
		binding, ok, bindingErr := m.unattachedBindingByID(r.Context(), ws, id)
		if bindingErr != nil {
			writeBindingError(w, bindingErr, "get legacy binding agent failed")
			return
		}
		if !ok {
			handler.WriteDomainError(w, domain.ErrNotFound, "get agent record failed")
			return
		}
		handler.WriteJSON(w, http.StatusOK, legacyBindingDTO(r.Context(), m.store, ws, binding, time.Now()))
		return
	}
	if err != nil {
		handler.WriteDomainError(w, err, "get agent record failed")
		return
	}
	out, err := m.agentRecordDTO(r.Context(), ws, record, time.Now())
	if err != nil {
		writeBindingError(w, err, "decorate agent record failed")
		return
	}
	handler.WriteJSON(w, http.StatusOK, out)
}

func (m *Module) patchAgent(w http.ResponseWriter, r *http.Request) { //nolint:cyclop,funlen // The endpoint must route three agent kinds before applying record-specific validation.
	if m.store == nil {
		handler.HandleServiceError(w, service.ErrUnavailable("fleet-db store not configured"))
		return
	}
	ws, ok := m.requireCanonicalWorkspace(w, r)
	if !ok {
		return
	}
	id := agentRouteValue(r, "name", "idOrName")
	if m.patchSupervisedAgent(w, r, ws, id) {
		return
	}

	var req patchAgentRecordRequest
	if err := handler.ReadJSON(w, r, &req); err != nil {
		handler.HandleServiceError(w, err)
		return
	}
	existingRecord, recordErr := m.store.AgentServices().Get(r.Context(), ws, id)
	if errors.Is(recordErr, domain.ErrNotFound) {
		m.patchLegacyBindingAgent(w, r, ws, id, req)
		return
	}
	if recordErr != nil {
		handler.WriteDomainError(w, recordErr, "get agent record failed")
		return
	}
	if req.Behavior != nil && req.Behavior.RoleName != nil {
		handler.RespondError(w, http.StatusBadRequest, "behavior.role_name is immutable for managed agent records")
		return
	}
	patch := store.AgentServiceUpdate{}
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

	bindingID, bindingPatch, ok := m.buildAttachedSchedulePatch(w, r, ws, existingRecord.ServiceID, req)
	if !ok {
		return
	}
	hasBindingPatch := bindingPatch.Schedule != nil || bindingPatch.ScheduleTimezone != nil
	if hasBindingPatch && (req.Name != nil || req.Behavior != nil || req.BudgetPolicy != nil) {
		handler.RespondError(w, http.StatusBadRequest, "schedule updates cannot be combined with agent record updates")
		return
	}
	if patch.Name == nil && patch.BudgetPolicy == nil && !hasBindingPatch {
		handler.RespondError(w, http.StatusBadRequest, "no fields to update")
		return
	}
	var updateBindingAuth authority.OperatorAuthority
	if hasBindingPatch {
		var ok bool
		updateBindingAuth, ok = m.resolveBindingAuthority(w, r, ws, automation.ActionUpdateManagedBinding)
		if !ok {
			return
		}
	}
	record := existingRecord
	if patch.Name != nil || patch.BudgetPolicy != nil {
		updatedRecord, updateErr := m.store.AgentServices().Update(r.Context(), ws, existingRecord.ServiceID, patch)
		if updateErr != nil {
			handler.WriteDomainError(w, updateErr, "update agent record failed")
			return
		}
		record = updatedRecord
	}
	if hasBindingPatch {
		if _, err := m.bindings.UpdateManagedBinding(r.Context(), updateBindingAuth, automation.UpdateManagedBindingCommand{
			WorkspaceKey: ws, BindingID: bindingID, AgentServiceID: record.ServiceID, Patch: bindingPatch,
		}); err != nil {
			writeBindingError(w, err, "update attached binding schedule failed")
			return
		}
	}
	out, err := m.agentRecordDTO(r.Context(), ws, record, time.Now())
	if err != nil {
		writeBindingError(w, err, "decorate agent record failed")
		return
	}
	broadcastAgentRefresh(m.hub, ws, out.ID, r.Header.Get("X-Actor"))
	handler.WriteJSON(w, http.StatusOK, out)
}

func (m *Module) patchSupervisedAgent(w http.ResponseWriter, r *http.Request, ws, id string) bool {
	if _, ok, err := m.supervisedByName(r.Context(), ws, id); err != nil {
		writeBindingError(w, err, "get supervised agent failed")
		return true
	} else if ok {
		if m.agentSvc == nil {
			handler.HandleServiceError(w, service.ErrUnavailable("agent service not configured"))
			return true
		}
		if !rejectRecordOnlySupervisedPatchFields(w, r) {
			return true
		}
		HandleUpdate(m.agentSvc, m.hub)(w, r)
		return true
	}
	return false
}

func rejectRecordOnlySupervisedPatchFields(w http.ResponseWriter, r *http.Request) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, handler.MaxRequestBody))
	if err != nil {
		handler.HandleServiceError(w, service.ErrPayloadTooLarge("request body too large (max 1MB)"))
		return false
	}
	resetAgentCreateBody(r, body)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		handler.HandleServiceError(w, service.ErrValidation("invalid request body"))
		return false
	}
	allowed := map[string]struct{}{
		"role_name": {}, "auto": {}, "backend": {}, "fallback_backends": {},
		"repos": {}, "repo_groups": {}, "cross_repo": {}, "parent": {},
		"state": {}, "desired_state": {},
	}
	for field := range fields {
		if _, supported := allowed[field]; !supported {
			handler.RespondError(w, http.StatusBadRequest, field+" is not supported for supervised agents")
			return false
		}
	}
	return true
}

func (m *Module) deleteAgent(w http.ResponseWriter, r *http.Request) { //nolint:cyclop,funlen // Lifecycle deletion keeps kind routing, binding cleanup, and archival visibly ordered.
	if m.store == nil {
		handler.HandleServiceError(w, service.ErrUnavailable("fleet-db store not configured"))
		return
	}
	ws, ok := m.requireCanonicalWorkspace(w, r)
	if !ok {
		return
	}
	id := agentRouteValue(r, "name", "idOrName")
	if _, ok, err := m.supervisedByName(r.Context(), ws, id); err != nil {
		writeBindingError(w, err, "get supervised agent failed")
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
	if errors.Is(err, domain.ErrNotFound) {
		m.deleteLegacyBindingAgent(w, r, ws, id)
		return
	}
	if err != nil {
		handler.WriteDomainError(w, err, "get agent record failed")
		return
	}
	if m.bindings == nil {
		writeBindingError(w, automation.ErrUnavailable, "list attached bindings failed")
		return
	}
	bindings, err := m.bindings.ListBindings(r.Context(), ws, automation.BindingFilter{TargetAgentServiceID: record.ServiceID})
	if err != nil {
		writeBindingError(w, err, "list attached bindings failed")
		return
	}
	bindingsDeleted := 0
	grantsRevoked := 0
	var disableBindingAuth, deleteBindingAuth authority.OperatorAuthority
	if len(bindings) > 0 {
		var ok bool
		disableBindingAuth, ok = m.resolveBindingAuthority(w, r, ws, automation.ActionDisableManagedBinding)
		if !ok {
			return
		}
		deleteBindingAuth, ok = m.resolveBindingAuthority(w, r, ws, automation.ActionDeleteManagedBinding)
		if !ok {
			return
		}
	}
	for _, b := range bindings {
		if b == nil {
			continue
		}
		result, err := m.deleteManagedBinding(
			r.Context(), ws, b.BindingID, record.ServiceID, disableBindingAuth, deleteBindingAuth,
		)
		if err != nil {
			writeBindingError(w, err, "delete attached binding failed")
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
		writeBindingError(w, err, "decorate archived agent record failed")
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

func (m *Module) unattachedBindingByID(ctx context.Context, ws, id string) (*domain.TriggerBinding, bool, error) {
	if m.bindings == nil {
		return nil, false, automation.ErrUnavailable
	}
	binding, err := m.bindings.GetBinding(ctx, ws, id)
	if bindingNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(binding.TargetAgentServiceID) != "" {
		return nil, false, nil
	}
	return binding, true, nil
}

func (m *Module) patchLegacyBindingAgent(w http.ResponseWriter, r *http.Request, ws, id string, req patchAgentRecordRequest) {
	binding, ok, err := m.unattachedBindingByID(r.Context(), ws, id)
	if err != nil {
		writeBindingError(w, err, "get legacy binding agent failed")
		return
	}
	if !ok {
		handler.WriteDomainError(w, domain.ErrNotFound, "get agent record failed")
		return
	}
	if req.Name == nil || req.Behavior != nil || req.BudgetPolicy != nil ||
		req.BindingID != nil || req.Schedule != nil || req.ScheduleTimezone != nil {
		handler.RespondError(w, http.StatusBadRequest, "legacy binding agents only support name updates")
		return
	}
	name := strings.TrimSpace(*req.Name)
	if name == "" {
		handler.RespondError(w, http.StatusBadRequest, "name cannot be empty")
		return
	}
	auth, ok := m.resolveBindingAuthority(w, r, ws, automation.ActionUpdateBinding)
	if !ok {
		return
	}
	updated, err := m.bindings.UpdateBinding(r.Context(), auth, automation.UpdateBindingCommand{
		WorkspaceKey: ws, BindingID: binding.BindingID, Patch: automation.BindingPatch{Name: &name},
	})
	if err != nil {
		writeBindingError(w, err, "update legacy binding agent failed")
		return
	}
	broadcastAgentRefresh(m.hub, ws, updated.BindingID, r.Header.Get("X-Actor"))
	handler.WriteJSON(w, http.StatusOK, legacyBindingDTO(r.Context(), m.store, ws, updated, time.Now()))
}

func (m *Module) deleteLegacyBindingAgent(w http.ResponseWriter, r *http.Request, ws, id string) {
	binding, ok, err := m.unattachedBindingByID(r.Context(), ws, id)
	if err != nil {
		writeBindingError(w, err, "get legacy binding agent failed")
		return
	}
	if !ok {
		handler.WriteDomainError(w, domain.ErrNotFound, "get agent record failed")
		return
	}
	disableAuth, ok := m.resolveBindingAuthority(w, r, ws, automation.ActionDisableBinding)
	if !ok {
		return
	}
	deleteAuth, ok := m.resolveBindingAuthority(w, r, ws, automation.ActionDeleteBinding)
	if !ok {
		return
	}
	result, err := m.deleteUnmanagedBinding(r.Context(), ws, binding.BindingID, disableAuth, deleteAuth)
	if err != nil {
		writeBindingError(w, err, "delete legacy binding agent failed")
		return
	}
	broadcastAgentRefresh(m.hub, ws, binding.BindingID, r.Header.Get("X-Actor"))
	handler.WriteJSON(w, http.StatusOK, result)
}

func (m *Module) requireAgentRunsStore(w http.ResponseWriter) bool {
	if m.store != nil {
		return true
	}
	handler.HandleServiceError(w, service.ErrUnavailable("fleet-db store not configured"))
	return false
}

func (m *Module) listAgentSessionsForHistory(
	ctx context.Context,
	ws, agentID string,
	limit int,
) ([]*domain.AgentSession, error) {
	return m.store.AgentSessions().List(ctx, ws, store.AgentSessionFilter{AgentID: agentID, Limit: limit})
}

func (m *Module) agentServiceForHistory(ctx context.Context, ws, id string) (*domain.AgentService, error) {
	return m.store.AgentServices().Get(ctx, ws, id)
}

func (m *Module) listAgentServiceRunsForHistory(
	ctx context.Context,
	ws, agentID string,
	limit int,
) ([]*domain.DriverRun, error) {
	return m.store.DriverRuns().List(ctx, ws, store.DriverRunFilter{AgentServiceID: agentID, Limit: limit})
}

func (m *Module) listBindingRunsForHistory(
	ctx context.Context,
	ws, bindingID string,
	limit int,
) ([]*domain.DriverRun, error) {
	return m.store.DriverRuns().List(ctx, ws, store.DriverRunFilter{BindingID: bindingID, Limit: limit})
}

func (m *Module) setRecordEnabled(enabled bool) http.HandlerFunc { //nolint:funlen // The handler keeps authority, record state, and attached-binding updates in one ordered transaction-like flow.
	return func(w http.ResponseWriter, r *http.Request) {
		if m.store == nil {
			handler.HandleServiceError(w, service.ErrUnavailable("fleet-db store not configured"))
			return
		}
		ws, ok := m.requireCanonicalWorkspace(w, r)
		if !ok {
			return
		}
		id := agentRouteValue(r, "id", "name")
		if _, ok, err := m.supervisedByName(r.Context(), ws, id); err != nil {
			writeBindingError(w, err, "get supervised agent failed")
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
		action := automation.ActionDisableManagedBinding
		if enabled {
			action = automation.ActionEnableManagedBinding
		}
		bindingAuth, ok := m.resolveBindingAuthority(w, r, ws, action)
		if !ok {
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
		if err := m.setAttachedBindingsEnabled(r.Context(), bindingAuth, ws, record.ServiceID, enabled); err != nil {
			writeBindingError(w, err, "update attached bindings failed")
			return
		}
		out, err := m.agentRecordDTO(r.Context(), ws, updated, time.Now())
		if err != nil {
			writeBindingError(w, err, "decorate agent record failed")
			return
		}
		broadcastAgentRefresh(m.hub, ws, out.ID, r.Header.Get("X-Actor"))
		handler.WriteJSON(w, http.StatusOK, out)
	}
}

func (m *Module) supervisedByName(ctx context.Context, ws, name string) (*domain.Agent, bool, error) {
	if m.store == nil || strings.TrimSpace(name) == "" {
		return nil, false, nil
	}
	agent, agentErr := m.store.Agents().Get(ctx, ws, name)
	if agentErr != nil && !errors.Is(agentErr, domain.ErrNotFound) {
		return nil, false, agentErr
	}
	agentExists := agentErr == nil && agent != nil

	record, recordErr := m.store.AgentServices().Get(ctx, ws, name)
	if recordErr != nil && !errors.Is(recordErr, domain.ErrNotFound) {
		return nil, false, recordErr
	}
	recordExists := recordErr == nil && record != nil

	_, bindingExists, bindingErr := m.unattachedBindingByID(ctx, ws, name)
	if bindingErr != nil {
		return nil, false, bindingErr
	}

	switch {
	case agentExists && recordExists:
		return nil, false, agentIdentifierCollisionError(name, "a supervised agent", "an agent record")
	case agentExists && bindingExists:
		return nil, false, agentIdentifierCollisionError(name, "a supervised agent", "a legacy binding agent")
	case recordExists && bindingExists:
		return nil, false, agentIdentifierCollisionError(name, "an agent record", "a legacy binding agent")
	case agentExists:
		return agent, true, nil
	default:
		return nil, false, nil
	}
}
