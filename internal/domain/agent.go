package domain

import "time"

// AgentState reports whether an agent assignment is currently running,
// stopped, or idle (registered but not active). This is loom's view of
// the assignment, distinct from fleet-db's per-claim Worker state.
type AgentState string

const (
	AgentStateIdle    AgentState = "idle"
	AgentStateActive  AgentState = "active"
	AgentStateStopped AgentState = "stopped"
)

// Agent is a long-lived assignment of a Role to one or more Repos within
// a Workspace. Distinct from a Worker (fleet-db's per-claim record); an
// Agent persists across many task claims.
//
// Name is the workspace-scoped agent identifier (unique within
// WorkspaceKey, typically used as the tmux session + worktree dir name).
// Empty Repos means "all repos in the workspace".
type Agent struct {
	WorkspaceKey     string     `json:"workspace_key"`
	Name             string     `json:"name"`
	RoleName         string     `json:"role_name"`
	Auto             bool       `json:"auto,omitempty"`
	Backend          string     `json:"backend,omitempty"`
	FallbackBackends []string   `json:"fallback_backends,omitempty"`
	Repos            []string   `json:"repos,omitempty"`
	RepoGroups       []string   `json:"repo_groups,omitempty"`
	CrossRepo        bool       `json:"cross_repo,omitempty"`
	Parent           string     `json:"parent,omitempty"`
	State            AgentState `json:"state,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
