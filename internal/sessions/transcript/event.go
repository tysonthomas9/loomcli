package transcript

import (
	"encoding/json"
	"time"
)

// Event is the canonical, backend-agnostic representation of a single
// moment in an agent session's transcript. Per-backend adapters
// (claude/, opencode/, codex/) translate their native formats into []Event
// so the API + UI only need to handle one shape.
type Event struct {
	Seq       int             `json:"seq"`
	Timestamp time.Time       `json:"timestamp"`
	Role      string          `json:"role"` // user, assistant, tool, system
	Type      string          `json:"type"` // text, tool_use, tool_result, session_start, session_end
	Text      string          `json:"text,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	Output    string          `json:"output,omitempty"` // tool_result text
	UUID      string          `json:"uuid,omitempty"`   // native message UUID when available
}

// Canonical role constants.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	RoleSystem    = "system"
)

// Canonical event type constants.
const (
	EventText        = "text"
	EventToolUse     = "tool_use"
	EventToolResult  = "tool_result"
	EventSessionMeta = "session_meta"
)
