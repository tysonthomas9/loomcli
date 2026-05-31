package sessionfinalize

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

func TestWithWorktreeEnrichesCodexUsageFromRollout(t *testing.T) {
	runtimeDir := t.TempDir()
	workDir := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	store, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName: "codex-agent",
		Backend:   backendnames.Codex,
		Phase:     "test",
		Prompt:    "prompt",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	rolloutPath := writeCodexRollout(t, codexHome, workDir)
	if _, err := WithWorktree(sess, WithWorktreeOptions{
		WorktreePath: workDir,
		TaskID:       "task-1",
		ExitCode:     0,
	}); err != nil {
		t.Fatalf("WithWorktree: %v", err)
	}

	meta, err := store.LoadMetadata(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.InputTokens != 13797 {
		t.Errorf("InputTokens = %d, want 13797", meta.InputTokens)
	}
	if meta.OutputTokens != 28 {
		t.Errorf("OutputTokens = %d, want 28", meta.OutputTokens)
	}
	if meta.CacheReadTokens != 4480 {
		t.Errorf("CacheReadTokens = %d, want 4480", meta.CacheReadTokens)
	}
	if meta.Model != "gpt-5.5" {
		t.Errorf("Model = %q, want gpt-5.5", meta.Model)
	}
	if _, err := os.Stat(store.NativeTranscriptPath(sess.SessionID())); err != nil {
		t.Fatalf("native transcript not mirrored from %s: %v", rolloutPath, err)
	}
}

func writeCodexRollout(t *testing.T, codexHome, workDir string) string {
	t.Helper()
	now := time.Now()
	rolloutDir := filepath.Join(
		codexHome,
		"sessions",
		fmt.Sprintf("%04d", now.Year()),
		fmt.Sprintf("%02d", int(now.Month())),
		fmt.Sprintf("%02d", now.Day()),
	)
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}
	rolloutPath := filepath.Join(rolloutDir, "rollout-test.jsonl")
	lines := fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"cwd":%q}}
{"timestamp":%q,"type":"turn_context","payload":{"model":"gpt-5.5"}}
{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":13797,"cached_input_tokens":4480,"output_tokens":28,"reasoning_output_tokens":17,"total_tokens":13825}}}}
`, now.Format(time.RFC3339Nano), workDir, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err := os.WriteFile(rolloutPath, []byte(lines), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	return rolloutPath
}
