package supervisor

import (
	"strings"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

func newSafetyGateSupervisor(backend string) *Supervisor {
	return &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Backend: backend}
		},
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		EmitEvent:     func(events.Event) {},
	}
}

// A role with knobs its backend cannot enforce must refuse to spawn — running
// without the restriction is the config-that-lies failure this gate removes.
func TestGateSafetyKnobs_UnenforceableFailsClosed(t *testing.T) {
	s := newSafetyGateSupervisor("opencode")
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "critic", Role: "critic"},
		RoleConfig: cfgpkg.RoleConfig{ReadOnly: true},
	}
	err := s.gateSafetyKnobsEnforceable(ap)
	if err == nil {
		t.Fatal("read_only on opencode must fail closed")
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.LastError == nil || !strings.Contains(ap.LastError.Message, "read_only") {
		t.Fatalf("LastError must name the knob; got %+v", ap.LastError)
	}
}

func TestGateSafetyKnobs_EnforceablePasses(t *testing.T) {
	s := newSafetyGateSupervisor("claude")
	ap := &AgentProcess{
		Entry: cfgpkg.AgentEntry{Worktree: "critic", Role: "critic"},
		RoleConfig: cfgpkg.RoleConfig{
			ReadOnly:     true,
			AllowedTools: []string{"Read", "Grep"},
			DeniedTools:  []string{"WebSearch"},
		},
	}
	if err := s.gateSafetyKnobsEnforceable(ap); err != nil {
		t.Fatalf("claude enforces all knobs; gate must pass, got %v", err)
	}
	// codex enforces read_only (OS sandbox) but has no tool vocabulary.
	s = newSafetyGateSupervisor("codex")
	ap = &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "w", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{ReadOnly: true},
	}
	if err := s.gateSafetyKnobsEnforceable(ap); err != nil {
		t.Fatalf("read_only on codex maps to --sandbox read-only; gate must pass, got %v", err)
	}
}
