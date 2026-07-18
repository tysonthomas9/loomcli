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
	// AgentStateBackendUnavailable means the agent is registered but
	// the backend CLI it would invoke is not on PATH. Distinct from
	// "stopped" (manual shutdown) or "failed" (exhausted retries): the
	// daemon reconciler re-checks PATH each tick and auto-transitions
	// back to AgentStateIdle once the binary appears.
	AgentStateBackendUnavailable AgentState = "backend_unavailable"
)

// AgentLiveStatus is a DERIVED, read-only liveness signal carried straight from
// fleet-db's agent response (fleet-db computes it from the session+lease join;
// see its ComputeLiveWork). loom does not derive it — it only passes it through.
// Distinct from State, which is the coarse stored intent.
type AgentLiveStatus string

const (
	// AgentLiveWorking means the agent has a live session (running + fresh lease).
	AgentLiveWorking AgentLiveStatus = "working"
	// AgentLiveIdle means no live session was found.
	AgentLiveIdle AgentLiveStatus = "idle"
)

// Agent is a long-lived assignment of a Role to one or more Repos within
// a Workspace. Distinct from a Worker (fleet-db's per-claim record); an
// Agent persists across many task claims.
//
// Name is the workspace-scoped agent identifier (unique within
// WorkspaceKey, typically used as the tmux session + worktree dir name).
// Empty Repos means "all repos in the workspace".
type Agent struct {
	WorkspaceKey     string   `json:"workspace_key"`
	Name             string   `json:"name"`
	RoleName         string   `json:"role_name"`
	Auto             bool     `json:"auto,omitempty"`
	Backend          string   `json:"backend,omitempty"`
	FallbackBackends []string `json:"fallback_backends,omitempty"`
	Repos            []string `json:"repos,omitempty"`
	RepoGroups       []string `json:"repo_groups,omitempty"`
	CrossRepo        bool     `json:"cross_repo,omitempty"`
	Parent           string   `json:"parent,omitempty"`
	// OrchestratorSessionID was here historically as a cache of the
	// lead-to-orchestration AgentSession join. AgentSession is the
	// single source of truth; use store.OrchestrationSessionIDFor.
	State          AgentState        `json:"state,omitempty"`
	Mode           AgentMode         `json:"mode,omitempty"`
	TaskFilter     string            `json:"task_filter,omitempty"`
	MaxConcurrency int               `json:"max_concurrency,omitempty"`
	BudgetPolicy   string            `json:"budget_policy,omitempty"`
	DesiredState   AgentDesiredState `json:"desired_state,omitempty"`
	Execution      string            `json:"execution,omitempty"` // "" (host) or "sandbox" (run under OpenShell)
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`

	// LiveStatus, ActiveTaskID, and ActivePhase are DERIVED, read-only fields
	// carried from fleet-db's agent response (computed there from the live
	// session+lease join). They are never persisted; ActiveTaskID/ActivePhase
	// are set only when LiveStatus == AgentLiveWorking.
	LiveStatus   AgentLiveStatus `json:"live_status,omitempty"`
	ActiveTaskID string          `json:"active_task_id,omitempty"`
	ActivePhase  string          `json:"active_phase,omitempty"`

	// LastErrorClass is a DERIVED, read-only field carried from fleet-db: the
	// error_class of the agent's most recent terminal session when that run
	// failed, surfaced only while the agent is idle. Lets the UI explain why a
	// stalled agent stopped instead of showing a bare "agent missing".
	LastErrorClass string `json:"last_error_class,omitempty"`
}
