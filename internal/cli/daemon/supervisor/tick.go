package supervisor

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// GoroutineClass determines how the liveness watchdog responds when a watched
// goroutine's tick goes stale.
type GoroutineClass int

const (
	// ClassCore marks a daemon-lifetime singleton (state updater, config
	// reconciler, node heartbeat, health checker, the watchdog itself). If one
	// of these wedges, the daemon as a whole has failed, so a stale core tick
	// escalates to a process-fatal — the only safe response for a singleton the
	// daemon cannot function without.
	ClassCore GoroutineClass = iota

	// ClassAgent marks one per-agent supervise goroutine among many. A wedged
	// agent goroutine is a localized fault: the watchdog quarantines it (stops
	// tracking it and signals that single agent to stop) while the rest of the
	// fleet keeps running. An agent fault must never take down the daemon.
	ClassAgent
)

// tickSlot is the watchdog's record for one watched goroutine.
//
// The slot pointer is its identity. Deregistration is a compare-and-delete
// against this exact pointer (see Supervisor.deregister), so a goroutine can
// only ever retract its own registration — even if a successor later reuses the
// same name (an agent removed and re-added on the same worktree). Recording
// also goes through the slot the goroutine owns, never by name, so an outgoing
// goroutine can never refresh its successor's slot.
type tickSlot struct {
	name    string
	class   GoroutineClass
	stamp   atomic.Int64 // UnixNano of last record()
	onStale func()       // quarantine action for ClassAgent; nil for ClassCore
}

// record stamps the current time on the slot.
func (sl *tickSlot) record() {
	sl.stamp.Store(time.Now().UnixNano())
}

// last returns the slot's last recorded time.
func (sl *tickSlot) last() time.Time {
	return time.Unix(0, sl.stamp.Load())
}

// registerTick allocates a tick slot, primes it to now, and stores it under
// name. It returns the slot so the owning goroutine can record() and
// deregister() by identity rather than by name.
func (s *Supervisor) registerTick(name string, class GoroutineClass, onStale func()) *tickSlot {
	sl := &tickSlot{name: name, class: class, onStale: onStale}
	sl.record()
	s.Ticks.Store(name, sl)
	return sl
}

// registerAgentTick registers a ClassAgent slot for ap, wires its quarantine
// action, and records the slot on ap so the supervise loop and its
// wait-heartbeat record by identity (ap.tick).
func (s *Supervisor) registerAgentTick(ap *AgentProcess) {
	sl := s.registerTick(agentTickName(ap), ClassAgent, nil)
	sl.onStale = func() { s.quarantineAgent(ap, sl) }
	ap.tick = sl
}

// deregister removes a slot from the registry, but only if the registry still
// holds this exact slot. It is a no-op when a successor already replaced the
// name or the watchdog already quarantined the slot — that compare-and-delete
// is what makes same-name reuse safe.
func (s *Supervisor) deregister(sl *tickSlot) {
	if sl == nil {
		return
	}
	s.Ticks.CompareAndDelete(sl.name, sl)
}

// quarantineAgent contains a wedged per-agent supervise goroutine without
// crashing the daemon. It is invoked only from the watchdog scan, so it must
// stay lock-free: it never takes ap.Mu or AgentsMu, because the wedged
// goroutine may be holding one and the watchdog must stay responsive.
//
// It (1) stops tracking the leaked goroutine so the scan does not re-flag it on
// every interval, and (2) signals that single agent to stop via its StopCh,
// which the supervise loop honors at its next checkpoint if it is merely slow
// rather than truly hung. A genuinely deadlocked goroutine leaks — but a
// contained, logged single-goroutine leak is strictly preferable to the
// fleet-wide outage of a process-fatal.
func (s *Supervisor) quarantineAgent(ap *AgentProcess, sl *tickSlot) {
	slog.Error("liveness watchdog quarantining wedged agent supervisor (fleet stays up)",
		"worktree", ap.Entry.Worktree,
		"tick", sl.name,
		"age", time.Since(sl.last()).Truncate(time.Second))
	s.deregister(sl)
	ap.signalStop()
}

// rangeSlots iterates over every registered tick slot.
func (s *Supervisor) rangeSlots(fn func(sl *tickSlot)) {
	s.Ticks.Range(func(_, v any) bool {
		if sl, ok := v.(*tickSlot); ok {
			fn(sl)
		}
		return true
	})
}
