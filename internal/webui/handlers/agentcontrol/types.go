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

// ClaimHoldFn sends a claim-hold control command to the daemon.
// op: "claims_hold_get" (args nil) or "claims_hold_set" (args carries the
// held/actor/reason/ttl/force body). Same transport and failure contract as
// AgentControlFn: nil result + error when the socket is unreachable.
type ClaimHoldFn func(op string, args json.RawMessage) (*AgentControlResult, error)

// ClaimHoldView mirrors supervisor.ClaimHold (same JSON wire format;
// duplicated to keep the cli -> webui dependency direction).
type ClaimHoldView struct {
	Held      bool   `json:"held"`
	Actor     string `json:"actor"`
	Reason    string `json:"reason"`
	Since     string `json:"since"`
	ExpiresAt string `json:"expires_at,omitempty"`
	// Repos is the hold's repo scope. Empty means workspace-wide, which is what
	// every hold taken before the field existed means.
	Repos []string `json:"repos,omitempty"`
}

// ClaimHoldRunningView mirrors cli/daemon.ClaimHoldRunningAgent — an agent
// whose run was already in flight when the hold went up. A hold never touches
// a running agent; these are reported so an operator can see what a quiesce is
// still waiting on.
type ClaimHoldRunningView struct {
	Agent     string `json:"agent"`
	TaskID    string `json:"task_id,omitempty"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at,omitempty"`
}

// ClaimHoldStatusView mirrors cli/daemon.ClaimHoldStatus and is the response
// body of all three claim-hold routes. Hold is nil when claims are free.
type ClaimHoldStatusView struct {
	Hold    *ClaimHoldView         `json:"hold"`
	Running []ClaimHoldRunningView `json:"running"`
	Gated   int                    `json:"gated"`
}

// claimHoldSetRequest is the POST body for taking a hold.
type claimHoldSetRequest struct {
	Reason     string   `json:"reason"`
	TTLSeconds int64    `json:"ttl_seconds,omitempty"`
	Actor      string   `json:"actor,omitempty"`
	Force      bool     `json:"force,omitempty"`
	Repos      []string `json:"repos,omitempty"` // empty = every repo
}

// claimHoldReleaseRequest is the optional DELETE body.
type claimHoldReleaseRequest struct {
	Actor string `json:"actor,omitempty"`
	Force bool   `json:"force,omitempty"`
}

// claimHoldSetArgs is the wire args of the claims_hold_set operation. It is
// the webui-local mirror of cli/daemon.claimHoldSetArgs.
type claimHoldSetArgs struct {
	Held       bool     `json:"held"`
	Actor      string   `json:"actor"`
	Reason     string   `json:"reason,omitempty"`
	TTLSeconds int64    `json:"ttl_seconds,omitempty"`
	Force      bool     `json:"force,omitempty"`
	Repos      []string `json:"repos,omitempty"`
}
