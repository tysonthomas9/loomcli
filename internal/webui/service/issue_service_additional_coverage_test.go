package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

func TestIssueServiceSearchReopenCommentsAndDependencies(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	fb := &fakeIssueBackend{
		searchResult: []backend.IssueData{{
			ID:         "LOOM-1",
			Title:      "Searchable issue",
			Status:     "open",
			Priority:   2,
			IssueType:  "task",
			SourceRepo: "api",
			CreatedAt:  now,
			UpdatedAt:  now,
		}},
		listCommentsResult: []backend.CommentData{{
			ID:        7,
			IssueID:   "LOOM-1",
			Author:    "web-ui",
			Text:      "hello",
			CreatedAt: now,
		}},
		getResult: &backend.IssueDetailData{
			IssueData: backend.IssueData{ID: "LOOM-1", Title: "Has deps", Status: "open", CreatedAt: now, UpdatedAt: now},
			Dependencies: []backend.DependencyData{{
				IssueID:     "LOOM-1",
				DependsOnID: "LOOM-0",
				Type:        "blocks",
				Title:       "blocker",
				Status:      "open",
				CreatedAt:   now,
			}},
		},
	}
	svc := newServiceWithFake(fb)
	ctx := context.Background()

	raw, err := svc.SearchIssues(ctx, SearchIssuesParams{Query: "search", Limit: 5})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	var searchOut []map[string]any
	if err := json.Unmarshal(raw, &searchOut); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	if len(searchOut) != 1 || searchOut[0]["id"] != "LOOM-1" {
		t.Fatalf("search output = %#v", searchOut)
	}
	if len(fb.searchCalls) != 1 || fb.searchCalls[0].query != "search" || fb.searchCalls[0].limit != 5 {
		t.Fatalf("search calls = %#v", fb.searchCalls)
	}
	if _, err := svc.SearchIssues(ctx, SearchIssuesParams{Query: "  "}); err == nil {
		t.Fatal("expected empty search query validation error")
	}
	if _, err := svc.SearchIssues(ctx, SearchIssuesParams{Query: "x", Limit: -1}); err == nil {
		t.Fatal("expected negative search limit validation error")
	}

	if err := svc.ReopenIssue(ctx, ReopenIssueParams{IssueID: "LOOM-1", Reason: "fixed"}); err != nil {
		t.Fatalf("ReopenIssue: %v", err)
	}
	if len(fb.reopenCalls) != 1 || fb.reopenCalls[0].id != "LOOM-1" || fb.reopenCalls[0].params.Reason != "fixed" {
		t.Fatalf("reopen calls = %#v", fb.reopenCalls)
	}
	if err := svc.ReopenIssue(ctx, ReopenIssueParams{}); err == nil {
		t.Fatal("expected missing issue ID validation error")
	}

	comments, err := svc.ListComments(ctx, "LOOM-1")
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(comments) != 1 || comments[0].Text != "hello" {
		t.Fatalf("comments = %+v", comments)
	}
	if len(fb.listCommentsCalls) != 1 || fb.listCommentsCalls[0] != "LOOM-1" {
		t.Fatalf("list comment calls = %#v", fb.listCommentsCalls)
	}
	if _, err := svc.ListComments(ctx, " "); err == nil {
		t.Fatal("expected missing comment issue ID validation error")
	}

	depsRaw, err := svc.ListDependencies(ctx, "LOOM-1")
	if err != nil {
		t.Fatalf("ListDependencies: %v", err)
	}
	if !strings.Contains(string(depsRaw), "LOOM-0") {
		t.Fatalf("dependencies output = %s", depsRaw)
	}
	if _, err := svc.ListDependencies(ctx, " "); err == nil {
		t.Fatal("expected missing dependency issue ID validation error")
	}
	fb.getResult = nil
	if _, err := svc.ListDependencies(ctx, "MISSING"); err == nil {
		t.Fatal("expected missing dependency issue not found error")
	}
}

func TestBuildResultFromKanbanRPC(t *testing.T) {
	base := &types.IssueWithCounts{
		Issue: &types.Issue{
			ID:       "LOOM-1",
			Title:    "Kanban issue",
			Status:   types.StatusOpen,
			Priority: 1,
		},
		DependencyCount: 2,
		DependentCount:  3,
	}
	resp := &rpc.ListKanbanResponse{Issues: []*rpc.KanbanIssueRPC{{
		IssueWithCounts: base,
		ParentID:        "EPIC-1",
		ParentTitle:     "Epic",
		Repo:            "api",
		IsReady:         true,
		BlockedByCount:  0,
		BlockedBy:       []string{"LOOM-0"},
	}}}

	plain := buildResultFromKanbanRPC(resp, false)
	if len(plain.Issues) != 1 || plain.Issues[0].Parent == nil || *plain.Issues[0].Parent != "EPIC-1" {
		t.Fatalf("plain result = %+v", plain)
	}
	if plain.Issues[0].Repo == nil || *plain.Issues[0].Repo != "api" {
		t.Fatalf("plain repo = %+v", plain.Issues[0].Repo)
	}

	kanban := buildResultFromKanbanRPC(resp, true)
	if len(kanban.KanbanIssues) != 1 {
		t.Fatalf("kanban result = %+v", kanban)
	}
	item := kanban.KanbanIssues[0]
	if !item.IsReady || !item.IsBlocked || item.BlockedByCount != 1 {
		t.Fatalf("kanban item = %+v", item)
	}
}

func TestMoveIssueViaBackendWarningsAndValidation(t *testing.T) {
	now := time.Now()
	fb := &fakeIssueBackend{
		getResult: &backend.IssueDetailData{
			IssueData: backend.IssueData{
				ID:        "LOOM-1",
				Title:     "Move me",
				Status:    "open",
				Priority:  2,
				Assignee:  "planner",
				Labels:    []string{"go"},
				CreatedAt: now,
				UpdatedAt: now,
			},
			Description: "body",
		},
		createResult:       &backend.IssueData{ID: "LOOM-9", Title: "Move me", Status: "open"},
		addCommentErr:      errors.New("comment failed"),
		closeErr:           errors.New("close failed"),
		listCommentsErr:    errors.New("unused"),
		listEventsErr:      errors.New("unused"),
		postClaimUpdateErr: errors.New("unused"),
	}
	svc := NewIssueServiceWithBackend(nil, nil, func(ctx context.Context, wsID string) context.Context {
		return context.WithValue(ctx, struct{}{}, wsID)
	}, func(context.Context) backend.IssueBackend { return fb })

	result, err := svc.MoveIssue(context.Background(), MoveIssueParams{
		IssueID:         "LOOM-1",
		TargetWorkspace: "Target",
		Validator:       testWorkspaceValidator{targetID: "TARGET"},
	})
	if err != nil {
		t.Fatalf("MoveIssue: %v", err)
	}
	if result.SourceID != "LOOM-1" || result.TargetID != "LOOM-9" {
		t.Fatalf("move result = %+v", result)
	}
	if len(result.Warnings) != 3 {
		t.Fatalf("warnings = %+v", result.Warnings)
	}
	if len(fb.createParams) != 1 || !strings.Contains(fb.createParams[0].Description, "Moved from LOOM-1") {
		t.Fatalf("create params = %#v", fb.createParams)
	}

	if _, err := svc.MoveIssue(context.Background(), MoveIssueParams{Validator: testWorkspaceValidator{targetID: "TARGET"}}); err == nil {
		t.Fatal("expected missing issue ID validation error")
	}
	if _, err := svc.MoveIssue(context.Background(), MoveIssueParams{IssueID: "LOOM-1"}); err == nil {
		t.Fatal("expected missing validator validation error")
	}
	fb.getResult.Status = string(types.StatusClosed)
	if _, err := svc.MoveIssue(context.Background(), MoveIssueParams{
		IssueID:         "LOOM-1",
		TargetWorkspace: "Target",
		Validator:       testWorkspaceValidator{targetID: "TARGET"},
	}); err == nil {
		t.Fatal("expected closed issue validation error")
	}
}
