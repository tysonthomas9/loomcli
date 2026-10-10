//go:build unix

package leadcontrol

import (
	"context"
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

// B1: a restart reaps an app-server orphaned by a lead runtime that died, and
// leaves a live lead's app-server (its parent still running) alone.
func TestReapOrphanedCodexAppServers(t *testing.T) {
	base := filepath.Join(t.TempDir(), "codex-leads")
	bin, _ := fakeCodex(t, "HUP")
	args := func(port string) []string {
		return []string{bin, "app-server", "--listen", "ws://127.0.0.1:" + port, "-c", "sqlite_home=" + strconv.Quote(filepath.Join(base, "ws", "lead", port, "sqlite"))}
	}

	live := exec.Command(args("1")[0], args("1")[1:]...)
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Process.Kill(); _ = live.Wait() })

	// Orphan one: `set -m` gives the background job its own process group,
	// and the launching shell exits so the job is reparented to PID 1.
	pidFile := filepath.Join(t.TempDir(), "orphan.pid")
	quoted := make([]string, 0, 6)
	for _, a := range args("2") {
		quoted = append(quoted, strconv.Quote(a))
	}
	launcher := exec.Command("/bin/sh", "-c", "set -m; "+strings.Join(quoted, " ")+" >/dev/null 2>&1 & echo $! > "+strconv.Quote(pidFile))
	if err := launcher.Run(); err != nil {
		t.Fatal(err)
	}
	orphan := waitForPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(-orphan, syscall.SIGKILL) })
	deadline := time.Now().Add(3 * time.Second)
	for parentPID(orphan) != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if parentPID(orphan) != 1 {
		t.Skipf("orphans are not reparented to PID 1 here (ppid %d)", parentPID(orphan))
	}

	reaped := reapOrphanedCodexAppServers(base)
	if len(reaped) != 1 || reaped[0] != orphan {
		t.Fatalf("reaped %v, want only the orphan %d", reaped, orphan)
	}
	waitGone(t, orphan, "orphaned app-server")
	if !processAlive(live.Process.Pid) {
		t.Fatal("reaper stopped a live lead's app-server")
	}
}

func parentPID(pid int) int {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return -1
	}
	ppid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return ppid
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
