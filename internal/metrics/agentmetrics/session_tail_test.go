package agentmetrics

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func indexPath(dir string) string { return filepath.Join(dir, "sessions", "index.jsonl") }

func sessionCount(t *testing.T, c *Collector, labels map[string]string) (float64, bool) {
	t.Helper()
	return counterValue(gather(t, c)["loom_agent_sessions_total"], labels)
}

// Two appends across two scrapes accumulate rather than replacing each other.
func TestTailerAccumulatesAcrossScrapes(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)

	appendIndex(t, dir, finished("s1", "worker", "implementation", "completed", 30))
	if got, _ := sessionCount(t, c, map[string]string{"role": "worker", "phase": "implementation", "status": "completed"}); got != 1 {
		t.Fatalf("after first append: %v, want 1", got)
	}

	appendIndex(t, dir, finished("s2", "worker", "implementation", "completed", 45))
	got, ok := sessionCount(t, c, map[string]string{"role": "worker", "phase": "implementation", "status": "completed"})
	if !ok || got != 2 {
		t.Fatalf("after second append: %v (present=%v), want 2", got, ok)
	}
}

// Every session writes two rows — running at create, terminal at finalize.
// Only the terminal one may be counted, or every session counts twice.
func TestRunningRowIsNotCountedAndTerminalRowIsCountedOnce(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)

	appendIndex(t, dir,
		running("s1", "worker", "implementation"),
		finished("s1", "worker", "implementation", "completed", 30),
	)

	families := gather(t, c)
	fam := families["loom_agent_sessions_total"]
	if n := len(fam.GetMetric()); n != 1 {
		t.Fatalf("session series = %d, want exactly 1: %v", n, fam.GetMetric())
	}
	got, _ := counterValue(fam, map[string]string{"role": "worker", "phase": "implementation", "status": "completed"})
	if got != 1 {
		t.Errorf("completed count = %v, want 1", got)
	}
	h := histogramFor(families["loom_agent_session_duration_seconds"], map[string]string{"role": "worker", "phase": "implementation"})
	if h == nil || h.GetSampleCount() != 1 || h.GetSampleSum() != 30 {
		t.Errorf("histogram = %v, want one observation of 30s", h)
	}
}

func TestRunningRowAloneProducesNoSamples(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	appendIndex(t, dir, running("s1", "worker", "planning"))

	if got := testutil.CollectAndCount(c, "loom_agent_sessions_total"); got != 0 {
		t.Errorf("session samples = %d for a running-only session, want 0", got)
	}
	if got := testutil.CollectAndCount(c, "loom_agent_session_duration_seconds"); got != 0 {
		t.Errorf("histogram samples = %d for a running-only session, want 0", got)
	}
}

// A retried finalize writes a second terminal row for the same session.
func TestDuplicateTerminalRowIsCountedOnce(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)

	appendIndex(t, dir,
		finished("s1", "worker", "implementation", "completed", 30),
		finished("s1", "worker", "implementation", "completed", 30),
	)

	got, _ := sessionCount(t, c, map[string]string{"role": "worker", "phase": "implementation", "status": "completed"})
	if got != 1 {
		t.Errorf("count = %v after a duplicate terminal row, want 1", got)
	}
}

// `loom cleanup` compacts index.jsonl. The old counts then describe rows that
// no longer exist, so the tailer starts over from the new file.
func TestCompactionResetsStateAndRereads(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)

	appendIndex(t, dir,
		finished("s1", "worker", "implementation", "completed", 30),
		finished("s2", "worker", "implementation", "completed", 30),
	)
	if got, _ := sessionCount(t, c, map[string]string{"role": "worker", "phase": "implementation", "status": "completed"}); got != 2 {
		t.Fatalf("before compaction: %v, want 2", got)
	}

	// Compact to a single surviving record.
	if err := os.Remove(indexPath(dir)); err != nil {
		t.Fatalf("remove index: %v", err)
	}
	appendIndex(t, dir, finished("s2", "worker", "implementation", "completed", 30))

	got, _ := sessionCount(t, c, map[string]string{"role": "worker", "phase": "implementation", "status": "completed"})
	if got != 1 {
		t.Errorf("after compaction: %v, want 1 (state and the dedupe window must reset)", got)
	}
}

// A concurrent append can be caught mid-write. A partial line is not a record
// yet; it must be read exactly once, later, when it is complete.
func TestPartialTrailingLineIsCountedOnlyOnceComplete(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)

	appendRaw(t, dir, `{"session_id":"s1","agent_name":"worker","phase":"implementation",`)
	if got := testutil.CollectAndCount(c, "loom_agent_sessions_total"); got != 0 {
		t.Fatalf("partial line produced %d samples, want 0", got)
	}

	appendRaw(t, dir, `"status":"completed","duration_s":30,"ended_at":"2026-08-30T12:00:00Z"}`+"\n")
	got, _ := sessionCount(t, c, map[string]string{"role": "worker", "phase": "implementation", "status": "completed"})
	if got != 1 {
		t.Errorf("completed line count = %v, want 1", got)
	}
}

func TestEmptyPhaseBecomesUnknown(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	appendIndex(t, dir, finished("s1", "worker", "", "completed", 12))

	if got, ok := sessionCount(t, c, map[string]string{"role": "worker", "phase": "unknown", "status": "completed"}); !ok || got != 1 {
		t.Errorf("phase=unknown count = %v (present=%v), want 1", got, ok)
	}
}

// Records written before the daemon learned to stamp a role carry only
// agent_name, and that is what the role label falls back to.
func TestMissingRoleFallsBackToAgentName(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)

	withRole := finished("s1", "worker-2", "implementation", "completed", 20)
	withRole.Role = "worker"
	appendIndex(t, dir, withRole, finished("s2", "tester-1", "implementation", "completed", 20))

	if got, ok := sessionCount(t, c, map[string]string{"role": "worker", "phase": "implementation", "status": "completed"}); !ok || got != 1 {
		t.Errorf("explicit role: %v (present=%v), want 1", got, ok)
	}
	if got, ok := sessionCount(t, c, map[string]string{"role": "tester-1", "phase": "implementation", "status": "completed"}); !ok || got != 1 {
		t.Errorf("agent_name fallback: %v (present=%v), want 1", got, ok)
	}
}

// The histogram must describe the filtered rows only: no running row, no
// duplicate, and the sum is those rows' duration_s.
func TestHistogramSumAndCountCoverFilteredRowsOnly(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)

	appendIndex(t, dir,
		running("s1", "worker", "implementation"),
		finished("s1", "worker", "implementation", "completed", 15),
		finished("s2", "worker", "implementation", "failed", 45),
		finished("s2", "worker", "implementation", "failed", 45), // retried finalize
	)

	h := histogramFor(gather(t, c)["loom_agent_session_duration_seconds"],
		map[string]string{"role": "worker", "phase": "implementation"})
	if h == nil {
		t.Fatal("no histogram")
	}
	if h.GetSampleCount() != 2 {
		t.Errorf("count = %d, want 2", h.GetSampleCount())
	}
	if h.GetSampleSum() != 60 {
		t.Errorf("sum = %v, want 60", h.GetSampleSum())
	}
	// 15s falls in the first bucket (le=20 is the second bound; le=10 is the
	// first, which 15 exceeds), 45s in le=80.
	for _, b := range h.GetBucket() {
		switch b.GetUpperBound() {
		case 10:
			if b.GetCumulativeCount() != 0 {
				t.Errorf("le=10 = %d, want 0", b.GetCumulativeCount())
			}
		case 20:
			if b.GetCumulativeCount() != 1 {
				t.Errorf("le=20 = %d, want 1", b.GetCumulativeCount())
			}
		case 80:
			if b.GetCumulativeCount() != 2 {
				t.Errorf("le=80 = %d, want 2", b.GetCumulativeCount())
			}
		}
	}
}

func TestRecentIDSetEvictsOldestAndStaysBounded(t *testing.T) {
	s := newRecentIDSet(2)
	if !s.add("a") || !s.add("b") {
		t.Fatal("first two adds must be new")
	}
	if s.add("a") {
		t.Error("a re-added within the window")
	}
	s.add("c") // evicts "a"
	if len(s.members) != 2 {
		t.Errorf("members = %d, want 2", len(s.members))
	}
	if !s.add("a") {
		t.Error("a should be new again after eviction")
	}
}
