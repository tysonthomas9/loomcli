// Package daemonlog gives the loom daemon a log file it owns end to end: it
// opens the file, writes to it, and re-opens it by path when a write fails.
//
// It exists because the daemon used to depend entirely on its process
// manager's stdout capture. When that manager's WriteStream hit a transient
// ENOSPC, the stream stayed permanently broken and the daemon logged nothing
// for two hours without noticing. A sink that re-opens its own file recovers
// from the same condition on its own.
package daemonlog

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// maxLogBytes is the size at which the live file is rolled aside to
	// path+".1". Deliberately a single rollover, not a rotation scheme: the
	// daemon's own log is a debugging aid, not an archive.
	maxLogBytes = 128 << 20 // 128 MiB

	initialBackoff = 5 * time.Second
	maxBackoff     = 60 * time.Second
)

// Health is a point-in-time snapshot of a Sink's file-side condition.
type Health struct {
	Path       string
	Healthy    bool
	LastOK     time.Time
	LastErr    time.Time
	LastErrMsg string
	ErrCount   int
}

// Sink is an io.Writer that tees every write to a fallback writer (normally
// stderr, which the process manager still captures) and to a file it owns.
//
// The file half is best effort by design: see Write.
type Sink struct {
	mu     sync.Mutex
	stderr io.Writer
	path   string
	file   *os.File
	size   int64

	// maxBytes is the roll threshold. Zero means maxLogBytes; tests inject a
	// small value so the rollover is reachable.
	maxBytes int64

	lastOK         time.Time
	lastErr        time.Time
	lastErrMsg     string
	errCount       int
	consecutiveErr int
	nextReopen     time.Time
	backoff        time.Duration

	// now is injectable so tests can advance past nextReopen without sleeping.
	now func() time.Time
}

// New builds a Sink writing to path and teeing to stderr.
//
// A failed initial open is NOT an error: the returned Sink degrades to
// stderr-only and records the failure in Health. A daemon that cannot open its
// own log must still supervise.
func New(path string, stderr io.Writer) *Sink {
	s := &Sink{
		stderr: stderr,
		path:   path,
		now:    time.Now,
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		s.recordErrLocked(err)
		return s
	}
	s.openLocked()
	return s
}

// Path returns the file path this sink writes to.
func (s *Sink) Path() string { return s.path }

// Write tees p to stderr and then to the file.
//
// It ALWAYS returns (len(p), nil). slog treats a writer error as a handler
// error, and the daemon's error paths log through slog — so surfacing a file
// failure here would recurse. For the same reason a sink failure must NEVER be
// reported through slog: failures live in the counters behind Health() only.
func (s *Sink) Write(p []byte) (int, error) {
	if s.stderr != nil {
		// stderr first: the file half must never delay the writer the process
		// manager is already capturing.
		_, _ = s.stderr.Write(p)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.file == nil {
		s.reopenLocked()
	}
	if s.file == nil {
		return len(p), nil
	}

	n, err := s.file.Write(p)
	if err != nil {
		s.recordErrLocked(err)
		s.scheduleReopenLocked()
		return len(p), nil
	}

	s.size += int64(n)
	s.lastOK = s.now()
	s.consecutiveErr = 0
	s.backoff = 0
	s.rollLocked()
	return len(p), nil
}

// Health returns a snapshot of the file half's condition.
func (s *Sink) Health() Health {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Health{
		Path:       s.path,
		Healthy:    s.file != nil && s.consecutiveErr == 0,
		LastOK:     s.lastOK,
		LastErr:    s.lastErr,
		LastErrMsg: s.lastErrMsg,
		ErrCount:   s.errCount,
	}
}

// Close closes the underlying file. The Sink stays usable as a stderr-only
// writer afterwards.
func (s *Sink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

// openLocked opens the file by path and re-stats its size. Caller holds mu
// (or is New, before the Sink is shared).
func (s *Sink) openLocked() {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 - path derived from daemon config log_dir
	if err != nil {
		s.recordErrLocked(err)
		s.scheduleReopenLocked()
		return
	}
	s.file = f
	s.size = 0
	if fi, statErr := f.Stat(); statErr == nil {
		s.size = fi.Size()
	}
	s.lastOK = s.now()
	s.consecutiveErr = 0
	s.backoff = 0
	s.nextReopen = time.Time{}
}

// reopenLocked closes and re-opens the file by path, but only once the backoff
// window has elapsed. Caller holds mu.
func (s *Sink) reopenLocked() {
	if !s.nextReopen.IsZero() && !s.now().After(s.nextReopen) {
		return
	}
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	// A parent directory can disappear along with the file (or never have been
	// creatable at start); retry it here so recovery does not need a restart.
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		s.recordErrLocked(err)
		s.scheduleReopenLocked()
		return
	}
	s.openLocked()
}

// rollLocked rolls the live file aside to path+".1" once it exceeds the size
// cap. A rename failure is non-fatal: keep appending to the oversized file and
// record the error. Caller holds mu.
func (s *Sink) rollLocked() {
	limit := s.maxBytes
	if limit <= 0 {
		limit = maxLogBytes
	}
	if s.size <= limit {
		return
	}
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	if err := os.Rename(s.path, s.path+".1"); err != nil {
		s.recordErrLocked(err)
	}
	s.openLocked()
}

// scheduleReopenLocked arms the next re-open attempt on an exponential
// backoff, capped at maxBackoff. Caller holds mu.
func (s *Sink) scheduleReopenLocked() {
	if s.backoff == 0 {
		s.backoff = initialBackoff
	} else {
		s.backoff *= 2
		if s.backoff > maxBackoff {
			s.backoff = maxBackoff
		}
	}
	s.nextReopen = s.now().Add(s.backoff)
}

// recordErrLocked records a file-side failure. It never logs: see Write.
// Caller holds mu.
func (s *Sink) recordErrLocked(err error) {
	s.lastErr = s.now()
	s.lastErrMsg = err.Error()
	s.errCount++
	s.consecutiveErr++
}

var _ io.Writer = (*Sink)(nil)
