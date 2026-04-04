package dto

import (
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/entity"
)

// Validate checks that the CreateIssueRequest is well-formed.
// Returns nil if valid, or *ValidationError with all field errors collected.
func (r *CreateIssueRequest) Validate() error {
	if r == nil {
		return &ValidationError{Errors: []FieldError{{Field: "request", Message: "is nil"}}}
	}

	var b validationBuilder

	// title: required, max length (checked on trimmed value)
	trimmed := strings.TrimSpace(r.Title)
	if trimmed == "" {
		b.add("title", "is required")
	} else if len(trimmed) > MaxTitleLength {
		b.add("title", fmt.Sprintf("must be %d characters or less (got %d)", MaxTitleLength, len(trimmed)))
	}

	// issue_type: required, must be valid entity type
	if r.IssueType == "" {
		b.add("issue_type", "is required")
	} else if !entity.IssueType(r.IssueType).IsValid() {
		b.add("issue_type", "must be one of: bug, feature, task, epic, chore")
	}

	// priority: 0-4 inclusive
	if r.Priority < 0 || r.Priority > 4 {
		b.add("priority", fmt.Sprintf("must be between 0 and 4 (got %d)", r.Priority))
	}

	// labels: size limit
	if len(r.Labels) > MaxLabels {
		b.add("labels", fmt.Sprintf("too many (max %d, got %d)", MaxLabels, len(r.Labels)))
	}

	// dependencies: size limit
	if len(r.Dependencies) > MaxDependencies {
		b.add("dependencies", fmt.Sprintf("too many (max %d, got %d)", MaxDependencies, len(r.Dependencies)))
	}

	// estimated_minutes: non-negative
	if r.EstimatedMinutes != nil && *r.EstimatedMinutes < 0 {
		b.add("estimated_minutes", "cannot be negative")
	}

	// due_at: valid RFC3339
	if r.DueAt != "" {
		if _, err := time.Parse(time.RFC3339, r.DueAt); err != nil {
			b.add("due_at", "must be a valid RFC 3339 timestamp (e.g., 2024-01-15T10:30:00Z)")
		}
	}

	// defer_until: valid RFC3339
	if r.DeferUntil != "" {
		if _, err := time.Parse(time.RFC3339, r.DeferUntil); err != nil {
			b.add("defer_until", "must be a valid RFC 3339 timestamp (e.g., 2024-01-15T10:30:00Z)")
		}
	}

	return b.build()
}

// Validate checks that the PatchIssueRequest is well-formed.
// Returns nil if valid (including all-nil fields = no-op update),
// or *ValidationError with all field errors collected.
func (r *PatchIssueRequest) Validate() error {
	if r == nil {
		return &ValidationError{Errors: []FieldError{{Field: "request", Message: "is nil"}}}
	}

	var b validationBuilder

	// title: if set, must be non-empty and within length (checked on trimmed value)
	if r.Title != nil {
		trimmed := strings.TrimSpace(*r.Title)
		if trimmed == "" {
			b.add("title", "cannot be empty")
		} else if len(trimmed) > MaxTitleLength {
			b.add("title", fmt.Sprintf("must be %d characters or less (got %d)", MaxTitleLength, len(trimmed)))
		}
	}

	// status: if set, must be a user-facing status (not internal like tombstone/pinned/hooked)
	if r.Status != nil {
		if !isAPIStatus(*r.Status) {
			b.add("status", "must be a valid status (open, in_progress, blocked, deferred, review, closed)")
		}
	}

	// priority: if set, must be 0-4
	if r.Priority != nil {
		if *r.Priority < 0 || *r.Priority > 4 {
			b.add("priority", fmt.Sprintf("must be between 0 and 4 (got %d)", *r.Priority))
		}
	}

	// issue_type: if set, must be non-empty and valid
	if r.IssueType != nil {
		if *r.IssueType == "" {
			b.add("issue_type", "must be one of: bug, feature, task, epic, chore")
		} else if !entity.IssueType(*r.IssueType).IsValid() {
			b.add("issue_type", "must be one of: bug, feature, task, epic, chore")
		}
	}

	// estimated_minutes: if set, non-negative
	if r.EstimatedMinutes != nil && *r.EstimatedMinutes < 0 {
		b.add("estimated_minutes", "cannot be negative")
	}

	validateOptionalTimestamp(&b, "due_at", r.DueAt)
	validateOptionalTimestamp(&b, "defer_until", r.DeferUntil)
	validateLabels(&b, r.SetLabels, r.AddLabels, r.RemoveLabels)

	return b.build()
}

// validateOptionalTimestamp checks that a *string timestamp, if set, is valid RFC3339.
func validateOptionalTimestamp(b *validationBuilder, field string, val *string) {
	if val == nil {
		return
	}
	if _, err := time.Parse(time.RFC3339, *val); err != nil {
		b.add(field, "must be a valid RFC 3339 timestamp (e.g., 2024-01-15T10:30:00Z)")
	}
}

// validateLabels checks mutual exclusivity and size limits for label operations.
func validateLabels(b *validationBuilder, set, add, remove []string) {
	if len(set) > 0 {
		if len(add) > 0 || len(remove) > 0 {
			b.add("set_labels", "cannot be combined with add_labels or remove_labels")
		}
	}
	if len(add) > MaxLabels {
		b.add("add_labels", fmt.Sprintf("too many (max %d, got %d)", MaxLabels, len(add)))
	}
	if len(remove) > MaxLabels {
		b.add("remove_labels", fmt.Sprintf("too many (max %d, got %d)", MaxLabels, len(remove)))
	}
	if len(set) > MaxLabels {
		b.add("set_labels", fmt.Sprintf("too many (max %d, got %d)", MaxLabels, len(set)))
	}
}
