package cli

import "encoding/json"

// AgentIPCRequest is sent by an agent subprocess to the daemon IPC socket.
type AgentIPCRequest struct {
	Operation string          `json:"operation"`      // "claim", "update", "complete"
	AgentName string          `json:"agent_name"`     // BD_ACTOR identity (required)
	IssueID   string          `json:"issue_id"`       // target issue (required)
	Args      json.RawMessage `json:"args,omitempty"` // operation-specific params
}

// AgentIPCResponse is sent by the daemon back to the agent subprocess.
type AgentIPCResponse struct {
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
	Kind    string          `json:"kind,omitempty"` // backend.ErrorKind for typed error handling
	Data    json.RawMessage `json:"data,omitempty"`
}

// IPC operation name constants.
const (
	IPCOpClaim    = "claim"
	IPCOpUpdate   = "update"
	IPCOpComplete = "complete"
)

// IPCClaimArgs are the optional arguments for the claim operation.
type IPCClaimArgs struct {
	LockTTLSeconds int `json:"lock_ttl_seconds,omitempty"`
}
