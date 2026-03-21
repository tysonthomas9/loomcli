package rpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestSourceReposFilter_List verifies that handleList filters by SourceRepos correctly.
func TestSourceReposFilter_List(t *testing.T) {
	_, client, store, cleanup := setupTestServerWithStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create issues with different source_repo values
	repos := []struct {
		title      string
		sourceRepo string
	}{
		{"Issue from repo-a", "repo-a"},
		{"Issue from repo-b", "repo-b"},
		{"Issue from repo-a again", "repo-a"},
		{"Issue from repo-c", "repo-c"},
		{"Issue with no repo", ""},
	}

	for _, r := range repos {
		issue := &types.Issue{
			Title:      r.title,
			IssueType:  "task",
			Status:     types.StatusOpen,
			Priority:   2,
			SourceRepo: r.sourceRepo,
		}
		if err := store.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("Failed to create issue: %v", err)
		}
	}

	// Test 1: Filter to repo-a only
	resp, err := client.List(&ListArgs{
		SourceRepos: []string{"repo-a"},
	})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("List failed: %s", resp.Error)
	}

	var issues []*types.IssueWithCounts
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(issues) != 2 {
		t.Errorf("Expected 2 issues for repo-a, got %d", len(issues))
	}
	for _, iss := range issues {
		if iss.Issue.SourceRepo != "repo-a" {
			t.Errorf("Expected source_repo=repo-a, got %q", iss.Issue.SourceRepo)
		}
	}

	// Test 2: Filter to multiple repos
	resp, err = client.List(&ListArgs{
		SourceRepos: []string{"repo-b", "repo-c"},
	})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("List failed: %s", resp.Error)
	}

	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(issues) != 2 {
		t.Errorf("Expected 2 issues for repo-b+repo-c, got %d", len(issues))
	}

	// Test 3: No SourceRepos filter returns all issues
	resp, err = client.List(&ListArgs{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("List failed: %s", resp.Error)
	}

	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(issues) != 5 {
		t.Errorf("Expected 5 issues with no filter, got %d", len(issues))
	}
}

// TestSourceReposFilter_Ready verifies that handleReady filters by SourceRepos correctly.
func TestSourceReposFilter_Ready(t *testing.T) {
	_, client, store, cleanup := setupTestServerWithStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create issues with different source_repo values (all open, no blockers)
	repos := []struct {
		title      string
		sourceRepo string
	}{
		{"Ready from repo-a", "repo-a"},
		{"Ready from repo-b", "repo-b"},
		{"Ready from repo-a v2", "repo-a"},
	}

	for _, r := range repos {
		issue := &types.Issue{
			Title:      r.title,
			IssueType:  "task",
			Status:     types.StatusOpen,
			Priority:   2,
			SourceRepo: r.sourceRepo,
		}
		if err := store.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("Failed to create issue: %v", err)
		}
	}

	// Test: Filter ready work to repo-b only
	resp, err := client.Ready(&ReadyArgs{
		SourceRepos: []string{"repo-b"},
	})
	if err != nil {
		t.Fatalf("Ready failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Ready failed: %s", resp.Error)
	}

	var issues []*types.Issue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(issues) != 1 {
		t.Errorf("Expected 1 ready issue for repo-b, got %d", len(issues))
	}
	if len(issues) > 0 && issues[0].SourceRepo != "repo-b" {
		t.Errorf("Expected source_repo=repo-b, got %q", issues[0].SourceRepo)
	}

	// Test: No filter returns all
	resp, err = client.Ready(&ReadyArgs{})
	if err != nil {
		t.Fatalf("Ready failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Ready failed: %s", resp.Error)
	}

	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(issues) != 3 {
		t.Errorf("Expected 3 ready issues with no filter, got %d", len(issues))
	}
}

// TestSourceReposFilter_Count verifies that handleCount filters by SourceRepos correctly.
func TestSourceReposFilter_Count(t *testing.T) {
	_, client, store, cleanup := setupTestServerWithStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create issues with different source_repo values
	for _, r := range []struct{ title, repo string }{
		{"Count repo-a 1", "repo-a"},
		{"Count repo-a 2", "repo-a"},
		{"Count repo-b 1", "repo-b"},
	} {
		issue := &types.Issue{
			Title:      r.title,
			IssueType:  "task",
			Status:     types.StatusOpen,
			Priority:   2,
			SourceRepo: r.repo,
		}
		if err := store.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("Failed to create issue: %v", err)
		}
	}

	// Count with SourceRepos filter
	resp, err := client.Count(&CountArgs{
		SourceRepos: []string{"repo-a"},
	})
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Count failed: %s", resp.Error)
	}

	var result struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if result.Count != 2 {
		t.Errorf("Expected count=2 for repo-a, got %d", result.Count)
	}
}

// TestSourceReposFilter_GetGraphData verifies that handleGetGraphData filters by SourceRepos correctly.
func TestSourceReposFilter_GetGraphData(t *testing.T) {
	_, client, store, cleanup := setupTestServerWithStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create issues with different source_repo values
	repos := []struct {
		title      string
		sourceRepo string
	}{
		{"Graph issue from repo-a", "repo-a"},
		{"Graph issue from repo-b", "repo-b"},
		{"Graph issue from repo-a again", "repo-a"},
		{"Graph issue from repo-c", "repo-c"},
		{"Graph issue with no repo", ""},
	}

	for _, r := range repos {
		issue := &types.Issue{
			Title:      r.title,
			IssueType:  "task",
			Status:     types.StatusOpen,
			Priority:   2,
			SourceRepo: r.sourceRepo,
		}
		if err := store.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("Failed to create issue: %v", err)
		}
	}

	// Test 1: Filter to repo-a only
	resp, err := client.GetGraphData(&GetGraphDataArgs{
		SourceRepos: []string{"repo-a"},
	})
	if err != nil {
		t.Fatalf("GetGraphData failed: %v", err)
	}
	if len(resp.Issues) != 2 {
		t.Errorf("Expected 2 issues for repo-a, got %d", len(resp.Issues))
	}

	// Test 2: Filter to multiple repos
	resp, err = client.GetGraphData(&GetGraphDataArgs{
		SourceRepos: []string{"repo-b", "repo-c"},
	})
	if err != nil {
		t.Fatalf("GetGraphData failed: %v", err)
	}
	if len(resp.Issues) != 2 {
		t.Errorf("Expected 2 issues for repo-b+repo-c, got %d", len(resp.Issues))
	}

	// Test 3: No SourceRepos filter returns all issues
	resp, err = client.GetGraphData(&GetGraphDataArgs{})
	if err != nil {
		t.Fatalf("GetGraphData failed: %v", err)
	}
	if len(resp.Issues) != 5 {
		t.Errorf("Expected 5 issues with no filter, got %d", len(resp.Issues))
	}

	// Test 4: Filter with nonexistent repo returns no issues
	resp, err = client.GetGraphData(&GetGraphDataArgs{
		SourceRepos: []string{"nonexistent-repo"},
	})
	if err != nil {
		t.Fatalf("GetGraphData failed: %v", err)
	}
	if len(resp.Issues) != 0 {
		t.Errorf("Expected 0 issues for nonexistent repo, got %d", len(resp.Issues))
	}
}

// TestSourceReposFilter_SQLInjection verifies parameterized queries prevent SQL injection.
func TestSourceReposFilter_SQLInjection(t *testing.T) {
	_, client, store, cleanup := setupTestServerWithStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create a real issue
	issue := &types.Issue{
		Title:      "Safe issue",
		IssueType:  "task",
		Status:     types.StatusOpen,
		Priority:   2,
		SourceRepo: "safe-repo",
	}
	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	// Try SQL injection via SourceRepos - should return 0 results, not error
	resp, err := client.List(&ListArgs{
		SourceRepos: []string{"'; DROP TABLE issues; --"},
	})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("List failed (should not): %s", resp.Error)
	}

	// Verify the table still exists and the safe issue is still there
	resp, err = client.List(&ListArgs{
		SourceRepos: []string{"safe-repo"},
	})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("List failed after injection attempt: %s", resp.Error)
	}

	var issues []*types.IssueWithCounts
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(issues) != 1 {
		t.Errorf("Expected 1 issue after injection attempt, got %d", len(issues))
	}
}
