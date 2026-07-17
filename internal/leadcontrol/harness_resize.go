package leadcontrol

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/olesho/harness-wrapper/pkg/chat"
	"github.com/olesho/harness-wrapper/pkg/wrapper"
)

const harnessResizeRetryDelay = 25 * time.Millisecond

type harnessResizeAttempt uint8

const (
	harnessResizeSucceeded harnessResizeAttempt = iota
	harnessResizeRetry
	harnessResizeStop
)

type harnessResizeSignalSubscription struct {
	signals <-chan os.Signal
	stop    func()
}

// shouldForwardHarnessResize preserves the Claude-only rollout boundary. Other
// harness adapters keep their launch-time size until their TUIs are validated.
func shouldForwardHarnessResize(harnessName string) bool {
	return harnessName == HarnessNameClaudeCode
}

func startHarnessResizeForwarder(ctx context.Context, cfg HarnessLeadRuntimeConfig, conv harnessConversation) func() {
	stdout, ok := cfg.Stdout.(*os.File)
	if !ok {
		return func() {}
	}
	if _, ok := harnessTerminalFileDescriptor(stdout); !ok {
		return func() {}
	}
	subscription := subscribeHarnessResizeSignals()

	resizeCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		forwardHarnessResizeEvents(
			resizeCtx,
			subscription.signals,
			func() (uint16, uint16, bool) { return currentHarnessTerminalSize(cfg.Stdout) },
			conv,
			cfg.Logger,
		)
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			subscription.stop()
			<-done
		})
	}
}

func forwardHarnessResizeEvents(
	ctx context.Context,
	signals <-chan os.Signal,
	currentSize func() (uint16, uint16, bool),
	conv harnessConversation,
	logger *slog.Logger,
) {
	var lastCols, lastRows uint16
	resize := func() harnessResizeAttempt {
		cols, rows, ok := currentSize()
		if !ok {
			return harnessResizeRetry
		}
		if cols == lastCols && rows == lastRows {
			return harnessResizeSucceeded
		}
		if err := conv.Resize(cols, rows); err != nil {
			if errors.Is(err, wrapper.ErrSessionTerminated) || errors.Is(err, chat.ErrClosed) {
				return harnessResizeStop
			}
			if ctx.Err() == nil {
				logger.Debug("failed to resize harness terminal", "cols", cols, "rows", rows, "err", err)
			}
			return harnessResizeRetry
		}
		lastCols, lastRows = cols, rows
		return harnessResizeSucceeded
	}

	var retryTimer *time.Timer
	var retry <-chan time.Time
	cancelRetry := func() {
		if retryTimer == nil {
			return
		}
		if !retryTimer.Stop() {
			select {
			case <-retryTimer.C:
			default:
			}
		}
		retryTimer = nil
		retry = nil
	}
	scheduleRetry := func() {
		if retryTimer != nil {
			return
		}
		retryTimer = time.NewTimer(harnessResizeRetryDelay)
		retry = retryTimer.C
	}
	defer cancelRetry()

	// Subscribe before this initial sample so a resize between conversation
	// startup and listener registration is either observed here or queued.
	switch resize() {
	case harnessResizeStop:
		return
	case harnessResizeRetry:
		scheduleRetry()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-retry:
			// Consume the one pending retry before attempting it. A failed retry
			// waits for another SIGWINCH rather than creating a log/timer loop.
			retryTimer = nil
			retry = nil
			if resize() == harnessResizeStop {
				return
			}
		case _, ok := <-signals:
			if !ok {
				return
			}
		drain:
			for {
				select {
				case _, ok := <-signals:
					if !ok {
						return
					}
				case <-ctx.Done():
					return
				default:
					break drain
				}
			}
			switch resize() {
			case harnessResizeStop:
				return
			case harnessResizeSucceeded:
				cancelRetry()
			case harnessResizeRetry:
				scheduleRetry()
			}
		}
	}
}
