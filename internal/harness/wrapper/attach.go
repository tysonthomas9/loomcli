package wrapper

import (
	"errors"
	"io"
	"sync"

	"github.com/creack/pty"
)

// ErrSessionTerminated is returned by Session.WriteStdin and
// Session.Resize when the underlying PTY is no longer open.
var ErrSessionTerminated = errors.New("wrapper: session terminated")

// outputFanout multiplexes PTY-master reads to the original Stdout and
// to every registered attach sink. Writes never block on a slow sink:
// each sink runs through a small ring buffer goroutine, and bursts
// that overflow the buffer are dropped (PTY output is observability
// for attach clients, not control flow).
type outputFanout struct {
	mu     sync.RWMutex
	stdout io.Writer
	sinks  map[int]*outputSink
	next   int
}

func newOutputFanout(stdout io.Writer) *outputFanout {
	return &outputFanout{stdout: stdout, sinks: map[int]*outputSink{}}
}

// Write fans bytes out to all current writers. Errors from individual
// sinks are swallowed: the fan-out has to make progress regardless of
// any single subscriber's state.
func (f *outputFanout) Write(p []byte) (int, error) {
	if f.stdout != nil {
		_, _ = f.stdout.Write(p)
	}
	f.mu.RLock()
	for _, s := range f.sinks {
		s.deliver(p)
	}
	f.mu.RUnlock()
	return len(p), nil
}

// add registers w as a new attach sink. The returned func removes the
// sink and waits for its delivery goroutine to exit. The sink does
// not block the fan-out: bytes written while w is slow accumulate up
// to the per-sink buffer and are dropped beyond it.
func (f *outputFanout) add(w io.Writer) func() {
	s := newOutputSink(w)
	f.mu.Lock()
	id := f.next
	f.next++
	f.sinks[id] = s
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		delete(f.sinks, id)
		f.mu.Unlock()
		s.close()
	}
}

// closeAll closes every sink. Called when the supervisor exits so
// attach handlers see a clean EOF on their writer side.
func (f *outputFanout) closeAll() {
	f.mu.Lock()
	sinks := f.sinks
	f.sinks = map[int]*outputSink{}
	f.mu.Unlock()
	for _, s := range sinks {
		s.close()
	}
}

// outputSink wraps a single subscriber writer. A small queue absorbs
// bursts so the fan-out's RLock is released quickly; the worker
// goroutine drains the queue into the writer.
type outputSink struct {
	w         io.Writer
	q         chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

func newOutputSink(w io.Writer) *outputSink {
	s := &outputSink{
		w:    w,
		q:    make(chan []byte, 64),
		done: make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *outputSink) run() {
	defer close(s.done)
	for buf := range s.q {
		if _, err := s.w.Write(buf); err != nil {
			// Drain the queue silently after a write error; the
			// caller will eventually unregister the sink.
			for range s.q {
			}
			return
		}
	}
}

func (s *outputSink) deliver(p []byte) {
	cp := make([]byte, len(p))
	copy(cp, p)
	select {
	case s.q <- cp:
	default:
		// Buffer full — drop. Attach clients prefer dropped bytes
		// over a stalled wrapper.
	}
}

func (s *outputSink) close() {
	s.closeOnce.Do(func() { close(s.q) })
	<-s.done
}

// AttachOutput registers w to receive a copy of every byte the
// wrapper reads from the PTY master, alongside the original Stdout.
// The returned func detaches the sink; it is safe to call multiple
// times. Detaching does not stop the run.
//
// Output sinks are observability, not control flow: if w blocks long
// enough to fill the per-sink buffer, additional bytes are dropped.
// Attach clients are responsible for keeping up.
func (s *Session) AttachOutput(w io.Writer) func() {
	if w == nil {
		return func() {}
	}
	return s.fanout.add(w)
}

// WriteStdin writes bytes to the PTY master, i.e. forwards them as
// keystrokes to the harness process. Multiple callers may write
// concurrently; writes are serialized so individual buffers arrive
// intact. Returns ErrSessionTerminated if the session has already
// closed the PTY.
func (s *Session) WriteStdin(p []byte) (int, error) {
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()
	select {
	case <-s.doneCh:
		return 0, ErrSessionTerminated
	default:
	}
	if s.ptmx == nil {
		return 0, ErrSessionTerminated
	}
	return s.ptmx.Write(p)
}

// Resize updates the PTY master's window size. cols/rows of zero are
// silently rejected (most terminals report bogus zero sizes during
// teardown). Returns ErrSessionTerminated after the session ends.
func (s *Session) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()
	select {
	case <-s.doneCh:
		return ErrSessionTerminated
	default:
	}
	if s.ptmx == nil {
		return ErrSessionTerminated
	}
	return pty.Setsize(s.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}

// AcquireWriter attempts to claim the exclusive stdin-writer lock.
// Returns ok=true and a release func when the caller is the active
// writer. Subsequent callers get ok=false and must treat themselves
// as read-only watchers until the holder releases.
func (s *Session) AcquireWriter() (release func(), ok bool) {
	s.writerMu.Lock()
	defer s.writerMu.Unlock()
	if s.writerHeld {
		return func() {}, false
	}
	s.writerHeld = true
	released := false
	return func() {
		s.writerMu.Lock()
		defer s.writerMu.Unlock()
		if released {
			return
		}
		released = true
		s.writerHeld = false
	}, true
}
