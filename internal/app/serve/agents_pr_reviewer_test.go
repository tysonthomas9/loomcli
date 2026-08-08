package serve

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/prreviewer"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestPRReviewerAuthoritiesAreFixedToManagedRoleAndAgentActions(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	provider := &prReviewerAuthorityProvider{
		issuer: issuer,
		now:    func() time.Time { return now },
	}
	admission, err := issuer.NewAdmission(agents.OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}

	role, err := provider.AuthorityForReviewerRole(t.Context(), "WS", "ensure reviewer role")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := provider.AuthorityForReviewerAgent(t.Context(), "WS", "ensure reviewer agent")
	if err != nil {
		t.Fatal(err)
	}
	if role.Subject() != prreviewer.AuthoritySubject ||
		role.Action() != agents.ActionEnsureManagedRole ||
		role.Workspace() != "WS" {
		t.Fatalf("Role authority = subject:%q action:%q workspace:%q", role.Subject(), role.Action(), role.Workspace())
	}
	if agent.Subject() != prreviewer.AuthoritySubject ||
		agent.Action() != agents.ActionEnsureManagedAgent ||
		agent.Workspace() != "WS" {
		t.Fatalf("Agent authority = subject:%q action:%q workspace:%q", agent.Subject(), agent.Action(), agent.Workspace())
	}
	if err := admission.RequireSystem(agents.ActionEnsureManagedRole, "WS", role); err != nil {
		t.Fatalf("Role authority rejected by Agents: %v", err)
	}
	if err := admission.RequireSystem(agents.ActionEnsureManagedAgent, "WS", agent); err != nil {
		t.Fatalf("Agent authority rejected by Agents: %v", err)
	}
	if err := admission.RequireSystem(
		agents.ActionEnsureManagedAgent,
		"WS",
		role,
	); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("Role authority admitted for Agent action: %v", err)
	}
}

func TestPRReviewerAuthorityProviderFailsClosed(t *testing.T) {
	var provider *prReviewerAuthorityProvider
	if _, err := provider.AuthorityForReviewerRole(
		t.Context(),
		"WS",
		"ensure reviewer role",
	); !errors.Is(err, prreviewer.ErrUnavailable) {
		t.Fatalf("nil provider error = %v", err)
	}
}
