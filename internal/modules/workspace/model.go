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
