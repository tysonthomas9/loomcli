package fleet

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// Blocked lists blocked issues from fleet-db's canonical /issues/blocked
// view, applying client-side filters the server doesn't support.
func (b *FleetBackend) Blocked(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	var unsupported []string
	if query.SortPolicy != "" {
		unsupported = append(unsupported, "SortPolicy")
	}
	if query.MolType != "" {
		unsupported = append(unsupported, "MolType")
	}
	if len(unsupported) > 0 {
		return nil, fmt.Errorf("fleet-db: unsupported blocked filters [%s]: %w",
			strings.Join(unsupported, ", "), backend.ErrFilterNotSupported)
	}
	path := "/issues/blocked"
	serverQuery := blockedServerQuery(query)
	if q := blockedQueryToQuery(serverQuery); q != "" {
		path += "?" + q
	}
	resp, err := b.exec(ctx, "Blocked", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []workitems.IssueSummary{}, nil
	}
	issues, err := unmarshalBlockedIssueList(resp.Data, "Blocked")
	if err != nil {
		return nil, err
	}
	return filterBlockedSummaries(issues, query), nil
}
