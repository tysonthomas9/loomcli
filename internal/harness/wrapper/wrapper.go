// Package wrapper supervises an external CLI agent harness running under
// a pseudoterminal. It runs the harness, observes its output and
// lifecycle, and returns a normalized status when the harness exits or
// is terminated.
//
// Phase 1 started with only terminal states: idle, failed, interrupted,
// unknown. It now also recognizes a small set of actionable harness
// states from recent output. The wrapper does not persist state; callers
// own persistence.
//
// Concurrency: the package is safe for multiple concurrent Run calls
// only in headless mode (non-TTY stdin/stdout). Concurrent foreground
// Run calls produce undefined behavior because they would compete for
// terminal control.
package wrapper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/tysonthomas9/loomcli/internal/harness/wrapper/trace"
)

// Config configures a single Run.
//
// Fields with zero values get sensible defaults documented per-field.
// Construct Config using keyed struct literals; positional initialization
// is unsupported and will break across versions.
type Config struct {
	// BinaryPath is the absolute or PATH-resolvable path to the harness
	// executable. Required.
	BinaryPath string

	// Args are passed verbatim to the harness as arguments after the
	// binary name.
	Args []string

	// WorkingDir is the harness's working directory. Defaults to the
	// current process's working directory.
	WorkingDir string

	// Env is the harness's environment. If nil, the current process
	// environment is inherited.
	Env []string

	// Stdin is the source forwarded into the harness's pseudoterminal
	// input. If nil, no input is forwarded; the harness will block if it
	// tries to read stdin.
	//
	// Pass *os.File (e.g. os.Stdin) for foreground TTY mode — the wrapper
	// will detect that and put the terminal into raw mode with SIGWINCH
	// forwarding. Pass any other io.Reader (e.g. strings.NewReader) for
	// headless input; raw-mode setup is skipped.
	Stdin io.Reader

	// Stdout is the sink that receives the harness's PTY output bytes.
	// Required (must be non-nil). Pass os.Stdout for foreground use, or
	// any io.Writer (file, bytes.Buffer, io.Discard) for headless capture.
	//
	// The wrapper writes raw PTY bytes including ANSI escapes. Callers
	// wanting a cleaned transcript should wrap the writer themselves.
	//
	// When both Stdin and Stdout are *os.File and both are TTYs, the
	// wrapper enables raw-mode passthrough and SIGWINCH forwarding;
	// otherwise it stays in headless mode.
	Stdout io.Writer

	// IdleQuiet is the duration of no output after which the wrapper
	// considers the harness "quiet." Defaults to 15s.
	//
	// Phase 1: parsed but not yet enforced; idle classifier is added in
	// a later commit.
	IdleQuiet time.Duration

	// IdleClassify is the duration of no output after which the wrapper
	// classifies the run as idle. Must be >= IdleQuiet. Defaults to 60s.
	IdleClassify time.Duration

	// StaleThreshold is the duration of no PTY output after which the
	// wrapper emits a non-terminal StatusStale SessionEvent and a
	// harness_stale trace event. Distinct from IdleClassify: stale is a
	// mid-run advisory, not a basis for terminating the harness — the
	// run continues and a fresh StatusStale fires after each subsequent
	// quiet stretch.
	//
	// Must be >= IdleClassify when both are non-zero. Defaults to 5
	// minutes. Set to a negative value (e.g. -1) to disable; the wrapper
	// will then emit no StatusStale events regardless of quiet duration.
	StaleThreshold time.Duration

	// WaitDelay is how long to wait after sending SIGTERM before
	// escalating to SIGKILL on context cancellation. Defaults to 5s.
	WaitDelay time.Duration

	// Trace receives diagnostic events emitted by the wrapper. If nil,
	// events are discarded.
	//
	// Trace is for observability, not control flow. Callers should not
	// make decisions based on trace event ordering or presence; the
	// trace vocabulary is not part of the API stability surface.
	Trace trace.Emitter

	// Harness names a built-in per-harness classifier (e.g. "claude",
	// "codex"). If both Harness and Classifier are set, Classifier
	// wins. Unknown names fall through to the default classifier.
	Harness string

	// Classifier inspects recent harness output and produces actionable
	// status classifications (blocked_by_cost, retry_later,
	// waiting_for_input). If nil, a built-in classifier matching the
	// Harness field — or, failing that, a generic cost/quota
	// classifier — is used.
	Classifier Classifier
}

// Status is the normalized run status returned by the wrapper.
type Status string

const (
	// StatusIdle indicates the harness exited cleanly or its output
	// remained unchanged past the configured classification threshold
	// with no actionable state detected.
	StatusIdle Status = "idle"

	// StatusFailed indicates the harness exited with a non-zero code.
	StatusFailed Status = "failed"

	// StatusBlockedByCost indicates the harness cannot continue until
	// budget, credits, quota, or rate limits allow continuation.
	StatusBlockedByCost Status = "blocked_by_cost"

	// StatusRetryLater indicates the harness hit a transient condition
	// that the engine should re-attempt after a backoff. It is reported
	// by classifiers when they recognize transient API errors, network
	// blips, or "try again later" prompts.
	StatusRetryLater Status = "retry_later"

	// StatusAPIError indicates the harness's upstream model API returned
	// a recognized error (HTTP 4xx/5xx, transport failure). Unlike
	// StatusRetryLater this is non-terminal: the wrapper keeps the
	// harness alive. The accompanying SessionEvent carries HTTPCode
	// (0 when the harness's output did not include a numeric code,
	// e.g. transport errors) and RetryAfter (0 when no retry hint was
	// parseable). External clients subscribe to Session.Events and
	// dispatch on HTTPCode to attach per-error behavior.
	StatusAPIError Status = "api_error"

	// StatusWaitingForInput indicates the harness is paused at an
	// interactive prompt and needs a human (or attached client) to
	// answer. Unlike the other actionable statuses, it is reported
	// mid-run: the wrapper does not terminate the process.
	StatusWaitingForInput Status = "waiting_for_input"

	// StatusStale is a non-terminal mid-run advisory: the harness has
	// produced no PTY output for cfg.StaleThreshold and may need
	// attention, but it is still alive and has not been classified as
	// idle, blocked, or otherwise actionable. StatusStale never appears
	// in Result.Status (which is the terminal status reported by Wait);
	// it is only emitted on Session.Events() and as a harness_stale
	// trace event.
	StatusStale Status = "stale"

	// StatusInterrupted indicates the harness was terminated by signal,
	// either because the caller cancelled the context or because the
	// wrapper forwarded a foreground interrupt.
	StatusInterrupted Status = "interrupted"

	// StatusUnknown indicates the wrapper could not classify the run
	// outcome. Result.Reason should explain why.
	StatusUnknown Status = "unknown"
)

// Result describes the outcome of a Run.
type Result struct {
	// Status is the normalized outcome.
	Status Status

	// ExitCode is the harness process's exit code, or 128+signum if the
	// process was terminated by a signal. -1 if the process never
	// started.
	ExitCode int

	// Signal is the signal name (e.g. "terminated") if the process was
	// terminated by signal, empty otherwise.
	Signal string

	// Reason is a short human-readable description, populated for
	// Failed, Interrupted, and Unknown statuses. Not stable for parsing.
	Reason string

	// PID is the harness process ID while it was running, 0 if it never
	// started.
	PID int

	// StartedAt is when the harness process started.
	StartedAt time.Time

	// EndedAt is when the harness process exited or was terminated.
	EndedAt time.Time

	// LastOutputAt is the time of the most recent byte received from
	// the harness PTY. Zero if no output was observed.
	LastOutputAt time.Time
}

// Sentinel errors. Callers can use errors.Is to distinguish wrapper-level
// failures from harness-level outcomes. A non-nil err from Run always
// means the wrapper itself failed; harness outcomes are always returned
// via Result with err == nil.
var (
	ErrInvalidConfig  = errors.New("wrapper: invalid config")
	ErrBinaryNotFound = errors.New("wrapper: binary not found")
	ErrPTYAllocation  = errors.New("wrapper: pty allocation failed")
	ErrPTYRead        = errors.New("wrapper: pty read failed")
)

// Run starts the configured harness under a pseudoterminal, supervises
// it until it exits or ctx is cancelled, and returns the normalized
// outcome. It is a blocking convenience wrapper around Start+Wait
// preserved for callers that don't need a live session handle.
//
// Errors are returned only when the wrapper itself fails to do its job
// (invalid configuration, missing binary, PTY allocation failure, IO
// errors on the master fd). Harness-level outcomes — clean exit,
// non-zero exit, signal termination, idle classification — are always
// reported through the returned Result with a nil error.
//
// Context cancellation is handled by sending the harness a termination
// signal. The returned Result will have Status == StatusInterrupted;
// ctx.Err() is not propagated as the returned error.
func Run(ctx context.Context, cfg Config) (Result, error) {
	s, err := Start(ctx, cfg)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	return s.Wait()
}

// Start launches the configured harness under a pseudoterminal and
// returns a live Session. Unlike Run, Start returns immediately; the
// caller observes lifecycle through Session.Events / Session.Snapshot
// and retrieves the final outcome via Session.Wait.
//
// Errors are returned only when the wrapper itself fails to start
// (invalid configuration, missing binary, PTY allocation failure).
// Once Start has returned a non-nil Session, every harness outcome
// flows through Wait with a nil error.
func Start(ctx context.Context, cfg Config) (*Session, error) {
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)
	return startSession(ctx, cfg)
}

func validateConfig(cfg *Config) error {
	if cfg.BinaryPath == "" {
		return fmt.Errorf("%w: BinaryPath is required", ErrInvalidConfig)
	}
	if cfg.Stdout == nil {
		return fmt.Errorf("%w: Stdout is required", ErrInvalidConfig)
	}
	if cfg.IdleClassify > 0 && cfg.IdleQuiet > 0 && cfg.IdleClassify < cfg.IdleQuiet {
		return fmt.Errorf("%w: IdleClassify (%v) must be >= IdleQuiet (%v)", ErrInvalidConfig, cfg.IdleClassify, cfg.IdleQuiet)
	}
	if cfg.StaleThreshold > 0 && cfg.IdleClassify > 0 && cfg.StaleThreshold < cfg.IdleClassify {
		return fmt.Errorf("%w: StaleThreshold (%v) must be >= IdleClassify (%v)", ErrInvalidConfig, cfg.StaleThreshold, cfg.IdleClassify)
	}
	return nil
}

func applyDefaults(cfg *Config) {
	if cfg.IdleQuiet == 0 {
		cfg.IdleQuiet = 15 * time.Second
	}
	if cfg.IdleClassify == 0 {
		cfg.IdleClassify = 60 * time.Second
	}
	if cfg.StaleThreshold == 0 {
		cfg.StaleThreshold = 5 * time.Minute
	}
	if cfg.WaitDelay == 0 {
		cfg.WaitDelay = 5 * time.Second
	}
	if cfg.Trace == nil {
		cfg.Trace = trace.Discard
	}
}
