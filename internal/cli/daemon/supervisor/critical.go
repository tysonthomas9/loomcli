package supervisor

import (
	"fmt"
	"log/slog"
	"runtime"
	"sync/atomic"
	"time"
)

// CriticalGoroutine names identify long-lived goroutines that the supervisor
// watches for liveness and crashes. Use these constants when wiring
// RunCritical / RegisterTick / RecordTick so the watchdog finds the right
// tick slot.
const (
	GoroutineHealthChecker    = "health_checker"
	GoroutineConfigReconciler = "config_reconciler"
	GoroutineNodeHeartbeat    = "node_heartbeat"
	GoroutineStateUpdater     = "state_updater"
	GoroutineLivenessWatchdog = "liveness_watchdog"
	GoroutineAgentPrefix      = "agent:"
)

// FatalChannel returns the channel that receives the first fatal supervisor
// error. Callers should select on this alongside their shutdown signal so the
// daemon process can exit non-zero when a critical goroutine dies.
func (s *Supervisor) FatalChannel() <-chan error {
	return s.FatalCh
}

// SignalFatal records the first fatal error from any critical goroutine and
// publishes it on FatalCh. Subsequent calls are ignored (sync.Once). The send
// is non-blocking because FatalCh has buffer 1; later panics still log but the
// daemon only needs one fatal to know it must exit.
func (s *Supervisor) SignalFatal(name string, err error) {
	s.FatalOnce.Do(func() {
		wrapped := fmt.Errorf("supervisor goroutine %q died: %w", name, err)
		select {
		case s.FatalCh <- wrapped:
		default:
		}
	})
}

// RecoverAndSignal is a deferred panic handler that logs the panic with a full
// goroutine stack dump and routes it to SignalFatal. Use it as the first
// `defer` in any goroutine the daemon must not silently lose.
func (s *Supervisor) RecoverAndSignal(name string) {
	r := recover()
	if r == nil {
		return
	}
	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, true)
	slog.Error("supervisor goroutine panicked",
		"goroutine", name,
		"panic", fmt.Sprintf("%v", r),
		"stack", string(buf[:n]))
	s.SignalFatal(name, fmt.Errorf("panic: %v", r))
}

// RunCritical runs fn in a new goroutine that is registered with the
// supervisor WaitGroup. A panic, or a normal return before s.Shutdown is
// closed, is treated as fatal: the panic stack is logged and SignalFatal is
// called so the daemon process can exit non-zero. Use this for goroutines
// that are expected to run for the lifetime of the daemon (cadence loops,
// watchdog, state updater, control plane heartbeat).
func (s *Supervisor) RunCritical(name string, fn func()) {
	s.Wg.Add(1)
	go func() {
		defer s.Wg.Done()
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 1<<16)
				n := runtime.Stack(buf, true)
				slog.Error("supervisor critical goroutine panicked",
					"goroutine", name,
					"panic", fmt.Sprintf("%v", r),
					"stack", string(buf[:n]))
				s.SignalFatal(name, fmt.Errorf("panic: %v", r))
				return
			}
			// Normal return without shutdown is fatal: the goroutine
			// abandoned its responsibility while the daemon still expects it.
			select {
			case <-s.Shutdown:
				// expected — daemon is shutting down
			default:
				slog.Error("supervisor critical goroutine returned without shutdown",
					"goroutine", name)
				s.SignalFatal(name, fmt.Errorf("returned without shutdown"))
			}
		}()
		fn()
	}()
}

// supervisedAgentBody is the body of a per-agent superviseAgent goroutine.
// The caller must have already performed s.Wg.Add(1) (typically under
// AgentsMu so Stop()'s Wg.Wait sees a consistent count) and must spawn this
// function as `go s.supervisedAgentBody(name, ap)`.
//
// Panics inside superviseAgent are caught and routed to SignalFatal so the
// daemon process exits non-zero rather than silently losing supervision.
// Normal returns are expected (max retries, config removed, shutdown) and not
// treated as failures — and every one of them freezes this goroutine's tick,
// so the return also retires that tick from the liveness watchdog.
//
// Defer order is load-bearing: the retirement must land BEFORE ap.Done closes.
// DrainAgent waits on Done and a re-add under the same worktree re-registers
// (and thereby un-retires) the same tick name, so retiring after the close
// could silently retire the NEW goroutine's tick.
func (s *Supervisor) supervisedAgentBody(name string, ap *AgentProcess) {
	defer s.Wg.Done()
	defer close(ap.Done)
	defer s.retireAgentSupervision(ap, name)
	defer s.RecoverAndSignal(name)
	s.superviseAgent(ap)
}

// RegisterTick allocates a tick slot for a goroutine name and primes it with
// the current time. Call this before starting the goroutine so the watchdog
// does not see a zero-valued tick on its first scan.
//
// Registering also un-retires the name: an agent that is drained and added
// back under the same worktree gets a fresh goroutine, and that goroutine is
// watched again.
func (s *Supervisor) RegisterTick(name string) {
	tick := new(atomic.Int64)
	tick.Store(time.Now().UnixNano())
	s.Ticks.Store(name, tick)
	s.retiredTicks.Delete(name)
}

// RetireTick tells the liveness watchdog to stop holding a goroutine to its
// staleness threshold, because that goroutine has intentionally ended.
//
// The watchdog's whole premise is "this loop should be ticking and is not, so
// it is wedged, so crash the daemon and let pm2 rebuild it". That premise is
// false for a goroutine that RETURNED — an agent stopped for a fatal auth
// error, a fast-fail, a config removal or shutdown. There is nothing to
// rebuild and nothing wedged; the tick simply stopped because the loop it
// belonged to is gone. Before this existed, one correctly-stopped agent took
// the entire daemon down ~12 minutes later, killing every healthy sibling's
// in-flight run with it.
//
// The tick slot itself is left in place so operator surfaces and goroutine
// dumps still show when the agent last ticked; only the fatal decision skips
// it. Retirement is reversed by RegisterTick.
func (s *Supervisor) RetireTick(name string) {
	s.retiredTicks.Store(name, struct{}{})
}

// tickRetired reports whether the named goroutine has intentionally ended and
// is therefore exempt from the staleness check. Lock-free by construction: the
// watchdog must stay responsive even when the supervisor is deadlocked, so it
// may never take AgentsMu or an AgentProcess mutex to answer this.
func (s *Supervisor) tickRetired(name string) bool {
	_, ok := s.retiredTicks.Load(name)
	return ok
}

// RecordTick stamps the current time on the named goroutine's tick slot. Call
// this at the top of every iteration of a watched loop. Missing the named slot
// is a programmer error (the goroutine was not registered) and is silently
// ignored — the watchdog will not flag what it does not know to watch.
func (s *Supervisor) RecordTick(name string) {
	v, ok := s.Ticks.Load(name)
	if !ok {
		return
	}
	tick, ok := v.(*atomic.Int64)
	if !ok {
		return
	}
	tick.Store(time.Now().UnixNano())
}

// LoadTick returns the last recorded tick time for a goroutine name, and false
// if the name was never registered.
func (s *Supervisor) LoadTick(name string) (time.Time, bool) {
	v, ok := s.Ticks.Load(name)
	if !ok {
		return time.Time{}, false
	}
	tick, ok := v.(*atomic.Int64)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(0, tick.Load()), true
}

// RangeTicks iterates over registered tick slots, invoking fn for each.
func (s *Supervisor) RangeTicks(fn func(name string, t time.Time)) {
	s.Ticks.Range(func(k, v any) bool {
		name, ok := k.(string)
		if !ok {
			return true
		}
		tick, ok := v.(*atomic.Int64)
		if !ok {
			return true
		}
		fn(name, time.Unix(0, tick.Load()))
		return true
	})
}
