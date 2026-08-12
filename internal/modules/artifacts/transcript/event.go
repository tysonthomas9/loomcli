// Package transcript defines Artifacts' canonical durable transcript format.
package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// MaxCanonicalEvents is the shared producer/consumer bound for a durable
// canonical transcript. Producers must reserve one event for truncation
// evidence when the source exceeds this limit.
const MaxCanonicalEvents = 100_000

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

// Canonical event type constants. text/tool_use/tool_result/session_meta are the
// original core set; reasoning (assistant chain-of-thought) and result (terminal
// status + token-usage summary) are emitted by the TS local-task-runner leaf and
// are blessed here so both execution planes share ONE event vocabulary (Phase U/U0).
const (
	EventText        = "text"
	EventReasoning   = "reasoning"
	EventToolUse     = "tool_use"
	EventToolResult  = "tool_result"
	EventResult      = "result"
	EventSessionMeta = "session_meta"
)

// KnownEventTypes / KnownRoles are the canonical vocabularies every producer — the
// Go backend adapters AND the TS local-task-runner leaf — must stay within. The
// Phase-U conformance test pins TS-leaf output to these sets so the two execution
// planes cannot silently drift apart on transcript schema.
var KnownEventTypes = map[string]bool{
	EventText:        true,
	EventReasoning:   true,
	EventToolUse:     true,
	EventToolResult:  true,
	EventResult:      true,
	EventSessionMeta: true,
}

var KnownRoles = map[string]bool{
	RoleUser:      true,
	RoleAssistant: true,
	RoleTool:      true,
	RoleSystem:    true,
}

// ValidateCanonicalEvent rejects events that cannot satisfy the canonical
// transcript wire contract shared by local and durable transcript readers.
func ValidateCanonicalEvent(event Event) error {
	if !KnownRoles[event.Role] {
		return fmt.Errorf("transcript event has unknown role %q", event.Role)
	}
	if !KnownEventTypes[event.Type] {
		return fmt.Errorf("transcript event has unknown type %q", event.Type)
	}
	if event.Timestamp.IsZero() {
		return errors.New("transcript event timestamp is required")
	}
	return nil
}
