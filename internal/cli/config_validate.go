package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	if dc.Daemon.FleetDB != nil {
		validateFleetDBSettings(r, dc.Daemon.FleetDB)
	}
	validateIssueBackend(r, dc.Daemon.IssueBackend)
	if dc.Daemon.Fleet != nil {
		validateFleetSettings(r, dc.Daemon.Fleet)
	}
	// Cross-field: fleet mode with no URL configured
	if dc.Daemon.IssueBackend == IssueBackendFleet && (dc.Daemon.Fleet == nil || dc.Daemon.Fleet.URL == "") {
		r.addWarning("daemon.fleet.url", "issue_backend is 'fleet' but daemon.fleet.url is not configured; set daemon.fleet.url or LOOM_FLEET_URL")
	}

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

		// Role name character validation (defense in depth against path traversal)
		if a.Role != "" && !isValidRoleName(a.Role) {
			r.addError(field+".role", fmt.Sprintf(
				"%q contains invalid characters; use only alphanumeric, hyphens, and underscores", a.Role))
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

		// Validate Repos entries are non-empty strings
		for j, repo := range a.Repos {
			if repo == "" {
				r.addError(fmt.Sprintf("%s.repos[%d]", field, j), "repo name must not be empty")
			}
		}

		// Validate RepoGroups entries are non-empty and valid format
		for j, rg := range a.RepoGroups {
			if rg == "" {
				r.addError(fmt.Sprintf("%s.repo_groups[%d]", field, j), "repo group name must not be empty")
			} else if !isValidGroupName(rg) {
				r.addError(fmt.Sprintf("%s.repo_groups[%d]", field, j), fmt.Sprintf(
					"%q is invalid; group names must be lowercase alphanumeric with hyphens, starting with alphanumeric", rg))
			}
		}
	}
}

// validateRoles checks role configs for valid prompt files and task filters.
func validateRoles(r *ValidationResult, roles map[string]RoleConfig, projectDir string) {
	roleNames := make([]string, 0, len(roles))
	for name := range roles {
		roleNames = append(roleNames, name)
	}
	sort.Strings(roleNames)
	for _, name := range roleNames {
		rc := roles[name]
		field := fmt.Sprintf("roles.%s", name)

		// Role name character validation (defense in depth against path traversal)
		if !isValidRoleName(name) {
			r.addError(field, fmt.Sprintf(
				"role name %q contains invalid characters; use only alphanumeric, hyphens, and underscores", name))
		}

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
			sort.Strings(keys)
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

// validateFleetDBSettings checks fleet-db config fields for validity.
func validateFleetDBSettings(r *ValidationResult, fdb *FleetDBSettings) {
	if fdb.RedisURL != "" {
		if !strings.HasPrefix(fdb.RedisURL, "redis://") && !strings.HasPrefix(fdb.RedisURL, "rediss://") {
			r.addWarning("daemon.fleetdb.redis_url", fmt.Sprintf(
				"%q does not start with redis:// or rediss://", fdb.RedisURL))
		}
	}
	if fdb.Workspace != "" && !isValidWorktreeName(fdb.Workspace) {
		r.addWarning("daemon.fleetdb.workspace", fmt.Sprintf(
			"%q contains invalid characters; use only alphanumeric, hyphens, and underscores", fdb.Workspace))
	}
}

// validateIssueBackend checks that an issue_backend value (if set) is one of the valid options.
func validateIssueBackend(r *ValidationResult, ib string) {
	if ib == "" {
		return
	}
	if !validIssueBackends[ib] {
		r.addError("daemon.issue_backend", fmt.Sprintf("%q must be one of: beads, fleetdb, fleet", ib))
	}
}

// validateFleetSettings checks fleet client config fields for validity.
func validateFleetSettings(r *ValidationResult, fs *FleetSettings) {
	if fs.URL != "" {
		if !strings.HasPrefix(fs.URL, "http://") && !strings.HasPrefix(fs.URL, "https://") {
			r.addError("daemon.fleet.url", fmt.Sprintf(
				"%q must start with http:// or https://", fs.URL))
		}
	}
	if fs.Workspace != "" && !isValidWorktreeName(fs.Workspace) {
		r.addWarning("daemon.fleet.workspace", fmt.Sprintf(
			"%q contains invalid characters; use only alphanumeric, hyphens, and underscores", fs.Workspace))
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

	// Workspace validation
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
		validateWorkspaceRepos(r, field, ws)
	}

	if cfg.Daemon != nil {
		if cfg.Daemon.FleetDB != nil {
			validateFleetDBSettings(r, cfg.Daemon.FleetDB)
		}
		validateIssueBackend(r, cfg.Daemon.IssueBackend)
		if cfg.Daemon.Fleet != nil {
			validateFleetSettings(r, cfg.Daemon.Fleet)
		}
	}

	return r
}

// validateWorkspaceRepos validates repos within a workspace for path existence,
// group name format, and SourceRepoID uniqueness.
func validateWorkspaceRepos(r *ValidationResult, wsField string, ws WorkspaceConfig) {
	seenSourceRepoIDs := make(map[string]string) // sourceRepoID -> "repoName"
	for i, repo := range ws.Repos {
		repoField := fmt.Sprintf("%s.repos[%d]", wsField, i)

		// Repo path existence (warnings only)
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

		// Group name validation
		for j, group := range repo.Groups {
			if !isValidGroupName(group) {
				r.addError(fmt.Sprintf("%s.groups[%d]", repoField, j), fmt.Sprintf(
					"%q is invalid; group names must be lowercase alphanumeric with hyphens, starting with alphanumeric", group))
			}
		}

		// SourceRepoID uniqueness (use Name as effective ID if not set)
		effectiveID := repo.SourceRepoID
		if effectiveID == "" {
			effectiveID = repo.Name
		}
		if effectiveID != "" {
			if otherRepo, ok := seenSourceRepoIDs[effectiveID]; ok {
				r.addError(repoField+".source_repo_id", fmt.Sprintf(
					"source_repo_id %q is a duplicate (also used by repo %q)", effectiveID, otherRepo))
			} else {
				seenSourceRepoIDs[effectiveID] = repo.Name
			}
		}
	}
}

// isValidGroupName checks that a group name is lowercase alphanumeric with hyphens,
// starting with an alphanumeric character.
func isValidGroupName(name string) bool {
	if name == "" {
		return false
	}
	first := name[0]
	if !((first >= 'a' && first <= 'z') || (first >= '0' && first <= '9')) {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

// isValidRoleName checks that a role name contains only safe characters.
// Same rules as worktree names: alphanumeric, hyphens, underscores.
// Unlike isValidWorktreeName, empty is rejected here because role names
// are never structurally optional — an empty role is always a mistake.
func isValidRoleName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
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
