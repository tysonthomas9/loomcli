package leadcontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/olesho/harness-wrapper/pkg/chat"
	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	// harnessAcquireControlTimeout bounds how long a delivery waits for the
	// chat-layer control token before giving the message back to the queue.
	harnessAcquireControlTimeout = 5 * time.Second
	// harnessOutputQuietWindow is how long the PTY must be quiet before a
	// turn is injected, so delivered text does not interleave with streaming
	// output or a half-typed human message (keystroke echo resets the clock).
	harnessOutputQuietWindow = 2 * time.Second
	// harnessGenericQuietWindow is the longer window for the generic adapter,
	// whose turn detection is heuristic.
	harnessGenericQuietWindow = 3 * time.Second
	// harnessInFlightOverrideWindow guards against a missed turn-completion
	// marker wedging delivery forever: an in-flight turn whose PTY has been
	// quiet this long is treated as finished.
	harnessInFlightOverrideWindow = 90 * time.Second

	harnessRegistryMissReason = "lead runtime is owned by another process; message queued for in-runtime delivery"
)

// leadConversationHandle pairs the in-process conversation with the delivery
// bookkeeping shared between the lead runtime (which observes turn events)
// and the deliverer (which must not paste while a turn is open — a failed
// post-paste submit would leave stray text in the input box and a retry would
// paste it again).
type leadConversationHandle struct {
	conv harnessConversation

	mu             sync.Mutex
	inFlight       bool
	inFlightSince  time.Time
	inputPending   bool
	inputRequestID string
	// stagedText is the message body already typed into the TUI composer by
	// a send attempt whose submit never fired (the harness wasn't accepting
	// input before the send timeout). A retry of the SAME message must skip
	// re-staging or the composer accumulates duplicate copies.
	stagedText string
}

func (h *leadConversationHandle) staged() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stagedText
}

func (h *leadConversationHandle) setStaged(text string) {
	h.mu.Lock()
	h.stagedText = text
	h.mu.Unlock()
}

func (h *leadConversationHandle) markTurnStarted() {
	h.mu.Lock()
	h.inFlight = true
	h.inFlightSince = time.Now()
	h.mu.Unlock()
}

func (h *leadConversationHandle) markTurnDone() {
	h.mu.Lock()
	h.inFlight = false
	h.inFlightSince = time.Time{}
	h.mu.Unlock()
}

// observeConversationEvent updates delivery bookkeeping from the chat event
// stream. Input request lifecycle events do not describe turn progress, but
// they must pause raw PTY staging so a queued assignment cannot be pasted into
// an interactive harness dialog.
// Called by the lead runtime's event watcher goroutine.
func (h *leadConversationHandle) observeConversationEvent(ev chat.ConversationEvent) {
	switch ev.Type {
	case chat.EventInputRequest:
		h.mu.Lock()
		h.inputPending = true
		if ev.Input != nil {
			h.inputRequestID = ev.Input.ID
		} else {
			h.inputRequestID = ""
		}
		h.mu.Unlock()
		return
	case chat.EventInputResolved:
		h.mu.Lock()
		if h.inputPending && (ev.Input == nil || ev.Input.ID == "" || h.inputRequestID == "" || ev.Input.ID == h.inputRequestID) {
			h.inputPending = false
			h.inputRequestID = ""
		}
		h.mu.Unlock()
		return
	case chat.EventTurn:
		// Continue below.
	default:
		return
	}
	if ev.Turn.Role != chat.RoleAssistant {
		return
	}
	switch ev.Turn.State {
	case chat.TurnStatePending, chat.TurnStateStreaming:
		h.markTurnStarted()
	case chat.TurnStateComplete, chat.TurnStateErrored:
		h.markTurnDone()
	}
}

func (h *leadConversationHandle) hasPendingInput() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.inputPending
}

// writeStdinUnlessInputPending serializes a raw PTY write with input-request
// event observation. Without this critical section a dialog could arrive
// after delivery's pending-input check but before the assignment bytes were
// staged, causing the assignment to be pasted into the dialog.
func (h *leadConversationHandle) writeStdinUnlessInputPending(p []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.inputPending {
		return chat.ErrInputPending
	}
	_, err := h.conv.WriteStdin(p)
	return err
}

func (h *leadConversationHandle) turnInFlight() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.inFlight {
		return false
	}
	// Missed-marker fallback: if the harness stopped producing output long
	// ago, the turn is finished even if no completion event arrived.
	snap := h.conv.Snapshot()
	if !snap.LastOutputAt.IsZero() && time.Since(snap.LastOutputAt) >= harnessInFlightOverrideWindow {
		h.inFlight = false
		h.inFlightSince = time.Time{}
		return false
	}
	return true
}

// leadConversationRegistry maps lead session IDs to the in-process harness
// conversation. harness-wrapper has no cross-process PTY attach, so only the
// process running the lead runtime can deliver turns; everywhere else the
// registry misses and delivery stays enqueue-only.
var leadConversationRegistry = struct {
	sync.RWMutex
	m map[string]*leadConversationHandle
}{m: map[string]*leadConversationHandle{}}

func registerLeadConversation(sessionID string, conv harnessConversation) (*leadConversationHandle, func()) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || conv == nil {
		return nil, func() {}
	}
	handle := &leadConversationHandle{conv: conv}
	leadConversationRegistry.Lock()
	leadConversationRegistry.m[sessionID] = handle
	leadConversationRegistry.Unlock()
	return handle, func() {
		leadConversationRegistry.Lock()
		if leadConversationRegistry.m[sessionID] == handle {
			delete(leadConversationRegistry.m, sessionID)
		}
		leadConversationRegistry.Unlock()
	}
}

func lookupLeadConversation(sessionID string) *leadConversationHandle {
	leadConversationRegistry.RLock()
	defer leadConversationRegistry.RUnlock()
	return leadConversationRegistry.m[strings.TrimSpace(sessionID)]
}

// harnessTurnDeliverer injects turns into a harness-wrapper-supervised lead
// session via the in-process conversation registry.
type harnessTurnDeliverer struct {
	providerName string
	runtime      HarnessRuntimeMetadata
}

func newHarnessTurnDeliverer(provider string, session *domain.AgentSession) *harnessTurnDeliverer {
	return &harnessTurnDeliverer{
		providerName: provider,
		runtime:      HarnessRuntimeMetadataFromSession(session),
	}
}

func (d *harnessTurnDeliverer) provider() string { return d.providerName }

func (d *harnessTurnDeliverer) hasRuntimeMetadata(metadata map[string]string) bool {
	return hasHarnessRuntimeMetadata(metadata)
}

func (d *harnessTurnDeliverer) notReadyReason() string {
	return "controlled lead runtime is not ready"
}

func (d *harnessTurnDeliverer) unsupportedReason(map[string]string) string {
	if !d.runtime.Controlled {
		return "lead session is not a controlled lead runtime"
	}
	return ""
}

func (d *harnessTurnDeliverer) pendingReason() string {
	switch d.runtime.Status {
	case RuntimeStatusStarting, "":
		return "controlled lead runtime is not ready"
	case RuntimeStatusDisconnected:
		return "controlled lead runtime is disconnected"
	case RuntimeStatusFailed:
		return "controlled lead runtime is failed"
	}
	return ""
}

func (d *harnessTurnDeliverer) claimedBy(sessionID string) string {
	return d.providerName + ":" + sessionID
}

func (d *harnessTurnDeliverer) populate(result *DeliveryResult, session *domain.AgentSession) {
	d.runtime = HarnessRuntimeMetadataFromSession(session)
	result.Provider = d.providerName
	result.HarnessRuntime = d.runtime
}

func (d *harnessTurnDeliverer) deliveredThreadID() string { return d.runtime.ChatSessionID }

func (d *harnessTurnDeliverer) deliverTurn(
	ctx context.Context,
	st store.Store,
	workspace string,
	sessionID string,
	result *DeliveryResult,
	message string,
	_ string,
) (*DeliveryResult, error) {
	handle := lookupLeadConversation(sessionID)
	if handle == nil {
		result.Reason = harnessRegistryMissReason
		return result, nil
	}

	snap := handle.conv.Snapshot()
	status := harnessRuntimeStatus(snap)
	d.runtime.Status = status
	result.HarnessRuntime = d.runtime
	_ = UpdateHarnessRuntimeMetadata(ctx, st, workspace, sessionID, d.runtime)
	if reason := d.turnBlockReason(handle, status, snap); reason != "" {
		result.Reason = reason
		return result, nil
	}

	acquireCtx, cancel := context.WithTimeout(ctx, harnessAcquireControlTimeout)
	defer cancel()
	release, err := handle.conv.AcquireControl(acquireCtx)
	if err != nil {
		result.Reason = fmt.Sprintf("lead harness control unavailable: %v", err)
		return result, nil
	}
	defer release()

	if err := sendHarnessTurn(ctx, handle, message); err != nil {
		if errors.Is(err, chat.ErrTurnInFlight) {
			result.Reason = "lead harness assistant turn is in flight"
		} else {
			result.Reason = err.Error()
		}
		return result, nil
	}
	handle.markTurnStarted()
	d.runtime.Status = RuntimeStatusActive
	result.HarnessRuntime = d.runtime
	_ = UpdateHarnessRuntimeMetadata(ctx, st, workspace, sessionID, d.runtime)
	result.State = DeliveryStateDelivered
	result.Reason = ""
	return result, nil
}

// turnBlockReason returns a non-empty pending reason when the harness is not
// ready to accept an injected turn: the harness failed, an assistant turn is
// in flight, or PTY output has not been quiet long enough.
func (d *harnessTurnDeliverer) turnBlockReason(handle *leadConversationHandle, status string, snap wrapper.Snapshot) string {
	if status == RuntimeStatusFailed {
		return "lead harness is failed"
	}
	if handle.hasPendingInput() {
		return "lead harness is waiting for interactive input"
	}
	if handle.turnInFlight() {
		return "lead harness assistant turn is in flight"
	}
	quiet := harnessOutputQuietWindow
	if strings.EqualFold(d.runtime.HarnessName, HarnessNameGeneric) {
		quiet = harnessGenericQuietWindow
	}
	if !snap.LastOutputAt.IsZero() && time.Since(snap.LastOutputAt) < quiet {
		return "lead harness output has not settled"
	}
	return ""
}

// harnessSubmitDelay separates staging the message text from the submitting
// carriage return. Claude Code treats text and a trailing CR arriving in one
// stdin chunk as a paste and swallows the newline instead of submitting, so
// the CR must land as its own key event after the TUI has ingested the text.
const harnessSubmitDelay = 300 * time.Millisecond

// harnessSendTimeout bounds one stage+submit attempt. Generous enough for a
// slow TUI redraw, short enough that a not-ready harness fails the attempt
// and the 2s drain ticker retries instead of wedging forever. A var so tests
// can exercise the timeout without a 10s wait.
var harnessSendTimeout = 10 * time.Second

// sendHarnessTurn stages the message into the TUI input and submits it.
// Multi-line bodies are framed as a bracketed paste so embedded newlines are
// content rather than per-line submits. The final empty Send writes a bare
// carriage return in a separate chunk (the submit keystroke) and registers
// the assistant turn with the chat layer.
//
// The whole call is bounded by harnessSendTimeout: the wrapper's Send blocks
// until its prompt-readiness heuristic matches the screen, and for a busy
// harness that can be indefinitely (claude's boot banner scrolls away and the
// heuristic never re-matches mid-run). An unbounded Send here permanently
// wedges the single drain goroutine — the exact live failure this guards
// against. On timeout the already-staged text is remembered on the handle so
// the retry submits it without typing a duplicate copy into the composer.
func sendHarnessTurn(ctx context.Context, handle *leadConversationHandle, message string) error {
	// Re-check after acquiring the control token. An input request may have
	// arrived after turnBlockReason ran but before delivery gained control.
	if handle.hasPendingInput() {
		return chat.ErrInputPending
	}
	if handle.staged() != message {
		payload := message
		if strings.ContainsAny(message, "\r\n") {
			payload = "\x1b[200~" + message + "\x1b[201~"
		}
		if err := handle.writeStdinUnlessInputPending([]byte(payload)); err != nil {
			return err
		}
		handle.setStaged(message)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(harnessSubmitDelay):
		}
	}
	// The timeout bounds only the submit: Send blocks in the wrapper's
	// prompt-readiness wait, which is the piece that can hang indefinitely.
	sendCtx, cancel := context.WithTimeout(ctx, harnessSendTimeout)
	defer cancel()
	if _, err := handle.conv.Send(sendCtx, ""); err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		// The wrapper's claude readiness heuristic only matches the boot
		// screen ("Claude Code" banner + composer prompt); once real output
		// scrolls the banner away it never re-matches, and Send times out
		// even at a live, idle composer. Delivery was already gated on the
		// PTY quiet window before this call, so the composer is not
		// mid-stream — submit the staged text directly. CSI 13u is the
		// unmodified Enter in claude's enhanced-keyboard mode, which is
		// always active under --dangerously-skip-permissions (the only mode
		// loom launches harness leads in).
		if werr := handle.writeStdinUnlessInputPending([]byte(harnessEnterKeystroke)); werr != nil {
			return werr
		}
	}
	handle.setStaged("")
	return nil
}

// harnessEnterKeystroke is CSI 13 u — the unmodified Enter key event in the
// kitty keyboard protocol claude's TUI enables. A bare \r would be swallowed
// as a paste fragment in that mode.
const harnessEnterKeystroke = "\x1b[13u"
