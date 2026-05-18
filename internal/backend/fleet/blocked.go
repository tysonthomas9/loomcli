package fleet

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func (b *FleetBackend) canonicalBlocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	path := "/issues/blocked"
	serverOpts := blockedServerOpts(opts)
	if q := blockedOptsToQuery(serverOpts); q != "" {
		path += "?" + q
	}
	resp, err := b.exec(ctx, "Blocked", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.IssueData{}, nil
	}
	issues, err := unmarshalBlockedIssueList(resp.Data, "Blocked")
	if err != nil {
		return nil, err
	}
	return filterBlockedIssues(issues, opts), nil
}
