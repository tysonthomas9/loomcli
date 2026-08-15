package daemon

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// ---------------------------------------------------------------------------
// waitBounded
// ---------------------------------------------------------------------------

func TestWaitBounded_DoneFirst(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(done)
	}()
	if !waitBounded(done, 5*time.Second) {
		t.Error("waitBounded() = false, want true (done closed first)")
	}
}

func TestWaitBounded_BudgetFirst(t *testing.T) {
	start := time.Now()
	if waitBounded(make(chan struct{}), 200*time.Millisecond) {
		t.Error("waitBounded() = true, want false (budget expired first)")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("waitBounded blocked for %v, want ~200ms", elapsed)
	}
}

func TestWaitBounded_NonPositiveBudget(t *testing.T) {
	start := time.Now()
	if waitBounded(make(chan struct{}), 0) {
		t.Error("waitBounded() = true, want false for a zero budget")
	}
	if waitBounded(make(chan struct{}), -time.Second) {
		t.Error("waitBounded() = true, want false for a negative budget")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waitBounded blocked for %v on a non-positive budget", elapsed)
	}

	closed := make(chan struct{})
	close(closed)
	if !waitBounded(closed, 0) {
		t.Error("waitBounded() = false, want true when done is already closed")
	}
}

// TestWaitBounded_WallClockDeadlineWins is the regression test for the 18m22s
// timestamp anomaly: a monotonic timer that does not fire (machine asleep) must
// not be able to defer the deadline, because the wall clock is checked too.
func TestWaitBounded_WallClockDeadlineWins(t *testing.T) {
	var mu sync.Mutex
	base := time.Now()
	offset := time.Duration(0)

	restore := nowFn
	nowFn = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return base.Add(offset)
	}
	t.Cleanup(func() { nowFn = restore })

	// Jump the wall clock past the one-hour deadline while the monotonic timer,
	// armed for that same hour, stays parked.
	go func() {
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		offset = 2 * time.Hour
		mu.Unlock()
	}()

	start := time.Now()
	if waitBounded(make(chan struct{}), time.Hour) {
		t.Error("waitBounded() = true, want false (wall-clock deadline passed)")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("waitBounded returned after %v of real time; the wall clock should have won", elapsed)
	}
}

func TestWaitBounded_WallClockStepsBackwards(t *testing.T) {
	base := time.Now()
	restore := nowFn
	nowFn = func() time.Time { return base.Add(-time.Hour) }
	t.Cleanup(func() { nowFn = restore })

	done := make(chan struct{})
	go func() {
		time.Sleep(1500 * time.Millisecond) // past at least one wall-clock tick
		close(done)
	}()
	if !waitBounded(done, 10*time.Second) {
		t.Error("waitBounded() = false, want true: a backwards clock step must not shorten the wait")
	}
}

// ---------------------------------------------------------------------------
// forceExit
// ---------------------------------------------------------------------------

func TestForceExit_RemovesFilesAndLogsStragglers(t *testing.T) {
	logs := captureSlog(t)
	exits := stubExit(t)
	stubDump(t)

	dir := t.TempDir()
	paths := daemonPaths{
		pidFile:   filepath.Join(dir, "daemon.pid"),
		stateFile: filepath.Join(dir, "state.json"),
		lockFile:  filepath.Join(dir, "daemon.lock"),
	}
	for _, p := range []string{paths.pidFile, paths.stateFile} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}

	cleanupRan := false
	report := supervisor.StopReport{
		Budget: 75 * time.Second,
		DrainOutcomes: []supervisor.DrainOutcome{
			{Worktree: "worker", Phase: supervisor.DrainPhaseYielded},
			{Worktree: "integrator", Phase: supervisor.DrainPhaseSigterm},
		},
	}

	forceExit(report, 75*time.Second, paths, func() { cleanupRan = true }, 2)

	if got := exits(); len(got) != 1 || got[0] != 2 {
		t.Errorf("exit codes = %v, want [2]", got)
	}
	if !cleanupRan {
		t.Error("cleanup closure did not run; the lock and workspace pid file would be left behind")
	}
	for _, p := range []string{paths.pidFile, paths.stateFile} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists after forceExit", p)
		}
	}

	out := logs()
	if !strings.Contains(out, "shutdown deadline exceeded") {
		t.Errorf("log has no deadline record:\n%s", out)
	}
	if !strings.Contains(out, "integrator") {
		t.Errorf("log does not name the straggler worktree:\n%s", out)
	}
	if !strings.Contains(out, "agent failed to yield during shutdown") {
		t.Errorf("log has no per-worktree yield failure line:\n%s", out)
	}
}

func TestForceExit_MissingFilesStillExits(t *testing.T) {
	captureSlog(t)
	exits := stubExit(t)
	stubDump(t)

	paths := daemonPaths{
		pidFile:   filepath.Join(t.TempDir(), "absent.pid"),
		stateFile: "",
	}
	forceExit(supervisor.StopReport{}, time.Second, paths, nil, 0)

	if got := exits(); len(got) != 1 || got[0] != 0 {
		t.Errorf("exit codes = %v, want [0]", got)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// stubExit replaces exitFn with a recorder and returns an accessor for the
// codes it saw. forceExit calls os.Exit, so the real one would take the test
// binary with it.
func stubExit(t *testing.T) func() []int {
	t.Helper()
	var mu sync.Mutex
	var codes []int
	restore := exitFn
	exitFn = func(code int) {
		mu.Lock()
		codes = append(codes, code)
		mu.Unlock()
	}
	t.Cleanup(func() { exitFn = restore })
	return func() []int {
		mu.Lock()
		defer mu.Unlock()
		out := make([]int, len(codes))
		copy(out, codes)
		return out
	}
}

// stubDump silences the goroutine dump, which would otherwise write a full
// stack trace to stderr on every forceExit test.
func stubDump(t *testing.T) {
	t.Helper()
	restore := dumpGoroutinesFn
	dumpGoroutinesFn = func(string) {}
	t.Cleanup(func() { dumpGoroutinesFn = restore })
}

// captureSlog redirects the default logger into a buffer. The acceptance
// criterion for PUPPET-39 is about what the log says, so it is asserted rather
// than eyeballed.
func captureSlog(t *testing.T) func() string {
	t.Helper()
	buf := &syncBuffer{}
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(restore) })
	return buf.String
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
