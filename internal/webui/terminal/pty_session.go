package terminal

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// screenResetSeq clears the terminal and homes the cursor. Emitted at the
// start of a scrollback replay so the client's grid doesn't render old
// contents interleaved with the replayed bytes.
var screenResetSeq = []byte("\x1b[2J\x1b[H")

// Exit reasons stored on ptySession.closeReason and reported by
// Attachment.ExitReason() after the output channel closes. The WS handler
// maps these to WebSocket close codes so the frontend can distinguish an
// explicit server-side Kill (e.g. DeleteTab) from a backend crash or a
// benign detach.
const (
	ExitReasonKilled   = "killed"   // explicit PTYManager.Kill
	ExitReasonExited   = "exited"   // child process exited on its own
	ExitReasonShutdown = "shutdown" // manager-wide Shutdown
)

// attachBufferSize is the capacity of an Attachment's output channel.
// Non-blocking sends from the drain goroutine mean a slow WebSocket drops
// frames rather than back-pressuring the shell. The ring buffer always has
// the ground truth.
const attachBufferSize = 64

// localAttachment is the in-process Attachment implementation. It forwards
// input and resize requests through the owning session's upstream.
type localAttachment struct {
	connID     string
	output     chan []byte
	scrollback []byte
	// session is held so ExitReason can report *why* the output channel
	// closed (the reason is stored on the session). Keeping a pointer after
	// close() is safe — closeReason is written before the channel is closed,
	// and the attachment is only consulted by a goroutine that already
	// observed the close.
	session *ptySession
}

func (a *localAttachment) ConnID() string        { return a.connID }
func (a *localAttachment) Output() <-chan []byte { return a.output }

func (a *localAttachment) WriteInput(p []byte) (int, error) { return a.session.writeInput(p) }
func (a *localAttachment) Scrollback() []byte               { return a.scrollback }
func (a *localAttachment) Resize(_ string, cols, rows uint16) error {
	return a.session.resize(context.Background(), cols, rows)
}

// ExitReason returns the reason the owning session closed, or "" if the
// session is still live (or was never given a reason). Only meaningful
// after Output() has been observed closed.
func (a *localAttachment) ExitReason() string {
	if a.session == nil {
		return ""
	}
	if v := a.session.closeReason.Load(); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ptySession owns the scrollback ring and the set of concurrent attachments
// (multi-client) for one (workspace, session) pair. Each attachment is a
// separate WebSocket / viewer that sees the same output and can write input
// into the shared upstream.
type ptySession struct {
	key        SessionKey
	upstream   PTYUpstream
	scrollback *ringBuffer
	// frameSink is read unsynchronized by drain, so it must be set before
	// drain starts. It must not mutate p: the same backing array is fanned
	// out to every attachment after the sink returns.
	frameSink func([]byte)
	createdAt int64 // unix nanos

	lastOutput atomic.Int64 // unix nanos, updated by drain

	attachMu sync.Mutex
	attaches map[string]*attachmentState // keyed by connID

	killMu    sync.Mutex
	killTimer *time.Timer

	closeOnce   sync.Once
	closeReason atomic.Value // string; stored before the output channels close
	done        chan struct{}

	onUpstreamEnd func(SessionKey)
}

// attachmentState is the internal mutable half of an Attachment.
//
// Concurrency: drain fans output to every attachment outside attachMu, so a
// Detach / Kill can race with an in-flight send. Guard the channel with an
// RWMutex — RLock per send (multiple senders allowed in parallel for
// different attachments), Lock to close (waits for any in-flight send,
// then closes the channel under the flag set). This also replaces the
// old recover-on-send-to-closed-channel hack, which worked at runtime but
// tripped the race detector.
type attachmentState struct {
	connID  string
	ch      chan []byte
	closeMu sync.RWMutex
	closed  bool // guarded by closeMu
}

func newPtySession(key SessionKey, upstream PTYUpstream, onUpstreamEnd func(SessionKey)) *ptySession {
	return &ptySession{
		key:           key,
		upstream:      upstream,
		scrollback:    newRingBuffer(defaultRingCapacity),
		createdAt:     time.Now().UnixNano(),
		attaches:      make(map[string]*attachmentState),
		done:          make(chan struct{}),
		onUpstreamEnd: onUpstreamEnd,
	}
}

// drain pumps upstream output forever: bytes go into the ring buffer and a
// best-effort copy is fanned out to every current attachment. Exits when the
// upstream output channel closes. On exit it asks the owner to handle the end
// unless close() has already marked it done.
func (s *ptySession) drain() {
	// Reused across iterations to avoid per-chunk allocation. Sized once
	// to a typical attachment count; grows naturally if needed.
	snapshot := make([]*attachmentState, 0, 4)
	for chunk := range s.upstream.Output() {
		s.scrollback.Append(chunk)
		if s.frameSink != nil {
			s.frameSink(chunk)
		}
		s.lastOutput.Store(time.Now().UnixNano())

		// Hold attachMu only for the map copy — sending outside the
		// lock means a slow client's backed-up channel can never block
		// the drain goroutine or any other attachment's delivery.
		s.attachMu.Lock()
		snapshot = snapshot[:0]
		for _, st := range s.attaches {
			snapshot = append(snapshot, st)
		}
		s.attachMu.Unlock()
		for _, st := range snapshot {
			st.send(chunk)
		}
	}

	select {
	case <-s.done:
		// close() already tore the session down.
	default:
		if s.onUpstreamEnd != nil {
			s.onUpstreamEnd(s.key)
		}
	}
}

// attachNew adds a fresh attachment to the session without disturbing any
// existing ones (multi-client). Returns a localAttachment preloaded with
// the replay bytes (reset escape + current scrollback) so the new client
// starts from the same grid state as the existing ones.
//
// Returns nil if the session was closed concurrently (close() nils
// s.attaches). The caller (PTYManager.AttachSession) is expected to retry
// the lookup/spawn in that case.
//
// connID must be unique; PTYManager generates it from a monotonic counter.
// A defensive same-connID check closes any prior attachment under that ID
// — this is guard-rail against a caller bug, not an expected code path.
func (s *ptySession) attachNew(connID string) *localAttachment {
	ch := make(chan []byte, attachBufferSize)
	st := &attachmentState{connID: connID, ch: ch}

	s.attachMu.Lock()
	if s.attaches == nil {
		// Session was closed between the manager-level lookup and this
		// attach. Signal the caller to retry.
		s.attachMu.Unlock()
		close(ch)
		return nil
	}
	if existing, ok := s.attaches[connID]; ok {
		existing.close()
	}
	s.attaches[connID] = st
	s.attachMu.Unlock()

	var replay []byte
	checkpoint, body := s.scrollback.ReplaySnapshot()
	if len(checkpoint) > 0 || len(body) > 0 {
		replay = make([]byte, 0, len(checkpoint)+len(screenResetSeq)+len(body))
		replay = append(replay, checkpoint...)
		replay = append(replay, screenResetSeq...)
		replay = append(replay, body...)
	}

	return &localAttachment{connID: connID, output: ch, scrollback: replay, session: s}
}

// detach releases the attachment identified by connID and reports whether
// the session has no remaining clients. The caller (PTYManager.Detach)
// arms the grace-period kill timer only when empty=true.
func (s *ptySession) detach(connID string) (empty bool) {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	if st, ok := s.attaches[connID]; ok {
		st.close()
		delete(s.attaches, connID)
	}
	return len(s.attaches) == 0
}

// attached reports whether at least one client is currently attached.
func (s *ptySession) attached() bool {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	return len(s.attaches) > 0
}

// attachmentCount returns the number of concurrent clients attached to this
// session. Used by the service layer to expose attached_clients on the tab
// DTO for multi-viewer UI treatment.
func (s *ptySession) attachmentCount() int {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	return len(s.attaches)
}

func (s *ptySession) lastOutputUnixNano() int64 { return s.lastOutput.Load() }
func (s *ptySession) createdUnixNano() int64    { return s.createdAt }

func (s *ptySession) writeInput(p []byte) (int, error) { return s.upstream.Write(p) }

func (s *ptySession) resize(ctx context.Context, cols, rows uint16) error {
	return s.upstream.Resize(ctx, cols, rows)
}

func (s *ptySession) armKillTimer(after time.Duration, fire func()) {
	s.killMu.Lock()
	defer s.killMu.Unlock()
	if s.killTimer != nil {
		s.killTimer.Stop()
	}
	s.killTimer = time.AfterFunc(after, fire)
}

func (s *ptySession) cancelKillTimer() {
	s.killMu.Lock()
	defer s.killMu.Unlock()
	if s.killTimer != nil {
		s.killTimer.Stop()
		s.killTimer = nil
	}
}

// close tears down the upstream. Idempotent. reason is
// recorded (first-writer-wins via closeOnce) so attached clients can
// distinguish an explicit kill from a child exit or shutdown via
// Attachment.ExitReason().
func (s *ptySession) close(reason string) error {
	var firstErr error
	s.closeOnce.Do(func() {
		if reason != "" {
			s.closeReason.Store(reason)
		}
		close(s.done)
		s.cancelKillTimer()

		s.attachMu.Lock()
		for _, st := range s.attaches {
			st.close()
		}
		s.attaches = nil
		s.attachMu.Unlock()

		if s.upstream != nil {
			if err := s.upstream.Close(); err != nil {
				firstErr = err
			}
		}
	})
	return firstErr
}

// send attempts a non-blocking copy to the attachment's channel. Dropped
// frames are fine — the scrollback ring always has the ground truth and a
// reattach will replay it. Holds closeMu.RLock so a concurrent close()
// sees all in-flight sends complete before the channel is closed.
func (a *attachmentState) send(data []byte) {
	a.closeMu.RLock()
	defer a.closeMu.RUnlock()
	if a.closed {
		return
	}
	select {
	case a.ch <- data:
	default:
	}
}

// close closes the attachment's output channel exactly once so a waiting
// pump goroutine exits. Takes closeMu exclusively, which blocks briefly
// for any in-flight sends (each is non-blocking, so the wait is bounded).
func (a *attachmentState) close() {
	a.closeMu.Lock()
	defer a.closeMu.Unlock()
	if a.closed {
		return
	}
	a.closed = true
	close(a.ch)
}
