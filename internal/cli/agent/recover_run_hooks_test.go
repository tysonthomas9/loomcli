package agent

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func TestRunRecoverNoLockUsesRecoveryHooks(t *testing.T) {
	restore := replaceRecoverRunHooks(t)
	defer restore()

	var reset struct {
		worktreePath string
		agentName    string
		taskID       string
		analyze      bool
	}
	resolveRecoverWorktreePathFn = func(name string) (string, error) {
		if name != "spark" {
			t.Fatalf("worktree name = %q", name)
		}
		return "/worktrees/spark", nil
	}
	checkRecoverLockFn = func(path string) (*cli.LockInfo, bool, error) {
		if path != "/worktrees/spark" {
			t.Fatalf("lock path = %q", path)
		}
		return nil, false, nil
	}
	resetRecoverOrphanedTasksFn = func(_ *cli.Deps, worktreePath, agentName, taskID string, analyze bool) {
		reset.worktreePath = worktreePath
		reset.agentName = agentName
		reset.taskID = taskID
		reset.analyze = analyze
	}

	oldNoAnalyze := recoverNoAnalyze
	recoverNoAnalyze = false
	t.Cleanup(func() { recoverNoAnalyze = oldNoAnalyze })

	out := captureRecoverStdout(t, func() { runRecover(nil, []string{"spark"}) })
	if !strings.Contains(out, "No lock file found") || !strings.Contains(out, "Agent is ready") {
		t.Fatalf("recover output = %q", out)
	}
	if reset.worktreePath != "/worktrees/spark" || reset.agentName != "spark" || reset.taskID != "" || !reset.analyze {
		t.Fatalf("reset hook = %+v", reset)
	}
}

func TestRunRecoverStaleLockUsesRecoveryHooks(t *testing.T) {
	restore := replaceRecoverRunHooks(t)
	defer restore()

	resolveRecoverWorktreePathFn = func(string) (string, error) { return "/worktrees/nova", nil }
	checkRecoverLockFn = func(string) (*cli.LockInfo, bool, error) {
		return &cli.LockInfo{PID: 42, AgentName: "locked-agent", TaskID: "TASK-1"}, false, nil
	}

	var clearedPID int
	var orphanedTask string
	var resetAnalyze bool
	var cleanedForce bool
	clearRecoverStaleLockFn = func(_ string, pid int) { clearedPID = pid }
	handleRecoverOrphanedTaskFn = func(_ *cli.Deps, _ string, taskID string, analyze bool) {
		orphanedTask = taskID
		resetAnalyze = analyze
	}
	resetRecoverOrphanedTasksFn = func(_ *cli.Deps, _, _, _ string, analyze bool) {
		resetAnalyze = resetAnalyze || analyze
	}
	cleanRecoverUntrackedFilesFn = func(_ string, force bool) { cleanedForce = force }

	oldNoAnalyze, oldForce := recoverNoAnalyze, recoverForce
	recoverNoAnalyze, recoverForce = true, true
	t.Cleanup(func() {
		recoverNoAnalyze, recoverForce = oldNoAnalyze, oldForce
	})

	out := captureRecoverStdout(t, func() { runRecover(nil, []string{"nova"}) })
	if !strings.Contains(out, "recovered and ready") {
		t.Fatalf("recover output = %q", out)
	}
	if clearedPID != 42 || orphanedTask != "TASK-1" || resetAnalyze || !cleanedForce {
		t.Fatalf("cleared=%d task=%q analyze=%v force=%v", clearedPID, orphanedTask, resetAnalyze, cleanedForce)
	}
}

func replaceRecoverRunHooks(t *testing.T) func() {
	t.Helper()
	oldResolve := resolveRecoverWorktreePathFn
	oldCheck := checkRecoverLockFn
	oldRunning := handleRunningAgentFn
	oldClear := clearRecoverStaleLockFn
	oldOrphan := handleRecoverOrphanedTaskFn
	oldReset := resetRecoverOrphanedTasksFn
	oldClean := cleanRecoverUntrackedFilesFn
	return func() {
		resolveRecoverWorktreePathFn = oldResolve
		checkRecoverLockFn = oldCheck
		handleRunningAgentFn = oldRunning
		clearRecoverStaleLockFn = oldClear
		handleRecoverOrphanedTaskFn = oldOrphan
		resetRecoverOrphanedTasksFn = oldReset
		cleanRecoverUntrackedFilesFn = oldClean
	}
}

func captureRecoverStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
		_ = r.Close()
	}()

	fn()
	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy stdout: %v", err)
	}
	return buf.String()
}
