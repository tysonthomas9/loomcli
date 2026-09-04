package sessionfinalize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// createTestSession creates a real session backed by a temp store.
func createTestSession(t *testing.T) (*sessions.Store, *sessions.Session) {
	t.Helper()
	store, err := sessions.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName:  "nova",
		Backend:    "claude",
		Prompt:     "test prompt",
		AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}
	return store, sess
}

// writeTranscript writes a Claude-shaped JSONL transcript into the session's
// native transcript path.
func writeTranscript(t *testing.T, store *sessions.Store, sess *sessions.Session, lines string) {
	t.Helper()
	path := store.NativeTranscriptPath(sess.Meta.SessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll error: %v", err)
	}
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
}

// readMetadata reads the persisted metadata.json for a session.
func readMetadata(t *testing.T, store *sessions.Store, sess *sessions.Session) sessions.SessionMetadata {
	t.Helper()
	path := filepath.Join(filepath.Dir(store.NativeTranscriptPath(sess.Meta.SessionID)), "metadata.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read metadata.json: %v", err)
	}
	var meta sessions.SessionMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("unmarshal metadata.json: %v", err)
	}
	return meta
}

const transcriptFixture = `{"type":"assistant","message":{"id":"msg_1","usage":{"input_tokens":1000,"output_tokens":200,"cache_read_input_tokens":300,"cache_creation_input_tokens":50}}}
{"type":"assistant","message":{"id":"msg_2","usage":{"input_tokens":500,"output_tokens":100,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`

func TestWithWorktree_TranscriptUsageFallback(t *testing.T) {
	store, sess := createTestSession(t)
	writeTranscript(t, store, sess, transcriptFixture)

	_, err := WithWorktree(sess, WithWorktreeOptions{
		WorktreePath: t.TempDir(),
		TaskID:       "task-1",
		ExitCode:     0,
	})
	if err != nil {
		t.Fatalf("WithWorktree error: %v", err)
	}

	meta := readMetadata(t, store, sess)
	if meta.InputTokens != 1500 {
		t.Errorf("InputTokens = %d, want 1500", meta.InputTokens)
	}
	if meta.OutputTokens != 300 {
		t.Errorf("OutputTokens = %d, want 300", meta.OutputTokens)
	}
	if meta.CacheReadTokens != 300 {
		t.Errorf("CacheReadTokens = %d, want 300", meta.CacheReadTokens)
	}
	if meta.CacheWriteTokens != 50 {
		t.Errorf("CacheWriteTokens = %d, want 50", meta.CacheWriteTokens)
	}
	if meta.EstimatedCostUSD != 0 {
		t.Errorf("EstimatedCostUSD = %f, want 0 (no token-based estimate)", meta.EstimatedCostUSD)
	}
}

func TestWithWorktree_NonZeroOptsWinOverTranscript(t *testing.T) {
	store, sess := createTestSession(t)
	writeTranscript(t, store, sess, transcriptFixture)

	_, err := WithWorktree(sess, WithWorktreeOptions{
		WorktreePath:     t.TempDir(),
		TaskID:           "task-2",
		InputTokens:      42,
		OutputTokens:     7,
		EstimatedCostUSD: 0.01,
	})
	if err != nil {
		t.Fatalf("WithWorktree error: %v", err)
	}

	meta := readMetadata(t, store, sess)
	if meta.InputTokens != 42 {
		t.Errorf("InputTokens = %d, want 42 (caller opts must win)", meta.InputTokens)
	}
	if meta.OutputTokens != 7 {
		t.Errorf("OutputTokens = %d, want 7 (caller opts must win)", meta.OutputTokens)
	}
	if meta.EstimatedCostUSD != 0.01 {
		t.Errorf("EstimatedCostUSD = %f, want 0.01", meta.EstimatedCostUSD)
	}
}

func TestRecoverUsageFromNativeTranscript_ZeroTranscriptLeavesOptsUntouched(t *testing.T) {
	store, sess := createTestSession(t)
	writeTranscript(t, store, sess,
		`{"type":"assistant","message":{"id":"msg_1","usage":{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`+"\n")

	opts := recoverUsageFromNativeTranscript(sess, WithWorktreeOptions{})
	if opts.InputTokens != 0 || opts.OutputTokens != 0 ||
		opts.CacheReadTokens != 0 || opts.CacheWriteTokens != 0 ||
		opts.EstimatedCostUSD != 0 {
		t.Errorf("opts modified on zero transcript sum: %+v", opts)
	}
}

func TestRecoverUsageFromNativeTranscript_UsesFinalTranscriptAfterPartialCapture(t *testing.T) {
	store, sess := createTestSession(t)
	writeTranscript(t, store, sess, transcriptFixture)

	// Simulate the SessionEnd hook having persisted token usage already.
	meta, err := store.LoadMetadata(sess.Meta.SessionID)
	if err != nil {
		t.Fatalf("LoadMetadata error: %v", err)
	}
	meta.InputTokens = 11
	meta.OutputTokens = 22
	if err := store.SaveMetadata(sess.Meta.SessionID, meta); err != nil {
		t.Fatalf("SaveMetadata error: %v", err)
	}

	opts := recoverUsageFromNativeTranscript(sess, WithWorktreeOptions{})
	if opts.InputTokens != 1500 || opts.OutputTokens != 300 || opts.EstimatedCostUSD != 0 {
		t.Errorf("usage not recovered from final transcript: %+v", opts)
	}
}

func TestRecoverUsageFromNativeTranscript_IsBackendAgnostic(t *testing.T) {
	store, err := sessions.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName: "nova", Backend: "codex", Prompt: "p", AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}
	writeTranscript(t, store, sess, transcriptFixture)

	opts := recoverUsageFromNativeTranscript(sess, WithWorktreeOptions{})
	if opts.InputTokens != 1500 || opts.OutputTokens != 300 || opts.EstimatedCostUSD != 0 {
		t.Errorf("usage not recovered for codex backend: %+v", opts)
	}
}

func TestRecoverUsageFromNativeTranscript_MissingTranscriptLeavesOptsUntouched(t *testing.T) {
	_, sess := createTestSession(t)

	opts := recoverUsageFromNativeTranscript(sess, WithWorktreeOptions{})
	if opts.InputTokens != 0 || opts.EstimatedCostUSD != 0 {
		t.Errorf("opts modified on missing transcript: %+v", opts)
	}
}

func TestRecoverUsageFromNativeTranscript_NonZeroOptsReturnEarly(t *testing.T) {
	store, sess := createTestSession(t)
	writeTranscript(t, store, sess, transcriptFixture)

	opts := recoverUsageFromNativeTranscript(sess, WithWorktreeOptions{CacheReadTokens: 9})
	if opts.InputTokens != 0 || opts.OutputTokens != 0 ||
		opts.CacheReadTokens != 9 || opts.EstimatedCostUSD != 0 {
		t.Errorf("fallback applied despite non-zero opts: %+v", opts)
	}
}
