package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// resolveRoleConfig looks up a role by name, supporting both built-in and custom roles.
// For built-in roles, merges any user-defined config (skills, path_patterns, etc.) on top of defaults.
func (d *Daemon) resolveRoleConfig(roleName string, agentIndex int) (RoleConfig, error) {
	if builtInRoles[roleName] {
		rc := RoleConfig{Description: fmt.Sprintf("Built-in %s agent", roleName)}
		// Merge user-defined config for built-in roles
		if userRC, ok := d.config.ResolveRole(roleName); ok {
			rc = mergeRoleConfig(rc, userRC)
		}
		return rc, nil
	}

	// Look up custom role in config
	rc, ok := d.config.ResolveRole(roleName)
	if !ok {
		return RoleConfig{}, fmt.Errorf("agent[%d]: role %q not found (not a built-in role and not defined in config.Roles)", agentIndex, roleName)
	}

	// Custom roles require a prompt file
	if rc.PromptFile == "" {
		return RoleConfig{}, fmt.Errorf("agent[%d]: custom role %q missing prompt_file", agentIndex, roleName)
	}

	// Resolve prompt file path relative to project dir
	promptPath := rc.PromptFile
	if !filepath.IsAbs(promptPath) {
		promptPath = filepath.Join(d.projectDir, promptPath)
	}
	if _, err := os.Stat(promptPath); err != nil {
		return RoleConfig{}, fmt.Errorf("agent[%d]: prompt file %q not found: %w", agentIndex, promptPath, err)
	}
	rc.PromptFile = promptPath

	return rc, nil
}

// mergeRoleConfig applies non-zero overlay fields onto base.
// Description falls back to base when overlay has none.
// PromptFile is NOT merged (built-in roles don't use prompt files).
func mergeRoleConfig(base, overlay RoleConfig) RoleConfig {
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
