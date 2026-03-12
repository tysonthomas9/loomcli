package cli

import "testing"

func TestMergeRoleConfig_OverlayFieldsWin(t *testing.T) {
	maxP := 3
	base := RoleConfig{Description: "Base description"}
	overlay := RoleConfig{
		Skills:       []string{"go", "daemon"},
		PathPatterns: []string{"internal/**"},
		MaxPriority:  &maxP,
		TaskFilter:   "needs_plan",
	}

	got := mergeRoleConfig(base, overlay)

	if len(got.Skills) != 2 || got.Skills[0] != "go" {
		t.Errorf("Skills = %v, want [go daemon]", got.Skills)
	}
	if len(got.PathPatterns) != 1 || got.PathPatterns[0] != "internal/**" {
		t.Errorf("PathPatterns = %v, want [internal/**]", got.PathPatterns)
	}
	if got.MaxPriority == nil || *got.MaxPriority != 3 {
		t.Errorf("MaxPriority = %v, want 3", got.MaxPriority)
	}
	if got.TaskFilter != "needs_plan" {
		t.Errorf("TaskFilter = %q, want %q", got.TaskFilter, "needs_plan")
	}
}

func TestMergeRoleConfig_BaseDescriptionKept(t *testing.T) {
	base := RoleConfig{Description: "Built-in plan agent"}
	overlay := RoleConfig{Skills: []string{"go"}}

	got := mergeRoleConfig(base, overlay)

	if got.Description != "Built-in plan agent" {
		t.Errorf("Description = %q, want %q", got.Description, "Built-in plan agent")
	}
}

func TestMergeRoleConfig_OverlayDescriptionWins(t *testing.T) {
	base := RoleConfig{Description: "Built-in plan agent"}
	overlay := RoleConfig{Description: "Custom planner"}

	got := mergeRoleConfig(base, overlay)

	if got.Description != "Custom planner" {
		t.Errorf("Description = %q, want %q", got.Description, "Custom planner")
	}
}

func TestMergeRoleConfig_PromptFileNotMerged(t *testing.T) {
	base := RoleConfig{Description: "Built-in plan agent"}
	overlay := RoleConfig{PromptFile: "prompts/custom.md"}

	got := mergeRoleConfig(base, overlay)

	if got.PromptFile != "" {
		t.Errorf("PromptFile = %q, want empty (should not merge for built-in roles)", got.PromptFile)
	}
}

func TestMergeRoleConfig_AllOverlayFields(t *testing.T) {
	maxP := 2
	maxC := 4
	base := RoleConfig{Description: "base"}
	overlay := RoleConfig{
		Description:    "overlay",
		Skills:         []string{"go"},
		PathPatterns:   []string{"cmd/**"},
		MaxPriority:    &maxP,
		TaskFilter:     "any",
		MaxConcurrency: &maxC,
		Backend:        "codex",
		Model:          "gpt-4",
		ReadOnly:       true,
		AllowedTools:   []string{"read"},
		DeniedTools:    []string{"write"},
	}

	got := mergeRoleConfig(base, overlay)

	if got.Description != "overlay" {
		t.Errorf("Description = %q", got.Description)
	}
	if got.Backend != "codex" {
		t.Errorf("Backend = %q", got.Backend)
	}
	if got.Model != "gpt-4" {
		t.Errorf("Model = %q", got.Model)
	}
	if !got.ReadOnly {
		t.Error("ReadOnly = false, want true")
	}
	if got.MaxConcurrency == nil || *got.MaxConcurrency != 4 {
		t.Errorf("MaxConcurrency = %v, want 4", got.MaxConcurrency)
	}
	if len(got.AllowedTools) != 1 || got.AllowedTools[0] != "read" {
		t.Errorf("AllowedTools = %v", got.AllowedTools)
	}
	if len(got.DeniedTools) != 1 || got.DeniedTools[0] != "write" {
		t.Errorf("DeniedTools = %v", got.DeniedTools)
	}
}

func TestMergeRoleConfig_EmptyOverlay(t *testing.T) {
	maxP := 2
	base := RoleConfig{
		Description:  "base",
		Skills:       []string{"go"},
		PathPatterns: []string{"src/**"},
		MaxPriority:  &maxP,
	}
	overlay := RoleConfig{}

	got := mergeRoleConfig(base, overlay)

	if got.Description != "base" {
		t.Errorf("Description = %q, want %q", got.Description, "base")
	}
	if len(got.Skills) != 1 || got.Skills[0] != "go" {
		t.Errorf("Skills = %v, want [go]", got.Skills)
	}
	if len(got.PathPatterns) != 1 || got.PathPatterns[0] != "src/**" {
		t.Errorf("PathPatterns = %v, want [src/**]", got.PathPatterns)
	}
	if got.MaxPriority == nil || *got.MaxPriority != 2 {
		t.Errorf("MaxPriority = %v, want 2", got.MaxPriority)
	}
}

func TestResolveRoleConfig_BuiltInWithUserConfig(t *testing.T) {
	maxP := 2
	d := &Daemon{
		config: &DaemonConfig{
			Roles: map[string]RoleConfig{
				"plan": {
					Skills:      []string{"planning", "architecture"},
					MaxPriority: &maxP,
				},
			},
		},
		projectDir: "/tmp",
	}

	rc, err := d.resolveRoleConfig("plan", 0)
	if err != nil {
		t.Fatalf("resolveRoleConfig(plan) error = %v", err)
	}

	if len(rc.Skills) != 2 || rc.Skills[0] != "planning" {
		t.Errorf("Skills = %v, want [planning architecture]", rc.Skills)
	}
	if rc.MaxPriority == nil || *rc.MaxPriority != 2 {
		t.Errorf("MaxPriority = %v, want 2", rc.MaxPriority)
	}
	// Description should still come from built-in default
	if rc.Description == "" {
		t.Error("Description should not be empty")
	}
}

func TestResolveRoleConfig_BuiltInWithoutUserConfig(t *testing.T) {
	d := &Daemon{
		config:     &DaemonConfig{},
		projectDir: "/tmp",
	}

	rc, err := d.resolveRoleConfig("task", 0)
	if err != nil {
		t.Fatalf("resolveRoleConfig(task) error = %v", err)
	}

	if rc.Description == "" {
		t.Error("Description should not be empty for built-in role")
	}
	if len(rc.Skills) != 0 {
		t.Errorf("Skills = %v, want empty (no user config)", rc.Skills)
	}
}
