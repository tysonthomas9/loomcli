package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/tysonthomas9/loomcli/internal/webui"
)

// deleteWorkspace removes a workspace from config without deleting git worktrees.
// Returns an error if the workspace is not found or has running agents.
func deleteWorkspace(name string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if cfg == nil || len(cfg.Workspaces) == 0 {
		return fmt.Errorf("workspace %q not found", name)
	}

	ws, ok := cfg.Workspaces[name]
	if !ok {
		return fmt.Errorf("workspace %q not found", name)
	}

	// Check for running agents (lock files)
	for _, repo := range ws.Repos {
		repoPath := repo.Path
		if !filepath.IsAbs(repoPath) {
			repoPath = filepath.Join(ws.Path, repoPath)
		}
		lockPath := filepath.Join(repoPath, LockFileName)
		if _, err := os.Stat(lockPath); err == nil {
			return fmt.Errorf("workspace %q has running agents", name)
		}
	}

	// Remove from config
	delete(cfg.Workspaces, name)

	// Update default workspace if needed
	if cfg.DefaultWorkspace == name {
		cfg.DefaultWorkspace = ""
		names := make([]string, 0, len(cfg.Workspaces))
		for n := range cfg.Workspaces {
			names = append(names, n)
		}
		if len(names) > 0 {
			sort.Strings(names)
			cfg.DefaultWorkspace = names[0]
		}
	}

	if err := SaveConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}

// buildWorkspaceInfo loads workspace topology from config and daemon config.
// Returns nil when no workspaces are configured (single-repo mode).
func buildWorkspaceInfo() (*webui.WorkspaceData, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil || len(cfg.Workspaces) == 0 {
		return nil, nil
	}

	// Find the active workspace (use default or first available)
	wsName := cfg.DefaultWorkspace
	ws, ok := cfg.Workspaces[wsName]
	if !ok {
		// Fall back to first workspace
		for name, w := range cfg.Workspaces {
			wsName = name
			ws = w
			break
		}
	}

	groupSet := make(map[string]bool)
	repos := make([]webui.WorkspaceRepo, 0, len(ws.Repos))
	for _, r := range ws.Repos {
		db := r.DefaultBranch
		if db == "" {
			db = "main"
		}
		remote := r.Remote
		if remote == "" {
			remote = "origin"
		}
		repos = append(repos, webui.WorkspaceRepo{
			Name:          r.Name,
			Path:          r.Path,
			DefaultBranch: db,
			Remote:        remote,
			SourceRepoID:  r.SourceRepoID,
			Groups:        r.Groups,
		})
		for _, g := range r.Groups {
			groupSet[g] = true
		}
	}

	// Convert group set to sorted slice
	groups := make([]string, 0, len(groupSet))
	for g := range groupSet {
		groups = append(groups, g)
	}
	sort.Strings(groups)

	// Collect agent assignments from daemon config
	var agents []webui.WorkspaceAgentInfo
	dc, dcErr := LoadDaemonConfig(".")
	if dcErr == nil && dc != nil {
		for _, a := range dc.Agents {
			name := filepath.Base(a.Worktree)
			agents = append(agents, webui.WorkspaceAgentInfo{
				Name:       name,
				Repos:      a.Repos,
				RepoGroups: a.RepoGroups,
				CrossRepo:  a.CrossRepo,
			})
		}
	}

	// Build lightweight summaries for all configured workspaces
	summaries := make([]webui.WorkspaceSummary, 0, len(cfg.Workspaces))
	for name, w := range cfg.Workspaces {
		summaries = append(summaries, webui.WorkspaceSummary{
			Name:      name,
			Path:      w.Path,
			Active:    name == wsName,
			RepoCount: len(w.Repos),
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Name < summaries[j].Name
	})

	return &webui.WorkspaceData{
		Name:       wsName,
		Path:       ws.Path,
		Repos:      repos,
		Groups:     groups,
		Agents:     agents,
		Workspaces: summaries,
	}, nil
}
