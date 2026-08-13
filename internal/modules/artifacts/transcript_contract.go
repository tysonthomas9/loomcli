package artifacts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const MaxCanonicalEvents = 100_000

// Event is Artifacts' canonical backend-agnostic durable transcript event.
type Event struct {
	Seq       int             `json:"seq"`
	Timestamp time.Time       `json:"timestamp"`
	Role      string          `json:"role"`
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	Output    string          `json:"output,omitempty"`
	UUID      string          `json:"uuid,omitempty"`
}

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	RoleSystem    = "system"

	EventText        = "text"
	EventReasoning   = "reasoning"
	EventToolUse     = "tool_use"
	EventToolResult  = "tool_result"
	EventResult      = "result"
	EventSessionMeta = "session_meta"
)

var (
	ErrInvalidTranscriptJSONL  = errors.New("invalid canonical transcript JSONL")
	ErrTranscriptTooLarge      = errors.New("canonical transcript exceeds byte limit")
	ErrTooManyTranscriptEvents = errors.New("canonical transcript exceeds event limit")

	knownTranscriptEventTypes = map[string]bool{
		EventText: true, EventReasoning: true, EventToolUse: true,
		EventToolResult: true, EventResult: true, EventSessionMeta: true,
	}
	knownTranscriptRoles = map[string]bool{
		RoleUser: true, RoleAssistant: true, RoleTool: true, RoleSystem: true,
	}
)

func ValidateCanonicalEvent(event Event) error {
	if !knownTranscriptRoles[event.Role] {
		return fmt.Errorf("transcript event has unknown role %q", event.Role)
	}
	if !knownTranscriptEventTypes[event.Type] {
		return fmt.Errorf("transcript event has unknown type %q", event.Type)
	}
	if event.Timestamp.IsZero() {
		return errors.New("transcript event timestamp is required")
	}
	return nil
}

func IsCanonicalTranscriptRole(value string) bool { return knownTranscriptRoles[value] }

func IsCanonicalTranscriptEventType(value string) bool { return knownTranscriptEventTypes[value] }

// DecodeCanonicalJSONL validates the exact bounded durable transcript
// representation. It rejects blank lines, malformed vocabulary, gaps in
// sequence numbers, and a missing final newline.
func DecodeCanonicalJSONL(content []byte, maxBytes, maxEvents int) ([]Event, bool, error) {
	if maxBytes <= 0 || maxEvents <= 0 {
		return nil, false, fmt.Errorf("positive transcript limits are required: %w", ErrInvalidTranscriptJSONL)
	}
	if len(content) > maxBytes {
		return nil, false, ErrTranscriptTooLarge
	}
	if len(content) == 0 || content[len(content)-1] != '\n' {
		return nil, false, fmt.Errorf("canonical transcript must end with a newline: %w", ErrInvalidTranscriptJSONL)
	}
	lines := bytes.Split(content[:len(content)-1], []byte{'\n'})
	events := make([]Event, 0, min(len(lines), maxEvents))
	for index, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			return nil, false, fmt.Errorf("canonical transcript line %d is blank: %w", index+1, ErrInvalidTranscriptJSONL)
		}
		if index >= maxEvents {
			return events, true, nil
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, false, fmt.Errorf("decode canonical transcript line %d: %w", index+1, errors.Join(ErrInvalidTranscriptJSONL, err))
		}
		if err := ValidateCanonicalEvent(event); err != nil {
			return nil, false, fmt.Errorf("validate canonical transcript line %d: %w", index+1, errors.Join(ErrInvalidTranscriptJSONL, err))
		}
		if event.Seq != index+1 {
			return nil, false, fmt.Errorf("canonical transcript line %d has sequence %d: %w", index+1, event.Seq, ErrInvalidTranscriptJSONL)
		}
		events = append(events, event)
	}
	return events, false, nil
}

type TranscriptEvent = Event

const (
	MaxTranscriptEvents  = MaxCanonicalEvents
	TranscriptRoleUser   = RoleUser
	TranscriptRoleAgent  = RoleAssistant
	TranscriptRoleTool   = RoleTool
	TranscriptRoleSystem = RoleSystem
	TranscriptEventText  = EventText
)

func DecodeCanonicalTranscript(content []byte, maxBytes, maxEvents int) ([]TranscriptEvent, bool, error) {
	return DecodeCanonicalJSONL(content, maxBytes, maxEvents)
}
