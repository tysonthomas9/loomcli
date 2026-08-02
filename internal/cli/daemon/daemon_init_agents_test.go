package daemon

import (
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// initSupervisorAgents runs once at daemon creation, which makes it a single
// point of failure for the whole workspace: when it returned the first
// resolution error, one agentdef pointing at a broken role kept the daemon
// from starting at all — every OTHER healthy agent in the workspace was down
// until an operator repaired a definition they had no reason to look at.
// A bad definition must cost exactly one agent.
func TestInitSupervisorAgents_SkipsInvalidDefinitionKeepsTheRest(t *testing.T) {
	good := t.TempDir() // absolute worktree paths bypass workspace resolution
	bad := t.TempDir()

	roles := map[string]cfgpkg.RoleConfig{
		"broken": {Description: "no prompt of either kind"},
	}
	cfg := &cfgpkg.DaemonConfig{Roles: roles}
	sup := &supervisor.Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg },
		ProjectDir:     t.TempDir(),
		EmitEvent:      func(events.Event) {},
		FindRepoConfig: func(string) *cfgpkg.RepoConfig { return nil },
	}

	agents := []cfgpkg.AgentEntry{
		{Worktree: bad, Role: "broken"},
		{Worktree: good, Role: "task"},
	}

	if err := initSupervisorAgents(sup, agents, roles); err != nil {
		t.Fatalf("initSupervisorAgents = %v, want nil (skip the bad agent, keep the daemon)", err)
	}
	if len(sup.Agents) != 1 {
		t.Fatalf("supervised agents = %d, want 1 (only the valid definition)", len(sup.Agents))
	}
	if sup.Agents[0].Entry.Worktree != good {
		t.Fatalf("supervised %q, want the valid agent %q", sup.Agents[0].Entry.Worktree, good)
	}
}
