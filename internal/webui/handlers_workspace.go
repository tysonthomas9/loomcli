package webui

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type workspaceResponse struct {
	Success bool           `json:"success"`
	Data    *WorkspaceData `json:"data,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// WorkspaceConfigByNameFn resolves workspace data for a specific workspace name.
// When name is empty, it should return the default workspace (same as configFn).
type WorkspaceConfigByNameFn func(name string) (*WorkspaceData, error)

// handleWorkspace returns workspace topology (repos with names and paths).
// If configFn is nil, returns an empty workspace (single-repo mode).
// When a Workspace header is present and names a workspace different from the
// default, the handler re-resolves the response so it returns that workspace's
// repos and agents instead of the default's.
func handleWorkspace(configFn func() (*WorkspaceData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if configFn == nil {
			respondJSON(w, http.StatusOK, workspaceResponse{
				Success: true,
				Data:    emptyWorkspaceData(),
			})
			return
		}

		data, err := configFn()
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, workspaceResponse{
				Success: false,
				Error:   "failed to load workspace config",
			})
			return
		}

		if data == nil {
			data = &WorkspaceData{}
		}

		// If the Workspace header requests a different workspace than the one
		// configFn returned (which defaults to cfg.DefaultWorkspace), look up
		// that workspace in the summaries and swap in its repos/agents/name/path.
		requestedWS := strings.TrimSpace(r.Header.Get("Workspace"))
		if requestedWS != "" && requestedWS != data.Name && requestedWS != data.ID {
			if resolved := resolveWorkspaceOverride(data, requestedWS); resolved != nil {
				data = resolved
			}
		}

		normalizeWorkspaceData(data)

		respondJSON(w, http.StatusOK, workspaceResponse{
			Success: true,
			Data:    data,
		})
	}
}

// emptyWorkspaceData returns a WorkspaceData with all slices initialized to empty (not nil).
func emptyWorkspaceData() *WorkspaceData {
	return &WorkspaceData{
		Repos:      []WorkspaceRepo{},
		Groups:     []string{},
		Agents:     []WorkspaceAgentInfo{},
		Workspaces: []WorkspaceSummary{},
	}
}

// resolveWorkspaceOverride re-loads workspace config for a specific workspace
// when the Workspace header names a workspace that differs from the default.
// It reuses the workspace summaries from the original data but loads the
// target workspace's repos and agents from disk via the global config path.
// Returns nil if the workspace is not found in the summaries (caller keeps
// the original data).
func resolveWorkspaceOverride(orig *WorkspaceData, targetName string) *WorkspaceData {
	// Verify the target exists in the known summaries (match by ID or Name)
	var targetSummary *WorkspaceSummary
	for i := range orig.Workspaces {
		if orig.Workspaces[i].ID == targetName || orig.Workspaces[i].Name == targetName {
			targetSummary = &orig.Workspaces[i]
			break
		}
	}
	if targetSummary == nil {
		return nil // unknown workspace, keep original
	}

	// Load repos and agents for the target workspace from its daemon config.
	// The workspace path is available in the summary.
	repos, agents := loadWorkspaceReposAndAgents(targetSummary.Path)

	// Clone summaries, updating the Active flag
	newSummaries := make([]WorkspaceSummary, len(orig.Workspaces))
	copy(newSummaries, orig.Workspaces)
	for i := range newSummaries {
		newSummaries[i].Active = newSummaries[i].Name == targetSummary.Name
	}

	return &WorkspaceData{
		ID:               targetSummary.ID,
		Name:             targetSummary.Name,
		Path:             targetSummary.Path,
		Repos:            repos,
		Groups:           nil,
		Agents:           agents,
		Workspaces:       newSummaries,
		WorkspaceOrder:   orig.WorkspaceOrder,
		DefaultWorkspace: orig.DefaultWorkspace,
	}
}

// loomYAMLAgent is the minimal agent entry read from loom.yaml.
type loomYAMLAgent struct {
	Worktree   string   `yaml:"worktree"`
	Repos      []string `yaml:"repos,omitempty"`
	RepoGroups []string `yaml:"repo_groups,omitempty"`
	CrossRepo  bool     `yaml:"cross_repo,omitempty"`
}

// loomYAML is the minimal project-level config read from loom.yaml.
type loomYAML struct {
	Agents []loomYAMLAgent `yaml:"agents"`
}

// loadWorkspaceReposAndAgents reads the loom.yaml from a workspace directory
// and returns repos (derived from subdirectories), groups, and agents.
// This is used by resolveWorkspaceOverride to load data for non-default workspaces
// without needing to import the cli package.
func loadWorkspaceReposAndAgents(wsPath string) ([]WorkspaceRepo, []WorkspaceAgentInfo) {
	var repos []WorkspaceRepo
	var agents []WorkspaceAgentInfo

	// Read loom.yaml for agents
	yamlPath := filepath.Clean(filepath.Join(wsPath, "loom.yaml"))
	if data, err := os.ReadFile(yamlPath); err == nil {
		var ly loomYAML
		if err := yaml.Unmarshal(data, &ly); err == nil {
			for _, a := range ly.Agents {
				name := filepath.Base(a.Worktree)
				agents = append(agents, WorkspaceAgentInfo{
					Name:       name,
					Repos:      a.Repos,
					RepoGroups: a.RepoGroups,
					CrossRepo:  a.CrossRepo,
				})
			}
		} else {
			slog.Warn("failed to parse loom.yaml", "path", yamlPath, "err", err)
		}
	}

	// Scan subdirectories for repos (directories containing .git)
	entries, err := os.ReadDir(wsPath)
	if err != nil {
		slog.Warn("failed to read workspace directory", "path", wsPath, "err", err)
		return repos, agents
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		subPath := filepath.Join(wsPath, entry.Name())
		// Check if it's a git repo (has .git directory or file)
		if _, err := os.Stat(filepath.Join(subPath, ".git")); err == nil {
			repos = append(repos, WorkspaceRepo{
				Name:          entry.Name(),
				Path:          subPath,
				DefaultBranch: "main",
				Remote:        "origin",
				SourceRepoID:  entry.Name(),
				Groups:        []string{},
			})
		}
	}
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].Name < repos[j].Name
	})

	return repos, agents
}

// normalizeWorkspaceData ensures all slice fields are non-nil so JSON marshals as [] not null.
func normalizeWorkspaceData(data *WorkspaceData) {
	if data.Repos == nil {
		data.Repos = []WorkspaceRepo{}
	}
	if data.Groups == nil {
		data.Groups = []string{}
	}
	if data.Agents == nil {
		data.Agents = []WorkspaceAgentInfo{}
	}
	if data.Workspaces == nil {
		data.Workspaces = []WorkspaceSummary{}
	}
	for i := range data.Repos {
		if data.Repos[i].Groups == nil {
			data.Repos[i].Groups = []string{}
		}
	}
	for i := range data.Agents {
		if data.Agents[i].Repos == nil {
			data.Agents[i].Repos = []string{}
		}
		if data.Agents[i].RepoGroups == nil {
			data.Agents[i].RepoGroups = []string{}
		}
	}
}
