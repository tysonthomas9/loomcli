package backends

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

// TestRealDaytonaRunner_EndToEnd is the proposal's Phase 2 E2E (gated): it runs
// the *real* runner against a real Daytona sandbox — building the managed flue
// project on first use, creating a sandbox, hydrating a public repo, running an
// agent turn, capturing the remote patch, and syncing it back into a local
// worktree. It asserts both the synced file and the recorded sandbox metadata.
//
// Skipped unless LOOM_FLUE_REAL_DAYTONA=1 and DAYTONA_API_KEY are set, because
// it provisions a remote sandbox and calls a model provider (cost + network).
// Uses a small public repo so the sandbox can clone without credentials.
//
//	LOOM_FLUE_REAL_DAYTONA=1 DAYTONA_API_KEY=... go test ./internal/cli/backends/ \
//	    -run TestRealDaytonaRunner_EndToEnd -count=1 -v -timeout 15m
func TestRealDaytonaRunner_EndToEnd(t *testing.T) {
	if os.Getenv("LOOM_FLUE_REAL_DAYTONA") == "" {
		t.Skip("set LOOM_FLUE_REAL_DAYTONA=1 (+ DAYTONA_API_KEY) to run the real Daytona e2e")
	}
	if os.Getenv("DAYTONA_API_KEY") == "" {
		t.Skip("DAYTONA_API_KEY required for the real Daytona e2e")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Cleanup(ClearLastRuntimeMetadata)

	// Clone a small public repo locally so runFlueDaytonaTask derives a public
	// origin the remote sandbox can clone without credentials.
	repo := t.TempDir()
	gitT(t, repo, "clone", "--depth", "1", "https://github.com/octocat/Hello-World.git", ".")

	baseHead := gitT(t, repo, "rev-parse", "HEAD")
	col := usage.NewCollector("flue", "novareal")
	prompt := "Create a new file named LOOM_OK.txt whose entire contents are the text: daytona ok"
	if err := runFlueDaytonaTask(repo, prompt, "novareal", nil, col); err != nil {
		t.Fatalf("runFlueDaytonaTask (real Daytona): %v", err)
	}

	// The agent's change was patch-synced back into the local worktree.
	data, err := os.ReadFile(filepath.Join(repo, "LOOM_OK.txt"))
	if err != nil {
		t.Fatalf("expected LOOM_OK.txt synced back from the sandbox: %v", err)
	}
	t.Logf("synced LOOM_OK.txt: %q", string(data))

	// "Back to loom": the work was committed locally (push to octocat/Hello-World
	// is expected to fail for lack of write access — that's best-effort).
	if head := gitT(t, repo, "rev-parse", "HEAD"); head == baseHead {
		t.Error("daytona work was not committed back into the local worktree")
	}

	// Sandbox metadata was recorded, and the sandbox was deleted on success.
	rt := GetLastRuntimeMetadata()
	if rt == nil || rt.SandboxID == "" || rt.Provider != "daytona" {
		t.Fatalf("runtime metadata not recorded: %+v", rt)
	}
	if rt.Cleanup != "deleted" {
		t.Errorf("sandbox cleanup = %q, want deleted (sandbox should be removed on success)", rt.Cleanup)
	}
	t.Logf("sandbox metadata: provider=%s id=%s cwd=%s base=%s sync=%s cleanup=%s",
		rt.Provider, rt.SandboxID, rt.RemoteCwd, rt.BaseRef, rt.SyncStrategy, rt.Cleanup)
}
