package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// RoleConfig defines an agent role (built-in like "plan"/"task", or custom).
type RoleConfig struct {
	Kind           string   `yaml:"kind,omitempty"`
	Description    string   `yaml:"description,omitempty"`
	Prompt         string   `yaml:"prompt,omitempty"`
	PromptFile     string   `yaml:"prompt_file,omitempty"`
	Model          string   `yaml:"model,omitempty"`
	TaskFilter     string   `yaml:"task_filter,omitempty"`
	Backend        string   `yaml:"backend,omitempty"`
	Effort         string   `yaml:"effort,omitempty"`
	PathPatterns   []string `yaml:"path_patterns,omitempty"`
	Skills         []string `yaml:"skills,omitempty"`
	MaxPriority    *int     `yaml:"max_priority,omitempty"`
	MaxConcurrency *int     `yaml:"max_concurrency,omitempty"`
	ReadOnly       bool     `yaml:"read_only,omitempty"`
	AllowedTools   []string `yaml:"allowed_tools,omitempty"`
	DeniedTools    []string `yaml:"denied_tools,omitempty"`
	MaxBudgetUSD   *float64 `yaml:"max_budget_usd,omitempty"`
}

// AgentEntry defines a single agent assignment.
//
// Multi-repo routing fields:
//
//	repos: ["backend", "frontend"]         # explicit repo names this agent handles
//	repo_groups: ["infra", "data"]         # bind to groups defined in RepoConfig
//	cross_repo: true                       # agent can pick up tasks spanning repos
//
// An agent with neither repos nor repo_groups can work on any repo.
type AgentEntry struct {
	Worktree         string                   `yaml:"worktree"`
	Role             string                   `yaml:"role"`
	Repo             string                   `yaml:"repo,omitempty"`
	Auto             bool                     `yaml:"auto,omitempty"`
	Backend          string                   `yaml:"backend,omitempty"`
	FallbackBackends []string                 `yaml:"fallback_backends,omitempty"`
	PathPatterns     []string                 `yaml:"path_patterns,omitempty"`
	SourceRepos      []string                 `yaml:"-" json:"-"` // resolved repo IDs; env-only transport, not persisted in YAML
	Repos            []string                 `yaml:"repos,omitempty"`
	RepoGroups       []string                 `yaml:"repo_groups,omitempty"`
	CrossRepo        bool                     `yaml:"cross_repo,omitempty"`
	Parent           string                   `yaml:"parent,omitempty"` // epic ID to scope this agent to; empty = no epic assignment
	Mode             domain.AgentMode         `yaml:"mode,omitempty"`   // ephemeral: exit cleanly after one successful task; service: loop forever (default)
	DesiredState     domain.AgentDesiredState `yaml:"desired_state,omitempty"`
}

// Equal compares persisted config fields only (excludes SourceRepos). Update when adding fields.
func (a AgentEntry) Equal(b AgentEntry) bool {
	return a.Worktree == b.Worktree && a.Role == b.Role && a.Repo == b.Repo &&
		a.Auto == b.Auto && a.Backend == b.Backend && a.CrossRepo == b.CrossRepo && a.Parent == b.Parent &&
		a.Mode == b.Mode &&
		a.DesiredState == b.DesiredState &&
		slices.Equal(a.FallbackBackends, b.FallbackBackends) && slices.Equal(a.PathPatterns, b.PathPatterns) &&
		slices.Equal(a.Repos, b.Repos) && slices.Equal(a.RepoGroups, b.RepoGroups)
}

// ShouldRun reports whether durable desired state permits this non-interactive
// agent to execute.
func (a AgentEntry) ShouldRun() bool {
	if domain.IsInteractiveRoleName(a.Role) {
		return false
	}
	return a.shouldRunByDesiredState()
}

// ShouldRunWithRoles applies role-kind metadata before desired state.
func (a AgentEntry) ShouldRunWithRoles(roles map[string]RoleConfig) bool {
	if rc, ok := roles[a.Role]; ok {
		role := &domain.Role{Kind: domain.RoleKind(rc.Kind)}
		if domain.ResolveRoleKind(role, a.Role) == domain.RoleKindInteractive {
			return false
		}
		return a.shouldRunByDesiredState()
	}
	return a.ShouldRun()
}

func (a AgentEntry) shouldRunByDesiredState() bool {
	switch a.DesiredState {
	case domain.AgentDesiredStopped, domain.AgentDesiredDraining:
		return false
	default:
		return true
	}
}

// RuntimeConfig is the machine-local runtime selector used during CLI startup.
// Durable Roles and Agents are owned by the Agents capability and are not
// projected into process configuration.
type RuntimeConfig struct {
	Backend string `yaml:"backend,omitempty"`
}

// LoadRuntimeConfig returns the node-local runtime provider for the active
// workspace. It deliberately does not open FleetDB or read the retired
// Role/Agent configuration projection.
func LoadRuntimeConfig(ctx context.Context, projectDir string) (*RuntimeConfig, error) {
	_ = projectDir
	dc := &RuntimeConfig{}
	key, err := bootstrap.ResolveActiveWorkspaceKey(ctx, nil)
	if err != nil {
		if errors.Is(err, bootstrap.ErrNoActiveWorkspace) {
			return dc, nil
		}
		return nil, fmt.Errorf("resolve active workspace: %w", err)
	}
	backend, err := bootstrap.RuntimeProvider(key)
	if err != nil {
		return nil, fmt.Errorf("load local node runtime provider: %w", err)
	}
	if backend != "" {
		dc.Backend = backend
	}
	return dc, nil
}

// validateAgents checks that agent entries are valid.
func validateAgents(agents []AgentEntry) error {
	for i, a := range agents {
		if a.Worktree == "" {
			return fmt.Errorf("agent[%d]: worktree is required", i)
		}
		if a.Role == "" {
			return fmt.Errorf("agent[%d]: role is required", i)
		}
		for j, fb := range a.FallbackBackends {
			if fb == "" {
				return fmt.Errorf("agent[%d]: fallback_backends[%d] is empty", i, j)
			}
		}
	}
	return nil
}

// ValidateAgentRepos checks that agent Repo fields reference valid repos in the workspace config.
// In workspace mode, unknown repo names are hard errors. Outside workspace mode, Repo fields
// trigger a warning but are not blocking.
func ValidateAgentRepos(ctx context.Context, agents []AgentEntry) error {
	// Check if any agent uses Repo
	hasRepo := false
	for _, a := range agents {
		if a.Repo != "" {
			hasRepo = true
			break
		}
	}
	if !hasRepo {
		return nil
	}

	ws, err := ResolveActiveWorkspace(ctx)
	if err != nil {
		return fmt.Errorf("validating agent repos: %w", err)
	}

	if ws == nil {
		// Not in workspace mode — warn but don't fail
		fmt.Fprintf(os.Stderr, "Warning: agent(s) declare repo but no workspace is configured; repo field will be ignored\n")
		return nil
	}

	// Build set of valid repo names
	repoNames := make(map[string]bool, len(ws.Repos))
	for _, r := range ws.Repos {
		repoNames[r.Name] = true
	}

	for i, a := range agents {
		if a.Repo == "" {
			continue
		}
		if !repoNames[a.Repo] {
			available := make([]string, 0, len(ws.Repos))
			for _, r := range ws.Repos {
				available = append(available, r.Name)
			}
			return fmt.Errorf("agent[%d]: repo %q not found in workspace; available repos: %v", i, a.Repo, available)
		}
	}
	return nil
}

// resolveRepoPath looks up a repo by name in the active workspace config and returns
// its absolute path. Returns an error if the repo is not found or the path doesn't exist.
func resolveRepoPath(ctx context.Context, repoName string) (string, error) {
	ws, err := ResolveActiveWorkspace(ctx)
	if err != nil {
		return "", fmt.Errorf("resolving workspace: %w", err)
	}
	if ws == nil {
		return "", fmt.Errorf("no active workspace configured")
	}

	for _, repo := range ws.Repos {
		if repo.Name == repoName {
			absPath := repo.ResolveAbsPath(ws.Path)
			if info, err := os.Stat(absPath); err != nil {
				return "", fmt.Errorf("repo path %q does not exist: %w", absPath, err)
			} else if !info.IsDir() {
				return "", fmt.Errorf("repo path %q is not a directory", absPath)
			}
			return absPath, nil
		}
	}
	return "", fmt.Errorf("repo %q not found in workspace", repoName)
}

func IntPtr(v int) *int    { return &v }
func BoolPtr(v bool) *bool { return &v }

// resolvePath resolves a path relative to baseDir, or returns as-is if absolute.
func resolvePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}
