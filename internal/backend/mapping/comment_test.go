package mapping

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/entity"
)

// ---------------------------------------------------------------------------
// CommentFromData
// ---------------------------------------------------------------------------

func TestCommentFromData(t *testing.T) {
	now := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	editedAt := now.Add(30 * time.Minute)
	parentID := int64(5)

	tests := []struct {
		name   string
		input  backend.CommentData
		verify func(t *testing.T, c *entity.Comment)
	}{
		{
			name: "fully populated CommentData maps all fields, Reactions/Edits/DeletedAt nil",
			input: backend.CommentData{
				ID:        42,
				IssueID:   "issue-1",
				Author:    "alice",
				Text:      "Looks good to me",
				CreatedAt: now,
				ParentID:  &parentID,
				EditedAt:  &editedAt,
			},
			verify: func(t *testing.T, c *entity.Comment) {
				require.NotNil(t, c)
				require.Equal(t, int64(42), c.ID)
				require.Equal(t, "issue-1", c.IssueID)
				require.Equal(t, "alice", c.Author)
				require.Equal(t, "Looks good to me", c.Text)
				require.True(t, c.CreatedAt.Equal(now))
				require.NotNil(t, c.ParentID)
				require.Equal(t, int64(5), *c.ParentID)
				require.NotNil(t, c.EditedAt)
				require.True(t, c.EditedAt.Equal(editedAt))

				// Fields not in CommentData must be nil/zero.
				require.Nil(t, c.Reactions)
				require.Nil(t, c.Edits)
				require.Nil(t, c.DeletedAt)
			},
		},
		{
			name: "nil ParentID and EditedAt produce nil in entity",
			input: backend.CommentData{
				ID:        1,
				IssueID:   "issue-2",
				Author:    "bob",
				Text:      "Hello",
				CreatedAt: now,
				ParentID:  nil,
				EditedAt:  nil,
			},
			verify: func(t *testing.T, c *entity.Comment) {
				require.Nil(t, c.ParentID)
				require.Nil(t, c.EditedAt)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CommentFromData(tt.input)
			tt.verify(t, got)
		})
	}
}

// ---------------------------------------------------------------------------
// CommentToData
// ---------------------------------------------------------------------------

func TestCommentToData(t *testing.T) {
	now := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	editedAt := now.Add(time.Hour)
	deletedAt := now.Add(2 * time.Hour)
	parentID := int64(10)

	t.Run("fully populated entity.Comment with Reactions/Edits/DeletedAt drops them in output", func(t *testing.T) {
		c := &entity.Comment{
			ID:        99,
			IssueID:   "issue-1",
			Author:    "carol",
			Text:      "Updated comment",
			CreatedAt: now,
			ParentID:  &parentID,
			EditedAt:  &editedAt,
			DeletedAt: &deletedAt,
			Reactions: []*entity.Reaction{
				{ID: 1, CommentID: 99, Author: "dan", Emoji: "thumbsup", CreatedAt: now},
			},
			Edits: []*entity.CommentEdit{
				{ID: 1, CommentID: 99, OldText: "old", NewText: "new", EditedBy: "carol", EditedAt: now},
			},
		}

		got := CommentToData(c)

		require.Equal(t, int64(99), got.ID)
		require.Equal(t, "issue-1", got.IssueID)
		require.Equal(t, "carol", got.Author)
		require.Equal(t, "Updated comment", got.Text)
		require.True(t, got.CreatedAt.Equal(now))
		require.NotNil(t, got.ParentID)
		require.Equal(t, int64(10), *got.ParentID)
		require.NotNil(t, got.EditedAt)
		require.True(t, got.EditedAt.Equal(editedAt))

		// DeletedAt, Reactions, Edits are not in CommentData (dropped).
		// CommentData struct has no DeletedAt, Reactions, or Edits fields.
	})

	t.Run("nil entity.Comment returns zero-value CommentData without panic", func(t *testing.T) {
		got := CommentToData(nil)

		require.Zero(t, got.ID)
		require.Empty(t, got.IssueID)
		require.Empty(t, got.Author)
		require.Empty(t, got.Text)
		require.True(t, got.CreatedAt.IsZero())
		require.Nil(t, got.ParentID)
		require.Nil(t, got.EditedAt)
	})
}

// ---------------------------------------------------------------------------
// CommentsFromData / CommentsToData (batch)
// ---------------------------------------------------------------------------

func TestCommentsFromData(t *testing.T) {
	now := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)

	t.Run("nil input produces non-nil empty output", func(t *testing.T) {
		result := CommentsFromData(nil)
		require.NotNil(t, result)
		require.Empty(t, result)
	})

	t.Run("multiple items mapped correctly", func(t *testing.T) {
		input := []backend.CommentData{
			{ID: 1, IssueID: "a", Author: "x", Text: "first", CreatedAt: now},
			{ID: 2, IssueID: "a", Author: "y", Text: "second", CreatedAt: now},
			{ID: 3, IssueID: "b", Author: "z", Text: "third", CreatedAt: now},
		}
		result := CommentsFromData(input)
		require.Len(t, result, 3)
		require.Equal(t, int64(1), result[0].ID)
		require.Equal(t, "x", result[0].Author)
		require.Equal(t, int64(2), result[1].ID)
		require.Equal(t, int64(3), result[2].ID)
	})
}

func TestCommentsToData(t *testing.T) {
	now := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)

	t.Run("nil input produces non-nil empty output", func(t *testing.T) {
		result := CommentsToData(nil)
		require.NotNil(t, result)
		require.Empty(t, result)
	})

	t.Run("multiple items mapped correctly", func(t *testing.T) {
		input := []*entity.Comment{
			{ID: 10, IssueID: "a", Author: "alice", Text: "hello", CreatedAt: now},
			{ID: 20, IssueID: "b", Author: "bob", Text: "world", CreatedAt: now},
		}
		result := CommentsToData(input)
		require.Len(t, result, 2)
		require.Equal(t, int64(10), result[0].ID)
		require.Equal(t, "alice", result[0].Author)
		require.Equal(t, int64(20), result[1].ID)
		require.Equal(t, "bob", result[1].Author)
	})
}
