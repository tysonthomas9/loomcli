//go:build unix

package leadcontrol

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// startInOwnProcessGroup puts the app-server (the codex npm launcher and the
// native binary it spawns) in its own process group, so stopping it signals
// the whole group instead of leaving the native child running.
func startInOwnProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return signalProcessGroup(cmd.Process.Pid, syscall.SIGTERM) }
}

func killProcessGroup(pid int) error {
	return signalProcessGroup(pid, syscall.SIGKILL)
}

func signalProcessGroup(pid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pid, sig); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}

// cancelOnHangup cancels ctx when the lead's terminal hangs up. The web
// terminal closes the PTY when the server stops, and the kernel does the same
// when the server dies; either way `loom lead` gets SIGHUP and must stop the
// app-server before it exits, or the app-server is orphaned.
func cancelOnHangup(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, syscall.SIGHUP)
}

// ReapOrphanedCodexAppServers stops lead app-servers left behind by a lead
// runtime that died without stopping them (SIGKILL, or a crash before its
// hangup cleanup ran). It only touches processes whose parent is gone
// (reparented to PID 1) and whose command line is a Loom lead app-server
// (`app-server --listen` with a sqlite_home under Loom's codex-leads cache),
// so another server's live leads are never affected. Returns the pids it
// stopped.
func ReapOrphanedCodexAppServers() []int {
	return reapOrphanedCodexAppServers(codexLeadsBaseDir())
}

func reapOrphanedCodexAppServers(leadsBaseDir string) []int {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,ppid=,pgid=,command=").Output()
	if err != nil {
		return nil
	}
	orphans := orphanedCodexAppServers(string(out), leadsBaseDir)
	for _, p := range orphans {
		if p.pgid == p.pid {
			_ = signalProcessGroup(p.pid, syscall.SIGTERM)
		} else {
			_ = syscall.Kill(p.pid, syscall.SIGTERM)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	pids := make([]int, 0, len(orphans))
	for _, p := range orphans {
		for processAlive(p.pid) && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if processAlive(p.pid) {
			if p.pgid == p.pid {
				_ = killProcessGroup(p.pid)
			} else {
				_ = syscall.Kill(p.pid, syscall.SIGKILL)
			}
		}
		pids = append(pids, p.pid)
	}
	return pids
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	return err == nil && proc.Signal(syscall.Signal(0)) == nil
}
