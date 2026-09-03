package supervisor

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestDumpGoroutines_WritesFramedStackTrace(t *testing.T) {
	var buf bytes.Buffer
	n, err := DumpGoroutines(&buf)
	if err != nil {
		t.Fatalf("DumpGoroutines returned error: %v", err)
	}
	if n <= 0 {
		t.Fatalf("expected positive byte count, got %d", n)
	}
	out := buf.String()
	if !strings.Contains(out, "=== goroutine dump at ") {
		t.Errorf("missing header in dump output:\n%s", out)
	}
	if !strings.Contains(out, "=== end goroutine dump ===") {
		t.Errorf("missing footer in dump output:\n%s", out)
	}
	// runtime.Stack(all=true) always includes at least the current goroutine.
	if !strings.Contains(out, "goroutine ") {
		t.Errorf("expected at least one goroutine frame, got:\n%s", out)
	}
}

// shortWriter fails after allowing a fixed number of bytes through, used to
// exercise DumpGoroutines' write-error branches.
type shortWriter struct {
	allow   int
	written int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	if w.written >= w.allow {
		return 0, errWriteFull
	}
	n := len(p)
	if w.written+n > w.allow {
		n = w.allow - w.written
	}
	w.written += n
	if n < len(p) {
		return n, errWriteFull
	}
	return n, nil
}

var errWriteFull = &writeErr{}

type writeErr struct{}

func (*writeErr) Error() string { return "writer full" }

func TestDumpGoroutines_PropagatesHeaderWriteError(t *testing.T) {
	w := &shortWriter{allow: 0}
	if _, err := DumpGoroutines(w); err == nil {
		t.Fatal("expected error when header write fails")
	}
}

func TestDumpGoroutinesToLog_DoesNotPanic(t *testing.T) {
	// Smoke test: the SIGUSR1/watchdog entrypoint writes to stderr and logs.
	// We only assert it returns without panicking.
	DumpGoroutinesToLog("unit-test")
}

// TestDumpThrottleSuppressesRepeats guards the log-flood half of the liveness
// fix: the watchdog can ask for a dump on every 10s scan, and each dump is up
// to 1 MiB of stderr.
func TestDumpThrottleSuppressesRepeats(t *testing.T) {
	resetGoroutineDumpThrottleForTest()
	t.Cleanup(resetGoroutineDumpThrottleForTest)

	base := time.Now()
	now := base
	prevNow := dumpNow
	dumpNow = func() time.Time { return now }
	t.Cleanup(func() { dumpNow = prevNow })

	if !DumpGoroutinesToLogThrottled("first") {
		t.Fatal("first dump was suppressed; the first dump must always go through")
	}
	if DumpGoroutinesToLogThrottled("immediate repeat") {
		t.Fatal("repeat dump within the throttle window was not suppressed")
	}

	// Still inside the window.
	now = base.Add(goroutineDumpMinInterval - time.Second)
	if DumpGoroutinesToLogThrottled("just inside window") {
		t.Fatal("dump one second before the window closed was not suppressed")
	}

	// Window elapsed.
	now = base.Add(goroutineDumpMinInterval)
	if !DumpGoroutinesToLogThrottled("after window") {
		t.Fatal("dump after the throttle window elapsed was suppressed")
	}
}
