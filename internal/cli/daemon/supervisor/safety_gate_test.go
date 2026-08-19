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

// A tool list the backend cannot apply must refuse to spawn — running without
// the restriction is the config-that-lies failure this gate removes.
func TestGateSafetyKnobs_UnenforceableToolListFailsClosed(t *testing.T) {
	s := newSafetyGateSupervisor("opencode")
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "critic", Role: "critic"},
		RoleConfig: cfgpkg.RoleConfig{DeniedTools: []string{"Bash"}},
	}
	err := s.gateSafetyKnobsEnforceable(ap)
	if err == nil {
		t.Fatal("denied_tools on opencode must fail closed")
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.LastError == nil || !strings.Contains(ap.LastError.Message, "denied_tools") {
		t.Fatalf("LastError must name the knob; got %+v", ap.LastError)
	}
}

// read_only is the one knob with a real soft layer, so it spawns — and the
// gate records the warning so the operator is told how deep the restriction
// goes. Failing closed here refused every seeded planner (WRITEUP R2).
func TestGateSafetyKnobs_ReadOnlyDegradesLoudly(t *testing.T) {
	s := newSafetyGateSupervisor("localdogfood")
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "planner", Role: "plan"},
		RoleConfig: cfgpkg.RoleConfig{ReadOnly: true},
	}
	if err := s.gateSafetyKnobsEnforceable(ap); err != nil {
		t.Fatalf("a seeded read_only planner must still spawn: %v", err)
	}
	ap.Mu.Lock()
	warning, lastErr := ap.SoftKnobWarning, ap.LastError
	ap.Mu.Unlock()
	if !strings.Contains(warning, "localdogfood") {
		t.Fatalf("gate must record a warning naming the backend; got %q", warning)
	}
	if lastErr != nil {
		t.Fatalf("soft enforcement is not a spawn failure; got %+v", lastErr)
	}
}

// The gate runs on every poll cycle, including cycles that claim nothing, so
// the warning is logged once per change rather than once per cycle.
func TestGateSafetyKnobs_WarningIsNotRepeatedEveryCycle(t *testing.T) {
	s := newSafetyGateSupervisor("localdogfood")
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "planner", Role: "plan"},
		RoleConfig: cfgpkg.RoleConfig{ReadOnly: true},
	}
	for range 3 {
		if err := s.gateSafetyKnobsEnforceable(ap); err != nil {
			t.Fatalf("gate must keep passing: %v", err)
		}
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.SoftKnobWarning == "" {
		t.Fatal("the warning must stay recorded across cycles")
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
