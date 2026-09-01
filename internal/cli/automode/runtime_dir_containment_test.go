package automode

import (
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/testutil"
)

// TestInitAutoLoop_DoesNotWriteInheritedRuntimeDir is the regression test for
// PUPPET-332. initAutoLoop builds usage.NewStore and sessions.NewStore from
// cli.GetWorkspaceRuntimeDir, and both create directories eagerly. Before the
// fix, a LOOM_WORKSPACE_RUNTIME_DIR inherited from the launching agent shell
// was honored under `go test`, so this call appended to the live workspace's
// ledgers. Here the canary stands in for that workspace: it is present in the
// environment at "process start" (simulated through the inherited-value seam)
// and must come out of the call untouched.
func TestInitAutoLoop_DoesNotWriteInheritedRuntimeDir(t *testing.T) {
	testutil.ClearLoomEnv(t)

	canary := t.TempDir()
	restore := cli.SetInheritedRuntimeDirForTest(canary)
	t.Cleanup(restore)

	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", canary)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

	t.Chdir(t.TempDir()) // the "." fallback must not land in the package dir either

	loop := initAutoLoop(AutoModeOptions{
		AgentName:    "containment-test",
		WorktreePath: t.TempDir(),
		EventBus:     events.NopBus{},
		CustomPromptGen: func(string, *config.WorkspaceConfig) string {
			return "unused"
		},
	})
	if loop == nil {
		t.Fatal("initAutoLoop returned nil")
	}

	entries, err := os.ReadDir(canary)
	if err != nil {
		t.Fatalf("read canary dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("inherited runtime dir was written to: %v", names)
	}
}
