package daemon

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"
)

const (
	// fatalSupervisorExitCode is the code runDaemonBody returns when a critical
	// supervisor goroutine dies (liveness watchdog, panic, or an unexpected
	// return). awaitDaemonExit produces it; the auto-restart policy keys off it.
	fatalSupervisorExitCode = 2

	// daemonMaxAutoRestarts bounds consecutive auto-restarts so a genuinely
	// broken daemon does not spin forever. The counter resets once an instance
	// has run for daemonHealthyUptime.
	daemonMaxAutoRestarts = 10

	// daemonRestartBackoff is the fixed delay before re-exec, giving a transient
	// cause (host wake settling, fleet-db restart) a moment to clear.
	daemonRestartBackoff = 3 * time.Second

	// daemonHealthyUptime is how long an instance must run before its death is
	// treated as a fresh incident rather than another crash-loop iteration.
	daemonHealthyUptime = 5 * time.Minute

	// daemonRestartCountEnv carries the consecutive-restart count across the
	// re-exec boundary (a fresh process has no in-memory counter).
	daemonRestartCountEnv = "LOOM_DAEMON_RESTART_COUNT"
)

// restartDecision is the outcome of decideDaemonRestart.
type restartDecision struct {
	Restart  bool
	Attempt  int // 1-based attempt number for this restart (valid when Restart)
	ExitCode int // code to exit with when Restart is false
}

// decideDaemonRestart decides whether to auto-restart after the daemon exited.
// It is pure (no I/O) so the policy is unit-testable. Only the fatal-supervisor
// exit code is retried; an instance that ran at least healthyUptime before
// dying resets the crash-loop budget so a long-lived daemon that finally trips
// (e.g. after a host suspend) does not inherit a stale restart count.
func decideDaemonRestart(exitCode int, autoRestart bool, prevAttempts int,
	uptime, healthyUptime time.Duration, maxRestarts int) restartDecision {
	if exitCode != fatalSupervisorExitCode || !autoRestart {
		return restartDecision{Restart: false, ExitCode: exitCode}
	}
	attempt := prevAttempts + 1
	if uptime >= healthyUptime {
		attempt = 1
	}
	if attempt > maxRestarts {
		return restartDecision{Restart: false, ExitCode: exitCode}
	}
	return restartDecision{Restart: true, Attempt: attempt}
}

// maybeRestartDaemon re-execs a fresh `loom daemon` after a fatal exit when the
// restart policy allows it. runDaemonBody's defers have already released the
// lock/PID files, so the new process starts clean and avoids in-process state
// leakage (signal handlers, otel hooks, store handles). On a successful re-exec
// this does not return; otherwise it returns the exit code the caller should
// exit with.
func maybeRestartDaemon(exitCode int, autoRestart bool, uptime time.Duration) int {
	prev := envInt(daemonRestartCountEnv, 0)
	d := decideDaemonRestart(exitCode, autoRestart, prev, uptime,
		daemonHealthyUptime, daemonMaxAutoRestarts)
	if !d.Restart {
		if exitCode == fatalSupervisorExitCode && autoRestart {
			fmt.Fprintf(os.Stderr,
				"[daemon] auto-restart budget exhausted after %d attempts; exiting %d\n",
				daemonMaxAutoRestarts, exitCode)
		}
		return d.ExitCode
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[daemon] cannot resolve executable for auto-restart: %v\n", err)
		return exitCode
	}

	fmt.Fprintf(os.Stderr,
		"[daemon] supervisor exited %d; auto-restarting in %s (attempt %d/%d)\n",
		exitCode, daemonRestartBackoff, d.Attempt, daemonMaxAutoRestarts)
	time.Sleep(daemonRestartBackoff)

	env := append(os.Environ(), fmt.Sprintf("%s=%d", daemonRestartCountEnv, d.Attempt))
	if err := syscall.Exec(exe, os.Args, env); err != nil { //nolint:gosec // G204: re-exec of our own binary with our own argv
		fmt.Fprintf(os.Stderr, "[daemon] re-exec failed: %v\n", err)
		return exitCode
	}
	return 0 // unreachable on a successful exec
}

// envInt reads an integer environment variable, returning def when it is unset
// or unparseable.
func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
