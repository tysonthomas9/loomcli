package cli

import (
	"fmt"
)

// resolveRoleConfig looks up a role by name, supporting both built-in and custom roles.
// For built-in roles, merges any user-defined config (skills, path_patterns, etc.) on top of defaults.
// Delegates to ResolveRoleConfigStatic (in daemon_queue.go) for the actual resolution.
func (d *Daemon) resolveRoleConfig(roleName string, agentIndex int) (RoleConfig, error) {
	cfg := d.configSnapshot()
	rc, err := ResolveRoleConfigStatic(roleName, cfg, d.projectDir)
	if err != nil {
		return RoleConfig{}, fmt.Errorf("agent[%d]: %w", agentIndex, err)
	}
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
