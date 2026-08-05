package agentscompat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// ManagedRetirements is the narrow system workflow used to remove a
// compatibility assignment after its owning application workflow has moved to
// the canonical Agent identity.
type ManagedRetirements interface {
	RetireManagedAssignment(context.Context, agents.RetireSupervisedAssignmentCommand, string) error
}

type managedRetirements struct {
	api    agents.CompatibilityAPI
	issuer *authority.Issuer
	now    func() time.Time
}

func NewManagedRetirements(
	api agents.CompatibilityAPI,
	issuer *authority.Issuer,
) (ManagedRetirements, error) {
	if api == nil || issuer == nil {
		return nil, fmt.Errorf("compose Agents managed retirement workflow: %w", agents.ErrUnavailable)
	}
	return &managedRetirements{api: api, issuer: issuer, now: time.Now}, nil
}

func (retirements *managedRetirements) RetireManagedAssignment(
	ctx context.Context,
	command agents.RetireSupervisedAssignmentCommand,
	reason string,
) error {
	if retirements == nil || retirements.api == nil || retirements.issuer == nil || retirements.now == nil {
		return agents.ErrUnavailable
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("managed assignment retirement reason is required: %w", agents.ErrInvalid)
	}
	principal, err := retirements.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   "agents-compat-managed-retirement",
		Class:     authority.ClassSystem,
		Workspace: command.WorkspaceKey,
		Actions:   []authority.Action{agents.ActionRetireManagedAssignment},
		ExpiresAt: retirements.now().Add(time.Minute),
	})
	if err != nil {
		return err
	}
	auth, err := retirements.issuer.IssueSystem(
		principal,
		command.WorkspaceKey,
		agents.ActionRetireManagedAssignment,
		reason,
	)
	if err != nil {
		return err
	}
	return retirements.api.RetireManagedSupervisedAssignment(ctx, auth, command)
}
