//go:build testbackend

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// TestSessionCapture_CreateAndFinalize verifies the full session lifecycle:
// create a session, run an EchoBackend invocation, and finalize.
func TestSessionCapture_CreateAndFinalize(t *testing.T) {
	env := NewEchoTestEnv(t)
	beadsDir := filepath.Join(env.WorkDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	store, err := sessions.NewStore(beadsDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Create session before invocation.
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName:  "test-agent",
		Backend:    "echo",
		Phase:      "implementation",
		Prompt:     "fix the bug",
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

	// Set up env vars (as automode.go would).
	SetActiveSessionEnv(beadsDir, sid)
	defer ClearActiveSessionEnv()

	// Run the echo backend.
	env.Backend.SetHandler(UsageHandler(500, 200))
	if err := env.RunNonInteractive("fix the bug"); err != nil {
		t.Fatalf("RunNonInteractive: %v", err)
	}

	// Finalize the session.
	err = sess.Finalize(sessions.FinalizeOptions{
		TaskID:   "test-task-1",
		ExitCode: 0,
		DiffStats: sessions.DiffStats{
			FilesChanged: 2,
			LinesAdded:   10,
			LinesRemoved: 3,
		},
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	// Verify finalized record.
	meta, err := store.LoadMetadata(sid)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.Status != sessions.StatusCompleted {
		t.Errorf("expected completed, got %s", meta.Status)
	}
	if meta.TaskID != "test-task-1" {
		t.Errorf("expected task test-task-1, got %s", meta.TaskID)
	}
	if meta.FilesChanged != 2 {
		t.Errorf("expected 2 files changed, got %d", meta.FilesChanged)
	}
	if meta.EndedAt == nil {
		t.Error("EndedAt should not be nil")
	}
}

// TestSessionCapture_TranscriptAppend verifies that AppendTranscript
// writes entries with auto-assigned Seq numbers.
func TestSessionCapture_TranscriptAppend(t *testing.T) {
	beadsDir := t.TempDir()
	store, err := sessions.NewStore(beadsDir)
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

// TestSessionCapture_HookDispatch verifies the hook dispatch pipeline:
// parse Claude hook input → dispatch to session transcript.
func TestSessionCapture_HookDispatch(t *testing.T) {
	beadsDir := t.TempDir()
	store, err := sessions.NewStore(beadsDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName:  "test-agent",
		Backend:    "claude",
		Prompt:     "implement feature",
		AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sid := sess.SessionID()

	// Simulate hook events by dispatching directly.
	events := []HookEvent{
		{
			Type:      HookSessionStart,
			SessionID: "claude-session-1",
			Model:     "claude-sonnet-4-20250514",
			Backend:   "claude",
			Timestamp: time.Now(),
		},
		{
			Type:      HookTurnStart,
			SessionID: "claude-session-1",
			Prompt:    "implement feature",
			Backend:   "claude",
			Timestamp: time.Now(),
		},
		{
			Type:      HookSubagentStart,
			SessionID: "claude-session-1",
			ToolUseID: "toolu_abc123",
			ToolInput: json.RawMessage(`{"prompt":"fix tests"}`),
			Backend:   "claude",
			Timestamp: time.Now(),
		},
		{
			Type:       HookSubagentEnd,
			SessionID:  "claude-session-1",
			ToolUseID:  "toolu_abc123",
			SubagentID: "agent-sub-001",
			Backend:    "claude",
			Timestamp:  time.Now(),
		},
		{
			Type:      HookTurnEnd,
			SessionID: "claude-session-1",
			Backend:   "claude",
			Timestamp: time.Now(),
		},
		{
			Type:      HookSessionEnd,
			SessionID: "claude-session-1",
			Backend:   "claude",
			Timestamp: time.Now(),
		},
	}

	for _, ev := range events {
		_ = dispatchHookEvent(&ev, beadsDir, sid)
	}

	// Verify transcript was written.
	loaded, err := store.LoadTranscript(sid)
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if len(loaded) != 6 {
		t.Fatalf("expected 6 transcript entries, got %d", len(loaded))
	}

	// Verify entry types in order.
	expectedTypes := []string{"session_start", "text", "subagent_start", "subagent_end", "turn_end", "session_end"}
	for i, e := range loaded {
		if e.Type != expectedTypes[i] {
			t.Errorf("entry[%d].Type = %q, want %q", i, e.Type, expectedTypes[i])
		}
		if e.Seq != i+1 {
			t.Errorf("entry[%d].Seq = %d, want %d", i, e.Seq, i+1)
		}
	}

	// Verify subagent entries have tool info.
	if loaded[2].ToolName != "Task" {
		t.Errorf("subagent_start.ToolName = %q, want Task", loaded[2].ToolName)
	}
}

// TestSessionCapture_QueryByTask verifies that finalized sessions
// can be queried by task ID.
func TestSessionCapture_QueryByTask(t *testing.T) {
	beadsDir := t.TempDir()
	store, err := sessions.NewStore(beadsDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Create two sessions for different tasks.
	sess1, _ := store.CreateSession(sessions.CreateOptions{
		AgentName: "agent-a", Backend: "echo", AttemptNum: 1,
	})
	sess2, _ := store.CreateSession(sessions.CreateOptions{
		AgentName: "agent-b", Backend: "echo", AttemptNum: 1,
	})

	_ = sess1.Finalize(sessions.FinalizeOptions{TaskID: "task-alpha"})
	_ = sess2.Finalize(sessions.FinalizeOptions{TaskID: "task-beta"})

	// Query by task.
	alpha, err := store.SessionsByTask("task-alpha")
	if err != nil {
		t.Fatalf("SessionsByTask: %v", err)
	}
	if len(alpha) != 1 {
		t.Errorf("expected 1 session for task-alpha, got %d", len(alpha))
	}
	if len(alpha) > 0 && alpha[0].AgentName != "agent-a" {
		t.Errorf("expected agent-a, got %s", alpha[0].AgentName)
	}

	beta, _ := store.SessionsByTask("task-beta")
	if len(beta) != 1 {
		t.Errorf("expected 1 session for task-beta, got %d", len(beta))
	}
}
