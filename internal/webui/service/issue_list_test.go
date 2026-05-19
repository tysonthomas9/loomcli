package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
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

func TestListIssues_Pool_ListKanbanStandardAndBlockedResults(t *testing.T) {
	now := time.Now().UTC()
	var captured []rpc.ListKanbanArgs
	socket := startMovePoolRPCServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case rpc.OpHealth:
			return movePoolJSONResponse(t, rpc.HealthResponse{Status: "healthy", Compatible: true})
		case rpc.OpListKanban:
			var args rpc.ListKanbanArgs
			if err := json.Unmarshal(req.Args, &args); err != nil {
				return rpc.Response{Success: false, Error: "bad args"}
			}
			captured = append(captured, args)
			return movePoolJSONResponse(t, rpc.ListKanbanResponse{Issues: []*rpc.KanbanIssueRPC{
				{
					IssueWithCounts: &types.IssueWithCounts{Issue: &types.Issue{
						ID: "ISS-1", Title: "Ready", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask,
						CreatedAt: now, UpdatedAt: now,
					}},
					ParentID: "EPIC-1", ParentTitle: "Epic", Repo: "api", IsReady: true,
				},
				{
					IssueWithCounts: &types.IssueWithCounts{Issue: &types.Issue{
						ID: "ISS-2", Title: "Blocked", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeBug,
						CreatedAt: now, UpdatedAt: now,
					}},
					IsBlocked: true, BlockedBy: []string{"ISS-0"},
				},
			}})
		default:
			return rpc.Response{Success: false, Error: "unexpected op " + req.Operation}
		}
	})
	pool, err := daemon.NewConnectionPool(socket, 1)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	pool.SetDialTimeout(time.Second)
	svc := NewIssueService(pool, nil, nil)

	standard, err := svc.ListIssues(context.Background(), ListIssuesParams{
		Args: &rpc.ListArgs{Status: string(types.StatusOpen), Limit: 2},
	})
	if err != nil {
		t.Fatalf("ListIssues standard: %v", err)
	}
	if len(standard.Issues) != 2 || standard.Issues[0].Parent == nil || *standard.Issues[0].Parent != "EPIC-1" {
		t.Fatalf("standard result = %+v", standard)
	}

	blocked, err := svc.ListIssues(context.Background(), ListIssuesParams{
		Args:           &rpc.ListArgs{Assignee: "alice"},
		IncludeBlocked: true,
		ExcludeStatus:  []string{string(types.StatusClosed)},
	})
	if err != nil {
		t.Fatalf("ListIssues blocked: %v", err)
	}
	if len(blocked.KanbanIssues) != 2 || !blocked.KanbanIssues[1].IsBlocked || blocked.KanbanIssues[1].BlockedByCount != 1 {
		t.Fatalf("blocked result = %+v", blocked)
	}
	if len(captured) != 2 || captured[1].Assignee != "alice" || !captured[1].IncludeBlocked || len(captured[1].ExcludeStatus) != 1 {
		t.Fatalf("captured args = %+v", captured)
	}
}

func TestListIssues_PoolFallbackAndErrorBranches(t *testing.T) {
	ctx := context.Background()
	fb := &fakeIssueBackend{listResult: []backend.IssueData{{ID: "BACKEND-1", Status: string(types.StatusOpen)}}}
	pool := &fakeIssuePool{getErr: context.DeadlineExceeded}
	svc := &issueServiceImpl{pool: pool, backendFn: func(context.Context) backend.IssueBackend { return fb }}
	result, err := svc.ListIssues(ctx, ListIssuesParams{Args: &rpc.ListArgs{}})
	if err != nil || len(result.Issues) != 1 || result.Issues[0].Issue.ID != "BACKEND-1" {
		t.Fatalf("backend fallback result = %+v err=%v", result, err)
	}

	svc = &issueServiceImpl{pool: &fakeIssuePool{getErr: context.DeadlineExceeded}}
	if _, err := svc.ListIssues(ctx, ListIssuesParams{Args: &rpc.ListArgs{}}); !serviceErrorKind(err, KindTimeout) {
		t.Fatalf("deadline without backend err = %v", err)
	}

	svc = &issueServiceImpl{pool: &fakeIssuePool{getErr: errors.New("dial failed")}}
	if _, err := svc.ListIssues(ctx, ListIssuesParams{Args: &rpc.ListArgs{}}); !serviceErrorKind(err, KindUnavailable) {
		t.Fatalf("dial failure err = %v", err)
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
