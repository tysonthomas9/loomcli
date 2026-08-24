package supervisor

import (
	"fmt"
	"os"
	"path/filepath"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// ResolveRoleConfigStatic looks up a role by name without requiring a Supervisor instance.
// For built-in roles, merges any user-defined config on top of defaults.
// For custom roles, requires a prompt_file: either a builtin:<id> reference to
// a prompt that ships with loom, or a file that must exist on disk.
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

	// builtin:<id> names a prompt that ships inside loom, so there is nothing to
	// stat and nothing to make absolute. The value is left EXACTLY as stored:
	// spawn forwards prompt_file verbatim to `loom agent --prompt`, which
	// resolves the same reference on the other side. Rewriting it into a path
	// here would hand the worker a filename that does not exist.
	if id, ok := domain.ParseBuiltinPromptRef(rc.PromptFile); ok {
		if !domain.IsBuiltinWorkerPrompt(id) {
			return cfgpkg.RoleConfig{}, fmt.Errorf("agent role %q references unknown built-in prompt %q", roleName, id)
		}
		return rc, nil
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
		rc.ExcludeLabels = []string{"architect"}
	case "task":
		rc.TaskFilter = "has_design"
	}
	return rc
}

// MergeRoleConfig applies non-zero overlay fields onto base.
// Description falls back to base when overlay has none.
// PromptFile is NOT merged (built-in roles don't use prompt files).
func MergeRoleConfig(base, overlay cfgpkg.RoleConfig) cfgpkg.RoleConfig {
	base = mergeRoleIdentity(base, overlay)
	return mergeRoleExecution(base, overlay)
}

// mergeRoleIdentity overlays the identity and routing half of the config:
// what the role is and which tasks it may claim.
func mergeRoleIdentity(base, overlay cfgpkg.RoleConfig) cfgpkg.RoleConfig {
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
	if overlay.Executor != "" {
		base.Executor = overlay.Executor
	}
	if overlay.MaxConcurrency != nil {
		base.MaxConcurrency = overlay.MaxConcurrency
	}
	return base
}

// mergeRoleExecution overlays the execution half of the config: backend
// selection, spend and duration bounds, and the safety knobs.
func mergeRoleExecution(base, overlay cfgpkg.RoleConfig) cfgpkg.RoleConfig {
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
