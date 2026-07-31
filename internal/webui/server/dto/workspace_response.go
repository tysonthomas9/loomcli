package dto

import (
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// WorkspaceResponse is the full workspace topology returned by the API.
// Field names and JSON tags match ops.WorkspaceData.
type WorkspaceResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`

	// Initialized to empty slice by mapper; must serialize as [] not null.
	Repos []WorkspaceRepo `json:"repos"`
	// Initialized to empty slice by mapper; must serialize as [] not null.
	Groups []string `json:"groups"`
	// Initialized to empty slice by mapper; must serialize as [] not null.
	Agents []WorkspaceAgentInfo `json:"agents"`
	// Initialized to empty slice by mapper; must serialize as [] not null.
	Workspaces []WorkspaceSummary `json:"workspaces"`

	WorkspaceOrder   []string `json:"workspace_order,omitempty"` // Optional
	DefaultWorkspace string   `json:"default_workspace"`
}

// WorkspaceSummary is a lightweight summary of a configured workspace.
type WorkspaceSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Path         string `json:"path"`
	Active       bool   `json:"active"`
	RepoCount    int    `json:"repo_count"`
	IsDefault    bool   `json:"is_default"`
	Backend      string `json:"backend,omitempty"`
	State        string `json:"state,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// WorkspaceRepo represents a repository within a workspace.
type WorkspaceRepo struct {
	Name             string `json:"name"`
	Path             string `json:"path"`
	DefaultBranch    string `json:"default_branch"`
	CurrentBranch    string `json:"current_branch,omitempty"`
	Remote           string `json:"remote"`
	RemoteURL        string `json:"remote_url,omitempty"`
	SourceRepoID     string `json:"source_repo_id,omitempty"`
	IsLinkedWorktree bool   `json:"is_linked_worktree,omitempty"`
	// Initialized to empty slice by mapper; must serialize as [] not null.
	Groups []string `json:"groups"`
}

// WorkspaceAgentInfo represents agent repo/group assignments within a workspace.
type WorkspaceAgentInfo struct {
	Name     string `json:"name"`
	RoleName string `json:"role_name,omitempty"`
	RoleKind string `json:"role_kind,omitempty"`
	Backend  string `json:"backend,omitempty"`
	// Initialized to empty slice by mapper; must serialize as [] not null.
	Repos []string `json:"repos"`
	// Initialized to empty slice by mapper; must serialize as [] not null.
	RepoGroups []string `json:"repo_groups"`
	CrossRepo  bool     `json:"cross_repo"`
}

// NewWorkspaceAgentInfo maps a domain agent into the web workspace contract.
// Keep nil repo assignments as JSON [] so create/list payloads match the
// frontend WorkspaceAgentInfo shape.
func NewWorkspaceAgentInfo(agent *domain.Agent) WorkspaceAgentInfo {
	return NewWorkspaceAgentInfoWithRoleKind(agent, "")
}

// NewWorkspaceAgentInfoWithRoleKind maps a domain agent plus the effective role
// kind into the web workspace contract.
func NewWorkspaceAgentInfoWithRoleKind(agent *domain.Agent, roleKind string) WorkspaceAgentInfo {
	if agent == nil {
		return WorkspaceAgentInfo{
			Repos:      []string{},
			RepoGroups: []string{},
		}
	}
	repos := append([]string{}, agent.Repos...)
	if repos == nil {
		repos = []string{}
	}
	repoGroups := append([]string{}, agent.RepoGroups...)
	if repoGroups == nil {
		repoGroups = []string{}
	}
	return WorkspaceAgentInfo{
		Name:       agent.Name,
		RoleName:   agent.RoleName,
		RoleKind:   normalizeWorkspaceRoleKind(roleKind),
		Backend:    agent.Backend,
		Repos:      repos,
		RepoGroups: repoGroups,
		CrossRepo:  agent.CrossRepo,
	}
}

func normalizeWorkspaceRoleKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case string(domain.RoleKindInteractive):
		return string(domain.RoleKindInteractive)
	case string(domain.RoleKindWorker):
		return string(domain.RoleKindWorker)
	default:
		return ""
	}
}
