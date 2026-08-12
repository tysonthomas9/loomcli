package supervisor

import (
	"os"
	"strconv"
	"time"
)

// The run-duration cap: an outer wall-clock ceiling on a single supervised run.
//
// Before this existed, loom had NO duration bound of any kind. The backend
// invoke context is built with context.WithCancel, not WithTimeout (see
// backends.contextFromShutdown, whose callers pass context.Background());
// ap.LastStart was read only to reset a restart count and to window a diff; and
// every context.WithTimeout in this package bounds a short control-plane call,
// not a run. The single thing that ever ended a runaway agent was the
// output-timeout watchdog — and that is a SILENCE cap. An agent that keeps
// printing was unbounded, and an agent parked on an interactive prompt became
// unbounded the moment the idle kill learned to suspend itself for one. See
// input_wait.go, which names this gap as the reason its own suspension had to
// carry a bound: with nothing behind it, the watchdog WAS the ceiling.
//
// Two decisions about where the cap lives:
//
//  1. In the supervisor, not the child's context. A wedged agent is precisely
//     the one that may never observe its own cancellation — a deadline it has
//     to notice is a deadline it can ignore. The health checker already ticks
//     every 30s, already holds LastStart, and already owns StopAgent, so it can
//     enforce the bound with a signal instead of a request.
//
//  2. Beside the idle check rather than inside it, and evaluated first. The cap
//     must survive both of the idle path's escape hatches — the "no activity
//     signal at all" early return and the input-wait suspension. See
//     applyRunDurationKill in health.go for why the second one matters most.

// envMaxRunDurationSeconds overrides the daemon-wide run-duration cap.
//
// It mirrors LOOM_DAEMON_INPUT_WAIT_MAX_SECONDS and
// LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS in both name and reason: fleet-db's wire
// schema does not persist daemon restart-policy fields (see
// internal/infra/fleetdb/daemon.go), so an env var is the only knob that reaches
// a deployed daemon — and no test can afford to wait four real hours to trip the
// bound.
const envMaxRunDurationSeconds = "LOOM_DAEMON_MAX_RUN_DURATION_SECONDS"

// defaultMaxRunDurationSeconds caps a single run at 4 hours.
//
// The number is picked to be unreachable by a healthy run while still being a
// number. A coding agent on a real task finishes in minutes to low hours, and
// the $50 default spend ceiling (backends.DefaultMaxBudgetUSD) bites long before
// four hours of continuous inference does — so anything this fires on is already
// pathological: a backend wedged in a loop that keeps emitting, a turn that
// never converges, a prompt nobody is ever going to answer. What the cap buys is
// not tight scheduling; it is the conversion of "infinite" into "bounded".
//
// A comparable system leaves the equivalent knob opt-in per agent with no
// framework default. loom differs on purpose. There, a missing cap still leaves
// other ceilings standing; here it leaves none at all, so a cap that shipped
// disabled would leave the gap exactly as it was found and merely relabel it as
// configurable.
const defaultMaxRunDurationSeconds = 4 * 60 * 60

// GetMaxRunDuration returns the daemon-wide run-duration cap in seconds.
//
// A value <= 0 disables the cap. Note this reads the opposite way to
// GetInputWaitMax, where 0 has to mean "never suspend" because reading it as
// "forever" would manufacture an agent nothing can reap. Here the dangerous
// direction is the other one: 0 means "no ceiling", which is honestly what
// switching a ceiling off means, and it restores the exact pre-cap behavior for
// an operator who wants it back.
func (s *Supervisor) GetMaxRunDuration() int {
	if v := os.Getenv(envMaxRunDurationSeconds); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultMaxRunDurationSeconds
}

// maxRunDurationFor resolves the cap for one agent: the role's max_run_duration
// when the role names one, otherwise the daemon-wide value. Returns 0 when the
// cap is disabled, so callers test a single duration rather than re-deriving the
// precedence.
//
// The role wins over the env override, inverting the precedence GetOutputTimeout
// applies against fleet-db config — and for the same underlying reason. That env
// var exists because a deployed daemon has no other way to move a daemon-WIDE
// default; a role that names a number is per-agent configuration someone wrote
// deliberately, and a blanket override has no claim to overrule it. A role that
// says nothing (nil) inherits, so the env var still reaches every agent that has
// not opted out.
func (s *Supervisor) maxRunDurationFor(ap *AgentProcess) time.Duration {
	seconds := s.GetMaxRunDuration()
	// RoleConfig is resolved once at construction and never mutated afterwards
	// (it is absent from the ap.Mu field list), so it is read unlocked here for
	// the same reason spawnAgent reads it unlocked.
	if ap != nil && ap.RoleConfig.MaxRunDuration != nil {
		seconds = *ap.RoleConfig.MaxRunDuration
	}
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
