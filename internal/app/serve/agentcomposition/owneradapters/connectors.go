package owneradapters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// ConnectorsAuthority is intentionally action-specific.
// Composition may issue only connectors.ActionEnsureGrant for the requested
// workspace and audit reason.
type ConnectorsAuthority interface {
	AuthorityForGrant(context.Context, string, string) (authority.SystemAuthority, error)
}

type ConnectorsAdapter struct {
	grants    agentprovisioning.GrantOperations
	authority ConnectorsAuthority
}

var _ agentprovisioning.GrantOperations = (*ConnectorsAdapter)(nil)

func NewConnectorsAdapter(
	grants agentprovisioning.GrantOperations,
	authorityProvider ConnectorsAuthority,
) (*ConnectorsAdapter, error) {
	if grants == nil || authorityProvider == nil {
		return nil, fmt.Errorf("compose AgentProvisioning Connector grants: dependencies are required: %w", agentprovisioning.ErrUnavailable)
	}
	return &ConnectorsAdapter{grants: grants, authority: authorityProvider}, nil
}

func (adapter *ConnectorsAdapter) EnsureGrant(
	ctx context.Context,
	command agentprovisioning.EnsureGrantCommand,
) error {
	if adapter == nil || adapter.grants == nil || adapter.authority == nil {
		return agentprovisioning.ErrUnavailable
	}
	if !canonicalAuditID(command.CommandID) {
		return fmt.Errorf("connector grant command id is not canonical: %w", agentprovisioning.ErrInvalid)
	}
	reason := "AgentProvisioning " + command.CommandID
	_, err := adapter.authority.AuthorityForGrant(ctx, command.WorkspaceKey, reason)
	if err != nil {
		return fmt.Errorf(
			"issue Connector grant authority: %w",
			errors.Join(agentprovisioning.ErrUnavailable, err),
		)
	}
	err = adapter.grants.EnsureGrant(ctx, command)
	return mapConnectorsError("ensure Connector grant", err)
}

func mapConnectorsError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s through Connectors guarded owner command: %w", operation, err)
}

func canonicalAuditID(value string) bool {
	if value == "" || value != strings.TrimSpace(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
