package agents

import (
	"context"
	"crypto/sha256"
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
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

func (m *Module) listAgents(w http.ResponseWriter, r *http.Request) {
	ws, ok := m.requireCanonicalWorkspace(w, r)
	if !ok {
		return
	}
	now := time.Now()
	items := []any{}

	includeArchived := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include")), "archived")
	records, err := m.listAgentRecords(r.Context(), ws, includeArchived)
	if err != nil {
		writeAgentRecordError(w, err, "list agent records failed")
		return
	}
	for _, record := range records {
		if record == nil {
			continue
		}
		dto, err := m.agentRecordDTO(r.Context(), ws, record, now)
		if err != nil {
			writeBindingError(w, err, "decorate agent record failed")
			return
		}
		items = append(items, dto)
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
		handler.HandleServiceError(w, apperrors.ErrPayloadTooLarge("request body too large (max 1MB)"))
		return
	}
	resetAgentCreateBody(r, body)

	var probe createAgentKindProbe
	if err := handler.DecodeOneJSONBytes(body, &probe, handler.JSONDecodeOptions{
		MaxBytes: handler.MaxRequestBody,
	}); err != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	switch strings.ToLower(strings.TrimSpace(probe.Kind)) {
	case "", string(domain.RoleKindInteractive):
		m.createCanonicalInteractiveAgent(w, r, ws, body)
	case string(domain.RoleKindWorker):
		handler.RespondError(w, http.StatusBadRequest, "background agents must be created through AgentProvisioning")
	case "supervised":
		handler.RespondError(w, http.StatusBadRequest, "supervised agents were retired; create an interactive agent or a managed prompt agent")
	case agentRecordKindPrompt:
		m.createPromptAgent(w, r, body)
	default:
		handler.RespondError(w, http.StatusBadRequest, "unsupported agent kind: "+probe.Kind)
	}
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
	if err := handler.DecodeOneJSONBytes(body, &req, handler.JSONDecodeOptions{
		MaxBytes: handler.MaxRequestBody,
	}); err != nil {
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
		handler.HandleServiceError(w, apperrors.ErrUnavailable("fleet-db store not configured"))
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
	ws, ok := m.requireCanonicalWorkspace(w, r)
	if !ok {
		return
	}
	id := agentRouteValue(r, "name", "idOrName")
	record, err := m.getAgentRecord(r.Context(), ws, id)
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
	ws, ok := m.requireCanonicalWorkspace(w, r)
	if !ok {
		return
	}
	id := agentRouteValue(r, "name", "idOrName")
	var req patchAgentRecordRequest
	if err := handler.ReadJSON(w, r, &req); err != nil {
		handler.HandleServiceError(w, err)
		return
	}
	existingRecord, recordErr := m.getAgentRecord(r.Context(), ws, id)
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

func (m *Module) deleteAgent(w http.ResponseWriter, r *http.Request) { //nolint:cyclop,funlen // Keep unified kind routing and the canonical Fleet delete transaction contiguous.
	ws, ok := m.requireCanonicalWorkspace(w, r)
	if !ok {
		return
	}
	id := agentRouteValue(r, "name", "idOrName")
	record, err := m.getAgentRecord(r.Context(), ws, id)
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

func (m *Module) requireAgentRunsStore(w http.ResponseWriter) bool {
	if m.store != nil {
		return true
	}
	handler.HandleServiceError(w, apperrors.ErrUnavailable("fleet-db store not configured"))
	return false
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

//nolint:funlen // Keep kind routing, authority checks, and desired-state mutation in one lifecycle endpoint transaction.
func (m *Module) setRecordEnabled(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.store == nil {
			handler.HandleServiceError(w, apperrors.ErrUnavailable("fleet-db store not configured"))
			return
		}
		ws, ok := m.requireCanonicalWorkspace(w, r)
		if !ok {
			return
		}
		id := agentRouteValue(r, "id", "name")
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
