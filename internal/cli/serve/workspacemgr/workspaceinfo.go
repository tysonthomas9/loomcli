package workspacemgr

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
	"github.com/tysonthomas9/loomcli/internal/ops"
)

// BuildWorkspaceInfo loads workspace topology from config and daemon config.
// Returns nil when no workspaces are configured (single-repo mode).
func BuildWorkspaceInfo() (*ops.WorkspaceData, error) {
	return BuildWorkspaceInfoForName("")
}

// BuildWorkspaceInfoForName loads workspace topology for a specific workspace.
// When targetName is empty, it uses the default workspace from config.
// Returns nil when no workspaces are configured (single-repo mode).
func BuildWorkspaceInfoForName(targetName string) (*ops.WorkspaceData, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil || len(cfg.Workspaces) == 0 {
		return nil, nil
	}

	wsName, ws := resolveActiveWorkspace(cfg, targetName)
	repos, groups := buildRepoList(ws.Repos)
	agents := loadWorkspaceAgents(ws.Path)
	summaries := buildWorkspaceSummaries(cfg, wsName)

	return &ops.WorkspaceData{
		ID:               ws.ID,
		Name:             wsName,
		Path:             ws.Path,
		Repos:            repos,
		Groups:           groups,
		Agents:           agents,
		Workspaces:       summaries,
		WorkspaceOrder:   cfg.WorkspaceOrder,
		DefaultWorkspace: cfg.DefaultWorkspace,
	}, nil
}

// resolveActiveWorkspace finds the workspace to use: targetName, default, or first available.
func resolveActiveWorkspace(cfg *config.LoomConfig, targetName string) (string, config.WorkspaceConfig) {
	wsName := targetName
	if wsName == "" {
		wsName = cfg.DefaultWorkspace
	}
	if ws, ok := cfg.Workspaces[wsName]; ok {
		return wsName, ws
	}
	wsName = cfg.DefaultWorkspace
	if ws, ok := cfg.Workspaces[wsName]; ok {
		return wsName, ws
	}
	for name, ws := range cfg.Workspaces {
		return name, ws
	}
	return "", config.WorkspaceConfig{}
}

// buildRepoList converts config repos to ops repos and collects group names.
func buildRepoList(cfgRepos []config.RepoConfig) ([]ops.WorkspaceRepo, []string) {
	groupSet := make(map[string]bool)
	repos := make([]ops.WorkspaceRepo, 0, len(cfgRepos))
	for _, r := range cfgRepos {
		db := r.DefaultBranch
		if db == "" {
			db = "main"
		}
		remote := r.Remote
		if remote == "" {
			remote = "origin"
		}
		repos = append(repos, ops.WorkspaceRepo{
			Name: r.Name, Path: r.Path, DefaultBranch: db,
			Remote: remote, SourceRepoID: r.SourceRepoID, Groups: r.Groups,
			IsLinkedWorktree: isGitLinkedWorktree(r.Path),
		})
		for _, g := range r.Groups {
			groupSet[g] = true
		}
	}
	groups := make([]string, 0, len(groupSet))
	for g := range groupSet {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	return repos, groups
}

// isGitLinkedWorktree reports whether path has a .git file (linked worktree)
// rather than a .git directory (source repo).
func isGitLinkedWorktree(repoPath string) bool {
	fi, err := os.Lstat(filepath.Join(repoPath, ".git"))
	if err != nil {
		return false
	}
	return !fi.IsDir()
}

// loadWorkspaceAgents loads agent info from the daemon config for a workspace path.
func loadWorkspaceAgents(wsPath string) []ops.WorkspaceAgentInfo {
	dc, dcErr := config.LoadDaemonConfig(wsPath)
	if dcErr != nil {
		slog.Warn("failed to load daemon config for workspace", "path", wsPath, "err", dcErr)
		return nil
	}
	if dc == nil {
		return nil
	}
	agents := make([]ops.WorkspaceAgentInfo, 0, len(dc.Agents))
	for _, a := range dc.Agents {
		agents = append(agents, ops.WorkspaceAgentInfo{
			Name: filepath.Base(a.Worktree), Repos: a.Repos,
			RepoGroups: a.RepoGroups, CrossRepo: a.CrossRepo,
		})
	}
	return agents
}

// buildWorkspaceSummaries builds summary entries for all configured workspaces.
func buildWorkspaceSummaries(cfg *config.LoomConfig, activeWS string) []ops.WorkspaceSummary {
	summaries := make([]ops.WorkspaceSummary, 0, len(cfg.Workspaces))
	for name, w := range cfg.Workspaces {
		repoCount := 0
		for _, r := range w.Repos {
			if !isGitLinkedWorktree(r.Path) {
				repoCount++
			}
		}
		summaries = append(summaries, ops.WorkspaceSummary{
			ID: w.ID, Name: name, Path: w.Path, Active: name == activeWS,
			RepoCount: repoCount, IsDefault: name == cfg.DefaultWorkspace, Backend: w.Backend,
			State: workspace.WSState(w.State), ErrorMessage: w.ErrorMessage,
		})
	}
	SortWorkspaceSummaries(summaries, cfg.WorkspaceOrder)
	return summaries
}

// BuildWorkspaceInfoForID loads workspace topology for a specific workspace UUID.
// Resolves the UUID to a config name, then delegates to BuildWorkspaceInfoForName.
func BuildWorkspaceInfoForID(targetID string) (*ops.WorkspaceData, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil || len(cfg.Workspaces) == 0 {
		return nil, fmt.Errorf("workspace not found: %s", targetID)
	}

	for name, ws := range cfg.Workspaces {
		if ws.ID == targetID {
			return BuildWorkspaceInfoForName(name)
		}
	}
	return nil, fmt.Errorf("workspace not found: %s", targetID)
}

// SortWorkspaceSummaries sorts workspace summaries using custom order if provided.
func SortWorkspaceSummaries(summaries []ops.WorkspaceSummary, order []string) {
	if len(order) > 0 {
		orderIndex := make(map[string]int, len(order))
		for i, name := range order {
			orderIndex[name] = i
		}
		sort.SliceStable(summaries, func(i, j int) bool {
			oi, okI := orderIndex[summaries[i].Name]
			oj, okJ := orderIndex[summaries[j].Name]
			if okI && okJ {
				return oi < oj
			}
			if okI {
				return true
			}
			if okJ {
				return false
			}
			return summaries[i].Name < summaries[j].Name
		})
	} else {
		sort.Slice(summaries, func(i, j int) bool {
			return summaries[i].Name < summaries[j].Name
		})
	}
}
