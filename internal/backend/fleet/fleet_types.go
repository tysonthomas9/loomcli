package fleet

import (
	"encoding/json"
	"time"

	"github.com/tysonthomas9/loomcli/internal/types"
)

// APIResponse is the standard fleet server response envelope.
// All fleet server endpoints return this structure.
type APIResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// ClaimResult is the parsed response from the fleet claim endpoint.
// It wraps a WorkHandoffPayload containing the claimed issue, its labels,
// and dependencies.
type ClaimResult struct {
	Payload *types.WorkHandoffPayload `json:"payload"`
}

// RegisterResult is the parsed response from the fleet register endpoint.
type RegisterResult struct {
	Token string `json:"token"`
}

// DoneResult is the parsed response from the fleet done endpoint.
type DoneResult struct {
	TaskID   string `json:"task_id"`
	WorkerID string `json:"worker_id"`
}

// HeartbeatResult is the parsed response from the fleet heartbeat endpoint.
type HeartbeatResult struct {
	LastHeartbeat time.Time `json:"last_heartbeat"`
}
