package hooks

import (
	"encoding/json"
	"time"
)

// HookEventType represents a normalized lifecycle event from any agent backend.
// Each backend maps its native hook names to these types.
type HookEventType int

const (
	// HookSessionStart indicates the agent session has begun.
	HookSessionStart HookEventType = iota + 1

	// HookTurnStart indicates the user submitted a prompt and the agent is about to work.
	HookTurnStart

	// HookTurnEnd indicates the agent finished responding to a prompt.
	HookTurnEnd

	// HookSessionEnd indicates the session has been terminated.
	HookSessionEnd

	// HookSubagentStart indicates a subagent (Task tool) is about to start.
	HookSubagentStart

	// HookSubagentEnd indicates a subagent (Task tool) has completed.
	HookSubagentEnd
)

// String returns a human-readable name for the event type.
func (e HookEventType) String() string {
	switch e {
	case HookSessionStart:
		return "SessionStart"
	case HookTurnStart:
		return "TurnStart"
	case HookTurnEnd:
		return "TurnEnd"
	case HookSessionEnd:
		return "SessionEnd"
	case HookSubagentStart:
		return "SubagentStart"
	case HookSubagentEnd:
		return "SubagentEnd"
	default:
		return "Unknown"
	}
}

// HookEvent is a backend-agnostic lifecycle event produced by parsing
// raw hook input from an agent CLI (e.g., Claude Code).
type HookEvent struct {
	// Type is the kind of lifecycle event.
	Type HookEventType

	// SessionID is the agent CLI's own session identifier (from the hook payload).
	SessionID string

	// SessionRef is an opaque backend reference to the agent's transcript
	// (typically a file path). Named to avoid confusion with loom's own
	// session transcript path.
	SessionRef string

	// Prompt is the user prompt text. Populated on TurnStart only.
	Prompt string

	// Model is the LLM model identifier (e.g., "claude-sonnet-4-20250514").
	// Populated on SessionStart only.
	Model string

	// Backend identifies which agent backend produced this event (e.g., "claude").
	Backend string

	// ToolUseID is the unique ID for a tool invocation (subagent events only).
	ToolUseID string

	// ToolInput is the raw JSON input to the Task tool (subagent events only).
	ToolInput json.RawMessage

	// SubagentID is the spawned subagent's identifier (SubagentEnd only, from tool_response).
	SubagentID string

	// Timestamp is when the event occurred.
	Timestamp time.Time
}

// Raw Claude Code stdin JSON structs.
// These mirror the JSON objects that Claude Code writes to hook stdin.

// claudeSessionStartPayload is an alias for the shared session info payload.
// Kept as a separate type for documentation clarity: SessionStart always
// includes the model field, while Stop/SessionEnd have it as optional.
type claudeSessionStartPayload = claudeSessionInfoPayload

type claudeUserPromptPayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Prompt         string `json:"prompt"`
}

// claudeSessionInfoPayload is used for SessionStart, Stop, and SessionEnd hooks.
// All three share the same JSON shape from Claude Code (model is optional).
type claudeSessionInfoPayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Model          string `json:"model,omitempty"`
}

// claudePreToolUsePayload is the JSON from PreToolUse[Task] hooks.
type claudePreToolUsePayload struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	ToolUseID      string          `json:"tool_use_id"`
	ToolInput      json.RawMessage `json:"tool_input"`
}

// claudePostToolUsePayload is the JSON from PostToolUse[Task] hooks.
type claudePostToolUsePayload struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	ToolUseID      string          `json:"tool_use_id"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   struct {
		AgentID string `json:"agentId"`
	} `json:"tool_response"`
}
