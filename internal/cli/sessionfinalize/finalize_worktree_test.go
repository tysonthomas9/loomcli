package sessionfinalize

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
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

func TestWithWorktreeCapturesOpenCodeExport(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	runtimeDir := t.TempDir()
	workDir := t.TempDir()

	store, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName: "opencode-agent",
		Backend:   backendnames.OpenCode,
		Phase:     "test",
		Prompt:    "prompt",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	const sessionID = "ses_123"
	exportOutput := `Exporting session: ses_123
{
  "info": {"id":"ses_123"},
  "messages": [
    {
      "info": {"id":"msg_user","role":"user","time":{"created":1700000000000}},
      "parts": [{"type":"text","text":"prompt"}]
    },
    {
      "info": {
        "id":"msg_assistant",
        "role":"assistant",
        "modelID":"anthropic/claude-sonnet-4",
        "time":{"created":1700000001000},
        "tokens":{"input":11,"output":7,"cache":{"read":3,"write":2}},
        "cost":0.0042
      },
      "parts": [{"type":"text","text":"loom real CLI smoke ok"}]
    }
  ]
}`

	orig := openCodeExportSession
	openCodeExportSession = func(gotWorkDir, gotSessionID string) ([]byte, error) {
		if gotWorkDir != workDir {
			t.Fatalf("workDir = %q, want %q", gotWorkDir, workDir)
		}
		if gotSessionID != sessionID {
			t.Fatalf("sessionID = %q, want %q", gotSessionID, sessionID)
		}
		return normalizeOpenCodeExportOutput(exportOutput), nil
	}
	t.Cleanup(func() { openCodeExportSession = orig })

	if _, err := WithWorktree(sess, WithWorktreeOptions{
		WorktreePath:      workDir,
		TaskID:            "task-1",
		ExitCode:          0,
		OpenCodeSessionID: sessionID,
	}); err != nil {
		t.Fatalf("WithWorktree: %v", err)
	}

	nativeData, err := os.ReadFile(store.NativeTranscriptPath(sess.SessionID()))
	if err != nil {
		t.Fatalf("read native transcript: %v", err)
	}
	if strings.Contains(string(nativeData), "Exporting session") {
		t.Fatalf("native transcript kept opencode export banner: %q", nativeData)
	}
	events, err := store.LoadNativeEvents(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadNativeEvents: %v", err)
	}
	if len(events) < 2 || events[1].Text != "loom real CLI smoke ok" {
		t.Fatalf("assistant event = %+v, want expected response", events)
	}

	meta, err := store.LoadMetadata(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.InputTokens != 11 {
		t.Errorf("InputTokens = %d, want 11", meta.InputTokens)
	}
	if meta.OutputTokens != 7 {
		t.Errorf("OutputTokens = %d, want 7", meta.OutputTokens)
	}
	if meta.CacheReadTokens != 3 {
		t.Errorf("CacheReadTokens = %d, want 3", meta.CacheReadTokens)
	}
	if meta.CacheWriteTokens != 2 {
		t.Errorf("CacheWriteTokens = %d, want 2", meta.CacheWriteTokens)
	}
	if math.Abs(meta.EstimatedCostUSD-0.0042) > 0.0000001 {
		t.Errorf("EstimatedCostUSD = %f, want 0.0042", meta.EstimatedCostUSD)
	}
	if meta.Model != "anthropic/claude-sonnet-4" {
		t.Errorf("Model = %q, want anthropic/claude-sonnet-4", meta.Model)
	}
}

func TestNormalizeOpenCodeExportOutput(t *testing.T) {
	got := normalizeOpenCodeExportOutput("Exporting session: ses_123\n{\"info\":{\"id\":\"ses_123\"}}\n")
	if string(got) != `{"info":{"id":"ses_123"}}` {
		t.Fatalf("normalizeOpenCodeExportOutput() = %q", got)
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
