package monitor

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/testdata/clitest"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func commandWithContext(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	return cmd
}

type CommandResult = cli.CommandResult
type LockInfo = cli.LockInfo
type MockExecRunner = clitest.MockExecRunner
type MockWorkItems = clitest.MockWorkItems
type MockGitRunner = clitest.MockGitRunner
type MockFileSystem = clitest.MockFileSystem
type ExecBridgeGitRunner = clitest.ExecBridgeGitRunner

var ResetWorkspaceRuntimeDirCache = cli.ResetWorkspaceRuntimeDirCache
var resetDefaultWorkItems = cli.ResetDefaultWorkItems
var setDefaultWorkItems = cli.SetDefaultWorkItems

func NewTestDeps(t *testing.T) (*cli.Deps, *clitest.MockGitRunner, *clitest.MockExecRunner, *clitest.MockFileSystem, *clitest.MockWorkItems) {
	return clitest.NewTestDeps(t)
}

func NewMockWorkItems() *clitest.MockWorkItems { return clitest.NewMockWorkItems() }

func listResultFromSummaries(summaries []workitems.IssueSummary) *workitems.ListResult {
	items := make([]workitems.ListItem, len(summaries))
	for index := range summaries {
		items[index] = workitems.ListItem{IssueSummary: summaries[index]}
	}
	return &workitems.ListResult{Issues: items}
}

func installExecMock(t *testing.T, m *clitest.MockExecRunner) {
	t.Helper()
	dd := cli.TestingGetDefaultDeps()
	origExec := dd.Exec
	origGit := dd.Git
	dd.Exec = m
	dd.Git = &clitest.ExecBridgeGitRunner{Exec: m}
	t.Cleanup(func() {
		dd.Exec = origExec
		dd.Git = origGit
	})
}

func setupMonitorWorkspaceConfig(t *testing.T, workspaceDir string, agentNames ...string) {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv("LOOM_FLEET_DB_ACTOR", "test")
	// Production resolveActiveWorkspaceName requires LOOM_WORKSPACE (or
	// --workspace) to pick the active workspace.
	t.Setenv("LOOM_WORKSPACE", "TEST")
	config.InvalidateConfigCache()
	cli.ResetWorkspaceRuntimeDirCache()
	oldResolver := cli.TestingResetDefaultResolver()

	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() {
		_ = st.Close()
		cli.TestingSetDefaultResolver(oldResolver)
		config.InvalidateConfigCache()
		cli.ResetWorkspaceRuntimeDirCache()
	})

	const workspaceKey = "TEST"
	const repoName = "repo"
	t.Setenv("LOOM_WORKSPACE", workspaceKey)
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:           workspaceKey,
		Name:          "test",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  workspaceKey,
		Name:          repoName,
		DefaultBranch: "main",
		SourceRepoID:  repoName,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	agentNames = normalizeMonitorAgentNames(t, workspaceDir, agentNames)
	agents := make(map[string]bootstrap.AgentLocalState, len(agentNames))
	for _, name := range agentNames {
		agents[name] = bootstrap.AgentLocalState{
			Worktree: filepath.Join(workspaceDir, "worktrees", name),
		}
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.LastWorkspace = workspaceKey
		sc.Workspaces[workspaceKey] = bootstrap.WorkspaceLocalState{
			Path:   workspaceDir,
			Repos:  map[string]string{repoName: filepath.Join(workspaceDir, repoName)},
			Agents: agents,
		}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}
	if _, err := config.TestingPrimeConfigCacheFromStore(ctx, st); err != nil {
		t.Fatalf("prime config cache: %v", err)
	}
	cli.ResetWorkspaceRuntimeDirCache()
	cli.TestingResetDefaultResolver()
}

func normalizeMonitorAgentNames(t *testing.T, workspaceDir string, agentNames []string) []string {
	t.Helper()
	if len(agentNames) > 0 {
		out := append([]string(nil), agentNames...)
		sort.Strings(out)
		return out
	}
	entries, err := os.ReadDir(filepath.Join(workspaceDir, "worktrees"))
	if err != nil {
		t.Fatalf("read worktrees dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			agentNames = append(agentNames, entry.Name())
		}
	}
	sort.Strings(agentNames)
	return agentNames
}

// defaultResolver is the package-level Resolver for backward-compat tests.
var defaultResolver *cli.Resolver

// collectMonitorData delegates to CollectMonitorData.
func collectMonitorData(ctx context.Context, readyLimit int, branch string) *MonitorData {
	return CollectMonitorData(ctx, readyLimit, branch)
}

// execBridgeGitRunner wraps clitest.ExecBridgeGitRunner for lowercase compat.
type execBridgeGitRunner = clitest.ExecBridgeGitRunner

// getWorktreeGitSyncStatus is a lowercase alias for GetWorktreeGitSyncStatus.
var getWorktreeGitSyncStatus = GetWorktreeGitSyncStatus

var collectReadyTasksByPriority = CollectReadyTasksByPriority
var displayWidth = DisplayWidth

// --- Command stubs for monitor tests ---

type CommandStub struct {
	Dir    string
	Name   string
	Args   []string
	Stdout string
	Stderr string
	Err    error
}

type CommandMock struct {
	t     *testing.T
	stubs []CommandStub
	calls []CommandStub
	idx   int
}

func NewCommandMock(t *testing.T, stubs []CommandStub) *CommandMock {
	return &CommandMock{t: t, stubs: stubs}
}

func (m *CommandMock) Run(dir, name string, args ...string) cli.CommandResult {
	call := CommandStub{Dir: dir, Name: name, Args: args}
	m.calls = append(m.calls, call)
	if m.idx >= len(m.stubs) {
		m.t.Fatalf("unexpected command call #%d: %s %v in %s", m.idx+1, name, args, dir)
	}
	stub := m.stubs[m.idx]
	m.idx++
	return cli.CommandResult{Stdout: stub.Stdout, Stderr: stub.Stderr, Err: stub.Err}
}

func (m *CommandMock) Verify() {
	if m.idx != len(m.stubs) {
		m.t.Errorf("expected %d command calls, got %d", len(m.stubs), m.idx)
	}
}

func (m *CommandMock) InstallOn(deps *cli.Deps) {
	deps.Exec = m
	deps.Git = &clitest.ExecBridgeGitRunner{Exec: m}
	m.t.Cleanup(func() { m.Verify() })
}

var truncateToWidth = TruncateToWidth
var padRight = PadRight
var centerText = CenterText
var renderBoxTop = RenderBoxTop
var renderBoxBottom = RenderBoxBottom
var renderBoxSeparator = RenderBoxSeparator
var renderBoxLine = RenderBoxLine
