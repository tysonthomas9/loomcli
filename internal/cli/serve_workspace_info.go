package cli

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

// buildWorkspaceInfo loads workspace topology from config and daemon config.
// Returns nil when no workspaces are configured (single-repo mode).
func buildWorkspaceInfo() (*ops.WorkspaceData, error) {
	return buildWorkspaceInfoForName("")
}

// buildWorkspaceInfoForName loads workspace topology for a specific workspace.
// When targetName is empty, it uses the default workspace from config.
// Returns nil when no workspaces are configured (single-repo mode).
func buildWorkspaceInfoForName(targetName string) (*ops.WorkspaceData, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil || len(cfg.Workspaces) == 0 {
		return nil, nil
	}

	// Find the active workspace: use targetName if provided, else default, else first
	wsName := targetName
	if wsName == "" {
		wsName = cfg.DefaultWorkspace
	}
	ws, ok := cfg.Workspaces[wsName]
	if !ok {
		wsName = cfg.DefaultWorkspace
		ws, ok = cfg.Workspaces[wsName]
	}
	if !ok {
		for name, w := range cfg.Workspaces {
			wsName = name
			ws = w
			break
		}
	}

	groupSet := make(map[string]bool)
	repos := make([]ops.WorkspaceRepo, 0, len(ws.Repos))
	for _, r := range ws.Repos {
		db := r.DefaultBranch
		if db == "" {
			db = "main"
		}
		remote := r.Remote
		if remote == "" {
			remote = "origin"
		}
		repos = append(repos, ops.WorkspaceRepo{
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

	groups := make([]string, 0, len(groupSet))
	for g := range groupSet {
		groups = append(groups, g)
	}
	sort.Strings(groups)

	var agents []ops.WorkspaceAgentInfo
	dc, dcErr := LoadDaemonConfig(ws.Path)
	if dcErr != nil {
		slog.Warn("failed to load daemon config for workspace", "path", ws.Path, "err", dcErr)
	}
	if dcErr == nil && dc != nil {
		for _, a := range dc.Agents {
			name := filepath.Base(a.Worktree)
			agents = append(agents, ops.WorkspaceAgentInfo{
				Name:       name,
				Repos:      a.Repos,
				RepoGroups: a.RepoGroups,
				CrossRepo:  a.CrossRepo,
			})
		}
	}

	summaries := make([]ops.WorkspaceSummary, 0, len(cfg.Workspaces))
	for name, w := range cfg.Workspaces {
		summaries = append(summaries, ops.WorkspaceSummary{
			ID:        w.ID,
			Name:      name,
			Path:      w.Path,
			Active:    name == wsName,
			RepoCount: len(w.Repos),
			IsDefault: name == cfg.DefaultWorkspace,
			Backend:   w.Backend,
		})
	}
	sortWorkspaceSummaries(summaries, cfg.WorkspaceOrder)

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

// buildWorkspaceInfoForID loads workspace topology for a specific workspace UUID.
// Resolves the UUID to a config name, then delegates to buildWorkspaceInfoForName.
func buildWorkspaceInfoForID(targetID string) (*ops.WorkspaceData, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil || len(cfg.Workspaces) == 0 {
		return nil, fmt.Errorf("workspace not found: %s", targetID)
	}

	for name, ws := range cfg.Workspaces {
		if ws.ID == targetID {
			return buildWorkspaceInfoForName(name)
		}
	}
	return nil, fmt.Errorf("workspace not found: %s", targetID)
}

// sortWorkspaceSummaries sorts workspace summaries using custom order if provided.
func sortWorkspaceSummaries(summaries []ops.WorkspaceSummary, order []string) {
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
