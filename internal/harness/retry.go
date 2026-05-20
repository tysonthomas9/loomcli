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

	"github.com/tysonthomas9/loomcli/internal/harness/wrapper"
)

// RetryPolicy controls how RunWithRetry handles transient failures.
// A zero-value RetryPolicy is replaced with DefaultRetryPolicy by
// RunWithRetry.
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

// attemptResult bundles a single supervised run's terminal Result with
// the most recent RetryAfter hint observed during the run.
//
// SawAPIError captures whether the wrapper emitted any
// StatusAPIError event during the run. The classifier marks
// StatusAPIError as non-terminal because some harnesses recover on
// their own; when the harness instead exits with a failure after
// surfacing the API error (typical of `claude -p`), the terminal
// status is StatusFailed and the API-error signal must be carried
// out-of-band so the retry layer can act on it.
type attemptResult struct {
	Result      wrapper.Result
	RetryAfter  time.Duration
	SawAPIError bool
}

// runOnceFn is the seam tests use to swap in a fake harness without
// spawning a real subprocess. Production code uses runOnceDefault.
type runOnceFn func(ctx context.Context, cfg wrapper.Config) (attemptResult, error)

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
func RunWithRetry(ctx context.Context, cfg wrapper.Config, p RetryPolicy) (wrapper.Result, error) {
	if p == (RetryPolicy{}) {
		p = DefaultRetryPolicy()
	}

	var lastResult wrapper.Result
	for attempt := 0; ; attempt++ {
		out, err := runOnce(ctx, cfg)
		if err != nil {
			return out.Result, err
		}
		lastResult = out.Result

		if !shouldRetry(out.Result.Status, out.SawAPIError) {
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

// shouldRetry reports whether the run should be retried. A retry
// fires when the terminal status is a known transient condition
// (StatusRetryLater, StatusAPIError) OR when the wrapper observed an
// API-error event mid-run and the harness then exited with a
// failure status (common for `claude -p` and similar print-mode
// harnesses that don't recover internally).
func shouldRetry(s wrapper.Status, sawAPIError bool) bool {
	switch s {
	case wrapper.StatusRetryLater, wrapper.StatusAPIError:
		return true
	case wrapper.StatusFailed, wrapper.StatusUnknown:
		return sawAPIError
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

// runOnceDefault is the production implementation of runOnceFn. It
// starts a wrapper session, drains its event stream to capture the
// latest RetryAfter hint and to remember whether an API-error event
// fired mid-run, then waits for the terminal result.
func runOnceDefault(ctx context.Context, cfg wrapper.Config) (attemptResult, error) {
	sess, err := wrapper.Start(ctx, cfg)
	if err != nil {
		return attemptResult{Result: wrapper.Result{ExitCode: -1}}, err
	}

	var lastRetryAfter time.Duration
	var sawAPIError bool
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for ev := range sess.Events() {
			if ev.RetryAfter > 0 {
				lastRetryAfter = ev.RetryAfter
			}
			if ev.Status == wrapper.StatusAPIError {
				sawAPIError = true
			}
		}
	}()

	res, err := sess.Wait()
	<-drained
	return attemptResult{Result: res, RetryAfter: lastRetryAfter, SawAPIError: sawAPIError}, err
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
