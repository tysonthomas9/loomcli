package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/usage"

	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
)

type testBackend struct {
	err   error
	calls []struct {
		workDir   string
		prompt    string
		agentName string
	}
}

func (b *testBackend) Name() string { return "test-agent-backend" }

func (b *testBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	b.calls = append(b.calls, struct {
		workDir   string
		prompt    string
		agentName string
	}{workDir: workDir, prompt: prompt, agentName: agentName})
	return b.err
}

func (b *testBackend) InvokeNonInteractive(string, string, string, <-chan struct{}, *usage.Collector) error {
	return nil
}

func TestRunAgentDaemonAndSingleTaskInvokeRegisteredBackend(t *testing.T) {
	cli.TestingResetBackendState(t)
	backend := &testBackend{}
	cli.RegisterBackend(backend)
	if err := cli.SetBackend(backend.Name()); err != nil {
		t.Fatalf("SetBackend: %v", err)
	}

	worktree := t.TempDir()
	t.Cleanup(func() { _ = cli.ReleaseLock(worktree) })
	promptGen := func(name string, _ *cfgpkg.WorkspaceConfig) string {
		return "prompt for " + name
	}

	runAgentDaemon(worktree, "daemon-agent", promptGen)
	if len(backend.calls) != 1 {
		t.Fatalf("daemon calls = %d, want 1", len(backend.calls))
	}
	if backend.calls[0].workDir != worktree || backend.calls[0].agentName != "daemon-agent" ||
		!strings.Contains(backend.calls[0].prompt, "daemon-agent") {
		t.Fatalf("daemon call = %+v", backend.calls[0])
	}
	if info, err := cli.ReadLockFile(worktree); err != nil || info.State != cli.StateActive {
		t.Fatalf("daemon lock info=%+v err=%v", info, err)
	}
	_ = cli.ReleaseLock(worktree)

	oldPrompt, oldFilter := agentPromptFile, agentTaskFilter
	t.Cleanup(func() { agentPromptFile, agentTaskFilter = oldPrompt, oldFilter })
	agentPromptFile = "prompt.txt"
	agentTaskFilter = "any"
	runAgentSingleTask(worktree, "single-agent", promptGen, func() (bool, error) { return true, nil })
	if len(backend.calls) != 2 {
		t.Fatalf("single-task calls = %d, want 2", len(backend.calls))
	}
	if backend.calls[1].agentName != "single-agent" || !strings.Contains(backend.calls[1].prompt, "single-agent") {
		t.Fatalf("single-task call = %+v", backend.calls[1])
	}
}

type exitPanic int

func installExitPanic(t *testing.T) {
	t.Helper()
	cli.TestingSetExitProcess(t, func(code int) { panic(exitPanic(code)) })
}

func requireExit(t *testing.T, want int, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected exit %d, got none", want)
		}
		code, ok := r.(exitPanic)
		if !ok || int(code) != want {
			t.Fatalf("panic = %#v, want exit %d", r, want)
		}
	}()
	fn()
}

func TestAgentExitBranchesWithInjectedExit(t *testing.T) {
	installExitPanic(t)

	missingPrompt := t.TempDir() + "/missing.txt"
	requireExit(t, 1, func() { validatePromptFile(missingPrompt) })
	requireExit(t, 1, func() { validatePromptFile(t.TempDir()) })

	oldValidate := validatePromptFileFn
	oldMap := mapTaskFilterFn
	oldResolve := resolveAgentTargetFn
	oldPrompt, oldFilter, oldAutoMode, oldDaemonMode := agentPromptFile, agentTaskFilter, agentAutoMode, agentDaemonMode
	t.Cleanup(func() {
		validatePromptFileFn = oldValidate
		mapTaskFilterFn = oldMap
		resolveAgentTargetFn = oldResolve
		agentPromptFile, agentTaskFilter, agentAutoMode, agentDaemonMode = oldPrompt, oldFilter, oldAutoMode, oldDaemonMode
	})
	validatePromptFileFn = func(string) {}
	agentPromptFile = "prompt.txt"
	agentTaskFilter = "bad-filter"
	agentAutoMode = false
	agentDaemonMode = false
	mapTaskFilterFn = func(string, string) (func() (bool, error), error) {
		return nil, errors.New("bad filter")
	}
	requireExit(t, 1, func() { runAgent(&cobra.Command{}, []string{"agent-a"}) })

	mapTaskFilterFn = func(string, string) (func() (bool, error), error) {
		return func() (bool, error) { return true, nil }, nil
	}
	resolveAgentTargetFn = func(string, string) (workspace.ResolvedTarget, error) {
		return workspace.ResolvedTarget{}, errors.New("missing target")
	}
	requireExit(t, 1, func() { runAgent(&cobra.Command{}, []string{"agent-a"}) })
}

func TestAgentExecutionErrorBranchesWithInjectedExit(t *testing.T) {
	installExitPanic(t)
	cli.TestingResetBackendState(t)
	backend := &testBackend{}
	cli.RegisterBackend(backend)
	if err := cli.SetBackend(backend.Name()); err != nil {
		t.Fatalf("SetBackend: %v", err)
	}

	worktree := t.TempDir()
	if err := cli.AcquireLock(worktree, "agent", "holder"); err != nil {
		t.Fatalf("pre-acquire lock: %v", err)
	}
	requireExit(t, 1, func() {
		runAgentDaemon(worktree, "daemon-agent", func(string, *cfgpkg.WorkspaceConfig) string { return "prompt" })
	})
	_ = cli.ReleaseLock(worktree)

	backend.err = errors.New("agent failed")
	daemonWorktree := t.TempDir()
	requireExit(t, 1, func() {
		runAgentDaemon(daemonWorktree, "daemon-agent", func(string, *cfgpkg.WorkspaceConfig) string { return "prompt" })
	})
	_ = cli.ReleaseLock(daemonWorktree)

	taskWorktree := t.TempDir()
	oldPrompt, oldFilter := agentPromptFile, agentTaskFilter
	t.Cleanup(func() {
		agentPromptFile, agentTaskFilter = oldPrompt, oldFilter
		_ = cli.ReleaseLock(taskWorktree)
	})
	agentPromptFile = "prompt.txt"
	agentTaskFilter = "needs_design"
	requireExit(t, 1, func() {
		runAgentSingleTask(taskWorktree, "single-agent", func(string, *cfgpkg.WorkspaceConfig) string { return "prompt" }, func() (bool, error) {
			return false, errors.New("task check failed")
		})
	})

	backend.err = nil
	runAgentSingleTask(taskWorktree, "single-agent", func(string, *cfgpkg.WorkspaceConfig) string { return "prompt" }, func() (bool, error) {
		return false, nil
	})

	backend.err = errors.New("agent failed")
	requireExit(t, 1, func() {
		runAgentSingleTask(taskWorktree, "single-agent", func(string, *cfgpkg.WorkspaceConfig) string { return "prompt" }, func() (bool, error) {
			return true, nil
		})
	})
}

func TestClaimAndCompleteExitBranchesWithInjectedExit(t *testing.T) {
	installExitPanic(t)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	claimDir := t.TempDir()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(claimDir); err != nil {
		t.Fatalf("chdir claim dir: %v", err)
	}
	requireExit(t, 1, func() { runClaim(&cobra.Command{}, []string{"loom-1"}) })

	tmpFile := filepath.Join(t.TempDir(), "tmp-file")
	if err := os.WriteFile(tmpFile, []byte("not a dir"), 0600); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}
	t.Setenv("TMPDIR", tmpFile)
	t.Setenv("LOOM_WORKTREE_PATH", claimDir)
	requireExit(t, 1, func() { runComplete(&cobra.Command{}, nil) })
}
