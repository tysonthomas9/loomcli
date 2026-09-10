//go:build unix

package supervisor

import (
	"syscall"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// stubProcInspector swaps the package-level process inspector for the duration
// of a test so scoping can be asserted without spawning anything. The tests
// using it are not parallel, which is what makes the swap safe.
func stubProcInspector(t *testing.T, procs []procInfo, cwds map[int]string) {
	t.Helper()
	saved := procInspector
	t.Cleanup(func() { procInspector = saved })
	procInspector = processInspector{
		List: func() ([]procInfo, error) { return procs, nil },
		CWD:  func(pid int) (string, error) { return cwds[pid], nil },
	}
}

// TestFindWorktreeOrphans_ScopedToGivenWorktree pins the property the deadline
// backstop depends on: a sweep handed one worktree must consider only the
// processes whose cwd lives under it. A daemon supervising several agents runs
// this on the classification path of whichever agent hit the deadline, so a
// sibling agent's healthy backend must never turn up as a candidate.
func TestFindWorktreeOrphans_ScopedToGivenWorktree(t *testing.T) {
	const (
		mine    = "/tmp/loom-wt/agent-a"
		sibling = "/tmp/loom-wt/agent-b"
	)
	stubProcInspector(t,
		[]procInfo{
			{PID: 100, PPID: 1, PGID: 100},  // orphan in my worktree
			{PID: 101, PPID: 1, PGID: 101},  // orphan in a sibling's worktree
			{PID: 102, PPID: 55, PGID: 102}, // still parented — not an orphan
			{PID: 103, PPID: 1, PGID: 103},  // orphan outside every worktree
		},
		map[int]string{
			100: mine + "/repo",
			101: sibling + "/repo",
			102: mine + "/repo",
			103: "/tmp/somewhere-else",
		},
	)

	got := findWorktreeOrphans([]string{mine})
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 candidate scoped to %q, got %d: %+v", mine, len(got), got)
	}
	if got[0].PID != 100 {
		t.Fatalf("expected the orphan in my worktree (PID 100), got %+v", got[0])
	}

	// The all-worktrees form still sees both, so daemon-startup behavior is
	// unchanged by the extraction.
	if all := findWorktreeOrphans([]string{mine, sibling}); len(all) != 2 {
		t.Fatalf("all-worktrees scan expected 2 candidates, got %d: %+v", len(all), all)
	}
}

// TestSweepOrphanedBackendsForWorktree_SkipsEmptyPath guards the worst possible
// misfire: an agent with no recorded worktree path must skip the sweep, never
// fall back to scanning (and signaling) everything.
func TestSweepOrphanedBackendsForWorktree_SkipsEmptyPath(t *testing.T) {
	listed := false
	saved := procInspector
	t.Cleanup(func() { procInspector = saved })
	procInspector = processInspector{
		List: func() ([]procInfo, error) { listed = true; return nil, nil },
		CWD:  func(int) (string, error) { return "", nil },
	}

	s := newDrainTestSupervisor(&config.DaemonConfig{})
	for _, path := range []string{"", "   "} {
		if killed := s.sweepOrphanedBackendsForWorktree(path, "PUPPET-612"); killed != 0 {
			t.Fatalf("sweep of empty worktree path %q reported %d kills, want 0", path, killed)
		}
	}
	if listed {
		t.Fatal("sweep with an empty worktree path scanned the process table")
	}
}

// TestSweepOrphanedBackendsForWorktree_KillsOnlyItsOwnWorktree is the
// end-to-end half: a genuine orphan (PPID==1, cwd under a worktree) must
// survive a sweep scoped to a different worktree and die under a sweep scoped
// to its own.
func TestSweepOrphanedBackendsForWorktree_KillsOnlyItsOwnWorktree(t *testing.T) {
	if procInspector.List == nil {
		t.Skip("no process inspector on this platform")
	}
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	mine := t.TempDir()
	sibling := t.TempDir()

	workerCmd, childPID := spawnFakeWorker(t, mine)
	workerPID := workerCmd.Process.Pid
	if err := syscall.Kill(-workerPID, syscall.SIGKILL); err != nil {
		t.Fatalf("SIGKILL worker: %v", err)
	}
	_ = workerCmd.Wait()
	t.Cleanup(func() { _ = syscall.Kill(-childPID, syscall.SIGKILL) })

	// Same generous reparenting window the startup-sweep test uses: on a loaded
	// runner the ps view lags the kernel by several poll cycles.
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
	if !processAlive(childPID) {
		t.Fatalf("fixture expired before the sweep ran: orphan PID %d already exited "+
			"(helperLingerSleep=%s elapsed too little for this run) — not a sweep failure",
			childPID, helperLingerSleep)
	}

	if killed := s.sweepOrphanedBackendsForWorktree(sibling, "PUPPET-612"); killed != 0 {
		t.Fatalf("sweep scoped to %q killed %d processes belonging to %q", sibling, killed, mine)
	}
	if !processAlive(childPID) {
		t.Fatalf("orphan PID %d was killed by a sweep scoped to another worktree", childPID)
	}

	if killed := s.sweepOrphanedBackendsForWorktree(mine, "PUPPET-612"); killed == 0 {
		t.Fatalf("sweep scoped to %q found no orphans (child PID %d alive=%v)",
			mine, childPID, processAlive(childPID))
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(childPID) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("orphaned child PID %d survived its own worktree's sweep", childPID)
}

// stubDeadlineSweep swaps the classification-path sweep seam for a recorder, so
// the both-directions test below asserts the WIRING without signaling anything.
func stubDeadlineSweep(t *testing.T) *[]string {
	t.Helper()
	saved := sweepDeadlineOrphans
	t.Cleanup(func() { sweepDeadlineOrphans = saved })
	var swept []string
	sweepDeadlineOrphans = func(_ *Supervisor, worktreePath, taskID string) int {
		swept = append(swept, worktreePath+"|"+taskID)
		return 1
	}
	return &swept
}

// TestSweepOrphansAfterDeadlineExit_OnlyOnDeadlineOutcome is the whole point of
// the hook: an orphan is created when a turn is CUT SHORT, so the backstop must
// fire on a run-turn deadline exit and on nothing else. Sweeping after every
// exit would put a process-table scan and a signal on the daemon's hot restart
// path for runs that ended normally.
func TestSweepOrphansAfterDeadlineExit_OnlyOnDeadlineOutcome(t *testing.T) {
	cases := []struct {
		name     string
		lastErr  *agenterr.AgentError
		wantSwep bool
	}{
		{"run-turn deadline", &agenterr.AgentError{
			Class: agenterr.OutcomeFromDomain(agenterr.RunTurnDeadlineOutcome)}, true},
		{"incomplete run", &agenterr.AgentError{
			Class: agenterr.OutcomeFromDomain(agenterr.IncompleteRunOutcome)}, false},
		{"no work", &agenterr.AgentError{
			Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)}, false},
		{"harness timeout (run-duration cap)", &agenterr.AgentError{
			Class: agenterr.OutcomeFromHarness(wrapper.ErrTimeout)}, false},
		{"clean exit", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			swept := stubDeadlineSweep(t)
			s := newDrainTestSupervisor(&config.DaemonConfig{})
			ap := &AgentProcess{
				WorktreePath:   t.TempDir(),
				AssignedTaskID: "PUPPET-612",
				LastError:      tc.lastErr,
			}
			s.sweepOrphansAfterDeadlineExit(ap)
			if got := len(*swept) > 0; got != tc.wantSwep {
				t.Fatalf("swept=%v, want %v (swept=%v)", got, tc.wantSwep, *swept)
			}
			if tc.wantSwep && (*swept)[0] != ap.WorktreePath+"|PUPPET-612" {
				t.Fatalf("sweep called with %q, want %q", (*swept)[0], ap.WorktreePath+"|PUPPET-612")
			}
		})
	}
}

// TestSweepOrphansAfterDeadlineExit_SkipsEmptyWorktree repeats the empty-path
// guard one level up: an agent with no recorded worktree must not reach the
// sweep at all, since the sweep's own fallback would be to sweep nothing but
// the guard belongs on both sides of the seam.
func TestSweepOrphansAfterDeadlineExit_SkipsEmptyWorktree(t *testing.T) {
	swept := stubDeadlineSweep(t)
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	for _, path := range []string{"", "   "} {
		ap := &AgentProcess{
			WorktreePath:   path,
			AssignedTaskID: "PUPPET-612",
			LastError: &agenterr.AgentError{
				Class: agenterr.OutcomeFromDomain(agenterr.RunTurnDeadlineOutcome)},
		}
		if killed := s.sweepOrphansAfterDeadlineExit(ap); killed != 0 {
			t.Fatalf("worktree path %q reported %d kills, want 0", path, killed)
		}
	}
	if len(*swept) != 0 {
		t.Fatalf("empty worktree path still reached the sweep: %v", *swept)
	}
}
