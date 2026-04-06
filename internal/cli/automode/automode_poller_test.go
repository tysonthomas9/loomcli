//go:build ignore

package automode

import "testing"

// TestBuildRouterTaskCheck_NilWhenNoConstraints verifies nil returned for unconstrained roles.
func TestBuildRouterTaskCheck_NilWhenNoConstraints(t *testing.T) {
	rc := RoleConfig{Description: "basic"} // no Skills or MaxPriority
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

// TestBuildRouterTaskCheck_NilWithOnlyPathPatterns verifies PathPatterns alone does NOT activate the router.
func TestBuildRouterTaskCheck_NilWithOnlyPathPatterns(t *testing.T) {
	// RoleConfig-level PathPatterns
	rc := RoleConfig{
		Description:  "frontend specialist",
		PathPatterns: []string{"src/components/**"},
	}
	ae := AgentEntry{Worktree: "falcon", Role: "task"}
	check := BuildRouterTaskCheck(rc, ae, "")
	if check != nil {
		t.Error("BuildRouterTaskCheck() should return nil when only RoleConfig.PathPatterns is set (not a routing constraint)")
	}

	// AgentEntry-level PathPatterns
	rc2 := RoleConfig{Description: "frontend specialist"}
	ae2 := AgentEntry{Worktree: "falcon", Role: "task", PathPatterns: []string{"internal/**"}}
	check2 := BuildRouterTaskCheck(rc2, ae2, "")
	if check2 != nil {
		t.Error("BuildRouterTaskCheck() should return nil when only AgentEntry.PathPatterns is set (not a routing constraint)")
	}
}
