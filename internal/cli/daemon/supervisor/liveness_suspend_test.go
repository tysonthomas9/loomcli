package supervisor

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// The tests here drive scanTicks with hand-set lastLivenessScan /
// lastLivenessScanWall values, which is the deliberate seam for exercising the
// two clock domains: a time.Time built by hand carries no monotonic reading, so
// Sub falls back to wall clock and both gaps come out equal. Only by setting
// the two scan stamps independently can a test express "runtime advanced 10s
// while wall time advanced 15 minutes" — which is exactly what a macOS wake
// looks like from inside the process.

// seedStaleStreak registers a tick, back-dates it well past its threshold, and
// scans until it is one scan short of fatal with the real-time span
// requirement already satisfied.
func seedStaleStreak(t *testing.T, s *Supervisor, name string) {
	t.Helper()
	s.RegisterTick(name)
	setTickForTest(s, name, time.Now().Add(-10*time.Minute))
	scanRepeatedN(s, livenessStaleScansBeforeFatal-1)
	if got := s.livenessStreak[name]; got != livenessStaleScansBeforeFatal-1 {
		t.Fatalf("streak not primed: got %d, want %d", got, livenessStaleScansBeforeFatal-1)
	}
}

func assertNoFatal(t *testing.T, s *Supervisor, what string) {
	t.Helper()
	select {
	case err := <-s.FatalChannel():
		t.Fatalf("%s: unexpected fatal: %v", what, err)
	case <-time.After(50 * time.Millisecond):
	}
}

func assertAllTicksPrimed(t *testing.T, s *Supervisor) {
	t.Helper()
	s.RangeTicks(func(name string, tick time.Time) {
		if age := time.Since(tick); age > time.Second {
			t.Errorf("tick %q not re-primed: age %s", name, age)
		}
	})
}

// TestScanTicksSkipsFatalOnWallClockJumpWithFrozenMonotonic is the regression
// test for the false fatals this host saw 1-2x/hour: macOS suspends the
// monotonic clock during sleep, so after a wake the scan-to-scan monotonic gap
// looks like a healthy 10s while every tick age — derived from wall clock —
// includes the whole sleep. The old single-clock guard was blind to this and
// killed the daemon within seconds of every wake.
func TestScanTicksSkipsFatalOnWallClockJumpWithFrozenMonotonic(t *testing.T) {
	s := newHarnessSupervisor()
	seedStaleStreak(t, s, GoroutineHealthChecker)

	// A normal 10s of runtime (monotonic-bearing) against a 15-minute wall jump.
	now := time.Now()
	s.lastLivenessScan = now.Add(-livenessScanInterval)
	s.lastLivenessScanWall = now.Round(0).Add(-15 * time.Minute)

	s.scanTicks(now)

	assertNoFatal(t, s, "wall-clock jump with frozen monotonic clock")
	if len(s.livenessStreak) != 0 {
		t.Errorf("streaks not cleared after sleep: %v", s.livenessStreak)
	}
	if len(s.livenessStreakStart) != 0 {
		t.Errorf("streak starts not cleared after sleep: %v", s.livenessStreakStart)
	}
	assertAllTicksPrimed(t, s)
}

// TestScanTicksRePrimesTicksAfterSuspension guards the second half of the fix:
// clearing streaks alone leaves the ages huge after thaw, so the very next
// scans re-flag everything and the daemon still fatals ~30s after wake.
func TestScanTicksRePrimesTicksAfterSuspension(t *testing.T) {
	s := newHarnessSupervisor()
	s.RegisterTick(GoroutineHealthChecker)
	s.RegisterTick(GoroutineConfigReconciler)
	setTickForTest(s, GoroutineHealthChecker, time.Now().Add(-20*time.Minute))
	setTickForTest(s, GoroutineConfigReconciler, time.Now().Add(-20*time.Minute))
	scanRepeatedN(s, livenessStaleScansBeforeFatal-1)

	now := time.Now()
	s.lastLivenessScan = now.Add(-livenessScanInterval)
	s.lastLivenessScanWall = now.Round(0).Add(-20 * time.Minute)
	s.scanTicks(now)

	assertAllTicksPrimed(t, s)

	// The scans that immediately follow the thaw must also stay quiet: this is
	// the sequence that produced "wake at 14:29:08, fatal at 14:29:17".
	scanRepeatedN(s, livenessStaleScansBeforeFatal)
	assertNoFatal(t, s, "scans immediately after thaw")
}

// TestScanTicksSkipsFatalOnBackwardClockStep covers an NTP step backwards:
// wall-derived tick ages are meaningless that scan, so skip and re-prime.
func TestScanTicksSkipsFatalOnBackwardClockStep(t *testing.T) {
	s := newHarnessSupervisor()
	seedStaleStreak(t, s, GoroutineHealthChecker)

	now := time.Now()
	s.lastLivenessScan = now.Add(-livenessScanInterval)
	s.lastLivenessScanWall = now.Round(0).Add(10 * time.Minute) // wall gap is negative

	s.scanTicks(now)

	assertNoFatal(t, s, "backward clock step")
	if len(s.livenessStreak) != 0 {
		t.Errorf("streaks not cleared after backward step: %v", s.livenessStreak)
	}
	assertAllTicksPrimed(t, s)
}

// TestScanTicksRequiresRealElapsedTimeBeforeFatal covers macOS DarkWake, where
// the process runs in ~2s bursts: the consecutive-scan count can reach its
// threshold across several wake windows without meaningful runtime passing, so
// the count alone is not the grace period it looks like.
func TestScanTicksRequiresRealElapsedTimeBeforeFatal(t *testing.T) {
	s := newHarnessSupervisor()
	s.RegisterTick(GoroutineHealthChecker)
	setTickForTest(s, GoroutineHealthChecker, time.Now().Add(-10*time.Minute))

	// Enough scans to satisfy the count, all in the same instant.
	for i := 0; i < livenessStaleScansBeforeFatal; i++ {
		s.scanTicks(time.Now())
	}
	if got := s.livenessStreak[GoroutineHealthChecker]; got < livenessStaleScansBeforeFatal {
		t.Fatalf("streak count not reached: got %d", got)
	}
	assertNoFatal(t, s, "scan count reached but no real time elapsed")

	// Once the streak has actually spanned livenessMinStaleSpan, the same
	// staleness must go fatal.
	backdateStreakStarts(s, livenessMinStaleSpan)
	s.scanTicks(time.Now())
	select {
	case err := <-s.FatalChannel():
		if !strings.Contains(err.Error(), GoroutineHealthChecker) {
			t.Errorf("fatal missing stale goroutine name: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no fatal once the stale streak spanned real runtime")
	}
}

// TestScanTicksStillFatalsOnGenuineWedge is the counterweight to the rest of
// this file: with both clocks in step and the span satisfied, a wedged
// goroutine must still crash the daemon.
func TestScanTicksStillFatalsOnGenuineWedge(t *testing.T) {
	s := newHarnessSupervisor()
	s.RegisterTick(GoroutineHealthChecker)
	setTickForTest(s, GoroutineHealthChecker, time.Now().Add(-10*time.Minute))

	// Normal 10s gaps in BOTH domains, as a healthy (awake) host produces.
	for i := 0; i < livenessStaleScansBeforeFatal; i++ {
		now := time.Now()
		s.lastLivenessScan = now.Add(-livenessScanInterval)
		s.lastLivenessScanWall = now.Round(0).Add(-livenessScanInterval)
		s.scanTicks(now)
		backdateStreakStarts(s, livenessMinStaleSpan)
	}

	select {
	case err := <-s.FatalChannel():
		if !strings.Contains(err.Error(), GoroutineHealthChecker) {
			t.Errorf("fatal missing stale goroutine name: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("genuine wedge no longer crashes the daemon")
	}
	if !s.livenessFatalSignaled {
		t.Error("livenessFatalSignaled not set after fatal")
	}
}

// countingHandler is a slog.Handler that records every message it is given.
type countingHandler struct {
	mu   *sync.Mutex
	msgs *[]string
}

func (h countingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h countingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.msgs = append(*h.msgs, r.Message)
	return nil
}

func (h countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h countingHandler) WithGroup(string) slog.Handler      { return h }

// captureLogs installs a message-recording slog default for the duration of the
// test and returns a snapshot function.
func captureLogs(t *testing.T) func() []string {
	t.Helper()
	var (
		mu   sync.Mutex
		msgs []string
	)
	prev := slog.Default()
	slog.SetDefault(slog.New(countingHandler{mu: &mu, msgs: &msgs}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), msgs...)
	}
}

// TestWatchdogStopsScanningAfterFatal asserts the watchdog goes quiet once it
// has signaled fatal — the daemon is already draining, and re-flagging the same
// ticks every interval for the whole drain window is pure noise — and that it
// then exits cleanly rather than tripping RunCritical's "returned without
// shutdown" fatal.
func TestWatchdogStopsScanningAfterFatal(t *testing.T) {
	resetGoroutineDumpThrottleForTest()
	t.Cleanup(resetGoroutineDumpThrottleForTest)

	s := newHarnessSupervisor()
	s.RegisterTick(GoroutineHealthChecker)
	s.RegisterTick(GoroutineLivenessWatchdog)
	setTickForTest(s, GoroutineHealthChecker, time.Now().Add(-10*time.Minute))
	// Pre-seed a streak one scan short of fatal, with a start far enough back
	// that the real-time span requirement is already met. The watchdog's first
	// scan then goes fatal without the test waiting three intervals.
	s.livenessStreak = map[string]int{GoroutineHealthChecker: livenessStaleScansBeforeFatal - 1}
	s.livenessStreakStart = map[string]time.Time{
		GoroutineHealthChecker: time.Now().Add(-10 * livenessMinStaleSpan),
	}

	snapshot := captureLogs(t)

	s.RunCritical(GoroutineLivenessWatchdog, func() {
		s.livenessWatchdogEvery(2 * time.Millisecond)
	})

	select {
	case <-s.FatalChannel():
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not signal fatal")
	}

	// Give the watchdog many more intervals than it would need to log again.
	time.Sleep(100 * time.Millisecond)
	if n := countMessages(snapshot(), "supervisor liveness check failed"); n != 1 {
		t.Fatalf("watchdog kept scanning after fatal: %d failure records, want 1", n)
	}

	// Closing Shutdown must release the watchdog, and RunCritical must not treat
	// that return as an abandonment.
	close(s.Shutdown)
	waitWg(t, s, 2*time.Second)
	if n := countMessages(snapshot(), "supervisor critical goroutine returned without shutdown"); n != 0 {
		t.Fatalf("watchdog return logged as abandonment %d times", n)
	}
}

func countMessages(msgs []string, want string) int {
	n := 0
	for _, m := range msgs {
		if m == want {
			n++
		}
	}
	return n
}

func waitWg(t *testing.T, s *Supervisor, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		s.Wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		var buf bytes.Buffer
		_, _ = DumpGoroutines(&buf)
		t.Fatalf("watchdog did not return after shutdown:\n%s", buf.String())
	}
}
