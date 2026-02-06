package sqlite

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestClaimIssue_Success(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create an unclaimed issue
	issue := &types.Issue{
		Title:     "Test issue",
		Status:    types.StatusOpen,
		IssueType: types.TypeTask,
		Assignee:  "", // unclaimed
	}
	if err := store.CreateIssue(ctx, issue, "test-user"); err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	// Claim it
	claimed, err := store.ClaimIssue(ctx, issue.ID, "agent-1")
	if err != nil {
		t.Fatalf("ClaimIssue failed: %v", err)
	}
	if !claimed {
		t.Error("Expected claim to succeed")
	}

	// Verify the issue is now claimed
	updated, err := store.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue failed: %v", err)
	}
	if updated.Assignee != "agent-1" {
		t.Errorf("Expected assignee 'agent-1', got '%s'", updated.Assignee)
	}
	if updated.Status != types.StatusInProgress {
		t.Errorf("Expected status 'in_progress', got '%s'", updated.Status)
	}
}

func TestClaimIssue_AlreadyClaimed(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create an already claimed issue
	issue := &types.Issue{
		Title:     "Test issue",
		Status:    types.StatusInProgress,
		IssueType: types.TypeTask,
		Assignee:  "agent-1", // already claimed
	}
	if err := store.CreateIssue(ctx, issue, "test-user"); err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	// Try to claim it with a different agent
	claimed, err := store.ClaimIssue(ctx, issue.ID, "agent-2")
	if err != nil {
		t.Fatalf("ClaimIssue should not error on already claimed: %v", err)
	}
	if claimed {
		t.Error("Expected claim to fail on already claimed issue")
	}

	// Verify the original assignee is unchanged
	updated, err := store.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue failed: %v", err)
	}
	if updated.Assignee != "agent-1" {
		t.Errorf("Assignee should still be 'agent-1', got '%s'", updated.Assignee)
	}
}

func TestClaimIssue_NotFound(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Try to claim a non-existent issue
	claimed, err := store.ClaimIssue(ctx, "bd-nonexistent", "agent-1")
	if err == nil {
		t.Error("Expected error for non-existent issue")
	}
	if claimed {
		t.Error("Claim should not succeed for non-existent issue")
	}
}

func TestClaimIssue_ConcurrentRace(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create an unclaimed issue
	issue := &types.Issue{
		Title:     "Race condition test",
		Status:    types.StatusOpen,
		IssueType: types.TypeTask,
		Assignee:  "",
	}
	if err := store.CreateIssue(ctx, issue, "test-user"); err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	// Launch multiple goroutines trying to claim simultaneously
	const numAgents = 10
	var wg sync.WaitGroup
	var successCount atomic.Int32
	winners := make(chan string, numAgents)

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(agentNum int) {
			defer wg.Done()
			agentName := "agent-" + string(rune('A'+agentNum))
			claimed, err := store.ClaimIssue(ctx, issue.ID, agentName)
			if err != nil {
				t.Errorf("ClaimIssue returned error: %v", err)
				return
			}
			if claimed {
				successCount.Add(1)
				winners <- agentName
			}
		}(i)
	}

	wg.Wait()
	close(winners)

	// Exactly one agent should have succeeded
	if successCount.Load() != 1 {
		t.Errorf("Expected exactly 1 successful claim, got %d", successCount.Load())
	}

	// Collect the winner
	var winner string
	for w := range winners {
		winner = w
	}

	// Verify the winner is the assignee
	updated, err := store.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue failed: %v", err)
	}
	if updated.Assignee != winner {
		t.Errorf("Expected assignee '%s', got '%s'", winner, updated.Assignee)
	}
	if updated.Status != types.StatusInProgress {
		t.Errorf("Expected status 'in_progress', got '%s'", updated.Status)
	}
}

func TestClaimIssue_EmptyAssigneeString(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create issue with empty string assignee (not NULL)
	issue := &types.Issue{
		Title:     "Empty assignee test",
		Status:    types.StatusOpen,
		IssueType: types.TypeTask,
		Assignee:  "",
	}
	if err := store.CreateIssue(ctx, issue, "test-user"); err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	// Claim should succeed
	claimed, err := store.ClaimIssue(ctx, issue.ID, "agent-1")
	if err != nil {
		t.Fatalf("ClaimIssue failed: %v", err)
	}
	if !claimed {
		t.Error("Expected claim to succeed on empty assignee")
	}
}

func TestClaimIssue_MultipleConcurrentIssues(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create multiple unclaimed issues
	const numIssues = 5
	const numAgents = 10
	issues := make([]*types.Issue, numIssues)

	for i := 0; i < numIssues; i++ {
		issues[i] = &types.Issue{
			Title:     "Issue " + string(rune('A'+i)),
			Status:    types.StatusOpen,
			IssueType: types.TypeTask,
			Assignee:  "",
		}
		if err := store.CreateIssue(ctx, issues[i], "test-user"); err != nil {
			t.Fatalf("CreateIssue failed: %v", err)
		}
	}

	// Each agent tries to claim all issues
	// Each issue should end up with exactly one assignee
	var wg sync.WaitGroup
	claimResults := make(map[string][]string) // issueID -> list of agents that successfully claimed
	var mu sync.Mutex

	for agentNum := 0; agentNum < numAgents; agentNum++ {
		wg.Add(1)
		go func(agentNum int) {
			defer wg.Done()
			agentName := "agent-" + string(rune('A'+agentNum))
			for _, issue := range issues {
				claimed, err := store.ClaimIssue(ctx, issue.ID, agentName)
				if err != nil {
					t.Errorf("ClaimIssue returned error: %v", err)
					continue
				}
				if claimed {
					mu.Lock()
					claimResults[issue.ID] = append(claimResults[issue.ID], agentName)
					mu.Unlock()
				}
			}
		}(agentNum)
	}

	wg.Wait()

	// Each issue should have exactly one winner
	for _, issue := range issues {
		winners := claimResults[issue.ID]
		if len(winners) != 1 {
			t.Errorf("Issue %s should have exactly 1 winner, got %d: %v", issue.ID, len(winners), winners)
		}
	}
}
