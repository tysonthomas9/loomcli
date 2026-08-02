package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// ResolveRoleConfigStatic looks up a role by name without requiring a Supervisor instance.
// For built-in roles, merges any user-defined config on top of defaults.
// For custom roles, requires a prompt_file that must exist.
func ResolveRoleConfigStatic(roleName string, config *cfgpkg.DaemonConfig, projectDir string) (cfgpkg.RoleConfig, error) {
	if BuiltInRoles[roleName] {
		rc := builtInRoleConfig(roleName)
		if userRC, ok := config.ResolveRole(roleName); ok {
			if roleConfigIsInteractive(roleName, userRC) {
				return cfgpkg.RoleConfig{}, fmt.Errorf("interactive role %q cannot be daemon-supervised; launch it from a terminal", roleName)
			}
			if userRC.PromptFile != "" {
				return cfgpkg.RoleConfig{}, fmt.Errorf("built-in role %q cannot set prompt_file; use a custom role name for prompt-based agents", roleName)
			}
			rc = MergeRoleConfig(rc, userRC)
		}
		return rc, nil
	}

	rc, ok := config.ResolveRole(roleName)
	if !ok {
		return cfgpkg.RoleConfig{}, fmt.Errorf("role %q not found (not a built-in role and not defined in config.Roles)", roleName)
	}
	if roleConfigIsInteractive(roleName, rc) {
		return cfgpkg.RoleConfig{}, fmt.Errorf("interactive role %q cannot be daemon-supervised; launch it from a terminal", roleName)
	}

	if rc.PromptFile == "" {
		return cfgpkg.RoleConfig{}, fmt.Errorf("custom role %q missing prompt_file", roleName)
	}

	// builtin: is resolved only by GenerateTerminalPrompt, for interactive
	// terminal prompts. Joining it onto projectDir yields a path nobody wrote
	// and fails daemon creation for every agent, not just this one — so name the
	// real cause here rather than reporting a missing file.
	if strings.HasPrefix(strings.TrimSpace(rc.PromptFile), "builtin:") {
		return cfgpkg.RoleConfig{}, fmt.Errorf(
			"role %q has prompt_file %q: built-in prompts are interactive-only and cannot be daemon-supervised; "+
				"set prompt_file to a path relative to the workspace root",
			roleName, rc.PromptFile)
	}

	promptPath := rc.PromptFile
	if !filepath.IsAbs(promptPath) {
		promptPath = filepath.Join(projectDir, promptPath)
	}
	if _, err := os.Stat(promptPath); err != nil {
		return cfgpkg.RoleConfig{}, fmt.Errorf("prompt file %q not found: %w", promptPath, err)
	}
	rc.PromptFile = promptPath

	return rc, nil
}

func roleConfigIsInteractive(roleName string, rc cfgpkg.RoleConfig) bool {
	role := &domain.Role{Kind: domain.RoleKind(rc.Kind)}
	return domain.ResolveRoleKind(role, roleName) == domain.RoleKindInteractive
}

func builtInRoleConfig(roleName string) cfgpkg.RoleConfig {
	rc := cfgpkg.RoleConfig{Description: fmt.Sprintf("Built-in %s agent", roleName)}
	switch roleName {
	case "plan":
		rc.TaskFilter = "needs_plan"
	case "task":
		rc.TaskFilter = "has_design"
	}
	return rc
}

// MergeRoleConfig applies non-zero overlay fields onto base.
// Description falls back to base when overlay has none.
// PromptFile is NOT merged (built-in roles don't use prompt files).
func MergeRoleConfig(base, overlay cfgpkg.RoleConfig) cfgpkg.RoleConfig {
	if overlay.Kind != "" {
		base.Kind = overlay.Kind
	}
	if overlay.Description != "" {
		base.Description = overlay.Description
	}
	if len(overlay.Skills) > 0 {
		base.Skills = overlay.Skills
	}
	if len(overlay.Labels) > 0 {
		base.Labels = overlay.Labels
	}
	if len(overlay.ExcludeLabels) > 0 {
		base.ExcludeLabels = overlay.ExcludeLabels
	}
	if len(overlay.PathPatterns) > 0 {
		base.PathPatterns = overlay.PathPatterns
	}
	if overlay.MaxPriority != nil {
		base.MaxPriority = overlay.MaxPriority
	}
	if overlay.TaskFilter != "" {
		base.TaskFilter = overlay.TaskFilter
	}
	if overlay.MaxConcurrency != nil {
		base.MaxConcurrency = overlay.MaxConcurrency
	}
	if overlay.MaxBudgetUSD != nil {
		base.MaxBudgetUSD = overlay.MaxBudgetUSD
	}
	// A pointer, so an explicit 0 survives the merge: "this role opts out of the
	// run-duration cap" has to be expressible, and a plain int could not tell it
	// apart from "unset, inherit the daemon default".
	if overlay.MaxRunDuration != nil {
		base.MaxRunDuration = overlay.MaxRunDuration
	}
	if overlay.Backend != "" {
		base.Backend = overlay.Backend
	}
	if overlay.Effort != "" {
		base.Effort = overlay.Effort
	}
	if overlay.Model != "" {
		base.Model = overlay.Model
	}
	if overlay.ReadOnly {
		base.ReadOnly = true
	}
	if len(overlay.AllowedTools) > 0 {
		base.AllowedTools = overlay.AllowedTools
	}
	if len(overlay.DeniedTools) > 0 {
		base.DeniedTools = overlay.DeniedTools
	}
	// A non-nil overlay policy replaces the base wholesale rather than merging
	// the Kinds maps. Merging would let a base entry the overlay deliberately
	// dropped survive, and for a policy whose entries grant permission that
	// resolves the wrong way: the surviving entry could be the permissive one.
	if overlay.InputPolicy != nil {
		base.InputPolicy = overlay.InputPolicy
	}
	// PromptFile intentionally NOT merged for built-in roles
	return base
}
