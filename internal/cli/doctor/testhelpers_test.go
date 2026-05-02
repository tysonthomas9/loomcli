package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

type CommandResult = cli.CommandResult
type LockInfo = cli.LockInfo
type LoomConfig = config.LoomConfig
type WorkspaceConfig = config.WorkspaceConfig
type FleetDBServerConfig = config.FleetDBServerConfig
type MockExecRunner = clitest.MockExecRunner
type MockIssueBackend = clitest.MockIssueBackend

const LockFileName = cli.LockFileName

var ResetWorkspaceRuntimeDirCache = cli.ResetWorkspaceRuntimeDirCache

func NewTestDeps(t *testing.T) (*cli.Deps, *clitest.MockGitRunner, *clitest.MockExecRunner, *clitest.MockFileSystem, *clitest.MockIssueBackend) {
	return clitest.NewTestDeps(t)
}

func installExecMock(t *testing.T, m *clitest.MockExecRunner) {
	t.Helper()
	dd := cli.TestingGetDefaultDeps()
	orig := dd.Exec
	dd.Exec = m
	t.Cleanup(func() { dd.Exec = orig })
}

func setupWorkspaceConfig(t *testing.T, cfg *config.LoomConfig) {
	t.Helper()
	configDir := t.TempDir()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_CONFIG_DIR", configDir)
}
