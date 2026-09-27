package fleet

import (
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// This file holds the List-only client-side predicates. fleet-db's list route
// evaluates status, type, assignee, label, repo, parent_id, priority and the
// updated_* range server-side; the ten filters below have no server equivalent
// (verified against fleet-db's storage.IssueFilter and parseListOptions), so
// List evaluates them on the rows it pages back.
//
// It is deliberately separate from deferred.go: that file is the shared
// Ready/Blocked/Deferred predicate engine, and none of these apply there.

// listRow pairs the slim backend.IssueData projection with the wire
// Description.
//
// backend.IssueData carries no Description on purpose — the webui list handler
// sets Lightweight so multi-KB bodies stay out of kanban rows — but
// description_contains, empty_description and q have to read it. Carrying it
// alongside for the duration of the filter pass, and projecting it away before
// List returns, keeps the payload contract unchanged.
type listRow struct {
	data        backend.IssueData
	description string
}

// pinnedStatus is how "pinned" is expressed in fleet-db: a status value, not a
// boolean column. types.StatusPinned is the same string; this package does not
// import types for a single constant.
const pinnedStatus = "pinned"

// listDateFilters holds the created-range bounds after parsing. Parsing happens
// once per List call rather than per row.
type listDateFilters struct {
	createdAfter  time.Time
	createdBefore time.Time
}

func (f listDateFilters) active() bool {
	return !f.createdAfter.IsZero() || !f.createdBefore.IsZero()
}

// parseListDateFilters parses ListOpts' created-range strings. Both RFC3339 and
// bare YYYY-MM-DD are accepted, matching handler.ParseDateParams, so the CLI and
// the HTTP layer agree on what a valid date is. Anything else is a validation
// error (a 400 downstream, not a 500).
func parseListDateFilters(opts backend.ListOpts) (listDateFilters, error) {
	var f listDateFilters
	var err error
	if f.createdAfter, err = parseListDate("created_after", opts.CreatedAfter); err != nil {
		return f, err
	}
	if f.createdBefore, err = parseListDate("created_before", opts.CreatedBefore); err != nil {
		return f, err
	}
	return f, nil
}

// parseListDate accepts RFC3339 or a bare YYYY-MM-DD date, which it reads as
// midnight UTC. An empty value is not a filter and yields the zero time.
func parseListDate(field, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, backend.ErrValidation("List",
		field+": expected RFC3339 or YYYY-MM-DD, got "+value)
}

// normalizeFleetDate re-emits a date in RFC3339 for the filters that are
// forwarded to fleet-db. fleet-db's parseListOptions uses a strict
// time.Parse(time.RFC3339, v) and 400s on anything else, so forwarding the bare
// "2030-01-01" that ParseDateParams accepts would turn a working query into an
// error. An unparseable value is passed through untouched and left for the
// server to reject — List validates the created-range bounds itself, and the
// updated_* pair reaches here only after the same handler validation.
func normalizeFleetDate(value string) string {
	t, err := parseListDate("", value)
	if err != nil || t.IsZero() {
		return value
	}
	return t.Format(time.RFC3339)
}

// containsFold reports whether needle occurs in haystack, case-insensitively.
// ASCII-oriented: strings.ToLower does not do full Unicode case folding, which
// is deliberate — matching fleet-db's own lowercasing beats pulling in
// golang.org/x/text for a substring filter.
func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// listTextFilter is the description-dependent half of the List predicates: the
// three that cannot be answered from backend.IssueData alone.
type listTextFilter struct {
	Query               string
	DescriptionContains string
	EmptyDescription    bool
}

func (f listTextFilter) needsFilter() bool {
	return f.Query != "" || f.DescriptionContains != "" || f.EmptyDescription
}

func (f listTextFilter) matches(row listRow) bool {
	if f.DescriptionContains != "" && !containsFold(row.description, f.DescriptionContains) {
		return false
	}
	if f.EmptyDescription && strings.TrimSpace(row.description) != "" {
		return false
	}
	// q is a case-insensitive substring across title, description and notes.
	// fleet-db has a real token index at GET /issues/search?q=, but that route
	// accepts no other filter, so routing q there would stop it composing with
	// status/labels/... — which is exactly what the kanban UI sends.
	if f.Query != "" &&
		!containsFold(row.data.Title, f.Query) &&
		!containsFold(row.description, f.Query) &&
		!containsFold(row.data.Notes, f.Query) {
		return false
	}
	return true
}

// listRowsToData projects filtered rows back to the slim shape List returns.
// Always non-nil: the handler marshals the result and the frontend expects [].
func listRowsToData(rows []listRow) []backend.IssueData {
	out := make([]backend.IssueData, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.data)
	}
	return out
}

// dataToListRows wraps plain IssueData as rows with no description. Used by the
// Ready/Blocked/Deferred paths, which share the predicate engine but never set
// the three description-dependent filters.
func dataToListRows(issues []backend.IssueData) []listRow {
	rows := make([]listRow, 0, len(issues))
	for _, d := range issues {
		rows = append(rows, listRow{data: d})
	}
	return rows
}
