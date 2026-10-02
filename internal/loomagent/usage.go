package loomagent

import (
	"context"
	"encoding/json"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// withCost sets the cost of a usage event that reports its session's running
// total (Claude) to the rise over the session's last saved total, read from
// the saved rows, so a resumed process or a serve restart counts nothing
// twice. A total below the last one starts again from zero.
func (s *Service) withCost(ctx context.Context, agentID string, e loomharness.Event) (loomharness.Event, error) {
	if e.Type != loomharness.EventUsage || e.Usage.CostTotalUSD == 0 {
		return e, nil
	}
	last := 0.0
	q := loomstore.EventQuery{AgentID: agentID, Kinds: []string{string(loomharness.EventUsage)}}
	for {
		p, err := s.store.ListEvents(ctx, q)
		if err != nil {
			return e, err
		}
		for _, r := range p.Events {
			var u struct {
				Session string  `json:"session"`
				Total   float64 `json:"costTotalUsd"`
			}
			if json.Unmarshal(r.Payload, &u) == nil && u.Session == e.Session.NativeID && u.Total > 0 {
				last = u.Total
			}
		}
		if !p.More {
			break
		}
		q.After, q.Snapshot = p.Next, p.SnapshotSeq
	}
	if e.Usage.CostTotalUSD < last {
		last = 0
	}
	e.Usage.CostUSD = e.Usage.CostTotalUSD - last
	return e, nil
}
