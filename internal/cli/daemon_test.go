package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
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

		daemon, err := NewDaemon(config, tmpDir, nil)
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
		_, err := NewDaemon(nil, "/tmp", nil)
		if err == nil {
			t.Fatal("expected error for nil config")
		}
		if !strings.Contains(err.Error(), "daemon config is nil") {
			t.Errorf("error = %q, want contains 'daemon config is nil'", err.Error())
		}
	})

	t.Run("empty agents list returns error", func(t *testing.T) {
		config := makeDaemonConfig([]AgentEntry{}, nil)

		_, err := NewDaemon(config, "/tmp", nil)
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

		_, err := NewDaemon(config, tmpDir, nil)
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

		_, err := NewDaemon(config, tmpDir, nil)
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

		_, err := NewDaemon(config, tmpDir, nil)
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

		daemon, err := NewDaemon(config, tmpDir, nil)
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

		daemon, err := NewDaemon(config, tmpDir, nil)
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

	t.Run("rate limited uses rate_limit_backoff as initial", func(t *testing.T) {
		ap := &AgentProcess{
			rateRetryCount: 0,
			lastError:      &agenterr.AgentError{Class: agenterr.RateLimited},
		}
		backoff := daemon.computeBackoff(ap)

		// default rate_limit_backoff=30 * 2^0 = 30s
		want := 30 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("rate limited respects RetryAfter hint", func(t *testing.T) {
		ap := &AgentProcess{
			rateRetryCount: 0,
			lastError: &agenterr.AgentError{
				Class:      agenterr.RateLimited,
				RetryAfter: 60 * time.Second,
			},
		}
		backoff := daemon.computeBackoff(ap)

		// RetryAfter (60s) > computed (30s), so use RetryAfter
		want := 60 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("rate limited RetryAfter capped at rate_limit_max_wait", func(t *testing.T) {
		ap := &AgentProcess{
			rateRetryCount: 0,
			lastError: &agenterr.AgentError{
				Class:      agenterr.RateLimited,
				RetryAfter: 600 * time.Second, // 10 minutes
			},
		}
		backoff := daemon.computeBackoff(ap)

		// Default rate_limit_max_wait=300s, so cap at 300s
		want := 300 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("rate limited exponential growth", func(t *testing.T) {
		ap := &AgentProcess{
			rateRetryCount: 2,
			lastError:      &agenterr.AgentError{Class: agenterr.RateLimited},
		}
		backoff := daemon.computeBackoff(ap)

		// 30 * 2^2 = 120s
		want := 120 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("timeout uses timeout_backoff as initial", func(t *testing.T) {
		ap := &AgentProcess{
			restartCount: 0,
			lastError:    &agenterr.AgentError{Class: agenterr.Timeout},
		}
		backoff := daemon.computeBackoff(ap)

		// default timeout_backoff=5 * 2^0 = 5s
		want := 5 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("transient uses standard backoff_initial", func(t *testing.T) {
		ap := &AgentProcess{
			restartCount: 0,
			lastError:    &agenterr.AgentError{Class: agenterr.Transient},
		}
		backoff := daemon.computeBackoff(ap)

		// default backoff_initial=2 * 2^0 = 2s
		want := 2 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("nil lastError uses standard backoff", func(t *testing.T) {
		ap := &AgentProcess{
			restartCount: 1,
			lastError:    nil,
		}
		backoff := daemon.computeBackoff(ap)

		// standard: 2 * 2^1 = 4s
		want := 4 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("NoWork uses fixed no_work_backoff", func(t *testing.T) {
		ap := &AgentProcess{
			restartCount: 5, // should be irrelevant
			lastError:    &agenterr.AgentError{Class: agenterr.NoWork},
		}
		backoff := daemon.computeBackoff(ap)

		// default no_work_backoff=30s (fixed, no exponential)
		want := 30 * time.Second
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
			restartCount: 2,                                // had previous restarts
			lastExitCode: 0,                                // successful exit
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

	t.Run("successful short run resets counter", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(3)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			restartCount: 1,
			lastExitCode: 0,                                 // successful exit
			lastStart:    time.Now().Add(-30 * time.Second), // ran for <1 minute
			lastError:    nil,                               // clean success
		}

		result := daemon.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true")
		}
		// Counter should be reset — clean success always resets regardless of runtime
		if ap.restartCount != 0 {
			t.Errorf("restartCount = %d, want 0 (should be reset)", ap.restartCount)
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

	t.Run("fatal error (AuthFailure) stops immediately", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(3)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "test"},
			restartCount: 0,
			lastExitCode: 1,
			lastStart:    time.Now().Add(-2 * time.Minute),
			lastError:    &agenterr.AgentError{Class: agenterr.AuthFailure, Message: "invalid API key"},
		}

		result := daemon.shouldRestart(ap)
		if result {
			t.Error("shouldRestart() = true, want false for AuthFailure (fatal)")
		}
		if ap.restartCount != 0 {
			t.Errorf("restartCount = %d, want 0 (should not be incremented for fatal)", ap.restartCount)
		}
	})

	t.Run("fatal error (BillingError) stops immediately", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(3)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "test"},
			restartCount: 0,
			lastExitCode: 1,
			lastStart:    time.Now().Add(-2 * time.Minute),
			lastError:    &agenterr.AgentError{Class: agenterr.BillingError, Message: "payment required"},
		}

		result := daemon.shouldRestart(ap)
		if result {
			t.Error("shouldRestart() = true, want false for BillingError (fatal)")
		}
	})

	t.Run("rate limited with no_count=true does not count toward retries", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(3)
		// RateLimitNoCount defaults to true (nil)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "test"},
			restartCount: 2,
			lastExitCode: 1,
			lastStart:    time.Now().Add(-2 * time.Minute),
			lastError:    &agenterr.AgentError{Class: agenterr.RateLimited, Message: "rate limited"},
		}

		result := daemon.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true for RateLimited with no_count=true")
		}
		if ap.restartCount != 2 {
			t.Errorf("restartCount = %d, want 2 (should be unchanged for rate-limited)", ap.restartCount)
		}
		if ap.rateRetryCount != 1 {
			t.Errorf("rateRetryCount = %d, want 1", ap.rateRetryCount)
		}
	})

	t.Run("rate limited with no_count=false counts normally", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(3)
		config.Daemon.RestartPolicy.RateLimitNoCount = boolPtr(false)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "test"},
			restartCount: 2,
			lastExitCode: 1,
			lastStart:    time.Now().Add(-2 * time.Minute),
			lastError:    &agenterr.AgentError{Class: agenterr.RateLimited, Message: "rate limited"},
		}

		result := daemon.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true (count 3 <= maxRetries 3)")
		}
		if ap.restartCount != 3 {
			t.Errorf("restartCount = %d, want 3 (should be incremented for rate-limited with no_count=false)", ap.restartCount)
		}
	})

	t.Run("successful run resets rateRetryCount", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(3)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			restartCount:   2,
			rateRetryCount: 5,
			lastExitCode:   0,
			lastStart:      time.Now().Add(-2 * time.Minute),
		}

		result := daemon.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true for successful long run")
		}
		if ap.restartCount != 0 {
			t.Errorf("restartCount = %d, want 0 (should be reset)", ap.restartCount)
		}
		if ap.rateRetryCount != 0 {
			t.Errorf("rateRetryCount = %d, want 0 (should be reset)", ap.rateRetryCount)
		}
	})

	t.Run("timeout error counts toward retries", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(3)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "test"},
			restartCount: 1,
			lastExitCode: 137,
			lastStart:    time.Now().Add(-2 * time.Minute),
			lastError:    &agenterr.AgentError{Class: agenterr.Timeout, Message: "connection timed out"},
		}

		result := daemon.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true")
		}
		if ap.restartCount != 2 {
			t.Errorf("restartCount = %d, want 2", ap.restartCount)
		}
	})

	t.Run("NoWork does not count toward retries and always restarts", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(0) // even with 0 retries allowed
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			restartCount:   3,
			rateRetryCount: 2,
			lastExitCode:   0,
			lastStart:      time.Now().Add(-10 * time.Second), // short run
			lastError:      &agenterr.AgentError{Class: agenterr.NoWork, Message: "no claimable tasks"},
		}

		result := daemon.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true for NoWork (always restart)")
		}
		if ap.restartCount != 0 {
			t.Errorf("restartCount = %d, want 0 (should be reset for NoWork)", ap.restartCount)
		}
		if ap.rateRetryCount != 0 {
			t.Errorf("rateRetryCount = %d, want 0 (should be reset for NoWork)", ap.rateRetryCount)
		}
	})

	t.Run("NoWork on fallback backend preserves currentBackendIdx", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan", FallbackBackends: []string{"codex"}}},
			nil,
		)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			currentBackendIdx: 1, // already on fallback backend
			restartCount:      1,
			lastExitCode:      0,
			lastStart:         time.Now().Add(-10 * time.Second),
			lastError:         &agenterr.AgentError{Class: agenterr.NoWork, Message: "no claimable tasks"},
		}

		result := daemon.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true for NoWork")
		}
		if ap.currentBackendIdx != 1 {
			t.Errorf("currentBackendIdx = %d, want 1 (NoWork should not reset failover state)", ap.currentBackendIdx)
		}
	})

	t.Run("nil lastError counts toward retries normally", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(3)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			restartCount: 1,
			lastExitCode: 1,
			lastStart:    time.Now().Add(-2 * time.Minute),
			lastError:    nil, // no classification available
		}

		result := daemon.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true")
		}
		if ap.restartCount != 2 {
			t.Errorf("restartCount = %d, want 2", ap.restartCount)
		}
	})

	t.Run("non-rate error resets rateRetryCount", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = intPtr(3)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:          AgentEntry{Worktree: "test"},
			restartCount:   1,
			rateRetryCount: 5,
			lastExitCode:   1,
			lastStart:      time.Now().Add(-2 * time.Minute),
			lastError:      &agenterr.AgentError{Class: agenterr.Transient, Message: "server error"},
		}

		result := daemon.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true")
		}
		if ap.rateRetryCount != 0 {
			t.Errorf("rateRetryCount = %d, want 0 (should be reset on non-rate error)", ap.rateRetryCount)
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

		daemon, err := NewDaemon(config, tmpDir, nil)
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

func TestGetOutputTimeout(t *testing.T) {
	t.Run("returns default when not configured", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		daemon := &Daemon{config: config}
		got := daemon.getOutputTimeout()
		if got != 900 {
			t.Errorf("getOutputTimeout() = %d, want 900", got)
		}
	})

	t.Run("returns configured value", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = intPtr(600)
		daemon := &Daemon{config: config}
		got := daemon.getOutputTimeout()
		if got != 600 {
			t.Errorf("getOutputTimeout() = %d, want 600", got)
		}
	})

	t.Run("returns 0 when disabled", func(t *testing.T) {
		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = intPtr(0)
		daemon := &Daemon{config: config}
		got := daemon.getOutputTimeout()
		if got != 0 {
			t.Errorf("getOutputTimeout() = %d, want 0", got)
		}
	})
}

func TestCheckAgentHealth_Watchdog(t *testing.T) {
	t.Run("does not kill agent with recent output", func(t *testing.T) {
		// Create a temporary log file with recent modification time
		tmpDir := t.TempDir()
		logPath := filepath.Join(tmpDir, "task-test.log")
		if err := os.WriteFile(logPath, []byte("recent output\n"), 0600); err != nil {
			t.Fatal(err)
		}

		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "task"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = intPtr(60) // 60 seconds

		ap := &AgentProcess{
			entry:       AgentEntry{Worktree: "test", Role: "task"},
			pid:         99999999, // fake PID that won't exist
			logFilePath: logPath,
			lastStart:   time.Now().Add(-30 * time.Second),
		}

		daemon := &Daemon{
			config: config,
			agents: []*AgentProcess{ap},
		}

		// Should not panic or kill anything — agent has recent output
		daemon.checkAgentHealth()

		// Agent should still have its PID (not killed)
		ap.mu.Lock()
		pid := ap.pid
		ap.mu.Unlock()
		if pid != 99999999 {
			t.Errorf("pid = %d, want 99999999 (should not be killed)", pid)
		}
	})

	t.Run("kills agent with stale output", func(t *testing.T) {
		// Create a temporary log file with old modification time
		tmpDir := t.TempDir()
		logPath := filepath.Join(tmpDir, "task-test.log")
		if err := os.WriteFile(logPath, []byte("old output\n"), 0600); err != nil {
			t.Fatal(err)
		}
		// Set modification time to 20 minutes ago
		oldTime := time.Now().Add(-20 * time.Minute)
		if err := os.Chtimes(logPath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}

		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "task"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = intPtr(60) // 60 seconds

		ap := &AgentProcess{
			entry:       AgentEntry{Worktree: "test", Role: "task"},
			pid:         99999999, // fake PID — stopAgent will fail gracefully
			logFilePath: logPath,
			lastStart:   time.Now().Add(-25 * time.Minute),
		}

		daemon := &Daemon{
			config: config,
			agents: []*AgentProcess{ap},
		}

		// This will try to stop the agent (stopAgent handles non-existent PIDs gracefully)
		daemon.checkAgentHealth()
		// We can't easily assert stopAgent was called since the PID doesn't exist,
		// but the code path should execute without panic
	})

	t.Run("skips watchdog when disabled", func(t *testing.T) {
		tmpDir := t.TempDir()
		logPath := filepath.Join(tmpDir, "task-test.log")
		if err := os.WriteFile(logPath, []byte("old output\n"), 0600); err != nil {
			t.Fatal(err)
		}
		oldTime := time.Now().Add(-20 * time.Minute)
		if err := os.Chtimes(logPath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}

		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "task"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = intPtr(0) // disabled

		ap := &AgentProcess{
			entry:       AgentEntry{Worktree: "test", Role: "task"},
			pid:         99999999,
			logFilePath: logPath,
			lastStart:   time.Now().Add(-25 * time.Minute),
		}

		daemon := &Daemon{
			config: config,
			agents: []*AgentProcess{ap},
		}

		daemon.checkAgentHealth()

		// Agent should still have its PID (watchdog disabled, so no kill)
		ap.mu.Lock()
		pid := ap.pid
		ap.mu.Unlock()
		if pid != 99999999 {
			t.Errorf("pid = %d, want 99999999 (watchdog disabled, should not kill)", pid)
		}
	})

	t.Run("uses lastStart when log not yet written", func(t *testing.T) {
		// Agent just spawned, log file exists but hasn't been written to
		tmpDir := t.TempDir()
		logPath := filepath.Join(tmpDir, "task-test.log")
		if err := os.WriteFile(logPath, []byte{}, 0600); err != nil {
			t.Fatal(err)
		}
		// Log file mtime is before lastStart (file was created before agent started)
		oldTime := time.Now().Add(-30 * time.Minute)
		if err := os.Chtimes(logPath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}

		config := makeDaemonConfig(
			[]AgentEntry{{Worktree: "test", Role: "task"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = intPtr(300) // 5 minutes

		// lastStart is 2 minutes ago — well within timeout
		ap := &AgentProcess{
			entry:       AgentEntry{Worktree: "test", Role: "task"},
			pid:         99999999,
			logFilePath: logPath,
			lastStart:   time.Now().Add(-2 * time.Minute),
		}

		daemon := &Daemon{
			config: config,
			agents: []*AgentProcess{ap},
		}

		daemon.checkAgentHealth()

		// Should NOT be killed — lastStart (2 min ago) is within 5 min timeout
		ap.mu.Lock()
		pid := ap.pid
		ap.mu.Unlock()
		if pid != 99999999 {
			t.Errorf("pid = %d, want 99999999 (should use lastStart, not stale log mtime)", pid)
		}
	})
}

func TestGetRateLimitBackoff(t *testing.T) {
	t.Run("returns default when not configured", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}
		if got := daemon.getRateLimitBackoff(); got != 30 {
			t.Errorf("getRateLimitBackoff() = %d, want 30", got)
		}
	})

	t.Run("returns configured value", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		config.Daemon.RestartPolicy.RateLimitBackoff = intPtr(60)
		daemon := &Daemon{config: config}
		if got := daemon.getRateLimitBackoff(); got != 60 {
			t.Errorf("getRateLimitBackoff() = %d, want 60", got)
		}
	})
}

func TestGetRateLimitMaxWait(t *testing.T) {
	t.Run("returns default when not configured", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}
		if got := daemon.getRateLimitMaxWait(); got != 300 {
			t.Errorf("getRateLimitMaxWait() = %d, want 300", got)
		}
	})

	t.Run("returns configured value", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		config.Daemon.RestartPolicy.RateLimitMaxWait = intPtr(600)
		daemon := &Daemon{config: config}
		if got := daemon.getRateLimitMaxWait(); got != 600 {
			t.Errorf("getRateLimitMaxWait() = %d, want 600", got)
		}
	})
}

func TestGetRateLimitNoCount(t *testing.T) {
	t.Run("returns default true when not configured", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}
		if got := daemon.getRateLimitNoCount(); !got {
			t.Errorf("getRateLimitNoCount() = %v, want true", got)
		}
	})

	t.Run("returns false when configured", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		config.Daemon.RestartPolicy.RateLimitNoCount = boolPtr(false)
		daemon := &Daemon{config: config}
		if got := daemon.getRateLimitNoCount(); got {
			t.Errorf("getRateLimitNoCount() = %v, want false", got)
		}
	})
}

func TestGetTimeoutBackoff(t *testing.T) {
	t.Run("returns default when not configured", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}
		if got := daemon.getTimeoutBackoff(); got != 5 {
			t.Errorf("getTimeoutBackoff() = %d, want 5", got)
		}
	})

	t.Run("returns configured value", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		config.Daemon.RestartPolicy.TimeoutBackoff = intPtr(10)
		daemon := &Daemon{config: config}
		if got := daemon.getTimeoutBackoff(); got != 10 {
			t.Errorf("getTimeoutBackoff() = %d, want 10", got)
		}
	})
}

func TestGetNoWorkBackoff(t *testing.T) {
	t.Run("returns default when not configured", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}
		if got := daemon.getNoWorkBackoff(); got != 30 {
			t.Errorf("getNoWorkBackoff() = %d, want 30", got)
		}
	})

	t.Run("returns configured value", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		config.Daemon.RestartPolicy.NoWorkBackoff = intPtr(45)
		daemon := &Daemon{config: config}
		if got := daemon.getNoWorkBackoff(); got != 45 {
			t.Errorf("getNoWorkBackoff() = %d, want 45", got)
		}
	})
}

func TestGetEffectiveBackend(t *testing.T) {
	t.Run("index 0 returns primary backend", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		config.Backend = "global"
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:             AgentEntry{Backend: "claude", FallbackBackends: []string{"codex", "opencode"}},
			currentBackendIdx: 0,
		}

		got := daemon.getEffectiveBackend(ap)
		if got != "claude" {
			t.Errorf("getEffectiveBackend() = %q, want %q", got, "claude")
		}
	})

	t.Run("index 0 falls back to config backend", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		config.Backend = "codex"
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:             AgentEntry{Backend: "", FallbackBackends: []string{"opencode"}},
			currentBackendIdx: 0,
		}

		got := daemon.getEffectiveBackend(ap)
		if got != "codex" {
			t.Errorf("getEffectiveBackend() = %q, want %q", got, "codex")
		}
	})

	t.Run("index 1 returns first fallback", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:             AgentEntry{Backend: "claude", FallbackBackends: []string{"codex", "opencode"}},
			currentBackendIdx: 1,
		}

		got := daemon.getEffectiveBackend(ap)
		if got != "codex" {
			t.Errorf("getEffectiveBackend() = %q, want %q", got, "codex")
		}
	})

	t.Run("index 2 returns second fallback", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:             AgentEntry{Backend: "claude", FallbackBackends: []string{"codex", "opencode"}},
			currentBackendIdx: 2,
		}

		got := daemon.getEffectiveBackend(ap)
		if got != "opencode" {
			t.Errorf("getEffectiveBackend() = %q, want %q", got, "opencode")
		}
	})

	t.Run("index beyond fallbacks returns primary", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:             AgentEntry{Backend: "claude", FallbackBackends: []string{"codex"}},
			currentBackendIdx: 5,
		}

		got := daemon.getEffectiveBackend(ap)
		if got != "claude" {
			t.Errorf("getEffectiveBackend() = %q, want %q (should return primary when beyond fallbacks)", got, "claude")
		}
	})
}

func TestTryFallbackBackend(t *testing.T) {
	t.Run("ModelNotFound triggers immediate failover", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:             AgentEntry{Worktree: "test", Backend: "claude", FallbackBackends: []string{"codex"}},
			currentBackendIdx: 0,
			restartCount:      2,
			rateRetryCount:    1,
			lastError:         &agenterr.AgentError{Class: agenterr.ModelNotFound, Message: "model not found"},
		}

		result := daemon.tryFallbackBackend(ap)
		if !result {
			t.Error("tryFallbackBackend() = false, want true for ModelNotFound")
		}
		if ap.currentBackendIdx != 1 {
			t.Errorf("currentBackendIdx = %d, want 1", ap.currentBackendIdx)
		}
		if ap.restartCount != 0 {
			t.Errorf("restartCount = %d, want 0 (should be reset)", ap.restartCount)
		}
		if ap.rateRetryCount != 0 {
			t.Errorf("rateRetryCount = %d, want 0 (should be reset)", ap.rateRetryCount)
		}
	})

	t.Run("RateLimited with rateRetryCount <= 3 does not trigger failover", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:             AgentEntry{Worktree: "test", FallbackBackends: []string{"codex"}},
			currentBackendIdx: 0,
			rateRetryCount:    2,
			lastError:         &agenterr.AgentError{Class: agenterr.RateLimited, Message: "rate limited"},
		}

		result := daemon.tryFallbackBackend(ap)
		if result {
			t.Error("tryFallbackBackend() = true, want false for RateLimited with count <= 3")
		}
		if ap.currentBackendIdx != 0 {
			t.Errorf("currentBackendIdx = %d, want 0 (unchanged)", ap.currentBackendIdx)
		}
	})

	t.Run("RateLimited with rateRetryCount > 3 triggers failover", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:             AgentEntry{Worktree: "test", FallbackBackends: []string{"codex"}},
			currentBackendIdx: 0,
			rateRetryCount:    4,
			lastError:         &agenterr.AgentError{Class: agenterr.RateLimited, Message: "rate limited"},
		}

		result := daemon.tryFallbackBackend(ap)
		if !result {
			t.Error("tryFallbackBackend() = false, want true for RateLimited with count > 3")
		}
		if ap.currentBackendIdx != 1 {
			t.Errorf("currentBackendIdx = %d, want 1", ap.currentBackendIdx)
		}
	})

	t.Run("no fallback backends configured returns false", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:     AgentEntry{Worktree: "test"},
			lastError: &agenterr.AgentError{Class: agenterr.ModelNotFound, Message: "model not found"},
		}

		result := daemon.tryFallbackBackend(ap)
		if result {
			t.Error("tryFallbackBackend() = true, want false (no fallback backends)")
		}
	})

	t.Run("all backends exhausted returns false", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:             AgentEntry{Worktree: "test", FallbackBackends: []string{"codex", "opencode"}},
			currentBackendIdx: 2, // already on last fallback (total 3 backends)
			lastError:         &agenterr.AgentError{Class: agenterr.ModelNotFound, Message: "model not found"},
		}

		result := daemon.tryFallbackBackend(ap)
		if result {
			t.Error("tryFallbackBackend() = true, want false (all backends exhausted)")
		}
	})

	t.Run("AuthFailure does not trigger failover", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:     AgentEntry{Worktree: "test", FallbackBackends: []string{"codex"}},
			lastError: &agenterr.AgentError{Class: agenterr.AuthFailure, Message: "invalid API key"},
		}

		result := daemon.tryFallbackBackend(ap)
		if result {
			t.Error("tryFallbackBackend() = true, want false for AuthFailure")
		}
	})

	t.Run("Transient does not trigger failover", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:     AgentEntry{Worktree: "test", FallbackBackends: []string{"codex"}},
			lastError: &agenterr.AgentError{Class: agenterr.Transient, Message: "server error"},
		}

		result := daemon.tryFallbackBackend(ap)
		if result {
			t.Error("tryFallbackBackend() = true, want false for Transient")
		}
	})

	t.Run("nil lastError returns false", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:     AgentEntry{Worktree: "test", FallbackBackends: []string{"codex"}},
			lastError: nil,
		}

		result := daemon.tryFallbackBackend(ap)
		if result {
			t.Error("tryFallbackBackend() = true, want false for nil lastError")
		}
	})

	t.Run("failover resets restartCount and rateRetryCount", func(t *testing.T) {
		config := makeDaemonConfig(nil, nil)
		daemon := &Daemon{config: config}

		ap := &AgentProcess{
			entry:             AgentEntry{Worktree: "test", FallbackBackends: []string{"codex"}},
			currentBackendIdx: 0,
			restartCount:      5,
			rateRetryCount:    3,
			lastError:         &agenterr.AgentError{Class: agenterr.ModelNotFound, Message: "not found"},
		}

		daemon.tryFallbackBackend(ap)

		if ap.restartCount != 0 {
			t.Errorf("restartCount = %d, want 0 (should be reset on failover)", ap.restartCount)
		}
		if ap.rateRetryCount != 0 {
			t.Errorf("rateRetryCount = %d, want 0 (should be reset on failover)", ap.rateRetryCount)
		}
	})
}

func TestShouldRestart_ResetsBackendOnSuccess(t *testing.T) {
	config := makeDaemonConfig(
		[]AgentEntry{{Worktree: "test", Role: "plan"}},
		nil,
	)
	config.Daemon.RestartPolicy.MaxRetries = intPtr(3)
	daemon := &Daemon{config: config}

	ap := &AgentProcess{
		restartCount:      1,
		rateRetryCount:    2,
		currentBackendIdx: 2, // on second fallback
		lastExitCode:      0,
		lastStart:         time.Now().Add(-2 * time.Minute),
	}

	result := daemon.shouldRestart(ap)
	if !result {
		t.Error("shouldRestart() = false, want true for successful long run")
	}
	if ap.currentBackendIdx != 0 {
		t.Errorf("currentBackendIdx = %d, want 0 (should reset to primary on success)", ap.currentBackendIdx)
	}
}

func TestBuildCommand_UsesEffectiveBackend(t *testing.T) {
	config := makeDaemonConfig(nil, nil)
	config.Backend = "claude"
	daemon := &Daemon{config: config}

	t.Run("uses primary backend at index 0", func(t *testing.T) {
		ap := &AgentProcess{
			entry:             AgentEntry{Worktree: "test", Role: "plan", Backend: "claude", FallbackBackends: []string{"codex"}},
			worktreePath:      "/tmp/test",
			currentBackendIdx: 0,
		}

		cmd := daemon.buildCommand(ap)
		args := strings.Join(cmd.Args, " ")
		if !strings.Contains(args, "--backend claude") {
			t.Errorf("buildCommand args = %q, want to contain '--backend claude'", args)
		}
	})

	t.Run("uses fallback backend at index 1", func(t *testing.T) {
		ap := &AgentProcess{
			entry:             AgentEntry{Worktree: "test", Role: "plan", Backend: "claude", FallbackBackends: []string{"codex"}},
			worktreePath:      "/tmp/test",
			currentBackendIdx: 1,
		}

		cmd := daemon.buildCommand(ap)
		args := strings.Join(cmd.Args, " ")
		if !strings.Contains(args, "--backend codex") {
			t.Errorf("buildCommand args = %q, want to contain '--backend codex'", args)
		}
	})
}

func TestAgents_IncludesCurrentBackend(t *testing.T) {
	config := makeDaemonConfig(nil, nil)
	config.Backend = "claude"
	daemon := &Daemon{
		config: config,
		agents: []*AgentProcess{
			{
				entry:             AgentEntry{Worktree: "alpha", Role: "plan", Backend: "claude", FallbackBackends: []string{"codex"}},
				worktreePath:      "/path/alpha",
				currentBackendIdx: 0,
			},
			{
				entry:             AgentEntry{Worktree: "beta", Role: "task", Backend: "claude", FallbackBackends: []string{"codex", "opencode"}},
				worktreePath:      "/path/beta",
				currentBackendIdx: 2,
			},
		},
	}

	statuses := daemon.Agents()

	if statuses[0].CurrentBackend != "claude" {
		t.Errorf("statuses[0].CurrentBackend = %q, want %q", statuses[0].CurrentBackend, "claude")
	}
	if statuses[1].CurrentBackend != "opencode" {
		t.Errorf("statuses[1].CurrentBackend = %q, want %q", statuses[1].CurrentBackend, "opencode")
	}
}

// TestBuildCommand_BackendResolutionChain verifies the per-agent > per-role > project > global precedence.
func TestBuildCommand_BackendResolutionChain(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("per-agent backend wins over per-role and project", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}, Backend: "global-backend"},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan", Backend: "agent-backend"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent", Backend: "role-backend"},
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		foundBackend := ""
		for i, arg := range cmd.Args {
			if arg == "--backend" && i+1 < len(cmd.Args) {
				foundBackend = cmd.Args[i+1]
			}
		}
		if foundBackend != "agent-backend" {
			t.Errorf("backend = %q, want %q (per-agent should win)", foundBackend, "agent-backend")
		}
	})

	t.Run("per-role backend wins when per-agent is empty", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}, Backend: "global-backend"},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan"}, // no Backend
			roleConfig:   RoleConfig{Description: "Built-in plan agent", Backend: "role-backend"},
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		foundBackend := ""
		for i, arg := range cmd.Args {
			if arg == "--backend" && i+1 < len(cmd.Args) {
				foundBackend = cmd.Args[i+1]
			}
		}
		if foundBackend != "role-backend" {
			t.Errorf("backend = %q, want %q (per-role should win)", foundBackend, "role-backend")
		}
	})

	t.Run("project backend used when per-agent and per-role are empty", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}, Backend: "project-backend"},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent"}, // no Backend
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		foundBackend := ""
		for i, arg := range cmd.Args {
			if arg == "--backend" && i+1 < len(cmd.Args) {
				foundBackend = cmd.Args[i+1]
			}
		}
		if foundBackend != "project-backend" {
			t.Errorf("backend = %q, want %q (project should be used)", foundBackend, "project-backend")
		}
	})

	t.Run("no backend flag when all levels are empty", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}}, // no Backend
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent"},
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		for _, arg := range cmd.Args {
			if arg == "--backend" {
				t.Error("--backend should not be present when all levels are empty")
			}
		}
	})
}

// TestBuildCommand_ToolConstraintEnvVars verifies LOOM_ALLOWED_TOOLS, LOOM_DENIED_TOOLS,
// and LOOM_READ_ONLY are set in cmd.Env when role has constraints.
func TestBuildCommand_ToolConstraintEnvVars(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("allowed tools env var set", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent", AllowedTools: []string{"read", "grep", "glob"}},
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		found := false
		for _, env := range cmd.Env {
			if env == "LOOM_ALLOWED_TOOLS=read,grep,glob" {
				found = true
			}
		}
		if !found {
			t.Error("LOOM_ALLOWED_TOOLS=read,grep,glob not found in cmd.Env")
		}
	})

	t.Run("denied tools env var set", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent", DeniedTools: []string{"bash", "write", "edit"}},
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		found := false
		for _, env := range cmd.Env {
			if env == "LOOM_DENIED_TOOLS=bash,write,edit" {
				found = true
			}
		}
		if !found {
			t.Error("LOOM_DENIED_TOOLS=bash,write,edit not found in cmd.Env")
		}
	})

	t.Run("read-only env var set", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent", ReadOnly: true},
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		found := false
		for _, env := range cmd.Env {
			if env == "LOOM_READ_ONLY=1" {
				found = true
			}
		}
		if !found {
			t.Error("LOOM_READ_ONLY=1 not found in cmd.Env")
		}
	})

	t.Run("all constraint env vars set together", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry: AgentEntry{Worktree: "falcon", Role: "plan"},
			roleConfig: RoleConfig{
				Description:  "Built-in plan agent",
				AllowedTools: []string{"read"},
				DeniedTools:  []string{"write"},
				ReadOnly:     true,
			},
			worktreePath: tmpDir,
		}

		cmd := d.buildCommand(ap)
		foundAllowed, foundDenied, foundReadOnly := false, false, false
		for _, env := range cmd.Env {
			switch {
			case env == "LOOM_ALLOWED_TOOLS=read":
				foundAllowed = true
			case env == "LOOM_DENIED_TOOLS=write":
				foundDenied = true
			case env == "LOOM_READ_ONLY=1":
				foundReadOnly = true
			}
		}
		if !foundAllowed {
			t.Error("LOOM_ALLOWED_TOOLS not found")
		}
		if !foundDenied {
			t.Error("LOOM_DENIED_TOOLS not found")
		}
		if !foundReadOnly {
			t.Error("LOOM_READ_ONLY not found")
		}
	})
}

// TestBuildCommand_RoutingEnvVars verifies LOOM_ROLE_SKILLS, LOOM_ROLE_PATH_PATTERNS,
// LOOM_ROLE_MAX_PRIORITY, LOOM_ROLE_TASK_FILTER, and LOOM_ROLE are set in cmd.Env.
func TestBuildCommand_RoutingEnvVars(t *testing.T) {
	tmpDir := t.TempDir()
	maxP := 2

	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}},
		projectDir: tmpDir,
	}
	ap := &AgentProcess{
		entry: AgentEntry{Worktree: "falcon", Role: "plan", PathPatterns: []string{"cmd/**"}},
		roleConfig: RoleConfig{
			Description:  "Built-in plan agent",
			Skills:       []string{"go", "daemon"},
			PathPatterns: []string{"internal/**"},
			MaxPriority:  &maxP,
			TaskFilter:   "needs_plan",
		},
		worktreePath: tmpDir,
	}

	cmd := d.buildCommand(ap)
	envMap := make(map[string]string)
	for _, env := range cmd.Env {
		if idx := strings.IndexByte(env, '='); idx >= 0 {
			envMap[env[:idx]] = env[idx+1:]
		}
	}

	if v, ok := envMap["LOOM_ROLE_SKILLS"]; !ok || v != "go,daemon" {
		t.Errorf("LOOM_ROLE_SKILLS = %q, want %q", v, "go,daemon")
	}
	if v, ok := envMap["LOOM_ROLE_PATH_PATTERNS"]; !ok || v != "internal/**" {
		t.Errorf("LOOM_ROLE_PATH_PATTERNS = %q, want %q", v, "internal/**")
	}
	if v, ok := envMap["LOOM_ROLE_MAX_PRIORITY"]; !ok || v != "2" {
		t.Errorf("LOOM_ROLE_MAX_PRIORITY = %q, want %q", v, "2")
	}
	if v, ok := envMap["LOOM_ROLE_TASK_FILTER"]; !ok || v != "needs_plan" {
		t.Errorf("LOOM_ROLE_TASK_FILTER = %q, want %q", v, "needs_plan")
	}
	if v, ok := envMap["LOOM_ROLE"]; !ok || v != "plan" {
		t.Errorf("LOOM_ROLE = %q, want %q", v, "plan")
	}
	if v, ok := envMap["LOOM_AGENT_PATH_PATTERNS"]; !ok || v != "cmd/**" {
		t.Errorf("LOOM_AGENT_PATH_PATTERNS = %q, want %q", v, "cmd/**")
	}
}

// TestBuildCommand_NoRoutingEnvVars verifies no routing env vars are set when
// role has no routing config (backward compatibility).
func TestBuildCommand_NoRoutingEnvVars(t *testing.T) {
	tmpDir := t.TempDir()

	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}},
		projectDir: tmpDir,
	}
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
		roleConfig:   RoleConfig{Description: "Built-in plan agent"}, // no routing config
		worktreePath: tmpDir,
	}

	cmd := d.buildCommand(ap)
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_ROLE_SKILLS=") {
			t.Error("LOOM_ROLE_SKILLS should not be set when Skills is empty")
		}
		if strings.HasPrefix(env, "LOOM_ROLE_PATH_PATTERNS=") {
			t.Error("LOOM_ROLE_PATH_PATTERNS should not be set when PathPatterns is empty")
		}
		if strings.HasPrefix(env, "LOOM_ROLE_MAX_PRIORITY=") {
			t.Error("LOOM_ROLE_MAX_PRIORITY should not be set when MaxPriority is nil")
		}
		if strings.HasPrefix(env, "LOOM_ROLE_TASK_FILTER=") {
			t.Error("LOOM_ROLE_TASK_FILTER should not be set when TaskFilter is empty")
		}
		if strings.HasPrefix(env, "LOOM_AGENT_PATH_PATTERNS=") {
			t.Error("LOOM_AGENT_PATH_PATTERNS should not be set when AgentEntry has no PathPatterns")
		}
		if strings.HasPrefix(env, "LOOM_SOURCE_REPOS=") {
			t.Error("LOOM_SOURCE_REPOS should not be set when agent has no repo affinity")
		}
	}
	// LOOM_ROLE should always be set
	foundRole := false
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_ROLE=") {
			foundRole = true
		}
	}
	if !foundRole {
		t.Error("LOOM_ROLE should always be set")
	}
}

// TestBuildCommand_SourceReposInjected verifies LOOM_SOURCE_REPOS is set when
// the agent has Repos declared and d.repos provides the RepoConfig mapping.
func TestBuildCommand_SourceReposInjected(t *testing.T) {
	tmpDir := t.TempDir()

	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}},
		projectDir: tmpDir,
		repos: []RepoConfig{
			{Name: "backend", SourceRepoID: "src-backend", Groups: []string{"infra"}},
			{Name: "frontend", SourceRepoID: "src-frontend"},
		},
	}
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "task", Repos: []string{"backend"}},
		roleConfig:   RoleConfig{Description: "Backend agent"},
		worktreePath: tmpDir,
	}

	cmd := d.buildCommand(ap)
	envMap := make(map[string]string)
	for _, env := range cmd.Env {
		if idx := strings.IndexByte(env, '='); idx >= 0 {
			envMap[env[:idx]] = env[idx+1:]
		}
	}

	if v, ok := envMap["LOOM_SOURCE_REPOS"]; !ok || v != "backend" {
		t.Errorf("LOOM_SOURCE_REPOS = %q, want %q", v, "backend")
	}
}

// TestBuildCommand_SourceReposAbsentWhenEmpty verifies LOOM_SOURCE_REPOS is not
// set when the agent has no repo affinity.
func TestBuildCommand_SourceReposAbsentWhenEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}},
		projectDir: tmpDir,
		repos: []RepoConfig{
			{Name: "backend", SourceRepoID: "src-backend"},
		},
	}
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "task"},
		roleConfig:   RoleConfig{Description: "Generic agent"},
		worktreePath: tmpDir,
	}

	cmd := d.buildCommand(ap)
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

	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}},
		projectDir: tmpDir,
	}
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
		roleConfig:   RoleConfig{Description: "Built-in plan agent"}, // no constraints
		worktreePath: tmpDir,
	}

	cmd := d.buildCommand(ap)
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

// TestDaemonStop_ClosesConcurrencyTracker verifies that Daemon.Stop() calls
// concurrency.Close() to unblock waiters.
func TestDaemonStop_ClosesConcurrencyTracker(t *testing.T) {
	limit := 1
	ct := NewConcurrencyTracker(map[string]RoleConfig{
		"task": {MaxConcurrency: &limit},
	})

	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					MaxRetries:     intPtr(0),
					BackoffInitial: intPtr(1),
					BackoffMax:     intPtr(1),
				},
			},
		},
		agents:       []*AgentProcess{},
		epicAssigner: NewEpicAssigner(),
		concurrency:  ct,
	}

	if err := d.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Fill the slot
	ct.Acquire("task")

	// Try to acquire in background — should block
	acquired := make(chan bool, 1)
	go func() {
		acquired <- ct.Acquire("task")
	}()

	// Stop should close the tracker and unblock the goroutine
	d.Stop()

	result := <-acquired
	if result {
		t.Error("Acquire after Stop should return false (tracker closed)")
	}
}
