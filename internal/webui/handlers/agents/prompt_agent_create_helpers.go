package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	rolehandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/roles"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

type promptAgentCreateAuthorities struct {
	createBinding  authority.OperatorAuthority
	disableBinding authority.OperatorAuthority
	deleteBinding  authority.OperatorAuthority
}

type promptAgentRoleResolution struct {
	role    *domain.Role
	receipt *rolehandlers.EnsureRoleResult
}

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

func (resolution promptAgentRoleResolution) compensate(ctx context.Context, m *Module, ws string) {
	if resolution.receipt == nil {
		return
	}
	if err := resolution.receipt.Compensate(context.WithoutCancel(ctx), m.store, ws); err != nil {
		slog.Warn("prompt agent create: role compensation failed",
			"workspace", ws, "role", resolution.role.Name, "err", err)
	}
}

func (m *Module) resolvePromptAgentCreateAuthorities(
	w http.ResponseWriter,
	r *http.Request,
	ws string,
	hasGrants bool,
) (promptAgentCreateAuthorities, bool) {
	var out promptAgentCreateAuthorities
	var ok bool
	out.createBinding, ok = m.resolveBindingAuthority(w, r, ws, automation.ActionCreateManagedBinding)
	if !ok {
		return promptAgentCreateAuthorities{}, false
	}
	if !hasGrants {
		return out, true
	}
	out.disableBinding, ok = m.resolveBindingAuthority(w, r, ws, automation.ActionDisableManagedBinding)
	if !ok {
		return promptAgentCreateAuthorities{}, false
	}
	out.deleteBinding, ok = m.resolveBindingAuthority(w, r, ws, automation.ActionDeleteManagedBinding)
	if !ok {
		return promptAgentCreateAuthorities{}, false
	}
	return out, true
}

func (m *Module) resolvePromptAgentRoleForCreate(
	w http.ResponseWriter,
	r *http.Request,
	ws string,
	plan promptAgentCreatePlan,
) (promptAgentRoleResolution, bool) {
	roleCreate := plan.request.Behavior.RoleCreate
	if roleCreate == nil {
		role, err := m.store.Roles().Get(r.Context(), ws, plan.roleName)
		if err != nil {
			handler.WriteDomainError(w, err, "get prompt-agent role failed")
			return promptAgentRoleResolution{}, false
		}
		role, ok := validatePromptAgentRoleForCreate(w, role)
		return promptAgentRoleResolution{role: role}, ok
	}
	// Validate the requested definition before EnsureRole writes a prompt or
	// creates a role. Driver preflight intentionally stays first so
	// build-toolchain failures retain their atomic 503 behavior.
	if err := rolehandlers.ValidatePromptAgentRole(&domain.Role{
		Name: plan.roleName, Prompt: roleCreate.Prompt, TaskFilter: roleCreate.TaskFilter,
	}); err != nil {
		handler.WriteDomainError(w, err, "invalid prompt-agent role")
		return promptAgentRoleResolution{}, false
	}
	result, err := rolehandlers.EnsureRoleWithReceipt(r.Context(), m.store, ws, rolehandlers.EnsureRoleRequest{
		Name:           plan.roleName,
		Description:    roleCreate.Description,
		Prompt:         roleCreate.Prompt,
		PromptFilename: roleCreate.PromptFilename,
		Model:          roleCreate.Model,
		TaskFilter:     roleCreate.TaskFilter,
		Backend:        roleCreate.Backend,
		Effort:         roleCreate.Effort,
		ReadOnly:       roleCreate.ReadOnly,
		AllowedTools:   roleCreate.AllowedTools,
		DeniedTools:    roleCreate.DeniedTools,
		Skills:         roleCreate.Skills,
	})
	if err != nil {
		handler.WriteDomainError(w, err, "ensure role failed")
		return promptAgentRoleResolution{}, false
	}
	role, ok := validatePromptAgentRoleForCreate(w, result.Role)
	if !ok {
		// Validation ran before EnsureRole, but the persisted role is still
		// checked in case a store adapter returned an unexpected projection.
		_ = result.Compensate(context.WithoutCancel(r.Context()), m.store, ws)
		return promptAgentRoleResolution{}, false
	}
	return promptAgentRoleResolution{role: role, receipt: result}, true
}

func validatePromptAgentRoleForCreate(w http.ResponseWriter, role *domain.Role) (*domain.Role, bool) {
	if err := rolehandlers.ValidatePromptAgentRole(role); err != nil {
		handler.WriteDomainError(w, err, "invalid prompt-agent role")
		return nil, false
	}
	return role, true
}
