package supervisor

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// Mirrors sessions.encodeClaudeCWD (unexported): Claude Code names its project
// dir by replacing every non-alphanumeric character with '-'.
var claudeCWDEncode = regexp.MustCompile(`[^A-Za-z0-9]`)

// The Go execution leaf writes no transcript before finalize — the backend
// re-sync inside finalizeLocalSession is what produces it. Reading the leaf
// transcript only BEFORE that re-sync therefore uploads nothing, so
// metadata["transcript_ref"] is never set and a non-owning serve node can never
// surface the transcript (it 404s on the control-plane path).
func TestFinalizeAgentSessionUploadsTranscriptProducedByResync(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

	worktree := t.TempDir()
	// Stage the backend's own native transcript where the claude re-sync looks.
	projectDir := filepath.Join(home, ".claude", "projects", claudeCWDEncode.ReplaceAllString(worktree, "-"))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	nativeLine := `{"type":"assistant","message":{"content":[{"type":"text","text":"did the work"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, "run.jsonl"), []byte(nativeLine), 0o600); err != nil {
		t.Fatalf("write native transcript: %v", err)
	}

	sessStore, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}

	st := memstore.New()
	s := newControlPlaneTestSupervisor(st)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task", Repo: "repo-a"},
		RoleConfig:   cfgpkg.RoleConfig{Backend: "claude"},
		WorktreePath: worktree,
	}
	s.createAgentSession(ap, "")
	if ap.AgentSessionID == "" {
		t.Fatal("AgentSessionID was not set")
	}
	controlSessionID := ap.AgentSessionID

	// Nothing has written the session's native transcript yet — this is exactly
	// the Go-leaf state at finalize time.
	if _, statErr := os.Stat(sessStore.NativeTranscriptPath(controlSessionID)); statErr == nil {
		t.Fatal("native transcript already exists; test does not reproduce the Go-leaf state")
	}

	s.finalizeAgentSession(ap, 0)

	// The re-sync must have produced the on-disk transcript...
	if _, statErr := os.Stat(sessStore.NativeTranscriptPath(controlSessionID)); statErr != nil {
		t.Fatalf("re-sync did not write the native transcript: %v", statErr)
	}
	// ...and the finalize must have uploaded it and referenced it.
	rec, err := st.AgentSessions().Get(t.Context(), "WS", controlSessionID)
	if err != nil {
		t.Fatalf("get control-plane session: %v", err)
	}
	ref := rec.Metadata["transcript_ref"]
	if ref == "" {
		t.Fatalf("transcript_ref not set after finalize; metadata=%#v", rec.Metadata)
	}
	if ref != "artifact://transcript-"+controlSessionID {
		t.Fatalf("transcript_ref = %q, want artifact://transcript-%s", ref, controlSessionID)
	}
	artifact, err := st.Artifacts().Get(t.Context(), "WS", "transcript-"+controlSessionID)
	if err != nil {
		t.Fatalf("get transcript artifact: %v", err)
	}
	if artifact.DurableStatus != "finalized" {
		t.Fatalf("artifact status = %q, want finalized", artifact.DurableStatus)
	}
}
