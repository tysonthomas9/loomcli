package supervisor

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// killWithReason drives the record hook once for a task-holding kill carrying
// the given stop reason.
func killWithReason(s *Supervisor, ap *AgentProcess, reason StopReason) {
	ap.Mu.Lock()
	ap.StopReason = reason
	ap.Mu.Unlock()
	s.recordTaskExitForQuarantine(ap, 137)
}

// infrastructureReasons is the exclusion table from the design, paired with the
// reason string each must produce.
var infrastructureReasons = []struct {
	stop StopReason
	why  string
}{
	{StopReasonShutdown, "daemon_shutdown"},
	{StopReasonManualStop, "manual_stop"},
	{StopReasonConfigRemoved, "config_removed"},
	{StopReasonBackendUnavailable, "backend_unavailable"},
	{StopReasonMaxRetries, "agent_budget"},
	{StopReasonMaxRetriesBlocked, "agent_budget"},
	{StopReasonFastFail, "agent_budget"},
}

// ---------------------------------------------------------------------------
// the predicate
// ---------------------------------------------------------------------------

// The heart of the fix: a kill that is a verdict about the daemon, the agent or
// the account must never advance the task's counter, however many times it
// happens. Three of these in a row is exactly what quarantined a task that had
// never stalled.
func TestQuarantineCountable_InfrastructureKillsNeverCount(t *testing.T) {
	for _, tc := range infrastructureReasons {
		t.Run(string(tc.stop), func(t *testing.T) {
			s := newQuarantineSupervisor(nil)
			ap := newKilledAgent(t, "falcon", "T-1", timeoutOutcome())

			// Well past the threshold: the count must not merely lag, it must
			// never move at all.
			for i := 0; i < 3*s.quarantineThreshold(); i++ {
				killWithReason(s, ap, tc.stop)
			}

			if got := recordCount(s, "T-1"); got != 0 {
				t.Errorf("Count = %d, want 0 (%s is not a verdict about the task)", got, tc.stop)
			}
			if rec := record(s, "T-1"); rec != nil && !rec.QuarantinedAt.IsZero() {
				t.Errorf("task quarantined by %s kills alone", tc.stop)
			}
			ev := killEvent{StopReason: string(tc.stop)}
			if countable, why := s.quarantineCountable(ev); countable || why != tc.why {
				t.Errorf("quarantineCountable(%s) = (%v, %q), want (false, %q)", tc.stop, countable, why, tc.why)
			}
		})
	}
}

// The mirror, and the reason the predicate is a table rather than a blanket
// "stop reasons don't count": the watchdog fires BECAUSE the agent went silent
// past its output timeout, which is the definition of a stall. A bare crash is
// the breaker's base case. Blind the counter to either and there is nothing
// left to count.
func TestQuarantineCountable_StallSignalsStillCount(t *testing.T) {
	t.Run("watchdog", func(t *testing.T) {
		s := newQuarantineSupervisor(nil)
		ap := newKilledAgent(t, "falcon", "T-2", timeoutOutcome())

		killWithReason(s, ap, StopReasonWatchdog)
		killWithReason(s, ap, StopReasonWatchdog)

		if got := recordCount(s, "T-2"); got != 2 {
			t.Errorf("Count = %d, want 2 (the watchdog is the breaker's primary signal)", got)
		}
	})

	t.Run("bare crash", func(t *testing.T) {
		s := newQuarantineSupervisor(nil)
		// A crash the classifier could not name: no stop reason, class Unknown.
		ap := newKilledAgent(t, "falcon", "T-3", agenterr.OutcomeFromHarness(wrapper.ErrUnknown))

		s.recordTaskExitForQuarantine(ap, 139)
		s.recordTaskExitForQuarantine(ap, 139)

		if got := recordCount(s, "T-3"); got != 2 {
			t.Errorf("Count = %d, want 2 (an unexplained crash is the breaker's base case)", got)
		}
	})
}

// Asserted directly rather than trusted to QuarantineEligible: an account-level
// failure is never the task's fault, and this is the regression the incident's
// auth flapping would have produced.
func TestQuarantineCountable_AuthFailureNeverIncrements(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap := newKilledAgent(t, "falcon", "T-4", agenterr.OutcomeFromHarness(wrapper.ErrAuth))

	killNTimes(s, ap, 5)

	if rec := record(s, "T-4"); rec != nil {
		t.Fatalf("an auth failure must never reach the ledger, got %+v", rec)
	}
}

// ---------------------------------------------------------------------------
// boot grace
// ---------------------------------------------------------------------------

// Kills that land in a burst right after a daemon restart — resume failures,
// stale-lock cleanup, adopted runs — are collateral of the restart, not
// evidence about any task.
func TestQuarantineCountable_BootGrace(t *testing.T) {
	t.Run("inside the grace, a watchdog kill does not count", func(t *testing.T) {
		s := newQuarantineSupervisor(nil)
		s.BootedAt = time.Now()
		ap := newKilledAgent(t, "falcon", "T-5", timeoutOutcome())

		killWithReason(s, ap, StopReasonWatchdog)

		if got := recordCount(s, "T-5"); got != 0 {
			t.Errorf("Count = %d, want 0 (a kill seconds after boot is restart collateral)", got)
		}
	})

	t.Run("past the grace, the same kill counts", func(t *testing.T) {
		s := newQuarantineSupervisor(nil)
		s.BootedAt = time.Now().Add(-10 * time.Minute)
		ap := newKilledAgent(t, "falcon", "T-6", timeoutOutcome())

		killWithReason(s, ap, StopReasonWatchdog)

		if got := recordCount(s, "T-6"); got != 1 {
			t.Errorf("Count = %d, want 1 (the grace must expire, or the breaker never arms)", got)
		}
	})

	// The no-regression case for every Supervisor literal that omits the field:
	// a zero BootedAt means "no boot time was recorded", never "booted at the
	// epoch", and it must suppress nothing.
	t.Run("a zero BootedAt disables the grace", func(t *testing.T) {
		s := newQuarantineSupervisor(nil)
		ap := newKilledAgent(t, "falcon", "T-7", timeoutOutcome())

		killWithReason(s, ap, StopReasonWatchdog)

		if got := recordCount(s, "T-7"); got != 1 {
			t.Errorf("Count = %d, want 1 (an unset BootedAt must behave exactly as before the field existed)", got)
		}
	})
}

// ---------------------------------------------------------------------------
// the uncounted path's omissions
// ---------------------------------------------------------------------------

// Recorded for diagnosis, but inert: the kill shows up in the timeline marked
// with why it was discounted, and the counter does not move.
func TestRecordUncountedKill_LandsInTheTimelineWithoutCounting(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap := newKilledAgent(t, "falcon", "T-8", timeoutOutcome())

	killWithReason(s, ap, StopReasonWatchdog) // a real kill, to open the record
	killWithReason(s, ap, StopReasonShutdown)

	rec := record(s, "T-8")
	if rec == nil {
		t.Fatal("expected a record for T-8")
	}
	if rec.Count != 1 {
		t.Errorf("Count = %d, want 1 (only the watchdog kill counts)", rec.Count)
	}
	if len(rec.Kills) != 2 {
		t.Fatalf("len(Kills) = %d, want 2 (uncounted kills are still recorded)", len(rec.Kills))
	}
	if rec.Kills[0].NotCounted != "" {
		t.Errorf("Kills[0].NotCounted = %q, want empty (it counted)", rec.Kills[0].NotCounted)
	}
	if rec.Kills[1].NotCounted != "daemon_shutdown" {
		t.Errorf("Kills[1].NotCounted = %q, want daemon_shutdown", rec.Kills[1].NotCounted)
	}

	text := formatKillTimeline("T-8", 3, rec.Count, rec.Kills)
	if !strings.Contains(text, "| note |") {
		t.Errorf("timeline is missing the note column:\n%s", text)
	}
	if !strings.Contains(text, "not counted: daemon_shutdown") {
		t.Errorf("timeline does not mark the discounted kill:\n%s", text)
	}
}

// An uncounted kill alone does not deserve a ledger slot: creating one would
// churn evictOldestLocked with records that can never reach the threshold.
func TestRecordUncountedKill_NeverCreatesARecord(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap := newKilledAgent(t, "falcon", "T-9", timeoutOutcome())

	killWithReason(s, ap, StopReasonManualStop)

	if rec := record(s, "T-9"); rec != nil {
		t.Fatalf("an uncounted kill must not open a record, got %+v", rec)
	}
}

// The sharp edge. recordEligibleKill's re-arm branch exists so a task a human
// released needs N FRESH kills before it re-quarantines. If an infrastructure
// kill could consume that re-arm, one daemon restart would silently spend the
// protection the release bought.
func TestRecordUncountedKill_DoesNotConsumeTheReArm(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap := newKilledAgent(t, "falcon", "T-10", timeoutOutcome())

	killNTimes(s, ap, 3)
	s.qrec().latch("T-10", true)

	rec := record(s, "T-10")
	latchedAt := rec.QuarantinedAt
	if latchedAt.IsZero() {
		t.Fatal("setup: expected a latched record")
	}

	killWithReason(s, ap, StopReasonShutdown)

	rec = record(s, "T-10")
	if !rec.QuarantinedAt.Equal(latchedAt) {
		t.Errorf("QuarantinedAt = %v, want %v (an infrastructure kill must not re-arm the latch)",
			rec.QuarantinedAt, latchedAt)
	}
	if rec.Count != 0 {
		t.Errorf("Count = %d, want 0 (the re-arm baseline must be untouched)", rec.Count)
	}
}

// ---------------------------------------------------------------------------
// part C: the duration cap counts only when the run was also silent
// ---------------------------------------------------------------------------

// markRunDurationExceeded argues a duration kill is a no-progress signal, and
// it is — for a run that sat idle until the ceiling hit it. A run whose
// transcript shows writes right up to the kill was working, and says nothing
// about the task.
func TestQuarantineCountable_DurationKillCountsOnlyWhenSilent(t *testing.T) {
	t.Run("active at the cap: not counted", func(t *testing.T) {
		s := newQuarantineSupervisor(nil)
		ap := newKilledAgent(t, "falcon", "T-11", timeoutOutcome())
		ap.RunSilentAtStop = false

		killWithReason(s, ap, StopReasonRunDurationExceeded)

		if got := recordCount(s, "T-11"); got != 0 {
			t.Errorf("Count = %d, want 0 (a run still talking at the ceiling is not stalled)", got)
		}
		ev := killEvent{StopReason: string(StopReasonRunDurationExceeded)}
		if countable, why := s.quarantineCountable(ev); countable || why != "duration_kill_while_active" {
			t.Errorf("quarantineCountable = (%v, %q), want (false, duration_kill_while_active)", countable, why)
		}
	})

	t.Run("silent at the cap: counted", func(t *testing.T) {
		s := newQuarantineSupervisor(nil)
		ap := newKilledAgent(t, "falcon", "T-12", timeoutOutcome())
		ap.RunSilentAtStop = true

		killWithReason(s, ap, StopReasonRunDurationExceeded)

		if got := recordCount(s, "T-12"); got != 1 {
			t.Errorf("Count = %d, want 1 (a wedged run hitting the ceiling is exactly the no-progress signal)", got)
		}
		if rec := record(s, "T-12"); rec == nil || !rec.Kills[0].RunSilent {
			t.Error("killEvent.RunSilent must carry the verdict stamped at kill time")
		}
	})
}

// ---------------------------------------------------------------------------
// latestActivity (pure refactor of checkWatchdog's tiering)
// ---------------------------------------------------------------------------

func TestLatestActivity_FreshestTierWins(t *testing.T) {
	dir := t.TempDir()
	logPath := dir + "/agent.log"
	txPath := dir + "/transcript.jsonl"
	writeAt := func(path string, at time.Time) {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now()
	s := newQuarantineSupervisor(nil)

	t.Run("heartbeat newest", func(t *testing.T) {
		ap := &AgentProcess{TranscriptPath: txPath, LastActivity: now}
		writeAt(txPath, now.Add(-time.Hour))
		writeAt(logPath, now.Add(-2*time.Hour))

		at, source := s.latestActivity(ap, logPath)
		if source != "heartbeat" || !at.Equal(now) {
			t.Errorf("latestActivity = (%v, %q), want (%v, heartbeat)", at, source, now)
		}
	})

	t.Run("transcript newest", func(t *testing.T) {
		ap := &AgentProcess{TranscriptPath: txPath, LastActivity: now.Add(-3 * time.Hour)}
		writeAt(txPath, now.Add(-time.Minute))
		writeAt(logPath, now.Add(-2*time.Hour))

		_, source := s.latestActivity(ap, logPath)
		if source != "transcript" {
			t.Errorf("source = %q, want transcript", source)
		}
	})

	t.Run("log newest", func(t *testing.T) {
		ap := &AgentProcess{TranscriptPath: txPath, LastActivity: now.Add(-3 * time.Hour)}
		writeAt(txPath, now.Add(-2*time.Hour))
		writeAt(logPath, now.Add(-time.Minute))

		_, source := s.latestActivity(ap, logPath)
		if source != "log" {
			t.Errorf("source = %q, want log", source)
		}
	})

	// The early-return case checkWatchdog depends on: nothing observable at all
	// yields a ZERO time, not a very old one, so no caller can mistake it for
	// an activity instant.
	t.Run("nothing observable", func(t *testing.T) {
		ap := &AgentProcess{}

		at, source := s.latestActivity(ap, "")
		if source != "none" || !at.IsZero() {
			t.Errorf("latestActivity = (%v, %q), want (zero, none)", at, source)
		}
	})
}

// ---------------------------------------------------------------------------
// the stamp, at the kill site
// ---------------------------------------------------------------------------

// The cap's independence is unchanged: it still fires through a pending
// input-wait and through an actively-writing agent. All that is new is the
// verdict it leaves behind.
func TestApplyRunDurationKill_StampsRunSilentWithoutChangingTheDecision(t *testing.T) {
	t.Run("active run: killed, stamped not-silent", func(t *testing.T) {
		s, ap := newRunCapFixture(t, 5*time.Hour) // LastActivity is now

		s.checkAgentHealth()

		if !runCapKilled(ap) {
			t.Fatalf("StopReason = %q, want %q (the cap must fire regardless of activity)",
				stopReasonOf(ap), StopReasonRunDurationExceeded)
		}
		if runSilentOf(ap) {
			t.Error("RunSilentAtStop = true, want false (the agent was writing right up to the kill)")
		}
	})

	t.Run("wedged run: killed, stamped silent", func(t *testing.T) {
		s, ap := newRunCapFixture(t, 5*time.Hour)
		backdate(ap, 2*time.Hour, 0) // silent far past the 60s output timeout

		s.checkAgentHealth()

		if !runCapKilled(ap) {
			t.Fatalf("StopReason = %q, want %q", stopReasonOf(ap), StopReasonRunDurationExceeded)
		}
		if !runSilentOf(ap) {
			t.Error("RunSilentAtStop = false, want true (a run silent past its output timeout is wedged)")
		}
	})

	t.Run("pending input wait: still killed, still stamped", func(t *testing.T) {
		s, ap := newRunCapFixture(t, 5*time.Hour)
		s.RecordAgentInputWait("test", true)
		backdate(ap, 20*time.Minute, time.Minute)

		s.checkAgentHealth()

		if !runCapKilled(ap) {
			t.Fatalf("StopReason = %q, want %q (an outstanding prompt must not shield a run from its cap)",
				stopReasonOf(ap), StopReasonRunDurationExceeded)
		}
		if !runSilentOf(ap) {
			t.Error("RunSilentAtStop = false, want true (silence is silence, whatever excuses it)")
		}
	})

	// With the silence check switched off there is no threshold to be silent
	// against, so there is no verdict to record — and the kill still lands.
	t.Run("output timeout disabled: killed, never stamped", func(t *testing.T) {
		s, ap := newRunCapFixture(t, 5*time.Hour)
		s.ConfigSnapshot().Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(0)
		backdate(ap, 2*time.Hour, 0)

		s.checkAgentHealth()

		if !runCapKilled(ap) {
			t.Fatalf("StopReason = %q, want %q (disabling the idle kill must not disable the cap)",
				stopReasonOf(ap), StopReasonRunDurationExceeded)
		}
		if runSilentOf(ap) {
			t.Error("RunSilentAtStop = true, want false (no threshold means no silence verdict)")
		}
	})

	// A run inside its cap is never stamped, because it is never stopped here.
	t.Run("inside the cap: untouched", func(t *testing.T) {
		s, ap := newRunCapFixture(t, 1*time.Hour)
		backdate(ap, 2*time.Hour, 0)

		s.checkAgentHealth()

		if got := stopReasonOf(ap); got != StopReasonWatchdog {
			t.Fatalf("StopReason = %q, want %q (silent past the output timeout, inside the cap)", got, StopReasonWatchdog)
		}
		if runSilentOf(ap) {
			t.Error("RunSilentAtStop = true, want false (only the duration cap stamps it)")
		}
	})
}

func runSilentOf(ap *AgentProcess) bool {
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	return ap.RunSilentAtStop
}
