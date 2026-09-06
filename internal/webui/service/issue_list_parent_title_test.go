package service

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// summaryFakeBackend is a fake that also implements IssueSummaryBackend, so the
// backfill's preferred path can be exercised alongside the Get fallback that
// fakeIssueBackend alone provides.
type summaryFakeBackend struct {
	*fakeIssueBackend

	mu              sync.Mutex
	summaryCalls    []string
	summaryByID     map[string]*backend.IssueData
	summaryFallback error
}

func (f *summaryFakeBackend) GetSummary(_ context.Context, id string) (*backend.IssueData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.summaryCalls = append(f.summaryCalls, id)
	if data, ok := f.summaryByID[id]; ok {
		return data, nil
	}
	return nil, f.summaryFallback
}

func (f *summaryFakeBackend) summaryCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.summaryCalls)
}

// The board sends exclude_status=tombstone. A tombstoned epic is therefore
// dropped from the returned rows — but it was in the list response, so its
// title is already in hand and its children must resolve from the index alone,
// with no per-issue request at all.
func TestListIssues_ParentTitle_FromExcludedParent(t *testing.T) {
	const epic = "PUPPET-100"
	issues := []backend.IssueData{
		{ID: epic, Title: "Retired epic", Status: string(types.StatusTombstone), IssueType: "epic"},
	}
	for i := 0; i < 5; i++ {
		issues = append(issues, backend.IssueData{
			ID: "child-" + strconv.Itoa(i), Title: "Child", Status: string(types.StatusOpen), Parent: epic,
		})
	}
	fb := &fakeIssueBackend{listResult: issues}
	svc := newServiceWithFake(fb)

	result, err := svc.ListIssues(context.Background(), ListIssuesParams{
		Args:          &rpc.ListArgs{},
		ExcludeStatus: []string{string(types.StatusTombstone)},
	})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(result.Issues) != 5 {
		t.Fatalf("returned %d rows, want 5 (the tombstoned epic must be filtered out)", len(result.Issues))
	}
	for _, got := range result.Issues {
		if got.Issue.ID == epic {
			t.Fatalf("tombstoned epic %s leaked into the result", epic)
		}
		if got.ParentTitle == nil || *got.ParentTitle != "Retired epic" {
			t.Fatalf("%s ParentTitle = %v, want %q", got.Issue.ID, got.ParentTitle, "Retired epic")
		}
	}
	if n := fb.getCallCount(); n != 0 {
		t.Fatalf("Get calls = %d, want 0: the excluded parent's title was already in hand", n)
	}
}

// Same shape through the kanban path, where the blocked/deferred merges also
// feed the index.
func TestListIssues_Kanban_ParentTitle_FromExcludedParent(t *testing.T) {
	const epic = "PUPPET-200"
	fb := &fakeIssueBackend{
		listResult: []backend.IssueData{
			{ID: epic, Title: "Retired epic", Status: string(types.StatusTombstone), IssueType: "epic"},
			{ID: "child-1", Title: "Child", Status: string(types.StatusOpen), Parent: epic},
		},
		blockedResult: []backend.IssueData{
			{ID: "child-2", Title: "Blocked child", Status: string(types.StatusBlocked), Parent: epic},
		},
	}
	svc := newServiceWithFake(fb)

	result, err := svc.ListIssues(context.Background(), ListIssuesParams{
		Args:           &rpc.ListArgs{},
		ExcludeStatus:  []string{string(types.StatusTombstone)},
		IncludeBlocked: true,
	})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(result.KanbanIssues) != 2 {
		t.Fatalf("returned %d kanban rows, want 2", len(result.KanbanIssues))
	}
	for _, got := range result.KanbanIssues {
		if got.ParentTitle == nil || *got.ParentTitle != "Retired epic" {
			t.Fatalf("%s ParentTitle = %v, want %q", got.Issue.ID, got.ParentTitle, "Retired epic")
		}
	}
	if n := fb.getCallCount(); n != 0 {
		t.Fatalf("Get calls = %d, want 0", n)
	}
}

// A parent that is genuinely absent from the list still needs a lookup. It goes
// through GetSummary when the backend has it — one request instead of Get's
// three on fleet-db — and through Get when it does not.
func TestBackfillParentTitles_PrefersGetSummary(t *testing.T) {
	inner := &fakeIssueBackend{
		listResult: []backend.IssueData{
			{ID: "child-1", Title: "Child", Status: string(types.StatusOpen), Parent: "parent-off"},
		},
	}
	fb := &summaryFakeBackend{
		fakeIssueBackend: inner,
		summaryByID: map[string]*backend.IssueData{
			"parent-off": {ID: "parent-off", Title: "Offscreen Parent"},
		},
	}
	svc := NewIssueServiceWithBackend(nil, nil, nil, func(_ context.Context) backend.IssueBackend { return fb })

	result, err := svc.ListIssues(context.Background(), ListIssuesParams{Args: &rpc.ListArgs{}})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if got := result.Issues[0].ParentTitle; got == nil || *got != "Offscreen Parent" {
		t.Fatalf("ParentTitle = %v, want Offscreen Parent", got)
	}
	if n := fb.summaryCallCount(); n != 1 {
		t.Fatalf("GetSummary calls = %d, want exactly 1", n)
	}
	if n := inner.getCallCount(); n != 0 {
		t.Fatalf("Get calls = %d, want 0: GetSummary is the cheaper path", n)
	}
}

func TestBackfillParentTitles_FallsBackToGet(t *testing.T) {
	fb := &fakeIssueBackend{
		listResult: []backend.IssueData{
			{ID: "child-1", Title: "Child", Status: string(types.StatusOpen), Parent: "parent-off"},
		},
		getByID: map[string]*backend.IssueDetailData{
			"parent-off": {IssueData: backend.IssueData{ID: "parent-off", Title: "Offscreen Parent"}},
		},
	}
	if _, ok := backend.IssueBackend(fb).(backend.IssueSummaryBackend); ok {
		t.Fatal("fakeIssueBackend implements IssueSummaryBackend; the fallback is untested")
	}
	svc := newServiceWithFake(fb)

	result, err := svc.ListIssues(context.Background(), ListIssuesParams{Args: &rpc.ListArgs{}})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if got := result.Issues[0].ParentTitle; got == nil || *got != "Offscreen Parent" {
		t.Fatalf("ParentTitle = %v, want Offscreen Parent", got)
	}
	if n := fb.getCallCount(); n != 1 {
		t.Fatalf("Get calls = %d, want exactly 1", n)
	}
}

// A GetSummary that fails degrades to a missing title, never to a failed list.
func TestBackfillParentTitles_SummaryErrorIsNonFatal(t *testing.T) {
	fb := &summaryFakeBackend{
		fakeIssueBackend: &fakeIssueBackend{
			listResult: []backend.IssueData{
				{ID: "child-1", Title: "Child", Status: string(types.StatusOpen), Parent: "parent-off"},
			},
		},
		summaryFallback: backend.ErrNotFound("GetSummary", "no such issue"),
	}
	svc := NewIssueServiceWithBackend(nil, nil, nil, func(_ context.Context) backend.IssueBackend { return fb })

	result, err := svc.ListIssues(context.Background(), ListIssuesParams{Args: &rpc.ListArgs{}})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(result.Issues) != 1 {
		t.Fatalf("returned %d rows, want 1", len(result.Issues))
	}
	if got := result.Issues[0].ParentTitle; got != nil {
		t.Fatalf("ParentTitle = %v, want nil so the FE renders the bare parent ID", *got)
	}
}
