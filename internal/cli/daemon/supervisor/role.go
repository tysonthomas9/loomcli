package supervisor

import (
	"fmt"
	"os"
	"path/filepath"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// ResolveRoleConfigStatic looks up a role by name without requiring a Supervisor instance.
// For built-in roles, merges any user-defined config on top of defaults.
// For custom roles, requires a prompt_file that must exist.
func ResolveRoleConfigStatic(roleName string, config *cfgpkg.DaemonConfig, projectDir string) (cfgpkg.RoleConfig, error) {
	if BuiltInRoles[roleName] {
		rc := builtInRoleConfig(roleName)
		if userRC, ok := config.ResolveRole(roleName); ok {
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

	if rc.PromptFile == "" {
		return cfgpkg.RoleConfig{}, fmt.Errorf("custom role %q missing prompt_file", roleName)
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
	if overlay.Description != "" {
		base.Description = overlay.Description
	}
	if len(overlay.Skills) > 0 {
		base.Skills = overlay.Skills
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
	if overlay.Backend != "" {
		base.Backend = overlay.Backend
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
	// PromptFile intentionally NOT merged for built-in roles
	return base
}
