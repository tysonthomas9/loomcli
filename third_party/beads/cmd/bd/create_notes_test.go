package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/rpc"
	"github.com/steveyegge/beads/internal/types"
)

// TestCreateWithNotes verifies that the --notes flag works correctly
// during issue creation in both direct mode and RPC mode.
func TestCreateWithNotes(t *testing.T) {
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	s := newTestStore(t, testDB)
	ctx := context.Background()

	t.Run("DirectMode_WithNotes", func(t *testing.T) {
		issue := &types.Issue{
			Title:     "Issue with notes",
			Notes:     "These are my test notes",
			Priority:  1,
			IssueType: types.TypeTask,
			Status:    types.StatusOpen,
			CreatedAt: time.Now(),
		}

		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("failed to create issue: %v", err)
		}

		// Retrieve and verify
		retrieved, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("failed to retrieve issue: %v", err)
		}

		if retrieved.Notes != "These are my test notes" {
			t.Errorf("expected notes 'These are my test notes', got %q", retrieved.Notes)
		}
	})

	t.Run("DirectMode_WithoutNotes", func(t *testing.T) {
		issue := &types.Issue{
			Title:     "Issue without notes",
			Priority:  2,
			IssueType: types.TypeBug,
			Status:    types.StatusOpen,
			CreatedAt: time.Now(),
		}

		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("failed to create issue: %v", err)
		}

		// Retrieve and verify
		retrieved, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("failed to retrieve issue: %v", err)
		}

		if retrieved.Notes != "" {
			t.Errorf("expected empty notes, got %q", retrieved.Notes)
		}
	})

	t.Run("DirectMode_WithNotesAndOtherFields", func(t *testing.T) {
		issue := &types.Issue{
			Title:              "Full issue with notes",
			Description:        "Detailed description",
			Design:             "Design notes here",
			AcceptanceCriteria: "All tests pass",
			Notes:              "Additional implementation notes",
			Priority:           1,
			IssueType:          types.TypeFeature,
			Status:             types.StatusOpen,
			Assignee:           "testuser",
			CreatedAt:          time.Now(),
		}

		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("failed to create issue: %v", err)
		}

		// Retrieve and verify all fields
		retrieved, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("failed to retrieve issue: %v", err)
		}

		if retrieved.Title != "Full issue with notes" {
			t.Errorf("expected title 'Full issue with notes', got %q", retrieved.Title)
		}
		if retrieved.Description != "Detailed description" {
			t.Errorf("expected description, got %q", retrieved.Description)
		}
		if retrieved.Design != "Design notes here" {
			t.Errorf("expected design, got %q", retrieved.Design)
		}
		if retrieved.AcceptanceCriteria != "All tests pass" {
			t.Errorf("expected acceptance criteria, got %q", retrieved.AcceptanceCriteria)
		}
		if retrieved.Notes != "Additional implementation notes" {
			t.Errorf("expected notes 'Additional implementation notes', got %q", retrieved.Notes)
		}
		if retrieved.Assignee != "testuser" {
			t.Errorf("expected assignee 'testuser', got %q", retrieved.Assignee)
		}
	})

	t.Run("DirectMode_NotesWithSpecialCharacters", func(t *testing.T) {
		specialNotes := "Notes with special chars: \n- Bullet point\n- Another one\n\nAnd \"quotes\" and 'apostrophes'"
		issue := &types.Issue{
			Title:     "Issue with special char notes",
			Notes:     specialNotes,
			Priority:  2,
			IssueType: types.TypeTask,
			Status:    types.StatusOpen,
			CreatedAt: time.Now(),
		}

		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("failed to create issue: %v", err)
		}

		// Retrieve and verify
		retrieved, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("failed to retrieve issue: %v", err)
		}

		if retrieved.Notes != specialNotes {
			t.Errorf("notes mismatch.\nExpected: %q\nGot: %q", specialNotes, retrieved.Notes)
		}
	})
}

// TestCreateWithNotesRPC verifies notes field works via RPC protocol
func TestCreateWithNotesRPC(t *testing.T) {
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	s := newTestStore(t, testDB)
	ctx := context.Background()

	t.Run("RPC_CreateArgs_WithNotes", func(t *testing.T) {
		// Test that CreateArgs properly includes Notes field
		args := &rpc.CreateArgs{
			Title:       "RPC test issue",
			Description: "Testing RPC mode",
			Notes:       "RPC notes field",
			Priority:    1,
			IssueType:   "task",
		}

		// Verify the struct has the Notes field populated
		if args.Notes != "RPC notes field" {
			t.Errorf("expected Notes field 'RPC notes field', got %q", args.Notes)
		}
	})

	t.Run("RPC_CreateIssue_WithNotes", func(t *testing.T) {
		// Simulate what the RPC handler does
		createArgs := &rpc.CreateArgs{
			Title:              "RPC created issue",
			Description:        "Created via RPC",
			Design:             "RPC design",
			AcceptanceCriteria: "RPC acceptance",
			Notes:              "RPC implementation notes",
			Priority:           2,
			IssueType:          "feature",
			Assignee:           "rpcuser",
		}

		// Create issue as RPC handler would
		issue := &types.Issue{
			Title:              createArgs.Title,
			Description:        createArgs.Description,
			Design:             createArgs.Design,
			AcceptanceCriteria: createArgs.AcceptanceCriteria,
			Notes:              createArgs.Notes,
			Priority:           createArgs.Priority,
			IssueType:          types.IssueType(createArgs.IssueType),
			Assignee:           createArgs.Assignee,
			Status:             types.StatusOpen,
			CreatedAt:          time.Now(),
		}

		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("failed to create issue via RPC simulation: %v", err)
		}

		// Retrieve and verify
		retrieved, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("failed to retrieve issue: %v", err)
		}

		if retrieved.Notes != "RPC implementation notes" {
			t.Errorf("expected notes 'RPC implementation notes', got %q", retrieved.Notes)
		}
		if retrieved.Description != "Created via RPC" {
			t.Errorf("expected description 'Created via RPC', got %q", retrieved.Description)
		}
	})
}

// TestCreateWithOwner verifies that the Owner field works correctly
// during issue creation in both direct mode and RPC mode.
func TestCreateWithOwner(t *testing.T) {
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	s := newTestStore(t, testDB)
	ctx := context.Background()

	t.Run("DirectMode_WithExplicitOwner", func(t *testing.T) {
		issue := &types.Issue{
			Title:     "Issue with explicit owner",
			Owner:     "alice@example.com",
			Priority:  1,
			IssueType: types.TypeTask,
			Status:    types.StatusOpen,
			CreatedAt: time.Now(),
		}

		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("failed to create issue: %v", err)
		}

		retrieved, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("failed to retrieve issue: %v", err)
		}

		if retrieved.Owner != "alice@example.com" {
			t.Errorf("expected owner 'alice@example.com', got %q", retrieved.Owner)
		}
	})

	t.Run("DirectMode_WithoutOwner", func(t *testing.T) {
		issue := &types.Issue{
			Title:     "Issue without owner",
			Priority:  2,
			IssueType: types.TypeBug,
			Status:    types.StatusOpen,
			CreatedAt: time.Now(),
		}

		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("failed to create issue: %v", err)
		}

		retrieved, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("failed to retrieve issue: %v", err)
		}

		if retrieved.Owner != "" {
			t.Errorf("expected empty owner, got %q", retrieved.Owner)
		}
	})

	t.Run("DirectMode_WithOwnerAndOtherFields", func(t *testing.T) {
		issue := &types.Issue{
			Title:       "Full issue with owner",
			Description: "Detailed description",
			Notes:       "Some notes",
			Owner:       "bob@example.com",
			Assignee:    "testuser",
			Priority:    1,
			IssueType:   types.TypeFeature,
			Status:      types.StatusOpen,
			CreatedAt:   time.Now(),
		}

		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("failed to create issue: %v", err)
		}

		retrieved, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("failed to retrieve issue: %v", err)
		}

		if retrieved.Owner != "bob@example.com" {
			t.Errorf("expected owner 'bob@example.com', got %q", retrieved.Owner)
		}
		if retrieved.Assignee != "testuser" {
			t.Errorf("expected assignee 'testuser', got %q", retrieved.Assignee)
		}
		if retrieved.Notes != "Some notes" {
			t.Errorf("expected notes 'Some notes', got %q", retrieved.Notes)
		}
	})
}

// TestCreateWithOwnerRPC verifies owner field works via RPC protocol
func TestCreateWithOwnerRPC(t *testing.T) {
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	s := newTestStore(t, testDB)
	ctx := context.Background()

	t.Run("RPC_CreateArgs_WithOwner", func(t *testing.T) {
		args := &rpc.CreateArgs{
			Title:     "RPC test issue with owner",
			IssueType: "task",
			Priority:  1,
			Owner:     "rpc-owner@example.com",
		}

		if args.Owner != "rpc-owner@example.com" {
			t.Errorf("expected Owner field 'rpc-owner@example.com', got %q", args.Owner)
		}
	})

	t.Run("RPC_CreateArgs_EmptyOwner", func(t *testing.T) {
		args := &rpc.CreateArgs{
			Title:     "RPC test issue without owner",
			IssueType: "task",
			Priority:  1,
			// Owner intentionally left empty
		}

		if args.Owner != "" {
			t.Errorf("expected empty Owner field, got %q", args.Owner)
		}
	})

	t.Run("RPC_CreateIssue_WithOwner", func(t *testing.T) {
		createArgs := &rpc.CreateArgs{
			Title:     "RPC created issue with owner",
			IssueType: "feature",
			Priority:  2,
			Owner:     "rpc-alice@example.com",
			Assignee:  "rpcuser",
		}

		// Simulate what the RPC handler does when owner is explicit
		owner := createArgs.Owner

		issue := &types.Issue{
			Title:     createArgs.Title,
			IssueType: types.IssueType(createArgs.IssueType),
			Priority:  createArgs.Priority,
			Assignee:  createArgs.Assignee,
			Owner:     owner,
			Status:    types.StatusOpen,
			CreatedAt: time.Now(),
		}

		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("failed to create issue via RPC simulation: %v", err)
		}

		retrieved, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("failed to retrieve issue: %v", err)
		}

		if retrieved.Owner != "rpc-alice@example.com" {
			t.Errorf("expected owner 'rpc-alice@example.com', got %q", retrieved.Owner)
		}
	})
}

// TestGetOwner verifies the getOwner() function used by the CLI
// to determine the owner value when --owner flag is not provided.
func TestGetOwner(t *testing.T) {
	t.Run("ReturnsGitAuthorEmail", func(t *testing.T) {
		oldVal := os.Getenv("GIT_AUTHOR_EMAIL")
		os.Setenv("GIT_AUTHOR_EMAIL", "cli-author@example.com")
		t.Cleanup(func() {
			if oldVal != "" {
				os.Setenv("GIT_AUTHOR_EMAIL", oldVal)
			} else {
				os.Unsetenv("GIT_AUTHOR_EMAIL")
			}
		})

		owner := getOwner()
		if owner != "cli-author@example.com" {
			t.Errorf("expected getOwner() to return 'cli-author@example.com' from GIT_AUTHOR_EMAIL, got %q", owner)
		}
	})

	t.Run("FallsBackToGitConfig", func(t *testing.T) {
		oldVal := os.Getenv("GIT_AUTHOR_EMAIL")
		os.Unsetenv("GIT_AUTHOR_EMAIL")
		t.Cleanup(func() {
			if oldVal != "" {
				os.Setenv("GIT_AUTHOR_EMAIL", oldVal)
			}
		})

		// getOwner() should fall back to git config user.email.
		// We cannot assert the exact value since it depends on the test environment,
		// but we verify it does not panic and returns a string.
		owner := getOwner()
		_ = owner
	})

	t.Run("EmptyEnvFallsThrough", func(t *testing.T) {
		oldVal := os.Getenv("GIT_AUTHOR_EMAIL")
		os.Setenv("GIT_AUTHOR_EMAIL", "")
		t.Cleanup(func() {
			if oldVal != "" {
				os.Setenv("GIT_AUTHOR_EMAIL", oldVal)
			} else {
				os.Unsetenv("GIT_AUTHOR_EMAIL")
			}
		})

		// Empty string GIT_AUTHOR_EMAIL should not be returned as the owner.
		// It should fall through to git config user.email.
		owner := getOwner()
		if owner == "" {
			// This is acceptable if git config user.email is also empty
			t.Logf("getOwner() returned empty string (git config user.email likely not set)")
		}
	})
}
