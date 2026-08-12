//go:build workitems_e2e

package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// workItemsSuiteConfig configures the adapter conformance suite around the
// canonical Work Items owner API. Adapters do not define a second lifecycle
// contract for this suite to target.
type workItemsSuiteConfig struct {
	NewAPI                   func(testing.TB) workitems.API
	SupportsExplicitCreateID bool
}

func runWorkItemsConformance(t *testing.T, cfg workItemsSuiteConfig) {
	t.Helper()
	if cfg.NewAPI == nil {
		t.Fatal("workItemsSuiteConfig.NewAPI is required")
	}

	t.Run("CreateListGetPatchCloseReopen", func(t *testing.T) {
		runCreateListGetPatchCloseReopen(t, cfg.NewAPI(t))
	})
	t.Run("ReadyAndBlockedAgreeOnUnblockedIssue", func(t *testing.T) {
		runReadyAndBlockedAgreeOnUnblockedIssue(t, cfg.NewAPI(t))
	})
	t.Run("ExplicitCreateID", func(t *testing.T) {
		runExplicitCreateID(t, cfg)
	})
}

func runCreateListGetPatchCloseReopen(t *testing.T, api workitems.API) {
	t.Helper()
	ctx := suiteContext(t)
	created := createWorkItem(t, ctx, api, "lifecycle")
	assertWorkItem(t, created, "lifecycle", "open")
	assertListIncludes(t, ctx, api, created.ID)
	assertGetMatches(t, ctx, api, created)
	assertPatchTitle(t, ctx, api, created)
	assertCloseReopen(t, ctx, api, created.ID)
}

func runReadyAndBlockedAgreeOnUnblockedIssue(t *testing.T, api workitems.API) {
	t.Helper()
	ctx := suiteContext(t)
	created := createWorkItem(t, ctx, api, "ready")

	ready, err := api.Ready(ctx, workitems.AvailabilityQuery{Limit: 100})
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if !containsIssueSummaryID(ready, created.ID) {
		t.Fatalf("Ready did not include unblocked issue %q; got ids %v", created.ID, issueSummaryIDs(ready))
	}

	blocked, err := api.Blocked(ctx, workitems.AvailabilityQuery{Limit: 100})
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	if containsIssueSummaryID(blocked, created.ID) {
		t.Fatalf("Blocked unexpectedly included unblocked issue %q", created.ID)
	}
}

func runExplicitCreateID(t *testing.T, cfg workItemsSuiteConfig) {
	t.Helper()
	if !cfg.SupportsExplicitCreateID {
		t.Skip("adapter does not currently honor CreateCommand.ID")
	}
	api := cfg.NewAPI(t)
	ctx := suiteContext(t)
	wantID := "CONTRACT-" + strings.ToUpper(safeName(t.Name()))
	issue, err := api.Create(ctx, workitems.CreateCommand{
		ID:        wantID,
		Title:     uniqueTitle(t, "explicit-id"),
		Status:    "open",
		IssueType: "task",
		Priority:  2,
	})
	if err != nil {
		t.Fatalf("Create explicit ID: %v", err)
	}
	if issue.ID != wantID {
		t.Fatalf("CreateCommand.ID was not honored: got %q, want %q", issue.ID, wantID)
	}
}

func assertListIncludes(t *testing.T, ctx context.Context, api workitems.API, id string) {
	t.Helper()
	result, err := api.List(ctx, workitems.ListQuery{Filter: workitems.ListFilter{Status: "open", Limit: 100}})
	if err != nil {
		t.Fatalf("List(open): %v", err)
	}
	if result == nil || !containsListItemID(result.Issues, id) {
		var ids []string
		if result != nil {
			ids = listItemIDs(result.Issues)
		}
		t.Fatalf("List(open) did not include created issue %q; got ids %v", id, ids)
	}
}

func assertGetMatches(t *testing.T, ctx context.Context, api workitems.API, issue *workitems.IssueSummary) {
	t.Helper()
	detail, err := api.Get(ctx, workitems.GetQuery{IssueID: issue.ID})
	if err != nil {
		t.Fatalf("Get(%s): %v", issue.ID, err)
	}
	if detail.ID != issue.ID || detail.Title != issue.Title {
		t.Fatalf("Get(%s) = {ID:%q Title:%q}, want {ID:%q Title:%q}",
			issue.ID, detail.ID, detail.Title, issue.ID, issue.Title)
	}
}

func assertPatchTitle(t *testing.T, ctx context.Context, api workitems.API, issue *workitems.IssueSummary) {
	t.Helper()
	updatedTitle := issue.Title + " updated"
	if _, err := api.Patch(ctx, workitems.PatchCommand{IssueID: issue.ID, Title: &updatedTitle}); err != nil {
		t.Fatalf("Patch title: %v", err)
	}
	detail, err := api.Get(ctx, workitems.GetQuery{IssueID: issue.ID})
	if err != nil {
		t.Fatalf("Get after Patch: %v", err)
	}
	if detail.Title != updatedTitle {
		t.Fatalf("updated title = %q, want %q", detail.Title, updatedTitle)
	}
}

func assertCloseReopen(t *testing.T, ctx context.Context, api workitems.API, id string) {
	t.Helper()
	closed, err := api.Close(ctx, workitems.CloseCommand{IssueID: id, Reason: "work items conformance"})
	if err != nil {
		t.Fatalf("Close(%s): %v", id, err)
	}
	if closed == nil || closed.Closed == nil || closed.Closed.ID != id {
		t.Fatalf("Close result = %#v, want closed issue %q", closed, id)
	}
	if err := api.Reopen(ctx, workitems.ReopenCommand{IssueID: id, Reason: "work items conformance"}); err != nil {
		t.Fatalf("Reopen(%s): %v", id, err)
	}
	detail, err := api.Get(ctx, workitems.GetQuery{IssueID: id})
	if err != nil {
		t.Fatalf("Get after Reopen: %v", err)
	}
	if detail.Status != "open" {
		t.Fatalf("status after Reopen = %q, want open", detail.Status)
	}
}

func suiteContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func createWorkItem(t *testing.T, ctx context.Context, api workitems.API, suffix string) *workitems.IssueSummary {
	t.Helper()
	issue, err := api.Create(ctx, workitems.CreateCommand{
		Title:       uniqueTitle(t, suffix),
		Description: "created by Work Items conformance suite",
		Status:      "open",
		IssueType:   "task",
		Priority:    2,
		Labels:      []string{"workitems-conformance"},
		SourceRepo:  "contract-repo",
	})
	if err != nil {
		t.Fatalf("Create(%s): %v", suffix, err)
	}
	return issue
}

func assertWorkItem(t *testing.T, issue *workitems.IssueSummary, titleSuffix, status string) {
	t.Helper()
	if issue == nil {
		t.Fatal("Create returned nil issue")
	}
	if issue.ID == "" {
		t.Fatal("Create returned empty issue ID")
	}
	if !strings.Contains(issue.Title, titleSuffix) {
		t.Fatalf("created title = %q, want suffix %q", issue.Title, titleSuffix)
	}
	if issue.Status != status {
		t.Fatalf("created status = %q, want %q", issue.Status, status)
	}
}

func uniqueTitle(t *testing.T, suffix string) string {
	t.Helper()
	return fmt.Sprintf("work items conformance %s %d", suffix, time.Now().UnixNano())
}

func containsListItemID(items []workitems.ListItem, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func listItemIDs(items []workitems.ListItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func containsIssueSummaryID(items []workitems.IssueSummary, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func issueSummaryIDs(items []workitems.IssueSummary) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func safeName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, name)
	if len(name) > 24 {
		name = name[len(name)-24:]
	}
	return strings.Trim(name, "-")
}
