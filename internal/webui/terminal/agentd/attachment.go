package agentd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentdpb "github.com/tysonthomas9/loom-agentd/proto/agentdpb"

	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// outputBufferSize matches the in-process PTYManager's attachment buffer
// (terminal/pty_session.go::attachBufferSize) so an agentd-backed attachment
// has the same back-pressure shape as a local one. Producers (the recv
// goroutine) drop into a non-blocking send + close-on-overflow policy if the
// consumer falls behind — see runRecv for the exact handling.
const outputBufferSize = 64

// agentdAttachment is the real terminal.Attachment that owns a per-session
// agentd Terminal/Attach bidi stream. Construction sends the AttachOpen frame
// and consumes the first AttachReady; the recv goroutine then dispatches the
// remaining server frames into outputCh / scrollback / exitReason as they
// arrive. The attachment also owns the underlying agentd *grpc.ClientConn:
// closing the attachment cancels the stream context (terminating the recv
// goroutine) and tears down the conn.
//
// All exported methods are safe for concurrent use. The recv goroutine is
// the sole writer to outputCh; everything else takes mu before mutating
// scrollback / exitReason / closed flags.
type agentdAttachment struct {
	conn   *agentdConn
	stream agentdpb.Terminal_AttachClient

	// streamCtx scopes the stream's lifetime; cancel terminates Recv() with
	// a context-cancelled error and drains the recv goroutine. closeFn
	// triggers the cancel.
	streamCtx context.Context
	closeFn   context.CancelFunc

	connID     string
	reattached bool

	outputCh chan []byte

	// done is closed when the recv goroutine exits. close() blocks on this
	// so the caller can be sure no further state mutations happen after the
	// attachment is torn down.
	done chan struct{}

	mu         sync.Mutex
	scrollback []byte
	exitReason string
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
// the attachment owns conn from this point forward.
func newAgentdAttachment(ctx context.Context, conn *agentdConn, key terminal.SessionKey, cols, rows uint16, expectReplay bool, argvHint []string) (*agentdAttachment, bool, error) {
	if conn == nil || conn.client == nil {
		return nil, false, errors.New("agentd: newAgentdAttachment: nil conn")
	}

	streamCtx, cancel := context.WithCancel(context.Background())
	stream, err := conn.client.Attach(streamCtx)
	if err != nil {
		cancel()
		return nil, false, fmt.Errorf("agentd: open Attach stream: %w", err)
	}

	hint := ""
	if len(argvHint) > 0 {
		hint = argvHint[0]
	}

	open := &agentdpb.AttachClientMsg{
		Msg: &agentdpb.AttachClientMsg_Open{
			Open: &agentdpb.AttachOpen{
				Session:      key.Name,
				Cols:         uint32(cols),
				Rows:         uint32(rows),
				CommandHint:  hint,
				ExpectReplay: expectReplay,
			},
		},
	}
	if err := stream.Send(open); err != nil {
		cancel()
		return nil, false, fmt.Errorf("agentd: send AttachOpen: %w", err)
	}

	// The very first server frame must be AttachReady — anything else is a
	// protocol violation. Use the supplied ctx for the wait so a slow
	// agentd can't wedge AttachSession past the caller's deadline.
	type firstFrame struct {
		msg *agentdpb.AttachServerMsg
		err error
	}
	firstCh := make(chan firstFrame, 1)
	go func() {
		msg, err := stream.Recv()
		firstCh <- firstFrame{msg: msg, err: err}
	}()

	var first firstFrame
	select {
	case <-ctx.Done():
		cancel()
		return nil, false, ctx.Err()
	case first = <-firstCh:
	}

	if first.err != nil {
		cancel()
		return nil, false, fmt.Errorf("agentd: recv AttachReady: %w", first.err)
	}
	ready := first.msg.GetReady()
	if ready == nil {
		cancel()
		return nil, false, status.Errorf(codes.FailedPrecondition,
			"agentd: first server frame was not AttachReady (got %T)", first.msg.GetMsg())
	}

	att := &agentdAttachment{
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

// runRecv is the single producer for outputCh / scrollback / exitReason.
// Loops on stream.Recv() and dispatches each frame; exits on Killed, EOF,
// or any other Recv error. Closing outputCh is the signal to consumers that
// no more output will arrive — they should then call ExitReason() to learn
// why.
func (a *agentdAttachment) runRecv() {
	defer close(a.done)

	for {
		msg, err := a.stream.Recv()
		if err != nil {
			a.handleRecvError(err)
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
				return
			}
		case *agentdpb.AttachServerMsg_Replay:
			// Replay arrives at most once and only before any live Output —
			// see the proto comment on AttachReplay. We snapshot the bytes
			// under mu so Scrollback() can return them verbatim. The first
			// 4 bytes are the screen-reset escape (\x1b[2J\x1b[H) per the
			// proto contract; we propagate them as-is so the WS handler
			// emits the exact same "clear + replay" sequence the local
			// PTYManager produces.
			data := m.Replay.GetData()
			buf := make([]byte, len(data))
			copy(buf, data)
			a.mu.Lock()
			a.scrollback = buf
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
			// A second Ready frame is a protocol violation; ignore it
			// rather than crash. The first Ready is consumed in the
			// constructor.
		default:
			// Unknown oneof case — ignore and keep reading. New server
			// messages should never break older clients.
		}
	}
}

// handleRecvError records a stream-level error reason and closes outputCh.
// Cancellation (the user closed the attachment) does not clobber a
// previously-recorded ExitReason — Killed is preferred when present.
func (a *agentdAttachment) handleRecvError(err error) {
	if errors.Is(err, io.EOF) {
		// Stream ended cleanly without a Killed frame; treat as normal
		// termination with no recorded reason.
		a.closeOutputOnce()
		return
	}
	if a.streamCtx.Err() != nil && (errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled) {
		// Local close: don't synthesize a stream_error.
		a.closeOutputOnce()
		return
	}
	a.mu.Lock()
	if a.exitReason == "" {
		a.exitReason = "stream_error: " + err.Error()
	}
	a.mu.Unlock()
	a.closeOutputOnce()
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

// ConnID returns the agentd-assigned connection identifier captured from the
// AttachReady frame. Stable for the lifetime of the attachment.
func (a *agentdAttachment) ConnID() string { return a.connID }

// Output returns the output channel. Closed when the attachment ends —
// callers should consult ExitReason() after observing a closed channel.
func (a *agentdAttachment) Output() <-chan []byte { return a.outputCh }

// WriteInput forwards p to the agentd as an AttachInput frame. Returns
// (len(p), nil) on success; on a stream-level error it records the failure
// in exitReason (so a subsequent ExitReason() call surfaces the cause) and
// returns (0, err) so the caller can choose to give up immediately.
func (a *agentdAttachment) WriteInput(p []byte) (int, error) {
	if a == nil || a.stream == nil {
		return 0, errors.New("agentd: WriteInput on nil attachment")
	}
	if len(p) == 0 {
		return 0, nil
	}
	msg := &agentdpb.AttachClientMsg{
		Msg: &agentdpb.AttachClientMsg_Input{
			Input: &agentdpb.AttachInput{Data: append([]byte(nil), p...)},
		},
	}
	if err := a.stream.Send(msg); err != nil {
		return 0, fmt.Errorf("agentd: send AttachInput: %w", err)
	}
	return len(p), nil
}

// Scrollback returns the most recently received AttachReplay payload, or
// nil if no replay has arrived yet (or expect_replay was false at attach
// time). The agentd payload already begins with the screen-reset escape
// per AttachReplay's proto comment, so callers should emit it verbatim
// before live output. Phase 3 exposes the bytes; Phase 4 (plan-rbp.4)
// owns the "exactly once" emission semantics.
func (a *agentdAttachment) Scrollback() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.scrollback == nil {
		return nil
	}
	out := make([]byte, len(a.scrollback))
	copy(out, a.scrollback)
	return out
}

// Resize forwards the new dimensions to agentd as an AttachResize frame.
// The connID argument is accepted for terminal.Attachment compatibility
// but ignored — agentd uses the in-stream session identity established by
// AttachOpen.
func (a *agentdAttachment) Resize(_ string, cols, rows uint16) error {
	if a == nil || a.stream == nil {
		return errors.New("agentd: Resize on nil attachment")
	}
	msg := &agentdpb.AttachClientMsg{
		Msg: &agentdpb.AttachClientMsg_Resize{
			Resize: &agentdpb.AttachResize{Cols: uint32(cols), Rows: uint32(rows)},
		},
	}
	if err := a.stream.Send(msg); err != nil {
		return fmt.Errorf("agentd: send AttachResize: %w", err)
	}
	return nil
}

// ExitReason returns the captured AttachKilled.reason or a synthesized
// "stream_error: …" message when the recv path failed without a Killed
// frame. Empty while the attachment is live; only meaningful after the
// caller has observed Output() closed.
func (a *agentdAttachment) ExitReason() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.exitReason
}

// close cancels the stream context (terminating the recv goroutine) and
// closes the underlying agentd conn. Idempotent: a second call returns
// immediately. The optional reason is recorded as exitReason iff none was
// captured by the recv loop — a Killed frame already in flight or already
// processed always wins.
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
	_ = a.conn.Close()
}

// Compile-time assertions: agentdAttachment must satisfy terminal.Attachment
// so AttachSession's return type compiles.
var _ terminal.Attachment = (*agentdAttachment)(nil)
