package agentwire

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
)

// idleTick is how often the lifecycle timer checks for idle agents.
var idleTick = time.Minute

// runIdle runs the one lifecycle timer over every workspace's service, since
// they share the OpenCode server (design v2 §4.15).
func (a *API) runIdle(ctx context.Context) {
	tick, stop := a.ticker(idleTick)
	defer stop()
	loomagent.RunIdle(ctx, tick, func() []*loomagent.Service {
		a.mu.Lock()
		defer a.mu.Unlock()
		return slices.Collect(maps.Values(a.services))
	})
}

// retentionTick is how often the R29 retention sweep runs; it also runs once
// at start, so a purge that failed before a restart is retried.
var retentionTick = 24 * time.Hour

// retentionEnv shortens R29 retention, and the sweep interval with it, for
// AFT's own serve only; the product never sets it.
const retentionEnv = "LOOM_AGENT_HISTORY_RETENTION"

// runRetention runs every workspace's RetentionSweep at start and then on
// each retentionTick.
func (a *API) runRetention(ctx context.Context) {
	tick, stop := a.ticker(retentionTick)
	defer stop()
	for {
		a.mu.Lock()
		services := slices.Collect(maps.Values(a.services))
		a.mu.Unlock()
		for _, s := range services {
			s.RetentionSweep(ctx, a.now())
		}
		select {
		case <-ctx.Done():
			return
		case <-tick:
		}
	}
}

// realTicker is time.NewTicker.
func realTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}
