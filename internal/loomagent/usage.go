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
	if e.Type != loomharness.EventUsage || e.Usage.CostTotalUSD == 0 {
		return e, nil
	}
	last, err := s.store.LastCostTotal(ctx, agentID, e.Session.NativeID)
	if err != nil {
		return e, err
	}
	if e.Usage.CostTotalUSD < last {
		last = 0
	}
	e.Usage.CostUSD = e.Usage.CostTotalUSD - last
	return e, nil
}
