package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

func TestListIssues_Backend_StandardListFiltersExcludedStatus(t *testing.T) {
	now := time.Now().UTC()
	fb := &fakeIssueBackend{
		listResult: []backend.IssueData{
			{
				ID:              "keep-1",
				Title:           "Keep",
				Status:          string(types.StatusOpen),
				IssueType:       string(types.TypeTask),
				Priority:        2,
				Parent:          "epic-1",
				SourceRepo:      "repo-a",
				DependencyCount: 1,
				DependentCount:  2,
				CreatedAt:       now,
			},
			{ID: "drop-1", Title: "Drop", Status: string(types.StatusClosed)},
		},
	}
	svc := newServiceWithFake(fb)

	result, err := svc.ListIssues(context.Background(), ListIssuesParams{
		Args: &rpc.ListArgs{
			Status:    string(types.StatusOpen),
			IssueType: string(types.TypeTask),
			Assignee:  "alice",
			Labels:    []string{"frontend"},
			ParentID:  "epic-1",
			Limit:     20,
		},
		ExcludeStatus: []string{string(types.StatusClosed)},
	})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(result.Issues) != 1 {
		t.Fatalf("Issues = %d, want 1", len(result.Issues))
	}
	got := result.Issues[0]
	if got.Issue.ID != "keep-1" || got.Issue.Status != types.StatusOpen {
		t.Fatalf("issue = %+v, want keep-1/open", got.Issue)
	}
	if got.Parent == nil || *got.Parent != "epic-1" {
		t.Fatalf("Parent = %v, want epic-1", got.Parent)
	}
	if got.Repo == nil || *got.Repo != "repo-a" {
		t.Fatalf("Repo = %v, want repo-a", got.Repo)
	}
	if got.DependencyCount != 1 || got.DependentCount != 2 {
		t.Fatalf("counts = %d/%d, want 1/2", got.DependencyCount, got.DependentCount)
	}
	if len(fb.listCalls) != 1 {
		t.Fatalf("list calls = %d, want 1", len(fb.listCalls))
	}
	call := fb.listCalls[0]
	if call.Status != string(types.StatusOpen) || call.IssueType != string(types.TypeTask) ||
		call.Assignee != "alice" || call.ParentID != "epic-1" || call.Limit != 20 {
		t.Fatalf("List opts = %+v", call)
	}
}

func TestListIssues_Backend_KanbanMergesBlockedIssues(t *testing.T) {
	fb := &fakeIssueBackend{
		listResult: []backend.IssueData{
			{
				ID:         "work-1",
				Title:      "Work",
				Status:     string(types.StatusOpen),
				IssueType:  string(types.TypeTask),
				Priority:   1,
				SourceRepo: "repo-a",
			},
		},
		blockedResult: []backend.IssueData{
			{
				ID:             "work-1",
				Status:         string(types.StatusOpen),
				BlockedByCount: 1,
				BlockedBy:      []string{"dep-1"},
			},
			{
				ID:             "blocked-extra",
				Title:          "Blocked Extra",
				Status:         string(types.StatusOpen),
				IssueType:      string(types.TypeBug),
				Priority:       3,
				BlockedByCount: 0,
			},
		},
	}
	svc := newServiceWithFake(fb)

	result, err := svc.ListIssues(context.Background(), ListIssuesParams{
		Args: &rpc.ListArgs{
			ParentID:  "epic-1",
			Assignee:  "alice",
			Priority:  intPtr(2),
			IssueType: string(types.TypeTask),
		},
		IncludeBlocked: true,
	})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(result.KanbanIssues) != 2 {
		t.Fatalf("KanbanIssues = %d, want 2", len(result.KanbanIssues))
	}
	work := findKanbanIssue(t, result.KanbanIssues, "work-1")
	if !work.IsBlocked || work.BlockedByCount != 1 || len(work.BlockedBy) != 1 || work.BlockedBy[0] != "dep-1" {
		t.Fatalf("work blocked summary = blocked:%v count:%d by:%v", work.IsBlocked, work.BlockedByCount, work.BlockedBy)
	}
	if work.Issue.Status != types.StatusOpen {
		t.Fatalf("work status = %q, want raw open status", work.Issue.Status)
	}
	extra := findKanbanIssue(t, result.KanbanIssues, "blocked-extra")
	if !extra.IsBlocked || extra.BlockedByCount != 0 {
		t.Fatalf("extra blocked summary = blocked:%v count:%d", extra.IsBlocked, extra.BlockedByCount)
	}
	if len(fb.blockedCalls) != 1 {
		t.Fatalf("blocked calls = %d, want 1", len(fb.blockedCalls))
	}
	call := fb.blockedCalls[0]
	if call.ParentID != "epic-1" || call.Assignee != "alice" || call.Priority == nil ||
		*call.Priority != 2 || call.Type != string(types.TypeTask) {
		t.Fatalf("Blocked opts = %+v", call)
	}
	if len(fb.readyCalls) != 1 {
		t.Fatalf("ready calls = %d, want 1", len(fb.readyCalls))
	}
	readyCall := fb.readyCalls[0]
	if readyCall.ParentID != "epic-1" || readyCall.Assignee != "alice" || readyCall.Priority == nil ||
		*readyCall.Priority != 2 || readyCall.Type != string(types.TypeTask) {
		t.Fatalf("Ready opts = %+v", readyCall)
	}
	if len(fb.deferredCalls) != 1 {
		t.Fatalf("deferred calls = %d, want 1", len(fb.deferredCalls))
	}
}

func TestListIssues_Backend_KanbanUsesCanonicalReadyAndDeferred(t *testing.T) {
	future := time.Now().UTC().Add(24 * time.Hour)
	fb := &fakeIssueBackend{
		listResult: []backend.IssueData{
			{
				ID:        "ready-1",
				Title:     "Ready",
				Status:    string(types.StatusOpen),
				IssueType: string(types.TypeTask),
				Priority:  1,
			},
			{
				ID:         "future-deferred",
				Title:      "Future Deferred",
				Status:     string(types.StatusOpen),
				IssueType:  string(types.TypeTask),
				Priority:   2,
				DeferUntil: &future,
			},
			{
				ID:        "review-1",
				Title:     "Review",
				Status:    string(types.StatusReview),
				IssueType: string(types.TypeTask),
				Priority:  3,
			},
		},
		readyResult: []backend.IssueData{
			{ID: "ready-1", Status: string(types.StatusOpen)},
		},
		deferredResult: []backend.IssueData{
			{ID: "future-deferred", Status: string(types.StatusOpen), DeferUntil: &future},
		},
	}
	svc := newServiceWithFake(fb)

	result, err := svc.ListIssues(context.Background(), ListIssuesParams{
		Args:           &rpc.ListArgs{},
		IncludeBlocked: true,
	})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	ready := findKanbanIssue(t, result.KanbanIssues, "ready-1")
	if !ready.IsReady || ready.IsDeferred || ready.IsBlocked {
		t.Fatalf("ready flags = ready:%v deferred:%v blocked:%v", ready.IsReady, ready.IsDeferred, ready.IsBlocked)
	}
	deferred := findKanbanIssue(t, result.KanbanIssues, "future-deferred")
	if deferred.IsReady || !deferred.IsDeferred || deferred.Issue.Status != types.StatusOpen {
		t.Fatalf("deferred flags/status = ready:%v deferred:%v status:%q", deferred.IsReady, deferred.IsDeferred, deferred.Issue.Status)
	}
	review := findKanbanIssue(t, result.KanbanIssues, "review-1")
	if review.IsReady || review.IsDeferred || review.IsBlocked {
		t.Fatalf("review flags = ready:%v deferred:%v blocked:%v", review.IsReady, review.IsDeferred, review.IsBlocked)
	}
}

func TestListIssues_Backend_KanbanAppendsDeferredOnlyItems(t *testing.T) {
	future := time.Now().UTC().Add(24 * time.Hour)
	fb := &fakeIssueBackend{
		listResult: []backend.IssueData{
			{
				ID:        "ready-1",
				Title:     "Ready",
				Status:    string(types.StatusOpen),
				IssueType: string(types.TypeTask),
				Priority:  1,
			},
		},
		readyResult: []backend.IssueData{
			{ID: "ready-1", Status: string(types.StatusOpen)},
		},
		deferredResult: []backend.IssueData{
			{
				ID:         "future-deferred",
				Title:      "Future Deferred",
				Status:     string(types.StatusOpen),
				IssueType:  string(types.TypeTask),
				Priority:   2,
				DeferUntil: &future,
			},
		},
	}
	svc := newServiceWithFake(fb)

	result, err := svc.ListIssues(context.Background(), ListIssuesParams{
		Args:           &rpc.ListArgs{},
		IncludeBlocked: true,
	})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	deferred := findKanbanIssue(t, result.KanbanIssues, "future-deferred")
	if deferred.IsReady || !deferred.IsDeferred || deferred.Issue.Status != types.StatusOpen {
		t.Fatalf("deferred flags/status = ready:%v deferred:%v status:%q", deferred.IsReady, deferred.IsDeferred, deferred.Issue.Status)
	}
}

func findKanbanIssue(t *testing.T, issues []KanbanIssue, id string) KanbanIssue {
	t.Helper()
	for _, issue := range issues {
		if issue.Issue.ID == id {
			return issue
		}
	}
	t.Fatalf("issue %q not found in %+v", id, issues)
	return KanbanIssue{}
}

func intPtr(v int) *int {
	return &v
}

// TestBackendIssueDataToWithCounts_CarriesNotes guards the kanban/list read
// path: notes must flow from the slim IssueData projection into the embedded
// types.Issue, so the board's isBlockedWithNotes (status == blocked && notes)
// surfaces a blocked issue's external-blocker reason without a detail fetch.
func TestBackendIssueDataToWithCounts_CarriesNotes(t *testing.T) {
	d := &backend.IssueData{
		ID:     "WEB-63",
		Title:  "P1-8 gate",
		Status: "blocked",
		Notes:  "BLOCKED: waiting on sibling P1 tasks",
	}
	wc := backendIssueDataToWithCounts(d)
	if wc == nil || wc.Issue == nil {
		t.Fatal("backendIssueDataToWithCounts returned nil issue")
	}
	if wc.Issue.Notes != "BLOCKED: waiting on sibling P1 tasks" {
		t.Errorf("Notes = %q, want it carried into the embedded Issue", wc.Issue.Notes)
	}
}

func TestBackendIssueDataToWithCounts_CarriesExternalRef(t *testing.T) {
	ref := "https://github.com/owner/repo/pull/42"
	d := &backend.IssueData{ID: "WEB-64", Title: "PR-linked", Status: "review", ExternalRef: ref}
	wc := backendIssueDataToWithCounts(d)
	if wc == nil || wc.Issue == nil {
		t.Fatal("backendIssueDataToWithCounts returned nil issue")
	}
	if wc.Issue.ExternalRef == nil || *wc.Issue.ExternalRef != ref {
		t.Errorf("ExternalRef = %v, want %q carried into the embedded Issue", wc.Issue.ExternalRef, ref)
	}
}

// --- parent_title enrichment (PUPPET-219) ---

func TestParentTitleIndex_SkipsEmptyIDsAndTitles(t *testing.T) {
	index := parentTitleIndex([]backend.IssueData{
		{ID: "a", Title: "Alpha"},
		{ID: "b", Title: ""},
		{ID: "", Title: "No ID"},
		{ID: "a", Title: "Alpha Duplicate"},
	})

	if len(index) != 1 {
		t.Fatalf("index = %v, want a single entry", index)
	}
	if index["a"] != "Alpha" {
		t.Errorf("index[a] = %q, want the first entry to win", index["a"])
	}
	if _, ok := index["b"]; ok {
		t.Errorf("empty title indexed; want it treated as unresolved")
	}
}

func TestListIssues_Backend_ParentTitleFromResultSet(t *testing.T) {
	fb := &fakeIssueBackend{
		listResult: []backend.IssueData{
			{ID: "parent-1", Title: "Parent One", Status: string(types.StatusOpen)},
			{ID: "child-1", Title: "Child", Status: string(types.StatusOpen), Parent: "parent-1"},
		},
	}
	svc := newServiceWithFake(fb)

	result, err := svc.ListIssues(context.Background(), ListIssuesParams{Args: &rpc.ListArgs{}})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	var child *IssueWithParent
	for i := range result.Issues {
		if result.Issues[i].Issue.ID == "child-1" {
			child = &result.Issues[i]
		}
	}
	if child == nil {
		t.Fatal("child-1 missing from result")
	}
	if child.ParentTitle == nil || *child.ParentTitle != "Parent One" {
		t.Fatalf("ParentTitle = %v, want Parent One", child.ParentTitle)
	}
	if n := fb.getCallCount(); n != 0 {
		t.Fatalf("Get calls = %d, want 0 — the title was already in the result set", n)
	}
}

// The board's real path: include_blocked=true, so KanbanIssues carry the title.
func TestListIssues_Backend_KanbanParentTitleFromResultSet(t *testing.T) {
	fb := &fakeIssueBackend{
		listResult: []backend.IssueData{
			{ID: "parent-1", Title: "Parent One", Status: string(types.StatusOpen)},
			{ID: "child-1", Title: "Child", Status: string(types.StatusOpen), Parent: "parent-1"},
			{ID: "orphan-1", Title: "Orphan", Status: string(types.StatusOpen)},
		},
	}
	svc := newServiceWithFake(fb)

	result, err := svc.ListIssues(context.Background(), ListIssuesParams{
		Args:           &rpc.ListArgs{},
		IncludeBlocked: true,
	})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	child := findKanbanIssue(t, result.KanbanIssues, "child-1")
	if child.ParentTitle == nil || *child.ParentTitle != "Parent One" {
		t.Fatalf("ParentTitle = %v, want Parent One", child.ParentTitle)
	}
	orphan := findKanbanIssue(t, result.KanbanIssues, "orphan-1")
	if orphan.Parent != nil || orphan.ParentTitle != nil {
		t.Fatalf("orphan Parent/ParentTitle = %v/%v, want both nil", orphan.Parent, orphan.ParentTitle)
	}
	if n := fb.getCallCount(); n != 0 {
		t.Fatalf("Get calls = %d, want 0", n)
	}
}

func TestListIssues_Backend_ParentTitleBackfillsMissingParent(t *testing.T) {
	fb := &fakeIssueBackend{
		listResult: []backend.IssueData{
			{ID: "child-1", Title: "Child", Status: string(types.StatusOpen), Parent: "parent-off"},
			{ID: "child-2", Title: "Child Two", Status: string(types.StatusOpen), Parent: "parent-off"},
		},
		getByID: map[string]*backend.IssueDetailData{
			"parent-off": {IssueData: backend.IssueData{ID: "parent-off", Title: "Offscreen Parent"}},
		},
	}
	svc := newServiceWithFake(fb)

	result, err := svc.ListIssues(context.Background(), ListIssuesParams{Args: &rpc.ListArgs{}})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	for _, got := range result.Issues {
		if got.ParentTitle == nil || *got.ParentTitle != "Offscreen Parent" {
			t.Fatalf("%s ParentTitle = %v, want Offscreen Parent", got.Issue.ID, got.ParentTitle)
		}
	}
	// Two children, one distinct parent: exactly one extra Get.
	if n := fb.getCallCount(); n != 1 {
		t.Fatalf("Get calls = %d, want exactly 1", n)
	}
}

func TestListIssues_Backend_ParentTitleBackfillErrorIsNonFatal(t *testing.T) {
	fb := &fakeIssueBackend{
		listResult: []backend.IssueData{
			{ID: "child-1", Title: "Child", Status: string(types.StatusOpen), Parent: "parent-off"},
		},
		getErr: backend.ErrNotFound("get", "no such issue"),
	}
	svc := newServiceWithFake(fb)

	result, err := svc.ListIssues(context.Background(), ListIssuesParams{Args: &rpc.ListArgs{}})
	if err != nil {
		t.Fatalf("ListIssues: %v — a failed title lookup must not fail the list", err)
	}
	if len(result.Issues) != 1 {
		t.Fatalf("Issues = %d, want 1", len(result.Issues))
	}
	got := result.Issues[0]
	if got.Parent == nil || *got.Parent != "parent-off" {
		t.Fatalf("Parent = %v, want parent-off", got.Parent)
	}
	if got.ParentTitle != nil {
		t.Fatalf("ParentTitle = %v, want nil", got.ParentTitle)
	}
}

func TestListIssues_Backend_ParentTitleBackfillSkippedPastCap(t *testing.T) {
	issues := make([]backend.IssueData, 0, parentTitleBackfillMax+1)
	getByID := make(map[string]*backend.IssueDetailData)
	for i := 0; i <= parentTitleBackfillMax; i++ {
		childID := "child-" + strconv.Itoa(i)
		parentID := "parent-" + strconv.Itoa(i)
		issues = append(issues, backend.IssueData{
			ID: childID, Title: "Child", Status: string(types.StatusOpen), Parent: parentID,
		})
		getByID[parentID] = &backend.IssueDetailData{
			IssueData: backend.IssueData{ID: parentID, Title: "Parent " + strconv.Itoa(i)},
		}
	}
	fb := &fakeIssueBackend{listResult: issues, getByID: getByID}
	svc := newServiceWithFake(fb)

	result, err := svc.ListIssues(context.Background(), ListIssuesParams{Args: &rpc.ListArgs{}})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if n := fb.getCallCount(); n != 0 {
		t.Fatalf("Get calls = %d, want 0 past the backfill cap", n)
	}
	for _, got := range result.Issues {
		if got.ParentTitle != nil {
			t.Fatalf("%s ParentTitle = %v, want nil past the cap", got.Issue.ID, got.ParentTitle)
		}
	}
}

func TestListIssues_Backend_ParentWithEmptyTitleStaysUnresolved(t *testing.T) {
	fb := &fakeIssueBackend{
		listResult: []backend.IssueData{
			{ID: "parent-1", Title: "", Status: string(types.StatusOpen)},
			{ID: "child-1", Title: "Child", Status: string(types.StatusOpen), Parent: "parent-1"},
		},
		getByID: map[string]*backend.IssueDetailData{
			"parent-1": {IssueData: backend.IssueData{ID: "parent-1", Title: ""}},
		},
	}
	svc := newServiceWithFake(fb)

	result, err := svc.ListIssues(context.Background(), ListIssuesParams{Args: &rpc.ListArgs{}})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	for _, got := range result.Issues {
		if got.Issue.ID != "child-1" {
			continue
		}
		if got.ParentTitle != nil {
			t.Fatalf("ParentTitle = %v, want nil for an empty parent title", got.ParentTitle)
		}
	}
}
