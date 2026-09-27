//go:build unix

package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// newMirrorTestSupervisor builds a Supervisor whose daemon log dir and archive
// root both live under t.TempDir(), so no test touches the real ~/.loom/logs.
// logDir == "" disables the daemon log sink (archive-only case).
func newMirrorTestSupervisor(t *testing.T, logDir string) (*Supervisor, *AgentProcess, string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", tmp)
	t.Setenv("LOOM_CONFIG_DIR", "")

	resolved := logDir
	if resolved == "daemon-logs" {
		resolved = filepath.Join(tmp, "daemon-logs")
	}

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{LogDir: resolved}}
		},
		ProjectDir:    tmp,
		WorkspaceID:   "ws-test",
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		EmitEvent:     func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "ember", Role: "plan"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent"},
		WorktreePath: tmp,
	}
	return s, ap, tmp
}

// setupAndMirror runs the production wiring the way spawnAgent does: set up the
// sinks, then start the mirror. Returns the stop func (nil when no mirror runs).
func setupAndMirror(t *testing.T, s *Supervisor, ap *AgentProcess, cmd *exec.Cmd) func() {
	t.Helper()
	ap.Mu.Lock()
	s.setupAgentLogFile(ap, cmd)
	ap.stopLogMirror = s.startAgentLogMirror(ap)
	stop := ap.stopLogMirror
	ap.Mu.Unlock()
	t.Cleanup(func() {
		ap.Mu.Lock()
		closeAgentLogs(ap)
		ap.Mu.Unlock()
	})
	return stop
}

// TestSetupAgentLogFile_ChildAlwaysGetsOsFile is the invariant this whole
// ticket exists to protect. If cmd.Stdout/Stderr is anything other than an
// *os.File, os/exec allocates an os.Pipe plus a copy goroutine and cmd.Wait()
// cannot return until EVERY process holding the pipe's write end closes it —
// including backends the supervisor's descendant-pgroup snapshot missed. That
// is the PUPPET-39 shutdown stall. See PUPPET-49.
func TestSetupAgentLogFile_ChildAlwaysGetsOsFile(t *testing.T) {
	cases := []struct {
		name        string
		logDir      string
		wantLogFile bool
	}{
		{name: "both sinks", logDir: "daemon-logs", wantLogFile: true},
		{name: "archive only", logDir: "", wantLogFile: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ap, _ := newMirrorTestSupervisor(t, tc.logDir)
			cmd := &exec.Cmd{}
			setupAndMirror(t, s, ap, cmd)

			if _, ok := cmd.Stdout.(*os.File); !ok {
				t.Fatalf("cmd.Stdout is %T, want *os.File — a non-*os.File (e.g. io.MultiWriter) "+
					"makes os/exec insert a pipe and copy goroutine, so cmd.Wait() blocks until "+
					"every descendant closes the write end. See PUPPET-49.", cmd.Stdout)
			}
			if _, ok := cmd.Stderr.(*os.File); !ok {
				t.Fatalf("cmd.Stderr is %T, want *os.File (same reason as Stdout). See PUPPET-49.", cmd.Stderr)
			}
			if tc.wantLogFile && ap.LogFile == nil {
				t.Fatal("daemon LogFile was not opened")
			}
			if !tc.wantLogFile {
				if ap.LogFile != nil || ap.LogFilePath != "" {
					t.Fatal("daemon log sink should be disabled when LogDir is empty")
				}
				// Archive-only: watchdog tier 2 is skipped (checkWatchdog guards
				// logPath != ""), tiers 0/1 still apply. No mirror runs.
				if ap.stopLogMirror != nil {
					t.Error("no mirror should run when only one sink is open")
				}
			}
			if ap.ArchiveLogFile == nil {
				t.Fatal("ArchiveLogFile was not opened")
			}
		})
	}
}

// TestWaitReturnsWhileDescendantHoldsStdout is the regression test for the
// stall itself: a grandchild in its own pgroup inherits the agent's stdout and
// outlives it. Under the old io.MultiWriter wiring cmd.Wait() blocks on the
// os/exec copy goroutine until that grandchild exits; with a real *os.File it
// returns as soon as the direct child does.
func TestWaitReturnsWhileDescendantHoldsStdout(t *testing.T) {
	s, ap, _ := newMirrorTestSupervisor(t, "daemon-logs")

	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--") //nolint:norawexec,gosec // G204/norawexec: test helper self-exec
	childPIDFile := filepath.Join(t.TempDir(), "child.pid")
	cmd.Env = append(os.Environ(),
		"LOOM_TEST_HELPER_MODE=fake_worker_with_inherited_stdout_child",
		"LOOM_TEST_CHILD_PID_FILE="+childPIDFile,
		"LOOM_TEST_ROOT_PID="+strconv.Itoa(os.Getpid()),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	setupAndMirror(t, s, ap, cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}

	// Wait for the grandchild to exist and hold the inherited stdout fd.
	var grandchild int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(childPIDFile); err == nil { //nolint:gosec // G304: test-controlled path
			if pid, cerr := strconv.Atoi(string(b)); cerr == nil && pid > 0 {
				grandchild = pid
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if grandchild == 0 {
		_ = cmd.Process.Kill()
		t.Fatal("grandchild never reported its PID")
	}
	t.Cleanup(func() { _ = syscall.Kill(grandchild, syscall.SIGKILL) })

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-done:
		// The direct child exited; Wait returned without waiting on the
		// grandchild. Confirm the grandchild is genuinely still alive, or the
		// test proved nothing.
		if err := syscall.Kill(grandchild, 0); err != nil {
			t.Fatalf("grandchild %d already gone (%v) — the test did not exercise the stall", grandchild, err)
		}
	case <-time.After(15 * time.Second):
		_ = syscall.Kill(grandchild, syscall.SIGKILL)
		t.Fatal("cmd.Wait() did not return while a descendant held the inherited stdout — " +
			"os/exec is copying through a pipe instead of dup'ing an *os.File. See PUPPET-49.")
	}
}

// TestAgentLogMirror_CopiesToArchive checks the mirror reproduces exactly the
// bytes the child appended to the daemon log this cycle.
func TestAgentLogMirror_CopiesToArchive(t *testing.T) {
	s, ap, tmp := newMirrorTestSupervisor(t, "daemon-logs")
	cmd := &exec.Cmd{}
	stop := setupAndMirror(t, s, ap, cmd)
	if stop == nil {
		t.Fatal("mirror did not start with both sinks open")
	}

	var want string
	for i := range 20 {
		line := "agent output line " + strconv.Itoa(i) + "\n"
		if _, err := cmd.Stdout.Write([]byte(line)); err != nil {
			t.Fatalf("write: %v", err)
		}
		want += line
	}
	stop()

	archive := filepath.Join(tmp, ".loom", "logs", "ws-test", "agents", "ember.log")
	if got := readFile(t, archive); got != want {
		t.Errorf("archive content = %q, want %q", got, want)
	}
	if got := readFile(t, ap.LogFilePath); got != want {
		t.Errorf("daemon log content = %q, want %q", got, want)
	}
}

// TestAgentLogMirror_SkipsPreviousCycles guards LogFileStartOffset: the daemon
// log is opened O_APPEND and survives restarts, so cycle N must not re-emit
// cycle N-1's bytes into the archive.
func TestAgentLogMirror_SkipsPreviousCycles(t *testing.T) {
	s, ap, tmp := newMirrorTestSupervisor(t, "daemon-logs")

	// Seed the daemon log as an earlier cycle would have left it.
	logDir := filepath.Join(tmp, "daemon-logs", "ws-test")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seeded := filepath.Join(logDir, "plan-ember.log")
	if err := os.WriteFile(seeded, []byte("stale output from a previous cycle\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cmd := &exec.Cmd{}
	stop := setupAndMirror(t, s, ap, cmd)
	if stop == nil {
		t.Fatal("mirror did not start")
	}
	if ap.LogFileStartOffset == 0 {
		t.Fatal("LogFileStartOffset was not snapshotted from the pre-existing daemon log")
	}

	const line = "fresh line from this cycle\n"
	if _, err := cmd.Stdout.Write([]byte(line)); err != nil {
		t.Fatalf("write: %v", err)
	}
	stop()

	archive := filepath.Join(tmp, ".loom", "logs", "ws-test", "agents", "ember.log")
	if got := readFile(t, archive); got != line {
		t.Errorf("archive content = %q, want only this cycle's %q", got, line)
	}
}

// TestAgentLogMirror_FinalDrain guards the ordering in closeAgentLogs: output
// written immediately before the stop must still reach the archive, otherwise a
// dying agent's last (usually most interesting) lines are lost.
func TestAgentLogMirror_FinalDrain(t *testing.T) {
	s, ap, tmp := newMirrorTestSupervisor(t, "daemon-logs")
	cmd := &exec.Cmd{}
	stop := setupAndMirror(t, s, ap, cmd)
	if stop == nil {
		t.Fatal("mirror did not start")
	}

	const last = "panic: the very last thing the agent said\n"
	if _, err := cmd.Stdout.Write([]byte(last)); err != nil {
		t.Fatalf("write: %v", err)
	}
	// No sleep: the drain, not the ticker, must be what delivers this.
	stop()

	archive := filepath.Join(tmp, ".loom", "logs", "ws-test", "agents", "ember.log")
	if got := readFile(t, archive); got != last {
		t.Errorf("archive content = %q, want %q — final drain did not run before stop returned", got, last)
	}

	// Idempotent: closeAgentLogs runs on both the spawn-failure and exit paths.
	stop()
	stop()
}

// TestAgentLogMirror_SurvivesTruncation checks a rotated/truncated daemon log
// does not wedge the mirror on a stale offset.
func TestAgentLogMirror_SurvivesTruncation(t *testing.T) {
	s, ap, tmp := newMirrorTestSupervisor(t, "daemon-logs")
	cmd := &exec.Cmd{}
	stop := setupAndMirror(t, s, ap, cmd)
	if stop == nil {
		t.Fatal("mirror did not start")
	}

	if _, err := cmd.Stdout.Write([]byte("before truncation\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Let the mirror consume it, then truncate under its feet.
	time.Sleep(500 * time.Millisecond)
	if err := os.Truncate(ap.LogFilePath, 0); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	const after = "after truncation\n"
	// O_APPEND, so this lands at the new offset 0 — exactly the rotation shape
	// the mirror has to survive without spinning on a now-past-EOF position.
	if _, err := ap.LogFile.Write([]byte(after)); err != nil {
		t.Fatalf("write after truncate: %v", err)
	}
	stop()

	archive := filepath.Join(tmp, ".loom", "logs", "ws-test", "agents", "ember.log")
	got := readFile(t, archive)
	if !strings.Contains(got, "before truncation") {
		t.Errorf("archive lost pre-truncation output: %q", got)
	}
	if !strings.Contains(got, after) {
		t.Errorf("mirror stopped after truncation; archive = %q", got)
	}
}

// TestAgentLogMirror_WatchdogMtimeAdvances checks the tier-2 liveness signal
// survives the rewiring: the child's own write(2) must advance the daemon log's
// mtime, which is what checkWatchdog stats.
func TestAgentLogMirror_WatchdogMtimeAdvances(t *testing.T) {
	s, ap, _ := newMirrorTestSupervisor(t, "daemon-logs")
	cmd := &exec.Cmd{}
	setupAndMirror(t, s, ap, cmd)

	before, err := os.Stat(ap.LogFilePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := cmd.Stdout.Write([]byte("liveness\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	after, err := os.Stat(ap.LogFilePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !after.ModTime().After(before.ModTime()) {
		t.Errorf("daemon log mtime did not advance on child output (%v → %v); "+
			"watchdog tier 2 would go blind", before.ModTime(), after.ModTime())
	}
}

// TestCloseAgentLogs_NoMirror covers the cmd.Start() failure path, where
// closeAgentLogs runs with stopLogMirror still nil.
func TestCloseAgentLogs_NoMirror(t *testing.T) {
	s, ap, _ := newMirrorTestSupervisor(t, "daemon-logs")
	cmd := &exec.Cmd{}
	ap.Mu.Lock()
	s.setupAgentLogFile(ap, cmd)
	closeAgentLogs(ap) // must not panic with stopLogMirror == nil
	ap.Mu.Unlock()

	if ap.LogFile != nil || ap.ArchiveLogFile != nil || ap.stopLogMirror != nil {
		t.Error("closeAgentLogs left handles or the mirror stop func set")
	}
	if ap.LogFileStartOffset != 0 {
		t.Error("closeAgentLogs did not clear LogFileStartOffset")
	}
}
