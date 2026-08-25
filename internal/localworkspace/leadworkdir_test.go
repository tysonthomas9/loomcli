package localworkspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
)

// rememberWorkspacePath seeds the state cache with a workspace root, which is
// what LeadWorkdir derives <ws>/lead from.
func rememberWorkspacePath(t *testing.T, wsKey, path string) {
	t.Helper()
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.Workspaces[wsKey] = bootstrap.WorkspaceLocalState{Path: path}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}
}

func TestLeadWorkdirUsesWorkspacePath(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(EnvLeadWorkdir, "")
	wsPath := t.TempDir()
	rememberWorkspacePath(t, "E2E", wsPath)

	got, ok := LeadWorkdir("E2E")
	if !ok {
		t.Fatal("LeadWorkdir: ok = false, want true for a workspace with a local path")
	}
	if want := filepath.Join(wsPath, LeadDirName); got != want {
		t.Fatalf("LeadWorkdir = %q, want %q", got, want)
	}
}

func TestLeadWorkdirEnvOverrideWinsOverWorkspace(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	wsPath := t.TempDir()
	rememberWorkspacePath(t, "E2E", wsPath)
	override := filepath.Join(t.TempDir(), "elsewhere")
	t.Setenv(EnvLeadWorkdir, override)

	got, ok := LeadWorkdir("E2E")
	if !ok || got != override {
		t.Fatalf("LeadWorkdir = (%q, %v), want (%q, true)", got, ok, override)
	}
}

// A relative override is ambiguous - it would resolve differently for the
// terminal launch and for the WebUI server - so it is ignored and the
// workspace-derived directory still wins.
func TestLeadWorkdirIgnoresRelativeOverride(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	wsPath := t.TempDir()
	rememberWorkspacePath(t, "E2E", wsPath)
	t.Setenv(EnvLeadWorkdir, "relative/lead")

	got, ok := LeadWorkdir("E2E")
	if want := filepath.Join(wsPath, LeadDirName); !ok || got != want {
		t.Fatalf("LeadWorkdir = (%q, %v), want (%q, true)", got, ok, want)
	}
}

func TestLeadWorkdirUnresolvable(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(EnvLeadWorkdir, "")
	rememberWorkspacePath(t, "E2E", t.TempDir())

	cases := map[string]string{
		"empty key":         "",
		"whitespace key":    "   ",
		"unknown workspace": "NOPE",
	}
	for name, wsKey := range cases {
		t.Run(name, func(t *testing.T) {
			if got, ok := LeadWorkdir(wsKey); ok || got != "" {
				t.Fatalf("LeadWorkdir(%q) = (%q, %v), want (\"\", false)", wsKey, got, ok)
			}
		})
	}
}

// A workspace known to fleet-db but with no local path recorded has nowhere to
// put a lead directory, so the caller keeps its own fallback.
func TestLeadWorkdirWorkspaceWithoutLocalPath(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(EnvLeadWorkdir, "")
	if err := RememberAgentWorktree("E2E", "nova", filepath.Join(t.TempDir(), "wt")); err != nil {
		t.Fatalf("remember worktree: %v", err)
	}

	if got, ok := LeadWorkdir("E2E"); ok || got != "" {
		t.Fatalf("LeadWorkdir = (%q, %v), want (\"\", false)", got, ok)
	}
}

func TestEnsureLeadWorkdirCreatesDirectory(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(EnvLeadWorkdir, "")
	wsPath := t.TempDir()
	rememberWorkspacePath(t, "E2E", wsPath)

	dir, ok := EnsureLeadWorkdir("E2E")
	if !ok {
		t.Fatal("EnsureLeadWorkdir: ok = false, want true")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("lead workdir not created at %q: %v", dir, err)
	}
	// Idempotent: a second call on an existing directory still succeeds.
	if again, okAgain := EnsureLeadWorkdir("E2E"); !okAgain || again != dir {
		t.Fatalf("EnsureLeadWorkdir (2nd) = (%q, %v), want (%q, true)", again, okAgain, dir)
	}
}

func TestEnsureLeadWorkdirUnresolvable(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(EnvLeadWorkdir, "")

	if dir, ok := EnsureLeadWorkdir("NOPE"); ok || dir != "" {
		t.Fatalf("EnsureLeadWorkdir = (%q, %v), want (\"\", false)", dir, ok)
	}
}
