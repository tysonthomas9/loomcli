package service

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/ops"
)

// --- patchParamsToUpdateArgs tests ---

func TestPatchParamsToUpdateArgs_AgentState_Set(t *testing.T) {
	state := "running"
	params := &PatchIssueParams{
		IssueID:    "issue-1",
		AgentState: &state,
	}

	args := patchParamsToUpdateArgs(params)

	if args.AgentState == nil {
		t.Fatal("expected AgentState to be non-nil in UpdateArgs")
	}
	if *args.AgentState != "running" {
		t.Errorf("AgentState = %q, want %q", *args.AgentState, "running")
	}
}

func TestPatchParamsToUpdateArgs_AgentState_Nil(t *testing.T) {
	params := &PatchIssueParams{
		IssueID: "issue-2",
		// AgentState not set (nil)
	}

	args := patchParamsToUpdateArgs(params)

	if args.AgentState != nil {
		t.Errorf("expected AgentState to be nil, got %q", *args.AgentState)
	}
}

func TestPatchParamsToUpdateArgs_AgentState_WithOtherFields(t *testing.T) {
	state := "idle"
	status := "open"
	title := "Updated title"
	params := &PatchIssueParams{
		IssueID:    "issue-3",
		Title:      &title,
		Status:     &status,
		AgentState: &state,
	}

	args := patchParamsToUpdateArgs(params)

	if args.ID != "issue-3" {
		t.Errorf("ID = %q, want %q", args.ID, "issue-3")
	}
	if args.Title == nil || *args.Title != "Updated title" {
		t.Errorf("Title = %v, want %q", args.Title, "Updated title")
	}
	if args.Status == nil || *args.Status != "open" {
		t.Errorf("Status = %v, want %q", args.Status, "open")
	}
	if args.AgentState == nil || *args.AgentState != "idle" {
		t.Errorf("AgentState = %v, want %q", args.AgentState, "idle")
	}
}

func TestIssueBackendWireHelpersFullShapes(t *testing.T) {
	now := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	due := now.Add(time.Hour)
	deferUntil := now.Add(2 * time.Hour)
	closedAt := now.Add(3 * time.Hour)
	estimate := 45

	detail := &backend.IssueDetailData{
		IssueData: backend.IssueData{
			ID:          "ISSUE-1",
			Title:       "ship it",
			Status:      "in_progress",
			Priority:    2,
			IssueType:   "task",
			Assignee:    "agent",
			Owner:       "owner",
			Labels:      []string{"backend"},
			SourceRepo:  "repo-a",
			Parent:      "EPIC-1",
			Design:      "design notes",
			CreatedAt:   now,
			UpdatedAt:   now,
			DueAt:       &due,
			DeferUntil:  &deferUntil,
			CreatedBy:   "creator",
			ClosedAt:    &closedAt,
			CloseReason: "done",
		},
		Description:        "details",
		AcceptanceCriteria: "criteria",
		Notes:              "notes",
		ClosedBySession:    "sess-1",
		ExternalRef:        "GH-1",
		EstimatedMinutes:   &estimate,
		Dependencies: []backend.DependencyData{{
			DependsOnID: "DEP-1",
			Type:        "blocks",
			Title:       "dependency",
			Status:      "open",
			Priority:    1,
			IssueType:   "bug",
			CreatedAt:   now,
			CreatedBy:   "dep-author",
		}},
		Dependents: []backend.DependencyData{{
			DependsOnID: "CHILD-1",
			Type:        "related",
			Title:       "dependent",
			CreatedAt:   now,
		}},
		Comments: []backend.CommentData{{ID: 7, IssueID: "ISSUE-1", Author: "me", Text: "hello", CreatedAt: now}},
	}

	wire := issueDetailDataToWire(detail)
	checks := map[string]any{
		"issue_type":          "task",
		"assignee":            "agent",
		"owner":               "owner",
		"source_repo":         "repo-a",
		"repo":                "repo-a",
		"parent":              "EPIC-1",
		"design":              "design notes",
		"description":         "details",
		"acceptance_criteria": "criteria",
		"notes":               "notes",
		"created_by":          "creator",
		"close_reason":        "done",
		"closed_by_session":   "sess-1",
		"external_ref":        "GH-1",
		"estimated_minutes":   45,
	}
	for key, want := range checks {
		if got := wire[key]; got != want {
			t.Fatalf("wire[%s] = %#v, want %#v", key, got, want)
		}
	}
	if wire["due_at"] != &due || wire["defer_until"] != &deferUntil || wire["closed_at"] != &closedAt {
		t.Fatalf("time pointer fields were not preserved: %#v", wire)
	}
	if deps := wire["dependencies"].([]map[string]any); len(deps) != 1 || deps[0]["issue_type"] != "bug" || deps[0]["created_by"] != "dep-author" {
		t.Fatalf("dependencies wire = %#v", deps)
	}
	if comments := wire["comments"].([]map[string]any); len(comments) != 1 || comments[0]["text"] != "hello" {
		t.Fatalf("comments wire = %#v", comments)
	}

	closeWire := closeResultToWire(&backend.CloseResult{
		Closed:    &detail.IssueData,
		Unblocked: []backend.IssueData{{ID: "ISSUE-2", Title: "next", CreatedAt: now, UpdatedAt: now}},
	})
	if closeWire["closed"] == nil || len(closeWire["unblocked"].([]map[string]any)) != 1 {
		t.Fatalf("closeResultToWire = %#v", closeWire)
	}
}

func TestIssueBackendWireHelpersNilAndEmptyShapes(t *testing.T) {
	if issueDataToWire(nil) != nil {
		t.Fatal("issueDataToWire(nil) should be nil")
	}
	if issueDetailDataToWire(nil) != nil {
		t.Fatal("issueDetailDataToWire(nil) should be nil")
	}
	if closeResultToWire(nil) != nil {
		t.Fatal("closeResultToWire(nil) should be nil")
	}
	if commentDataToTypesComment(nil) != nil {
		t.Fatal("commentDataToTypesComment(nil) should be nil")
	}
	if got := depsToWire(nil); got == nil || len(got) != 0 {
		t.Fatalf("depsToWire(nil) = %#v, want empty slice", got)
	}
	wire := issueDetailDataToWire(&backend.IssueDetailData{IssueData: backend.IssueData{ID: "ISSUE-1"}})
	if labels := wire["labels"].([]string); labels == nil || len(labels) != 0 {
		t.Fatalf("labels = %#v, want empty slice", labels)
	}
	if closeWire := closeResultToWire(&backend.CloseResult{}); closeWire["closed"] != nil || len(closeWire["unblocked"].([]map[string]any)) != 0 {
		t.Fatalf("empty closeResultToWire = %#v", closeWire)
	}
	comment := commentDataToTypesComment(&backend.CommentData{ID: 9, IssueID: "ISSUE-1", Author: "ann", Text: "note"})
	if comment.ID != 9 || comment.IssueID != "ISSUE-1" || comment.Text != "note" {
		t.Fatalf("commentDataToTypesComment = %#v", comment)
	}
}

func TestWorkspaceDataCacheAdditionalBranches(t *testing.T) {
	ctx := context.Background()
	source := &ops.WorkspaceData{
		ID:     "WS",
		Name:   "Workspace",
		Repos:  []ops.WorkspaceRepo{{Name: "repo", Groups: []string{"backend"}}},
		Groups: []string{"backend"},
		Agents: []ops.WorkspaceAgentInfo{{
			Name:       "agent",
			Repos:      []string{"repo"},
			RepoGroups: []string{"backend"},
		}},
		Workspaces:     []ops.WorkspaceSummary{{ID: "WS", Name: "Workspace"}},
		WorkspaceOrder: []string{"WS"},
	}

	var calls int
	cache := newWorkspaceDataCache(0)
	first, err := cache.get(ctx, "WS", func(context.Context, string) (*ops.WorkspaceData, error) {
		calls++
		return source, nil
	})
	if err != nil {
		t.Fatalf("cache get: %v", err)
	}
	first.Repos[0].Groups[0] = "mutated"
	first.Agents[0].Repos[0] = "mutated"

	second, err := cache.get(ctx, "WS", func(context.Context, string) (*ops.WorkspaceData, error) {
		calls++
		return &ops.WorkspaceData{ID: "fresh"}, nil
	})
	if err != nil {
		t.Fatalf("cache get cached: %v", err)
	}
	if calls != 1 || second.Repos[0].Groups[0] != "backend" || second.Agents[0].Repos[0] != "repo" {
		t.Fatalf("cache clone/calls failed: calls=%d data=%+v", calls, second)
	}

	cache.invalidateAll()
	third, err := cache.get(ctx, "WS", func(context.Context, string) (*ops.WorkspaceData, error) {
		calls++
		return &ops.WorkspaceData{ID: "fresh"}, nil
	})
	if err != nil || third.ID != "fresh" || calls != 2 {
		t.Fatalf("cache after invalidate = %+v calls=%d err=%v", third, calls, err)
	}

	fromNilCache, err := (*workspaceDataCache)(nil).get(ctx, "direct", func(context.Context, string) (*ops.WorkspaceData, error) {
		return &ops.WorkspaceData{ID: "direct"}, nil
	})
	if err != nil || fromNilCache.ID != "direct" {
		t.Fatalf("nil cache get = %+v err=%v", fromNilCache, err)
	}
	if got, err := cache.get(ctx, "none", nil); err != nil || got != nil {
		t.Fatalf("nil load get = %+v err=%v", got, err)
	}
}

func TestWorkspaceDataCacheEntryLimitAndNilClone(t *testing.T) {
	if cloneWorkspaceData(nil) != nil {
		t.Fatal("cloneWorkspaceData(nil) should be nil")
	}
	cache := newWorkspaceDataCache(time.Minute)
	for i := 0; i < maxWorkspaceDataCacheEntries; i++ {
		cache.entries[string(rune('a'+(i%26)))+time.Duration(i).String()] = &workspaceDataCacheEntry{}
	}
	entry := cache.cacheEntry("overflow")
	if entry == nil {
		t.Fatal("overflow cacheEntry returned nil")
	}
	if _, ok := cache.entries["overflow"]; ok {
		t.Fatal("overflow cacheEntry should not be stored when cache is full")
	}
}
