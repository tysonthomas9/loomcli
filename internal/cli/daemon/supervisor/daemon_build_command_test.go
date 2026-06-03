package supervisor

import (
	"os"
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

// TestBuildCommand_FlueSandboxInjected verifies that the daemon profile's
// flue_sandbox setting is propagated to spawned agents as LOOM_FLUE_SANDBOX, so
// UI-created planner/task agents run in a Daytona sandbox per task.
func TestBuildCommand_FlueSandboxInjected(t *testing.T) {
	tmpDir := t.TempDir()
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{FlueSandbox: "daytona"}}
		},
		ProjectDir:    tmpDir,
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		EmitEvent:     func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "nova", Role: "task"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Task agent"},
		WorktreePath: tmpDir,
	}

	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}
	got := ""
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_FLUE_SANDBOX=") {
			got = strings.TrimPrefix(env, "LOOM_FLUE_SANDBOX=")
		}
	}
	if got != "daytona" {
		t.Errorf("LOOM_FLUE_SANDBOX = %q, want daytona", got)
	}
}

// TestBuildCommand_FlueSandboxAbsentWhenUnset verifies LOOM_FLUE_SANDBOX is not
// injected when the daemon profile leaves it empty (default = local).
func TestBuildCommand_FlueSandboxAbsentWhenUnset(t *testing.T) {
	tmpDir := t.TempDir()
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "nova", Role: "task"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Task agent"},
		WorktreePath: tmpDir,
	}

	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_FLUE_SANDBOX=") {
			t.Errorf("LOOM_FLUE_SANDBOX should not be set when unset; got %q", env)
		}
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
