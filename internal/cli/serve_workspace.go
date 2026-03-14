package cli

import (
	"path/filepath"
	"sort"

	"github.com/tysonthomas9/loomcli/internal/webui"
)

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

	return &webui.WorkspaceData{
		Name:   wsName,
		Path:   ws.Path,
		Repos:  repos,
		Groups: groups,
		Agents: agents,
	}, nil
}
