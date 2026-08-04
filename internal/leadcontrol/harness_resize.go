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
	state := harnessResizeState{
		ctx:         ctx,
		currentSize: currentSize,
		conv:        conv,
		logger:      logger,
	}
	defer state.cancelRetry()

	// Subscribe before this initial sample so a resize between conversation
	// startup and listener registration is either observed here or queued.
	if !state.handleAttempt(state.attempt(), true) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-state.retry:
			// Consume the one pending retry before attempting it. A failed retry
			// waits for another SIGWINCH rather than creating a log/timer loop.
			state.consumeRetry()
			if !state.handleAttempt(state.attempt(), false) {
				return
			}
		case _, ok := <-signals:
			if !ok || !drainHarnessResizeSignals(ctx, signals) {
				return
			}
			if !state.handleAttempt(state.attempt(), true) {
				return
			}
		}
	}
}

type harnessResizeState struct {
	ctx         context.Context
	currentSize func() (uint16, uint16, bool)
	conv        harnessConversation
	logger      *slog.Logger
	lastCols    uint16
	lastRows    uint16
	retryTimer  *time.Timer
	retry       <-chan time.Time
}

func (s *harnessResizeState) attempt() harnessResizeAttempt {
	cols, rows, ok := s.currentSize()
	if !ok {
		return harnessResizeRetry
	}
	if cols == s.lastCols && rows == s.lastRows {
		return harnessResizeSucceeded
	}
	if err := s.conv.Resize(cols, rows); err != nil {
		if errors.Is(err, wrapper.ErrSessionTerminated) || errors.Is(err, chat.ErrClosed) {
			return harnessResizeStop
		}
		if s.ctx.Err() == nil {
			s.logger.Debug("failed to resize harness terminal", "cols", cols, "rows", rows, "err", err)
		}
		return harnessResizeRetry
	}
	s.lastCols, s.lastRows = cols, rows
	return harnessResizeSucceeded
}

func (s *harnessResizeState) handleAttempt(attempt harnessResizeAttempt, allowRetry bool) bool {
	switch attempt {
	case harnessResizeStop:
		return false
	case harnessResizeSucceeded:
		s.cancelRetry()
	case harnessResizeRetry:
		if allowRetry {
			s.scheduleRetry()
		}
	}
	return true
}

func (s *harnessResizeState) scheduleRetry() {
	if s.retryTimer != nil {
		return
	}
	s.retryTimer = time.NewTimer(harnessResizeRetryDelay)
	s.retry = s.retryTimer.C
}

func (s *harnessResizeState) consumeRetry() {
	s.retryTimer = nil
	s.retry = nil
}

func (s *harnessResizeState) cancelRetry() {
	if s.retryTimer == nil {
		return
	}
	if !s.retryTimer.Stop() {
		select {
		case <-s.retryTimer.C:
		default:
		}
	}
	s.consumeRetry()
}

func drainHarnessResizeSignals(ctx context.Context, signals <-chan os.Signal) bool {
	for {
		select {
		case _, ok := <-signals:
			if !ok {
				return false
			}
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
}
