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
// Every result is the daemon's semantic response. The socket client uses an
// operation-specific deadline long enough for stop/restart escalation.
// Returns nil result + error when the daemon is unreachable.
// Returns non-nil result with Success=false when the daemon rejects the command.
type AgentControlFn func(op, agentName string, force bool) (*AgentControlResult, error)

// stopRequest is the optional JSON body for the stop endpoint.
type stopRequest struct {
	Force bool `json:"force"`
}

// AgentInputFn sends a pending-input control command to the daemon.
// op: "agent_input_get" (agentName may be empty for "all") or
// "agent_input_answer" (args carries the answer body).
// Same transport and failure contract as AgentControlFn.
type AgentInputFn func(op, agentName string, args json.RawMessage) (*AgentControlResult, error)

// PendingInputView mirrors cli/daemon.PendingInput (same JSON wire format;
// duplicated to keep the cli -> webui dependency direction).
type PendingInputView struct {
	RequestID string               `json:"request_id"`
	Agent     string               `json:"agent"`
	Kind      string               `json:"kind"`
	Prompt    string               `json:"prompt"`
	Options   []PendingInputOption `json:"options,omitempty"`
	AskedAt   string               `json:"asked_at"`
}

// PendingInputOption mirrors cli/daemon.PendingInputOption.
type PendingInputOption struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

// answerRequest is the POST body for the answer endpoint.
type answerRequest struct {
	RequestID string `json:"request_id,omitempty"`
	OptionID  string `json:"option_id,omitempty"`
	Text      string `json:"text,omitempty"`
	Decline   bool   `json:"decline,omitempty"`
}
