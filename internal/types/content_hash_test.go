package types

import (
	"testing"
	"time"
)

func TestComputeContentHash_Determinism(t *testing.T) {
	issue := createBaseIssue()

	hash1 := issue.ComputeContentHash()
	hash2 := issue.ComputeContentHash()

	if hash1 != hash2 {
		t.Errorf("Same issue should produce same hash, got %s and %s", hash1, hash2)
	}
}

func TestComputeContentHash_FieldDifferences(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*Issue)
	}{
		{
			name: "EstimatedMinutes difference",
			modify: func(i *Issue) {
				minutes := 60
				i.EstimatedMinutes = &minutes
			},
		},
		{
			name: "DueAt difference",
			modify: func(i *Issue) {
				due := time.Now().Add(24 * time.Hour)
				i.DueAt = &due
			},
		},
		{
			name: "DeferUntil difference",
			modify: func(i *Issue) {
				defer_ := time.Now().Add(48 * time.Hour)
				i.DeferUntil = &defer_
			},
		},
		{
			name: "CloseReason difference",
			modify: func(i *Issue) {
				i.CloseReason = "completed successfully"
			},
		},
		{
			name: "ClosedBySession difference",
			modify: func(i *Issue) {
				i.ClosedBySession = "session-123"
			},
		},
		{
			name: "Labels difference",
			modify: func(i *Issue) {
				i.Labels = []string{"bug", "critical"}
			},
		},
		{
			name: "Dependencies difference",
			modify: func(i *Issue) {
				i.Dependencies = []*Dependency{
					{
						IssueID:     "issue-1",
						DependsOnID: "issue-2",
						Type:        DepBlocks,
						CreatedBy:   "user1",
					},
				}
			},
		},
		{
			name: "Comments difference",
			modify: func(i *Issue) {
				i.Comments = []*Comment{
					{
						ID:      1,
						IssueID: "issue-1",
						Author:  "user1",
						Text:    "A comment",
					},
				}
			},
		},
		{
			name: "Sender difference",
			modify: func(i *Issue) {
				i.Sender = "agent@example.com"
			},
		},
		{
			name: "SourceFormula difference",
			modify: func(i *Issue) {
				i.SourceFormula = "my-formula"
			},
		},
		{
			name: "SourceLocation difference",
			modify: func(i *Issue) {
				i.SourceLocation = "steps[0]"
			},
		},
		{
			name: "Ephemeral difference",
			modify: func(i *Issue) {
				i.Ephemeral = true
			},
		},
		{
			name: "DeletedBy difference",
			modify: func(i *Issue) {
				i.DeletedBy = "admin"
			},
		},
		{
			name: "DeleteReason difference",
			modify: func(i *Issue) {
				i.DeleteReason = "duplicate"
			},
		},
		{
			name: "OriginalType difference",
			modify: func(i *Issue) {
				i.OriginalType = "task"
			},
		},
	}

	baseIssue := createBaseIssue()
	baseHash := baseIssue.ComputeContentHash()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modifiedIssue := createBaseIssue()
			tt.modify(modifiedIssue)
			modifiedHash := modifiedIssue.ComputeContentHash()

			if baseHash == modifiedHash {
				t.Errorf("Field change should produce different hash for %s", tt.name)
			}
		})
	}
}

func TestComputeContentHash_LabelOrder(t *testing.T) {
	issue1 := createBaseIssue()
	issue1.Labels = []string{"alpha", "beta", "gamma"}

	issue2 := createBaseIssue()
	issue2.Labels = []string{"gamma", "alpha", "beta"}

	hash1 := issue1.ComputeContentHash()
	hash2 := issue2.ComputeContentHash()

	if hash1 != hash2 {
		t.Errorf("Different label order should produce same hash, got %s and %s", hash1, hash2)
	}
}

func TestComputeContentHash_DependencyOrder(t *testing.T) {
	dep1 := &Dependency{
		IssueID:     "issue-1",
		DependsOnID: "issue-2",
		Type:        DepBlocks,
		CreatedBy:   "user1",
	}
	dep2 := &Dependency{
		IssueID:     "issue-3",
		DependsOnID: "issue-4",
		Type:        DepRelated,
		CreatedBy:   "user2",
	}

	issue1 := createBaseIssue()
	issue1.Dependencies = []*Dependency{dep1, dep2}

	issue2 := createBaseIssue()
	issue2.Dependencies = []*Dependency{
		{
			IssueID:     "issue-3",
			DependsOnID: "issue-4",
			Type:        DepRelated,
			CreatedBy:   "user2",
		},
		{
			IssueID:     "issue-1",
			DependsOnID: "issue-2",
			Type:        DepBlocks,
			CreatedBy:   "user1",
		},
	}

	hash1 := issue1.ComputeContentHash()
	hash2 := issue2.ComputeContentHash()

	if hash1 != hash2 {
		t.Errorf("Different dependency order should produce same hash, got %s and %s", hash1, hash2)
	}
}

func TestComputeContentHash_CommentOrder(t *testing.T) {
	comment1 := &Comment{
		ID:      1,
		IssueID: "issue-1",
		Author:  "user1",
		Text:    "First comment",
	}
	comment2 := &Comment{
		ID:      2,
		IssueID: "issue-1",
		Author:  "user2",
		Text:    "Second comment",
	}

	issue1 := createBaseIssue()
	issue1.Comments = []*Comment{comment1, comment2}

	issue2 := createBaseIssue()
	issue2.Comments = []*Comment{
		{
			ID:      2,
			IssueID: "issue-1",
			Author:  "user2",
			Text:    "Second comment",
		},
		{
			ID:      1,
			IssueID: "issue-1",
			Author:  "user1",
			Text:    "First comment",
		},
	}

	hash1 := issue1.ComputeContentHash()
	hash2 := issue2.ComputeContentHash()

	if hash1 != hash2 {
		t.Errorf("Different comment order should produce same hash, got %s and %s", hash1, hash2)
	}
}

func TestComputeContentHash_FieldBoundary(t *testing.T) {
	// Test that adjacent string fields don't collide due to concatenation
	// E.g., CloseReason="ab" + ClosedBySession="cd" should not equal
	//       CloseReason="abc" + ClosedBySession="d"
	issue1 := createBaseIssue()
	issue1.CloseReason = "ab"
	issue1.ClosedBySession = "cd"

	issue2 := createBaseIssue()
	issue2.CloseReason = "abc"
	issue2.ClosedBySession = "d"

	hash1 := issue1.ComputeContentHash()
	hash2 := issue2.ComputeContentHash()

	if hash1 == hash2 {
		t.Error("Adjacent field concatenation should not produce same hash")
	}
}

func TestComputeContentHash_NilVsEmpty(t *testing.T) {
	// Nil slices and empty slices should produce the same hash
	issue1 := createBaseIssue()
	issue1.Labels = nil

	issue2 := createBaseIssue()
	issue2.Labels = []string{}

	hash1 := issue1.ComputeContentHash()
	hash2 := issue2.ComputeContentHash()

	if hash1 != hash2 {
		t.Errorf("Nil and empty slices should produce same hash, got %s and %s", hash1, hash2)
	}
}

func TestComputeContentHash_WaitersOrder(t *testing.T) {
	issue1 := createBaseIssue()
	issue1.Waiters = []string{"email1@test.com", "email2@test.com", "email3@test.com"}

	issue2 := createBaseIssue()
	issue2.Waiters = []string{"email3@test.com", "email1@test.com", "email2@test.com"}

	hash1 := issue1.ComputeContentHash()
	hash2 := issue2.ComputeContentHash()

	if hash1 != hash2 {
		t.Errorf("Different waiter order should produce same hash, got %s and %s", hash1, hash2)
	}
}

func TestComputeContentHash_TimePointerVsNil(t *testing.T) {
	issue1 := createBaseIssue()
	issue1.DueAt = nil

	issue2 := createBaseIssue()
	now := time.Now()
	issue2.DueAt = &now

	hash1 := issue1.ComputeContentHash()
	hash2 := issue2.ComputeContentHash()

	if hash1 == hash2 {
		t.Error("Nil time pointer and non-nil should produce different hashes")
	}
}

func TestComputeContentHash_IntPointerVsNil(t *testing.T) {
	issue1 := createBaseIssue()
	issue1.EstimatedMinutes = nil

	issue2 := createBaseIssue()
	minutes := 0
	issue2.EstimatedMinutes = &minutes

	hash1 := issue1.ComputeContentHash()
	hash2 := issue2.ComputeContentHash()

	if hash1 == hash2 {
		t.Error("Nil int pointer and non-nil (even 0) should produce different hashes")
	}
}

func createBaseIssue() *Issue {
	return &Issue{
		ID:          "test-123",
		Title:       "Test Issue",
		Description: "A test description",
		Status:      StatusOpen,
		Priority:    2,
		IssueType:   TypeTask,
		Assignee:    "user@example.com",
		Owner:       "owner@example.com",
		CreatedBy:   "creator@example.com",
	}
}
