package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidationIssue represents a single config validation problem.
type ValidationIssue struct {
	Severity string // "error" or "warning"
	Field    string // dot-path like "daemon.agents[0].role"
	Message  string // human-readable message with fix suggestion
}

// ValidationResult holds all issues found during validation.
type ValidationResult struct {
	Issues []ValidationIssue
}

func (r *ValidationResult) addError(field, msg string) {
	r.Issues = append(r.Issues, ValidationIssue{Severity: "error", Field: field, Message: msg})
}

func (r *ValidationResult) addWarning(field, msg string) {
	r.Issues = append(r.Issues, ValidationIssue{Severity: "warning", Field: field, Message: msg})
}

// HasErrors returns true if any issue has severity "error".
func (r *ValidationResult) HasErrors() bool {
	for _, issue := range r.Issues {
		if issue.Severity == "error" {
			return true
		}
	}
	return false
}

// FormatIssues formats all issues for display. Returns empty string if no issues.
func (r *ValidationResult) FormatIssues() string {
	if len(r.Issues) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("config validation errors:\n")
	for _, issue := range r.Issues {
		fmt.Fprintf(&b, "  [%s] %s: %s\n", issue.Severity, issue.Field, issue.Message)
	}
	return b.String()
}

// validTaskFilters lists the accepted values for RoleConfig.TaskFilter.
var validTaskFilters = map[string]bool{
	"needs_design": true,
	"has_design":   true,
	"any":          true,
}

// ValidateProjectConfig validates a merged DaemonConfig.
// projectDir is used for resolving relative paths.
func ValidateProjectConfig(dc *DaemonConfig, projectDir string) *ValidationResult {
	r := &ValidationResult{}
	if dc == nil {
		return r
	}

	validateBackendName(r, "backend", dc.Backend)
	validateAgentEntries(r, dc.Agents, dc.Roles)
	validateRoles(r, dc.Roles, projectDir)
	validateRestartPolicy(r, dc.Daemon.RestartPolicy)

	return r
}

// validateBackendName checks that a backend name (if set) matches a registered backend.
func validateBackendName(r *ValidationResult, field, name string) {
	if name == "" {
		return
	}
	available := ListBackends()
	if len(available) == 0 {
		return
	}
	for _, b := range available {
		if b == name {
			return
		}
	}
	r.addError(field, fmt.Sprintf(
		"%q is not a registered backend; available: %s",
		name, strings.Join(available, ", ")))
}

// validateAgentEntries checks all agent entries for worktree validity, role references, and duplicates.
func validateAgentEntries(r *ValidationResult, agents []AgentEntry, roles map[string]RoleConfig) {
	seenWorktrees := make(map[string]int)
	for i, a := range agents {
		field := fmt.Sprintf("agents[%d]", i)

		// Worktree character validation (catches path traversal, dots, slashes, etc.)
		if a.Worktree != "" && !isValidWorktreeName(a.Worktree) {
			r.addError(field+".worktree", fmt.Sprintf(
				"%q contains invalid characters; use only alphanumeric, hyphens, and underscores", a.Worktree))
		}

		// Duplicate worktree detection
		if a.Worktree != "" {
			if prevIdx, ok := seenWorktrees[a.Worktree]; ok {
				r.addError(field+".worktree", fmt.Sprintf(
					"%q is a duplicate (also used by agents[%d])", a.Worktree, prevIdx))
			} else {
				seenWorktrees[a.Worktree] = i
			}
		}

		// Role must be built-in or defined in Roles
		if a.Role != "" && !builtInRoles[a.Role] {
			if _, ok := roles[a.Role]; !ok {
				r.addError(field+".role", fmt.Sprintf(
					"%q is not a built-in role and not defined in roles", a.Role))
			}
		}

		// Agent-level backend override
		validateBackendName(r, field+".backend", a.Backend)
	}
}

// validateRoles checks role configs for valid prompt files and task filters.
func validateRoles(r *ValidationResult, roles map[string]RoleConfig, projectDir string) {
	for name, rc := range roles {
		field := fmt.Sprintf("roles.%s", name)

		// Prompt file existence (warning, not error)
		if rc.PromptFile != "" {
			promptPath := rc.PromptFile
			if !filepath.IsAbs(promptPath) {
				promptPath = filepath.Join(projectDir, promptPath)
			}
			if _, err := os.Stat(promptPath); err != nil {
				r.addWarning(field+".prompt_file", fmt.Sprintf(
					"%q does not exist", rc.PromptFile))
			}
		}

		// Task filter validation
		if rc.TaskFilter != "" && !validTaskFilters[rc.TaskFilter] {
			keys := make([]string, 0, len(validTaskFilters))
			for k := range validTaskFilters {
				keys = append(keys, k)
			}
			r.addError(field+".task_filter", fmt.Sprintf(
				"%q must be one of: %s", rc.TaskFilter, strings.Join(keys, ", ")))
		}
	}
}

// validateRestartPolicy checks restart policy numeric constraints.
func validateRestartPolicy(r *ValidationResult, rp RestartPolicy) {
	if rp.BackoffInitial != nil && *rp.BackoffInitial < 0 {
		r.addError("daemon.restart_policy.backoff_initial", fmt.Sprintf(
			"must be non-negative, got %d", *rp.BackoffInitial))
	}
	if rp.BackoffMax != nil && *rp.BackoffMax < 0 {
		r.addError("daemon.restart_policy.backoff_max", fmt.Sprintf(
			"must be non-negative, got %d", *rp.BackoffMax))
	}
	if rp.BackoffInitial != nil && rp.BackoffMax != nil && *rp.BackoffMax < *rp.BackoffInitial {
		r.addError("daemon.restart_policy.backoff_max", fmt.Sprintf(
			"%d must be >= backoff_initial (%d)", *rp.BackoffMax, *rp.BackoffInitial))
	}
	if rp.OutputTimeout != nil && *rp.OutputTimeout < 0 {
		r.addError("daemon.restart_policy.output_timeout", fmt.Sprintf(
			"must be non-negative, got %d", *rp.OutputTimeout))
	}
	if rp.MaxRetries != nil && *rp.MaxRetries < 0 {
		r.addError("daemon.restart_policy.max_retries", fmt.Sprintf(
			"must be non-negative, got %d", *rp.MaxRetries))
	}
}

// ValidateGlobalConfig validates the global LoomConfig.
func ValidateGlobalConfig(cfg *LoomConfig) *ValidationResult {
	r := &ValidationResult{}
	if cfg == nil {
		return r
	}

	// default_workspace should reference a defined workspace (warning to avoid
	// breaking daemon startup when workspace names change independently)
	if cfg.DefaultWorkspace != "" {
		if _, ok := cfg.Workspaces[cfg.DefaultWorkspace]; !ok {
			r.addWarning("default_workspace", fmt.Sprintf(
				"%q is not defined in workspaces", cfg.DefaultWorkspace))
		}
	}

	// Workspace path existence (warnings only)
	for name, ws := range cfg.Workspaces {
		field := fmt.Sprintf("workspaces.%s", name)
		if ws.Path != "" {
			if info, err := os.Stat(ws.Path); err != nil {
				r.addWarning(field+".path", fmt.Sprintf(
					"%q does not exist", ws.Path))
			} else if !info.IsDir() {
				r.addWarning(field+".path", fmt.Sprintf(
					"%q is not a directory", ws.Path))
			}
		}

		// Repo path existence (warnings only)
		for i, repo := range ws.Repos {
			repoField := fmt.Sprintf("%s.repos[%d]", field, i)
			if repo.Path != "" {
				repoPath := repo.Path
				if !filepath.IsAbs(repoPath) && ws.Path != "" {
					repoPath = filepath.Join(ws.Path, repoPath)
				}
				if _, err := os.Stat(repoPath); err != nil {
					r.addWarning(repoField+".path", fmt.Sprintf(
						"%q does not exist", repo.Path))
				}
			}
		}
	}

	return r
}

// isValidWorktreeName checks that a worktree name contains only safe characters.
func isValidWorktreeName(name string) bool {
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
