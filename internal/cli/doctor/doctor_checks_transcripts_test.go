package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// stageClaudeSession creates a claude session with no captured transcript and,
// when stageCC is true, stages a Claude Code transcript under
// ~/.claude/projects for the same agent so the backfill can correlate it.
func stageClaudeSession(t *testing.T, runtimeDir, home, agent string, stageCC bool) (*sessions.Store, string) {
	t.Helper()
	store, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName: agent, Backend: "claude", Phase: "implementation",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if stageCC {
		token := filepath.Base(runtimeDir)
		projectDir := filepath.Join(home, ".claude", "projects", "-tmp-"+token+"-worktrees-"+agent)
		if mkErr := os.MkdirAll(projectDir, 0o755); mkErr != nil {
			t.Fatalf("mkdir project: %v", mkErr)
		}
		line := `{"type":"assistant","message":{"id":"m1","usage":` +
			`{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":10,"cache_creation_input_tokens":5}}}` + "\n"
		if wErr := os.WriteFile(filepath.Join(projectDir, "cc-uuid.jsonl"), []byte(line), 0o600); wErr != nil {
			t.Fatalf("write cc transcript: %v", wErr)
		}
	}
	return store, sess.SessionID()
}

func TestCheckOrphanedTranscripts_Backfills(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	runtimeDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	store, sid := stageClaudeSession(t, runtimeDir, home, "jack-worker", true)

	doctorFix = true
	t.Cleanup(func() { doctorFix = false })

	res := checkOrphanedTranscripts()
	if res.Status != StatusPass {
		t.Fatalf("status = %v, summary=%q detail=%q", res.Status, res.Summary, res.Detail)
	}
	if _, err := os.Stat(store.NativeTranscriptPath(sid)); err != nil {
		t.Errorf("agent_transcript.jsonl not written: %v", err)
	}
	meta, err := store.LoadMetadata(sid)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.InputTokens != 100 || meta.OutputTokens != 50 ||
		meta.CacheReadTokens != 10 || meta.CacheWriteTokens != 5 {
		t.Errorf("tokens = in%d/out%d/cr%d/cw%d, want 100/50/10/5",
			meta.InputTokens, meta.OutputTokens, meta.CacheReadTokens, meta.CacheWriteTokens)
	}
	if meta.EstimatedCostUSD <= 0 {
		t.Errorf("estimated cost = %v, want > 0", meta.EstimatedCostUSD)
	}
}

func TestCheckOrphanedTranscripts_NoFixWarns(t *testing.T) {
	runtimeDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	stageClaudeSession(t, runtimeDir, home, "jack-worker", false)

	doctorFix = false
	res := checkOrphanedTranscripts()
	if res.Status != StatusWarn {
		t.Fatalf("status = %v, want warn (summary=%q)", res.Status, res.Summary)
	}
	if !strings.Contains(res.Summary, "missing a captured transcript") {
		t.Errorf("summary = %q", res.Summary)
	}
}
