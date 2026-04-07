package hooks

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
		return parseSessionStart(r)
	case "user-prompt-submit":
		return parseUserPromptSubmit(r)
	case "stop":
		return parseStop(r)
	case "session-end":
		return parseSessionEnd(r)
	case "pre-task":
		return parsePreTask(r)
	case "post-task":
		return parsePostTask(r)
	default:
		return nil, nil //nolint:nilnil // nil event = unhandled hook, not an error
	}
}

func parseSessionStart(r io.Reader) (*HookEvent, error) {
	raw, err := readAndParseHookInput[claudeSessionStartPayload](r)
	if err != nil {
		return nil, err
	}
	return &HookEvent{
		Type: HookSessionStart, SessionID: raw.SessionID, SessionRef: raw.TranscriptPath,
		Model: raw.Model, Backend: "claude", Timestamp: time.Now(),
	}, nil
}

func parseUserPromptSubmit(r io.Reader) (*HookEvent, error) {
	raw, err := readAndParseHookInput[claudeUserPromptPayload](r)
	if err != nil {
		return nil, err
	}
	return &HookEvent{
		Type: HookTurnStart, SessionID: raw.SessionID, SessionRef: raw.TranscriptPath,
		Prompt: raw.Prompt, Backend: "claude", Timestamp: time.Now(),
	}, nil
}

func parseStop(r io.Reader) (*HookEvent, error) {
	raw, err := readAndParseHookInput[claudeSessionInfoPayload](r)
	if err != nil {
		return nil, err
	}
	return &HookEvent{
		Type: HookTurnEnd, SessionID: raw.SessionID, SessionRef: raw.TranscriptPath,
		Backend: "claude", Timestamp: time.Now(),
	}, nil
}

func parseSessionEnd(r io.Reader) (*HookEvent, error) {
	raw, err := readAndParseHookInput[claudeSessionInfoPayload](r)
	if err != nil {
		return nil, err
	}
	return &HookEvent{
		Type: HookSessionEnd, SessionID: raw.SessionID, SessionRef: raw.TranscriptPath,
		Backend: "claude", Timestamp: time.Now(),
	}, nil
}

func parsePreTask(r io.Reader) (*HookEvent, error) {
	raw, err := readAndParseHookInput[claudePreToolUsePayload](r)
	if err != nil {
		return nil, err
	}
	return &HookEvent{
		Type: HookSubagentStart, SessionID: raw.SessionID, SessionRef: raw.TranscriptPath,
		ToolUseID: raw.ToolUseID, ToolInput: raw.ToolInput, Backend: "claude", Timestamp: time.Now(),
	}, nil
}

func parsePostTask(r io.Reader) (*HookEvent, error) {
	raw, err := readAndParseHookInput[claudePostToolUsePayload](r)
	if err != nil {
		return nil, err
	}
	event := &HookEvent{
		Type: HookSubagentEnd, SessionID: raw.SessionID, SessionRef: raw.TranscriptPath,
		ToolUseID: raw.ToolUseID, ToolInput: raw.ToolInput, Backend: "claude", Timestamp: time.Now(),
	}
	if raw.ToolResponse.AgentID != "" {
		event.SubagentID = raw.ToolResponse.AgentID
	}
	return event, nil
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
