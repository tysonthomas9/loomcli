package fleet

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// Blocked lists blocked issues from fleet-db's canonical /issues/blocked
// view, applying client-side filters the server doesn't support.
func (b *FleetBackend) Blocked(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	if err := checkProjectedAvailabilityQuerySupported("blocked", query); err != nil {
		return nil, err
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
	return filterAvailabilitySummaries(issues, query), nil
}
