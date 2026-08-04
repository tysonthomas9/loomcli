package agentcontrol

import "encoding/json"

// AgentControlResult is the webui-local mirror of cli/daemon.DaemonControlResponse.
// Same JSON wire format, different Go package — intentional duplication to maintain
// the dependency direction (cli -> webui, not reverse).
type AgentControlResult struct {
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// AgentControlEntry mirrors cli/daemon.AgentListEntry.
type AgentControlEntry struct {
	Name   string `json:"name"`
	Role   string `json:"role"`
	Status string `json:"status"`
}

// AgentControlFn sends a control command to the daemon via the control socket.
// op: "agent_stop", "agent_start", "agent_restart", "agent_yield", "agent_list"
// agentName: target agent worktree name (empty for "agent_list")
// force: only applies to "agent_stop"
// Returns nil result + error when the daemon is unreachable.
// Returns non-nil result with Success=false when the daemon rejects the command.
type AgentControlFn func(op, agentName string, force bool) (*AgentControlResult, error)

// stopRequest is the optional JSON body for the stop endpoint.
type stopRequest struct {
	Force bool `json:"force"`
}
