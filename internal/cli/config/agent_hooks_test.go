package config

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func hookPipeline() *domain.AgentHooks {
	return &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionAddLabel, Value: "criticized"},
	}}
}

func TestAgentEntryFromDomain_CarriesAndClonesHooks(t *testing.T) {
	shared := hookPipeline()
	entry := agentEntryFromDomain(&domain.Agent{
		Name: "critic", RoleName: "critic", Hooks: shared,
	})

	if !entry.Hooks.Equal(hookPipeline()) {
		t.Fatalf("entry.Hooks = %+v, want the pipeline", entry.Hooks)
	}
	entry.Hooks.OnComplete[1].Value = "mutated"
	if shared.OnComplete[1].Value != "criticized" {
		t.Error("agentEntryFromDomain aliased the domain slice instead of cloning it")
	}
}

func TestAgentEntryFromDomain_NoHooks(t *testing.T) {
	entry := agentEntryFromDomain(&domain.Agent{Name: "plain", RoleName: "critic"})
	if entry.Hooks != nil {
		t.Errorf("entry.Hooks = %+v, want nil", entry.Hooks)
	}
}

// The reconciler restarts an agent only when Equal reports a difference
// (daemon_reconciler.diffAgents), so an edit that only changes hooks must be
// visible here or it will silently never take effect.
func TestAgentEntry_EqualIncludesHooks(t *testing.T) {
	base := AgentEntry{Worktree: "critic", Role: "critic"}

	withHooks := base
	withHooks.Hooks = hookPipeline()

	if base.Equal(withHooks) {
		t.Error("an entry that gained hooks must not compare equal")
	}
	if withHooks.Equal(base) {
		t.Error("Equal must be symmetric for a hooks difference")
	}

	sameHooks := base
	sameHooks.Hooks = hookPipeline()
	if !withHooks.Equal(sameHooks) {
		t.Error("identical pipelines should compare equal")
	}

	reordered := base
	reordered.Hooks = &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionAddLabel, Value: "reviewed"},
	}}
	if withHooks.Equal(reordered) {
		t.Error("a different label value must not compare equal")
	}

	emptyHooks := base
	emptyHooks.Hooks = &domain.AgentHooks{}
	if !base.Equal(emptyHooks) {
		t.Error("nil and empty hooks are the same value and must compare equal")
	}
}
