package supervisor

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

func TestBuildCommand_SourceReposInjected(t *testing.T) {
	tmpDir := t.TempDir()

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Repos: []cfgpkg.RepoConfig{
			{Name: "backend", SourceRepoID: "src-backend", Groups: []string{"infra"}},
			{Name: "frontend", SourceRepoID: "src-frontend"},
		},
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		EmitEvent:     func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "task", Repos: []string{"backend"}},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Backend agent"},
		WorktreePath: tmpDir,
	}

	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}
	envMap := make(map[string]string)
	for _, env := range cmd.Env {
		if idx := strings.IndexByte(env, '='); idx >= 0 {
			envMap[env[:idx]] = env[idx+1:]
		}
	}

	if v, ok := envMap["LOOM_SOURCE_REPOS"]; !ok || v != "src-backend" {
		t.Errorf("LOOM_SOURCE_REPOS = %q, want %q", v, "src-backend")
	}
}

func TestPinAgentLoomShellSurvivesLoginShellPathReset(t *testing.T) {
	runtimeRoot := t.TempDir()
	executable := filepath.Join(runtimeRoot, "Loom Agents.app", "Contents", "MacOS", "loom")
	env, err := pinAgentLoomShell(
		[]string{"PATH=/Users/test/go/bin:/usr/bin", "ZDOTDIR=/old", "KEEP=value"},
		executable,
		runtimeRoot,
		"planner",
	)
	if err != nil {
		t.Fatalf("pinAgentLoomShell: %v", err)
	}

	if got := envValue(env, "LOOM_CLI_BIN"); got != executable {
		t.Fatalf("LOOM_CLI_BIN = %q, want %q", got, executable)
	}
	if got := filepath.SplitList(envValue(env, "PATH")); len(got) == 0 || got[0] != filepath.Dir(executable) {
		t.Fatalf("PATH = %q, want packaged executable directory first", envValue(env, "PATH"))
	}
	shellHome := envValue(env, "ZDOTDIR")
	if shellHome == "" || shellHome == "/old" {
		t.Fatalf("ZDOTDIR = %q, want controlled shell home", shellHome)
	}
	startupPath := filepath.Join(shellHome, ".zprofile")
	startup, err := os.ReadFile(startupPath)
	if err != nil {
		t.Fatalf("read controlled startup: %v", err)
	}
	for _, want := range []string{
		"export LOOM_CLI_BIN=" + shellSingleQuote(executable),
		"loom() { \"$LOOM_CLI_BIN\" \"$@\"; }",
	} {
		if !strings.Contains(string(startup), want) {
			t.Fatalf("startup = %q, want %q", startup, want)
		}
	}
	if got := envValue(env, "BASH_ENV"); got != filepath.Join(shellHome, "shell-env") {
		t.Fatalf("BASH_ENV = %q, want controlled startup", got)
	}
}

func TestPinAgentLoomShellRejectsParentAgentPath(t *testing.T) {
	runtimeRoot := t.TempDir()
	executable := filepath.Join(runtimeRoot, "loom")
	env, err := pinAgentLoomShell(nil, executable, runtimeRoot, "..")
	if err != nil {
		t.Fatalf("pinAgentLoomShell: %v", err)
	}
	want := filepath.Join(runtimeRoot, ".loom", "agent-shells", "agent")
	if got := envValue(env, "ZDOTDIR"); got != want {
		t.Fatalf("ZDOTDIR = %q, want contained fallback %q", got, want)
	}
}

// TestBuildCommand_SourceReposAbsentWhenEmpty verifies LOOM_SOURCE_REPOS is not
// set when the agent has no repo affinity.
func TestBuildCommand_SourceReposAbsentWhenEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Repos: []cfgpkg.RepoConfig{
			{Name: "backend", SourceRepoID: "src-backend"},
		},
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		EmitEvent:     func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Generic agent"},
		WorktreePath: tmpDir,
	}

	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_SOURCE_REPOS=") {
			t.Error("LOOM_SOURCE_REPOS should not be set when agent has no repo affinity")
		}
	}
}

// TestBuildCommand_NoConstraints_BackwardCompat verifies no constraint env vars
// are set when role has no tool constraints.
func TestBuildCommand_NoConstraints_BackwardCompat(t *testing.T) {
	tmpDir := t.TempDir()

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent"}, // no constraints
		WorktreePath: tmpDir,
	}

	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_ALLOWED_TOOLS=") {
			t.Error("LOOM_ALLOWED_TOOLS should not be set when AllowedTools is empty")
		}
		if strings.HasPrefix(env, "LOOM_DENIED_TOOLS=") {
			t.Error("LOOM_DENIED_TOOLS should not be set when DeniedTools is empty")
		}
		if strings.HasPrefix(env, "LOOM_READ_ONLY=") {
			t.Error("LOOM_READ_ONLY should not be set when ReadOnly is false")
		}
	}
}

func TestBuildCommand_CustomBugRoleRequiresReadOnly(t *testing.T) {
	tmpDir := t.TempDir()
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}}
		},
		ProjectDir:    tmpDir,
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		EmitEvent:     func(events.Event) {},
	}

	ap := &AgentProcess{
		Entry: cfgpkg.AgentEntry{
			Worktree: "triage-agent",
			Role:     "bug-triage",
		},
		RoleConfig: cfgpkg.RoleConfig{
			PromptFile: filepath.Join(tmpDir, "bug-triage.md"),
			TaskFilter: "bug",
			ReadOnly:   false,
		},
		WorktreePath: tmpDir,
	}

	if _, err := s.buildCommand(ap); err == nil || !strings.Contains(err.Error(), "requires read_only=true") {
		t.Fatalf("writable bug role build error = %v, want read-only guard", err)
	}

	ap.RoleConfig.ReadOnly = true
	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("read-only bug role buildCommand: %v", err)
	}
	if !slices.Contains(cmd.Env, "LOOM_READ_ONLY=1") {
		t.Fatalf("read-only bug role env missing LOOM_READ_ONLY=1: %v", cmd.Env)
	}
}

// TestBuildCommand_ErrorOnUnresolvableRepos verifies buildCommand returns an error
// when the agent declares repo affinity but all groups are unknown.
func TestBuildCommand_ErrorOnUnresolvableRepos(t *testing.T) {
	tmpDir := t.TempDir()

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Repos: []cfgpkg.RepoConfig{
			{Name: "backend", SourceRepoID: "src-backend", Groups: []string{"infra"}},
		},
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		EmitEvent:     func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "task", RepoGroups: []string{"nonexistent"}},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Agent with bad group"},
		WorktreePath: tmpDir,
	}

	_, err := s.buildCommand(ap)
	if err == nil {
		t.Fatal("expected error from buildCommand when repo groups are unresolvable, got nil")
	}
	if !strings.Contains(err.Error(), "resolve agent repos") {
		t.Errorf("error = %q, want it to contain 'resolve agent repos'", err.Error())
	}
}

// TestBuildCommand_DaemonSocketEnvVar verifies LOOM_DAEMON_SOCKET is set
// when ipcSocketPath is non-empty and omitted when empty.
func TestBuildCommand_DaemonSocketEnvVar(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("ipcSocketPath set propagates LOOM_DAEMON_SOCKET", func(t *testing.T) {
		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
			ProjectDir:     tmpDir,
			IpcSocketPath:  "/tmp/test-ipc/agent-ipc.sock",
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
			RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent"},
			WorktreePath: tmpDir,
		}

		cmd, err := s.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand error: %v", err)
		}
		found := false
		for _, env := range cmd.Env {
			if env == "LOOM_DAEMON_SOCKET=/tmp/test-ipc/agent-ipc.sock" {
				found = true
			}
		}
		if !found {
			t.Error("LOOM_DAEMON_SOCKET=/tmp/test-ipc/agent-ipc.sock not found in cmd.Env")
		}
	})

	t.Run("empty ipcSocketPath omits LOOM_DAEMON_SOCKET", func(t *testing.T) {
		// Clear any inherited LOOM_DAEMON_SOCKET from parent env
		t.Setenv("LOOM_DAEMON_SOCKET", "")
		os.Unsetenv("LOOM_DAEMON_SOCKET")

		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
			ProjectDir:     tmpDir,
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "hawk", Role: "task"},
			RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in task agent"},
			WorktreePath: tmpDir,
		}

		cmd, err := s.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand error: %v", err)
		}
		for _, env := range cmd.Env {
			if strings.HasPrefix(env, "LOOM_DAEMON_SOCKET=") {
				t.Errorf("LOOM_DAEMON_SOCKET should not be in cmd.Env when ipcSocketPath is empty, got: %s", env)
			}
		}
	})
}

// TestDaemonStop_ClosesConcurrencyTracker verifies that Daemon.Stop() calls
// concurrency.Close() to unblock waiters.
