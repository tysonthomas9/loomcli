package agents

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/domain"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/infra/workflowdistribution/authoring"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
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
	records, err := m.listAgentRecords(r.Context(), ws, true)
	if err != nil {
		writeAgentRecordError(w, err, "list agent records failed")
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
	if _, err := m.getAgentRecord(ctx, ws, id); err == nil {
		handler.RespondError(w, http.StatusConflict, "agent identifier is already used by an agent record")
		return true
	} else if !errors.Is(err, agentsmodule.ErrNotFound) {
		writeAgentRecordError(w, err, "check agent identifier failed")
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

func (m *Module) createSupervisedAgent(w http.ResponseWriter, r *http.Request) {
	if m.agentSvc == nil {
		handler.HandleServiceError(w, service.ErrUnavailable("agent service not configured"))
		return
	}
	authorized, ok := m.withSupervisedOperatorAuthority(
		w,
		r,
		agentsmodule.ActionCreateSupervisedAssignment,
	)
	if !ok {
		return
	}
	HandleCreate(m.agentSvc, m.hub)(w, authorized)
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

func (m *Module) resolvePromptAgentDriverForCreate(
	w http.ResponseWriter,
	r *http.Request,
	ws string,
) (*workflowcatalog.Driver, bool) {
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

//nolint:funlen // Keep prompt-agent validation, authority resolution, provisioning, and compatibility projection in one compensating transaction.
func (m *Module) createPromptAgent(w http.ResponseWriter, r *http.Request, body []byte) {
	if m.store == nil || m.provisioning == nil || m.provisioningAuthority == nil {
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
	auth, err := m.provisioningAuthority.ResolveOperatorAuthority(
		r,
		ws,
		agentprovisioning.ActionBeginProvisioning,
	)
	if err != nil {
		writeAgentProvisioningError(w, err, "prompt-agent provisioning authority denied")
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
	role, ok := m.resolvePromptAgentRoleForCreate(w, r, ws, plan)
	if !ok {
		return
	}
	spec, err := m.promptAgentProvisioningSpec(
		r.Context(),
		ws,
		plan,
		role,
		driverRecord,
	)
	if err != nil {
		writeAgentProvisioningError(w, err, "build prompt-agent provisioning intent failed")
		return
	}
	durable, err := m.provisioning.Begin(r.Context(), auth, spec)
	if err != nil {
		writeAgentProvisioningError(w, err, "begin prompt-agent provisioning failed")
		return
	}
	if durable == nil || strings.TrimSpace(durable.ProvisioningID) == "" {
		writeAgentProvisioningError(
			w,
			agentprovisioning.ErrConflict,
			"begin prompt-agent provisioning returned invalid state",
		)
		return
	}
	completed, err := m.provisioning.Run(
		r.Context(),
		ws,
		durable.ProvisioningID,
	)
	if err != nil {
		writeAgentProvisioningError(w, err, "run prompt-agent provisioning failed")
		return
	}
	record, binding, err := promptAgentCommittedProjection(completed, time.Now())
	if err != nil {
		writeAgentProvisioningError(w, err, "read prompt-agent provisioning result failed")
		return
	}
	out := m.createdPromptAgentResponse(r.Context(), ws, record, binding, time.Now())
	broadcastAgentRefresh(m.hub, ws, out.ID, r.Header.Get("X-Actor"))
	handler.WriteJSON(w, http.StatusCreated, out)
}

func (m *Module) resolvePromptAgentDriver(ctx context.Context, ws string) (*workflowcatalog.Driver, error) {
	if m == nil || m.prepareWorkflowTarget == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	// The composition-provided target preparation verifies reusable bundle
	// placement and self-heals the managed builtin through Workflow Catalog's
	// atomic authoring port before returning its active durable identity.
	return m.prepareWorkflowTarget(ctx, ws, workflowdefs.BuiltinPromptAgentWorkflowName)
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
		writeUnifiedAgentLookupError(w, err)
		return
	} else if ok {
		handler.WriteJSON(w, http.StatusOK, newSupervisedAgentDTO(supervised))
		return
	}
	record, err := m.getAgentRecord(r.Context(), ws, id)
	if errors.Is(err, agentsmodule.ErrNotFound) {
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
		writeAgentRecordError(w, err, "get agent record failed")
		return
	}
	out, err := m.agentRecordDTO(r.Context(), ws, record, time.Now())
	if err != nil {
		writeBindingError(w, err, "decorate agent record failed")
		return
	}
	handler.WriteJSON(w, http.StatusOK, out)
}

func (m *Module) patchAgent(w http.ResponseWriter, r *http.Request) { //nolint:cyclop,funlen,gocognit // Keep multi-aggregate validation, authority, and mutation ordering contiguous to prevent partial Agent or schedule updates.
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
	existingRecord, recordErr := m.getAgentRecord(r.Context(), ws, id)
	if errors.Is(recordErr, agentsmodule.ErrNotFound) {
		m.patchLegacyBindingAgent(w, r, ws, id, req)
		return
	}
	if recordErr != nil {
		writeAgentRecordError(w, recordErr, "get agent record failed")
		return
	}
	if req.Behavior != nil && req.Behavior.RoleName != nil {
		handler.RespondError(w, http.StatusBadRequest, "behavior.role_name is immutable for managed agent records")
		return
	}
	patch := agentsmodule.AgentPatch{}
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
		updateAuth, ok := m.resolveAgentRecordAuthority(w, r, ws, agentsmodule.ActionUpdateAgent)
		if !ok {
			return
		}
		updatedRecord, updateErr := m.agentRecords.UpdateAgent(
			r.Context(),
			updateAuth,
			agentsmodule.UpdateAgentCommand{
				WorkspaceKey:      ws,
				AgentID:           existingRecord.ServiceID,
				ExpectedUpdatedAt: existingRecord.UpdatedAt,
				Patch:             patch,
			},
		)
		if updateErr != nil {
			writeAgentRecordError(w, updateErr, "update agent record failed")
			return
		}
		record = canonicalAgentServiceProjection(updatedRecord)
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
		writeUnifiedAgentLookupError(w, err)
		return true
	} else if ok {
		if m.agentSvc == nil {
			handler.HandleServiceError(w, service.ErrUnavailable("agent service not configured"))
			return true
		}
		if !rejectRecordOnlySupervisedPatchFields(w, r) {
			return true
		}
		authorized, ok := m.withSupervisedOperatorAuthority(
			w,
			r,
			agentsmodule.ActionUpdateSupervisedAssignmentIntent,
		)
		if !ok {
			return true
		}
		HandleUpdate(m.agentSvc, m.hub)(w, authorized)
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
		"repos": {}, "repo_groups": {}, "cross_repo": {}, "desired_state": {},
	}
	for field := range fields {
		if _, supported := allowed[field]; !supported {
			handler.RespondError(w, http.StatusBadRequest, field+" is not supported for supervised agents")
			return false
		}
	}
	return true
}

func (m *Module) deleteAgent(w http.ResponseWriter, r *http.Request) { //nolint:cyclop,funlen // Keep unified kind routing and the canonical Fleet delete transaction contiguous.
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
		writeUnifiedAgentLookupError(w, err)
		return
	} else if ok {
		if m.agentSvc == nil {
			handler.HandleServiceError(w, service.ErrUnavailable("agent service not configured"))
			return
		}
		authorized, authorizedOK := m.withSupervisedOperatorAuthority(
			w,
			r,
			agentsmodule.ActionRetireSupervisedAssignment,
		)
		if !authorizedOK {
			return
		}
		HandleDelete(m.agentSvc, m.hub)(w, authorized)
		return
	}

	record, err := m.getAgentRecord(r.Context(), ws, id)
	if errors.Is(err, agentsmodule.ErrNotFound) {
		m.deleteLegacyBindingAgent(w, r, ws, id)
		return
	}
	if err != nil {
		writeAgentRecordError(w, err, "get agent record failed")
		return
	}
	if m.agentLifecycle == nil {
		writeAgentRecordError(w, agentsmodule.ErrUnavailable, "agent lifecycle is unavailable")
		return
	}
	lifecycleAuth, ok := m.resolveAgentRecordAuthority(w, r, ws, agentsmodule.ActionApplyLifecycle)
	if !ok {
		return
	}
	result, err := m.agentLifecycle.ApplyLifecycle(
		r.Context(),
		lifecycleAuth,
		agentsmodule.ApplyLifecycleCommand{
			WorkspaceKey:         ws,
			AgentID:              record.ServiceID,
			Action:               agentsmodule.LifecycleDelete,
			ExpectedUpdatedAt:    record.UpdatedAt,
			ExpectedGenerationID: record.GenerationID,
			IdempotencyKey: agentLifecycleIdempotencyKey(
				ws, record.ServiceID, agentsmodule.LifecycleDelete, record.UpdatedAt,
			),
		},
	)
	if err != nil {
		writeAgentRecordError(w, err, "delete agent record failed")
		return
	}
	archived := canonicalAgentServiceProjection(result.Agent)
	out, err := m.agentRecordDTO(r.Context(), ws, archived, time.Now())
	if err != nil {
		writeBindingError(w, err, "decorate archived agent record failed")
		return
	}
	broadcastAgentRefresh(m.hub, ws, out.ID, r.Header.Get("X-Actor"))
	handler.WriteJSON(w, http.StatusOK, map[string]any{
		"agent":            out,
		"archived":         true,
		"bindings_deleted": len(result.BindingIDs),
		"grants_revoked":   len(result.GrantIDs),
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

func (m *Module) patchLegacyBindingAgent(
	w http.ResponseWriter,
	r *http.Request,
	ws, id string,
	req patchAgentRecordRequest,
) {
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
	result, err := m.deleteUnmanagedBinding(
		r.Context(),
		ws,
		binding.BindingID,
		disableAuth,
		deleteAuth,
	)
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
	return m.getAgentRecord(ctx, ws, id)
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

//nolint:funlen // Keep kind routing, authority checks, and desired-state mutation in one lifecycle endpoint transaction.
func (m *Module) setRecordEnabled(enabled bool) http.HandlerFunc {
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
			writeUnifiedAgentLookupError(w, err)
			return
		} else if ok {
			handler.RespondError(w, http.StatusBadRequest, "supervised agents use start/stop lifecycle actions")
			return
		}
		record, err := m.getAgentRecord(r.Context(), ws, id)
		if err != nil {
			writeAgentRecordError(w, err, "get agent record failed")
			return
		}
		if isAgentRecordArchived(record) {
			handler.RespondError(w, http.StatusConflict, "agent is archived")
			return
		}
		if m.agentLifecycle == nil {
			writeAgentRecordError(w, agentsmodule.ErrUnavailable, "agent lifecycle is unavailable")
			return
		}
		action := agentsmodule.LifecycleDisable
		if enabled {
			action = agentsmodule.LifecycleEnable
		}
		lifecycleAuth, ok := m.resolveAgentRecordAuthority(w, r, ws, agentsmodule.ActionApplyLifecycle)
		if !ok {
			return
		}
		result, err := m.agentLifecycle.ApplyLifecycle(
			r.Context(),
			lifecycleAuth,
			agentsmodule.ApplyLifecycleCommand{
				WorkspaceKey: ws, AgentID: record.ServiceID, Action: action,
				ExpectedUpdatedAt:    record.UpdatedAt,
				ExpectedGenerationID: record.GenerationID,
				IdempotencyKey: agentLifecycleIdempotencyKey(
					ws, record.ServiceID, action, record.UpdatedAt,
				),
			},
		)
		if err != nil {
			writeAgentRecordError(w, err, "apply agent lifecycle failed")
			return
		}
		updatedRecord := canonicalAgentServiceProjection(result.Agent)
		out, err := m.agentRecordDTO(r.Context(), ws, updatedRecord, time.Now())
		if err != nil {
			writeBindingError(w, err, "decorate agent record failed")
			return
		}
		broadcastAgentRefresh(m.hub, ws, out.ID, r.Header.Get("X-Actor"))
		handler.WriteJSON(w, http.StatusOK, out)
	}
}

func agentLifecycleIdempotencyKey(
	workspace,
	agentID string,
	action agentsmodule.LifecycleAction,
	expectedUpdatedAt time.Time,
) string {
	payload := workspace + "\x00" + agentID + "\x00" + string(action) + "\x00" +
		expectedUpdatedAt.UTC().Format(time.RFC3339Nano)
	digest := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("webui-agent-lifecycle-%x", digest[:16])
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

	record, recordErr := m.getAgentRecord(ctx, ws, name)
	if recordErr != nil && !errors.Is(recordErr, agentsmodule.ErrNotFound) {
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
