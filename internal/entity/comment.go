package entity

import (
	"fmt"
	"time"
)

// MaxCommentLength is the maximum allowed comment text length in bytes (64KB).
const MaxCommentLength = 64 * 1024

// Comment represents a comment on an issue, supporting threading, soft-delete, and edit tracking.
type Comment struct {
	ID        int64      `json:"id"`
	IssueID   string     `json:"issue_id"`
	Author    string     `json:"author"`
	Text      string     `json:"text"`
	CreatedAt time.Time  `json:"created_at"`
	ParentID  *int64     `json:"parent_id,omitempty"`
	EditedAt  *time.Time `json:"edited_at,omitempty"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`

	// Optionally populated collections.
	Reactions []*Reaction    `json:"reactions,omitempty"`
	Edits     []*CommentEdit `json:"edits,omitempty"`
}

// Validate checks structural invariants of a Comment.
func (c *Comment) Validate() error {
	if c.IssueID == "" {
		return fmt.Errorf("issue_id is required")
	}
	if c.Author == "" {
		return fmt.Errorf("author is required")
	}
	if c.Text == "" {
		return fmt.Errorf("text is required")
	}
	if len(c.Text) > MaxCommentLength {
		return fmt.Errorf("text exceeds maximum length of %d bytes (got %d)", MaxCommentLength, len(c.Text))
	}
	if c.ParentID != nil && *c.ParentID <= 0 {
		return fmt.Errorf("parent_id must be positive")
	}
	return nil
}

// IsDeleted returns true when the comment has been soft-deleted.
func (c *Comment) IsDeleted() bool {
	return c.DeletedAt != nil
}

// IsEdited returns true when the comment text has been edited.
func (c *Comment) IsEdited() bool {
	return c.EditedAt != nil
}

// IsReply returns true when the comment is a reply to another comment.
func (c *Comment) IsReply() bool {
	return c.ParentID != nil
}

// CommentEdit records a single edit to a comment's text.
type CommentEdit struct {
	ID        int64     `json:"id"`
	CommentID int64     `json:"comment_id"`
	OldText   string    `json:"old_text"`
	NewText   string    `json:"new_text"`
	EditedBy  string    `json:"edited_by"`
	EditedAt  time.Time `json:"edited_at"`
}

// Validate checks structural invariants of a CommentEdit.
func (e *CommentEdit) Validate() error {
	if e.CommentID <= 0 {
		return fmt.Errorf("comment_id must be positive")
	}
	if e.OldText == "" {
		return fmt.Errorf("old_text is required")
	}
	if e.NewText == "" {
		return fmt.Errorf("new_text is required")
	}
	if e.OldText == e.NewText {
		return fmt.Errorf("old_text and new_text must differ")
	}
	if e.EditedBy == "" {
		return fmt.Errorf("edited_by is required")
	}
	return nil
}

// Reaction represents an emoji reaction on a comment.
type Reaction struct {
	ID        int64     `json:"id"`
	CommentID int64     `json:"comment_id"`
	Author    string    `json:"author"`
	Emoji     string    `json:"emoji"`
	CreatedAt time.Time `json:"created_at"`
}

// Validate checks structural invariants of a Reaction.
func (r *Reaction) Validate() error {
	if r.CommentID <= 0 {
		return fmt.Errorf("comment_id must be positive")
	}
	if r.Author == "" {
		return fmt.Errorf("author is required")
	}
	if r.Emoji == "" {
		return fmt.Errorf("emoji is required")
	}
	if len(r.Emoji) > 64 {
		return fmt.Errorf("emoji exceeds maximum length of 64 bytes")
	}
	return nil
}
