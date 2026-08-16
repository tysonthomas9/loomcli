package serve

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestPRReviewerAuthorityIsFixedToOneConvergenceAction(t *testing.T) {
	issuer := authority.NewIssuer()
	capability := &AgentsCapability{issuer: issuer}
	admission, err := issuer.NewAdmission(agents.OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}

	auth, err := capability.reviewerAuthority(t.Context(), "WS", "review-octo-repo-pr-7")
	if err != nil {
		t.Fatal(err)
	}
	if auth.Subject() != prReviewerAuthoritySubject ||
		auth.Action() != agents.ActionConvergeManagedReviewer ||
		auth.Workspace() != "WS" {
		t.Fatalf("reviewer authority = subject:%q action:%q workspace:%q", auth.Subject(), auth.Action(), auth.Workspace())
	}
	if err := admission.RequireSystem(agents.ActionConvergeManagedReviewer, "WS", auth); err != nil {
		t.Fatalf("reviewer authority rejected by Agents: %v", err)
	}
	if err := admission.RequireSystem(
		agents.ActionEnsureManagedRole,
		"WS",
		auth,
	); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("reviewer authority admitted for generic Role action: %v", err)
	}
}

func TestPRReviewerAuthorityFailsClosed(t *testing.T) {
	var capability *AgentsCapability
	if _, err := capability.reviewerAuthority(
		t.Context(),
		"WS",
		"reviewer",
	); !errors.Is(err, agents.ErrUnavailable) {
		t.Fatalf("nil capability error = %v", err)
	}
}
