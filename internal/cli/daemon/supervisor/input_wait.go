package supervisor

import (
	"log/slog"
	"os"
	"strconv"
	"time"
)

// Interactive-input waits vs. the output-timeout watchdog.
//
// An agent parked on a blocking interactive prompt is silent BY DESIGN.
// harness-wrapper's pkg/chat refuses to idle-complete a turn while a request is
// surfaced, so the harness sits at the dialog and emits no PTY output at all.
// checkWatchdog cannot tell that apart from a hang — ap.LastActivity only ever
// moves when output is observed — so it kills the agent at output_timeout for
// doing exactly what it was told to do. That is the reason the role input
// policy's "ask" disposition ("hand this prompt to a human") cannot be honored
// today: waiting is indistinguishable from hanging.
//
// The fix suspends the idle kill while a request is in flight. Three properties
// of that suspension are load-bearing:
//
//  1. It is keyed on a COUNTER, not a boolean. A multi-question dialog can
//     surface a new request while the previous one is still settling, and with
//     a boolean the first answer would clear a flag the second request still
//     needs — un-suspending the watchdog underneath a still-waiting agent. The
//     count rises around each wait and falls in a defer, so overlapping
//     requests nest and the suspension lifts only with the last one.
//
//  2. It suspends ONLY the idle kill. Nothing else about supervision changes:
//     shutdown, drain, manual stop, ownership and the lease machinery all still
//     act on a waiting agent, and StopAgent never consults the counter. A
//     waiting agent can always be stopped immediately.
//
//  3. It is itself bounded. A suspension that outlives its cause is a hang with
//     extra steps, and there is nothing behind it to catch one: loom has NO
//     wall-clock or run-duration ceiling on an agent run. The backend invoke
//     context is built with context.WithCancel, not WithTimeout (see
//     backends.contextFromShutdown and defaultClaudeNonInteractiveInvoker), and
//     no supervisor path kills on LastStart age — this watchdog IS the only
//     ceiling. So the hold-off carries its own: past inputWaitMax the
//     suspension lifts and the normal watchdog resumes and kills, pending
//     requests or not.

// envInputWaitMaxSeconds bounds how long pending interactive requests may
// suspend the output-timeout kill. It mirrors LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS
// in both name and reason: fleet-db's wire schema does not persist daemon
// restart-policy fields (see internal/infra/fleetdb/daemon.go), so an env var is
// the only knob that reaches a deployed daemon — and integration tests need to
// trip the bound without waiting fifteen real minutes.
const envInputWaitMaxSeconds = "LOOM_DAEMON_INPUT_WAIT_MAX_SECONDS"

// defaultInputWaitMaxSeconds caps a single suspension at 15 minutes — the same
// order as the default output timeout. Long enough for a human to notice a
// prompt and answer it, short enough that an unanswered one is reaped within a
// coffee break instead of holding a worker slot overnight.
const defaultInputWaitMaxSeconds = 900

// GetInputWaitMax returns how many seconds pending interactive requests may
// hold the output-timeout watchdog off.
//
// A value <= 0 disables the suspension outright rather than making it
// unbounded. That direction is deliberate: reading 0 as "forever" would turn a
// typo'd env var into an agent nothing can ever reap, whereas reading it as
// "never suspend" degrades to exactly the pre-existing watchdog behavior — the
// safe side of a misconfiguration, and a usable kill switch for an operator who
// wants the old semantics back.
func (s *Supervisor) GetInputWaitMax() int {
	if v := os.Getenv(envInputWaitMaxSeconds); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultInputWaitMaxSeconds
}

// RecordAgentInputWait moves the named agent's in-flight interactive-request
// counter: begin=true is the child announcing it has parked on a prompt,
// begin=false is that prompt having been resolved. No-op for an agent that is
// not currently supervised.
//
// This is the supervisor end of the agent-IPC signal — the same channel that
// already feeds LastActivity (see daemon.recordIPCInputWait). The child owns the
// increment/decrement pairing; the supervisor owns the count, because the
// watchdog that reads it lives here.
//
// Both edges also advance LastActivity to the supervisor's own clock, and the
// "end" edge is the one that matters: the wait was silent by construction, so
// the instant a human answers, LastActivity is already older than
// output_timeout and the very next health tick would kill the agent it just
// un-blocked. Stamping the edge gives the resumed agent a full output_timeout to
// start producing again. The supervisor's clock is used rather than a
// child-supplied timestamp because checkWatchdog measures with time.Since here —
// a skewed child clock must not be able to move a kill deadline.
func (s *Supervisor) RecordAgentInputWait(agentName string, begin bool) {
	if agentName == "" {
		return
	}
	target := s.findAgentByWorktree(agentName)
	if target == nil {
		return
	}

	now := time.Now()
	target.Mu.Lock()
	switch {
	case begin:
		// Anchor the bound on the 0→1 transition ONLY. If a request joining an
		// already-open wait restarted the clock, a harness that re-prompts on a
		// timer could hold the watchdog off forever one question at a time —
		// precisely the unbounded suspension the bound exists to prevent.
		if target.InputWaitPending == 0 {
			target.InputWaitSince = now
		}
		target.InputWaitPending++
	case target.InputWaitPending > 0:
		// Clamp at zero. A duplicate or replayed "end" (client retry, a child
		// that crashed and was respawned) must not drive the count negative,
		// where a later "begin" would leave it at zero and silently fail to
		// suspend.
		target.InputWaitPending--
		if target.InputWaitPending == 0 {
			target.InputWaitSince = time.Time{}
		}
	}
	if now.After(target.LastActivity) {
		target.LastActivity = now
	}
	pending := target.InputWaitPending
	target.Mu.Unlock()

	slog.Debug("agent interactive input wait recorded",
		"worktree", agentName, "begin", begin, "pending", pending)
}

// inputWaitHoldsOff reports whether pending interactive requests should suspend
// the idle kill for this agent, along with the state that decision was made from
// so the kill log can explain itself.
//
// It deliberately does not mutate the counter when the bound expires. Letting
// the count stand means a request that IS eventually answered still decrements
// normally, and — more importantly — an unanswered burst gets exactly one
// bound's worth of grace rather than a fresh one per question: InputWaitSince
// stays pinned to the original 0→1 transition, so nothing re-arms it.
func inputWaitHoldsOff(ap *AgentProcess, maxSeconds int) (holdOff bool, pending int, expired bool) {
	ap.Mu.Lock()
	pending = ap.InputWaitPending
	since := ap.InputWaitSince
	ap.Mu.Unlock()

	if pending <= 0 || maxSeconds <= 0 {
		return false, pending, false
	}
	// A pending count with no anchor should not be an open-ended license to
	// suspend, so treat it as already expired. Defensive: the two are written
	// together under ap.Mu and cleared together between cycles.
	if since.IsZero() || time.Since(since) > time.Duration(maxSeconds)*time.Second {
		return false, pending, true
	}
	return true, pending, false
}
