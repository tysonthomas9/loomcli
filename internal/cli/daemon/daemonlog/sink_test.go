package daemonlog

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is the injectable clock the sink's backoff is measured against, so
// tests advance time instead of sleeping.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestSink_TeesToBothTargets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "daemon.log")
	var stderr bytes.Buffer

	s := New(path, &stderr)
	defer func() { _ = s.Close() }()

	if _, err := s.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got := stderr.String(); got != "hello\n" {
		t.Errorf("stderr = %q, want %q", got, "hello\n")
	}
	if got := readFile(t, path); got != "hello\n" {
		t.Errorf("file = %q, want %q", got, "hello\n")
	}
	if h := s.Health(); !h.Healthy {
		t.Errorf("Health().Healthy = false, want true (%s)", h.LastErrMsg)
	}
}

func TestSink_WriteSurvivesFileError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	var stderr bytes.Buffer

	s := New(path, &stderr)
	clock := newClock()
	s.now = clock.now

	// Break the file half behind the sink's back: the handle is closed, so the
	// next write to it fails the way a broken stream does.
	if err := s.file.Close(); err != nil {
		t.Fatalf("closing handle: %v", err)
	}

	line := []byte("still logged\n")
	n, err := s.Write(line)
	if n != len(line) || err != nil {
		t.Fatalf("Write = (%d, %v), want (%d, nil)", n, err, len(line))
	}
	if got := stderr.String(); got != string(line) {
		t.Errorf("stderr = %q, want %q", got, line)
	}
	if h := s.Health(); h.Healthy {
		t.Error("Health().Healthy = true, want false after a file write error")
	}
	if h := s.Health(); h.ErrCount == 0 || h.LastErrMsg == "" {
		t.Errorf("Health() = %+v, want a recorded error", h)
	}
}

// TestSink_ReopensAfterTransientFailure is the regression test for the outage
// this package exists for: a log target that fails once must recover without a
// process restart.
func TestSink_ReopensAfterTransientFailure(t *testing.T) {
	dir := t.TempDir()
	if os.Geteuid() == 0 {
		t.Skip("permission-based failure injection does not work as root")
	}
	path := filepath.Join(dir, "daemon.log")
	var stderr bytes.Buffer

	// Unwritable at open time: the sink degrades to stderr-only.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	s := New(path, &stderr)
	defer func() { _ = s.Close() }()
	clock := newClock()
	s.now = clock.now
	s.nextReopen = clock.now().Add(initialBackoff)

	if _, err := s.Write([]byte("lost line\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if s.Health().Healthy {
		t.Fatal("Health().Healthy = true, want false while the directory is unwritable")
	}

	// The condition clears, and time passes the backoff window.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	clock.advance(initialBackoff + time.Second)

	if _, err := s.Write([]byte("recovered line\n")); err != nil {
		t.Fatalf("Write after recovery: %v", err)
	}
	if got := readFile(t, path); !strings.Contains(got, "recovered line") {
		t.Errorf("file = %q, want it to contain the post-recovery line", got)
	}
	if h := s.Health(); !h.Healthy {
		t.Errorf("Health().Healthy = false after re-open, want true (%s)", h.LastErrMsg)
	}
	if !strings.Contains(stderr.String(), "lost line") {
		t.Error("stderr lost the line written while the file was unavailable")
	}
}

func TestSink_ReopenBackoffIsRespected(t *testing.T) {
	dir := t.TempDir()
	if os.Geteuid() == 0 {
		t.Skip("permission-based failure injection does not work as root")
	}
	path := filepath.Join(dir, "daemon.log")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()

	s := New(path, &bytes.Buffer{})
	clock := newClock()
	s.now = clock.now
	s.nextReopen = clock.now().Add(initialBackoff)
	before := s.Health().ErrCount

	// Many writes inside one backoff window must not hammer the filesystem.
	for i := 0; i < 20; i++ {
		clock.advance(100 * time.Millisecond)
		if _, err := s.Write([]byte("x\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if got := s.Health().ErrCount; got != before {
		t.Errorf("ErrCount = %d, want %d: no re-open should be attempted inside the window", got, before)
	}

	// Past the window, exactly one attempt is made.
	clock.advance(initialBackoff)
	if _, err := s.Write([]byte("x\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := s.Health().ErrCount; got != before+1 {
		t.Errorf("ErrCount = %d, want %d: exactly one re-open attempt past the window", got, before+1)
	}
}

func TestSink_RollsAtSizeCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	s := New(path, &bytes.Buffer{})
	defer func() { _ = s.Close() }()
	s.maxBytes = 32

	for i := 0; i < 10; i++ {
		if _, err := s.Write([]byte("0123456789\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rolled file %s.1 missing: %v", path, err)
	}
	live, err := os.Stat(path)
	if err != nil {
		t.Fatalf("live file missing: %v", err)
	}
	if live.Size() > 32 {
		t.Errorf("live file size = %d, want the file to have restarted below the cap", live.Size())
	}
	if h := s.Health(); !h.Healthy {
		t.Errorf("Health().Healthy = false after roll, want true (%s)", h.LastErrMsg)
	}
}

func TestSink_ConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	s := New(path, nil)
	defer func() { _ = s.Close() }()

	const goroutines, perGoroutine = 50, 100
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if _, err := s.Write([]byte(fmt.Sprintf("goroutine %02d line %03d\n", g, i))); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSuffix(readFile(t, path), "\n"), "\n")
	if len(lines) != goroutines*perGoroutine {
		t.Fatalf("got %d lines, want %d", len(lines), goroutines*perGoroutine)
	}
	for _, line := range lines {
		var g, i int
		if _, err := fmt.Sscanf(line, "goroutine %d line %d", &g, &i); err != nil {
			t.Fatalf("interleaved or truncated line %q: %v", line, err)
		}
	}
}

func TestSink_OpenFailureIsNotFatal(t *testing.T) {
	// A path whose parent is an existing FILE can never be a directory, so both
	// the MkdirAll and the open fail — without depending on process privileges.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("writing blocker: %v", err)
	}
	var stderr bytes.Buffer

	s := New(filepath.Join(blocker, "daemon.log"), &stderr)
	if s == nil {
		t.Fatal("New returned nil, want a usable stderr-only sink")
	}
	defer func() { _ = s.Close() }()

	n, err := s.Write([]byte("degraded\n"))
	if n != len("degraded\n") || err != nil {
		t.Fatalf("Write = (%d, %v), want (%d, nil)", n, err, len("degraded\n"))
	}
	if got := stderr.String(); got != "degraded\n" {
		t.Errorf("stderr = %q, want the line to still reach stderr", got)
	}
	if h := s.Health(); h.Healthy || h.ErrCount == 0 {
		t.Errorf("Health() = %+v, want unhealthy with a recorded error", h)
	}
}
