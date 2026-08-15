package sessionfinalize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

func TestWithWorktree_EmptyBackendMirrorsCodexRollout(t *testing.T) {
	runtimeDir := t.TempDir()
	store, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName: "worker",
		Backend:   "",
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
