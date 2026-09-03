//go:build unix

package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// TestHelperProcess is the subprocess entry point. When invoked via
// `os.Args[0] -test.run=TestHelperProcess` with the env var set, it acts as a
// fake worker or fake backend instead of running a normal test. This avoids
// needing a separate compiled helper binary and works on macOS where setsid
// isn't installed.
func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("LOOM_TEST_HELPER_MODE")
	if mode == "" {
		return
	}

	// Guard against leaking helper subprocesses when the test binary dies
	// without running t.Cleanup (e.g. Ctrl-C / SIGKILL). The helpers run with
	// Setpgid:true, so they don't receive the terminal's SIGINT, and on signal
	// death the framework never fires the cleanups that would kill them — so
	// they would otherwise squat for the full sleep below, spawning a swarm
	// that the supervisor's per-PID lsof sweep amplifies into a load spike.
	watchRootPID()

	switch mode {
	case "fake_worker_with_isolated_child":
		runFakeWorkerWithIsolatedChild(t)
	case "fake_backend_sleep":
		runFakeBackendSleep(t)
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

// watchRootPID exits the helper process as soon as the test binary that
// started this chain (identified by LOOM_TEST_ROOT_PID) is gone. We watch the
// *root* test-binary PID rather than the immediate parent on purpose: the
// startup-sweep tests deliberately SIGKILL the intermediate worker to reparent
// the child to init (PPID==1), so a parent-death watch would kill those
// helpers prematurely. In that scenario the test binary is still alive, so the
// helper keeps running; on a real Ctrl-C the whole test binary dies and every
// descendant helper exits within a poll interval.
func watchRootPID() {
	v := os.Getenv("LOOM_TEST_ROOT_PID")
	if v == "" {
		return
	}
	root, err := strconv.Atoi(v)
	if err != nil || root <= 0 {
		return
	}
	go func() {
		for {
			if syscall.Kill(root, 0) != nil {
				os.Exit(0)
			}
			time.Sleep(250 * time.Millisecond)
		}
	}()
}

// runFakeWorkerWithIsolatedChild mimics what the real worker (`loom agent ...`)
// does to spawn a backend: it execs a child process with Setpgid:true so the
// child becomes leader of its own pgroup. The PID of that child is written to
// the file given by LOOM_TEST_CHILD_PID_FILE so the parent test can verify it
// gets killed.
//
// This helper deliberately does NOT install a signal handler that forwards to
// the child. That mirrors the bug condition: when the supervisor SIGKILLs the
// worker, no one is left to clean up the backend.
func runFakeWorkerWithIsolatedChild(_ *testing.T) {
	selfPath, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "executable:", err)
		os.Exit(1)
	}
	cmd := exec.Command(selfPath, "-test.run=TestHelperProcess", "--") //nolint:norawexec,gosec // G204/norawexec: test helper self-exec
	cmd.Env = append(os.Environ(), "LOOM_TEST_HELPER_MODE=fake_backend_sleep")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "start child:", err)
		os.Exit(1)
	}
	if path := os.Getenv("LOOM_TEST_CHILD_PID_FILE"); path != "" {
		_ = os.WriteFile(path, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	}
	// Sleep long enough that the test's StopAgent will hit us with SIGTERM
	// (its grace windows are ≤8s), but short enough to bound the blast radius
	// if this helper ever leaks past watchRootPID's safety net.
	time.Sleep(helperLingerSleep)
	os.Exit(0)
}

// runFakeBackendSleep just sleeps in its own pgroup. It is the codex stand-in.
func runFakeBackendSleep(_ *testing.T) {
	time.Sleep(helperLingerSleep)
	os.Exit(0)
}

// helperLingerSleep is how long a fake worker/backend lingers awaiting the
// signals the tests deliver.
//
// The "≤8s acceptance windows, so 15s is ample" reasoning it used to carry
// undercounted the startup-sweep test: that one spends up to 8s waiting for
// the kernel to reparent the orphan to init BEFORE its own 5s kill window
// opens, and the whole case measures 11–17s on an unloaded laptop. A loaded
// runner pushed it past 15s, the orphan self-exited, and the sweep then
// truthfully reported finding nothing — surfacing as
// "startup sweep found no orphans (child PID N alive=false)", a failure of
// the fixture rather than of the code under test.
//
// The bound exists only to stop a leaked helper squatting for minutes, and
// every test kills its helpers in cleanup, so it can afford to sit well clear
// of the slowest case. watchRootPID is the real safety net: a helper whose
// test binary dies exits within a poll interval regardless of this value.
const helperLingerSleep = 90 * time.Second

// spawnFakeWorker starts the helper "worker" with Setpgid:true and returns the
// *exec.Cmd plus the PID of the helper's isolated child (the codex stand-in).
// The caller is responsible for cleaning both up. The workerCwd argument
// becomes the working directory for both the worker and (inherited) the child.
func spawnFakeWorker(t *testing.T, workerCwd string) (*exec.Cmd, int) {
	t.Helper()
	selfPath, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	childPIDFile := filepath.Join(t.TempDir(), "child.pid")

	cmd := exec.Command(selfPath, "-test.run=TestHelperProcess", "--") //nolint:norawexec,gosec // G204/norawexec: test helper self-exec
	cmd.Env = append(os.Environ(),
		"LOOM_TEST_HELPER_MODE=fake_worker_with_isolated_child",
		"LOOM_TEST_CHILD_PID_FILE="+childPIDFile,
		// Let the worker (and, by inheritance, its isolated child) self-terminate
		// if this test binary dies without running cleanups. See watchRootPID.
		"LOOM_TEST_ROOT_PID="+strconv.Itoa(os.Getpid()),
	)
	cmd.Dir = workerCwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake worker: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(childPIDFile)
		if err == nil && len(data) > 0 {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && pid > 0 {
				return cmd, pid
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Wait()
	t.Fatal("timed out waiting for child PID file")
	return nil, 0
}

// TestStopAgent_KillsIsolatedGrandchildProcessGroup is the regression test for
// Problem 3: when a backend subprocess (e.g. codex) uses Setpgid:true it is
// invisible to syscall.Kill(-workerPID, SIGTERM) because it lives in its own
// pgroup. The fix walks the descendant tree and signals each distinct pgroup.
//
// Without the fix: the helper worker dies on SIGTERM, the isolated grandchild
// is reparented to init and survives well past the 5-second window.
// With the fix: StopAgent's descendant-pgroup sweep signals the grandchild's
// own pgroup, so it dies within the grace window.
func TestStopAgent_KillsIsolatedGrandchildProcessGroup(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	worktreeDir := t.TempDir()

	workerCmd, childPID := spawnFakeWorker(t, worktreeDir)
	workerPID := workerCmd.Process.Pid

	t.Cleanup(func() {
		_ = syscall.Kill(-workerPID, syscall.SIGKILL)
		_ = syscall.Kill(childPID, syscall.SIGKILL)
		_ = workerCmd.Wait()
	})

	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "test-orphan"},
		Cmd:          workerCmd,
		Pid:          workerPID,
		WorktreePath: worktreeDir,
	}

	// Reap the worker in the background so polling in StopAgent sees Pid==0.
	go func() {
		_ = workerCmd.Wait()
		ap.Mu.Lock()
		ap.Pid = 0
		ap.Mu.Unlock()
	}()

	// Sanity: child is alive before the kill.
	if !processAlive(childPID) {
		t.Fatalf("isolated grandchild PID %d should be alive before StopAgent", childPID)
	}

	s.StopAgent(ap, 2*time.Second)

	// Acceptance: no descendant of the spawned worker survives 5 seconds after
	// the kill.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(childPID) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Surface diagnostic context before failing.
	if ppid := readPPID(t, childPID); ppid > 0 {
		t.Logf("grandchild PID %d still alive, current PPID=%d", childPID, ppid)
	}
	t.Fatalf("isolated grandchild PID %d survived StopAgent — descendant pgroup not signaled", childPID)
}

// processAlive reports whether pid is still a live, non-zombie process. In a
// PID-1 test container, killed orphan descendants can remain as zombies until
// container exit; kill -0 still sees those PIDs, but they are no longer running.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if processZombie(pid) {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func processZombie(pid int) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output() //nolint:norawexec // test-only process status via ps
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}

// readPPID best-effort parses ps to return the current parent PID of pid (so a
// failed test can show whether the grandchild has been reparented to init).
func readPPID(t *testing.T, pid int) int {
	t.Helper()
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=").Output() //nolint:norawexec // test-only readPPID via ps
	if err != nil {
		return -1
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return -1
	}
	return v
}

// TestKillOrphanedWorktreeProcesses_StartupSweep is the acceptance test for
// the "didn't shut down cleanly last time" safety net. It simulates a daemon
// SIGKILL that left a backend reparented to init: spawn a fake worker that
// spawns an isolated child, then SIGKILL just the worker so the child becomes
// a true orphan with PPID==1. The startup sweep must find it by cwd and kill
// its pgroup with a log line per kill.
func TestKillOrphanedWorktreeProcesses_StartupSweep(t *testing.T) {
	if procInspector.List == nil {
		t.Skip("no process inspector on this platform")
	}
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	worktreeDir := t.TempDir()

	workerCmd, childPID := spawnFakeWorker(t, worktreeDir)
	workerPID := workerCmd.Process.Pid

	// Force-orphan the child by SIGKILLing only the worker's pgroup. The
	// fake_worker helper isn't in the child's pgroup (Setpgid makes them
	// independent), so the child survives and gets reparented to init.
	if err := syscall.Kill(-workerPID, syscall.SIGKILL); err != nil {
		t.Fatalf("SIGKILL worker: %v", err)
	}
	_ = workerCmd.Wait()

	// Reap the child if our cleanup fires before the sweep does.
	t.Cleanup(func() {
		_ = syscall.Kill(-childPID, syscall.SIGKILL)
	})

	// Wait for the kernel to reparent the child to init. Local runs see this
	// immediately; on a loaded CI runner the ps view can lag the kernel by
	// multiple poll cycles, so the window is intentionally generous.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if readPPID(t, childPID) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ppid := readPPID(t, childPID); ppid != 1 {
		t.Fatalf("child PID %d not yet reparented to init: PPID=%d", childPID, ppid)
	}

	// A dead orphan means the FIXTURE expired, not that the sweep missed
	// anything: there is nothing left to find. Say which failure this is, so
	// the next reader does not go looking for a bug in the sweep.
	if !processAlive(childPID) {
		t.Fatalf("fixture expired before the sweep ran: orphan PID %d already exited "+
			"(helperLingerSleep=%s elapsed too little for this run) — not a sweep failure",
			childPID, helperLingerSleep)
	}

	killed := s.killOrphanedWorktreeProcesses([]string{worktreeDir})
	if killed == 0 {
		t.Fatalf("startup sweep found no orphans for %q (child PID %d alive=%v)",
			worktreeDir, childPID, processAlive(childPID))
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(childPID) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("orphaned child PID %d survived startup sweep", childPID)
}

// TestFindWorktreeOrphans_PrefixesMatchOnResolvedPath covers the
// macOS-specific case where lsof reports the resolved cwd (e.g.
// /private/var/folders/...) but the configured worktree path uses the
// symlinked form (/var/folders/...). The sweep must resolve symlinks before
// prefix matching or it will miss every orphan on Darwin.
func TestFindWorktreeOrphans_PrefixesMatchOnResolvedPath(t *testing.T) {
	if procInspector.List == nil {
		t.Skip("no process inspector on this platform")
	}
	worktreeDir := t.TempDir()

	cmd := exec.Command("sleep", "15") //nolint:norawexec,gosec // G204/norawexec: fixed args
	cmd.Dir = worktreeDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	cwd, err := procInspector.CWD(pid)
	if err != nil || cwd == "" {
		t.Fatalf("CWD lookup failed: pid=%d err=%v cwd=%q", pid, err, cwd)
	}

	// Mimic the prefix-matching the sweep does. With symlink resolution this
	// must succeed even when worktreeDir uses /var and lsof returns /private/var.
	resolved, err := filepath.EvalSymlinks(worktreeDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", worktreeDir, err)
	}
	if !strings.HasPrefix(cwd, resolved) {
		t.Fatalf("cwd %q does not have prefix %q (worktreeDir=%q)", cwd, resolved, worktreeDir)
	}
}

// TestSignalableOrphans_ExcludesInitAndOwnPgroup is the regression test for the
// startup-sweep own-pgroup gap: killOrphanedWorktreeProcesses sends
// syscall.Kill(-pgid), so any candidate sharing the daemon's own process group
// (or pgroup 0/1) must be filtered out before signaling — otherwise the sweep
// can SIGKILL the daemon itself. The hung-process descendant path already
// guards this via findDescendantPGIDs; this confirms the sweep path matches.
func TestSignalableOrphans_ExcludesInitAndOwnPgroup(t *testing.T) {
	const ownPGID = 4242
	in := []orphanCandidate{
		{PID: 100, PGID: 0},       // kernel pgroup
		{PID: 101, PGID: 1},       // init pgroup
		{PID: 102, PGID: ownPGID}, // daemon's own pgroup — Kill(-pgid) is suicide
		{PID: 103, PGID: 5555},    // genuine orphan — must survive the filter
		{PID: 104, PGID: ownPGID}, // second member of the daemon's pgroup
	}

	got := signalableOrphans(in, ownPGID)

	if len(got) != 1 {
		t.Fatalf("expected exactly 1 signalable orphan, got %d: %+v", len(got), got)
	}
	if got[0].PID != 103 || got[0].PGID != 5555 {
		t.Fatalf("expected only the genuine orphan (PID 103, PGID 5555) to remain, got %+v", got[0])
	}
}

// TestKillOrphanedWorktreeProcesses_PgroupKillEndToEnd exercises the pgroup
// kill path used by the startup sweep: given a candidate list, sending
// SIGTERM to its pgroup stops the process within the grace window. The
// real "PPID==1 reparenting" condition is end-to-end covered by
// TestStopAgent_KillsIsolatedGrandchildProcessGroup above; this test just
// confirms the sweep's signal delivery works.
func TestKillOrphanedWorktreeProcesses_PgroupKillEndToEnd(t *testing.T) {
	if procInspector.List == nil {
		t.Skip("no process inspector on this platform")
	}
	worktreeDir := t.TempDir()

	cmd := exec.Command("sleep", "15") //nolint:norawexec,gosec // G204/norawexec: fixed args
	cmd.Dir = worktreeDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid

	// Reap in the background so processAlive (kill -0) doesn't see a zombie.
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-exited
	})

	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		t.Fatalf("kill pgroup: %v", err)
	}
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatalf("pgroup SIGTERM did not stop pid %d within 3s", pid)
	}
}

// TestPathMatches_CaseSensitivity pins the comparison itself: pure, no
// filesystem, no processes, so the case-sensitive branch is checkable on a
// case-insensitive dev host and vice versa.
func TestPathMatches_CaseSensitivity(t *testing.T) {
	cases := []struct {
		cwd             string
		worktree        string
		caseInsensitive bool
		want            bool
	}{
		{"/a/PUPPET/x", "/a/puppet", false, false},
		{"/a/PUPPET/x", "/a/puppet", true, true},
		{"/a/puppet/x", "/a/PUPPET", true, true},
		{"/a/puppet", "/a/PUPPET", true, true},
		{"/a/puppet", "/a/puppet", false, true},
		// The classic prefix bug: a sibling directory whose name merely starts
		// with the worktree's must not match, folded or not.
		{"/a/puppet-other/x", "/a/puppet", true, false},
		{"/a/puppet-other/x", "/a/puppet", false, false},
	}
	for _, tc := range cases {
		if got := pathMatches(tc.cwd, tc.worktree, tc.caseInsensitive); got != tc.want {
			t.Errorf("pathMatches(%q, %q, %v) = %v, want %v",
				tc.cwd, tc.worktree, tc.caseInsensitive, got, tc.want)
		}
	}
}

// TestPathIsCaseInsensitive exercises the probe. The answer for a real
// directory is host-dependent by design, so the assertion is self-consistency
// (a directory and its descendant live on the same filesystem) plus the two
// "cannot tell" paths, which must both return false without hanging.
func TestPathIsCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	mixed := filepath.Join(root, "Mixed")
	child := filepath.Join(mixed, "Inner")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if a, b := pathIsCaseInsensitive(mixed), pathIsCaseInsensitive(child); a != b {
		t.Errorf("probe disagrees on the same filesystem: %q=%v, %q=%v", mixed, a, child, b)
	}

	if pathIsCaseInsensitive(filepath.Join(root, "does-not-exist")) {
		t.Error("probe returned true for a nonexistent path; false is the safe answer")
	}

	// All-digit component: the upward walk must terminate rather than spin.
	digits := filepath.Join(root, "123")
	if err := os.MkdirAll(digits, 0o755); err != nil {
		t.Fatalf("mkdir digits: %v", err)
	}
	_ = pathIsCaseInsensitive(digits)
}

// TestFindWorktreeOrphans_MatchesDespiteCaseMismatch is the regression test for
// the silent-no-op sweep: the kernel reports a cwd in the directory's canonical
// on-disk case, while the configured worktree path carries whatever case the
// config used. It calls findWorktreeOrphans itself — a test that re-implements
// the match inline would pass while the bug survived.
//
// Direction matters: the directory created on disk is the LOWERCASE one and the
// path handed to findWorktreeOrphans the UPPERCASE one. The other way round the
// kernel would report the uppercase spelling and the test would pass unfixed.
func TestFindWorktreeOrphans_MatchesDespiteCaseMismatch(t *testing.T) {
	if procInspector.List == nil || procInspector.CWD == nil {
		t.Skip("no process inspector on this platform")
	}
	root := t.TempDir()
	lower := filepath.Join(root, "puppet", "wt")
	if err := os.MkdirAll(lower, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	upper := filepath.Join(root, "PUPPET", "wt") // never created; config spelling

	if !pathIsCaseInsensitive(lower) {
		t.Skip("filesystem is case-sensitive; the case-mismatch scenario cannot occur here")
	}

	cmd := exec.Command("sleep", "15") //nolint:norawexec,gosec // G204/norawexec: fixed args
	cmd.Dir = lower
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	// Stub only List, so the process looks reparented to init; the real CWD
	// lookup stays in place and reports the genuine kernel spelling.
	origList := procInspector.List
	procInspector.List = func() ([]procInfo, error) {
		return []procInfo{{PID: pid, PPID: 1, PGID: pid}}, nil
	}
	t.Cleanup(func() { procInspector.List = origList })

	got := findWorktreeOrphans([]string{upper})
	if len(got) != 1 {
		t.Fatalf("findWorktreeOrphans(%q) = %+v, want exactly 1 candidate for pid %d", upper, got, pid)
	}
	if got[0].PID != pid {
		t.Fatalf("candidate PID = %d, want %d", got[0].PID, pid)
	}
}
