package daemon

import (
	"log/slog"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// Injection points for tests. forceExit terminates the process and waitBounded
// consults the wall clock, neither of which is testable otherwise.
var (
	exitFn           = os.Exit
	nowFn            = time.Now
	dumpGoroutinesFn = supervisor.DumpGoroutinesToLog
)

// wallClockTick is how often waitBounded re-checks the wall clock against its
// deadline. It bounds how far past the deadline a force-exit can slip when the
// monotonic clock has stalled.
const wallClockTick = time.Second

// forceExitBackstop bounds the whole forceExit body. Every step in forceExit is
// meant to be quick and best-effort, but a goroutine dump or a filesystem
// removal on a wedged mount is not something to bet the exit on.
const forceExitBackstop = 10 * time.Second

// stateUpdaterBudget bounds the wait for the state updater on the success path.
const stateUpdaterBudget = 10 * time.Second

// waitBounded blocks until done closes or budget elapses, whichever comes
// first, and reports whether done closed in time.
//
// The deadline is enforced against BOTH the monotonic clock (a timer) and the
// wall clock (a ticker comparing nowFn()). Go timers on darwin are driven by
// mach_absolute_time, which does not advance while the machine is asleep — a
// plain timer can therefore fire many wall-clock minutes after its nominal
// deadline, which is what PUPPET-39 observed: a 30s watchdog that logged at
// T+18m22s. Whichever clock reaches the deadline first wins, so a suspended
// machine cannot defer a shutdown force-exit.
//
// A wall clock that steps backwards (NTP) never shortens the wait: negative
// elapsed time is treated as zero and the monotonic timer remains in charge.
func waitBounded(done <-chan struct{}, budget time.Duration) bool {
	if budget <= 0 {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}

	timer := time.NewTimer(budget)
	defer timer.Stop()
	ticker := time.NewTicker(wallClockTick)
	defer ticker.Stop()

	wallStart := nowFn()
	for {
		select {
		case <-done:
			return true
		case <-timer.C:
			return false
		case <-ticker.C:
			// Round(0) strips the monotonic reading so the subtraction is a
			// true wall-clock comparison.
			elapsed := nowFn().Round(0).Sub(wallStart.Round(0))
			if elapsed >= budget {
				return false
			}
		}
	}
}

// gracefulShutdown runs d.Stop() under budget and force-exits the process if it
// overruns. It returns normally only when Stop() completed within the budget.
func gracefulShutdown(d *Daemon, budget time.Duration, paths daemonPaths, cleanup func(), exitCode int) {
	// The report travels over a channel rather than a shared variable: on the
	// overrun path the Stop goroutine is still running, so a plain variable
	// would be a data race.
	reportCh := make(chan supervisor.StopReport, 1)
	stopDone := make(chan struct{})
	go func() {
		reportCh <- d.StopWithBudget(budget)
		close(stopDone)
	}()

	// waitBounded's own deadline is the same budget; a completion landing in the
	// same instant is preferred over the timeout via the non-blocking recheck.
	completed := waitBounded(stopDone, budget)
	if !completed {
		select {
		case <-stopDone:
			completed = true
		default:
		}
	}

	if !completed {
		// No report exists yet — name the agents still running so the failure
		// log is attributable anyway.
		forceExit(unfinishedStopReport(d, budget), budget, paths, cleanup, exitCode)
		return
	}

	report := <-reportCh
	logDrainOutcomes(report)
	if report.TimedOut() {
		// Stop() returned, but bounded a wait internally: the process is still
		// wedged somewhere it cannot be unwedged from. Exit for real.
		forceExit(report, budget, paths, cleanup, exitCode)
	}
}

// agentSnapshotBudget bounds the attribution snapshot taken after a Stop()
// overrun. Reading the agent list takes AgentsMu, and a deadlocked AgentsMu is
// one of the things that can wedge Stop() in the first place — so even this
// diagnostic read cannot be allowed to block the exit.
const agentSnapshotBudget = 2 * time.Second

// unfinishedStopReport synthesizes a report for a Stop() that never returned,
// naming every agent whose process is still alive as a straggler. Returns a
// report with no outcomes if the agent list cannot be read in time.
func unfinishedStopReport(d *Daemon, budget time.Duration) supervisor.StopReport {
	report := supervisor.StopReport{Budget: budget, Elapsed: budget}

	agentsCh := make(chan []supervisor.SupervisedAgentStatus, 1)
	snapDone := make(chan struct{})
	go func() {
		agentsCh <- d.Agents()
		close(snapDone)
	}()
	if !waitBounded(snapDone, agentSnapshotBudget) {
		slog.Warn("could not snapshot agents for shutdown attribution", "budget", agentSnapshotBudget)
		return report
	}

	for _, a := range <-agentsCh {
		if a.PID == 0 {
			continue
		}
		report.DrainOutcomes = append(report.DrainOutcomes, supervisor.DrainOutcome{
			Worktree: a.Worktree,
			Phase:    supervisor.DrainPhaseUnfinished,
		})
	}
	return report
}

// logDrainOutcomes records the per-worktree drain result of a completed Stop,
// so a successful shutdown also says which agents yielded and which needed
// SIGTERM. That asymmetry is what took a ticket to diagnose in PUPPET-39.
func logDrainOutcomes(report supervisor.StopReport) {
	for _, o := range report.DrainOutcomes {
		slog.Info("agent drain outcome",
			"worktree", o.Worktree, "phase", string(o.Phase), "elapsed", o.Elapsed)
	}
}

// forceExit logs the straggler worktrees, dumps goroutines, removes the
// daemon's on-disk footprint, and terminates the process. It does not return.
//
// The cleanup closure carries runDaemonBody's deferred teardown (lock file, PID
// file, state file, workspace lock) which os.Exit would otherwise skip. Doing it
// here keeps the on-disk footprint identical between the normal and forced
// paths, so the next `loom daemon` start is not blocked by a stale lock and a
// future collision message does not name a dead PID.
func forceExit(report supervisor.StopReport, budget time.Duration, paths daemonPaths, cleanup func(), exitCode int) {
	// Nothing below is allowed to be the reason the daemon fails to die.
	backstop := time.AfterFunc(forceExitBackstop, func() { exitFn(exitCode) })
	defer backstop.Stop()

	attrs := append([]any{}, report.LogAttrs()...)
	attrs = append(attrs, "watchdog_budget", budget)
	slog.Error("shutdown deadline exceeded", attrs...)
	for _, o := range report.DrainOutcomes {
		if o.Yielded() {
			continue
		}
		slog.Warn("agent failed to yield during shutdown",
			"worktree", o.Worktree, "phase", string(o.Phase), "elapsed", o.Elapsed)
	}

	// Before the cleanup: a wedged cmd.Wait() is visible in the stack, and this
	// is the only chance to capture it.
	dumpGoroutinesFn("shutdown-deadline")

	removeDaemonFile(paths.pidFile)
	removeDaemonFile(paths.stateFile)
	if cleanup != nil {
		cleanup()
	}

	exitFn(exitCode)
}

// removeDaemonFile deletes one of the daemon's on-disk artifacts, best effort.
// A failure is logged and never blocks the exit — refusing to exit on a cleanup
// error would reintroduce exactly the bug this path exists to fix.
func removeDaemonFile(path string) {
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Warn("failed to remove daemon file during force-exit", "path", path, "err", err)
	}
}
