package pty

import (
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creack/pty"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

const terminalReadBufferSize = 32 * 1024

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
// frames rather than back-pressuring the shell. The replay timeline always has
// the ground truth.
const attachBufferSize = 64

// localAttachment is the in-process Attachment implementation. It wraps the
// session's PTY fd directly. An Attachment interface exists in source.go so
// a future gRPC-backed implementation can plug into the WS handler unchanged.
type localAttachment struct {
	connID string
	pty    *os.File
	output chan []byte
	replay []interaction.TerminalReplayEvent
	// session is held so ExitReason can report *why* the output channel
	// closed (the reason is stored on the session). Keeping a pointer after
	// close() is safe — closeReason is written before the channel is closed,
	// and the attachment is only consulted by a goroutine that already
	// observed the close.
	session *ptySession
}

func (a *localAttachment) ConnID() string                            { return a.connID }
func (a *localAttachment) Output() <-chan []byte                     { return a.output }
func (a *localAttachment) Replay() []interaction.TerminalReplayEvent { return a.replay }

// WriteInput writes keystrokes to the shared PTY fd. Multiple attachments
// share the same *os.File; POSIX guarantees concurrent write(2) calls up
// to PIPE_BUF (4096 bytes) are atomic, and terminal keystrokes are far
// below that, so no mutex is needed on this path.
func (a *localAttachment) WriteInput(p []byte) (int, error) { return a.pty.Write(p) }
func (a *localAttachment) Resize(_ string, cols, rows uint16) error {
	if a.session == nil {
		return pty.Setsize(a.pty, &pty.Winsize{Cols: cols, Rows: rows})
	}
	return a.session.resize(cols, rows)
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

// ptySession owns the PTY fd, child process, replay timeline, and the set
// of concurrent attachments (multi-client) for one (workspace, session)
// pair. Each attachment is a separate WebSocket / viewer that sees the
// same output and can write input into the shared PTY.
type ptySession struct {
	key       SessionKey
	pty       *os.File
	cmd       *exec.Cmd
	replay    *replayBuffer
	createdAt int64 // unix nanos

	lastOutput atomic.Int64 // unix nanos, updated by drain

	attachMu sync.Mutex
	attaches map[string]*attachmentState // keyed by connID

	killMu    sync.Mutex
	killTimer *time.Timer

	closeOnce   sync.Once
	closeReason atomic.Value // string; stored before the output channels close
	done        chan struct{}
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

func newPtySession(key SessionKey, f *os.File, cmd *exec.Cmd, cols, rows uint16) *ptySession {
	session := &ptySession{
		key:       key,
		pty:       f,
		cmd:       cmd,
		replay:    newReplayBuffer(defaultRingCapacity),
		createdAt: time.Now().UnixNano(),
		attaches:  make(map[string]*attachmentState),
		done:      make(chan struct{}),
	}
	session.replay.AppendResize(cols, rows)
	return session
}

// drain reads from the PTY forever: bytes go into the replay timeline and a
// best-effort copy is fanned out to every current attachment. Exits when
// the PTY returns an error (child exit, fd close). On exit it asks the
// manager to clean up the session unless close() has already marked it
// done.
func (s *ptySession) drain(m *PTYManager) {
	buf := make([]byte, terminalReadBufferSize)
	// Reused across iterations to avoid per-chunk allocation. Sized once
	// to a typical attachment count; grows naturally if needed.
	snapshot := make([]*attachmentState, 0, 4)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.lastOutput.Store(time.Now().UnixNano())

			// Append and register attachments under the same lock. That
			// makes the replay snapshot and live-output handoff atomic: an
			// output chunk is present in exactly one side of the boundary.
			s.attachMu.Lock()
			s.replay.AppendOutput(chunk)
			snapshot = snapshot[:0]
			for _, st := range s.attaches {
				snapshot = append(snapshot, st)
			}
			s.attachMu.Unlock()
			for _, st := range snapshot {
				st.send(chunk)
			}
		}
		if err != nil {
			select {
			case <-s.done:
				// close() already tore the session down.
			default:
				m.onSessionExited(s.key)
			}
			return
		}
	}
}

// attachNew adds a fresh attachment to the session without disturbing any
// existing ones (multi-client). The returned attachment carries a bounded
// ordered output/resize timeline so the new client reconstructs the same
// grid state as existing clients.
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
	replay := s.replay.Snapshot()
	s.attaches[connID] = st
	s.attachMu.Unlock()

	return &localAttachment{
		connID: connID, pty: s.pty, output: ch,
		replay: replay, session: s,
	}
}

// resize records the geometry change and applies it to the PTY under the same
// ordering lock used by output capture and attachment snapshots. Replayers can
// therefore apply resize and output events in the order the live terminal saw.
func (s *ptySession) resize(cols, rows uint16) error {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	s.replay.AppendResize(cols, rows)
	return pty.Setsize(s.pty, &pty.Winsize{Cols: cols, Rows: rows})
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

// close tears down the PTY and child process. Idempotent. reason is
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

		if s.pty != nil {
			if err := s.pty.Close(); err != nil {
				firstErr = err
			}
		}
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
			_ = s.cmd.Wait()
		}
	})
	return firstErr
}

// send attempts a non-blocking copy to the attachment's channel. Dropped
// frames are fine — the replay timeline always has the ground truth and a
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
