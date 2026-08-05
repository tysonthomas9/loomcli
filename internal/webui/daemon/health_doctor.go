package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
)

// HealthDoctor monitors workspace daemon circuit breakers and resets breakers
// that are stuck in an unhealthy state.
type HealthDoctor struct {
	multiPool *MultiPool
	wsPathsFn func() (map[string]string, error) // wsID → filesystem path
	logger    *slog.Logger

	// How often to check breaker states.
	checkInterval time.Duration

	// How long a breaker must be continuously unhealthy before triggering a restart.
	stuckThreshold time.Duration

	// Per-workspace tracking.
	mu      sync.Mutex
	watches map[string]*breakerWatch
}

// breakerWatch tracks the health timeline for a single workspace's circuit breaker.
type breakerWatch struct {
	unhealthySince time.Time // when first observed open/half-open (zero = healthy)
	lastRestart    time.Time // when we last triggered a restart
	restartCount   int       // consecutive restart attempts without recovery
}

// HealthDoctorConfig configures the health doctor.
type HealthDoctorConfig struct {
	CheckInterval  time.Duration
	StuckThreshold time.Duration
}

// DefaultHealthDoctorConfig returns sensible defaults.
func DefaultHealthDoctorConfig() HealthDoctorConfig {
	return HealthDoctorConfig{
		CheckInterval:  15 * time.Second,
		StuckThreshold: 90 * time.Second, // 3 × OpenTimeout (30s)
	}
}

// NewHealthDoctor creates a health doctor that monitors all workspace pools.
func NewHealthDoctor(
	multiPool *MultiPool,
	wsPathsFn func() (map[string]string, error),
	logger *slog.Logger,
	cfg HealthDoctorConfig,
) *HealthDoctor {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.CheckInterval <= 0 {
		cfg.CheckInterval = 15 * time.Second
	}
	if cfg.StuckThreshold <= 0 {
		cfg.StuckThreshold = 90 * time.Second
	}
	return &HealthDoctor{
		multiPool:      multiPool,
		wsPathsFn:      wsPathsFn,
		logger:         logger,
		checkInterval:  cfg.CheckInterval,
		stuckThreshold: cfg.StuckThreshold,
		watches:        make(map[string]*breakerWatch),
	}
}

// Run starts the health doctor loop. Blocks until ctx is canceled.
func (hd *HealthDoctor) Run(ctx context.Context) {
	hd.logger.Info("health doctor started",
		"component", "health_doctor",
		"check_interval", hd.checkInterval,
		"stuck_threshold", hd.stuckThreshold,
	)

	ticker := time.NewTicker(hd.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			hd.logger.Info("health doctor stopped", "component", "health_doctor")
			return
		case <-ticker.C:
			hd.checkAllWorkspaces(ctx)
		}
	}
}

// checkAllWorkspaces iterates over all registered workspace pools and checks
// their circuit breaker health.
func (hd *HealthDoctor) checkAllWorkspaces(ctx context.Context) {
	wsIDs := hd.multiPool.WorkspaceIDs()

	for _, wsID := range wsIDs {
		if ctx.Err() != nil {
			return
		}

		pool := hd.multiPool.PoolForWorkspace(wsID)
		if pool == nil {
			continue
		}

		pp, ok := pool.(*ProtectedPool)
		if !ok {
			continue // not a protected pool, skip
		}

		hd.checkWorkspace(ctx, wsID, pp)
	}
}

// checkWorkspace checks a single workspace's breaker and triggers restart if stuck.
func (hd *HealthDoctor) checkWorkspace(ctx context.Context, wsID string, pp *ProtectedPool) {
	state := pp.BreakerState()

	hd.mu.Lock()
	w, exists := hd.watches[wsID]
	if !exists {
		w = &breakerWatch{}
		hd.watches[wsID] = w
	}
	hd.mu.Unlock()

	switch state {
	case circuitbreaker.StateClosed:
		hd.markHealthy(wsID, w)
	case circuitbreaker.StateOpen, circuitbreaker.StateHalfOpen:
		hd.handleUnhealthy(ctx, wsID, pp, w, state)
	}
}

// markHealthy clears tracking for a workspace whose breaker has closed.
func (hd *HealthDoctor) markHealthy(wsID string, w *breakerWatch) {
	if w.unhealthySince.IsZero() {
		return
	}
	hd.logger.Info("circuit breaker recovered",
		"component", "health_doctor",
		"workspace", wsID,
		"was_unhealthy_for", time.Since(w.unhealthySince).Round(time.Second),
	)
	w.unhealthySince = time.Time{}
	w.restartCount = 0
}

// handleUnhealthy processes an open/half-open breaker, resetting it if stuck past threshold.
func (hd *HealthDoctor) handleUnhealthy(ctx context.Context, wsID string, pp *ProtectedPool, w *breakerWatch, state circuitbreaker.State) {
	now := time.Now()

	if w.unhealthySince.IsZero() {
		w.unhealthySince = now
		hd.logger.Warn("circuit breaker unhealthy",
			"component", "health_doctor",
			"workspace", wsID,
			"state", state,
		)
		return
	}

	stuckDuration := now.Sub(w.unhealthySince)
	if stuckDuration < hd.stuckThreshold {
		return
	}

	backoff := hd.backoffDuration(w.restartCount)
	if !w.lastRestart.IsZero() && now.Sub(w.lastRestart) < backoff {
		return
	}

	hd.attemptRestart(ctx, wsID, pp, w, state, stuckDuration, now)
}

// attemptRestart runs a restart cycle, updating watch state and logging outcomes.
func (hd *HealthDoctor) attemptRestart(ctx context.Context, wsID string, pp *ProtectedPool, w *breakerWatch, state circuitbreaker.State, stuckDuration time.Duration, now time.Time) {
	hd.logger.Warn("circuit breaker stuck, attempting recovery",
		"component", "health_doctor",
		"workspace", wsID,
		"state", state,
		"stuck_for", stuckDuration.Round(time.Second),
		"restart_attempt", w.restartCount+1,
	)

	if err := hd.recoverDaemon(ctx, wsID); err != nil {
		hd.logger.Error("daemon recovery failed",
			"component", "health_doctor",
			"workspace", wsID,
			"err", err,
			"restart_attempt", w.restartCount+1,
		)
		w.lastRestart = now
		w.restartCount++
		return
	}

	pp.ResetBreaker()
	w.lastRestart = now
	w.restartCount++
	w.unhealthySince = time.Time{}

	hd.logger.Info("daemon breaker reset",
		"component", "health_doctor",
		"workspace", wsID,
		"restart_attempt", w.restartCount,
	)
}

// backoffDuration returns the backoff for the Nth restart attempt.
// Formula: min(30s × 2^n, 10min).
func (hd *HealthDoctor) backoffDuration(restartCount int) time.Duration {
	base := 30 * time.Second
	d := base
	for i := 0; i < restartCount; i++ {
		d *= 2
		if d > 10*time.Minute {
			return 10 * time.Minute
		}
	}
	return d
}

// recoverDaemon validates that the workspace still exists before allowing the
// breaker to probe the daemon again. FleetDB-backed Loom no longer shells out
// to a per-workspace issue daemon from WebUI health recovery.
func (hd *HealthDoctor) recoverDaemon(_ context.Context, wsID string) error {
	paths, err := hd.wsPathsFn()
	if err != nil {
		return fmt.Errorf("resolve workspace paths: %w", err)
	}
	if _, ok := paths[wsID]; !ok {
		return fmt.Errorf("workspace %s not found in store", wsID)
	}
	return nil
}
