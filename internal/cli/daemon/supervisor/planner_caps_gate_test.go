package supervisor

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestGatePlannerCaps_ClaudeWithoutHooksFailsClosed(t *testing.T) {
	s := newSafetyGateSupervisor("claude")
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "planner", Role: "plan"},
		RoleConfig: cfgpkg.RoleConfig{ReadOnly: true, TaskFilter: "needs_plan"},
	}
	err := s.gatePlannerCapsEnforceable(ap)
	if err == nil {
		t.Fatal("plan+claude+no hooks must refuse spawn")
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome) {
		t.Fatalf("LastError = %+v, want SpawnFailure", ap.LastError)
	}
	msg := ap.LastError.Message
	for _, needle := range []string{"fail-closed", "on-complete-write-design", backends.CapTaskDesignSubmit} {
		if !strings.Contains(msg, needle) {
			t.Errorf("admit message missing %q: %s", needle, msg)
		}
	}
}

func TestGatePlannerCaps_ClaudeWithHostHooksAdmits(t *testing.T) {
	s := newSafetyGateSupervisor("claude")
	ap := &AgentProcess{
		Entry: cfgpkg.AgentEntry{
			Worktree: "planner",
			Role:     "plan",
			Hooks:    backends.DefaultPlanHostHooks(),
		},
		RoleConfig: cfgpkg.RoleConfig{ReadOnly: true, TaskFilter: "needs_plan"},
	}
	if err := s.gatePlannerCapsEnforceable(ap); err != nil {
		t.Fatalf("plan+claude+hooks must admit: %v", err)
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.LastError != nil {
		t.Fatalf("LastError = %+v, want nil", ap.LastError)
	}
}

func TestGatePlannerCaps_CursorSoftWithoutHooksAdmits(t *testing.T) {
	s := newSafetyGateSupervisor("cursor")
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "planner", Role: "plan"},
		RoleConfig: cfgpkg.RoleConfig{ReadOnly: true, TaskFilter: "needs_plan"},
	}
	if err := s.gatePlannerCapsEnforceable(ap); err != nil {
		t.Fatalf("cursor soft shell path must admit without hooks: %v", err)
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.SoftKnobWarning == "" {
		t.Fatal("soft enforcement must record EnforcementDetail wording")
	}
	if !strings.Contains(ap.SoftKnobWarning, "prompt_only") &&
		!strings.Contains(ap.SoftKnobWarning, "SOFT") &&
		!strings.Contains(strings.ToLower(ap.SoftKnobWarning), "prompt") {
		t.Fatalf("soft warning = %q, want enforcement detail", ap.SoftKnobWarning)
	}
}
