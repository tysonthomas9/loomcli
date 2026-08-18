package sessionfinalize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// newSessionWithCodexRollout stands up a session on the given backend next to a
// codex rollout that names the session's worktree, so a test can assert only on
// whether WithWorktree chose to probe for it.
func newSessionWithCodexRollout(t *testing.T, backend string) (*sessions.Store, *sessions.Session, string) {
	t.Helper()
	runtimeDir := t.TempDir()
	store, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName: "worker",
		Backend:   backend,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	worktreeDir := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	now := time.Now()
	rolloutDir := filepath.Join(
		codexHome,
		"sessions",
		now.Format("2006"),
		now.Format("01"),
		now.Format("02"),
	)
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("create rollout directory: %v", err)
	}
	meta, err := json.Marshal(map[string]any{
		"type":    "session_meta",
		"payload": map[string]any{"cwd": worktreeDir},
	})
	if err != nil {
		t.Fatalf("marshal rollout metadata: %v", err)
	}
	rolloutPath := filepath.Join(rolloutDir, "rollout-test.jsonl")
	if err := os.WriteFile(rolloutPath, append(meta, '\n'), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	return store, sess, worktreeDir
}

// TestWithWorktree_EmptyBackendMirrorsCodexRollout pins the migration shim: a
// session persisted by a pre-backend binary still gets its transcript mirrored.
func TestWithWorktree_EmptyBackendMirrorsCodexRollout(t *testing.T) {
	store, sess, worktreeDir := newSessionWithCodexRollout(t, "")

	if _, err := WithWorktree(sess, WithWorktreeOptions{WorktreePath: worktreeDir}); err != nil {
		t.Fatalf("WithWorktree: %v", err)
	}

	transcript, err := os.ReadFile(store.NativeTranscriptPath(sess.SessionID()))
	if err != nil {
		t.Fatalf("read native transcript: %v", err)
	}
	if len(transcript) == 0 {
		t.Fatal("native transcript is empty")
	}
}

// TestWithWorktree_UnhandledBackendIsNotProbed guards the boundary the shim must
// not cross: gemini is a registered backend with neither a codex rollout nor a
// claude transcript, so finalize must mirror nothing rather than attribute
// another backend's transcript to it.
func TestWithWorktree_UnhandledBackendIsNotProbed(t *testing.T) {
	store, sess, worktreeDir := newSessionWithCodexRollout(t, "gemini")

	if _, err := WithWorktree(sess, WithWorktreeOptions{WorktreePath: worktreeDir}); err != nil {
		t.Fatalf("WithWorktree: %v", err)
	}

	transcript, err := os.ReadFile(store.NativeTranscriptPath(sess.SessionID()))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read native transcript: %v", err)
	}
	if len(transcript) != 0 {
		t.Fatalf("gemini session was probed for a codex rollout; mirrored %d bytes", len(transcript))
	}
}
