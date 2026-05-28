//go:build unix

package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

type signalCall struct {
	PGID int
	PID  int
	Sig  syscall.Signal
}

// TestStopAgent_KillsIsolatedGrandchildProcessGroup covers the StopAgent
// contract without creating a real orphaned child. The process inspector seam
// presents a worker plus a backend child in a separate pgroup, and the signal
// seam records the process groups StopAgent would signal.
func TestStopAgent_KillsIsolatedGrandchildProcessGroup(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "test-orphan"},
		Cmd:          &exec.Cmd{Process: &os.Process{}},
		Pid:          200,
		WorktreePath: t.TempDir(),
	}

	oldInspector := procInspector
	oldCurrentProcessGroup := currentProcessGroup
	oldSignalProcessGroup := signalProcessGroup
	oldProcessIsRunning := processIsRunning
	oldSleepProcessPoll := sleepProcessPoll
	var calls []signalCall
	procInspector = processInspector{
		List: func() ([]procInfo, error) {
			return []procInfo{
				{PID: 200, PPID: 100, PGID: 200},
				{PID: 201, PPID: 200, PGID: 300},
			}, nil
		},
		CWD:  oldInspector.CWD,
		CWDs: oldInspector.CWDs,
	}
	currentProcessGroup = func() int { return 999 }
	signalProcessGroup = func(pgid int, sig syscall.Signal) error {
		calls = append(calls, signalCall{PGID: pgid, Sig: sig})
		return nil
	}
	processIsRunning = func(int) bool { return false }
	sleepProcessPoll = func(time.Duration) {}
	t.Cleanup(func() {
		procInspector = oldInspector
		currentProcessGroup = oldCurrentProcessGroup
		signalProcessGroup = oldSignalProcessGroup
		processIsRunning = oldProcessIsRunning
		sleepProcessPoll = oldSleepProcessPoll
	})

	s.StopAgent(ap, 2*time.Second)

	want := []signalCall{
		{PGID: 200, Sig: syscall.SIGTERM},
		{PGID: 300, Sig: syscall.SIGTERM},
		{PGID: 300, Sig: syscall.SIGKILL},
	}
	if len(calls) != len(want) {
		t.Fatalf("signal calls = %+v, want %+v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("signal calls = %+v, want %+v", calls, want)
		}
	}
}

// TestKillOrphanedWorktreeProcesses_StartupSweep is the acceptance test for
// the "didn't shut down cleanly last time" safety net. It feeds a reparented
// backend candidate through the process inspector seam, then verifies the
// startup sweep signals the owning process group.
func TestKillOrphanedWorktreeProcesses_StartupSweep(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	worktreeDir := t.TempDir()
	resolvedWorktree, err := filepath.EvalSymlinks(worktreeDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", worktreeDir, err)
	}

	oldInspector := procInspector
	oldCurrentProcessGroup := currentProcessGroup
	oldSignalProcessGroup := signalProcessGroup
	oldSignalProcessID := signalProcessID
	var calls []signalCall
	procInspector = processInspector{
		List: func() ([]procInfo, error) {
			return []procInfo{{PID: 201, PPID: 1, PGID: 300}}, nil
		},
		CWD: func(pid int) (string, error) {
			if pid == 201 {
				return resolvedWorktree, nil
			}
			return "", nil
		},
		CWDs: func(pids []int) (map[int]string, error) {
			return map[int]string{201: resolvedWorktree}, nil
		},
	}
	currentProcessGroup = func() int { return 999 }
	signalProcessGroup = func(pgid int, sig syscall.Signal) error {
		calls = append(calls, signalCall{PGID: pgid, Sig: sig})
		return nil
	}
	signalProcessID = func(pid int, sig syscall.Signal) error {
		calls = append(calls, signalCall{PID: pid, Sig: sig})
		return syscall.ESRCH
	}
	t.Cleanup(func() {
		procInspector = oldInspector
		currentProcessGroup = oldCurrentProcessGroup
		signalProcessGroup = oldSignalProcessGroup
		signalProcessID = oldSignalProcessID
	})

	killed := s.killOrphanedWorktreeProcesses([]string{worktreeDir})
	if killed == 0 {
		t.Fatalf("startup sweep found no orphans for %q", worktreeDir)
	}

	want := []signalCall{
		{PGID: 300, Sig: syscall.SIGTERM},
		{PID: 201, Sig: 0},
		{PGID: 300, Sig: syscall.SIGKILL},
	}
	if len(calls) != len(want) {
		t.Fatalf("signal calls = %+v, want %+v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("signal calls = %+v, want %+v", calls, want)
		}
	}
}

// TestFindWorktreeOrphans_PrefixesMatchOnResolvedPath covers the
// macOS-specific case where lsof reports the resolved cwd (e.g.
// /private/var/folders/...) but the configured worktree path uses the
// symlinked form (/var/folders/...). The sweep must resolve symlinks before
// prefix matching or it will miss every orphan on Darwin.
func TestFindWorktreeOrphans_PrefixesMatchOnResolvedPath(t *testing.T) {
	worktreeDir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(worktreeDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", worktreeDir, err)
	}
	cwd := filepath.Join(resolved, "nested")

	oldInspector := procInspector
	procInspector = processInspector{
		List: func() ([]procInfo, error) {
			return []procInfo{{PID: 4242, PPID: 1, PGID: 4242}}, nil
		},
		CWDs: func(pids []int) (map[int]string, error) {
			return map[int]string{4242: cwd}, nil
		},
	}
	t.Cleanup(func() { procInspector = oldInspector })

	got := findWorktreeOrphans([]string{worktreeDir})
	if len(got) != 1 {
		t.Fatalf("findWorktreeOrphans returned %d candidates, want 1: %+v", len(got), got)
	}
	if got[0].PID != 4242 || got[0].CWD != cwd || got[0].Worktree != resolved {
		t.Fatalf("orphan = %+v, want pid=4242 cwd=%q worktree=%q", got[0], cwd, resolved)
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

func TestKillOrphanedWorktreeProcesses_GroupsSignalsByPgroup(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	worktreeDir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(worktreeDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", worktreeDir, err)
	}

	oldInspector := procInspector
	oldCurrentProcessGroup := currentProcessGroup
	oldSignalProcessGroup := signalProcessGroup
	oldSignalProcessID := signalProcessID
	var calls []signalCall
	procInspector = processInspector{
		List: func() ([]procInfo, error) {
			return []procInfo{
				{PID: 201, PPID: 1, PGID: 300},
				{PID: 202, PPID: 1, PGID: 300},
				{PID: 203, PPID: 1, PGID: 400},
			}, nil
		},
		CWDs: func(pids []int) (map[int]string, error) {
			return map[int]string{
				201: resolved,
				202: filepath.Join(resolved, "nested"),
				203: resolved,
			}, nil
		},
	}
	currentProcessGroup = func() int { return 999 }
	signalProcessGroup = func(pgid int, sig syscall.Signal) error {
		calls = append(calls, signalCall{PGID: pgid, Sig: sig})
		return nil
	}
	signalProcessID = func(pid int, sig syscall.Signal) error {
		calls = append(calls, signalCall{PID: pid, Sig: sig})
		return syscall.ESRCH
	}
	t.Cleanup(func() {
		procInspector = oldInspector
		currentProcessGroup = oldCurrentProcessGroup
		signalProcessGroup = oldSignalProcessGroup
		signalProcessID = oldSignalProcessID
	})

	if killed := s.killOrphanedWorktreeProcesses([]string{worktreeDir}); killed != 3 {
		t.Fatalf("killed = %d, want 3", killed)
	}

	termGroups := map[int]int{}
	killGroups := map[int]int{}
	for _, call := range calls {
		if call.PGID == 0 {
			continue
		}
		switch call.Sig {
		case syscall.SIGTERM:
			termGroups[call.PGID]++
		case syscall.SIGKILL:
			killGroups[call.PGID]++
		}
	}
	if termGroups[300] != 1 || termGroups[400] != 1 || len(termGroups) != 2 {
		t.Fatalf("SIGTERM groups = %+v, want one signal for pgids 300 and 400", termGroups)
	}
	if killGroups[300] != 1 || killGroups[400] != 1 || len(killGroups) != 2 {
		t.Fatalf("SIGKILL groups = %+v, want one signal for pgids 300 and 400", killGroups)
	}
}
