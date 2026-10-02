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
	t := time.NewTicker(idleTick)
	defer t.Stop()
	loomagent.RunIdle(ctx, t.C, func() []*loomagent.Service {
		a.mu.Lock()
		defer a.mu.Unlock()
		return slices.Collect(maps.Values(a.services))
	})
}
