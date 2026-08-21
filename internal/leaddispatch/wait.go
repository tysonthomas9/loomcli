package leaddispatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	defaultWaitInterval       = 2 * time.Second
	defaultMaxTransientErrors = 10
	defaultQueuedWarnAfter    = 2 * time.Minute
)

type StatusFunc func(ctx context.Context) (RunStatus, error)

type WaitOptions struct {
	Interval         time.Duration
	Out              io.Writer
	MaxTransientErrs int
	QueuedWarnAfter  time.Duration
	sleep            func(context.Context, time.Duration) error
}

// Wait polls immediately and then at Interval until serve reports terminal.
func Wait(ctx context.Context, runID string, status StatusFunc, opts WaitOptions) (RunStatus, error) {
	opts = normalizeWaitOptions(opts)
	started := time.Now()
	lastStatus := ""
	transientErrors := 0
	warned := false
	for {
		if err := ctx.Err(); err != nil {
			return RunStatus{}, interruptedWaitError(runID, err)
		}
		current, err := status(ctx)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return RunStatus{}, interruptedWaitError(runID, ctxErr)
			}
			transientErrors++
			if !retryableWaitError(err) || transientErrors > opts.MaxTransientErrs {
				return RunStatus{}, fmt.Errorf("poll epic workflow run %s: %w", runID, err)
			}
		} else {
			transientErrors = 0
			if current.Status != lastStatus {
				_, _ = fmt.Fprintf(opts.Out, "[epic-run] run %s status: %s\n", runID, current.Status)
				lastStatus = current.Status
			}
			if current.Terminal {
				return current, nil
			}
			warned = writeQueuedWarning(opts, started, runID, current, warned)
		}
		if err := opts.sleep(ctx, opts.Interval); err != nil {
			return RunStatus{}, interruptedWaitError(runID, err)
		}
	}
}

func normalizeWaitOptions(opts WaitOptions) WaitOptions {
	if opts.Interval <= 0 {
		opts.Interval = defaultWaitInterval
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.MaxTransientErrs <= 0 {
		opts.MaxTransientErrs = defaultMaxTransientErrors
	}
	if opts.QueuedWarnAfter == 0 {
		opts.QueuedWarnAfter = defaultQueuedWarnAfter
	}
	if opts.sleep == nil {
		opts.sleep = sleepContext
	}
	return opts
}

func retryableWaitError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable()
	}
	return true
}

func writeQueuedWarning(opts WaitOptions, started time.Time, runID string,
	status RunStatus, warned bool,
) bool {
	if warned || opts.QueuedWarnAfter < 0 || status.Status != "queued" || time.Since(started) < opts.QueuedWarnAfter {
		return warned
	}
	_, _ = fmt.Fprintf(opts.Out, "[epic-run] run %s still queued after %s: serve's workflow executor may be disabled\n",
		runID, opts.QueuedWarnAfter)
	return true
}

func interruptedWaitError(runID string, err error) error {
	return fmt.Errorf("epic workflow run %s watch interrupted; the run continues on the server: %w", runID, err)
}

func sleepContext(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
