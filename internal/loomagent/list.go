package loomagent

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// List returns one page of agents matching f and the next page's cursor. It
// reads only the registry and never calls a harness (design v2 §4.5).
func (s *Service) List(ctx context.Context, f loomstore.AgentFilter) ([]AgentInfo, string, error) {
	rows, next, err := s.store.ListAgents(ctx, f)
	if err != nil {
		return nil, "", err
	}
	out := make([]AgentInfo, len(rows))
	for i, a := range rows {
		out[i] = info(a)
	}
	return out, next, nil
}
