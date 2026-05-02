package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/testutil"
)

// --- Type aliases ---

type CommandResult = cli.CommandResult
type LockInfo = cli.LockInfo
type WorktreeInfo = cli.WorktreeInfo
type LoomConfig = config.LoomConfig
type RepoConfig = config.RepoConfig
type WorkspaceConfig = config.WorkspaceConfig
type AgentEntry = config.AgentEntry
type RoleConfig = config.RoleConfig
type ProjectFile = config.ProjectFile
type MockExecRunner = clitest.MockExecRunner
type MockGitRunner = clitest.MockGitRunner
type MockFileSystem = clitest.MockFileSystem
type MockIssueBackend = clitest.MockIssueBackend
type AgentStatus = monitor.AgentStatus
type MonitorData = monitor.MonitorData
type MonitorStats = monitor.MonitorStats
type SyncInfo = monitor.SyncInfo
type TaskInfo = monitor.TaskInfo
type Deps = cli.Deps

var (
	LoadConfig           = config.LoadConfig
	SaveConfig           = config.SaveConfig
	ResetBeadsDirCache   = cli.ResetBeadsDirCache
	CurrentConfigVersion = config.CurrentConfigVersion
	WithDeps             = cli.WithDeps
	GetWorkspaceDir      = config.GetWorkspaceDir
	GetWorktreesDir      = cli.GetWorktreesDir
)

func NewTestDeps(t *testing.T) (*cli.Deps, *clitest.MockGitRunner, *clitest.MockExecRunner, *clitest.MockFileSystem, *clitest.MockIssueBackend) {
	return clitest.NewTestDeps(t)
}

func NewMockIssueBackend() *clitest.MockIssueBackend { return clitest.NewMockIssueBackend() }

func slicesEqual(a, b []string) bool { return clitest.SlicesEqual(a, b) }

func MockStdin(t *testing.T, input string) { testutil.MockStdin(t, input) }

func useLegacyBeadsBackend(t *testing.T) {
	t.Helper()
	t.Skip("legacy beads backend removed; workspace commands now require fleet-db store fixtures")
}

// --- Command stubs ---

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

func (m *CommandMock) Install() {
	m.InstallOn(cli.TestingGetDefaultDeps())
}

func (m *CommandMock) Calls() []CommandStub { return m.calls }

// --- Other helpers ---

func createGitRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "test"},
		{"git", "commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...) //nolint:gosec //nolint:norawexec
		cmd.Dir = path
		cmd.Env = clitest.GitSafeEnv(
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
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

type DaemonAgentStateEntry = monitor.DaemonAgentStateEntry
type DaemonAgentInfo = monitor.DaemonAgentInfo
type DaemonAgentState = monitor.DaemonAgentState

// DaemonState is defined locally to avoid importing daemon (which would create
// an import cycle: workspace_test → daemon → daemon/supervisor → workspace).
type DaemonState struct {
	PID       int                 `json:"pid"`
	StartedAt time.Time           `json:"started_at"`
	Agents    []DaemonAgentStatus `json:"agents"`
}

// DaemonAgentStatus mirrors daemon.DaemonAgentStatus for test serialization.
type DaemonAgentStatus struct {
	Worktree       string    `json:"worktree"`
	Role           string    `json:"role"`
	Repo           string    `json:"repo,omitempty"`
	PID            int       `json:"pid"`
	Status         string    `json:"status"`
	TaskID         string    `json:"task_id,omitempty"`
	EpicID         string    `json:"epic_id,omitempty"`
	CurrentBackend string    `json:"current_backend,omitempty"`
	RestartCount   int       `json:"restart_count"`
	LastStart      time.Time `json:"last_start,omitempty"`
	LastExit       time.Time `json:"last_exit,omitempty"`
	LastExitCode   int       `json:"last_exit_code,omitempty"`
	StopReason     string    `json:"stop_reason,omitempty"`
	StoppedAt      time.Time `json:"stopped_at,omitempty"`
	WorktreePath   string    `json:"worktree_path,omitempty"`
	LastErrorClass string    `json:"last_error_class,omitempty"`
	NoWorkCount    int       `json:"no_work_count,omitempty"`
	BackoffUntil   time.Time `json:"backoff_until,omitempty"`
	RemoteBranch   string    `json:"remote_branch,omitempty"`
}

// backendFlagPtr points to the real cli.backendFlag for test manipulation.
var backendFlagPtr = cli.TestingBackendFlag()
