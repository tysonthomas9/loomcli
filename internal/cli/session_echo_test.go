//go:build testbackend

package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// TestSessionCapture_FullLifecycle validates the complete session audit trail:
// CreateSession -> EchoBackend invoke (with UsageHandler) -> Finalize ->
// verify index.jsonl, metadata.json, prompt.txt.
func TestSessionCapture_FullLifecycle(t *testing.T) {
	beadsDir := t.TempDir()
	store, err := sessions.NewStore(beadsDir)
	require.NoError(t, err)

	// Create session
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName:  "test-agent",
		Backend:    "echo",
		Prompt:     "implement feature X",
		AttemptNum: 1,
	})
	require.NoError(t, err)
	require.NotEmpty(t, sess.SessionID())

	// Set up EchoTestEnv with UsageHandler
	env := NewEchoTestEnv(t)
	env.Backend.SetHandler(UsageHandler(5000, 2000))

	// Set session ID env var for hook dispatch context
	t.Setenv("LOOM_SESSION_ID", sess.SessionID())

	// Run backend
	err = env.RunNonInteractive("implement feature X")
	require.NoError(t, err)
	env.AssertInvoked(1)

	// Finalize the session using token counts from the collector
	su := env.Collector.Finalize("task-lifecycle", "", sess.Meta.StartedAt, time.Now().UTC(), 0)

	err = sess.Finalize(sessions.FinalizeOptions{
		TaskID:       "task-lifecycle",
		ExitCode:     0,
		InputTokens:  su.InputTokens,
		OutputTokens: su.OutputTokens,
	})
	require.NoError(t, err)

	// Verify index.jsonl: query by task_id returns 1 record
	records, err := store.SessionsByTask("task-lifecycle")
	require.NoError(t, err)
	require.Len(t, records, 1)

	rec := records[0]
	assert.Equal(t, sess.SessionID(), rec.SessionID)
	assert.Equal(t, "task-lifecycle", rec.TaskID)
	assert.Equal(t, 0, rec.ExitCode)
	assert.Equal(t, sessions.StatusCompleted, rec.Status)
	assert.Equal(t, int64(5000), rec.InputTokens)
	assert.Equal(t, int64(2000), rec.OutputTokens)

	// Verify metadata.json
	meta, err := store.LoadMetadata(sess.SessionID())
	require.NoError(t, err)
	assert.Equal(t, sess.SessionID(), meta.SessionID)
	assert.Equal(t, "task-lifecycle", meta.TaskID)
	assert.NotNil(t, meta.EndedAt)
	assert.Greater(t, meta.DurationS, float64(0))

	// Verify prompt.txt
	prompt, err := store.ReadPrompt(sess.SessionID())
	require.NoError(t, err)
	assert.Equal(t, "implement feature X", prompt)
}

// TestSessionCapture_HookAppendTranscript verifies that dispatchHookEvent
// correctly appends transcript entries and that LoadTranscript returns them.
func TestSessionCapture_HookAppendTranscript(t *testing.T) {
	beadsDir := t.TempDir()
	store, err := sessions.NewStore(beadsDir)
	require.NoError(t, err)

	// Create a session so the directory exists
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName:  "test-agent",
		Backend:    "echo",
		Prompt:     "test prompt",
		AttemptNum: 1,
	})
	require.NoError(t, err)

	// Dispatch several hook events directly.
	// dispatchHookEvent takes beadsDir (not the sessions/ subdir) and sessionID.
	events := []*HookEvent{
		{Type: HookSessionStart, Model: "claude-sonnet-4-5-20250514"},
		{Type: HookTurnStart, Prompt: "user question"},
		{Type: HookTurnEnd},
		{Type: HookSessionEnd},
	}

	for _, event := range events {
		err := dispatchHookEvent(event, beadsDir, sess.SessionID())
		require.NoError(t, err)
	}

	// Load transcript and verify
	entries, err := store.LoadTranscript(sess.SessionID())
	require.NoError(t, err)
	require.Len(t, entries, 4)

	// Entry 0: session_start
	assert.Equal(t, "system", entries[0].Role)
	assert.Equal(t, "session_start", entries[0].Type)
	assert.Contains(t, entries[0].Content, "claude-sonnet-4-5-20250514")

	// Entry 1: user prompt
	assert.Equal(t, "user", entries[1].Role)
	assert.Equal(t, "text", entries[1].Type)
	assert.Equal(t, "user question", entries[1].Content)

	// Entry 2: turn_end
	assert.Equal(t, "assistant", entries[2].Role)
	assert.Equal(t, "turn_end", entries[2].Type)

	// Entry 3: session_end
	assert.Equal(t, "system", entries[3].Role)
	assert.Equal(t, "session_end", entries[3].Type)
}

// TestSessionCapture_MultipleSessionsPerTask creates 3 sessions for the same
// task (simulating retries) and verifies SessionsByTask returns all 3 sorted
// by time.
func TestSessionCapture_MultipleSessionsPerTask(t *testing.T) {
	beadsDir := t.TempDir()
	store, err := sessions.NewStore(beadsDir)
	require.NoError(t, err)

	for i := 1; i <= 3; i++ {
		sess, err := store.CreateSession(sessions.CreateOptions{
			AgentName:  "test-agent",
			Backend:    "echo",
			Prompt:     "retry prompt",
			AttemptNum: i,
		})
		require.NoError(t, err)

		err = sess.Finalize(sessions.FinalizeOptions{
			TaskID:   "task-retry",
			ExitCode: 0,
		})
		require.NoError(t, err)

		// Small delay to ensure different timestamps
		time.Sleep(10 * time.Millisecond)
	}

	// Query sessions by task
	records, err := store.SessionsByTask("task-retry")
	require.NoError(t, err)
	require.Len(t, records, 3)

	// All should have the same task ID and completed status
	for _, rec := range records {
		assert.Equal(t, "task-retry", rec.TaskID)
		assert.Equal(t, sessions.StatusCompleted, rec.Status)
	}

	// Records should be sorted by StartedAt ascending
	for i := 1; i < len(records); i++ {
		assert.True(t,
			!records[i].StartedAt.Before(records[i-1].StartedAt),
			"records should be sorted by StartedAt ascending",
		)
	}

	// Attempt numbers should be 1, 2, 3
	assert.Equal(t, 1, records[0].AttemptNum)
	assert.Equal(t, 2, records[1].AttemptNum)
	assert.Equal(t, 3, records[2].AttemptNum)
}

// TestSessionCapture_FailedSession verifies that a backend error results in
// status=failed in both metadata.json and index.jsonl.
func TestSessionCapture_FailedSession(t *testing.T) {
	beadsDir := t.TempDir()
	store, err := sessions.NewStore(beadsDir)
	require.NoError(t, err)

	// Create session
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName:  "test-agent",
		Backend:    "echo",
		Prompt:     "failing task",
		AttemptNum: 1,
	})
	require.NoError(t, err)

	// Set up EchoTestEnv with ErrorHandler
	env := NewEchoTestEnv(t)
	env.Backend.SetHandler(ErrorHandler(errors.New("lint failure")))

	// Run backend - expect error return
	err = env.RunNonInteractive("failing task")
	assert.Error(t, err)
	env.AssertInvoked(1)

	// Finalize with exit code 1
	err = sess.Finalize(sessions.FinalizeOptions{
		TaskID:     "task-failed",
		ExitCode:   1,
		ErrorClass: "lint_failure",
	})
	require.NoError(t, err)

	// Verify metadata.json
	meta, err := store.LoadMetadata(sess.SessionID())
	require.NoError(t, err)
	assert.Equal(t, sessions.StatusFailed, meta.Status)
	assert.Equal(t, 1, meta.ExitCode)
	assert.Equal(t, "lint_failure", meta.ErrorClass)

	// Verify index.jsonl
	records, err := store.SessionsByTask("task-failed")
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, sessions.StatusFailed, records[0].Status)
	assert.Equal(t, 1, records[0].ExitCode)
}

// sessionListResponse mirrors webui.SessionListResponse for test assertions
// without importing the webui package directly (handleListTaskSessions is unexported).
type sessionListResponse struct {
	Success bool             `json:"success"`
	Data    *sessionListData `json:"data,omitempty"`
	Error   string           `json:"error,omitempty"`
}

type sessionListData struct {
	TaskID   string            `json:"task_id"`
	Sessions []sessionListItem `json:"sessions"`
}

type sessionListItem struct {
	sessions.SessionRecord
	IsActive      bool `json:"is_active"`
	HasTranscript bool `json:"has_transcript"`
	HasDiff       bool `json:"has_diff"`
}

// TestSessionCapture_ApiQuery creates sessions via the store, then builds
// an HTTP handler that mimics handleListTaskSessions and verifies the API
// response shape using httptest.
//
// This test uses a minimal inline handler rather than importing the unexported
// webui.handleListTaskSessions. The response shape matches the SessionListResponse
// contract from handlers_sessions.go.
func TestSessionCapture_ApiQuery(t *testing.T) {
	beadsDir := t.TempDir()
	store, err := sessions.NewStore(beadsDir)
	require.NoError(t, err)

	// Create and finalize 2 sessions for "task-api"
	for i := 1; i <= 2; i++ {
		sess, err := store.CreateSession(sessions.CreateOptions{
			AgentName:  "test-agent",
			Backend:    "echo",
			Prompt:     "api test prompt",
			AttemptNum: i,
		})
		require.NoError(t, err)

		err = sess.Finalize(sessions.FinalizeOptions{
			TaskID:   "task-api",
			ExitCode: 0,
		})
		require.NoError(t, err)
	}

	// Build a handler that mirrors webui.handleListTaskSessions
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("taskId")
		if taskID == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(sessionListResponse{Error: "missing task ID"})
			return
		}

		records, err := store.SessionsByTask(taskID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(sessionListResponse{Error: "failed to list sessions"})
			return
		}

		items := make([]sessionListItem, 0, len(records))
		for _, rec := range records {
			items = append(items, sessionListItem{
				SessionRecord: rec,
				IsActive:      rec.Status == sessions.StatusRunning,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(sessionListResponse{
			Success: true,
			Data: &sessionListData{
				TaskID:   taskID,
				Sessions: items,
			},
		})
	})

	// Test: query existing task
	req := httptest.NewRequest("GET", "/api/tasks/task-api/sessions", nil)
	req.SetPathValue("taskId", "task-api")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp sessionListResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.True(t, resp.Success)
	require.NotNil(t, resp.Data)
	assert.Equal(t, "task-api", resp.Data.TaskID)
	require.Len(t, resp.Data.Sessions, 2)

	for _, s := range resp.Data.Sessions {
		assert.NotEmpty(t, s.SessionID)
		assert.Equal(t, "task-api", s.TaskID)
		assert.Equal(t, sessions.StatusCompleted, s.Status)
		assert.False(t, s.IsActive)
	}

	// Test: query non-existent task returns empty array (not null)
	reqEmpty := httptest.NewRequest("GET", "/api/tasks/task-nonexistent/sessions", nil)
	reqEmpty.SetPathValue("taskId", "task-nonexistent")
	recEmpty := httptest.NewRecorder()

	handler.ServeHTTP(recEmpty, reqEmpty)

	assert.Equal(t, http.StatusOK, recEmpty.Code)

	var respEmpty sessionListResponse
	err = json.Unmarshal(recEmpty.Body.Bytes(), &respEmpty)
	require.NoError(t, err)
	assert.True(t, respEmpty.Success)
	require.NotNil(t, respEmpty.Data)
	assert.Len(t, respEmpty.Data.Sessions, 0)
}
