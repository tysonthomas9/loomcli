package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

func writeProfiles(t *testing.T, runtimeDir string, agents ...string) {
	t.Helper()
	for _, agent := range agents {
		if err := os.MkdirAll(filepath.Join(runtimeDir, "profiles", agent), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCheckOrphanedTranscripts_SkipsUnconfiguredAgent checks that a session
// written under an agent the workspace does not configure is invisible to the
// health check — the defensive layer against a stray writer.
func TestCheckOrphanedTranscripts_SkipsUnconfiguredAgent(t *testing.T) {
	runtimeDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	writeProfiles(t, runtimeDir, "jack-worker")
	store, sid := stageClaudeSession(t, runtimeDir, home, "stray-writer", false)
	markSessionCompleted(t, store, sid)

	if res := checkOrphanedTranscripts(); res.Status != StatusPass {
		t.Fatalf("status = %v, want pass; summary=%q detail=%q", res.Status, res.Summary, res.Detail)
	}
}

// TestCheckOrphanedTranscripts_NoProfilesDirDoesNotFilter pins the
// nil-means-no-filter contract at the caller: without profiles/, the same
// session is still reported.
func TestCheckOrphanedTranscripts_NoProfilesDirDoesNotFilter(t *testing.T) {
	runtimeDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	store, sid := stageClaudeSession(t, runtimeDir, home, "stray-writer", false)
	markSessionCompleted(t, store, sid)

	res := checkOrphanedTranscripts()
	if res.Status != StatusWarn {
		t.Fatalf("status = %v, want warn; summary=%q detail=%q", res.Status, res.Summary, res.Detail)
	}
}

// TestScanSessionDirs_AllowlistCoversBothSides checks that a session excluded
// from the index by the allowlist is also excluded from the directory scan.
// Otherwise it would be reported as orphaned and `--fix` would re-append it to
// the index on every run.
func TestScanSessionDirs_AllowlistCoversBothSides(t *testing.T) {
	runtimeDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	writeProfiles(t, runtimeDir, "jack-worker")
	store, sid := stageClaudeSession(t, runtimeDir, home, "stray-writer", false)
	markSessionCompleted(t, store, sid)

	knownAgents := []string{"jack-worker"}
	indexedIDs, err := queryIndexedSessionIDs(store, knownAgents)
	if err != nil {
		t.Fatalf("queryIndexedSessionIDs: %v", err)
	}
	if indexedIDs[sid] {
		t.Fatalf("session %s of an unconfigured agent should not be indexed", sid)
	}
	scan, err := scanSessionDirs(store, store.Dir(), indexedIDs, knownAgents)
	if err != nil {
		t.Fatalf("scanSessionDirs: %v", err)
	}
	if len(scan.orphanedDirs) != 0 {
		t.Fatalf("orphanedDirs = %v, want none — the allowlist must apply to both sides", scan.orphanedDirs)
	}

	// Without the allowlist the same session is indexed and not orphaned.
	unfiltered, err := queryIndexedSessionIDs(store, nil)
	if err != nil {
		t.Fatalf("queryIndexedSessionIDs: %v", err)
	}
	if !unfiltered[sid] {
		t.Fatalf("session %s missing from the unfiltered index", sid)
	}
	scan, err = scanSessionDirs(store, store.Dir(), unfiltered, nil)
	if err != nil {
		t.Fatalf("scanSessionDirs: %v", err)
	}
	if len(scan.orphanedDirs) != 0 || len(scan.halfWritten) != 0 {
		t.Fatalf("unexpected issues: %+v", scan)
	}
}
