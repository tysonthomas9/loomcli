package supervisor

import (
	"strings"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/fleethttp"
)

func TestBuildCommandWorkerActorOverridesParentActor(t *testing.T) {
	t.Setenv(fleethttp.EnvFleetDBActor, "local-mode-harness@fixture.local")
	t.Setenv(fleethttp.EnvAgentName, "parent-agent")
	worktree := t.TempDir()
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}}
		},
		ProjectDir:    worktree,
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		EmitEvent:     func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "api-architect-1", Role: "task"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Architecture worker"},
		WorktreePath: worktree,
	}
	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	actorCount := 0
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, fleethttp.EnvFleetDBActor+"=") {
			actorCount++
			if entry != fleethttp.EnvFleetDBActor+"=api-architect-1" {
				t.Fatalf("worker actor env = %q", entry)
			}
		}
	}
	if actorCount != 1 {
		t.Fatalf("worker actor env count = %d, want exactly 1; env=%v", actorCount, cmd.Env)
	}
}
