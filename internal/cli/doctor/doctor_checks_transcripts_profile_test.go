package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

// stageProfiledClaudeTranscript stages a Claude Code transcript for agent under
// its harness profile — <runtimeDir>/.loom/agent-profiles/<agent>/claude/projects
// — rather than under the operator's ~/.claude, which is where a profiled agent
// actually writes.
func stageProfiledClaudeTranscript(t *testing.T, runtimeDir, agent string) {
	t.Helper()
	token := filepath.Base(runtimeDir)
	projectDir := filepath.Join(runtimeDir, ".loom", "agent-profiles", agent,
		"claude", "projects", "-tmp-"+token+"-worktrees-"+agent)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir profiled project: %v", err)
	}
	line := `{"type":"assistant","message":{"id":"m1","usage":` +
		`{"input_tokens":700,"output_tokens":300,"cache_read_input_tokens":70,"cache_creation_input_tokens":30}}}` + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, "profiled-uuid.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatalf("write profiled transcript: %v", err)
	}
}

// TestCheckOrphanedTranscripts_BackfillsProfiledAndLegacyAgents is the direct
// regression for "doctor under-reports for profiled agents": `loom doctor` runs
// in the operator's shell, which carries none of the agent's injected
// CLAUDE_CONFIG_DIR, so a process-scoped projects root reported every profiled
// session as unmatched. Both agents must backfill in one run — the profiled one
// from its profile, the legacy one from ~/.claude.
func TestCheckOrphanedTranscripts_BackfillsProfiledAndLegacyAgents(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	runtimeDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "") // the operator's shell: no override
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	// Legacy agent: transcript in ~/.claude/projects, no profile dir.
	legacyStore, legacySID := stageClaudeSession(t, runtimeDir, home, "legacy-worker", true)
	markSessionCompleted(t, legacyStore, legacySID)

	// Profiled agent: transcript ONLY under its profile.
	profiledStore, profiledSID := stageClaudeSession(t, runtimeDir, home, "profiled-worker", false)
	stageProfiledClaudeTranscript(t, runtimeDir, "profiled-worker")
	markSessionCompleted(t, profiledStore, profiledSID)

	doctorFix = true
	t.Cleanup(func() { doctorFix = false })

	res := checkOrphanedTranscripts()
	if res.Status != StatusPass {
		t.Fatalf("status = %v, want pass (summary=%q detail=%q)", res.Status, res.Summary, res.Detail)
	}

	for _, tc := range []struct {
		name                   string
		sid                    string
		in, out, cread, cwrite int64
	}{
		{"legacy", legacySID, 100, 50, 10, 5},
		{"profiled", profiledSID, 700, 300, 70, 30},
	} {
		if _, err := os.Stat(profiledStore.NativeTranscriptPath(tc.sid)); err != nil {
			t.Errorf("%s: agent_transcript.jsonl not written: %v", tc.name, err)
			continue
		}
		meta, err := profiledStore.LoadMetadata(tc.sid)
		if err != nil {
			t.Fatalf("%s: LoadMetadata: %v", tc.name, err)
		}
		if meta.InputTokens != tc.in || meta.OutputTokens != tc.out ||
			meta.CacheReadTokens != tc.cread || meta.CacheWriteTokens != tc.cwrite {
			t.Errorf("%s: tokens = in%d/out%d/cr%d/cw%d, want %d/%d/%d/%d",
				tc.name, meta.InputTokens, meta.OutputTokens, meta.CacheReadTokens, meta.CacheWriteTokens,
				tc.in, tc.out, tc.cread, tc.cwrite)
		}
	}
}

// TestClaudeProjectsRootFor_FallsBackWithoutProfile keeps the opt-in contract
// honest at the doctor call site: an agent with no profile still resolves the
// process-scoped root, so a fleet without profiles sees an identical report.
func TestClaudeProjectsRootFor_FallsBackWithoutProfile(t *testing.T) {
	runtimeDir := t.TempDir()
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	if got, want := claudeProjectsRootFor("nobody"), filepath.Join(configDir, "projects"); got != want {
		t.Errorf("claudeProjectsRootFor = %q, want %q", got, want)
	}

	profile := filepath.Join(runtimeDir, ".loom", "agent-profiles", "somebody", "claude")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := claudeProjectsRootFor("somebody"), filepath.Join(profile, "projects"); got != want {
		t.Errorf("profiled claudeProjectsRootFor = %q, want %q", got, want)
	}
}
