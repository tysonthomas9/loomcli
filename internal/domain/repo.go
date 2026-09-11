package domain

import "time"

// Repo is a source-code repository within a Workspace. Multiple repos per
// workspace is the normal case (multi-repo workflows).
//
// Name is the workspace-scoped identifier (unique within WorkspaceKey).
// RemoteURL is the canonical clone URL. Local checkout paths live in the
// per-machine state cache, not here — Repo is shared state.
//
// SourceRepoID is the stable identifier server-side code uses for
// filtering issues by repo (Issue.Repo matches SourceRepoID). When
// unset, callers should default it to Name.
type Repo struct {
	WorkspaceKey            string                  `json:"workspace_key"`
	Name                    string                  `json:"name"`
	RemoteURL               string                  `json:"remote_url,omitempty"`
	Remote                  string                  `json:"remote,omitempty"`
	DefaultBranch           string                  `json:"default_branch,omitempty"`
	Groups                  []string                `json:"groups,omitempty"`
	SourceRepoID            string                  `json:"source_repo_id,omitempty"`
	TaskDeliveryRequirement TaskDeliveryRequirement `json:"task_delivery_requirement,omitempty"`
	CreatedAt               time.Time               `json:"created_at"`
	UpdatedAt               time.Time               `json:"updated_at"`
}
