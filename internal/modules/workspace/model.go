package workspace

import "time"

const (
	DesignFormatMarkdown = "markdown"
	DesignFormatHTML     = "html"
)

// State is the Workspace-owned lifecycle from durable admission through
// readiness. Local checkout/materialization state belongs to Source Control.
type State string

const (
	StateCreating     State = "creating"
	StateCloning      State = "cloning"
	StateInitializing State = "initializing"
	StateReady        State = "ready"
	StateError        State = "error"
)

// Workspace is the canonical machine-agnostic aggregate. Machine-local paths,
// worktrees, and Git state are deliberately excluded.
type Workspace struct {
	Key           string    `json:"key"`
	Name          string    `json:"name"`
	Description   string    `json:"description,omitempty"`
	State         State     `json:"state,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	DesignFormat  string    `json:"design_format,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Reference is the immutable Workspace catalog projection used by
// cross-capability coordinators.
type Reference = Workspace

// Repository is Workspace-owned shared catalog state. Checkout paths,
// worktrees, and current branches are machine-local Source Control state.
type Repository struct {
	WorkspaceKey  string    `json:"workspace_key"`
	Name          string    `json:"name"`
	RemoteURL     string    `json:"remote_url,omitempty"`
	Remote        string    `json:"remote,omitempty"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	Groups        []string  `json:"groups,omitempty"`
	SourceRepoID  string    `json:"source_repo_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
