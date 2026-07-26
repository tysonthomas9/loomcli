package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		stageClaudeCodeTranscript(t, runtimeDir, home, agent)
	}
	return store, sess.SessionID()
}

func markSessionCompleted(t *testing.T, store *sessions.Store, sid string) {
	t.Helper()
	meta, err := store.LoadMetadata(sid)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	now := time.Now().UTC()
	meta.TaskID = "TASK-1"
	meta.Status = sessions.StatusCompleted
	meta.ExitCode = 0
	meta.EndedAt = &now
	if err := store.SaveMetadata(sid, meta); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}
	if err := store.ReIndex(meta.SessionRecord); err != nil {
		t.Fatalf("ReIndex: %v", err)
	}
}

func stageClaudeCodeTranscript(t *testing.T, runtimeDir, home, agent string) {
	t.Helper()
	token := filepath.Base(runtimeDir)
	projectDir := filepath.Join(home, ".claude", "projects", "-tmp-"+token+"-worktrees-"+agent)
	if mkErr := os.MkdirAll(projectDir, 0o755); mkErr != nil {
		t.Fatalf("mkdir project: %v", mkErr)
	}
	line := `{"type":"assistant","message":{"id":"m1","model":"claude-opus-4-8","usage":` +
		`{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":10,"cache_creation_input_tokens":5}}}` + "\n" +
		`{"type":"result","cost_usd":0.0042}` + "\n"
	if wErr := os.WriteFile(filepath.Join(projectDir, "cc-uuid.jsonl"), []byte(line), 0o600); wErr != nil {
		t.Fatalf("write cc transcript: %v", wErr)
	}
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
	markSessionCompleted(t, store, sid)

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
	if meta.EstimatedCostUSD != 0.0042 {
		t.Errorf("estimated cost = %v, want 0.0042", meta.EstimatedCostUSD)
	}
	if meta.Model != "claude-opus-4-8" {
		t.Errorf("model = %q, want claude-opus-4-8", meta.Model)
	}
}

func TestCheckOrphanedTranscripts_BackfillCaseInsensitiveWorkspaceToken(t *testing.T) {
	// Regression: the workspace path case can drift (registered "WEB" while
	// Claude encoded the worktree cwd as "web"). A case-sensitive token match
	// found nothing and left every transcript unbackfilled.
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	runtimeDir := filepath.Join(t.TempDir(), "WEB") // token = "WEB"
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	store, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName: "quill", Backend: "claude", Phase: "implementation",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sid := sess.SessionID()

	// Claude encoded the cwd with lowercase "web" — differs only in case.
	projectDir := filepath.Join(home, ".claude", "projects", "-tmp-web-worktrees-quill")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","message":{"id":"m1","usage":` +
		`{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":10,"cache_creation_input_tokens":5}}}` + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, "cc-uuid.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	markSessionCompleted(t, store, sid)

	doctorFix = true
	t.Cleanup(func() { doctorFix = false })

	res := checkOrphanedTranscripts()
	if res.Status != StatusPass {
		t.Fatalf("status = %v; case-only token difference should still backfill (summary=%q detail=%q)", res.Status, res.Summary, res.Detail)
	}
	if _, err := os.Stat(store.NativeTranscriptPath(sid)); err != nil {
		t.Errorf("transcript not backfilled despite case-only workspace-token difference: %v", err)
	}
}

func TestCheckOrphanedTranscripts_SkipsRunningSessions(t *testing.T) {
	runtimeDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	stageClaudeSession(t, runtimeDir, home, "jack-worker", true)

	doctorFix = false
	res := checkOrphanedTranscripts()
	if res.Status != StatusPass {
		t.Fatalf("status = %v, want pass for running session (summary=%q detail=%q)", res.Status, res.Summary, res.Detail)
	}
}

func TestCheckOrphanedTranscripts_BackfillReindexesEmptyTranscript(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	runtimeDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	store, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName: "jack-worker",
		Backend:   "claude",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{TaskID: "TASK-1", ExitCode: 0}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if err := os.WriteFile(store.NativeTranscriptPath(sess.SessionID()), nil, 0o600); err != nil {
		t.Fatalf("write empty native transcript: %v", err)
	}
	stageClaudeCodeTranscript(t, runtimeDir, home, "jack-worker")

	doctorFix = true
	t.Cleanup(func() { doctorFix = false })

	res := checkOrphanedTranscripts()
	if res.Status != StatusPass {
		t.Fatalf("status = %v, summary=%q detail=%q", res.Status, res.Summary, res.Detail)
	}
	info, err := os.Stat(store.NativeTranscriptPath(sess.SessionID()))
	if err != nil {
		t.Fatalf("stat native transcript: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("native transcript remained empty after backfill")
	}
	records, err := store.SessionsByTask("TASK-1")
	if err != nil {
		t.Fatalf("SessionsByTask: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].InputTokens != 100 || records[0].OutputTokens != 50 {
		t.Fatalf("indexed usage = in:%d out:%d, want 100/50", records[0].InputTokens, records[0].OutputTokens)
	}
}

func TestCheckOrphanedTranscripts_NoFixWarns(t *testing.T) {
	runtimeDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	store, sid := stageClaudeSession(t, runtimeDir, home, "jack-worker", false)
	markSessionCompleted(t, store, sid)

	doctorFix = false
	res := checkOrphanedTranscripts()
	if res.Status != StatusWarn {
		t.Fatalf("status = %v, want warn (summary=%q)", res.Status, res.Summary)
	}
	if !strings.Contains(res.Summary, "missing a captured transcript") {
		t.Errorf("summary = %q", res.Summary)
	}
}
