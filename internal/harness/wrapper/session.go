package wrapper

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/tysonthomas9/loomcli/internal/harness/wrapper/trace"
)

// Snapshot is the most recent state observation for a Session. Snapshot
// is safe to read concurrently with the session running; it always
// reflects a coherent point-in-time view.
type Snapshot struct {
	// Status is the wrapper's current classification. Mid-run, it may
	// be empty (the session is producing output and has not been
	// classified) or one of the actionable mid-run statuses
	// (waiting_for_input). After Wait returns, Status is the terminal
	// status from Result.
	Status Status

	// Reason mirrors the Reason field on the most recent classification.
	Reason string

	// LastOutputAt is the time of the most recent byte received from
	// the harness PTY. Zero if no output has been observed yet.
	LastOutputAt time.Time
}

// SessionEvent is a state transition observed by a Session. Events are
// delivered on Session.Events() in order. Mid-run classifications
// (waiting_for_input, blocked_by_cost, retry_later, api_error) flow as
// Status events. The final event is always Terminated, after which the
// channel is closed.
type SessionEvent struct {
	At         time.Time
	Status     Status
	Reason     string
	Terminated bool

	// HTTPCode is the upstream API status code when Status is
	// StatusAPIError and the harness surfaced one. Zero otherwise.
	HTTPCode int

	// RetryAfter is the wait duration the harness suggested in its
	// error message. Zero when no hint was parseable.
	RetryAfter time.Duration
}

// Session is a live handle to a supervised harness process. Construct
// one with Start; retrieve the terminal outcome with Wait. Stop
// requests a graceful shutdown without forcing the caller to track
// context cancellation. Concurrent calls to Wait, Stop, Snapshot, and
// Events are safe.
type Session struct {
	cfg       Config
	cmd       *exec.Cmd
	ptmx      *os.File
	pid       int
	startedAt time.Time

	classifier   Classifier
	lastOutput   *atomic.Int64
	recentOutput *recentOutputBuffer
	termState    *terminalState

	events       chan SessionEvent
	stopOnce     sync.Once
	stopRequest  chan struct{}
	classifierCh chan classification
	classifierOn chan struct{}

	doneCh chan struct{}

	fanout  *outputFanout
	stdinMu sync.Mutex

	writerMu   sync.Mutex
	writerHeld bool

	mu       sync.Mutex
	snap     Snapshot
	result   Result
	finalErr error
}

// classification is the internal mid-run handoff between the classifier
// goroutine and the supervisor.
type classification struct {
	status     Status
	reason     string
	terminal   bool
	httpCode   int
	retryAfter time.Duration
}

// Wait blocks until the Session terminates and returns the final
// Result. Calling Wait more than once is safe; every call returns the
// same value. Errors are returned only when the wrapper itself failed
// during supervision (PTY IO, classifier panic). Harness-level
// outcomes are reported via Result.Status with err == nil.
func (s *Session) Wait() (Result, error) {
	<-s.doneCh
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result, s.finalErr
}

// Stop requests a graceful shutdown. The wrapper sends SIGTERM and
// escalates to SIGKILL after Config.WaitDelay if the process has not
// exited. Stop returns when the session has fully terminated (Wait
// would not block) or when ctx is cancelled. The session's final
// status will be Interrupted unless the harness happened to exit on
// its own before the signal arrived.
//
// Stop is idempotent. The first call wins; subsequent calls block on
// termination just like Wait.
func (s *Session) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() { close(s.stopRequest) })
	select {
	case <-s.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Snapshot returns a coherent point-in-time view of the session's
// state. It never blocks.
func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := s.snap
	if last := s.lastOutput.Load(); last > 0 {
		snap.LastOutputAt = time.Unix(0, last)
	}
	return snap
}

// Events returns the channel of state transitions for this Session.
// The channel is closed after the terminal event has been delivered.
// Slow consumers have events dropped on the floor; events are
// observability, not control flow.
func (s *Session) Events() <-chan SessionEvent { return s.events }

// PID returns the harness process ID, or 0 if the session never
// successfully started.
func (s *Session) PID() int { return s.pid }

// RecentOutput returns a snapshot of the last ~64KB of harness PTY
// output, ANSI escapes intact. This is the same buffer the built-in
// classifier inspects on each poll. Safe to call concurrently with the
// session running; the snapshot reflects bytes observed up to the call
// time and may grow on subsequent calls.
//
// Useful for callers that want to run their own post-hoc classification
// (e.g. matching harness-specific error fingerprints) over the same
// bytes the wrapper saw, without maintaining a parallel ring buffer.
func (s *Session) RecentOutput() string { return s.recentOutput.String() }

// startSession is the constructor used by Start. cfg is assumed to
// have been validated and defaulted.
//
//nolint:funlen // Linear setup: emit trace, build cmd, wire pty, launch supervisor goroutine. Mirrors upstream harness-wrapper.
func startSession(ctx context.Context, cfg Config) (*Session, error) {
	cfg.Trace.Emit(trace.Event{
		At:   time.Now(),
		Kind: "wrapper_started",
		Fields: map[string]any{
			"binary_path":   cfg.BinaryPath,
			"args":          cfg.Args,
			"working_dir":   cfg.WorkingDir,
			"idle_quiet":    cfg.IdleQuiet.String(),
			"idle_classify": cfg.IdleClassify.String(),
			"wait_delay":    cfg.WaitDelay.String(),
		},
	})

	cmd := exec.CommandContext(ctx, cfg.BinaryPath, cfg.Args...) //nolint:gosec // G204: launching the configured harness binary IS the wrapper's documented purpose.
	cmd.Dir = cfg.WorkingDir
	if cfg.Env != nil {
		cmd.Env = cfg.Env
	}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = cfg.WaitDelay

	startedAt := time.Now()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		if isBinaryNotFound(err) {
			return nil, fmt.Errorf("%w: %v", ErrBinaryNotFound, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrPTYAllocation, err)
	}

	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	cfg.Trace.Emit(trace.Event{
		At:     time.Now(),
		Kind:   "pty_opened",
		Fields: map[string]any{"pid": pid},
	})

	termState := setupTerminalIfTTY(cfg.Stdin, cfg.Stdout, ptmx, cfg.Trace)

	s := &Session{
		cfg:          cfg,
		cmd:          cmd,
		ptmx:         ptmx,
		pid:          pid,
		startedAt:    startedAt,
		classifier:   resolveClassifier(cfg),
		lastOutput:   &atomic.Int64{},
		recentOutput: newRecentOutput(64 * 1024),
		termState:    termState,
		events:       make(chan SessionEvent, 16),
		stopRequest:  make(chan struct{}),
		classifierCh: make(chan classification, 1),
		classifierOn: make(chan struct{}),
		doneCh:       make(chan struct{}),
		fanout:       newOutputFanout(cfg.Stdout),
	}

	go s.supervise(ctx)
	return s, nil
}

// supervise owns the session's lifecycle. It runs the IO copy
// goroutines, dispatches the classifier, waits for the harness to
// exit (or for Stop / classification / context cancel to force
// termination), assembles the final Result, and closes the Events
// channel.
//
//nolint:funlen // The supervise loop is a single coherent state machine. Splitting it fragments the lifecycle without aiding comprehension. Mirrors upstream harness-wrapper.
func (s *Session) supervise(ctx context.Context) {
	defer close(s.doneCh)
	defer close(s.events)
	defer s.termState.cleanup()
	defer s.fanout.closeAll()

	go runSessionClassifier(ctx, s)

	var outWG sync.WaitGroup
	outWG.Add(1)
	go func() {
		defer outWG.Done()
		copyPTYOutput(s.ptmx, s.fanout, s.lastOutput, s.recentOutput)
	}()

	var stdinDone chan struct{}
	if s.cfg.Stdin != nil {
		stdinDone = make(chan struct{})
		go func() {
			defer close(stdinDone)
			_, _ = io.Copy(s.ptmx, s.cfg.Stdin)
			// PTYs don't propagate the underlying io.Reader's EOF to the
			// slave automatically. For headless callers (Stdin is not
			// an os.File TTY), send EOT (Ctrl+D, 0x04) twice: the first
			// submits any pending unterminated line to the harness, the
			// second is interpreted by the PTY's canonical-mode line
			// discipline as end-of-file (at start of line, ^D returns
			// 0 bytes from read()). Skip when Stdin is a real TTY so
			// interactive sessions where the user keeps typing aren't
			// corrupted.
			if _, isTTYFile := s.cfg.Stdin.(*os.File); !isTTYFile {
				_, _ = s.ptmx.Write([]byte{0x04, 0x04})
			}
		}()
	}

	waitCh := make(chan waitResult, 1)
	go func() {
		waitCh <- waitResult{err: s.cmd.Wait(), endedAt: time.Now()}
	}()

	var (
		waitErr           error
		endedAt           time.Time
		terminalClassDone *classification
		stopRequested     bool
	)

waitLoop:
	for {
		select {
		case wr := <-waitCh:
			waitErr = wr.err
			endedAt = wr.endedAt
			break waitLoop
		case c := <-s.classifierCh:
			if !c.terminal {
				s.recordStatusChange(c, false)
				continue
			}
			terminalClassDone = &c
			s.recordStatusChange(c, false)
			endedAt, waitErr = terminateAndWait(s.cmd, waitCh, s.cfg.WaitDelay)
			break waitLoop
		case <-s.stopRequest:
			stopRequested = true
			endedAt, waitErr = terminateAndWait(s.cmd, waitCh, s.cfg.WaitDelay)
			break waitLoop
		}
	}

	close(s.classifierOn)
	_ = s.ptmx.Close()
	outWG.Wait()

	s.cfg.Trace.Emit(trace.Event{
		At:     time.Now(),
		Kind:   "pty_closed",
		Fields: map[string]any{"pid": s.pid},
	})
	if stdinDone != nil {
		select {
		case <-stdinDone:
		default:
		}
	}

	res := Result{
		PID:       s.pid,
		StartedAt: s.startedAt,
		EndedAt:   endedAt,
	}
	if last := s.lastOutput.Load(); last > 0 {
		res.LastOutputAt = time.Unix(0, last)
	}

	res.Status, res.ExitCode, res.Signal, res.Reason = classifyExit(s.cmd.ProcessState, waitErr, ctx.Err(), s.recentOutput.String())
	if terminalClassDone != nil {
		res.Status = terminalClassDone.status
		res.Reason = terminalClassDone.reason
	}
	if stopRequested && terminalClassDone == nil {
		res.Status = StatusInterrupted
		if res.Reason == "" {
			res.Reason = "stop requested"
		}
	}

	s.cfg.Trace.Emit(trace.Event{
		At:   time.Now(),
		Kind: "harness_exited",
		Fields: map[string]any{
			"status":      string(res.Status),
			"exit_code":   res.ExitCode,
			"signal":      res.Signal,
			"reason":      res.Reason,
			"pid":         res.PID,
			"started_at":  res.StartedAt,
			"ended_at":    res.EndedAt,
			"duration_ms": res.EndedAt.Sub(res.StartedAt).Milliseconds(),
		},
	})

	s.mu.Lock()
	s.result = res
	s.snap.Status = res.Status
	s.snap.Reason = res.Reason
	s.mu.Unlock()

	final := SessionEvent{
		At:         time.Now(),
		Status:     res.Status,
		Reason:     res.Reason,
		Terminated: true,
	}
	if terminalClassDone != nil {
		final.HTTPCode = terminalClassDone.httpCode
		final.RetryAfter = terminalClassDone.retryAfter
	}
	s.emitEvent(final)
}

// recordStatusChange updates Snapshot and emits a non-terminal event.
// It de-duplicates identical consecutive classifications so the
// classifier can poll freely without flooding subscribers.
func (s *Session) recordStatusChange(c classification, terminated bool) {
	s.mu.Lock()
	if s.snap.Status == c.status && s.snap.Reason == c.reason {
		s.mu.Unlock()
		return
	}
	s.snap.Status = c.status
	s.snap.Reason = c.reason
	s.mu.Unlock()
	s.emitEvent(SessionEvent{
		At:         time.Now(),
		Status:     c.status,
		Reason:     c.reason,
		Terminated: terminated,
		HTTPCode:   c.httpCode,
		RetryAfter: c.retryAfter,
	})
}

// emitEvent delivers e to subscribers, dropping it if the channel
// buffer is full so a slow consumer cannot stall the supervisor.
func (s *Session) emitEvent(e SessionEvent) {
	select {
	case s.events <- e:
	default:
	}
}

// terminateAndWait sends SIGTERM, waits up to waitDelay for the harness
// to exit, then escalates to SIGKILL.
func terminateAndWait(cmd *exec.Cmd, waitCh <-chan waitResult, waitDelay time.Duration) (time.Time, error) {
	if cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case wr := <-waitCh:
		return wr.endedAt, wr.err
	case <-time.After(waitDelay):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		wr := <-waitCh
		return wr.endedAt, wr.err
	}
}

// runSessionClassifier polls the configured Classifier on a fixed
// cadence, building a ClassifierInput from the live activity counters
// and forwarding non-empty Classifications to the supervisor. It also
// emits the original output_quiet / output_classify_threshold trace
// events for parity with the Phase 1 idle classifier.
//
//nolint:gocognit,cyclop,funlen // Classifier dispatch fans out across several signal sources (quiet, classify-threshold, terminal status); complexity and length reflect the inputs, not poor structure. Mirrors upstream harness-wrapper.
func runSessionClassifier(ctx context.Context, s *Session) {
	cfg := s.cfg
	tick := max(cfg.IdleQuiet/3, 100*time.Millisecond)
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	var (
		lastSeen        int64 = -1
		quietEmitted    bool
		classifyEmitted bool
		staleEmitted    bool
		dispatched      bool
	)
	staleEnabled := cfg.StaleThreshold > 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.classifierOn:
			return
		case <-ticker.C:
			last := s.lastOutput.Load()
			if last == 0 {
				continue
			}
			outputChanged := last != lastSeen
			if outputChanged {
				lastSeen = last
				quietEmitted = false
				classifyEmitted = false
				staleEmitted = false
				// Fall through so high-confidence classifiers
				// (api_error) can fire even while output is still
				// streaming. Cost/Retry/Prompt are gated on
				// Quiet/Idle below, which won't be true here, so
				// they stay silent until the output settles.
			}
			sinceLast := time.Since(time.Unix(0, last))
			quiet := !outputChanged && sinceLast >= cfg.IdleQuiet
			idle := !outputChanged && sinceLast >= cfg.IdleClassify
			stale := !outputChanged && staleEnabled && sinceLast >= cfg.StaleThreshold

			if quiet && !quietEmitted {
				cfg.Trace.Emit(trace.Event{
					At:   time.Now(),
					Kind: "output_quiet",
					Fields: map[string]any{
						"since_last_output_ms": sinceLast.Milliseconds(),
						"threshold_ms":         cfg.IdleQuiet.Milliseconds(),
					},
				})
				quietEmitted = true
			}
			if idle && !classifyEmitted {
				cfg.Trace.Emit(trace.Event{
					At:   time.Now(),
					Kind: "output_classify_threshold",
					Fields: map[string]any{
						"since_last_output_ms": sinceLast.Milliseconds(),
						"threshold_ms":         cfg.IdleClassify.Milliseconds(),
					},
				})
				classifyEmitted = true
			}
			if stale && !staleEmitted {
				cfg.Trace.Emit(trace.Event{
					At:   time.Now(),
					Kind: "harness_stale",
					Fields: map[string]any{
						"since_last_output_ms": sinceLast.Milliseconds(),
						"threshold_ms":         cfg.StaleThreshold.Milliseconds(),
					},
				})
				s.recordStatusChange(classification{
					status:   StatusStale,
					reason:   fmt.Sprintf("no output for %s", sinceLast.Round(time.Second)),
					terminal: false,
				}, false)
				staleEmitted = true
			}

			if dispatched {
				continue
			}

			classification := s.classifier.Classify(ClassifierInput{
				RecentOutput:    s.recentOutput.String(),
				SinceLastOutput: sinceLast,
				Quiet:           quiet,
				Idle:            idle,
			})
			if classification.Status == "" {
				continue
			}

			emitClassifierTrace(cfg, classification)
			select {
			case s.classifierCh <- toInternalClassification(classification):
				if classification.Terminal {
					dispatched = true
				}
			default:
			}
		}
	}
}

func toInternalClassification(c Classification) classification {
	return classification{
		status:     c.Status,
		reason:     c.Reason,
		terminal:   c.Terminal,
		httpCode:   c.HTTPCode,
		retryAfter: c.RetryAfter,
	}
}

func emitClassifierTrace(cfg Config, c Classification) {
	kind := "harness_classified"
	switch c.Status {
	case StatusBlockedByCost:
		kind = "harness_blocked_by_cost"
	case StatusRetryLater:
		kind = "harness_retry_later"
	case StatusWaitingForInput:
		kind = "harness_waiting_for_input"
	case StatusAPIError:
		kind = "harness_api_error"
	}
	fields := map[string]any{
		"status":   string(c.Status),
		"reason":   c.Reason,
		"terminal": c.Terminal,
	}
	if c.HTTPCode != 0 {
		fields["http_code"] = c.HTTPCode
	}
	if c.RetryAfter > 0 {
		fields["retry_after_ms"] = c.RetryAfter.Milliseconds()
	}
	cfg.Trace.Emit(trace.Event{
		At:     time.Now(),
		Kind:   kind,
		Fields: fields,
	})
}
