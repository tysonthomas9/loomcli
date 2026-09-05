package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestAppendRoleEnv_MaxBudgetUSD(t *testing.T) {
	t.Parallel()

	t.Run("set when non-nil", func(t *testing.T) {
		t.Parallel()
		budget := 8.50
		ap := &AgentProcess{
			RoleConfig: cfgpkg.RoleConfig{
				MaxBudgetUSD: &budget,
			},
		}

		env := appendRoleEnv(nil, ap)

		found := false
		for _, entry := range env {
			if strings.HasPrefix(entry, "LOOM_MAX_BUDGET_USD=") {
				found = true
				want := fmt.Sprintf("LOOM_MAX_BUDGET_USD=%.2f", budget)
				if entry != want {
					t.Errorf("env entry = %q, want %q", entry, want)
				}
				break
			}
		}
		if !found {
			t.Errorf("expected LOOM_MAX_BUDGET_USD in env, got %v", env)
		}
	})

	t.Run("absent when nil", func(t *testing.T) {
		t.Parallel()
		ap := &AgentProcess{
			RoleConfig: cfgpkg.RoleConfig{
				MaxBudgetUSD: nil,
			},
		}

		env := appendRoleEnv(nil, ap)

		for _, entry := range env {
			if strings.HasPrefix(entry, "LOOM_MAX_BUDGET_USD=") {
				t.Errorf("expected LOOM_MAX_BUDGET_USD to be absent, but found %q", entry)
			}
		}
	})

	t.Run("zero value is formatted", func(t *testing.T) {
		t.Parallel()
		budget := 0.0
		ap := &AgentProcess{
			RoleConfig: cfgpkg.RoleConfig{
				MaxBudgetUSD: &budget,
			},
		}

		env := appendRoleEnv(nil, ap)

		found := false
		for _, entry := range env {
			if strings.HasPrefix(entry, "LOOM_MAX_BUDGET_USD=") {
				found = true
				if entry != "LOOM_MAX_BUDGET_USD=0.00" {
					t.Errorf("env entry = %q, want %q", entry, "LOOM_MAX_BUDGET_USD=0.00")
				}
				break
			}
		}
		if !found {
			t.Errorf("expected LOOM_MAX_BUDGET_USD=0.00 in env, got %v", env)
		}
	})
}

func TestAppendRoleEnv_Effort(t *testing.T) {
	t.Parallel()

	ap := &AgentProcess{
		RoleConfig: cfgpkg.RoleConfig{
			Effort: "max",
		},
	}

	env := appendRoleEnv(nil, ap)

	want := map[string]bool{
		"LOOM_AGENT_EFFORT=max":  false,
		"LOOM_CLAUDE_EFFORT=max": false,
	}
	for _, entry := range env {
		if _, ok := want[entry]; ok {
			want[entry] = true
		}
	}
	for entry, found := range want {
		if !found {
			t.Fatalf("expected %s in env, got %v", entry, env)
		}
	}
}

func TestAppendSessionEnvConcurrentLeaseAccess(t *testing.T) {
	ap := &AgentProcess{}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			ap.Mu.Lock()
			ap.AgentLeaseID = fmt.Sprintf("lease-%d", i)
			ap.AgentLeaseToken = fmt.Sprintf("token-%d", i)
			ap.Mu.Unlock()
			runtime.Gosched()
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			_ = appendSessionEnv(nil, ap)
			runtime.Gosched()
		}
	}()

	wg.Wait()
}

// TestSetupAgentLogFile_LogStartOffset pins the recording side of the per-run
// classification window. The daemon log is opened O_APPEND and outlives every
// run in it, so "where does this run start" is only knowable at spawn time.
func TestSetupAgentLogFile_LogStartOffset(t *testing.T) {
	// not parallel: mutates HOME so the agent archive sink stays in a temp dir
	t.Setenv("HOME", t.TempDir())

	logDir := t.TempDir()
	cfg := &cfgpkg.DaemonConfig{}
	cfg.Daemon.LogDir = logDir
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg },
		WorkspaceID:    "ws",
		ProjectDir:     logDir,
	}

	newAP := func() *AgentProcess {
		return &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "worker"}}
	}

	t.Run("empty log starts at zero", func(t *testing.T) {
		ap := newAP()
		// A bare Cmd is enough: setupAgentLogFile only assigns its
		// Stdout/Stderr, and nothing here is ever started.
		s.setupAgentLogFile(ap, &exec.Cmd{})
		t.Cleanup(func() { _ = ap.LogFile.Close() })

		if ap.LogFilePath == "" {
			t.Fatal("LogFilePath is empty; the daemon log sink did not open")
		}
		if ap.LogStartOffset != 0 {
			t.Errorf("LogStartOffset = %d, want 0 for a fresh log", ap.LogStartOffset)
		}
	})

	t.Run("append picks up the pre-existing size", func(t *testing.T) {
		first := newAP()
		s.setupAgentLogFile(first, &exec.Cmd{})
		prior := []byte("a previous run's output\nNot logged in · Run /login\n")
		if _, err := first.LogFile.Write(prior); err != nil {
			t.Fatalf("write prior run output: %v", err)
		}
		if err := first.LogFile.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}

		second := newAP()
		s.setupAgentLogFile(second, &exec.Cmd{})
		t.Cleanup(func() { _ = second.LogFile.Close() })

		if second.LogFilePath != first.LogFilePath {
			t.Fatalf("log path changed between runs: %q then %q", first.LogFilePath, second.LogFilePath)
		}
		info, err := os.Stat(second.LogFilePath)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if second.LogStartOffset != info.Size() {
			t.Errorf("LogStartOffset = %d, want %d (pre-existing log size)", second.LogStartOffset, info.Size())
		}
		if second.LogStartOffset != int64(len(prior)) {
			t.Errorf("LogStartOffset = %d, want %d", second.LogStartOffset, len(prior))
		}
	})
}
