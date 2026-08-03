package prreview

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/prreviewer"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

func TestEnsureReviewerAgentUsesCanonicalAgents(t *testing.T) {
	registry := newReviewerAgentRegistry()
	module := &Module{reviewerProvisioning: registry, reviewerAgents: registry}
	const agentID = "review-octocat-hello-abc12345-pr-7"

	if err := module.ensureReviewerAgent(t.Context(), prReviewTestWorkspace, agentID); err != nil {
		t.Fatalf("ensureReviewerAgent: %v", err)
	}
	agent, err := registry.GetAgent(t.Context(), prReviewTestWorkspace, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if agent.Kind != agents.AgentKindSupport ||
		agent.Behavior != (agents.BehaviorReference{RoleName: prreviewer.RoleName}) ||
		agent.DesiredState != agents.DesiredRunning {
		t.Fatalf("canonical Agent = %#v", agent)
	}
}

func TestEnsureReviewerAgentFailsClosedWithoutCanonicalCapability(t *testing.T) {
	module := &Module{}
	if err := module.ensureReviewerAgent(
		t.Context(),
		prReviewTestWorkspace,
		"reviewer",
	); !errors.Is(err, prreviewer.ErrUnavailable) {
		t.Fatalf("ensureReviewerAgent error = %v, want unavailable", err)
	}
}
