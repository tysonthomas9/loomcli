package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

func TestGetDependenciesForIssues_Basic(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Create 3 issues with dependencies between them
	issue1 := &types.Issue{Title: "Issue 1", Priority: 2, Status: types.StatusOpen, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, issue1, "test"); err != nil {
		t.Fatalf("Failed to create issue 1: %v", err)
	}
	issue2 := &types.Issue{Title: "Issue 2", Priority: 2, Status: types.StatusOpen, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, issue2, "test"); err != nil {
		t.Fatalf("Failed to create issue 2: %v", err)
	}
	issue3 := &types.Issue{Title: "Issue 3", Priority: 2, Status: types.StatusOpen, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, issue3, "test"); err != nil {
		t.Fatalf("Failed to create issue 3: %v", err)
	}

	// Issue 2 depends on Issue 1
	dep1 := &types.Dependency{IssueID: issue2.ID, DependsOnID: issue1.ID, Type: types.DepBlocks}
	if err := store.AddDependency(ctx, dep1, "test"); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}
	// Issue 3 depends on Issue 2
	dep2 := &types.Dependency{IssueID: issue3.ID, DependsOnID: issue2.ID, Type: types.DepBlocks}
	if err := store.AddDependency(ctx, dep2, "test"); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Get dependencies for all 3 issues
	depsMap, err := store.GetDependenciesForIssues(ctx, []string{issue1.ID, issue2.ID, issue3.ID})
	if err != nil {
		t.Fatalf("GetDependenciesForIssues failed: %v", err)
	}

	// Verify results
	if len(depsMap[issue1.ID]) != 0 {
		t.Errorf("Expected 0 dependencies for issue 1, got %d", len(depsMap[issue1.ID]))
	}
	if len(depsMap[issue2.ID]) != 1 {
		t.Errorf("Expected 1 dependency for issue 2, got %d", len(depsMap[issue2.ID]))
	}
	if len(depsMap[issue3.ID]) != 1 {
		t.Errorf("Expected 1 dependency for issue 3, got %d", len(depsMap[issue3.ID]))
	}

	// Verify dependency details
	if depsMap[issue2.ID][0].DependsOnID != issue1.ID {
		t.Errorf("Expected issue 2 to depend on issue 1, got %s", depsMap[issue2.ID][0].DependsOnID)
	}
	if depsMap[issue3.ID][0].DependsOnID != issue2.ID {
		t.Errorf("Expected issue 3 to depend on issue 2, got %s", depsMap[issue3.ID][0].DependsOnID)
	}
}

func TestGetDependenciesForIssues_EmptyList(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	depsMap, err := store.GetDependenciesForIssues(ctx, []string{})
	if err != nil {
		t.Fatalf("GetDependenciesForIssues failed: %v", err)
	}

	if len(depsMap) != 0 {
		t.Errorf("Expected empty map, got %d entries", len(depsMap))
	}
}

func TestGetDependenciesForIssues_NoDependencies(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Create issues without dependencies
	issue1 := &types.Issue{Title: "Issue 1", Priority: 2, Status: types.StatusOpen, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, issue1, "test"); err != nil {
		t.Fatalf("Failed to create issue 1: %v", err)
	}
	issue2 := &types.Issue{Title: "Issue 2", Priority: 2, Status: types.StatusOpen, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, issue2, "test"); err != nil {
		t.Fatalf("Failed to create issue 2: %v", err)
	}

	depsMap, err := store.GetDependenciesForIssues(ctx, []string{issue1.ID, issue2.ID})
	if err != nil {
		t.Fatalf("GetDependenciesForIssues failed: %v", err)
	}

	// Should have entries for both issues with empty slices (not nil)
	if depsMap[issue1.ID] == nil {
		t.Error("Expected empty slice for issue 1, got nil")
	}
	if depsMap[issue2.ID] == nil {
		t.Error("Expected empty slice for issue 2, got nil")
	}
	if len(depsMap[issue1.ID]) != 0 {
		t.Errorf("Expected 0 dependencies for issue 1, got %d", len(depsMap[issue1.ID]))
	}
	if len(depsMap[issue2.ID]) != 0 {
		t.Errorf("Expected 0 dependencies for issue 2, got %d", len(depsMap[issue2.ID]))
	}
}

func TestGetDependenciesForIssues_MixedDependencyTypes(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	issue1 := &types.Issue{Title: "Issue 1", Priority: 2, Status: types.StatusOpen, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, issue1, "test"); err != nil {
		t.Fatalf("Failed to create issue 1: %v", err)
	}
	issue2 := &types.Issue{Title: "Issue 2", Priority: 2, Status: types.StatusOpen, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, issue2, "test"); err != nil {
		t.Fatalf("Failed to create issue 2: %v", err)
	}
	issue3 := &types.Issue{Title: "Issue 3", Priority: 2, Status: types.StatusOpen, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, issue3, "test"); err != nil {
		t.Fatalf("Failed to create issue 3: %v", err)
	}

	// Add different types of dependencies
	dep1 := &types.Dependency{IssueID: issue1.ID, DependsOnID: issue2.ID, Type: types.DepBlocks}
	if err := store.AddDependency(ctx, dep1, "test"); err != nil {
		t.Fatalf("Failed to add blocks dependency: %v", err)
	}
	dep2 := &types.Dependency{IssueID: issue1.ID, DependsOnID: issue3.ID, Type: types.DepRelated}
	if err := store.AddDependency(ctx, dep2, "test"); err != nil {
		t.Fatalf("Failed to add related dependency: %v", err)
	}

	depsMap, err := store.GetDependenciesForIssues(ctx, []string{issue1.ID})
	if err != nil {
		t.Fatalf("GetDependenciesForIssues failed: %v", err)
	}

	if len(depsMap[issue1.ID]) != 2 {
		t.Errorf("Expected 2 dependencies for issue 1, got %d", len(depsMap[issue1.ID]))
	}

	// Verify both dependency types are returned
	typesSeen := make(map[types.DependencyType]bool)
	for _, dep := range depsMap[issue1.ID] {
		typesSeen[dep.Type] = true
	}
	if !typesSeen[types.DepBlocks] {
		t.Error("Expected blocks dependency to be returned")
	}
	if !typesSeen[types.DepRelated] {
		t.Error("Expected related dependency to be returned")
	}
}

func TestGetDependenciesForIssues_PartialIssues(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Create only one issue
	issue1 := &types.Issue{Title: "Issue 1", Priority: 2, Status: types.StatusOpen, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, issue1, "test"); err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	// Request deps for mix of existing and non-existing IDs
	depsMap, err := store.GetDependenciesForIssues(ctx, []string{issue1.ID, "nonexistent-1", "nonexistent-2"})
	if err != nil {
		t.Fatalf("GetDependenciesForIssues failed: %v", err)
	}

	// Should have entries for all requested IDs (even non-existent ones)
	if depsMap[issue1.ID] == nil {
		t.Error("Expected entry for issue 1")
	}
	if depsMap["nonexistent-1"] == nil {
		t.Error("Expected entry for nonexistent-1 (empty slice)")
	}
	if depsMap["nonexistent-2"] == nil {
		t.Error("Expected entry for nonexistent-2 (empty slice)")
	}
}

func TestScanIssuesIncludesDependencies(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Create a closed blocker issue and an issue that depends on it
	// The dependent issue will be "ready" (not blocked) because the blocker is closed
	now := time.Now()
	blocker := &types.Issue{
		Title:     "Closed Blocker",
		Priority:  1,
		Status:    types.StatusClosed,
		IssueType: types.TypeTask,
		ClosedAt:  &now,
	}
	if err := store.CreateIssue(ctx, blocker, "test"); err != nil {
		t.Fatalf("Failed to create blocker issue: %v", err)
	}

	readyIssue := &types.Issue{Title: "Ready Issue With Deps", Priority: 2, Status: types.StatusOpen, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, readyIssue, "test"); err != nil {
		t.Fatalf("Failed to create ready issue: %v", err)
	}

	// Ready issue depends on closed blocker
	dep := &types.Dependency{IssueID: readyIssue.ID, DependsOnID: blocker.ID, Type: types.DepBlocks}
	if err := store.AddDependency(ctx, dep, "test"); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Use GetReadyWork which calls scanIssues internally
	filter := types.WorkFilter{Limit: 100}
	readyIssues, err := store.GetReadyWork(ctx, filter)
	if err != nil {
		t.Fatalf("GetReadyWork failed: %v", err)
	}

	var foundReady *types.Issue
	for _, issue := range readyIssues {
		if issue.ID == readyIssue.ID {
			foundReady = issue
			break
		}
	}

	if foundReady == nil {
		t.Fatal("Could not find ready issue in results")
	}

	if foundReady.Dependencies == nil {
		t.Error("Expected Dependencies to be populated, got nil")
	} else if len(foundReady.Dependencies) != 1 {
		t.Errorf("Expected 1 dependency, got %d", len(foundReady.Dependencies))
	} else {
		dep := foundReady.Dependencies[0]
		if dep.DependsOnID != blocker.ID {
			t.Errorf("Expected dependency to point to blocker issue, got %s", dep.DependsOnID)
		}
		if dep.Type != types.DepBlocks {
			t.Errorf("Expected blocks dependency type, got %s", dep.Type)
		}
	}
}
