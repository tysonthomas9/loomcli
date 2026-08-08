package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/domain"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	rolehandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/roles"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

func resetAgentCreateBody(r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
}

func withoutJSONField(body []byte, field string) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	delete(payload, field)
	return json.Marshal(payload)
}

func (m *Module) resolvePromptAgentRoleForCreate(
	w http.ResponseWriter,
	r *http.Request,
	ws string,
	plan promptAgentCreatePlan,
) (agentprovisioning.RoleSpec, bool) {
	roleCreate := plan.request.Behavior.RoleCreate
	if roleCreate == nil {
		role, err := m.store.Roles().Get(r.Context(), ws, plan.roleName)
		if err != nil {
			handler.WriteDomainError(w, err, "get prompt-agent role failed")
			return agentprovisioning.RoleSpec{}, false
		}
		role, ok := validatePromptAgentRoleForCreate(w, role)
		if !ok {
			return agentprovisioning.RoleSpec{}, false
		}
		return promptAgentRoleSpec(role), true
	}
	// A new definition remains pure request intent here. AgentProvisioning
	// durably records the complete specification before Agents owns the exact
	// idempotent Role ensure. Inline prompt persistence replaces the legacy
	// pre-commit filesystem write; PromptFilename is a UI naming hint only.
	role := &domain.Role{
		WorkspaceKey: ws, Name: plan.roleName,
		Description: roleCreate.Description, Prompt: roleCreate.Prompt,
		Model: roleCreate.Model, TaskFilter: roleCreate.TaskFilter,
		Backend: roleCreate.Backend, Effort: roleCreate.Effort,
		ReadOnly:     roleCreate.ReadOnly,
		AllowedTools: append([]string(nil), roleCreate.AllowedTools...),
		DeniedTools:  append([]string(nil), roleCreate.DeniedTools...),
		Skills:       append([]string(nil), roleCreate.Skills...),
	}
	if err := rolehandlers.ValidatePromptAgentRole(role); err != nil {
		handler.WriteDomainError(w, err, "invalid prompt-agent role")
		return agentprovisioning.RoleSpec{}, false
	}
	return promptAgentRoleSpec(role), true
}

func validatePromptAgentRoleForCreate(w http.ResponseWriter, role *domain.Role) (*domain.Role, bool) {
	if err := rolehandlers.ValidatePromptAgentRole(role); err != nil {
		handler.WriteDomainError(w, err, "invalid prompt-agent role")
		return nil, false
	}
	return role, true
}

func promptAgentRoleSpec(role *domain.Role) agentprovisioning.RoleSpec {
	if role == nil {
		return agentprovisioning.RoleSpec{}
	}
	return agentprovisioning.RoleSpec{
		Name: role.Name, Kind: string(role.Kind), Description: role.Description,
		Prompt: role.Prompt, PromptFile: role.PromptFile, Model: role.Model,
		TaskFilter: role.TaskFilter, Backend: role.Backend, Effort: role.Effort,
		PathPatterns: append([]string(nil), role.PathPatterns...),
		Skills:       append([]string(nil), role.Skills...),
		MaxPriority:  clonePromptAgentInt(role.MaxPriority),
		MaxConcurrency: clonePromptAgentInt(
			role.MaxConcurrency,
		),
		ReadOnly:     role.ReadOnly,
		AllowedTools: append([]string(nil), role.AllowedTools...),
		DeniedTools:  append([]string(nil), role.DeniedTools...),
		MaxBudgetUSD: clonePromptAgentFloat64(role.MaxBudgetUSD),
	}
}

func (m *Module) promptAgentProvisioningSpec(
	ctx context.Context,
	workspace string,
	plan promptAgentCreatePlan,
	role agentprovisioning.RoleSpec,
	driver *workflowcatalog.Driver,
) (agentprovisioning.Spec, error) {
	if m == nil || m.store == nil || driver == nil {
		return agentprovisioning.Spec{}, agentprovisioning.ErrUnavailable
	}
	versionID, err := activePromptAgentVersion(driver)
	if err != nil {
		return agentprovisioning.Spec{}, err
	}
	agentID, err := m.mintAvailablePromptAgentID(
		ctx,
		workspace,
		firstNonEmpty(plan.request.Name, plan.roleName),
	)
	if err != nil {
		return agentprovisioning.Spec{}, err
	}
	bindingID := promptAgentBindingID(plan, agentID)
	sourceConfigRef, err := promptAgentSourceConfigRef(
		plan.roleName,
		plan.request.Backend,
	)
	if err != nil {
		return agentprovisioning.Spec{}, err
	}
	eventPatterns := promptAgentEventPatterns(plan)
	agentSpec, err := promptAgentServiceSpec(plan, role, agentID)
	if err != nil {
		return agentprovisioning.Spec{}, err
	}
	return agentprovisioning.Spec{
		ProvisioningID: "provision-" + agentID,
		WorkspaceKey:   workspace,
		Role:           role,
		Agent:          agentSpec,
		Binding: promptAgentBindingSpec(
			plan,
			bindingID,
			sourceConfigRef,
			eventPatterns,
			driver,
			versionID,
		),
		Grants: promptAgentGrantSpecs(bindingID, plan.request.Grants),
	}, nil
}

func activePromptAgentVersion(driver *workflowcatalog.Driver) (string, error) {
	versionID := strings.TrimSpace(driver.ActiveVersionID)
	if strings.TrimSpace(driver.DriverID) == "" || versionID == "" {
		return "", fmt.Errorf("prompt-agent driver requires an active version: %w", agentprovisioning.ErrInvalid)
	}
	return versionID, nil
}

func promptAgentBindingID(plan promptAgentCreatePlan, agentID string) string {
	if bindingID := strings.TrimSpace(plan.request.Trigger.BindingID); bindingID != "" {
		return bindingID
	}
	return agentID + "-1"
}

func promptAgentEventPatterns(plan promptAgentCreatePlan) []string {
	eventPatterns := append(
		[]string(nil),
		plan.request.Trigger.EventTypePatterns...,
	)
	if plan.sourceKind == "internal" && len(eventPatterns) == 0 {
		return []string{"internal.task.ready"}
	}
	return eventPatterns
}

func promptAgentServiceSpec(
	plan promptAgentCreatePlan,
	role agentprovisioning.RoleSpec,
	agentID string,
) (agentprovisioning.AgentSpec, error) {
	metadata, err := agentsmodule.WithRuntimeMetadata(nil, agentsmodule.RuntimeMetadata{
		RoleKind: strings.TrimSpace(role.Kind), Backend: strings.TrimSpace(plan.request.Backend),
	})
	if err != nil {
		return agentprovisioning.AgentSpec{}, err
	}
	return agentprovisioning.AgentSpec{
		AgentID: agentID, Name: firstNonEmpty(plan.request.Name, plan.roleName),
		Kind:         string(agentServiceKindForSource(plan.sourceKind)),
		DesiredState: string(plan.desired), RoleName: plan.roleName,
		BudgetPolicy: plan.request.BudgetPolicy, Metadata: metadata,
	}, nil
}

func promptAgentBindingSpec(
	plan promptAgentCreatePlan,
	bindingID,
	sourceConfigRef string,
	eventPatterns []string,
	driver *workflowcatalog.Driver,
	versionID string,
) agentprovisioning.BindingSpec {
	return agentprovisioning.BindingSpec{
		BindingID: bindingID,
		Name: firstNonEmpty(
			plan.request.Name,
			plan.roleName,
			bindingID,
		),
		SourceKind: plan.sourceKind, SourceConfigRef: sourceConfigRef,
		RouteKey:      strings.TrimSpace(plan.request.Trigger.RouteKey),
		EventPatterns: eventPatterns,
		DriverID:      driver.DriverID, DriverVersionID: versionID,
		Entrypoint:        firstNonEmpty(plan.request.Trigger.Entrypoint, "run"),
		ConcurrencyPolicy: string(automation.ConcurrencyOneActivePerEpic),
		Schedule:          plan.request.Trigger.Schedule,
		ScheduleZone:      plan.request.Trigger.ScheduleTimezone,
		Enabled:           plan.enabled,
	}
}

func promptAgentGrantSpecs(
	bindingID string,
	requests []promptAgentGrantRequest,
) []agentprovisioning.GrantSpec {
	grants := make([]agentprovisioning.GrantSpec, 0, len(requests))
	for _, request := range requests {
		action := strings.TrimSpace(request.Action)
		grantID := strings.TrimSpace(request.GrantID)
		if grantID == "" {
			grantID = "grant-" + bindingID + "-" + strings.ReplaceAll(action, ".", "-")
		}
		grants = append(grants, agentprovisioning.GrantSpec{
			GrantID: grantID, ConnectorID: request.ConnectorID,
			Action: action, ResourcePattern: request.ResourcePattern,
		})
	}
	return grants
}

func (m *Module) mintAvailablePromptAgentID(
	ctx context.Context,
	workspace,
	name string,
) (string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		agentID, err := mintAgentRecordID(name)
		if err != nil {
			return "", err
		}
		if _, err := m.store.AgentServices().Get(ctx, workspace, agentID); err == nil {
			continue
		} else if !errors.Is(err, domain.ErrNotFound) {
			return "", err
		}
		return agentID, nil
	}
	return "", fmt.Errorf(
		"mint unique agent id in workspace %q: %w",
		workspace,
		agentprovisioning.ErrConflict,
	)
}

func promptAgentCommittedProjection(
	record *agentprovisioning.Record,
	now time.Time,
) (*domain.AgentService, *automation.Binding, error) {
	if record == nil || record.State != agentprovisioning.StateCompleted ||
		record.Spec.Agent.AgentID == "" || record.Spec.Binding.BindingID == "" {
		return nil, nil, fmt.Errorf(
			"AgentProvisioning returned incomplete terminal state: %w",
			agentprovisioning.ErrConflict,
		)
	}
	createdAt, updatedAt := promptAgentProjectionTimes(record, now)
	agent := &domain.AgentService{
		WorkspaceKey: record.WorkspaceKey,
		ServiceID:    record.Spec.Agent.AgentID,
		Name:         record.Spec.Agent.Name,
		Kind:         domain.AgentServiceKind(record.Spec.Agent.Kind),
		DesiredState: domain.AgentServiceDesiredState(record.Spec.Agent.DesiredState),
		RoleName:     record.Spec.Agent.RoleName,
		MaxInstances: 1,
		BudgetPolicy: record.Spec.Agent.BudgetPolicy,
		Metadata:     clonePromptAgentMap(record.Spec.Agent.Metadata),
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}
	binding := &automation.Binding{
		WorkspaceKey:    record.WorkspaceKey,
		BindingID:       record.Spec.Binding.BindingID,
		Name:            record.Spec.Binding.Name,
		SourceKind:      record.Spec.Binding.SourceKind,
		SourceConfigRef: record.Spec.Binding.SourceConfigRef,
		RouteKey:        record.Spec.Binding.RouteKey,
		EventTypePatterns: append(
			[]string(nil),
			record.Spec.Binding.EventPatterns...,
		),
		DriverID:             record.Spec.Binding.DriverID,
		DriverVersionID:      record.Spec.Binding.DriverVersionID,
		TargetEntrypoint:     record.Spec.Binding.Entrypoint,
		TargetAgentServiceID: record.Spec.Agent.AgentID,
		ConcurrencyPolicy: automation.BindingConcurrencyPolicy(
			record.Spec.Binding.ConcurrencyPolicy,
		),
		Schedule:         record.Spec.Binding.Schedule,
		ScheduleTimezone: record.Spec.Binding.ScheduleZone,
		Enabled:          record.Spec.Binding.Enabled,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
	}
	return agent, binding, nil
}

func promptAgentProjectionTimes(
	record *agentprovisioning.Record,
	now time.Time,
) (time.Time, time.Time) {
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	return createdAt, updatedAt
}

func writeAgentProvisioningError(
	w http.ResponseWriter,
	err error,
	fallback string,
) {
	switch {
	case errors.Is(err, agentprovisioning.ErrInvalid),
		errors.Is(err, domain.ErrInvalid):
		handler.RespondError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, agentprovisioning.ErrNotFound),
		errors.Is(err, domain.ErrNotFound):
		handler.RespondError(w, http.StatusNotFound, fallback)
	case errors.Is(err, agentprovisioning.ErrConflict),
		errors.Is(err, agentprovisioning.ErrConcurrentWrite),
		errors.Is(err, agentprovisioning.ErrInvalidTransition),
		errors.Is(err, agentprovisioning.ErrPermanentFailure),
		errors.Is(err, domain.ErrAlreadyExists),
		errors.Is(err, domain.ErrConflict):
		handler.RespondError(w, http.StatusConflict, err.Error())
	case errors.Is(err, agentprovisioning.ErrUnavailable),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		handler.RespondError(w, http.StatusServiceUnavailable, fallback)
	default:
		writeBindingError(w, err, fallback)
	}
}

func clonePromptAgentInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePromptAgentFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePromptAgentMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	cloned := make(map[string]string, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}
