//go:build unix

package leadcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeCodex writes a stand-in codex binary that, like the npm launcher, runs
// a child and waits on it. Both ignore SIGTERM and SIGHUP so only a kill of
// the whole process group stops them. The child's pid is written to
// <dir>/child.pid.
func fakeCodex(t *testing.T, ignore string) (path, childPIDFile string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "codex")
	childPIDFile = filepath.Join(dir, "child.pid")
	script := "#!/bin/sh\ntrap '' " + ignore + "\nsleep 300 &\necho $! > " + childPIDFile + "\nwait\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, childPIDFile
}

func waitForPID(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(file); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no pid in %s", file)
	return 0
}

func waitGone(t *testing.T, pid int, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("%s (pid %d) outlived the stop", what, pid)
	}
}

// B1: stopping the lead's app-server stops the native codex child too, even
// when it ignores SIGTERM, instead of orphaning it.
func TestStopCodexAppServerStopsTheWholeProcessGroup(t *testing.T) {
	bin, childPIDFile := fakeCodex(t, "TERM HUP")
	runtimeHome := t.TempDir()
	cfg := CodexLeadRuntimeConfig{CodexPath: bin, WorkDir: t.TempDir()}
	cmd, appErr, cancel, logFile, err := startCodexAppServer(context.Background(), cfg, runtimeHome, filepath.Join(runtimeHome, "sqlite"), "ws://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logFile.Close() }()
	child := waitForPID(t, childPIDFile)

	_ = stopCodexAppServer(cmd, appErr, cancel)
	waitGone(t, child, "app-server's native child")
}

// B1: when the npm launcher exits on SIGTERM but its native child does not,
// the child is still stopped instead of being left running.
func TestStopCodexAppServerStopsAChildThatOutlivesTheLauncher(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	childPIDFile := filepath.Join(dir, "child.pid")
	// The child reports its pid only once it ignores SIGTERM.
	script := "#!/bin/sh\n/bin/sh -c 'trap \"\" TERM; echo $$ > " + childPIDFile + "; while :; do sleep 1; done' &\nwait\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runtimeHome := t.TempDir()
	cfg := CodexLeadRuntimeConfig{CodexPath: bin, WorkDir: t.TempDir()}
	cmd, appErr, cancel, logFile, err := startCodexAppServer(context.Background(), cfg, runtimeHome, filepath.Join(runtimeHome, "sqlite"), "ws://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logFile.Close() }()
	child := waitForPID(t, childPIDFile)

	_ = stopCodexAppServer(cmd, appErr, cancel)
	waitGone(t, child, "native child that outlived the launcher")
}

// B1: the lead runtime records the app-server's launcher and, once known, its
// native child, and keeps the record when the app-server could not be stopped.
func TestCodexAppServerRecordForCrashCleanup(t *testing.T) {
	bin, childPIDFile := fakeCodex(t, "HUP")
	runtimeHome := t.TempDir()
	cfg := CodexLeadRuntimeConfig{CodexPath: bin, WorkDir: t.TempDir()}
	cmd, appErr, cancel, logFile, err := startCodexAppServer(context.Background(), cfg, runtimeHome, filepath.Join(runtimeHome, "sqlite"), "ws://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logFile.Close() }()
	defer func() { _ = stopCodexAppServer(cmd, appErr, cancel) }()
	if rec := readRecord(t, runtimeHome); rec.PGID != cmd.Process.Pid || rec.Owner.PID != os.Getpid() || len(rec.Processes) == 0 || rec.Processes[0].PID != cmd.Process.Pid {
		t.Fatalf("record %+v does not name launcher %d owned by %d", rec, cmd.Process.Pid, os.Getpid())
	}
	child := waitForPID(t, childPIDFile)
	if err := recordCodexAppServer(runtimeHome, cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if !recordHasPID(readRecord(t, runtimeHome), child) {
		t.Fatalf("record %+v lacks the native child %d", readRecord(t, runtimeHome), child)
	}

	_ = stopAndForgetCodexAppServer(runtimeHome, func() error { return errCodexAppServerStillRunning })
	if _, err := os.Stat(filepath.Join(runtimeHome, codexAppServerRecordName)); err != nil {
		t.Fatalf("record dropped although the app-server would not stop: %v", err)
	}
	_ = stopAndForgetCodexAppServer(runtimeHome, func() error { return errors.New("signal: terminated") })
	if _, err := os.Stat(filepath.Join(runtimeHome, codexAppServerRecordName)); !os.IsNotExist(err) {
		t.Fatalf("record still present after a clean stop: %v", err)
	}
}

func recordHasPID(rec codexAppServerRecord, pid int) bool {
	for _, p := range rec.Processes {
		if p.PID == pid {
			return true
		}
	}
	return false
}

func readRecord(t *testing.T, runtimeHome string) codexAppServerRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(runtimeHome, codexAppServerRecordName))
	if err != nil {
		t.Fatal(err)
	}
	var rec codexAppServerRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// B1: a restart reaps only app-servers Loom recorded whose owning lead runtime
// is gone. A process with the same command line that Loom never recorded (a
// user's own Codex), a recorded pid whose start time no longer matches (pid
// reuse) and a recorded app-server whose lead runtime is alive all survive.
func TestReapOrphanedCodexAppServersReapsOnlyRecordedOrphans(t *testing.T) {
	base := filepath.Join(t.TempDir(), "codex-leads")
	bin, _ := fakeCodex(t, "HUP")
	start := func(session string) *exec.Cmd {
		home := filepath.Join(base, "ws", "lead", session)
		if err := os.MkdirAll(home, 0o700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bin, codexAppServerArgs("ws://127.0.0.1:1", filepath.Join(home, "sqlite"), "/work")...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
		return cmd
	}
	startOf := func(pid int) string {
		s, err := processStartTime(pid)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	write := func(session string, rec codexAppServerRecord) {
		data, _ := json.Marshal(rec)
		if err := os.WriteFile(filepath.Join(base, "ws", "lead", session, codexAppServerRecordName), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	deadOwner := exec.Command("/bin/sh", "-c", "exit 0")
	if err := deadOwner.Run(); err != nil {
		t.Fatal(err)
	}

	users := start("users-own-codex")
	reused := start("reused-pid")
	gone := recordedProcess{PID: deadOwner.Process.Pid, Start: "gone"}
	write("reused-pid", codexAppServerRecord{PGID: reused.Process.Pid, Processes: []recordedProcess{{PID: reused.Process.Pid, Start: "an earlier process"}}, Owner: gone})
	live := start("live-lead")
	write("live-lead", codexAppServerRecord{PGID: live.Process.Pid, Processes: []recordedProcess{{PID: live.Process.Pid, Start: startOf(live.Process.Pid)}}, Owner: recordedProcess{PID: os.Getpid(), Start: startOf(os.Getpid())}})
	orphan := start("orphan")
	write("orphan", codexAppServerRecord{PGID: orphan.Process.Pid, Processes: []recordedProcess{{PID: orphan.Process.Pid, Start: startOf(orphan.Process.Pid)}}, Owner: gone})
	// The launcher died abruptly; its recorded native child lives on in the group.
	headless, headlessChild := startHeadless(t, base)
	write("headless", codexAppServerRecord{PGID: headless, Processes: []recordedProcess{{PID: headless, Start: "launcher, now dead"}, {PID: headlessChild, Start: startOf(headlessChild)}}, Owner: gone})
	if err := syscall.Kill(headless, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	reaped := reapOrphanedCodexAppServers(base)
	if len(reaped) != 2 || !((reaped[0] == orphan.Process.Pid && reaped[1] == headless) || (reaped[1] == orphan.Process.Pid && reaped[0] == headless)) {
		t.Fatalf("reaped %v, want only the recorded orphan %d and headless group %d", reaped, orphan.Process.Pid, headless)
	}
	_ = orphan.Wait()
	waitGone(t, headlessChild, "native child whose launcher died")
	for name, cmd := range map[string]*exec.Cmd{"unrecorded user Codex": users, "pid-reused record": reused, "live lead's app-server": live} {
		if !processAlive(cmd.Process.Pid) {
			t.Fatalf("reaper stopped the %s (pid %d)", name, cmd.Process.Pid)
		}
	}
	for session, want := range map[string]bool{"orphan": false, "headless": false, "reused-pid": false, "live-lead": true} {
		_, err := os.Stat(filepath.Join(base, "ws", "lead", session, codexAppServerRecordName))
		if got := err == nil; got != want {
			t.Fatalf("record for %s present=%v, want %v", session, got, want)
		}
	}
}

// B1: when the lead's terminal hangs up (the server stopped or died), the lead
// runtime's context is cancelled so it stops its app-server before exiting,
// instead of the default SIGHUP action killing it on the spot.
func TestCancelOnHangupCancelsOnSIGHUP(t *testing.T) {
	ctx, stop := cancelOnHangup(context.Background())
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("SIGHUP did not cancel the lead runtime context")
	}
}

// startHeadless starts a recorded-style app-server group (launcher with its own
// process group and a native child) and returns both pids.
func startHeadless(t *testing.T, base string) (int, int) {
	t.Helper()
	bin, childPIDFile := fakeCodex(t, "HUP")
	home := filepath.Join(base, "ws", "lead", "headless")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, codexAppServerArgs("ws://127.0.0.1:1", filepath.Join(home, "sqlite"), "/work")...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	child := waitForPID(t, childPIDFile)
	return cmd.Process.Pid, child
}
