//go:build unix

package leadcontrol

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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

// codexAppServerRecord is what a lead runtime writes next to its app-server
// so a later `loom serve` can tell a leftover it owns from anything else. A
// process is identified by pid plus start time, so a reused pid never matches.
type codexAppServerRecord struct {
	PID        int    `json:"pid"`
	Start      string `json:"start"`
	OwnerPID   int    `json:"owner_pid"`
	OwnerStart string `json:"owner_start"`
}

const codexAppServerRecordName = "app-server.json"

// recordCodexAppServer notes the app-server (its own process group) and this
// lead runtime as its owner under the lead's runtime home.
func recordCodexAppServer(runtimeHome string, pid int) error {
	start, err := processStartTime(pid)
	if err != nil {
		return err
	}
	ownerStart, err := processStartTime(os.Getpid())
	if err != nil {
		return err
	}
	data, err := json.Marshal(codexAppServerRecord{PID: pid, Start: start, OwnerPID: os.Getpid(), OwnerStart: ownerStart})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runtimeHome, codexAppServerRecordName), data, 0o600)
}

// forgetCodexAppServer drops the record once the runtime has stopped its
// app-server itself.
func forgetCodexAppServer(runtimeHome string) {
	_ = os.Remove(filepath.Join(runtimeHome, codexAppServerRecordName))
}

// ReapOrphanedCodexAppServers stops lead app-servers that Loom started and
// recorded, whose lead runtime has since died without stopping them (SIGKILL,
// or a crash before its hangup cleanup ran). Only recorded processes are
// touched, and only while their pid and start time still match: never a
// process matched by its command line, so a user's own Codex is safe, and
// never one whose owning lead runtime is still alive. Returns the pids it
// stopped.
func ReapOrphanedCodexAppServers() []int {
	return reapOrphanedCodexAppServers(codexLeadsBaseDir())
}

func reapOrphanedCodexAppServers(leadsBaseDir string) []int {
	files, _ := filepath.Glob(filepath.Join(leadsBaseDir, "*", "*", "*", codexAppServerRecordName))
	var reaped []int
	for _, file := range files {
		// #nosec G304 -- file is a record Loom wrote under its own codex-leads cache.
		data, err := os.ReadFile(file)
		var rec codexAppServerRecord
		if err != nil || json.Unmarshal(data, &rec) != nil || rec.PID <= 1 {
			continue
		}
		if sameProcess(rec.OwnerPID, rec.OwnerStart) {
			continue // its lead runtime is alive and still owns it
		}
		if sameProcess(rec.PID, rec.Start) {
			stopRecordedAppServer(rec.PID)
			reaped = append(reaped, rec.PID)
		}
		_ = os.Remove(file)
	}
	return reaped
}

func sameProcess(pid int, start string) bool {
	if pid <= 0 || start == "" {
		return false
	}
	got, err := processStartTime(pid)
	return err == nil && got == start
}

// stopRecordedAppServer stops a recorded app-server's process group (it was
// started with Setpgid, so its pgid is its pid): SIGTERM, then SIGKILL.
func stopRecordedAppServer(pgid int) {
	_ = signalProcessGroup(pgid, syscall.SIGTERM)
	deadline := time.Now().Add(3 * time.Second)
	for groupAlive(pgid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	_ = killProcessGroup(pgid)
}

func groupAlive(pgid int) bool {
	return syscall.Kill(-pgid, 0) == nil
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	return err == nil && proc.Signal(syscall.Signal(0)) == nil
}
