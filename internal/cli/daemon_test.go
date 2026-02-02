package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// requireIntPtrD is a test helper to check pointer int values in daemon tests.
func requireIntPtrD(t *testing.T, name string, got *int, want int) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %d", name, want)
	}
	if *got != want {
		t.Errorf("%s = %d, want %d", name, *got, want)
	}
}

// makeDaemonConfig creates a DaemonConfig with defaults for testing.
func makeDaemonConfig(agents []AgentEntry, roles map[string]RoleConfig) *DaemonConfig {
	return &DaemonConfig{
		Daemon: DaemonSettings{
			PIDFile: ".loom/daemon.pid",
			LogDir:  ".loom/logs",
			RestartPolicy: RestartPolicy{
				MaxRetries:     intPtr(3),
				BackoffInitial: intPtr(2),
				BackoffMax:     intPtr(300),
			},
		},
		Roles:  roles,
		Agents: agents,
	}
}

func TestNewDaemon(t *testing.T) {
	t.Run("valid config creates daemon with correct number of agents", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Create worktree directories with .git
		wt1 := filepath.Join(tmpDir, "worktrees", "falcon")
		wt2 := filepath.Join(tmpDir, "worktrees", "nova")
		if err := os.MkdirAll(filepath.Join(wt1, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(wt2, ".git"), 0755); err != nil {
			t.Fatal(err)
		}

		// Set env to use our temp worktrees dir
		t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir()) // Use empty config dir

		config := makeDaemonConfig(
			[]AgentEntry{
				{Worktree: "falcon", Role: "plan"},
				{Worktree: "nova", Role: "task"},
			},
			nil,
		)

		daemon, err := NewDaemon(config, tmpDir)
		if err != nil {
			t.Fatalf("NewDaemon() error = %v", err)
		}
		if daemon == nil {
			t.Fatal("NewDaemon() returned nil daemon")
		}
		if daemon.AgentCount() != 2 {
			t.Errorf("AgentCount() = %d, want 2", daemon.AgentCount())
		}
	})

	t.Run("nil config returns error", func(t *testing.T) {
		_, err := NewDaemon(nil, "/tmp")
		if err == nil {
			t.Fatal("expected error for nil config")
		}
		if !strings.Contains(err.Error(), "daemon config is nil") {
			t.Errorf("error = %q, want contains 'daemon config is nil'", err.Error())
		}
	})

	t.Run("empty agents list returns error", func(t *testing.T) {
		config := makeDaemonConfig([]AgentEntry{}, nil)

		_, err := NewDaemon(config, "/tmp")
		if err == nil {
			t.Fatal("expected error for empty agents")
		}
		if !strings.Contains(err.Error(), "no agents configured") {
			t.Errorf("error = %q, want contains 'no agents configured'", err.Error())
		}
	})

	t.Run("unknown custom role name returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		wt := filepath.Join(tmpDir, "worktrees", "falcon")
		if err := os.MkdirAll(filepath.Join(wt, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

		config := makeDaemonConfig(
			[]AgentEntry{
				{Worktree: "falcon", Role: "nonexistent_role"},
			},
			nil, // no custom roles defined
		)

		_, err := NewDaemon(config, tmpDir)
		if err == nil {
			t.Fatal("expected error for unknown role")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("error = %q, want contains 'not found'", err.Error())
		}
	})

	t.Run("custom role missing prompt_file returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		wt := filepath.Join(tmpDir, "worktrees", "falcon")
		if err := os.MkdirAll(filepath.Join(wt, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

		config := makeDaemonConfig(
			[]AgentEntry{
				{Worktree: "falcon", Role: "reviewer"},
			},
			map[string]RoleConfig{
				"reviewer": {Description: "Code reviewer"}, // missing prompt_file
			},
		)

		_, err := NewDaemon(config, tmpDir)
		if err == nil {
			t.Fatal("expected error for missing prompt_file")
		}
		if !strings.Contains(err.Error(), "missing prompt_file") {
			t.Errorf("error = %q, want contains 'missing prompt_file'", err.Error())
		}
	})

	t.Run("custom role with non-existent prompt file returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		wt := filepath.Join(tmpDir, "worktrees", "falcon")
		if err := os.MkdirAll(filepath.Join(wt, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

		config := makeDaemonConfig(
			[]AgentEntry{
				{Worktree: "falcon", Role: "reviewer"},
			},
			map[string]RoleConfig{
				"reviewer": {
					Description: "Code reviewer",
					PromptFile:  "prompts/nonexistent.md",
				},
			},
		)

		_, err := NewDaemon(config, tmpDir)
		if err == nil {
			t.Fatal("expected error for non-existent prompt file")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("error = %q, want contains 'not found'", err.Error())
		}
	})

	t.Run("built-in roles plan and task are accepted without custom config", func(t *testing.T) {
		tmpDir := t.TempDir()
		wt1 := filepath.Join(tmpDir, "worktrees", "falcon")
		wt2 := filepath.Join(tmpDir, "worktrees", "nova")
		if err := os.MkdirAll(filepath.Join(wt1, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(wt2, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

		config := makeDaemonConfig(
			[]AgentEntry{
				{Worktree: "falcon", Role: "plan"},
				{Worktree: "nova", Role: "task"},
			},
			nil, // no custom roles - built-in should work
		)

		daemon, err := NewDaemon(config, tmpDir)
		if err != nil {
			t.Fatalf("NewDaemon() error = %v", err)
		}
		if daemon.AgentCount() != 2 {
			t.Errorf("AgentCount() = %d, want 2", daemon.AgentCount())
		}

		// Verify agents are properly configured
		agents := daemon.Agents()
		for _, agent := range agents {
			if agent.Role != "plan" && agent.Role != "task" {
				t.Errorf("agent %s has unexpected role %q", agent.Worktree, agent.Role)
			}
		}
	})

	t.Run("custom role with valid prompt file works", func(t *testing.T) {
		tmpDir := t.TempDir()
		wt := filepath.Join(tmpDir, "worktrees", "falcon")
		if err := os.MkdirAll(filepath.Join(wt, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

		// Create a valid prompt file
		promptsDir := filepath.Join(tmpDir, "prompts")
		if err := os.MkdirAll(promptsDir, 0755); err != nil {
			t.Fatal(err)
		}
		promptFile := filepath.Join(promptsDir, "reviewer.md")
		if err := os.WriteFile(promptFile, []byte("You are a code reviewer."), 0644); err != nil {
			t.Fatal(err)
		}

		config := makeDaemonConfig(
			[]AgentEntry{
				{Worktree: "falcon", Role: "reviewer"},
			},
			map[string]RoleConfig{
				"reviewer": {
					Description: "Code reviewer",
					PromptFile:  "prompts/reviewer.md",
				},
			},
		)

		daemon, err := NewDaemon(config, tmpDir)
		if err != nil {
			t.Fatalf("NewDaemon() error = %v", err)
		}
		if daemon.AgentCount() != 1 {
			t.Errorf("AgentCount() = %d, want 1", daemon.AgentCount())
		}

		// Verify agent status is available
		agents := daemon.Agents()
		if agents[0].Worktree != "falcon" {
			t.Errorf("Worktree = %q, want %q", agents[0].Worktree, "falcon")
		}
		if agents[0].Role != "reviewer" {
			t.Errorf("Role = %q, want %q", agents[0].Role, "reviewer")
		}
	})
}

func TestComputeBackoff(t *testing.T) {
	// Create a daemon with known config values
	config := makeDaemonConfig(
		[]AgentEntry{{Worktree: "test", Role: "plan"}},
		nil,
	)

	// Override restart policy for predictable testing
	config.Daemon.RestartPolicy.BackoffInitial = intPtr(2)
	config.Daemon.RestartPolicy.BackoffMax = intPtr(300)

	daemon := &Daemon{config: config}

	t.Run("restartCount=0 returns initial backoff", func(t *testing.T) {
		ap := &AgentProcess{restartCount: 0}
		backoff := daemon.computeBackoff(ap)

		// initial * 2^0 = 2 * 1 = 2s
		want := 2 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("restartCount=1 returns 4s", func(t *testing.T) {
		ap := &AgentProcess{restartCount: 1}
		backoff := daemon.computeBackoff(ap)

		// initial * 2^1 = 2 * 2 = 4s
		want := 4 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("restartCount=5 returns 64s", func(t *testing.T) {
		ap := &AgentProcess{restartCount: 5}
		backoff := daemon.computeBackoff(ap)

		// initial * 2^5 = 2 * 32 = 64s
		want := 64 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("large restartCount is capped at BackoffMax", func(t *testing.T) {
		ap := &AgentProcess{restartCount: 20}
		backoff := daemon.computeBackoff(ap)

		// 2 * 2^20 = 2097152s, should be capped at 300s
		want := 300 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("restartCount=7 exceeds max and is capped", func(t *testing.T) {
		ap := &AgentProcess{restartCount: 7}
		backoff := daemon.computeBackoff(ap)

		// 2 * 2^7 = 256s, which is < 300s, so not capped
		want := 256 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("restartCount=8 exceeds max and is capped", func(t *testing.T) {
		ap := &AgentProcess{restartCount: 8}
		backoff := daemon.computeBackoff(ap)

		// 2 * 2^8 = 512s > 300s, should be capped
		want := 300 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})
}

func TestShouldRestart(t *testing.T) {
	t.Run("successful run (exit 0, long runtime) resets counter and returns true", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(3)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			restartCount: 2,              // had previous restarts
			lastExitCode: 0,              // successful exit
			lastStart:    time.Now().Add(-2 * time.Minute), // ran for >1 minute
		}

		result := daemon.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true for successful long run")
		}
		if ap.restartCount != 0 {
			t.Errorf("restartCount = %d, want 0 (should be reset)", ap.restartCount)
		}
	})

	t.Run("successful short run does not reset counter", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(3)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			restartCount: 1,
			lastExitCode: 0,                               // successful exit
			lastStart:    time.Now().Add(-30 * time.Second), // ran for <1 minute
		}

		result := daemon.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true")
		}
		// Counter should be incremented since run was short
		if ap.restartCount != 2 {
			t.Errorf("restartCount = %d, want 2 (should be incremented)", ap.restartCount)
		}
	})

	t.Run("failed run (non-zero exit) increments counter and returns true if under limit", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(3)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			restartCount: 1,
			lastExitCode: 1, // non-zero exit
			lastStart:    time.Now().Add(-2 * time.Minute),
		}

		result := daemon.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true")
		}
		if ap.restartCount != 2 {
			t.Errorf("restartCount = %d, want 2", ap.restartCount)
		}
	})

	t.Run("counter exceeds maxRetries returns false", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(3)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			restartCount: 3, // at limit
			lastExitCode: 1, // non-zero exit
			lastStart:    time.Now().Add(-2 * time.Minute),
		}

		result := daemon.shouldRestart(ap)
		// After increment, count becomes 4 which exceeds maxRetries of 3
		if result {
			t.Error("shouldRestart() = true, want false (counter exceeds max)")
		}
		if ap.restartCount != 4 {
			t.Errorf("restartCount = %d, want 4", ap.restartCount)
		}
	})

	t.Run("counter at exactly maxRetries still allows restart", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(3)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			restartCount: 2, // one below limit
			lastExitCode: 1, // non-zero exit
			lastStart:    time.Now().Add(-2 * time.Minute),
		}

		result := daemon.shouldRestart(ap)
		// After increment, count becomes 3 which equals maxRetries
		if !result {
			t.Error("shouldRestart() = false, want true (counter at max)")
		}
		if ap.restartCount != 3 {
			t.Errorf("restartCount = %d, want 3", ap.restartCount)
		}
	})

	t.Run("maxRetries=0 means no retries allowed", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(0)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			restartCount: 0,
			lastExitCode: 1, // non-zero exit
			lastStart:    time.Now().Add(-2 * time.Minute),
		}

		result := daemon.shouldRestart(ap)
		// After increment, count becomes 1 which exceeds maxRetries of 0
		if result {
			t.Error("shouldRestart() = true, want false (maxRetries=0)")
		}
	})
}

func TestResolveRoleConfig(t *testing.T) {
	t.Run("built-in role plan returns valid config", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config, projectDir: "/tmp"}

		rc, err := daemon.resolveRoleConfig("plan", 0)
		if err != nil {
			t.Fatalf("resolveRoleConfig(plan) error = %v", err)
		}
		if rc.Description == "" {
			t.Error("Description is empty, want non-empty for built-in role")
		}
		if !strings.Contains(rc.Description, "plan") {
			t.Errorf("Description = %q, want contains 'plan'", rc.Description)
		}
	})

	t.Run("built-in role task returns valid config", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config, projectDir: "/tmp"}

		rc, err := daemon.resolveRoleConfig("task", 0)
		if err != nil {
			t.Fatalf("resolveRoleConfig(task) error = %v", err)
		}
		if rc.Description == "" {
			t.Error("Description is empty, want non-empty for built-in role")
		}
		if !strings.Contains(rc.Description, "task") {
			t.Errorf("Description = %q, want contains 'task'", rc.Description)
		}
	})

	t.Run("custom role with valid prompt_file works", func(t *testing.T) {
		tmpDir := t.TempDir()
		promptFile := filepath.Join(tmpDir, "prompt.md")
		if err := os.WriteFile(promptFile, []byte("test prompt"), 0644); err != nil {
			t.Fatal(err)
		}

		config := makeDaemonConfig(
			nil,
			map[string]RoleConfig{
				"reviewer": {
					Description: "Code reviewer",
					PromptFile:  "prompt.md",
					TaskFilter:  "review",
				},
			},
		)
		daemon := &Daemon{config: config, projectDir: tmpDir}

		rc, err := daemon.resolveRoleConfig("reviewer", 0)
		if err != nil {
			t.Fatalf("resolveRoleConfig(reviewer) error = %v", err)
		}
		if rc.Description != "Code reviewer" {
			t.Errorf("Description = %q, want %q", rc.Description, "Code reviewer")
		}
		if rc.TaskFilter != "review" {
			t.Errorf("TaskFilter = %q, want %q", rc.TaskFilter, "review")
		}
		// PromptFile should be resolved to absolute path
		if !filepath.IsAbs(rc.PromptFile) {
			t.Errorf("PromptFile = %q, want absolute path", rc.PromptFile)
		}
		if rc.PromptFile != promptFile {
			t.Errorf("PromptFile = %q, want %q", rc.PromptFile, promptFile)
		}
	})

	t.Run("custom role in config without prompt_file returns error", func(t *testing.T) {
		config := makeDaemonConfig(
			nil,
			map[string]RoleConfig{
				"reviewer": {
					Description: "Code reviewer",
					// missing PromptFile
				},
			},
		)
		daemon := &Daemon{config: config, projectDir: "/tmp"}

		_, err := daemon.resolveRoleConfig("reviewer", 0)
		if err == nil {
			t.Fatal("expected error for missing prompt_file")
		}
		if !strings.Contains(err.Error(), "missing prompt_file") {
			t.Errorf("error = %q, want contains 'missing prompt_file'", err.Error())
		}
	})

	t.Run("custom role not found in config returns error", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil) // no custom roles
		daemon := &Daemon{config: config, projectDir: "/tmp"}

		_, err := daemon.resolveRoleConfig("unknown_role", 0)
		if err == nil {
			t.Fatal("expected error for unknown role")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("error = %q, want contains 'not found'", err.Error())
		}
	})

	t.Run("custom role with non-existent prompt file returns error", func(t *testing.T) {
		tmpDir := t.TempDir()

		config := makeDaemonConfig(
			nil,
			map[string]RoleConfig{
				"reviewer": {
					Description: "Code reviewer",
					PromptFile:  "nonexistent.md",
				},
			},
		)
		daemon := &Daemon{config: config, projectDir: tmpDir}

		_, err := daemon.resolveRoleConfig("reviewer", 0)
		if err == nil {
			t.Fatal("expected error for non-existent prompt file")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("error = %q, want contains 'not found'", err.Error())
		}
	})

	t.Run("custom role with absolute prompt path works", func(t *testing.T) {
		tmpDir := t.TempDir()
		promptFile := filepath.Join(tmpDir, "prompt.md")
		if err := os.WriteFile(promptFile, []byte("test prompt"), 0644); err != nil {
			t.Fatal(err)
		}

		config := makeDaemonConfig(
			nil,
			map[string]RoleConfig{
				"reviewer": {
					Description: "Code reviewer",
					PromptFile:  promptFile, // absolute path
				},
			},
		)
		daemon := &Daemon{config: config, projectDir: "/different/dir"}

		rc, err := daemon.resolveRoleConfig("reviewer", 0)
		if err != nil {
			t.Fatalf("resolveRoleConfig(reviewer) error = %v", err)
		}
		if rc.PromptFile != promptFile {
			t.Errorf("PromptFile = %q, want %q", rc.PromptFile, promptFile)
		}
	})
}

func TestBuiltInRoles(t *testing.T) {
	t.Run("plan is a built-in role", func(t *testing.T) {
		if !builtInRoles["plan"] {
			t.Error("builtInRoles[plan] = false, want true")
		}
	})

	t.Run("task is a built-in role", func(t *testing.T) {
		if !builtInRoles["task"] {
			t.Error("builtInRoles[task] = false, want true")
		}
	})

	t.Run("unknown role is not built-in", func(t *testing.T) {
		if builtInRoles["reviewer"] {
			t.Error("builtInRoles[reviewer] = true, want false")
		}
	})
}

func TestAgentProcess(t *testing.T) {
	t.Run("initial state has zero values", func(t *testing.T) {
		ap := &AgentProcess{
			entry: AgentEntry{Worktree: "test", Role: "plan"},
		}

		if ap.restartCount != 0 {
			t.Errorf("restartCount = %d, want 0", ap.restartCount)
		}
		if ap.pid != 0 {
			t.Errorf("pid = %d, want 0", ap.pid)
		}
		if ap.lastExitCode != 0 {
			t.Errorf("lastExitCode = %d, want 0", ap.lastExitCode)
		}
		if ap.cmd != nil {
			t.Error("cmd != nil, want nil")
		}
	})
}

func TestDaemonAgents(t *testing.T) {
	t.Run("Agents returns copy of agent list", func(t *testing.T) {
		tmpDir := t.TempDir()
		wt := filepath.Join(tmpDir, "worktrees", "falcon")
		if err := os.MkdirAll(filepath.Join(wt, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "falcon", Role: "plan"}},
			nil,
		)

		daemon, err := NewDaemon(config, tmpDir)
		if err != nil {
			t.Fatalf("NewDaemon() error = %v", err)
		}

		agents := daemon.Agents()
		if len(agents) != 1 {
			t.Fatalf("len(Agents()) = %d, want 1", len(agents))
		}
		if agents[0].Worktree != "falcon" {
			t.Errorf("agent.Worktree = %q, want %q", agents[0].Worktree, "falcon")
		}
		if agents[0].Role != "plan" {
			t.Errorf("agent.Role = %q, want %q", agents[0].Role, "plan")
		}
	})
}
