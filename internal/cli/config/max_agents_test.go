package config

import "testing"

// TestValidateAgentsDoesNotRejectOnMaxAgents pins the removal of the
// `runnable > max_agents` load-time rejection.
//
// That check was the only enforcement max_agents ever had, and it ran inside
// the daemon's config load. Exceeding the cap therefore poisoned the whole
// snapshot: at boot the daemon exited, and on a periodic reload the reconciler
// logged "config reload failed, keeping current config" and silently stopped
// applying every later change while still reporting healthy. The count could
// cross the threshold from paths a creation-time guard would never see —
// lowering the cap, changing an agent's desired state or role, or flipping a
// role from interactive to worker — so the rejection is removed outright rather
// than guarded. max_agents survives as a field; enforcing it as a real runtime
// gate is separate work.
func TestValidateAgentsDoesNotRejectOnMaxAgents(t *testing.T) {
	agents := []AgentEntry{
		{Worktree: "codex-planner", Role: "plan"},
		{Worktree: "codex-coder", Role: "task"},
		{Worktree: "planner", Role: "plan"},
		{Worktree: "tasker", Role: "task"},
	}
	roles := map[string]RoleConfig{
		"plan": {Kind: "worker"},
		"task": {Kind: "worker"},
	}

	// Guard against a vacuous pass: the removed check only counted agents the
	// daemon would actually supervise, so all four must be runnable for the
	// old code to have rejected this fixture at all.
	for _, a := range agents {
		if !a.ShouldSuperviseWithRoles(roles) {
			t.Fatalf("fixture agent %q is not runnable; the test would pass vacuously", a.Worktree)
		}
	}

	for _, max := range []int{1, 2, 3} {
		limit := max
		if err := validateAgents(agents, &limit); err != nil {
			t.Fatalf("validateAgents with %d agents and max_agents=%d returned %v, want nil — the cap must not poison config load", len(agents), limit, err)
		}
	}
}

// TestValidateAgentsStillRejectsNegativeMaxAgents keeps the scalar sanity check:
// a negative cap is a typo, not a policy, and rejecting it costs nothing.
func TestValidateAgentsStillRejectsNegativeMaxAgents(t *testing.T) {
	negative := -1
	if err := validateAgents(nil, &negative); err == nil {
		t.Fatal("validateAgents accepted max_agents=-1, want an error")
	}
}
