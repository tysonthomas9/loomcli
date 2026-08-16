// Package operationalview defines the immutable cross-owner Workspace
// topology projection consumed by delivery and local Source Control mechanics.
package operationalview

// Workspace represents the complete cross-owner Workspace topology.
type Workspace struct {
	ID               string       `json:"id"`
	Name             string       `json:"name"`
	Path             string       `json:"path"`
	Repos            []Repository `json:"repos"`
	Groups           []string     `json:"groups"`
	Agents           []Agent      `json:"agents"`
	Workspaces       []Summary    `json:"workspaces"`
	WorkspaceOrder   []string     `json:"workspace_order,omitempty"`
	DefaultWorkspace string       `json:"default_workspace"`
	DesignFormat     string       `json:"design_format,omitempty"`
}

// Summary is the immutable catalog summary for one Workspace.
type Summary struct {
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

// Repository is the immutable Workspace plus Source Control repository view.
type Repository struct {
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

// Agent is the immutable Agents plus Workspace placement view.
type Agent struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind,omitempty"`
	RoleName   string   `json:"role_name,omitempty"`
	Backend    string   `json:"backend,omitempty"`
	Repos      []string `json:"repos"`
	RepoGroups []string `json:"repo_groups"`
	CrossRepo  bool     `json:"cross_repo"`
}
