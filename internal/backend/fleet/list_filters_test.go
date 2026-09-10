package fleet

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestParseListDate(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    time.Time
		wantErr bool
	}{
		{"empty is not a filter", "", time.Time{}, false},
		{"rfc3339", "2026-03-04T05:06:07Z", time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC), false},
		{
			// Normalized to UTC so a row comparison never depends on the
			// offset the caller happened to write.
			"rfc3339 with offset normalizes to UTC",
			"2026-03-04T05:06:07+02:00",
			time.Date(2026, 3, 4, 3, 6, 7, 0, time.UTC),
			false,
		},
		{"date-only is midnight UTC", "2030-01-01", time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), false},
		{"garbage", "banana", time.Time{}, true},
		{"almost a date", "2030-13-45", time.Time{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseListDate("created_after", tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseListDate(%q): expected an error, got nil", tt.value)
				}
				var be *backend.BackendError
				if !errors.As(err, &be) || be.Kind != backend.KindValidation {
					t.Fatalf("parseListDate(%q): want KindValidation, got %#v", tt.value, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseListDate(%q): %v", tt.value, err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("parseListDate(%q) = %s, want %s", tt.value, got, tt.want)
			}
		})
	}
}

// TestParseListDate_MidnightUTCIsExplicit guards the timezone reading of a bare
// date against a later "fix" that quietly reads it as local time — which would
// shift every created-range boundary by the builder's offset.
func TestParseListDate_MidnightUTCIsExplicit(t *testing.T) {
	got, err := parseListDate("created_after", "2030-01-01")
	if err != nil {
		t.Fatalf("parseListDate: %v", err)
	}
	if zone, offset := got.Zone(); offset != 0 {
		t.Fatalf("bare date parsed in %s (offset %d), want UTC", zone, offset)
	}
	if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 {
		t.Errorf("bare date = %s, want midnight", got)
	}
}

func TestNormalizeFleetDate(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		// The reason this exists: fleet-db parses updated_* with a strict
		// time.Parse(time.RFC3339, v) and 400s on a bare date, which this layer
		// accepts.
		{"2030-01-01", "2030-01-01T00:00:00Z"},
		{"2026-03-04T05:06:07Z", "2026-03-04T05:06:07Z"},
		{"2026-03-04T05:06:07+02:00", "2026-03-04T03:06:07Z"},
		{"", ""},
		// Left for the server to reject rather than silently dropped.
		{"banana", "banana"},
	}
	for _, tt := range tests {
		if got := normalizeFleetDate(tt.in); got != tt.want {
			t.Errorf("normalizeFleetDate(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestContainsFold(t *testing.T) {
	tests := []struct {
		haystack, needle string
		want             bool
	}{
		{"Fix the Parser", "parser", true},
		{"Fix the Parser", "PARSER", true},
		{"Fix the Parser", "fix the", true},
		{"Fix the Parser", "zzz", false},
		{"", "x", false},
		{"anything", "", true},
	}
	for _, tt := range tests {
		if got := containsFold(tt.haystack, tt.needle); got != tt.want {
			t.Errorf("containsFold(%q, %q) = %v, want %v", tt.haystack, tt.needle, got, tt.want)
		}
	}
}

func TestListTextFilter(t *testing.T) {
	row := listRow{
		data:        backend.IssueData{Title: "Fix the Parser", Notes: "Blocked on UPSTREAM"},
		description: "  A long DESCRIPTION of the bug  ",
	}
	empty := listRow{data: backend.IssueData{Title: "No body"}, description: "   "}

	tests := []struct {
		name   string
		filter listTextFilter
		row    listRow
		want   bool
	}{
		{"description_contains matches case-insensitively", listTextFilter{DescriptionContains: "description"}, row, true},
		{"description_contains misses", listTextFilter{DescriptionContains: "zzzz"}, row, false},
		{"empty_description trims whitespace", listTextFilter{EmptyDescription: true}, empty, true},
		{"empty_description rejects a real body", listTextFilter{EmptyDescription: true}, row, false},
		{"q hits the title", listTextFilter{Query: "PARSER"}, row, true},
		{"q hits the description", listTextFilter{Query: "long desc"}, row, true},
		{"q hits the notes", listTextFilter{Query: "upstream"}, row, true},
		{"q misses all three", listTextFilter{Query: "zzzznomatch"}, row, false},
		{"no filter matches everything", listTextFilter{}, row, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filter.matches(tt.row); got != tt.want {
				t.Errorf("matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIssueDataMatchesList(t *testing.T) {
	yes, no := true, false
	created := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	issue := backend.IssueData{
		Title:     "Fix the Parser",
		Notes:     "Blocked on UPSTREAM",
		Status:    "open",
		Assignee:  "tyson",
		Labels:    []string{"alpha"},
		CreatedAt: created,
	}
	bare := backend.IssueData{Title: "Bare", Status: pinnedStatus, CreatedAt: created}

	tests := []struct {
		name   string
		filter issueDataFilter
		issue  backend.IssueData
		want   bool
	}{
		{"title_contains matches case-insensitively", issueDataFilter{TitleContains: "parser"}, issue, true},
		{"title_contains misses", issueDataFilter{TitleContains: "zzzznomatch"}, issue, false},
		{"notes_contains matches", issueDataFilter{NotesContains: "upstream"}, issue, true},
		{"notes_contains misses", issueDataFilter{NotesContains: "zzzz"}, issue, false},
		{"no_assignee excludes an assigned row", issueDataFilter{NoAssignee: true}, issue, false},
		{"no_assignee keeps an unassigned row", issueDataFilter{NoAssignee: true}, bare, true},
		{"no_labels excludes a labeled row", issueDataFilter{NoLabels: true}, issue, false},
		{"no_labels keeps an unlabeled row", issueDataFilter{NoLabels: true}, bare, true},

		// pinned is a STATUS in fleet-db, not a boolean column.
		{"pinned=true keeps a pinned row", issueDataFilter{Pinned: &yes}, bare, true},
		{"pinned=true excludes an open row", issueDataFilter{Pinned: &yes}, issue, false},
		{"pinned=false excludes a pinned row", issueDataFilter{Pinned: &no}, bare, false},
		{"pinned=false keeps an open row", issueDataFilter{Pinned: &no}, issue, true},

		// Exclusive bounds, mirroring fleet-db's own updated_after/before.
		{
			"created_after is exclusive of the boundary",
			issueDataFilter{CreatedAfter: created},
			issue, false,
		},
		{
			"created_before is exclusive of the boundary",
			issueDataFilter{CreatedBefore: created},
			issue, false,
		},
		{
			"created_after keeps a later row",
			issueDataFilter{CreatedAfter: created.Add(-time.Hour)},
			issue, true,
		},
		{
			"created_before keeps an earlier row",
			issueDataFilter{CreatedBefore: created.Add(time.Hour)},
			issue, true,
		},
		{
			"a future created_after excludes everything",
			issueDataFilter{CreatedAfter: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)},
			issue, false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := issueDataMatchesList(tt.issue, tt.filter); got != tt.want {
				t.Errorf("issueDataMatchesList() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCreatedRangeMatches_ZeroTimestampIsExcluded pins the edge case that would
// otherwise leak rows: IssueData.CreatedAt is a non-pointer time.Time, so a row
// whose created_at was absent from the wire is the zero time — which is before
// every conceivable created_before bound.
func TestCreatedRangeMatches_ZeroTimestampIsExcluded(t *testing.T) {
	zero := backend.IssueData{ID: "no-timestamp"}
	bound := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	if createdRangeMatches(zero, issueDataFilter{CreatedBefore: bound}) {
		t.Error("a row with no created_at passed a created_before bound")
	}
	if createdRangeMatches(zero, issueDataFilter{CreatedAfter: bound}) {
		t.Error("a row with no created_at passed a created_after bound")
	}
	// With no created range active it must not be filtered out at all.
	if !createdRangeMatches(zero, issueDataFilter{}) {
		t.Error("a row with no created_at was dropped with no created range set")
	}
}

func TestListRowsToData_AlwaysNonNil(t *testing.T) {
	// The handler marshals this straight to JSON and the frontend expects [].
	if got := listRowsToData(nil); got == nil {
		t.Fatal("listRowsToData(nil) = nil, want an empty slice")
	}
	if got := listRowsToData([]listRow{}); len(got) != 0 || got == nil {
		t.Fatalf("listRowsToData([]) = %#v, want an empty non-nil slice", got)
	}
}

// TestListRowsToData_DropsDescription is the payload-contract guard: the wire
// Description is carried only for the duration of the filter pass. Adding it to
// backend.IssueData would put multi-KB bodies into every kanban row.
func TestListRowsToData_DropsDescription(t *testing.T) {
	rows := []listRow{{data: backend.IssueData{ID: "a"}, description: "a very long body"}}
	got := listRowsToData(rows)
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("projection = %+v", got)
	}
	// backend.IssueData has no Description field at all; this asserts the
	// projected value is the slim shape and nothing was smuggled through Notes.
	if got[0].Notes != "" {
		t.Errorf("Notes = %q, want empty", got[0].Notes)
	}
}

func TestParseListDateFilters(t *testing.T) {
	got, err := parseListDateFilters(backend.ListOpts{
		CreatedAfter:  "2026-01-01",
		CreatedBefore: "2026-12-31T23:59:59Z",
	})
	if err != nil {
		t.Fatalf("parseListDateFilters: %v", err)
	}
	if !got.active() {
		t.Error("active() = false with both bounds set")
	}
	if !got.createdAfter.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("createdAfter = %s", got.createdAfter)
	}
	if !got.createdBefore.Equal(time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)) {
		t.Errorf("createdBefore = %s", got.createdBefore)
	}

	if _, err := parseListDateFilters(backend.ListOpts{CreatedAfter: "banana"}); err == nil {
		t.Error("expected an error for an unparseable created_after")
	}
	if _, err := parseListDateFilters(backend.ListOpts{CreatedBefore: "banana"}); err == nil {
		t.Error("expected an error for an unparseable created_before")
	}

	none, err := parseListDateFilters(backend.ListOpts{})
	if err != nil {
		t.Fatalf("parseListDateFilters(empty): %v", err)
	}
	if none.active() {
		t.Error("active() = true with no bounds set")
	}
}
