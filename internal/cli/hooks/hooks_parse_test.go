package hooks

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseClaudeHookInput_SessionStart(t *testing.T) {
	input := `{"session_id":"ses-abc123","transcript_path":"/tmp/transcript.jsonl","model":"claude-sonnet-4-20250514"}`
	ev, err := ParseClaudeHookInput("session-start", strings.NewReader(input))
	require.NoError(t, err)
	require.NotNil(t, ev)

	assert.Equal(t, HookSessionStart, ev.Type)
	assert.Equal(t, "ses-abc123", ev.SessionID)
	assert.Equal(t, "/tmp/transcript.jsonl", ev.SessionRef)
	assert.Equal(t, "claude-sonnet-4-20250514", ev.Model)
	assert.Equal(t, "claude", ev.Backend)
	assert.False(t, ev.Timestamp.IsZero())
}

func TestParseClaudeHookInput_UserPromptSubmit(t *testing.T) {
	input := `{"session_id":"ses-abc123","transcript_path":"/tmp/transcript.jsonl","prompt":"implement the feature"}`
	ev, err := ParseClaudeHookInput("user-prompt-submit", strings.NewReader(input))
	require.NoError(t, err)
	require.NotNil(t, ev)

	assert.Equal(t, HookTurnStart, ev.Type)
	assert.Equal(t, "ses-abc123", ev.SessionID)
	assert.Equal(t, "/tmp/transcript.jsonl", ev.SessionRef)
	assert.Equal(t, "implement the feature", ev.Prompt)
	assert.Equal(t, "claude", ev.Backend)
	assert.False(t, ev.Timestamp.IsZero())
}

func TestParseClaudeHookInput_Stop(t *testing.T) {
	input := `{"session_id":"ses-abc123","transcript_path":"/tmp/transcript.jsonl"}`
	ev, err := ParseClaudeHookInput("stop", strings.NewReader(input))
	require.NoError(t, err)
	require.NotNil(t, ev)

	assert.Equal(t, HookTurnEnd, ev.Type)
	assert.Equal(t, "ses-abc123", ev.SessionID)
	assert.Equal(t, "/tmp/transcript.jsonl", ev.SessionRef)
	assert.Equal(t, "claude", ev.Backend)
	assert.False(t, ev.Timestamp.IsZero())
}

func TestParseClaudeHookInput_SessionEnd(t *testing.T) {
	input := `{"session_id":"ses-abc123","transcript_path":"/tmp/transcript.jsonl"}`
	ev, err := ParseClaudeHookInput("session-end", strings.NewReader(input))
	require.NoError(t, err)
	require.NotNil(t, ev)

	assert.Equal(t, HookSessionEnd, ev.Type)
	assert.Equal(t, "ses-abc123", ev.SessionID)
	assert.Equal(t, "/tmp/transcript.jsonl", ev.SessionRef)
	assert.Equal(t, "claude", ev.Backend)
	assert.False(t, ev.Timestamp.IsZero())
}

func TestParseClaudeHookInput_UnknownHook(t *testing.T) {
	input := `{"session_id":"ses-abc123"}`
	ev, err := ParseClaudeHookInput("some-future-hook", strings.NewReader(input))
	assert.NoError(t, err)
	assert.Nil(t, ev)
}

func TestParseClaudeHookInput_EmptyInput(t *testing.T) {
	hooks := []string{"session-start", "user-prompt-submit", "stop", "session-end"}
	for _, hook := range hooks {
		t.Run(hook, func(t *testing.T) {
			ev, err := ParseClaudeHookInput(hook, strings.NewReader(""))
			assert.Error(t, err)
			assert.Nil(t, ev)
			assert.Contains(t, err.Error(), "empty hook input")
		})
	}
}

func TestParseClaudeHookInput_InvalidJSON(t *testing.T) {
	hooks := []string{"session-start", "user-prompt-submit", "stop", "session-end"}
	for _, hook := range hooks {
		t.Run(hook, func(t *testing.T) {
			ev, err := ParseClaudeHookInput(hook, strings.NewReader("{not valid json"))
			assert.Error(t, err)
			assert.Nil(t, ev)
			assert.Contains(t, err.Error(), "failed to parse hook input")
		})
	}
}

func TestParseClaudeHookInput_MissingOptionalFields(t *testing.T) {
	// session-start without model field should still work
	input := `{"session_id":"ses-abc123","transcript_path":"/tmp/transcript.jsonl"}`
	ev, err := ParseClaudeHookInput("session-start", strings.NewReader(input))
	require.NoError(t, err)
	require.NotNil(t, ev)

	assert.Equal(t, HookSessionStart, ev.Type)
	assert.Equal(t, "ses-abc123", ev.SessionID)
	assert.Equal(t, "/tmp/transcript.jsonl", ev.SessionRef)
	assert.Empty(t, ev.Model)
	assert.Equal(t, "claude", ev.Backend)
}

func TestParseClaudeHookInput_Backend(t *testing.T) {
	tests := []struct {
		name     string
		hookName string
		input    string
	}{
		{
			name:     "session-start",
			hookName: "session-start",
			input:    `{"session_id":"s1","transcript_path":"/t","model":"m"}`,
		},
		{
			name:     "user-prompt-submit",
			hookName: "user-prompt-submit",
			input:    `{"session_id":"s1","transcript_path":"/t","prompt":"p"}`,
		},
		{
			name:     "stop",
			hookName: "stop",
			input:    `{"session_id":"s1","transcript_path":"/t"}`,
		},
		{
			name:     "session-end",
			hookName: "session-end",
			input:    `{"session_id":"s1","transcript_path":"/t"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := ParseClaudeHookInput(tt.hookName, strings.NewReader(tt.input))
			require.NoError(t, err)
			require.NotNil(t, ev)
			assert.Equal(t, "claude", ev.Backend, "all Claude hook events must have Backend=\"claude\"")
		})
	}
}
