package ops

// WorkspaceData represents the full workspace topology returned by the API.
type WorkspaceData struct {
	ID               string               `json:"id"`
	Name             string               `json:"name"`
	Path             string               `json:"path"`
	Repos            []WorkspaceRepo      `json:"repos"`
	Groups           []string             `json:"groups"`
	Agents           []WorkspaceAgentInfo `json:"agents"`
	Workspaces       []WorkspaceSummary   `json:"workspaces"`
	WorkspaceOrder   []string             `json:"workspace_order,omitempty"`
	DefaultWorkspace string               `json:"default_workspace"`
	DesignFormat     string               `json:"design_format,omitempty"`
}

// WorkspaceSummary provides a lightweight summary of a configured workspace.
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
	Name             string   `json:"name"`
	Path             string   `json:"path"`
	DefaultBranch    string   `json:"default_branch"`
	CurrentBranch    string   `json:"current_branch,omitempty"`
	Remote           string   `json:"remote"`
	RemoteURL        string   `json:"remote_url,omitempty"`
	SourceRepoID     string   `json:"source_repo_id,omitempty"`
	Groups           []string `json:"groups"`
	IsLinkedWorktree bool     `json:"is_linked_worktree,omitempty"`
}

// WorkspaceAgentInfo represents an agent's repo/group assignments.
type WorkspaceAgentInfo struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind,omitempty"`
	RoleName   string   `json:"role_name,omitempty"`
	Backend    string   `json:"backend,omitempty"`
	Repos      []string `json:"repos"`
	RepoGroups []string `json:"repo_groups"`
	CrossRepo  bool     `json:"cross_repo"`
}
