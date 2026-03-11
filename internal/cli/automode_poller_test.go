package cli

import "testing"

// TestBuildRouterTaskCheck_NilWhenNoConstraints verifies nil returned for unconstrained roles.
func TestBuildRouterTaskCheck_NilWhenNoConstraints(t *testing.T) {
	rc := RoleConfig{Description: "basic"} // no Skills, PathPatterns, or MaxPriority
	ae := AgentEntry{Worktree: "falcon", Role: "task"}

	check := BuildRouterTaskCheck(rc, ae, "")
	if check != nil {
		t.Error("BuildRouterTaskCheck() should return nil for unconstrained role")
	}
}

// TestBuildRouterTaskCheck_NonNilWithSkills verifies non-nil returned when role has skills.
func TestBuildRouterTaskCheck_NonNilWithSkills(t *testing.T) {
	rc := RoleConfig{
		Description: "security specialist",
		Skills:      []string{"security", "auth"},
	}
	ae := AgentEntry{Worktree: "falcon", Role: "task"}

	check := BuildRouterTaskCheck(rc, ae, "")
	if check == nil {
		t.Error("BuildRouterTaskCheck() should return non-nil for role with skills")
	}
}

// TestBuildRouterTaskCheck_NonNilWithPathPatterns verifies non-nil returned when role has path patterns.
func TestBuildRouterTaskCheck_NonNilWithPathPatterns(t *testing.T) {
	rc := RoleConfig{
		Description:  "frontend specialist",
		PathPatterns: []string{"src/components/**"},
	}
	ae := AgentEntry{Worktree: "falcon", Role: "task"}

	check := BuildRouterTaskCheck(rc, ae, "")
	if check == nil {
		t.Error("BuildRouterTaskCheck() should return non-nil for role with path patterns")
	}
}

// TestBuildRouterTaskCheck_NonNilWithMaxPriority verifies non-nil returned when role has max priority.
func TestBuildRouterTaskCheck_NonNilWithMaxPriority(t *testing.T) {
	maxP := 2
	rc := RoleConfig{
		Description: "p0-p2 only",
		MaxPriority: &maxP,
	}
	ae := AgentEntry{Worktree: "falcon", Role: "task"}

	check := BuildRouterTaskCheck(rc, ae, "")
	if check == nil {
		t.Error("BuildRouterTaskCheck() should return non-nil for role with max priority")
	}
}

// TestBuildRouterTaskCheck_AgentOverridesPathPatterns verifies agent-level path patterns
// override role-level ones when determining if router is needed.
func TestBuildRouterTaskCheck_AgentOverridesPathPatterns(t *testing.T) {
	rc := RoleConfig{Description: "no constraints"} // no PathPatterns
	ae := AgentEntry{
		Worktree:     "falcon",
		Role:         "task",
		PathPatterns: []string{"internal/**"},
	}

	check := BuildRouterTaskCheck(rc, ae, "")
	if check == nil {
		t.Error("BuildRouterTaskCheck() should return non-nil when agent has path patterns")
	}
}
