//go:build testbackend

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// TestSessionCapture_FullLifecycle verifies the full session lifecycle:
// create → EchoBackend invocation with UsageHandler → finalize → verify
// index.jsonl, metadata.json, prompt.txt, and token counts.
func TestSessionCapture_FullLifecycle(t *testing.T) {
	env := NewEchoTestEnv(t)
	runtimeDir := filepath.Join(env.WorkDir, ".loom/runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	store, err := sessions.NewStore(t.Context(), runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Create session before invocation.
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName:  "test-agent",
		Backend:    "echo",
		Phase:      "implementation",
		Prompt:     "implement feature X",
		AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sid := sess.SessionID()
	if sid == "" {
		t.Fatal("session ID is empty")
	}

	// Verify session appears in query with running status.
	recs, err := store.Query(sessions.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].Status != sessions.StatusRunning {
		t.Errorf("expected status running, got %s", recs[0].Status)
	}
	if recs[0].Phase != "implementation" {
		t.Errorf("expected phase implementation, got %s", recs[0].Phase)
	}

	// Run the echo backend with known token counts.
	env.Backend.SetHandler(UsageHandler(5000, 2000))
	if err := env.RunNonInteractive("implement feature X"); err != nil {
		t.Fatalf("RunNonInteractive: %v", err)
	}

	// Finalize the collector to get accumulated token counts.
	now := time.Now()
	su := env.Collector.Finalize("task-lifecycle", "", now, now, 0)

	// Finalize the session with token counts from collector.
	err = sess.Finalize(sessions.FinalizeOptions{
		TaskID:       "task-lifecycle",
		ExitCode:     0,
		InputTokens:  su.InputTokens,
		OutputTokens: su.OutputTokens,
		DiffStats: sessions.DiffStats{
			FilesChanged: 2,
			LinesAdded:   10,
			LinesRemoved: 3,
		},
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	env.AssertInvoked(1)

	// Verify finalized metadata.
	meta, err := store.LoadMetadata(sid)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.Status != sessions.StatusCompleted {
		t.Errorf("status = %s, want completed", meta.Status)
	}
	if meta.TaskID != "task-lifecycle" {
		t.Errorf("task_id = %s, want task-lifecycle", meta.TaskID)
	}
	if meta.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", meta.ExitCode)
	}
	if meta.InputTokens != 5000 {
		t.Errorf("input_tokens = %d, want 5000", meta.InputTokens)
	}
	if meta.OutputTokens != 2000 {
		t.Errorf("output_tokens = %d, want 2000", meta.OutputTokens)
	}
	if meta.FilesChanged != 2 {
		t.Errorf("files_changed = %d, want 2", meta.FilesChanged)
	}
	if meta.EndedAt == nil {
		t.Error("EndedAt should not be nil")
	}
	if meta.DurationS <= 0 {
		t.Errorf("duration_s = %f, want > 0", meta.DurationS)
	}

	// Verify prompt.txt was saved.
	prompt, err := store.ReadPrompt(sid)
	if err != nil {
		t.Fatalf("ReadPrompt: %v", err)
	}
	if prompt != "implement feature X" {
		t.Errorf("prompt = %q, want %q", prompt, "implement feature X")
	}

	// Verify index.jsonl query by task_id returns the finalized record.
	byTask, err := store.SessionsByTask("task-lifecycle")
	if err != nil {
		t.Fatalf("SessionsByTask: %v", err)
	}
	if len(byTask) != 1 {
		t.Fatalf("expected 1 record for task-lifecycle, got %d", len(byTask))
	}
	rec := byTask[0]
	if rec.SessionID != sid {
		t.Errorf("record session_id = %s, want %s", rec.SessionID, sid)
	}
	if rec.Status != sessions.StatusCompleted {
		t.Errorf("record status = %s, want completed", rec.Status)
	}
	if rec.InputTokens != 5000 {
		t.Errorf("record input_tokens = %d, want 5000", rec.InputTokens)
	}
	if rec.OutputTokens != 2000 {
		t.Errorf("record output_tokens = %d, want 2000", rec.OutputTokens)
	}
}

// TestSessionCapture_TranscriptAppend verifies that AppendTranscript
// writes entries with auto-assigned Seq numbers.
func TestSessionCapture_TranscriptAppend(t *testing.T) {
	runtimeDir := t.TempDir()
	store, err := sessions.NewStore(t.Context(), runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName:  "test-agent",
		Backend:    "echo",
		Prompt:     "test prompt",
		AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sid := sess.SessionID()
	now := time.Now().UTC()

	// Append entries (Seq should be auto-assigned).
	entries := []sessions.TranscriptEntry{
		{Timestamp: now, Role: "system", Type: "session_start", Content: "Session started"},
		{Timestamp: now.Add(time.Second), Role: "user", Type: "text", Content: "Hello"},
		{Timestamp: now.Add(2 * time.Second), Role: "assistant", Type: "text", Content: "Hi there"},
	}
	for _, e := range entries {
		if err := store.AppendTranscript(sid, e); err != nil {
			t.Fatalf("AppendTranscript: %v", err)
		}
	}

	// Load and verify.
	loaded, err := store.LoadTranscript(sid)
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(loaded))
	}

	for i, e := range loaded {
		if e.Seq != i+1 {
			t.Errorf("entry[%d].Seq = %d, want %d", i, e.Seq, i+1)
		}
	}
	if loaded[0].Type != "session_start" {
		t.Errorf("entry[0].Type = %q, want session_start", loaded[0].Type)
	}
	if loaded[1].Content != "Hello" {
		t.Errorf("entry[1].Content = %q, want Hello", loaded[1].Content)
	}
}

// TestSessionCapture_MultipleSessionsPerTask verifies that creating
// multiple sessions for the same task (retry scenario) produces
// queryable records sorted by StartedAt.
func TestSessionCapture_MultipleSessionsPerTask(t *testing.T) {
	runtimeDir := t.TempDir()
	store, err := sessions.NewStore(t.Context(), runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Create 3 sessions sequentially (simulating retries).
	for attempt := 1; attempt <= 3; attempt++ {
		sess, createErr := store.CreateSession(sessions.CreateOptions{
			AgentName:  "retry-agent",
			Backend:    "echo",
			AttemptNum: attempt,
		})
		if createErr != nil {
			t.Fatalf("CreateSession attempt %d: %v", attempt, createErr)
		}
		if finalizeErr := sess.Finalize(sessions.FinalizeOptions{
			TaskID:   "task-retry",
			ExitCode: 0,
		}); finalizeErr != nil {
			t.Fatalf("Finalize attempt %d: %v", attempt, finalizeErr)
		}
	}

	// Query all sessions for this task.
	recs, err := store.SessionsByTask("task-retry")
	if err != nil {
		t.Fatalf("SessionsByTask: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}

	// Verify all have correct task ID and completed status.
	for i, rec := range recs {
		if rec.TaskID != "task-retry" {
			t.Errorf("record[%d].TaskID = %q, want task-retry", i, rec.TaskID)
		}
		if rec.Status != sessions.StatusCompleted {
			t.Errorf("record[%d].Status = %s, want completed", i, rec.Status)
		}
	}

	// Verify all 3 attempt numbers are present (order-independent).
	attemptsSeen := make(map[int]bool, 3)
	for _, rec := range recs {
		attemptsSeen[rec.AttemptNum] = true
	}
	for _, want := range []int{1, 2, 3} {
		if !attemptsSeen[want] {
			t.Errorf("missing AttemptNum %d in results", want)
		}
	}

	// Verify records are sorted by StartedAt ascending.
	for i := 1; i < len(recs); i++ {
		if recs[i].StartedAt.Before(recs[i-1].StartedAt) {
			t.Errorf("record[%d].StartedAt (%v) is before record[%d].StartedAt (%v)",
				i, recs[i].StartedAt, i-1, recs[i-1].StartedAt)
		}
	}
}

// TestSessionCapture_FailedSession verifies that an EchoBackend error
// results in a failed session with correct status in both metadata and index.
func TestSessionCapture_FailedSession(t *testing.T) {
	env := NewEchoTestEnv(t)
	runtimeDir := filepath.Join(env.WorkDir, ".loom/runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	store, err := sessions.NewStore(t.Context(), runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName:  "fail-agent",
		Backend:    "echo",
		Prompt:     "run linter",
		AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sid := sess.SessionID()

	// Configure the backend to return an error.
	env.Backend.SetHandler(ErrorHandler(errors.New("lint failure")))
	runErr := env.RunNonInteractive("run linter")
	if runErr == nil {
		t.Fatal("expected error from RunNonInteractive, got nil")
	}

	// Invocation should still be recorded.
	env.AssertInvoked(1)

	// Finalize with failure.
	if err := sess.Finalize(sessions.FinalizeOptions{
		TaskID:     "task-fail",
		ExitCode:   1,
		ErrorClass: "lint_failure",
	}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	// Verify metadata.json shows failed status.
	meta, err := store.LoadMetadata(sid)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.Status != sessions.StatusFailed {
		t.Errorf("metadata status = %s, want failed", meta.Status)
	}
	if meta.ExitCode != 1 {
		t.Errorf("metadata exit_code = %d, want 1", meta.ExitCode)
	}
	if meta.ErrorClass != "lint_failure" {
		t.Errorf("metadata error_class = %q, want lint_failure", meta.ErrorClass)
	}

	// Verify index.jsonl also has failed status.
	recs, err := store.SessionsByTask("task-fail")
	if err != nil {
		t.Fatalf("SessionsByTask: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].Status != sessions.StatusFailed {
		t.Errorf("index status = %s, want failed", recs[0].Status)
	}
	if recs[0].ExitCode != 1 {
		t.Errorf("index exit_code = %d, want 1", recs[0].ExitCode)
	}
}

// TestSessionCapture_ApiQuery verifies that sessions created through
// the full lifecycle are correctly queryable via the store's query API,
// matching what the HTTP handler would return.
func TestSessionCapture_ApiQuery(t *testing.T) {
	runtimeDir := t.TempDir()
	store, err := sessions.NewStore(t.Context(), runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Create and finalize 2 sessions for the same task.
	var sessionIDs []string
	for i := 1; i <= 2; i++ {
		sess, createErr := store.CreateSession(sessions.CreateOptions{
			AgentName:  "api-agent",
			Backend:    "echo",
			AttemptNum: i,
		})
		if createErr != nil {
			t.Fatalf("CreateSession %d: %v", i, createErr)
		}
		sessionIDs = append(sessionIDs, sess.SessionID())

		if finalizeErr := sess.Finalize(sessions.FinalizeOptions{
			TaskID:       "task-api",
			ExitCode:     0,
			InputTokens:  int64(i * 1000),
			OutputTokens: int64(i * 500),
		}); finalizeErr != nil {
			t.Fatalf("Finalize %d: %v", i, finalizeErr)
		}
	}

	// Query by task — this is the same call handleListTaskSessions makes.
	recs, err := store.SessionsByTask("task-api")
	if err != nil {
		t.Fatalf("SessionsByTask: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}

	// Verify each record has the expected fields (matching API response shape).
	for i, rec := range recs {
		if rec.SessionID == "" {
			t.Errorf("record[%d].SessionID is empty", i)
		}
		if rec.TaskID != "task-api" {
			t.Errorf("record[%d].TaskID = %q, want task-api", i, rec.TaskID)
		}
		if rec.Status != sessions.StatusCompleted {
			t.Errorf("record[%d].Status = %s, want completed", i, rec.Status)
		}
		if rec.AgentName != "api-agent" {
			t.Errorf("record[%d].AgentName = %q, want api-agent", i, rec.AgentName)
		}
		if rec.Backend != "echo" {
			t.Errorf("record[%d].Backend = %q, want echo", i, rec.Backend)
		}
	}

	// Verify token counts match what was set during finalize.
	if recs[0].InputTokens != 1000 {
		t.Errorf("record[0].InputTokens = %d, want 1000", recs[0].InputTokens)
	}
	if recs[1].InputTokens != 2000 {
		t.Errorf("record[1].InputTokens = %d, want 2000", recs[1].InputTokens)
	}

	// Verify empty task returns empty (not error) — matching API empty response.
	empty, err := store.SessionsByTask("task-nonexistent")
	if err != nil {
		t.Fatalf("SessionsByTask(nonexistent): %v", err)
	}
	if empty != nil && len(empty) != 0 {
		t.Errorf("expected empty result for nonexistent task, got %d", len(empty))
	}

	// Verify each session's metadata is individually loadable (as handleGetSession does).
	for _, sid := range sessionIDs {
		meta, loadErr := store.LoadMetadata(sid)
		if loadErr != nil {
			t.Errorf("LoadMetadata(%s): %v", sid, loadErr)
			continue
		}
		if meta.TaskID != "task-api" {
			t.Errorf("metadata(%s).TaskID = %q, want task-api", sid, meta.TaskID)
		}
	}
}
