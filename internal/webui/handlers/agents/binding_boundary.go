package agents

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

type bindingDeletionResult struct {
	BindingID     string `json:"binding_id"`
	Deleted       bool   `json:"deleted"`
	GrantsRevoked int    `json:"grants_revoked"`
}

func (m *Module) canonicalWorkspace(r *http.Request) (string, bool) {
	if m == nil || r == nil || strings.TrimSpace(r.PathValue("ws")) == "" || m.workspaceFromContext == nil {
		return "", false
	}
	workspace := strings.TrimSpace(m.workspaceFromContext(r.Context()))
	return workspace, workspace != ""
}

func (m *Module) requireCanonicalWorkspace(w http.ResponseWriter, r *http.Request) (string, bool) {
	workspace, ok := m.canonicalWorkspace(r)
	if !ok {
		handler.RespondError(w, http.StatusBadRequest, "workspace is required")
	}
	return workspace, ok
}

func (m *Module) resolveBindingAuthority(
	w http.ResponseWriter,
	r *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, bool) {
	if m == nil || m.operatorAuthority == nil {
		writeBindingError(w, automation.ErrUnavailable, "binding operator authority is unavailable")
		return authority.OperatorAuthority{}, false
	}
	auth, err := m.operatorAuthority.ResolveOperatorAuthority(r, workspace, action)
	if err != nil {
		writeBindingError(w, err, "binding operator authority denied")
		return authority.OperatorAuthority{}, false
	}
	return auth, true
}

func writeBindingError(w http.ResponseWriter, err error, fallback string) {
	if classification, ok := handler.ClassifyAuthenticationAuthorityError(err); ok {
		message := "authentication required"
		if classification.Status == http.StatusForbidden {
			message = "forbidden"
		}
		handler.RespondError(w, classification.Status, message)
		return
	}
	switch {
	case errors.Is(err, automation.ErrNotFound), errors.Is(err, persistence.ErrNotFound):
		handler.RespondError(w, http.StatusNotFound, fallback)
	case errors.Is(err, automation.ErrInvalid), errors.Is(err, automation.ErrWrongWorkspace), errors.Is(err, persistence.ErrInvalid):
		handler.RespondError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, automation.ErrConflict), errors.Is(err, automation.ErrManagedBinding),
		errors.Is(err, automation.ErrBindingEnabled), errors.Is(err, persistence.ErrConflict):
		handler.RespondError(w, http.StatusConflict, err.Error())
	case handler.IsControlPlaneRateLimited(err):
		handler.RespondError(w, http.StatusTooManyRequests, fallback)
	case handler.IsControlPlaneUnavailable(err):
		handler.RespondError(w, http.StatusServiceUnavailable, fallback)
	case errors.Is(err, automation.ErrUnavailable):
		handler.RespondError(w, http.StatusServiceUnavailable, fallback)
	default:
		handler.RespondError(w, http.StatusInternalServerError, fallback)
	}
}

func bindingNotFound(err error) bool {
	return errors.Is(err, automation.ErrNotFound) || errors.Is(err, persistence.ErrNotFound)
}

func (m *Module) deleteUnmanagedBinding(
	ctx context.Context,
	workspace, bindingID string,
	disableAuth, deleteAuth authority.OperatorAuthority,
) (bindingDeletionResult, error) {
	result := bindingDeletionResult{BindingID: bindingID}
	if m == nil || m.bindings == nil || m.bindingGrants == nil {
		return result, automation.ErrUnavailable
	}
	command := automation.BindingCommand{WorkspaceKey: workspace, BindingID: bindingID}
	if _, err := m.bindings.DisableBinding(ctx, disableAuth, command); err != nil {
		return result, err
	}
	revoked, err := m.bindingGrants.RevokeBindingGrants(ctx, workspace, bindingID)
	result.GrantsRevoked = revoked
	if err != nil {
		return result, err
	}
	if err := m.bindings.DeleteBinding(ctx, deleteAuth, command); err != nil {
		return result, err
	}
	result.Deleted = true
	return result, nil
}

func (m *Module) deleteManagedBinding(
	ctx context.Context,
	workspace, bindingID, agentServiceID string,
	disableAuth, deleteAuth authority.OperatorAuthority,
) (bindingDeletionResult, error) {
	result := bindingDeletionResult{BindingID: bindingID}
	if m == nil || m.bindings == nil || m.bindingGrants == nil {
		return result, automation.ErrUnavailable
	}
	command := automation.ManagedBindingCommand{
		WorkspaceKey: workspace, BindingID: bindingID, AgentServiceID: agentServiceID,
	}
	if _, err := m.bindings.DisableManagedBinding(ctx, disableAuth, command); err != nil {
		return result, err
	}
	revoked, err := m.bindingGrants.RevokeBindingGrants(ctx, workspace, bindingID)
	result.GrantsRevoked = revoked
	if err != nil {
		return result, err
	}
	if err := m.bindings.DeleteManagedBinding(ctx, deleteAuth, command); err != nil {
		return result, err
	}
	result.Deleted = true
	return result, nil
}
