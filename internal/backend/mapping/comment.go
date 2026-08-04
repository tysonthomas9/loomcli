package mapping

import (
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/entity"
)

// CommentFromData converts backend.CommentData to *entity.Comment.
// Fields not present in CommentData (Reactions, Edits, DeletedAt) are nil/zero.
func CommentFromData(d backend.CommentData) *entity.Comment {
	return &entity.Comment{
		ID:        d.ID,
		IssueID:   d.IssueID,
		Author:    d.Author,
		Text:      d.Text,
		CreatedAt: d.CreatedAt,
		ParentID:  d.ParentID,
		EditedAt:  d.EditedAt,
	}
}

// CommentsFromData converts a slice of backend.CommentData to []*entity.Comment.
// Returns a non-nil empty slice for nil or empty input.
func CommentsFromData(ds []backend.CommentData) []*entity.Comment {
	out := make([]*entity.Comment, 0, len(ds))
	for i := range ds {
		out = append(out, CommentFromData(ds[i]))
	}
	return out
}

// CommentToData converts *entity.Comment to backend.CommentData.
// Fields not present in CommentData (Reactions, Edits, DeletedAt) are dropped.
// Returns zero-value backend.CommentData if c is nil (no panic).
func CommentToData(c *entity.Comment) backend.CommentData {
	if c == nil {
		return backend.CommentData{}
	}
	return backend.CommentData{
		ID:        c.ID,
		IssueID:   c.IssueID,
		Author:    c.Author,
		Text:      c.Text,
		CreatedAt: c.CreatedAt,
		ParentID:  c.ParentID,
		EditedAt:  c.EditedAt,
	}
}

// CommentsToData converts a slice of *entity.Comment to []backend.CommentData.
// Returns a non-nil empty slice for nil or empty input. Nil entries in the input
// slice are converted to zero-value CommentData (not skipped).
func CommentsToData(cs []*entity.Comment) []backend.CommentData {
	out := make([]backend.CommentData, 0, len(cs))
	for i := range cs {
		out = append(out, CommentToData(cs[i]))
	}
	return out
}
