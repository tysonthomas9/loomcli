package fleet

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// Blocked lists blocked issues from fleet-db's canonical /issues/blocked
// view, applying client-side filters the server doesn't support. A task
// that may start behind code review is ready, not blocked.
func (b *FleetBackend) Blocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	blocked, err := b.blockedFromFleet(ctx, opts)
	if err != nil {
		return nil, err
	}
	return b.withoutTasksBehindReview(ctx, blocked), nil
}

func (b *FleetBackend) blockedFromFleet(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
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
