package loomagent

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// withCost sets the cost of a usage event that reports its session's running
// total (Claude) to the rise over the session's last saved total, read from
// the saved rows, so a resumed process or a serve restart counts nothing
// twice. A total below the last one starts again from zero.
func (s *Service) withCost(ctx context.Context, agentID string, e loomharness.Event) (loomharness.Event, error) {
	if !hasCostTotal(e) {
		return e, nil
	}
	last, err := s.store.LastCostTotal(ctx, agentID, e.Session.NativeID)
	if err != nil {
		return e, err
	}
	return costOver(e, last), nil
}

// hasCostTotal reports whether e is a usage event with its session's running
// total.
func hasCostTotal(e loomharness.Event) bool {
	return e.Type == loomharness.EventUsage && e.Usage.CostTotalUSD != 0
}

// costOver sets the cost of e, a usage event with a running total, to its
// rise over last, or to the whole total when it is below last.
func costOver(e loomharness.Event, last float64) loomharness.Event {
	if e.Usage.CostTotalUSD < last {
		last = 0
	}
	e.Usage.CostUSD = e.Usage.CostTotalUSD - last
	return e
}
