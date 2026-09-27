package automode

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// TestMain sandboxes the whole package's runtime writes.
//
// This is the package that leaked: NewAutoModeContext builds both
// usage.NewStore and sessions.NewStore from cli.GetWorkspaceRuntimeDir, so
// before PUPPET-332 every `go test` run here appended rows to the live
// workspace's session and usage ledgers. cli.GetWorkspaceRuntimeDir now
// neutralizes an inherited LOOM_WORKSPACE_RUNTIME_DIR under test, which makes
// this redundant — deliberately so. It is a local assertion that this package
// writes inside its own sandbox and nowhere else, and it holds even if the
// in-process guard is later changed.
//
// The env var is set directly rather than through testutil.ClearLoomEnv,
// which takes a *testing.T and so cannot be used from TestMain. Individual
// tests should still use testutil.ClearLoomEnv.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "loom-automode-runtime-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "automode TestMain: create runtime sandbox: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", filepath.Clean(dir)); err != nil {
		fmt.Fprintf(os.Stderr, "automode TestMain: set runtime dir: %v\n", err)
		os.Exit(1)
	}
	cli.ResetWorkspaceRuntimeDirCache()

	code := m.Run()

	cli.ResetWorkspaceRuntimeDirCache()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
