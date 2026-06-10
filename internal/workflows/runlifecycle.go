// Package workflows is the control-plane domain for Loom's dynamic
// workflow runner: run lifecycle management, Flue event capture, and
// the epic-runner reconciler. It depends only on the platform store
// and execplane interfaces so the same code serves the local daemon
// today and a cloud loom server later.
package workflows

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/workflows/platform"
)

// defaultHeartbeatInterval renews run ownership well inside fleet-db's
// 5-minute stale-run recovery window.
const defaultHeartbeatInterval = 15 * time.Second

// RunLifecycle owns one claimed DriverRun: it holds the owner triple
// (node, lease, fencing token), renews the heartbeat in the
// background, and performs the terminal Finish transition. Reused by
// every workflow kind (epic runner now, trigger router in Phase 2).
type RunLifecycle struct {
	store  platform.Store
	ws     string
	nodeID string
	lease  string
	fence  int64
	run    *platform.DriverRun
	logger *slog.Logger

	stopOnce sync.Once
	stopHB   chan struct{}
	hbDone   chan struct{}
}

// ClaimRun claims a queued run for nodeID and starts the heartbeat
// goroutine. Returns domain.ErrConflict (wrapped) when the run is
// already claimed or not queued.
func ClaimRun(ctx context.Context, store platform.Store, ws, runID, nodeID string, logger *slog.Logger) (*RunLifecycle, error) {
	return claimRunWithInterval(ctx, store, ws, runID, nodeID, logger, defaultHeartbeatInterval)
}

func claimRunWithInterval(ctx context.Context, store platform.Store, ws, runID, nodeID string, logger *slog.Logger, hbInterval time.Duration) (*RunLifecycle, error) {
	if logger == nil {
		logger = slog.Default()
	}
	leaseID := fmt.Sprintf("%s-%d", nodeID, time.Now().UnixNano())
	run, err := store.DriverRuns().Claim(ctx, ws, runID, nodeID, leaseID)
	if err != nil {
		return nil, fmt.Errorf("claim run %s: %w", runID, err)
	}
	l := &RunLifecycle{
		store:  store,
		ws:     ws,
		nodeID: nodeID,
		lease:  leaseID,
		fence:  run.FencingToken,
		run:    run,
		logger: logger,
		stopHB: make(chan struct{}),
		hbDone: make(chan struct{}),
	}
	go l.heartbeatLoop(hbInterval)
	return l, nil
}

// Run returns the claimed run record (as of claim time).
func (l *RunLifecycle) Run() *platform.DriverRun { return l.run }

// heartbeatLoop renews ownership until Finish/Stop. A failed heartbeat
// is logged but not fatal: if ownership was truly lost the Finish call
// will be rejected by the fencing check, and fleet-db's stale recovery
// handles a dead owner.
func (l *RunLifecycle) heartbeatLoop(interval time.Duration) {
	defer close(l.hbDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stopHB:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err := l.store.DriverRuns().Heartbeat(ctx, l.ws, l.run.RunID, l.nodeID, l.lease, l.fence)
			cancel()
			if err != nil {
				l.logger.Warn("driver run heartbeat failed", "run_id", l.run.RunID, "err", err)
			}
		}
	}
}

// Stop halts the heartbeat without finishing the run (used when
// abandoning a run whose ownership was lost).
func (l *RunLifecycle) Stop() {
	l.stopOnce.Do(func() { close(l.stopHB) })
	<-l.hbDone
}

// Finish stops the heartbeat and performs the terminal transition.
func (l *RunLifecycle) Finish(ctx context.Context, in platform.DriverRunFinish) (*platform.DriverRun, error) {
	l.Stop()
	run, err := l.store.DriverRuns().Finish(ctx, l.ws, l.run.RunID, l.nodeID, l.lease, l.fence, in)
	if err != nil {
		return nil, fmt.Errorf("finish run %s: %w", l.run.RunID, err)
	}
	return run, nil
}
