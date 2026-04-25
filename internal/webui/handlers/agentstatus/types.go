// Package agentstatus implements the workspace-scoped agent status endpoint
// (GET /api/workspaces/{ws}/agents/status). It merges daemon supervision state
// (daemon-agents.json) with live git + lock data into a single response shaped
// for the frontend LoomAgentStatus type.
package agentstatus

import "time"

// yieldInfo mirrors supervisor.YieldRequest so the handler does not import
// cli/daemon/supervisor (preserving the webui → cli one-way import direction).
type yieldInfo struct {
	Reason      string    `json:"reason"`
	RequestedAt time.Time `json:"requested_at"`
	RequestedBy string    `json:"requested_by"`
}

// AgentStatusEntry is the wire shape for a single agent in the response. JSON
// names match the OpenAPI schema. Daemon-sourced fields come from
// daemon-agents.json; git/lock fields come from AgentStatusCollectFn; yield
// fields come from .agent.yield in the worktree.
type AgentStatusEntry struct {
	Worktree         string    `json:"worktree"`
	WorktreePath     string    `json:"worktree_path"`
	Path             string    `json:"path"`
	Role             string    `json:"role,omitempty"`
	Repo             string    `json:"repo,omitempty"`
	Workspace        string    `json:"workspace"`
	CrossRepo        bool      `json:"cross_repo"`
	PID              int       `json:"pid"`
	Status           string    `json:"status"`
	SupervisorStatus string    `json:"supervisor_status"`
	RestartCount     int       `json:"restart_count"`
	LastErrorClass   string    `json:"last_error_class,omitempty"`
	BackoffUntil     time.Time `json:"backoff_until,omitempty"`
	StopReason       string    `json:"stop_reason,omitempty"`
	TaskID           string    `json:"task_id,omitempty"`
	EpicID           string    `json:"epic_id,omitempty"`
	CurrentBackend   string    `json:"current_backend,omitempty"`
	Branch           string    `json:"branch"`
	Ahead            int       `json:"ahead"`
	Behind           int       `json:"behind"`
	Changes          int       `json:"changes"`
	RemoteBranch     string    `json:"remote_branch,omitempty"`
	YieldRequested   bool      `json:"yield_requested"`
	YieldReason      string    `json:"yield_reason,omitempty"`
	YieldRequestedAt time.Time `json:"yield_requested_at,omitempty"`
	Error            string    `json:"error,omitempty"`
}

// AgentStatusResponse is the top-level data payload of the success envelope.
type AgentStatusResponse struct {
	Agents          []AgentStatusEntry `json:"agents"`
	IPCSocketActive bool               `json:"ipc_socket_active"`
	DaemonPID       int                `json:"daemon_pid"`
	DaemonStartedAt time.Time          `json:"daemon_started_at"`
	WorkspaceName   string             `json:"workspace_name"`
	Timestamp       time.Time          `json:"timestamp"`
}
