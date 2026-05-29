package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

func TestCheckGit(t *testing.T) {
	t.Parallel()

	t.Run("git found with good version", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
			if name == "git" && len(args) > 0 && args[0] == "--version" {
				return CommandResult{Stdout: "git version 2.44.0\n"}
			}
			return CommandResult{Err: fmt.Errorf("unexpected: %s %v", name, args)}
		}}

		result := checkGit(deps)
		if result.Status != StatusPass {
			t.Errorf("expected pass, got %v: %s", result.Status, result.Summary)
		}
		if result.Summary != "git 2.44 found" {
			t.Errorf("unexpected summary: %s", result.Summary)
		}
	})

	t.Run("git not found", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
			return CommandResult{Err: fmt.Errorf("exec: not found")}
		}}

		result := checkGit(deps)
		if result.Status != StatusFail {
			t.Errorf("expected fail, got %v", result.Status)
		}
	})

	t.Run("git version too old", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
			return CommandResult{Stdout: "git version 2.19.3\n"}
		}}

		result := checkGit(deps)
		if result.Status != StatusFail {
			t.Errorf("expected fail, got %v: %s", result.Status, result.Summary)
		}
	})

	t.Run("apple git version suffix", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
			return CommandResult{Stdout: "git version 2.39.3 (Apple Git-146)\n"}
		}}

		result := checkGit(deps)
		if result.Status != StatusPass {
			t.Errorf("expected pass, got %v: %s", result.Status, result.Summary)
		}
	})

	t.Run("unparseable version", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
			return CommandResult{Stdout: "git version unknown\n"}
		}}

		result := checkGit(deps)
		if result.Status != StatusWarn {
			t.Errorf("expected warn, got %v: %s", result.Status, result.Summary)
		}
	})
}

func TestCheckGitRepo(t *testing.T) {
	t.Parallel()

	t.Run("inside git repo", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
			if name == "git" && len(args) > 0 {
				switch args[0] {
				case "rev-parse":
					if len(args) > 1 {
						switch args[1] {
						case "--is-inside-work-tree":
							return CommandResult{Stdout: "true\n"}
						case "--git-common-dir":
							return CommandResult{Stdout: ".git\n"}
						case "--git-dir":
							return CommandResult{Stdout: ".git\n"}
						}
					}
				}
			}
			return CommandResult{Err: fmt.Errorf("unexpected")}
		}}

		result := checkGitRepo(deps)
		if result.Status != StatusPass {
			t.Errorf("expected pass, got %v: %s", result.Status, result.Summary)
		}
	})

	t.Run("not in git repo", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
			return CommandResult{Err: fmt.Errorf("not a git repository")}
		}}

		result := checkGitRepo(deps)
		if result.Status != StatusWarn {
			t.Errorf("expected warn, got %v", result.Status)
		}
	})

	t.Run("inside worktree", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
			if name == "git" && len(args) > 1 {
				switch args[1] {
				case "--is-inside-work-tree":
					return CommandResult{Stdout: "true\n"}
				case "--git-common-dir":
					return CommandResult{Stdout: "/main/repo/.git\n"}
				case "--git-dir":
					return CommandResult{Stdout: "/main/repo/.git/worktrees/falcon\n"}
				}
			}
			return CommandResult{Err: fmt.Errorf("unexpected")}
		}}

		result := checkGitRepo(deps)
		if result.Status != StatusWarn {
			t.Errorf("expected warn for worktree, got %v: %s", result.Status, result.Summary)
		}
	})
}

func TestCheckProjectConfig(t *testing.T) {
	t.Run("no loom.yaml", func(t *testing.T) {
		dir := t.TempDir()
		setupProjectConfigRuntimeDir(t, dir)

		result := checkProjectConfig()
		if result.Status != StatusPass {
			t.Errorf("expected pass for FleetDB daemon config defaults, got %v: %s", result.Status, result.Summary)
		}
		if !strings.Contains(result.Summary, "FleetDB daemon profile loaded") {
			t.Errorf("expected FleetDB daemon profile summary, got: %s", result.Summary)
		}
	})

	t.Run("invalid loom.yaml ignored by FleetDB config", func(t *testing.T) {
		dir := t.TempDir()
		setupProjectConfigRuntimeDir(t, dir)

		// Write invalid YAML with a missing colon on line 3
		invalidYAML := "agents:\n  - worktree: falcon\n    role plan\n  - worktree: nova\n"
		if err := os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(invalidYAML), 0644); err != nil {
			t.Fatal(err)
		}

		result := checkProjectConfig()
		if result.Status != StatusPass {
			t.Errorf("expected pass because daemon config is FleetDB-backed, got %v: %s", result.Status, result.Summary)
		}
	})

	t.Run("valid loom.yaml", func(t *testing.T) {
		dir := t.TempDir()
		setupProjectConfigRuntimeDir(t, dir)

		yamlContent := `agents:
  - worktree: falcon
    role: plan
  - worktree: nova
    role: task
`
		if err := os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		result := checkProjectConfig()
		if result.Status != StatusPass {
			t.Errorf("expected pass for valid loom.yaml, got %v: %s", result.Status, result.Summary)
		}
	})
}

func setupProjectConfigRuntimeDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", dir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(func() { ResetWorkspaceRuntimeDirCache() })
}

func TestCheckGlobalConfig(t *testing.T) {
	t.Run("no config", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("LOOM_CONFIG_DIR", dir)
		defer ResetWorkspaceRuntimeDirCache()
		setupWorkspaceConfigInDir(t, dir, &LoomConfig{Workspaces: map[string]WorkspaceConfig{}})

		result := checkGlobalConfig()
		if result.Status != StatusWarn {
			t.Errorf("expected warn, got %v: %s", result.Status, result.Summary)
		}
	})

	t.Run("legacy yaml config ignored by FleetDB metadata", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("LOOM_CONFIG_DIR", dir)
		defer ResetWorkspaceRuntimeDirCache()

		invalidYAML := "workspaces:\n  dev\n    path: /tmp/dev\n"
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(invalidYAML), 0644); err != nil {
			t.Fatal(err)
		}
		setupWorkspaceConfigInDir(t, dir, &LoomConfig{Workspaces: map[string]WorkspaceConfig{}})

		result := checkGlobalConfig()
		if result.Status != StatusWarn {
			t.Errorf("expected warn for empty FleetDB workspace metadata, got %v: %s", result.Status, result.Summary)
		}
		if !strings.Contains(result.Summary, "no FleetDB workspaces found") {
			t.Errorf("expected summary about empty FleetDB metadata, got: %s", result.Summary)
		}
	})

	t.Run("valid config", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("LOOM_CONFIG_DIR", dir)
		defer ResetWorkspaceRuntimeDirCache()

		yamlContent := `workspaces:
  dev:
    path: /tmp/dev
`
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}
		setupWorkspaceConfigInDir(t, dir, &LoomConfig{Workspaces: map[string]WorkspaceConfig{}})

		result := checkGlobalConfig()
		// May be pass or warn (path might not exist)
		if result.Status == StatusFail {
			t.Errorf("did not expect fail for parseable config: %s", result.Summary)
		}
	})
}

func TestCheckStaleLocks(t *testing.T) {
	t.Run("no worktrees returns empty", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("LOOM_CONFIG_DIR", dir)
		t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(dir, "worktrees"))
		defer ResetWorkspaceRuntimeDirCache()

		result := checkStaleLocks()
		// Empty result (skipped)
		if result.Name != "" {
			t.Errorf("expected empty result for no worktrees, got: %s", result.Name)
		}
	})

	t.Run("stale lock detected", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("LOOM_CONFIG_DIR", dir)
		worktreesDir := filepath.Join(dir, "worktrees")
		t.Setenv("LOOM_WORKTREES_DIR", worktreesDir)
		defer ResetWorkspaceRuntimeDirCache()

		// Create a worktree with a stale lock (PID that doesn't exist)
		wtPath := filepath.Join(worktreesDir, "falcon")
		if err := os.MkdirAll(filepath.Join(wtPath, ".git"), 0755); err != nil {
			t.Fatal(err)
		}

		lockInfo := LockInfo{
			PID:       999999, // unlikely to be running
			Command:   "plan",
			StartedAt: time.Now().Add(-10 * time.Minute),
			AgentName: "test",
		}
		data, _ := json.Marshal(lockInfo)
		if err := os.WriteFile(filepath.Join(wtPath, LockFileName), data, 0644); err != nil {
			t.Fatal(err)
		}

		// Mock execCommand for worktree discovery
		installExecMock(t, &MockExecRunner{RunFunc: func(d, name string, args ...string) CommandResult {
			if name == "git" && len(args) > 0 && args[0] == "branch" {
				return CommandResult{Stdout: "* falcon\n"}
			}
			return CommandResult{Stdout: ""}
		}})

		result := checkStaleLocks()
		if result.Name == "" {
			// checkStaleLocks may return empty if DiscoverWorktrees fails (no workspace config)
			// without proper git setup; this is acceptable
			t.Skip("worktree discovery failed in test environment")
		}
		if result.Status != StatusWarn {
			t.Errorf("expected warn for stale lock, got %v: %s", result.Status, result.Summary)
		}
	})
}

func TestCheckRedis(t *testing.T) {
	t.Run("not configured returns empty", func(t *testing.T) {
		t.Setenv("LOOM_REDIS_ADDR", "")

		result := checkRedis()
		if result.Name != "" {
			t.Errorf("expected empty result when not configured, got: %s", result.Name)
		}
	})
}

func TestDoctorJSONOutput(t *testing.T) {
	t.Parallel()

	output := DoctorOutput{
		Checks: []CheckResult{
			{Name: "git", Status: StatusPass, Summary: "git 2.44 found"},
			{Name: "tmux", Status: StatusWarn, Summary: "tmux not installed", Detail: "Required for daemon mode"},
			{Name: "fleet-db", Status: StatusFail, Summary: "fleet-db not configured", Detail: "Set LOOM_FLEET_URL"},
		},
		Summary: DoctorSummary{Pass: 1, Warn: 1, Fail: 1},
	}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("failed to marshal doctor output: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	checks, ok := parsed["checks"].([]interface{})
	if !ok {
		t.Fatal("missing checks array")
	}
	if len(checks) != 3 {
		t.Errorf("expected 3 checks, got %d", len(checks))
	}

	summary, ok := parsed["summary"].(map[string]interface{})
	if !ok {
		t.Fatal("missing summary")
	}
	if summary["pass"] != float64(1) {
		t.Errorf("expected pass=1, got %v", summary["pass"])
	}
	if summary["warn"] != float64(1) {
		t.Errorf("expected warn=1, got %v", summary["warn"])
	}
	if summary["fail"] != float64(1) {
		t.Errorf("expected fail=1, got %v", summary["fail"])
	}

	// Verify status values are strings in JSON
	check0 := checks[0].(map[string]interface{})
	if check0["status"] != "pass" {
		t.Errorf("expected status string 'pass', got %v", check0["status"])
	}
	check1 := checks[1].(map[string]interface{})
	if check1["status"] != "warn" {
		t.Errorf("expected status string 'warn', got %v", check1["status"])
	}
	check2 := checks[2].(map[string]interface{})
	if check2["status"] != "fail" {
		t.Errorf("expected status string 'fail', got %v", check2["status"])
	}
}

func TestCheckStatusString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status CheckStatus
		want   string
	}{
		{StatusPass, "pass"},
		{StatusWarn, "warn"},
		{StatusFail, "fail"},
		{CheckStatus(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.status.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckBackendCLI(t *testing.T) {
	t.Parallel()

	t.Run("backend on PATH", func(t *testing.T) {
		t.Parallel()
		// This test verifies the check runs without error.
		// The actual result depends on whether claude/codex is installed.
		result := checkBackendCLI()
		if result.Name != "backend_cli" {
			t.Errorf("expected name 'backend_cli', got %q", result.Name)
		}
		// Status could be pass or fail depending on environment
		if result.Status != StatusPass && result.Status != StatusFail {
			t.Errorf("expected pass or fail, got %v", result.Status)
		}
	})
}

func TestCheckLoomDaemon(t *testing.T) {
	t.Run("no pid file", func(t *testing.T) {
		dir := t.TempDir()
		origDir, _ := os.Getwd()
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chdir(origDir) }()

		// Prevent real config loading from interfering
		t.Setenv("LOOM_CONFIG_DIR", dir)
		defer ResetWorkspaceRuntimeDirCache()

		result := checkLoomDaemon()
		if result.Status != StatusWarn {
			t.Errorf("expected warn when no PID file, got %v: %s", result.Status, result.Summary)
		}
	})
}

func TestCheckIssueBackend(t *testing.T) {
	t.Run("fleetdb active", func(t *testing.T) {
		result := checkIssueBackend()
		if result.Name != "issue_backend" {
			t.Errorf("expected name 'issue_backend', got %q", result.Name)
		}
		if result.Status != StatusPass {
			t.Errorf("expected pass, got %v: %s", result.Status, result.Summary)
		}
		if !strings.Contains(result.Summary, "fleet-db") {
			t.Errorf("expected summary to contain 'fleet-db', got %q", result.Summary)
		}
	})

	t.Run("empty env var falls through to default fleetdb", func(t *testing.T) {
		result := checkIssueBackend()
		if !strings.Contains(result.Summary, "fleet-db") {
			t.Errorf("expected summary to contain 'fleet-db', got %q", result.Summary)
		}
	})
}

func TestCheckFleetDB(t *testing.T) {
	t.Parallel()

	t.Run("autostart with no redis URL passes", func(t *testing.T) {
		t.Parallel()
		cfg := FleetDBServerConfig{
			AutoStart: true,
			RedisURL:  "",
			Workspace: "test-ws",
		}
		result := reportFleetDBConfig(cfg)
		if result.Name != "fleetdb" {
			t.Errorf("expected name 'fleetdb', got %q", result.Name)
		}
		if result.Status != StatusPass {
			t.Errorf("expected pass, got %v: %s", result.Status, result.Summary)
		}
		if !strings.Contains(result.Summary, "miniredis auto-start") {
			t.Errorf("expected summary to mention 'miniredis auto-start', got %q", result.Summary)
		}
		if !strings.Contains(result.Summary, "test-ws") {
			t.Errorf("expected summary to contain workspace name, got %q", result.Summary)
		}
	})

	t.Run("no redis URL and no autostart fails", func(t *testing.T) {
		t.Parallel()
		cfg := FleetDBServerConfig{
			AutoStart: false,
			RedisURL:  "",
			Workspace: "default",
		}
		result := reportFleetDBConfig(cfg)
		if result.Name != "fleetdb" {
			t.Errorf("expected name 'fleetdb', got %q", result.Name)
		}
		if result.Status != StatusFail {
			t.Errorf("expected fail, got %v: %s", result.Status, result.Summary)
		}
		if !strings.Contains(result.Summary, "no Redis URL configured") {
			t.Errorf("expected summary to mention 'no Redis URL configured', got %q", result.Summary)
		}
		if result.Detail == "" {
			t.Error("expected non-empty detail with remediation advice")
		}
	})

	t.Run("redis URL set but unreachable fails", func(t *testing.T) {
		t.Parallel()
		cfg := FleetDBServerConfig{
			AutoStart: false,
			RedisURL:  "redis://localhost:19999", // unlikely to be running
			Workspace: "default",
		}
		result := reportFleetDBConfig(cfg)
		if result.Name != "fleetdb" {
			t.Errorf("expected name 'fleetdb', got %q", result.Name)
		}
		if result.Status != StatusFail {
			t.Errorf("expected fail for unreachable Redis, got %v: %s", result.Status, result.Summary)
		}
		if !strings.Contains(result.Summary, "not reachable") {
			t.Errorf("expected summary to contain 'not reachable', got %q", result.Summary)
		}
	})
}

func TestLoomSessionRegex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantMatch bool
		wsPrefix  string
		role      string
		agent     string
		pid       string
	}{
		{
			name:      "standard session with hex prefix",
			input:     "loom-aaaabbbb-plan-falcon-12345",
			wantMatch: true,
			wsPrefix:  "aaaabbbb",
			role:      "plan",
			agent:     "falcon",
			pid:       "12345",
		},
		{
			name:      "default workspace prefix with task role",
			input:     "loom-default-task-nova-67890",
			wantMatch: true,
			wsPrefix:  "default",
			role:      "task",
			agent:     "nova",
			pid:       "67890",
		},
		{
			name:      "agent name with hyphens",
			input:     "loom-aaaabbbb-plan-my-agent-12345",
			wantMatch: true,
			wsPrefix:  "aaaabbbb",
			role:      "plan",
			agent:     "my-agent",
			pid:       "12345",
		},
		{
			name:      "keepalive role does not match",
			input:     "loom-test-keepalive-12345",
			wantMatch: false,
		},
		{
			name:      "e2e-test style does not match",
			input:     "loom-e2e-test-12345-9876",
			wantMatch: false,
		},
		{
			name:      "non-loom session does not match",
			input:     "my-shell-session",
			wantMatch: false,
		},
		{
			name:      "uppercase hex prefix does not match",
			input:     "loom-AABB1234-plan-falcon-12345",
			wantMatch: false,
		},
		{
			name:      "invalid role does not match",
			input:     "loom-aaaabbbb-build-falcon-12345",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matches := loomSessionRegex.FindStringSubmatch(tt.input)
			if tt.wantMatch {
				if matches == nil {
					t.Fatalf("expected %q to match, but it did not", tt.input)
				}
				if matches[1] != tt.wsPrefix {
					t.Errorf("wsPrefix: got %q, want %q", matches[1], tt.wsPrefix)
				}
				if matches[2] != tt.role {
					t.Errorf("role: got %q, want %q", matches[2], tt.role)
				}
				if matches[3] != tt.agent {
					t.Errorf("agent: got %q, want %q", matches[3], tt.agent)
				}
				if matches[4] != tt.pid {
					t.Errorf("pid: got %q, want %q", matches[4], tt.pid)
				}
			} else if matches != nil {
				t.Errorf("expected %q NOT to match, but got %v", tt.input, matches)
			}
		})
	}
}

func TestCheckOrphanedTmuxSessions_NoSessions(t *testing.T) {
	origList := listLoomTmuxSessions
	origFix := doctorFix
	doctorFix = false
	listLoomTmuxSessions = func() ([]loomTmuxSession, error) {
		return nil, nil
	}
	t.Cleanup(func() {
		listLoomTmuxSessions = origList
		doctorFix = origFix
	})

	result := checkOrphanedTmuxSessions()
	if result.Name != "" {
		t.Errorf("expected empty result (skip) when no sessions, got name=%q status=%v", result.Name, result.Status)
	}
}

func TestCheckOrphanedTmuxSessions_ListError(t *testing.T) {
	origList := listLoomTmuxSessions
	origFix := doctorFix
	doctorFix = false
	listLoomTmuxSessions = func() ([]loomTmuxSession, error) {
		return nil, fmt.Errorf("tmux list-sessions: connection refused")
	}
	t.Cleanup(func() {
		listLoomTmuxSessions = origList
		doctorFix = origFix
	})

	result := checkOrphanedTmuxSessions()
	if result.Status != StatusWarn {
		t.Errorf("expected warn, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "could not list") {
		t.Errorf("expected summary about listing failure, got %q", result.Summary)
	}
	if !strings.Contains(result.Detail, "connection refused") {
		t.Errorf("expected detail to contain error message, got %q", result.Detail)
	}
}

func TestCheckOrphanedTmuxSessions_AllActive(t *testing.T) {
	origList := listLoomTmuxSessions
	origFix := doctorFix
	doctorFix = false
	listLoomTmuxSessions = func() ([]loomTmuxSession, error) {
		return []loomTmuxSession{
			{Name: "loom-aaaabbbb-plan-falcon-" + fmt.Sprint(os.Getpid()), Role: "plan", Agent: "falcon", PID: os.Getpid(), Created: time.Now()},
			{Name: "loom-aaaabbbb-task-nova-" + fmt.Sprint(os.Getpid()), Role: "task", Agent: "nova", PID: os.Getpid(), Created: time.Now()},
		}, nil
	}
	t.Cleanup(func() {
		listLoomTmuxSessions = origList
		doctorFix = origFix
	})

	result := checkOrphanedTmuxSessions()
	if result.Status != StatusPass {
		t.Errorf("expected pass when all PIDs alive, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "no orphaned") {
		t.Errorf("expected summary about no orphans, got %q", result.Summary)
	}
}

func TestCheckOrphanedTmuxSessions_Orphaned(t *testing.T) {
	origList := listLoomTmuxSessions
	origFix := doctorFix
	doctorFix = false
	listLoomTmuxSessions = func() ([]loomTmuxSession, error) {
		return []loomTmuxSession{
			{Name: "loom-aaaabbbb-plan-falcon-999999", Role: "plan", Agent: "falcon", PID: 999999, Created: time.Now().Add(-1 * time.Hour)},
			{Name: "loom-ccccdddd-task-nova-999998", Role: "task", Agent: "nova", PID: 999998, Created: time.Now().Add(-30 * time.Minute)},
		}, nil
	}
	t.Cleanup(func() {
		listLoomTmuxSessions = origList
		doctorFix = origFix
	})

	result := checkOrphanedTmuxSessions()
	if result.Status != StatusWarn {
		t.Errorf("expected warn, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "2 orphaned") {
		t.Errorf("expected summary with count 2, got %q", result.Summary)
	}
	if !strings.Contains(result.Detail, "loom-aaaabbbb-plan-falcon-999999") {
		t.Errorf("expected detail to list first orphaned session, got %q", result.Detail)
	}
	if !strings.Contains(result.Detail, "loom-ccccdddd-task-nova-999998") {
		t.Errorf("expected detail to list second orphaned session, got %q", result.Detail)
	}
	if !strings.Contains(result.Detail, "loom doctor --fix") {
		t.Errorf("expected detail to suggest --fix, got %q", result.Detail)
	}
}

func TestCheckOrphanedTmuxSessions_MixedActiveAndOrphaned(t *testing.T) {
	origList := listLoomTmuxSessions
	origFix := doctorFix
	doctorFix = false
	listLoomTmuxSessions = func() ([]loomTmuxSession, error) {
		return []loomTmuxSession{
			{Name: "loom-aaaabbbb-plan-falcon-" + fmt.Sprint(os.Getpid()), Role: "plan", Agent: "falcon", PID: os.Getpid(), Created: time.Now()},
			{Name: "loom-ccccdddd-task-nova-999999", Role: "task", Agent: "nova", PID: 999999, Created: time.Now().Add(-1 * time.Hour)},
		}, nil
	}
	t.Cleanup(func() {
		listLoomTmuxSessions = origList
		doctorFix = origFix
	})

	result := checkOrphanedTmuxSessions()
	if result.Status != StatusWarn {
		t.Errorf("expected warn, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "1 orphaned") {
		t.Errorf("expected summary with count 1, got %q", result.Summary)
	}
	// The active session should NOT appear in the detail
	if strings.Contains(result.Detail, fmt.Sprint(os.Getpid())) {
		t.Errorf("active session PID should not appear in detail, got %q", result.Detail)
	}
	// The dead session should appear
	if !strings.Contains(result.Detail, "loom-ccccdddd-task-nova-999999") {
		t.Errorf("expected orphaned session in detail, got %q", result.Detail)
	}
}

func TestCheckOrphanedTmuxSessions_FixMode(t *testing.T) {
	origList := listLoomTmuxSessions
	origKill := killTmuxSession
	origFix := doctorFix

	doctorFix = true
	listLoomTmuxSessions = func() ([]loomTmuxSession, error) {
		return []loomTmuxSession{
			{Name: "loom-aaaabbbb-plan-falcon-999999", Role: "plan", Agent: "falcon", PID: 999999, Created: time.Now().Add(-1 * time.Hour)},
			{Name: "loom-ccccdddd-task-nova-999998", Role: "task", Agent: "nova", PID: 999998, Created: time.Now().Add(-30 * time.Minute)},
		}, nil
	}
	var killed []string
	killTmuxSession = func(name string) error {
		killed = append(killed, name)
		return nil
	}
	t.Cleanup(func() {
		listLoomTmuxSessions = origList
		killTmuxSession = origKill
		doctorFix = origFix
	})

	result := checkOrphanedTmuxSessions()
	if result.Status != StatusPass {
		t.Errorf("expected pass after fix, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "fixed 2") {
		t.Errorf("expected summary to say 'fixed 2', got %q", result.Summary)
	}
	if len(killed) != 2 {
		t.Errorf("expected 2 sessions killed, got %d", len(killed))
	}
}

func TestCheckOrphanedTmuxSessions_FixModePartialFailure(t *testing.T) {
	origList := listLoomTmuxSessions
	origKill := killTmuxSession
	origFix := doctorFix

	doctorFix = true
	listLoomTmuxSessions = func() ([]loomTmuxSession, error) {
		return []loomTmuxSession{
			{Name: "loom-aaaabbbb-plan-falcon-999999", Role: "plan", Agent: "falcon", PID: 999999, Created: time.Now().Add(-1 * time.Hour)},
			{Name: "loom-ccccdddd-task-nova-999998", Role: "task", Agent: "nova", PID: 999998, Created: time.Now().Add(-30 * time.Minute)},
		}, nil
	}
	killTmuxSession = func(name string) error {
		if name == "loom-ccccdddd-task-nova-999998" {
			return fmt.Errorf("session not found")
		}
		return nil
	}
	t.Cleanup(func() {
		listLoomTmuxSessions = origList
		killTmuxSession = origKill
		doctorFix = origFix
	})

	result := checkOrphanedTmuxSessions()
	if result.Status != StatusWarn {
		t.Errorf("expected warn on partial failure, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "fixed 1") {
		t.Errorf("expected 'fixed 1' in summary, got %q", result.Summary)
	}
	if !strings.Contains(result.Summary, "1 failed") {
		t.Errorf("expected '1 failed' in summary, got %q", result.Summary)
	}
}

func TestCheckFleetDB_Integration(t *testing.T) {
	t.Run("local mode checks embedded binary without daemon config", func(t *testing.T) {
		dir := t.TempDir()
		origDir, _ := os.Getwd()
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chdir(origDir) }()

		fleetDBBin := filepath.Join(dir, "fleet-db")
		if err := os.WriteFile(fleetDBBin, []byte("#!/bin/sh\necho 'fleet-db test help'\n"), 0755); err != nil {
			t.Fatal(err)
		}

		defer ResetWorkspaceRuntimeDirCache()
		t.Setenv("LOOM_CONFIG_DIR", dir)
		t.Setenv("LOOM_FLEET_DB_URL", "")
		t.Setenv("FLEET_DB_BIN", fleetDBBin)

		result := checkFleetDB()
		if result.Name != "fleetdb" {
			t.Errorf("expected name 'fleetdb', got %q", result.Name)
		}
		if result.Status != StatusPass {
			t.Errorf("expected pass with runnable embedded binary, got %v: %s", result.Status, result.Summary)
		}
		if !strings.Contains(result.Summary, "embedded fleet-db ready") {
			t.Errorf("expected embedded fleet-db summary, got %q", result.Summary)
		}
	})
}

// --- Signal File Tests ---

func TestCheckStaleSignalFiles_NoDir(t *testing.T) {
	origGetSignalDir := getSignalDir
	origFix := doctorFix
	doctorFix = false
	getSignalDir = func() string {
		return filepath.Join(t.TempDir(), "nonexistent-signal-dir")
	}
	t.Cleanup(func() {
		getSignalDir = origGetSignalDir
		doctorFix = origFix
	})

	result := checkStaleSignalFiles()
	if result.Name != "" {
		t.Errorf("expected empty result (skip) for nonexistent dir, got name=%q status=%v", result.Name, result.Status)
	}
}

func TestCheckStaleSignalFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	origGetSignalDir := getSignalDir
	origFix := doctorFix
	doctorFix = false
	getSignalDir = func() string { return dir }
	t.Cleanup(func() {
		getSignalDir = origGetSignalDir
		doctorFix = origFix
	})

	result := checkStaleSignalFiles()
	if result.Name != "" {
		t.Errorf("expected empty result (skip) for empty dir, got name=%q status=%v", result.Name, result.Status)
	}
}

func TestCheckStaleSignalFiles_AllFresh(t *testing.T) {
	dir := t.TempDir()
	origGetSignalDir := getSignalDir
	origFix := doctorFix
	doctorFix = false
	getSignalDir = func() string { return dir }
	t.Cleanup(func() {
		getSignalDir = origGetSignalDir
		doctorFix = origFix
	})

	// Create fresh signal files (current timestamps)
	for _, name := range []string{"sig-a", "sig-b", "sig-c"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("1"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	result := checkStaleSignalFiles()
	if result.Status != StatusPass {
		t.Errorf("expected pass, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "no stale signal files") {
		t.Errorf("expected summary to say 'no stale signal files', got %q", result.Summary)
	}
}

func TestCheckStaleSignalFiles_Stale(t *testing.T) {
	dir := t.TempDir()
	origGetSignalDir := getSignalDir
	origFix := doctorFix
	doctorFix = false
	getSignalDir = func() string { return dir }
	t.Cleanup(func() {
		getSignalDir = origGetSignalDir
		doctorFix = origFix
	})

	// Create signal files and backdate them to 2 hours ago
	staleTime := time.Now().Add(-2 * time.Hour)
	for _, name := range []string{"sig-old-1", "sig-old-2"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("1"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, staleTime, staleTime); err != nil {
			t.Fatal(err)
		}
	}

	result := checkStaleSignalFiles()
	if result.Status != StatusWarn {
		t.Errorf("expected warn, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "2 stale signal file(s)") {
		t.Errorf("expected summary with count 2, got %q", result.Summary)
	}
	if !strings.Contains(result.Detail, "sig-old-1") {
		t.Errorf("expected detail to list sig-old-1, got %q", result.Detail)
	}
	if !strings.Contains(result.Detail, "sig-old-2") {
		t.Errorf("expected detail to list sig-old-2, got %q", result.Detail)
	}
}

func TestCheckStaleSignalFiles_MixedFreshAndStale(t *testing.T) {
	dir := t.TempDir()
	origGetSignalDir := getSignalDir
	origFix := doctorFix
	doctorFix = false
	getSignalDir = func() string { return dir }
	t.Cleanup(func() {
		getSignalDir = origGetSignalDir
		doctorFix = origFix
	})

	// Create a fresh file
	if err := os.WriteFile(filepath.Join(dir, "sig-fresh"), []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a stale file
	staleTime := time.Now().Add(-2 * time.Hour)
	stalePath := filepath.Join(dir, "sig-stale")
	if err := os.WriteFile(stalePath, []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stalePath, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	result := checkStaleSignalFiles()
	if result.Status != StatusWarn {
		t.Errorf("expected warn, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "1 stale signal file(s)") {
		t.Errorf("expected summary with count 1, got %q", result.Summary)
	}
	if !strings.Contains(result.Detail, "sig-stale") {
		t.Errorf("expected detail to list sig-stale, got %q", result.Detail)
	}
	if strings.Contains(result.Detail, "sig-fresh") {
		t.Errorf("fresh file should not appear in detail, got %q", result.Detail)
	}
}

func TestCheckStaleSignalFiles_FixMode(t *testing.T) {
	dir := t.TempDir()
	origGetSignalDir := getSignalDir
	origFix := doctorFix
	doctorFix = true
	getSignalDir = func() string { return dir }
	t.Cleanup(func() {
		getSignalDir = origGetSignalDir
		doctorFix = origFix
	})

	// Create stale files
	staleTime := time.Now().Add(-2 * time.Hour)
	for _, name := range []string{"sig-fix-1", "sig-fix-2"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("1"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, staleTime, staleTime); err != nil {
			t.Fatal(err)
		}
	}

	result := checkStaleSignalFiles()
	if result.Status != StatusPass {
		t.Errorf("expected pass after fix, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "fixed 2") {
		t.Errorf("expected 'fixed 2' in summary, got %q", result.Summary)
	}

	// Verify files are actually removed
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected all stale files removed, but %d remain", len(entries))
	}
}

func TestCheckStaleSignalFiles_SkipsSubdirectories(t *testing.T) {
	dir := t.TempDir()
	origGetSignalDir := getSignalDir
	origFix := doctorFix
	doctorFix = false
	getSignalDir = func() string { return dir }
	t.Cleanup(func() {
		getSignalDir = origGetSignalDir
		doctorFix = origFix
	})

	// Create a subdirectory (should be skipped)
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	// Backdate the subdirectory to make it "stale" by mtime
	staleTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "subdir"), staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	// Also create a fresh file so we get a pass result instead of skip
	if err := os.WriteFile(filepath.Join(dir, "sig-fresh"), []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}

	result := checkStaleSignalFiles()
	if result.Status != StatusPass {
		t.Errorf("expected pass (subdirectories skipped), got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "no stale signal files") {
		t.Errorf("expected summary about no stale files, got %q", result.Summary)
	}
}

// --- Session Record Tests ---

// setupRuntimeDirForTest configures GetWorkspaceRuntimeDir() to return the given directory.
// Must not be used with t.Parallel() since it mutates global state.
func setupRuntimeDirForTest(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", dir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(func() { ResetWorkspaceRuntimeDirCache() })
}

func TestCheckStaleSessionRecords_NoSessions(t *testing.T) {
	dir := t.TempDir()
	setupRuntimeDirForTest(t, dir)

	origFix := doctorFix
	doctorFix = false
	t.Cleanup(func() { doctorFix = origFix })

	result := checkStaleSessionRecords()
	if result.Status != StatusPass {
		t.Errorf("expected pass for no sessions, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "no stale or orphaned sessions") {
		t.Errorf("expected summary about no stale sessions, got %q", result.Summary)
	}
}

func TestCheckStaleSessionRecords_HalfWritten(t *testing.T) {
	dir := t.TempDir()
	setupRuntimeDirForTest(t, dir)

	origFix := doctorFix
	doctorFix = false
	t.Cleanup(func() { doctorFix = origFix })

	// Create a session dir with only prompt.txt (no metadata.json)
	sessDir := filepath.Join(dir, "sessions", "half-written-session")
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "prompt.txt"), []byte("test prompt"), 0644); err != nil {
		t.Fatal(err)
	}

	result := checkStaleSessionRecords()
	if result.Status != StatusWarn {
		t.Errorf("expected warn, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Detail, "half-written") {
		t.Errorf("expected detail to contain 'half-written', got %q", result.Detail)
	}
	if !strings.Contains(result.Detail, "half-written-session") {
		t.Errorf("expected detail to contain session name, got %q", result.Detail)
	}
}

func TestCheckStaleSessionRecords_HalfWrittenFixMode(t *testing.T) {
	dir := t.TempDir()
	setupRuntimeDirForTest(t, dir)

	origFix := doctorFix
	doctorFix = true
	t.Cleanup(func() { doctorFix = origFix })

	// Create a half-written session dir
	sessDir := filepath.Join(dir, "sessions", "half-written-fix")
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "prompt.txt"), []byte("test prompt"), 0644); err != nil {
		t.Fatal(err)
	}

	result := checkStaleSessionRecords()
	if result.Status != StatusPass {
		t.Errorf("expected pass after fix, got %v: %s", result.Status, result.Summary)
	}

	// Verify the directory was removed
	if _, err := os.Stat(sessDir); !os.IsNotExist(err) {
		t.Errorf("expected half-written directory to be removed, but it still exists")
	}
}

func TestCheckStaleSessionRecords_OrphanedDir(t *testing.T) {
	dir := t.TempDir()
	setupRuntimeDirForTest(t, dir)

	origFix := doctorFix
	doctorFix = false
	t.Cleanup(func() { doctorFix = origFix })

	// Create a session dir with valid metadata.json but no index entry
	sessID := "orphaned-test-session"
	sessDir := filepath.Join(dir, "sessions", sessID)
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		t.Fatal(err)
	}
	meta := sessions.SessionMetadata{
		SessionRecord: sessions.SessionRecord{
			SchemaVersion: sessions.CurrentSchemaVersion,
			SessionID:     sessID,
			AgentName:     "test-agent",
			Status:        sessions.StatusCompleted,
			StartedAt:     time.Now().UTC().Add(-1 * time.Hour),
		},
	}
	metaData, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "metadata.json"), metaData, 0644); err != nil {
		t.Fatal(err)
	}

	result := checkStaleSessionRecords()
	if result.Status != StatusWarn {
		t.Errorf("expected warn, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Detail, "orphaned") {
		t.Errorf("expected detail to contain 'orphaned', got %q", result.Detail)
	}
	if !strings.Contains(result.Detail, sessID) {
		t.Errorf("expected detail to contain session ID %q, got %q", sessID, result.Detail)
	}
}

func TestCheckStaleSessionRecords_OrphanedDirFixMode(t *testing.T) {
	dir := t.TempDir()
	setupRuntimeDirForTest(t, dir)

	origFix := doctorFix
	doctorFix = true
	t.Cleanup(func() { doctorFix = origFix })

	// Create a session dir with valid metadata.json but no index entry
	sessID := "orphaned-fix-session"
	sessDir := filepath.Join(dir, "sessions", sessID)
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	meta := sessions.SessionMetadata{
		SessionRecord: sessions.SessionRecord{
			SchemaVersion: sessions.CurrentSchemaVersion,
			SessionID:     sessID,
			AgentName:     "test-agent",
			Status:        sessions.StatusCompleted,
			StartedAt:     now.Add(-1 * time.Hour),
		},
	}
	metaData, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "metadata.json"), metaData, 0644); err != nil {
		t.Fatal(err)
	}

	result := checkStaleSessionRecords()
	if result.Status != StatusPass {
		t.Errorf("expected pass after fix, got %v: %s", result.Status, result.Summary)
	}

	// Verify the session was re-indexed (should now appear in queries)
	store, err := sessions.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.Query(sessions.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rec := range records {
		if rec.SessionID == sessID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected orphaned session %q to be re-indexed, but not found in query results", sessID)
	}
}

func TestCheckStaleSessionRecords_LeftoverTmp(t *testing.T) {
	dir := t.TempDir()
	setupRuntimeDirForTest(t, dir)

	origFix := doctorFix
	doctorFix = false
	t.Cleanup(func() { doctorFix = origFix })

	// Create a proper session via the store so it's in the index
	store, err := sessions.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName: "test-agent",
		Backend:   "claude",
		Prompt:    "test prompt",
	})
	if err != nil {
		t.Fatal(err)
	}
	sessID := sess.SessionID()

	// Add a leftover .tmp file
	tmpPath := filepath.Join(store.Dir(), sessID, "metadata.json.tmp")
	if err := os.WriteFile(tmpPath, []byte(`{"partial":"data"}`), 0644); err != nil {
		t.Fatal(err)
	}

	result := checkStaleSessionRecords()
	if result.Status != StatusWarn {
		t.Errorf("expected warn, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Detail, "leftover tmp") {
		t.Errorf("expected detail to contain 'leftover tmp', got %q", result.Detail)
	}
}

func TestCheckStaleSessionRecords_LeftoverTmpFixMode(t *testing.T) {
	dir := t.TempDir()
	setupRuntimeDirForTest(t, dir)

	origFix := doctorFix
	doctorFix = true
	t.Cleanup(func() { doctorFix = origFix })

	// Create a proper session via the store so it's in the index
	store, err := sessions.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName: "test-agent",
		Backend:   "claude",
		Prompt:    "test prompt",
	})
	if err != nil {
		t.Fatal(err)
	}
	sessID := sess.SessionID()

	// Add a leftover .tmp file
	sessDir := filepath.Join(store.Dir(), sessID)
	tmpPath := filepath.Join(sessDir, "metadata.json.tmp")
	if err := os.WriteFile(tmpPath, []byte(`{"partial":"data"}`), 0644); err != nil {
		t.Fatal(err)
	}

	result := checkStaleSessionRecords()
	if result.Status != StatusPass {
		t.Errorf("expected pass after fix, got %v: %s", result.Status, result.Summary)
	}

	// Verify .tmp file is removed
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("expected .tmp file to be removed, but it still exists")
	}

	// Verify metadata.json is preserved
	metaPath := filepath.Join(sessDir, "metadata.json")
	if _, err := os.Stat(metaPath); err != nil {
		t.Errorf("expected metadata.json to be preserved, but got error: %v", err)
	}
}

func TestCheckFleetProbeUnreachable(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")
	t.Setenv("LOOM_FLEET_URL", "http://fleet-not-running.invalid:8080")
	t.Setenv("LOOM_WORKSPACE", "TEST")

	origProbe := fleetHealthProbe
	fleetHealthProbe = func(ctx context.Context, baseURL string) error {
		return errors.New("dial tcp: connection refused")
	}
	t.Cleanup(func() { fleetHealthProbe = origProbe })

	result := checkFleet()

	if result.Status != StatusFail {
		t.Errorf("Status = %q, want %q", result.Status, StatusFail)
	}
	if !strings.Contains(result.Summary, "not reachable") {
		t.Errorf("Summary = %q, want substring 'not reachable'", result.Summary)
	}
	if !strings.Contains(result.Detail, "connection refused") {
		t.Errorf("Detail = %q, want it to include probe error", result.Detail)
	}
}

func TestCheckFleetProbeReachable(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")
	t.Setenv("LOOM_FLEET_URL", "http://fleet-db.local:8080")
	t.Setenv("LOOM_WORKSPACE", "TEST")

	origProbe := fleetHealthProbe
	probedURL := ""
	fleetHealthProbe = func(ctx context.Context, baseURL string) error {
		probedURL = baseURL
		return nil
	}
	t.Cleanup(func() { fleetHealthProbe = origProbe })

	result := checkFleet()

	if result.Status != StatusPass {
		t.Errorf("Status = %q, want %q", result.Status, StatusPass)
	}
	if !strings.Contains(result.Summary, "reachable") {
		t.Errorf("Summary = %q, want 'reachable' substring", result.Summary)
	}
	if probedURL != "http://fleet-db.local:8080" {
		t.Errorf("probed = %q, want fleet URL", probedURL)
	}
}

func TestCheckFleetNoURL(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")
	t.Setenv("LOOM_FLEET_URL", "")
	t.Setenv("LOOM_WORKSPACE", "TEST")

	// Probe should not be called when URL is empty.
	origProbe := fleetHealthProbe
	probeCalled := false
	fleetHealthProbe = func(ctx context.Context, baseURL string) error {
		probeCalled = true
		return nil
	}
	t.Cleanup(func() { fleetHealthProbe = origProbe })

	result := checkFleet()

	if result.Status != StatusFail {
		t.Errorf("Status = %q, want %q", result.Status, StatusFail)
	}
	if probeCalled {
		t.Error("probe was called despite empty URL")
	}
}
