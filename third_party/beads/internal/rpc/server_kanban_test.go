package rpc

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestBuildIssueFilter_InvalidCreatedAfter verifies that an unparseable
// CreatedAfter string causes buildIssueFilter to return an error mentioning
// the "--created-after" flag.
func TestBuildIssueFilter_InvalidCreatedAfter(t *testing.T) {
	args := &ListArgs{CreatedAfter: "not-a-date"}
	_, err := buildIssueFilter(args)
	if err == nil {
		t.Fatalf("expected error for invalid CreatedAfter, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --created-after date") {
		t.Errorf("expected error to contain %q, got %q",
			"invalid --created-after date", err.Error())
	}
}

// TestBuildIssueFilter_InvalidDeferBefore verifies that an unparseable
// DeferBefore string causes buildIssueFilter to return an error mentioning
// the "--defer-before" flag.
func TestBuildIssueFilter_InvalidDeferBefore(t *testing.T) {
	args := &ListArgs{DeferBefore: "xyz"}
	_, err := buildIssueFilter(args)
	if err == nil {
		t.Fatalf("expected error for invalid DeferBefore, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --defer-before date") {
		t.Errorf("expected error to contain %q, got %q",
			"invalid --defer-before date", err.Error())
	}
}

// TestBuildIssueFilter_AllDateFieldsReportCorrectly verifies that each of
// the 10 date fields reports its own flag name in the error message when
// given an unparseable value (with all other fields empty).
func TestBuildIssueFilter_AllDateFieldsReportCorrectly(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(a *ListArgs)
		wantErrSubs string
	}{
		{
			name:        "CreatedAfter",
			mutate:      func(a *ListArgs) { a.CreatedAfter = "not-a-date" },
			wantErrSubs: "invalid --created-after date",
		},
		{
			name:        "CreatedBefore",
			mutate:      func(a *ListArgs) { a.CreatedBefore = "not-a-date" },
			wantErrSubs: "invalid --created-before date",
		},
		{
			name:        "UpdatedAfter",
			mutate:      func(a *ListArgs) { a.UpdatedAfter = "not-a-date" },
			wantErrSubs: "invalid --updated-after date",
		},
		{
			name:        "UpdatedBefore",
			mutate:      func(a *ListArgs) { a.UpdatedBefore = "not-a-date" },
			wantErrSubs: "invalid --updated-before date",
		},
		{
			name:        "ClosedAfter",
			mutate:      func(a *ListArgs) { a.ClosedAfter = "not-a-date" },
			wantErrSubs: "invalid --closed-after date",
		},
		{
			name:        "ClosedBefore",
			mutate:      func(a *ListArgs) { a.ClosedBefore = "not-a-date" },
			wantErrSubs: "invalid --closed-before date",
		},
		{
			name:        "DeferAfter",
			mutate:      func(a *ListArgs) { a.DeferAfter = "not-a-date" },
			wantErrSubs: "invalid --defer-after date",
		},
		{
			name:        "DeferBefore",
			mutate:      func(a *ListArgs) { a.DeferBefore = "not-a-date" },
			wantErrSubs: "invalid --defer-before date",
		},
		{
			name:        "DueAfter",
			mutate:      func(a *ListArgs) { a.DueAfter = "not-a-date" },
			wantErrSubs: "invalid --due-after date",
		},
		{
			name:        "DueBefore",
			mutate:      func(a *ListArgs) { a.DueBefore = "not-a-date" },
			wantErrSubs: "invalid --due-before date",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			args := &ListArgs{}
			tc.mutate(args)
			_, err := buildIssueFilter(args)
			if err == nil {
				t.Fatalf("expected error for invalid %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubs) {
				t.Errorf("expected error to contain %q, got %q",
					tc.wantErrSubs, err.Error())
			}
		})
	}
}

// TestBuildIssueFilter_AllDatesValid verifies that when all 10 date fields
// are populated with a parseable date, buildIssueFilter returns no error and
// populates every corresponding filter field.
func TestBuildIssueFilter_AllDatesValid(t *testing.T) {
	const validDate = "2026-01-01"
	args := &ListArgs{
		CreatedAfter:  validDate,
		CreatedBefore: validDate,
		UpdatedAfter:  validDate,
		UpdatedBefore: validDate,
		ClosedAfter:   validDate,
		ClosedBefore:  validDate,
		DeferAfter:    validDate,
		DeferBefore:   validDate,
		DueAfter:      validDate,
		DueBefore:     validDate,
	}

	filter, err := buildIssueFilter(args)
	if err != nil {
		t.Fatalf("expected no error for all-valid dates, got: %v", err)
	}

	checks := []struct {
		name string
		got  *time.Time
	}{
		{"CreatedAfter", filter.CreatedAfter},
		{"CreatedBefore", filter.CreatedBefore},
		{"UpdatedAfter", filter.UpdatedAfter},
		{"UpdatedBefore", filter.UpdatedBefore},
		{"ClosedAfter", filter.ClosedAfter},
		{"ClosedBefore", filter.ClosedBefore},
		{"DeferAfter", filter.DeferAfter},
		{"DeferBefore", filter.DeferBefore},
		{"DueAfter", filter.DueAfter},
		{"DueBefore", filter.DueBefore},
	}
	for _, c := range checks {
		if c.got == nil {
			t.Errorf("expected filter.%s to be non-nil", c.name)
			continue
		}
		// parseTimeRPC with "2006-01-02" layout yields UTC midnight of 2026-01-01.
		if y, m, d := c.got.Date(); y != 2026 || m != time.January || d != 1 {
			t.Errorf("filter.%s: expected 2026-01-01, got %04d-%02d-%02d",
				c.name, y, m, d)
		}
	}
}

// TestBuildIssueFilter_EmptyDatesNoError verifies that a zero-value ListArgs
// produces no error and leaves every date filter pointer nil.
func TestBuildIssueFilter_EmptyDatesNoError(t *testing.T) {
	args := &ListArgs{}
	filter, err := buildIssueFilter(args)
	if err != nil {
		t.Fatalf("expected no error for empty ListArgs, got: %v", err)
	}

	checks := []struct {
		name string
		ptr  *time.Time
	}{
		{"CreatedAfter", filter.CreatedAfter},
		{"CreatedBefore", filter.CreatedBefore},
		{"UpdatedAfter", filter.UpdatedAfter},
		{"UpdatedBefore", filter.UpdatedBefore},
		{"ClosedAfter", filter.ClosedAfter},
		{"ClosedBefore", filter.ClosedBefore},
		{"DeferAfter", filter.DeferAfter},
		{"DeferBefore", filter.DeferBefore},
		{"DueAfter", filter.DueAfter},
		{"DueBefore", filter.DueBefore},
	}
	for _, c := range checks {
		if c.ptr != nil {
			t.Errorf("expected filter.%s to be nil, got %v", c.name, *c.ptr)
		}
	}
}

// TestBuildIssueFilter_FirstInvalidDateWins verifies that when multiple date
// fields are invalid, the function returns the error for the one checked
// first in buildIssueFilter's body (CreatedAfter precedes DueBefore).
func TestBuildIssueFilter_FirstInvalidDateWins(t *testing.T) {
	args := &ListArgs{
		CreatedAfter: "bad1",
		DueBefore:    "bad2",
	}
	_, err := buildIssueFilter(args)
	if err == nil {
		t.Fatalf("expected error for invalid dates, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "created-after") {
		t.Errorf("expected error to reference created-after (checked first), got %q", msg)
	}
	if strings.Contains(msg, "due-before") {
		t.Errorf("expected error NOT to reference due-before (checked later), got %q", msg)
	}
}

// TestHandleListKanban_InvalidDate verifies that an invalid date sent to the
// list_kanban RPC surfaces as a failed Response with the field-specific error
// instead of silently dropping the filter.
func TestHandleListKanban_InvalidDate(t *testing.T) {
	_, client, _, cleanup := setupTestServerWithStore(t)
	defer cleanup()

	args := &ListKanbanArgs{
		ListArgs: ListArgs{CreatedAfter: "not-a-date"},
	}
	// Execute surfaces resp.Success=false as a non-nil error while still
	// returning the decoded Response; we care about resp.Error here.
	resp, _ := client.Execute(OpListKanban, args)
	if resp == nil {
		t.Fatalf("expected non-nil response for Success=false path")
	}
	if resp.Success {
		t.Fatalf("expected Success=false for invalid date, got Success=true")
	}
	if !strings.Contains(resp.Error, "invalid --created-after date") {
		t.Errorf("expected Error to contain %q, got %q",
			"invalid --created-after date", resp.Error)
	}
}

// TestBuildIssueFilter_MaxIDsNotEnforced documents that buildIssueFilter does
// not enforce the maxIDs limit — that policy is the handler's responsibility.
// The function should accept any number of IDs and propagate them in filter.IDs.
func TestBuildIssueFilter_MaxIDsNotEnforced(t *testing.T) {
	ids := make([]string, 1500)
	for i := range ids {
		ids[i] = fmt.Sprintf("loomcli-x%05d", i)
	}
	args := &ListArgs{IDs: ids}
	filter, err := buildIssueFilter(args)
	if err != nil {
		t.Fatalf("expected no error from buildIssueFilter with 1500 IDs, got: %v", err)
	}
	if len(filter.IDs) != 1500 {
		t.Errorf("expected filter.IDs to contain all 1500 IDs, got %d", len(filter.IDs))
	}
}

// TestHandleListKanban_ExcessiveIDs verifies that passing >1000 IDs to the
// list_kanban RPC surfaces the maxIDs guard error instead of an opaque SQLite
// error from the IN-clause parameter limit.
func TestHandleListKanban_ExcessiveIDs(t *testing.T) {
	_, client, _, cleanup := setupTestServerWithStore(t)
	defer cleanup()

	ids := make([]string, 1001)
	for i := range ids {
		ids[i] = fmt.Sprintf("loomcli-x%04d", i)
	}
	args := &ListKanbanArgs{ListArgs: ListArgs{IDs: ids}}
	resp, _ := client.Execute(OpListKanban, args)
	if resp == nil {
		t.Fatalf("expected non-nil response for Success=false path")
	}
	if resp.Success {
		t.Fatalf("expected Success=false for 1001 IDs, got Success=true")
	}
	if !strings.Contains(resp.Error, "--id flag supports at most 1000 issue IDs, got 1001") {
		t.Errorf("expected Error to contain maxIDs guard message, got %q", resp.Error)
	}
}

// TestHandleListKanban_ExactlyMaxIDs verifies that passing exactly 1000 IDs
// does not trigger the guard (len > maxIDs, not >=).
func TestHandleListKanban_ExactlyMaxIDs(t *testing.T) {
	_, client, _, cleanup := setupTestServerWithStore(t)
	defer cleanup()

	ids := make([]string, 1000)
	for i := range ids {
		ids[i] = fmt.Sprintf("loomcli-x%04d", i)
	}
	args := &ListKanbanArgs{ListArgs: ListArgs{IDs: ids}}
	resp, err := client.Execute(OpListKanban, args)
	if err != nil {
		t.Fatalf("Execute returned transport error for 1000 IDs: %v", err)
	}
	if resp == nil || !resp.Success {
		errStr := ""
		if resp != nil {
			errStr = resp.Error
		}
		t.Fatalf("expected Success=true for 1000 IDs, got Success=false, Error=%q", errStr)
	}
}

// TestHandleListKanban_NoIDs verifies that the guard does not trigger when
// no IDs are provided.
func TestHandleListKanban_NoIDs(t *testing.T) {
	_, client, _, cleanup := setupTestServerWithStore(t)
	defer cleanup()

	args := &ListKanbanArgs{ListArgs: ListArgs{}}
	resp, err := client.Execute(OpListKanban, args)
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if resp == nil || !resp.Success {
		errStr := ""
		if resp != nil {
			errStr = resp.Error
		}
		t.Fatalf("expected Success=true for empty IDs, got Success=false, Error=%q", errStr)
	}
}

// TestHandleListKanban_FewIDs verifies that a small number of IDs (well under
// the limit) flows through the RPC without triggering the guard.
func TestHandleListKanban_FewIDs(t *testing.T) {
	_, client, _, cleanup := setupTestServerWithStore(t)
	defer cleanup()

	args := &ListKanbanArgs{
		ListArgs: ListArgs{IDs: []string{"loomcli-a", "loomcli-b", "loomcli-c", "loomcli-d", "loomcli-e"}},
	}
	resp, err := client.Execute(OpListKanban, args)
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if resp == nil || !resp.Success {
		errStr := ""
		if resp != nil {
			errStr = resp.Error
		}
		t.Fatalf("expected Success=true for 5 IDs, got Success=false, Error=%q", errStr)
	}
}

// TestHandleListKanban_ValidDates verifies that a valid date filter flows
// through the RPC path without error and yields a successful response.
func TestHandleListKanban_ValidDates(t *testing.T) {
	_, client, _, cleanup := setupTestServerWithStore(t)
	defer cleanup()

	createArgs := &CreateArgs{
		Title:     "kanban date fixture",
		IssueType: "task",
		Priority:  2,
	}
	if _, err := client.Create(createArgs); err != nil {
		t.Fatalf("Failed to create fixture issue: %v", err)
	}

	args := &ListKanbanArgs{
		ListArgs: ListArgs{CreatedAfter: "2020-01-01", Limit: 10},
	}
	resp, err := client.Execute(OpListKanban, args)
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected non-nil response")
	}
	if !resp.Success {
		t.Fatalf("expected Success=true for valid date, got Error=%q", resp.Error)
	}

	var payload ListKanbanResponse
	if err := json.Unmarshal(resp.Data, &payload); err != nil {
		t.Fatalf("failed to decode ListKanbanResponse: %v", err)
	}
	if len(payload.Issues) == 0 {
		t.Errorf("expected at least one issue in valid-date response, got 0")
	}
}
