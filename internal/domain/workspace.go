package domain

import "time"

// WorkspaceState is the lifecycle state of a workspace from creation
// through readiness.
type WorkspaceState string

const (
	WorkspaceStateCreating     WorkspaceState = "creating"
	WorkspaceStateCloning      WorkspaceState = "cloning"
	WorkspaceStateInitializing WorkspaceState = "initializing"
	WorkspaceStateReady        WorkspaceState = "ready"
	WorkspaceStateError        WorkspaceState = "error"
)

// Workspace is the top-level container for a multi-repo project.
//
// Key is the immutable fleet-db workspace identifier (uppercase
// alphanumeric+hyphens, 1–32 chars). Name is the human-readable display
// label and may change over a workspace's lifetime.
//
// Local-machine-specific data (filesystem path of checkouts, etc.) does
// NOT live here — see the bootstrap state cache. Workspace is shared
// across users in cloud mode and must stay machine-agnostic.
type Workspace struct {
	Key           string         `json:"key"`
	Name          string         `json:"name"`
	Description   string         `json:"description,omitempty"`
	State         WorkspaceState `json:"state,omitempty"`
	ErrorMessage  string         `json:"error_message,omitempty"`
	DefaultBranch string         `json:"default_branch,omitempty"`
	DesignFormat  string         `json:"design_format,omitempty"`
	// Zero means unset; callers should resolve effective defaults via
	// evals.EffectivePolicy.
	EvalSamplingPercent int       `json:"eval_sampling_percent,omitempty"`
	EvalBatchSize       int       `json:"eval_batch_size,omitempty"`
	EvalLookbackDays    int       `json:"eval_lookback_days,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}
