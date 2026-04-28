package agentd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentdpb "github.com/tysonthomas9/loom-agentd/proto/agentdpb"

	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// outputBufferSize is the agentd-backed attachment's output channel cap;
// reuses terminal.AttachBufferSize so a tuning change lands in one place.
const outputBufferSize = terminal.AttachBufferSize

// reconnectAttempts caps the number of dial+attach retries the recv loop
// will run after a bare stream close. The total wall-clock budget is roughly
// the sum of reconnectBackoff plus a few hundred ms for the dial+ready
// handshakes — about 3 s on the slow path. Tunable surfaces are a Phase 5
// concern; bake the values in for now.
const reconnectAttempts = 3

// reconnectBackoff is the per-attempt sleep schedule applied before the Nth
// reconnect attempt (index 0 → before the first retry). Length must equal
// reconnectAttempts.
var reconnectBackoff = []time.Duration{
	200 * time.Millisecond,
	800 * time.Millisecond,
	2 * time.Second,
}

// agentdAttachment is the real terminal.Attachment that owns a per-session
// agentd Terminal/Attach bidi stream. Construction sends the AttachOpen frame
// and consumes the first AttachReady; the recv goroutine then dispatches the
// remaining server frames into outputCh / scrollback / exitReason as they
// arrive. The attachment also owns the underlying agentd *grpc.ClientConn:
// closing the attachment cancels the stream context (terminating the recv
// goroutine) and tears down the conn.
//
// Reconnect: when the stream closes WITHOUT an AttachKilled frame (host
// blip, agentd restart, gRPC keepalive timeout) the recv loop transparently
// rebuilds the conn + stream against the same routing tuple captured at
// construction time. Up to reconnectAttempts retries with reconnectBackoff
// delays; on exhaustion exitReason is set to "reconnect_failed: <last err>"
// and outputCh is closed so the WS handler tears down. AttachKilled remains
// the explicit "session is dead, do not reconnect" signal.
//
// All exported methods are safe for concurrent use. The recv goroutine is
// the sole writer to outputCh; everything else takes mu before mutating
// scrollback / exitReason / closed flags.
type agentdAttachment struct {
	// Routing tuple captured at construction time. The recv loop replays
	// dialAgentd against this tuple on a bare stream close — no control-
	// plane round trip during reconnect (the cache + the original tuple
	// are sufficient for Phase 4; vm-host migration is a Phase 5+ concern).
	dial    agentdDialer
	vmHost  string
	port    int32
	tlsCfg  *tls.Config
	session string
	cols    uint16
	rows    uint16
	hint    string

	// conn + stream are mutated under mu by the recv loop on reconnect.
	// External callers must take mu before reading either pointer.
	conn   *agentdConn
	stream agentdpb.Terminal_AttachClient

	// streamCtx scopes the attachment's lifetime; cancel terminates the
	// recv loop (including any in-flight reconnect sleeps) and unblocks
	// close(). Only ever cancelled by close().
	streamCtx context.Context
	closeFn   context.CancelFunc

	// connID tracks the agentd-assigned identifier from the latest
	// AttachReady. It can change across reconnects — agentd is free to mint
	// a fresh ID for each Attach stream.
	connID     string
	reattached bool

	outputCh chan []byte

	// done is closed when the recv goroutine exits. close() blocks on this
	// so the caller can be sure no further state mutations happen after the
	// attachment is torn down.
	done chan struct{}

	mu sync.Mutex
	// scrollback holds the most recent AttachReplay payload. replayConsumed
	// flips on the first Scrollback() call so a misbehaving caller can't
	// double-render. A successful reconnect resets the flag because a fresh
	// replay frame will arrive that the consumer should see.
	scrollback      []byte
	replayConsumed  bool
	exitReason      string
	// closed guards close() against being called twice (e.g. by an
	// AgentdClient.Detach + an AgentdClient.Kill racing each other). It
	// also signals "outputCh has already been closed by the recv goroutine"
	// so a late close() doesn't double-close.
	closed bool
}

// newAgentdAttachment opens an Attach stream against conn, sends the initial
// AttachOpen frame, and waits for the AttachReady. Returns the attachment,
// the reattached flag from agentd, and any error encountered while
// negotiating the stream. argvHint's first element (if any) is forwarded as
// AttachOpen.command_hint.
//
// The caller is responsible for closing the returned attachment when done;
// the attachment owns conn from this point forward. The dial / vmHost / port
// / tlsCfg quartet is captured so the recv loop can rebuild the conn on a
// transient stream close without going back through control-plane Resolve.
func newAgentdAttachment(ctx context.Context, dial agentdDialer, conn *agentdConn, vmHost string, port int32, tlsCfg *tls.Config, key terminal.SessionKey, cols, rows uint16, expectReplay bool, argvHint []string) (*agentdAttachment, bool, error) {
	if conn == nil || conn.client == nil {
		return nil, false, errors.New("agentd: newAgentdAttachment: nil conn")
	}
	if dial == nil {
		return nil, false, errors.New("agentd: newAgentdAttachment: nil dialer")
	}

	hint := ""
	if len(argvHint) > 0 {
		hint = argvHint[0]
	}

	streamCtx, cancel := context.WithCancel(context.Background())
	stream, ready, err := openAttachStream(ctx, streamCtx, conn, key.Name, cols, rows, hint, expectReplay)
	if err != nil {
		cancel()
		return nil, false, err
	}

	att := &agentdAttachment{
		dial:       dial,
		vmHost:     vmHost,
		port:       port,
		tlsCfg:     tlsCfg,
		session:    key.Name,
		cols:       cols,
		rows:       rows,
		hint:       hint,
		conn:       conn,
		stream:     stream,
		streamCtx:  streamCtx,
		closeFn:    cancel,
		connID:     ready.GetConnId(),
		reattached: ready.GetReattached(),
		outputCh:   make(chan []byte, outputBufferSize),
		done:       make(chan struct{}),
	}

	go att.runRecv()

	return att, ready.GetReattached(), nil
}

// openAttachStream is the shared "open Attach + send AttachOpen + wait for
// AttachReady" handshake used both by initial construction and by reconnect.
// streamCtx scopes the long-lived stream's lifetime; ctx is the caller's
// short-lived deadline for the handshake itself — a slow agentd cannot wedge
// AttachSession or the reconnect loop past that deadline.
func openAttachStream(ctx, streamCtx context.Context, conn *agentdConn, session string, cols, rows uint16, hint string, expectReplay bool) (agentdpb.Terminal_AttachClient, *agentdpb.AttachReady, error) {
	stream, err := conn.client.Attach(streamCtx)
	if err != nil {
		return nil, nil, fmt.Errorf("agentd: open Attach stream: %w", err)
	}

	open := &agentdpb.AttachClientMsg{
		Msg: &agentdpb.AttachClientMsg_Open{
			Open: &agentdpb.AttachOpen{
				Session:      session,
				Cols:         uint32(cols),
				Rows:         uint32(rows),
				CommandHint:  hint,
				ExpectReplay: expectReplay,
			},
		},
	}
	if err := stream.Send(open); err != nil {
		return nil, nil, fmt.Errorf("agentd: send AttachOpen: %w", err)
	}

	// The very first server frame must be AttachReady — anything else is a
	// protocol violation.
	type firstFrame struct {
		msg *agentdpb.AttachServerMsg
		err error
	}
	firstCh := make(chan firstFrame, 1)
	// Per-handshake context derived from streamCtx. Cancelling this on
	// ctx.Done() unblocks the Recv goroutine immediately so it can't leak
	// past the handshake deadline. We can't cancel streamCtx itself —
	// callers (initial AttachSession + reconnect loop) reuse it for the
	// long-lived stream that follows a successful handshake.
	recvCtx, cancelRecv := context.WithCancel(streamCtx)
	go func() {
		msg, err := stream.Recv()
		select {
		case firstCh <- firstFrame{msg: msg, err: err}:
		case <-recvCtx.Done():
		}
	}()

	var first firstFrame
	select {
	case <-ctx.Done():
		cancelRecv()
		// Tear down the half-open stream so the server-side Attach handler
		// returns; otherwise we'd leave a one-sided stream on conn until
		// TCP eventually times out.
		_ = stream.CloseSend()
		return nil, nil, ctx.Err()
	case first = <-firstCh:
		cancelRecv()
	}

	if first.err != nil {
		return nil, nil, fmt.Errorf("agentd: recv AttachReady: %w", first.err)
	}
	ready := first.msg.GetReady()
	if ready == nil {
		return nil, nil, status.Errorf(codes.FailedPrecondition,
			"agentd: first server frame was not AttachReady (got %T)", first.msg.GetMsg())
	}
	return stream, ready, nil
}

// runRecv is the single producer for outputCh / scrollback / exitReason.
// Loops on stream.Recv() and dispatches each frame; on a bare stream close
// (EOF or non-Killed error) it triggers a reconnect against the cached
// routing tuple. Exits on AttachKilled, on close() cancellation, or after
// reconnect retries are exhausted. Closing outputCh is the signal to
// consumers that no more output will arrive — they should then call
// ExitReason() to learn why.
func (a *agentdAttachment) runRecv() {
	defer close(a.done)

	for {
		// Snapshot the current stream pointer under mu — reconnect mutates
		// it while the loop runs.
		a.mu.Lock()
		stream := a.stream
		a.mu.Unlock()

		msg, err := stream.Recv()
		if err != nil {
			if a.streamCtx.Err() != nil {
				// Local close: don't synthesize a stream_error and don't
				// attempt reconnect — the user has torn the attachment down.
				a.closeOutputOnce()
				return
			}
			// Bare stream close (EOF, Unavailable, keepalive timeout, …).
			// Try to reconnect; if that fails terminally we close outputCh
			// with a reconnect_failed exitReason.
			if a.tryReconnect(err) {
				continue
			}
			return
		}
		switch m := msg.GetMsg().(type) {
		case *agentdpb.AttachServerMsg_Output:
			data := m.Output.GetData()
			if len(data) == 0 {
				continue
			}
			// Copy the protobuf-owned slice so a future Reset by the gRPC
			// runtime can't surprise the consumer with mutated bytes.
			buf := make([]byte, len(data))
			copy(buf, data)
			select {
			case a.outputCh <- buf:
			case <-a.streamCtx.Done():
				// Attachment is being torn down; drop the frame and exit.
				a.closeOutputOnce()
				return
			}
		case *agentdpb.AttachServerMsg_Replay:
			// Replay arrives at most once per stream, before any live
			// Output. We snapshot the bytes under mu so Scrollback() can
			// return them verbatim. The first 4 bytes are the screen-reset
			// escape (\x1b[2J\x1b[H) per the proto contract; we propagate
			// them as-is so the WS handler emits the exact same "clear +
			// replay" sequence the local PTYManager produces.
			//
			// A second Replay on the same stream is a protocol violation;
			// rather than crash, append to the existing buffer if it hasn't
			// been consumed yet, otherwise drop. Reconnect explicitly resets
			// replayConsumed so a fresh replay frame after a transient
			// disconnect surfaces to the consumer.
			data := m.Replay.GetData()
			a.mu.Lock()
			if a.replayConsumed {
				// Consumer already drained the prior buffer; drop the dup.
			} else if a.scrollback == nil {
				buf := make([]byte, len(data))
				copy(buf, data)
				a.scrollback = buf
			} else {
				a.scrollback = append(a.scrollback, data...)
			}
			a.mu.Unlock()
		case *agentdpb.AttachServerMsg_Killed:
			a.mu.Lock()
			if a.exitReason == "" {
				a.exitReason = m.Killed.GetReason()
			}
			a.mu.Unlock()
			a.closeOutputOnce()
			return
		case *agentdpb.AttachServerMsg_Ready:
			// A second Ready frame on the live recv path is a protocol
			// violation; ignore it rather than crash. The first Ready is
			// consumed in openAttachStream.
		default:
			// Unknown oneof case — ignore and keep reading. New server
			// messages should never break older clients.
		}
	}
}

// tryReconnect drives the bounded retry loop after a bare stream close.
// Returns true if it successfully re-established the stream (caller should
// resume the recv loop), false if all retries were exhausted or close() was
// called mid-reconnect (caller should exit; outputCh is already closed when
// false is returned).
//
// The first error (initialErr) is preserved for the exitReason on terminal
// failure when no later attempt produces a more specific one.
func (a *agentdAttachment) tryReconnect(initialErr error) bool {
	// Tear down the current stream + conn before retrying. A new conn is
	// minted per attempt against the original routing tuple.
	a.mu.Lock()
	oldConn := a.conn
	a.conn = nil
	a.stream = nil
	a.mu.Unlock()
	if oldConn != nil {
		_ = oldConn.Close()
	}

	lastErr := initialErr
	for i := 0; i < reconnectAttempts; i++ {
		// Honour close() cancellation in the backoff sleep so a Kill while
		// reconnecting tears down promptly.
		select {
		case <-a.streamCtx.Done():
			a.closeOutputOnce()
			return false
		case <-time.After(reconnectBackoff[i]):
		}

		// Fresh handshake deadline per attempt. Keep it generous — slow
		// agentd plus mTLS plus first RPC can easily eat 1-2 s.
		dialCtx, cancel := context.WithTimeout(a.streamCtx, 5*time.Second)
		conn, err := a.dial(dialCtx, a.vmHost, a.port, a.tlsCfg)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}

		stream, ready, err := openAttachStream(dialCtx, a.streamCtx, conn, a.session, a.cols, a.rows, a.hint, true /* expectReplay on reconnect */)
		cancel()
		if err != nil {
			_ = conn.Close()
			lastErr = err
			continue
		}

		// Success. Install the new conn / stream and let the recv loop
		// resume. Reset replayConsumed so the post-reconnect replay frame
		// will be exposed to the consumer; clear the prior scrollback so
		// the new replay overwrites cleanly (the next Replay frame
		// allocates a fresh buffer).
		a.mu.Lock()
		a.conn = conn
		a.stream = stream
		a.connID = ready.GetConnId()
		a.reattached = true
		a.scrollback = nil
		a.replayConsumed = false
		a.mu.Unlock()
		return true
	}

	// All retries exhausted. Record the failure and close outputCh.
	a.mu.Lock()
	if a.exitReason == "" {
		a.exitReason = "reconnect_failed: " + lastErr.Error()
	}
	a.mu.Unlock()
	a.closeOutputOnce()
	return false
}

// closeOutputOnce closes outputCh exactly once. Subsequent calls are no-ops.
// Invariant: only the recv goroutine and close() should call this.
func (a *agentdAttachment) closeOutputOnce() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	a.mu.Unlock()
	close(a.outputCh)
}

// ConnID returns the agentd-assigned connection identifier captured from
// the most recent AttachReady frame. Stable for the duration of a single
// stream; can change across reconnects when agentd assigns a fresh ID.
func (a *agentdAttachment) ConnID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.connID
}

// Output returns the output channel. Closed when the attachment ends —
// callers should consult ExitReason() after observing a closed channel.
func (a *agentdAttachment) Output() <-chan []byte { return a.outputCh }

// WriteInput forwards p to the agentd as an AttachInput frame. Returns
// (len(p), nil) on success; on a stream-level error it records the failure
// in exitReason (so a subsequent ExitReason() call surfaces the cause) and
// returns (0, err) so the caller can choose to give up immediately.
//
// During reconnect the stream pointer is nil — the call returns
// codes.Unavailable so the caller can drop or buffer. xterm.js naturally
// queues keystrokes typed during a sub-3-second outage at the websocket
// layer, so an occasional Unavailable here is acceptable; the user can
// retype if the keystroke landed in the gap.
func (a *agentdAttachment) WriteInput(p []byte) (int, error) {
	if a == nil {
		return 0, errors.New("agentd: WriteInput on nil attachment")
	}
	if len(p) == 0 {
		return 0, nil
	}
	a.mu.Lock()
	stream := a.stream
	a.mu.Unlock()
	if stream == nil {
		return 0, status.Error(codes.Unavailable, "agentd: WriteInput during reconnect")
	}
	msg := &agentdpb.AttachClientMsg{
		Msg: &agentdpb.AttachClientMsg_Input{
			Input: &agentdpb.AttachInput{Data: append([]byte(nil), p...)},
		},
	}
	if err := stream.Send(msg); err != nil {
		return 0, fmt.Errorf("agentd: send AttachInput: %w", err)
	}
	return len(p), nil
}

// Scrollback returns the most recently received AttachReplay payload, or
// nil if no replay has arrived yet (or expect_replay was false at attach
// time). The agentd payload already begins with the screen-reset escape
// per AttachReplay's proto comment, so callers should emit it verbatim
// before live output.
//
// "Consume on first read": once a non-nil replay is returned, subsequent
// calls return nil until a reconnect resets the flag. This guards against
// a misbehaving caller (or a realtime.WSToPTY retry) double-rendering the
// scrollback.
func (a *agentdAttachment) Scrollback() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.replayConsumed || a.scrollback == nil {
		return nil
	}
	out := make([]byte, len(a.scrollback))
	copy(out, a.scrollback)
	a.replayConsumed = true
	return out
}

// Resize forwards the new dimensions to agentd as an AttachResize frame.
// The connID argument is accepted for terminal.Attachment compatibility
// but ignored — agentd uses the in-stream session identity established by
// AttachOpen. Also updates the cached cols/rows so a subsequent reconnect
// re-opens the stream at the latest dimensions.
func (a *agentdAttachment) Resize(_ string, cols, rows uint16) error {
	if a == nil {
		return errors.New("agentd: Resize on nil attachment")
	}
	a.mu.Lock()
	stream := a.stream
	a.cols = cols
	a.rows = rows
	a.mu.Unlock()
	if stream == nil {
		return status.Error(codes.Unavailable, "agentd: Resize during reconnect")
	}
	msg := &agentdpb.AttachClientMsg{
		Msg: &agentdpb.AttachClientMsg_Resize{
			Resize: &agentdpb.AttachResize{Cols: uint32(cols), Rows: uint32(rows)},
		},
	}
	if err := stream.Send(msg); err != nil {
		return fmt.Errorf("agentd: send AttachResize: %w", err)
	}
	return nil
}

// ExitReason returns the captured AttachKilled.reason, a "stream_error: …"
// message when the recv path failed without a Killed frame, or
// "reconnect_failed: …" when reconnect retries were exhausted. Empty while
// the attachment is live; only meaningful after the caller has observed
// Output() closed.
func (a *agentdAttachment) ExitReason() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.exitReason
}

// close cancels the stream context (terminating the recv goroutine and
// any in-flight reconnect sleep) and closes the underlying agentd conn.
// Idempotent: a second call returns immediately. The optional reason is
// recorded as exitReason iff none was captured by the recv loop — a Killed
// frame already in flight or already processed always wins.
func (a *agentdAttachment) close(reason string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	if reason != "" && a.exitReason == "" {
		a.exitReason = reason
	}
	a.mu.Unlock()

	a.closeFn()
	<-a.done
	a.mu.Lock()
	conn := a.conn
	a.conn = nil
	a.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

// Compile-time assertions: agentdAttachment must satisfy terminal.Attachment
// so AttachSession's return type compiles.
var _ terminal.Attachment = (*agentdAttachment)(nil)
