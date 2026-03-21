package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// ParseClaudeHookInput reads raw JSON from a Claude Code hook's stdin and
// returns a normalized HookEvent. Returns (nil, nil) for unrecognized hook
// names — this is not an error, just a hook we don't need to handle.
func ParseClaudeHookInput(hookName string, r io.Reader) (*HookEvent, error) {
	switch hookName {
	case "session-start":
		raw, err := readAndParseHookInput[claudeSessionStartPayload](r)
		if err != nil {
			return nil, err
		}
		return &HookEvent{
			Type:       HookSessionStart,
			SessionID:  raw.SessionID,
			SessionRef: raw.TranscriptPath,
			Model:      raw.Model,
			Backend:    "claude",
			Timestamp:  time.Now(),
		}, nil

	case "user-prompt-submit":
		raw, err := readAndParseHookInput[claudeUserPromptPayload](r)
		if err != nil {
			return nil, err
		}
		return &HookEvent{
			Type:       HookTurnStart,
			SessionID:  raw.SessionID,
			SessionRef: raw.TranscriptPath,
			Prompt:     raw.Prompt,
			Backend:    "claude",
			Timestamp:  time.Now(),
		}, nil

	case "stop":
		raw, err := readAndParseHookInput[claudeSessionInfoPayload](r)
		if err != nil {
			return nil, err
		}
		return &HookEvent{
			Type:       HookTurnEnd,
			SessionID:  raw.SessionID,
			SessionRef: raw.TranscriptPath,
			Backend:    "claude",
			Timestamp:  time.Now(),
		}, nil

	case "session-end":
		raw, err := readAndParseHookInput[claudeSessionInfoPayload](r)
		if err != nil {
			return nil, err
		}
		return &HookEvent{
			Type:       HookSessionEnd,
			SessionID:  raw.SessionID,
			SessionRef: raw.TranscriptPath,
			Backend:    "claude",
			Timestamp:  time.Now(),
		}, nil

	default:
		// Intentionally unhandled: pre-task, post-task, post-todo (subagent/todo lifecycle).
		// Loom only tracks session-level and turn-level events.
		return nil, nil //nolint:nilnil // nil event = unhandled hook, not an error
	}
}

// readAndParseHookInput reads all bytes from r and unmarshals JSON into T.
// Returns a descriptive error on empty input or unmarshal failure.
func readAndParseHookInput[T any](r io.Reader) (*T, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read hook input: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("empty hook input")
	}
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse hook input: %w", err)
	}
	return &result, nil
}
