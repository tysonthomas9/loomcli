package fleet

// List and its paging loop. Split out of fleet.go when the loop was fixed to
// page while pages come back full (the loomcli chain's #717 amendment).

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// unmarshalIssueRowsPage is unmarshalIssueList for List's paging loop: it keeps
// the wire Description that the slim projection drops (List's client-side
// filters need it). It deliberately does not read has_more: fleet-db's issue
// lists never send it, so List decides from the page size instead.
func unmarshalIssueRowsPage(resp *apiResponse, op string) ([]listRow, error) {
	if !hasData(resp) {
		return []listRow{}, nil
	}
	wires, err := unmarshalListOrWrapper[fleetIssueWithCountsWire](resp.Data, op)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(wires))
	for _, w := range wires {
		rows = append(rows, listRow{data: w.toIssueData(), description: w.Description})
	}
	return rows, nil
}

// listPageSize is the page List asks fleet-db for: its per-page clamp. Asking
// for less would only add round-trips; asking for more is silently clamped.
const listPageSize = 200

// maxListPages bounds List's paging loop: at listPageSize rows a page this is
// 20k issues per call — far beyond any real workspace, while still making a
// server that keeps answering full pages terminate instead of spinning.
const maxListPages = 100

func (b *FleetBackend) List(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	if err := checkFleetUnsupportedFilters(opts); err != nil {
		return nil, err
	}
	if opts.Status == "all" {
		return b.listAllStatuses(ctx, opts)
	}
	return b.list(ctx, opts)
}

func (b *FleetBackend) list(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	// Parse the created-range bounds once, up front, so a malformed date is a
	// validation error (400) before any request goes out rather than a silent
	// no-op per row.
	dates, err := parseListDateFilters(opts)
	if err != nil {
		return nil, err
	}
	serverOpts := listServerOpts(opts)

	// Page through the result. fleet-db clamps a page to listPageSize rows and
	// its issue-list responses carry no has_more, so a FULL page is the only
	// signal that more rows exist: keep paging while pages come back full, and
	// stop on a short or empty one. Returning the first page silently is how
	// `loom data list --limit 500` came to answer with 200 rows and no
	// indication the other 327 existed — and how the client-side filters
	// (no_assignee, q, *_contains, created_*, which zero the server limit)
	// answered from the 50 newest rows only (PUPPET-576/603).
	want := serverOpts.Limit // 0 = everything the server has
	var out []listRow
	// Offset paging is not stable under concurrent writes (and fleet-db main
	// can repeat rows across pages until fleet-db#316), so drop a row already
	// seen instead of returning it twice.
	seen := make(map[string]struct{})
	for page := 0; page < maxListPages; page++ {
		pageOpts := serverOpts
		pageOpts.Limit = listPageSize
		if want > 0 && want-len(out) < listPageSize {
			pageOpts.Limit = want - len(out)
		}
		resp, err := b.exec(ctx, "List", "GET", "/issues?"+listOptsToQuery(pageOpts), nil)
		if err != nil {
			return nil, err
		}
		issues, err := unmarshalIssueRowsPage(resp, "List")
		if err != nil {
			return nil, err
		}
		for _, row := range issues {
			if _, dup := seen[row.data.ID]; dup {
				continue
			}
			seen[row.data.ID] = struct{}{}
			out = append(out, row)
		}
		if len(issues) < pageOpts.Limit || (want > 0 && len(out) >= want) {
			break
		}
		serverOpts.Offset += len(issues)
	}
	if want > 0 && len(out) > want {
		out = out[:want]
	}
	// Project last: listRowsToData drops the wire Description again, so the
	// lightweight list payload the kanban board receives is unchanged, and it
	// always returns a non-nil slice for the empty case.
	return listRowsToData(filterListIssues(out, opts, dates)), nil
}

// FleetDB's issue list uses an omitted status for active work and an explicit
// "closed" status for completed work. The web UI exposes the more useful
// aggregate "all" contract, so resolve it at this adapter boundary instead of
// leaking a FleetDB-specific multi-request into every caller.
func (b *FleetBackend) listAllStatuses(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	queryOpts := opts
	queryOpts.Status = ""
	// Preserve the caller's limit on each FleetDB request. Omitting it does not
	// mean unlimited: FleetDB falls back to 50, which can hide a newly closed
	// issue in a long-lived workspace before the two result sets are merged.
	// The merged result is still capped to the caller's limit below.
	active, err := b.list(ctx, queryOpts)
	if err != nil {
		return nil, err
	}

	queryOpts.Status = "closed"
	closed, err := b.list(ctx, queryOpts)
	if err != nil {
		return nil, err
	}

	merged := make([]backend.IssueData, 0, len(active)+len(closed))
	seen := make(map[string]struct{}, len(active)+len(closed))
	for _, issue := range append(active, closed...) {
		if _, exists := seen[issue.ID]; exists {
			continue
		}
		seen[issue.ID] = struct{}{}
		merged = append(merged, issue)
	}

	// Each b.list pass already applied the client-side filters, including the
	// wire-Description ones only available there; re-apply the description-free
	// subset and the caller's limit across the merged result.
	return filterIssueData(merged, issueDataFilter{
		Assignee:    opts.Assignee,
		Type:        opts.IssueType,
		ParentID:    opts.ParentID,
		Labels:      opts.Labels,
		SourceRepos: opts.SourceRepos,
		Limit:       opts.Limit,
	}), nil
}
