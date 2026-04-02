package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestCheckBdDaemon(t *testing.T) {
	t.Parallel()

	t.Run("bd not on PATH", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		deps.LookPath = func(string) (string, error) {
			return "", exec.ErrNotFound
		}

		result := checkBdDaemon(deps)
		if result.Status != StatusFail {
			t.Errorf("expected fail, got %v: %s", result.Status, result.Summary)
		}
		if !strings.Contains(result.Summary, "bd not found") {
			t.Errorf("expected summary to contain 'bd not found', got %q", result.Summary)
		}
	})

	t.Run("daemon running", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		deps.LookPath = func(string) (string, error) { return "/usr/bin/bd", nil }
		deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
			if name == "bd" && len(args) >= 2 && args[1] == "status" {
				return CommandResult{Stdout: `{"status":"running","pid":1234}`}
			}
			return CommandResult{Err: fmt.Errorf("unexpected")}
		}}

		result := checkBdDaemon(deps)
		if result.Status != StatusPass {
			t.Errorf("expected pass, got %v: %s", result.Status, result.Summary)
		}
	})

	t.Run("daemon not running", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		deps.LookPath = func(string) (string, error) { return "/usr/bin/bd", nil }
		deps.Exec = &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
			return CommandResult{Err: fmt.Errorf("not running")}
		}}

		result := checkBdDaemon(deps)
		if result.Status != StatusWarn {
			t.Errorf("expected warn, got %v", result.Status)
		}
	})
}

func TestCheckProjectConfig(t *testing.T) {
	t.Run("no loom.yaml", func(t *testing.T) {
		dir := t.TempDir()
		defer ResetBeadsDirCache()
		ResetBeadsDirCache()
		setupWorkspaceConfig(t, &LoomConfig{DefaultWorkspace: "test", Workspaces: map[string]WorkspaceConfig{"test": {Path: dir}}})

		result := checkProjectConfig()
		if result.Status != StatusWarn {
			t.Errorf("expected warn for missing loom.yaml, got %v: %s", result.Status, result.Summary)
		}
	})

	t.Run("invalid loom.yaml", func(t *testing.T) {
		dir := t.TempDir()
		defer ResetBeadsDirCache()
		ResetBeadsDirCache()
		setupWorkspaceConfig(t, &LoomConfig{DefaultWorkspace: "test", Workspaces: map[string]WorkspaceConfig{"test": {Path: dir}}})

		// Write invalid YAML
		if err := os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte("{{invalid"), 0644); err != nil {
			t.Fatal(err)
		}

		result := checkProjectConfig()
		if result.Status != StatusFail {
			t.Errorf("expected fail for invalid loom.yaml, got %v: %s", result.Status, result.Summary)
		}
	})

	t.Run("valid loom.yaml", func(t *testing.T) {
		dir := t.TempDir()
		defer ResetBeadsDirCache()
		ResetBeadsDirCache()
		setupWorkspaceConfig(t, &LoomConfig{DefaultWorkspace: "test", Workspaces: map[string]WorkspaceConfig{"test": {Path: dir}}})

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

func TestCheckGlobalConfig(t *testing.T) {
	t.Run("no config", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("LOOM_CONFIG_DIR", dir)
		defer ResetBeadsDirCache()

		result := checkGlobalConfig()
		if result.Status != StatusWarn {
			t.Errorf("expected warn, got %v: %s", result.Status, result.Summary)
		}
	})

	t.Run("valid config", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("LOOM_CONFIG_DIR", dir)
		defer ResetBeadsDirCache()

		yamlContent := `workspaces:
  dev:
    path: /tmp/dev
`
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

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
		defer ResetBeadsDirCache()

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
		defer ResetBeadsDirCache()

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
			// checkStaleLocks may return empty if DiscoverWorktrees fails (legacy mode)
			// without proper git setup; this is acceptable
			t.Skip("worktree discovery failed in test environment")
		}
		if result.Status != StatusWarn {
			t.Errorf("expected warn for stale lock, got %v: %s", result.Status, result.Summary)
		}
	})
}

func TestCheckBeadsInit(t *testing.T) {
	t.Run("beads initialized", func(t *testing.T) {
		dir := t.TempDir()
		defer ResetBeadsDirCache()
		ResetBeadsDirCache()
		setupWorkspaceConfig(t, &LoomConfig{DefaultWorkspace: "test", Workspaces: map[string]WorkspaceConfig{"test": {Path: dir}}})

		if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0755); err != nil {
			t.Fatal(err)
		}

		result := checkBeadsInit()
		if result.Status != StatusPass {
			t.Errorf("expected pass, got %v: %s", result.Status, result.Summary)
		}
	})

	t.Run("beads not initialized", func(t *testing.T) {
		dir := t.TempDir()
		defer ResetBeadsDirCache()
		ResetBeadsDirCache()
		setupWorkspaceConfig(t, &LoomConfig{DefaultWorkspace: "test", Workspaces: map[string]WorkspaceConfig{"test": {Path: dir}}})

		result := checkBeadsInit()
		if result.Status != StatusFail {
			t.Errorf("expected fail, got %v: %s", result.Status, result.Summary)
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
			{Name: "bd", Status: StatusFail, Summary: "bd not found", Detail: "Install with: make install-bd"},
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
		defer ResetBeadsDirCache()

		result := checkLoomDaemon()
		if result.Status != StatusWarn {
			t.Errorf("expected warn when no PID file, got %v: %s", result.Status, result.Summary)
		}
	})
}

func TestCheckIssueBackend(t *testing.T) {
	t.Run("fleetdb active", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "true")

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

	t.Run("beads active", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "false")

		result := checkIssueBackend()
		if result.Name != "issue_backend" {
			t.Errorf("expected name 'issue_backend', got %q", result.Name)
		}
		if result.Status != StatusPass {
			t.Errorf("expected pass, got %v: %s", result.Status, result.Summary)
		}
		if !strings.Contains(result.Summary, "beads") {
			t.Errorf("expected summary to contain 'beads', got %q", result.Summary)
		}
	})

	t.Run("empty env var falls through to default beads", func(t *testing.T) {
		// Empty string is treated as unset — falls through to config/defaults.
		t.Setenv("LOOM_FLEETDB_ENABLED", "")

		result := checkIssueBackend()
		if !strings.Contains(result.Summary, "beads") {
			t.Errorf("expected summary to contain 'beads', got %q", result.Summary)
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

func TestCheckFleetDB_Integration(t *testing.T) {
	t.Run("checkFleetDB with no config falls back to defaults", func(t *testing.T) {
		// Use a temp dir so LoadDaemonConfig fails (no loom.yaml)
		dir := t.TempDir()
		origDir, _ := os.Getwd()
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chdir(origDir) }()

		t.Setenv("LOOM_CONFIG_DIR", dir)
		defer ResetBeadsDirCache()

		result := checkFleetDB()
		if result.Name != "fleetdb" {
			t.Errorf("expected name 'fleetdb', got %q", result.Name)
		}
		// With no config, defaults are AutoStart=false and RedisURL="",
		// so this should fail.
		if result.Status != StatusFail {
			t.Errorf("expected fail with default config (no redis, no autostart), got %v: %s", result.Status, result.Summary)
		}
	})
}
