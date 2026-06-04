// Package harness wires loomcli's backend invocations to the in-tree
// harness-wrapper supervisor. The RunWithRetry helper is the primary
// integration seam: it runs a single harness invocation under
// wrapper.Start, watches the event stream for RetryAfter hints, and
// transparently respawns the harness when the terminal status is
// StatusRetryLater or StatusAPIError.
//
// Callers that want raw wrapper behavior (no retry) should call
// wrapper.Run directly; RunWithRetry is the loomcli policy on top.
package harness

import (
	"context"
	"time"

	hwharness "github.com/olesho/harness-wrapper/pkg/harness"
	"github.com/olesho/harness-wrapper/pkg/wrapper"
	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
)

// RetryPolicy controls how RunWithRetry handles transient failures and how
// liveness is observed during a session. A zero-value RetryPolicy is replaced
// with DefaultRetryPolicy by RunWithRetry — except OnActivity, which has no
// default because it requires an external sink.
type RetryPolicy struct {
	// Max is the maximum number of retries after the first attempt.
	// Max=3 means up to 4 total attempts.
	Max int

	// BaseBackoff is the initial sleep between attempts when the
	// harness does not surface a RetryAfter hint. Doubles each
	// attempt up to MaxBackoff.
	BaseBackoff time.Duration

	// MaxBackoff caps both the exponential backoff and any
	// RetryAfter hint emitted by the harness.
	MaxBackoff time.Duration

	// OnActivity, when non-nil, is invoked from a background goroutine while
	// the wrapper.Session is alive. Each call carries the latest
	// wrapper.Snapshot (notably LastOutputAt) so the caller can forward
	// liveness to an external observer — e.g. a loom daemon IPC heartbeat.
	// The callback must be cheap and non-blocking; the wrapper drops calls
	// on a busy observer rather than queuing them.
	OnActivity func(wrapper.Snapshot)

	// ActivityInterval is the tick period for OnActivity. Zero means use the
	// harness default (harness.DefaultActivityInterval). Ignored when OnActivity
	// is nil.
	ActivityInterval time.Duration
}

// DefaultRetryPolicy is the policy applied when RunWithRetry receives
// a zero-value RetryPolicy.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		Max:         3,
		BaseBackoff: 2 * time.Second,
		MaxBackoff:  60 * time.Second,
	}
}

// runOnceFn is the seam tests use to swap in a fake harness without spawning a
// real subprocess. Production code uses runOnceDefault. The hwharness.Result it
// returns carries the terminal wrapper.Result (embedded) — including the
// canonical error Class, onto which the wrapper inherits a mid-run non-terminal
// classification (e.g. an API error before a Failed exit) — plus the RetryAfter
// hint.
type runOnceFn func(ctx context.Context, cfg hwharness.Config, p RetryPolicy) (hwharness.Result, error)

// sleepFn is the seam tests use to skip real sleeps. Production code
// uses sleepDefault.
type sleepFnT func(ctx context.Context, d time.Duration) error

var (
	runOnce runOnceFn = runOnceDefault
	sleepFn sleepFnT  = sleepDefault
)

// RunWithRetry runs cfg under wrapper supervision, automatically
// retrying when the terminal status is StatusRetryLater or
// StatusAPIError. Other statuses are returned to the caller unchanged.
//
// The returned Result is the final attempt's outcome. err is non-nil
// only for wrapper-level failures (PTY allocation, bad config) or
// ctx.Err() if ctx was cancelled during backoff.
func RunWithRetry(ctx context.Context, cfg hwharness.Config, p RetryPolicy) (hwharness.Result, error) {
	if p.Max == 0 && p.BaseBackoff == 0 && p.MaxBackoff == 0 {
		// Caller passed a zero-value RetryPolicy (modulo OnActivity, which
		// stays nil-or-set independent of defaults). Apply DefaultRetryPolicy
		// for the retry/backoff fields and preserve any activity callback.
		dp := DefaultRetryPolicy()
		dp.OnActivity = p.OnActivity
		dp.ActivityInterval = p.ActivityInterval
		p = dp
	}

	var lastResult hwharness.Result
	for attempt := 0; ; attempt++ {
		out, err := runOnce(ctx, cfg, p)
		if err != nil {
			return out, err
		}
		lastResult = out

		if !shouldRetry(out) {
			return lastResult, nil
		}
		if attempt >= p.Max {
			return lastResult, nil
		}

		delay := backoffFor(p, attempt, out.RetryAfter)
		if err := sleepFn(ctx, delay); err != nil {
			return lastResult, err
		}
	}
}

// shouldRetry reports whether the run should be retried. A retry fires when
// the terminal status is a known transient condition (StatusRetryLater,
// StatusAPIError) OR when the harness exited with a failure status but the
// carried error class is one the policy retries — e.g. a non-terminal API
// error inherited onto a Failed exit by the wrapper (common for `claude -p`
// and similar print-mode harnesses that don't recover internally).
//
// The class check replaces the out-of-band SawAPIError signal and is finer:
// a Failed exit carrying a fatal class (auth, billing) or a deterministic one
// (context overflow) is no longer pointlessly retried. Non-retryable terminal
// statuses (e.g. blocked_by_cost) stay definitive regardless of class — the
// caller, not the invocation layer, owns what happens to those.
func shouldRetry(res hwharness.Result) bool {
	switch res.Status {
	case wrapper.StatusRetryLater, wrapper.StatusAPIError:
		return true
	case wrapper.StatusFailed, wrapper.StatusUnknown:
		if res.Class == wrapper.ErrNone {
			return false
		}
		d := agentpolicy.Decide(agenterr.OutcomeFromHarness(res.Class))
		return d.Decision == agentpolicy.Retry || d.Decision == agentpolicy.RetryUncounted
	}
	return false
}

// backoffFor selects the sleep duration before the next attempt.
// A non-zero RetryAfter hint from the harness takes precedence over
// the exponential schedule, but both are capped at MaxBackoff.
func backoffFor(p RetryPolicy, attempt int, hint time.Duration) time.Duration {
	if hint > 0 {
		if p.MaxBackoff > 0 && hint > p.MaxBackoff {
			return p.MaxBackoff
		}
		return hint
	}
	d := p.BaseBackoff << attempt
	if p.MaxBackoff > 0 && (d > p.MaxBackoff || d < p.BaseBackoff) {
		return p.MaxBackoff
	}
	return d
}

// runOnceDefault is the production implementation of runOnceFn. It routes the
// invocation through harness.Run (the wrapper's orchestration layer), which
// composes wrapper.Start, drains the event stream for the RetryAfter hint +
// whether an API-error fired, and runs the OnActivity observer — so loom no
// longer hand-rolls that supervision. The transcript-acquisition fields
// (TranscriptMode/OnEvent/OnDisplayLine/HookCommand) are left zero here: this is
// the entrypoint switch only (TranscriptMode defaults to Off → no acquisition,
// no behavior change). The OnEvent/eventstore wiring lands behind flags next.
func runOnceDefault(ctx context.Context, cfg hwharness.Config, p RetryPolicy) (hwharness.Result, error) {
	// OnActivity stays a RetryPolicy/supervision concern; merge it into the
	// harness.Config the orchestrator consumes.
	cfg.OnActivity = p.OnActivity
	cfg.ActivityInterval = p.ActivityInterval
	return hwharness.Run(ctx, cfg)
}

// sleepDefault is the production implementation of sleepFnT. It
// honors context cancellation so a slow backoff doesn't strand the
// caller.
func sleepDefault(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
