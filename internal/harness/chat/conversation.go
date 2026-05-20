package chat

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/harness/screen"
	"github.com/tysonthomas9/loomcli/internal/harness/turns"
	"github.com/tysonthomas9/loomcli/internal/harness/turns/generic"
	"github.com/tysonthomas9/loomcli/internal/harness/turns/harness/claudecode"
	"github.com/tysonthomas9/loomcli/internal/harness/turns/harness/codex"
	"github.com/tysonthomas9/loomcli/internal/harness/turns/harness/gemini"
	"github.com/tysonthomas9/loomcli/internal/harness/wrapper"
)

// Options configures a single Conversation.
type Options struct {
	// Harness names the per-harness adapter ("codex", "claude-code",
	// "generic"). Required.
	Harness string

	// BinaryPath is the harness executable. Required.
	BinaryPath string

	// Args are passed verbatim to the harness.
	Args []string

	// WorkingDir is the harness's working directory. Defaults to the
	// current process's CWD.
	WorkingDir string

	// Env is the harness's environment. Defaults to the current
	// process's environment.
	Env []string

	// Cols, Rows configure the virtual PTY size. Defaults: 120x40.
	Cols, Rows int

	// Store backs the chat metadata. Required; pass memstore.New() for
	// the in-process default (the memstore package is kept separate so
	// pkg/chat has no dependency on a specific persistence backend).
	Store Store

	// EventBuffer sizes the Conversation.Events() channel. Defaults to 32.
	EventBuffer int
}

// Conversation owns one supervised harness process and serves the
// chat-style API on top of it.
type Conversation struct {
	opts    Options
	store   Store
	adapter turns.Adapter

	sess    *wrapper.Session
	screen  *screen.Screen
	watcher *turns.Watcher

	releaseWriter func()

	queue *controlQueue

	session Session // chat-level Session record (also stored in Store)

	eventCh chan TurnEvent

	mu          sync.Mutex
	currentTurn *Turn // pending/streaming assistant turn, if any

	closeOnce sync.Once
	closed    chan struct{}
}

// Open starts a harness, wires the screen + turn watcher, and returns
// a live Conversation.
//
//nolint:funlen // Linear constructor (validate → spawn → wire screen → wire turns → persist); extraction fragments lifecycle without aiding comprehension. Mirrors upstream harness-wrapper.
func Open(ctx context.Context, opts Options) (*Conversation, error) {
	if opts.Harness == "" || opts.BinaryPath == "" {
		return nil, fmt.Errorf("%w: Harness and BinaryPath are required", ErrInvalidOptions)
	}
	if opts.Store == nil {
		return nil, fmt.Errorf("%w: Store is required (pass memstore.New() for the default)", ErrInvalidOptions)
	}
	if opts.Cols <= 0 {
		opts.Cols = 120
	}
	if opts.Rows <= 0 {
		opts.Rows = 40
	}
	if opts.EventBuffer <= 0 {
		opts.EventBuffer = 32
	}

	adapter, err := resolveAdapter(opts.Harness)
	if err != nil {
		return nil, err
	}

	scr := screen.New(opts.Cols, opts.Rows)

	sess, err := wrapper.Start(ctx, wrapper.Config{
		BinaryPath: opts.BinaryPath,
		Args:       opts.Args,
		WorkingDir: opts.WorkingDir,
		Env:        opts.Env,
		Stdin:      nil,
		Stdout:     scr,
		Harness:    opts.Harness,
	})
	if err != nil {
		return nil, fmt.Errorf("chat: wrapper start: %w", err)
	}

	releaseWriter, ok := sess.AcquireWriter()
	if !ok {
		// Should be impossible immediately after Start; treat as fatal.
		_ = sess.Stop(context.Background())
		return nil, fmt.Errorf("chat: failed to acquire wrapper writer lock")
	}

	// Match the PTY size to the virtual screen size so the harness's
	// re-renders target the same dimensions our emulator is tracking.
	//nolint:gosec // G115: opts.Cols/Rows are terminal dimensions; uint16 max (65535) far exceeds any realistic PTY size and the Resize API takes uint16.
	_ = sess.Resize(uint16(opts.Cols), uint16(opts.Rows))

	sessionRec := Session{
		ID:         newID(),
		Harness:    opts.Harness,
		WorkingDir: opts.WorkingDir,
		CreatedAt:  time.Now(),
	}
	if err := opts.Store.CreateSession(ctx, &sessionRec); err != nil {
		releaseWriter()
		_ = sess.Stop(context.Background())
		return nil, fmt.Errorf("chat: store CreateSession: %w", err)
	}

	c := &Conversation{
		opts:          opts,
		store:         opts.Store,
		adapter:       adapter,
		sess:          sess,
		screen:        scr,
		releaseWriter: releaseWriter,
		queue:         newControlQueue(),
		session:       sessionRec,
		eventCh:       make(chan TurnEvent, opts.EventBuffer),
		closed:        make(chan struct{}),
	}
	c.watcher = turns.Watch(sess, scr, adapter)

	go c.consumeWatcher()

	return c, nil
}

// SessionID returns the chat-level session ID. Distinct from the
// underlying harness's session ID (which is Session.HarnessSessionID
// and is empty in v1 until adapter-level extraction lands).
func (c *Conversation) SessionID() string { return c.session.ID }

// Events returns the channel of turn-state transitions. Closed after
// Close has completed and the watcher has drained.
func (c *Conversation) Events() <-chan TurnEvent { return c.eventCh }

// AcquireControl blocks until this caller is granted the exclusive
// control token. Holders are queued FIFO. The returned release function
// passes the token to the next waiter (or leaves it free); call it
// (typically with defer) when done sending messages.
func (c *Conversation) AcquireControl(ctx context.Context) (release func(), err error) {
	return c.queue.Acquire(ctx)
}

// Close terminates the harness process, releases the wrapper writer
// lock, stops the watcher, and closes the events channel. Safe to call
// multiple times.
func (c *Conversation) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.queue.Close()
		if c.releaseWriter != nil {
			c.releaseWriter()
		}
		if c.sess != nil {
			_ = c.sess.Stop(ctx)
		}
		if c.watcher != nil {
			_ = c.watcher.Close()
		}
	})
	return nil
}

// consumeWatcher pumps turns.Event from the watcher into Conversation
// state and emits TurnEvent on c.eventCh.
func (c *Conversation) consumeWatcher() {
	defer close(c.eventCh)
	for ev := range c.watcher.Events() {
		c.handleTurnsEvent(ev)
	}
}

// handleTurnsEvent translates a low-level turns.Event into a
// chat-level Turn state transition. If there is no current assistant
// turn (e.g. an adapter signal fired before the first Send), the event
// is ignored as a stale heuristic firing.
//
// On every TurnComplete the watcher loop also opportunistically tries
// to extract the harness's own session ID — most harnesses print it as
// part of their end-of-turn footer, so this is the natural moment to
// grab it.
func (c *Conversation) handleTurnsEvent(ev turns.Event) {
	if ev.Kind == turns.TurnComplete {
		c.maybeExtractSessionID()
	}

	c.mu.Lock()
	turn := c.currentTurn
	c.currentTurn = nil
	c.mu.Unlock()

	if turn == nil {
		return
	}

	switch ev.Kind {
	case turns.TurnComplete:
		turn.State = TurnStateComplete
		turn.CompletedAt = ev.At
		turn.Reason = ev.Reason
		if ev.Snap != nil {
			turn.Text = ev.Snap.Text
		}
	case turns.Blocked, turns.Errored:
		turn.State = TurnStateErrored
		turn.CompletedAt = ev.At
		turn.Reason = ev.Reason
		turn.HTTPCode = ev.HTTPCode
		turn.RetryAfter = ev.RetryAfter
	case turns.ToolCall:
		// ToolCall is informational mid-turn; restore the current-turn
		// pointer so the next event can complete the turn.
		c.mu.Lock()
		c.currentTurn = turn
		c.mu.Unlock()
		return
	default:
		// Unknown kind — leave turn as-is, restore pointer.
		c.mu.Lock()
		c.currentTurn = turn
		c.mu.Unlock()
		return
	}

	if err := c.store.UpdateTurn(context.Background(), turn); err != nil {
		c.emit(TurnEvent{Turn: *turn, Err: err})
		return
	}
	c.emit(TurnEvent{Turn: *turn})
}

// maybeExtractSessionID opportunistically scrapes the harness's own
// session ID from the rendered screen. Once we've persisted one, we
// don't probe again. No-op for adapters that don't implement
// turns.SessionIDExtractor.
func (c *Conversation) maybeExtractSessionID() {
	c.mu.Lock()
	if c.session.HarnessSessionID != "" {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	ext, ok := c.adapter.(turns.SessionIDExtractor)
	if !ok {
		return
	}
	id, ok := ext.ExtractSessionID(c.screen.Snapshot())
	if !ok {
		return
	}

	c.mu.Lock()
	c.session.HarnessSessionID = id
	updated := c.session
	c.mu.Unlock()
	_ = c.store.UpdateSession(context.Background(), &updated)
}

// History returns the conversation history for this Conversation.
//
// When the adapter supports turns.TranscriptReader and the harness
// session ID is known, History reads the harness's own JSONL log and
// returns its parsed contents — this is the higher-fidelity source
// because the harness records exactly what the model said, not what
// the TUI rendered.
//
// When transcript reading isn't possible (adapter has no reader, or
// the harness session ID has not yet been extracted), History falls
// back to the Store's recorded turns. The fallback only contains the
// user-side text and any screen-derived assistant text the watcher
// captured at TurnComplete.
func (c *Conversation) History(ctx context.Context) ([]Turn, error) {
	c.mu.Lock()
	sessionCopy := c.session
	c.mu.Unlock()

	reader, hasReader := c.adapter.(turns.TranscriptReader)
	if !hasReader || sessionCopy.HarnessSessionID == "" {
		return c.store.ListTurns(ctx, sessionCopy.ID)
	}

	tturns, err := reader.ReadTranscript(sessionCopy.HarnessSessionID, c.opts.WorkingDir)
	if err != nil {
		return nil, fmt.Errorf("chat: read transcript: %w", err)
	}
	out := make([]Turn, 0, len(tturns))
	for _, tt := range tturns {
		out = append(out, Turn{
			SessionID:   sessionCopy.ID,
			Role:        Role(tt.Role),
			State:       TurnStateComplete,
			Text:        tt.Text,
			StartedAt:   tt.Timestamp,
			CompletedAt: tt.Timestamp,
		})
	}
	return out, nil
}

// emit pushes an event onto the chan. Drops if the buffer is full
// rather than blocking the watcher pump.
func (c *Conversation) emit(ev TurnEvent) {
	select {
	case c.eventCh <- ev:
	case <-c.closed:
	default:
		// Buffer full — drop. Slow consumers lose events; this matches
		// the wrapper's own slow-consumer policy.
	}
}

// resolveAdapter maps Options.Harness to a concrete turns.Adapter.
func resolveAdapter(name string) (turns.Adapter, error) {
	switch name {
	case "codex":
		return codex.New(), nil
	case "claude-code":
		return claudecode.New(), nil
	case "gemini":
		return gemini.New(), nil
	case "generic", "":
		return generic.New(), nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownHarness, name)
	}
}
