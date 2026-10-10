//go:build unix

package leadcontrol

import (
	"context"
	"encoding/json"
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

// B1: the lead runtime records the app-server it starts and drops the record
// once it has stopped it itself.
func TestStartCodexAppServerRecordsItForCrashCleanup(t *testing.T) {
	bin, _ := fakeCodex(t, "HUP")
	runtimeHome := t.TempDir()
	cfg := CodexLeadRuntimeConfig{CodexPath: bin, WorkDir: t.TempDir()}
	cmd, appErr, cancel, logFile, err := startCodexAppServer(context.Background(), cfg, runtimeHome, filepath.Join(runtimeHome, "sqlite"), "ws://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logFile.Close() }()
	defer func() { _ = stopCodexAppServer(cmd, appErr, cancel) }()
	rec := readRecord(t, runtimeHome)
	if rec.PID != cmd.Process.Pid || rec.OwnerPID != os.Getpid() || rec.Start == "" || rec.OwnerStart == "" {
		t.Fatalf("record %+v does not name app-server %d owned by %d", rec, cmd.Process.Pid, os.Getpid())
	}
	forgetCodexAppServer(runtimeHome)
	if _, err := os.Stat(filepath.Join(runtimeHome, codexAppServerRecordName)); !os.IsNotExist(err) {
		t.Fatalf("record still present after forget: %v", err)
	}
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
	write("reused-pid", codexAppServerRecord{PID: reused.Process.Pid, Start: "an earlier process", OwnerPID: deadOwner.Process.Pid, OwnerStart: "gone"})
	live := start("live-lead")
	write("live-lead", codexAppServerRecord{PID: live.Process.Pid, Start: startOf(live.Process.Pid), OwnerPID: os.Getpid(), OwnerStart: startOf(os.Getpid())})
	orphan := start("orphan")
	write("orphan", codexAppServerRecord{PID: orphan.Process.Pid, Start: startOf(orphan.Process.Pid), OwnerPID: deadOwner.Process.Pid, OwnerStart: "gone"})

	reaped := reapOrphanedCodexAppServers(base)
	if len(reaped) != 1 || reaped[0] != orphan.Process.Pid {
		t.Fatalf("reaped %v, want only the recorded orphan %d", reaped, orphan.Process.Pid)
	}
	_ = orphan.Wait()
	for name, cmd := range map[string]*exec.Cmd{"unrecorded user Codex": users, "pid-reused record": reused, "live lead's app-server": live} {
		if !processAlive(cmd.Process.Pid) {
			t.Fatalf("reaper stopped the %s (pid %d)", name, cmd.Process.Pid)
		}
	}
	for session, want := range map[string]bool{"orphan": false, "reused-pid": false, "live-lead": true} {
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
