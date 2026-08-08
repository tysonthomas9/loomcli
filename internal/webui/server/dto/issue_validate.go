package dto

import (
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// Validate checks that the CreateIssueRequest is well-formed.
// Returns nil if valid, or *ValidationError with all field errors collected.
func (r *CreateIssueRequest) Validate() error {
	if r == nil {
		return &ValidationError{Errors: []FieldError{{Field: "request", Message: "is nil"}}}
	}

	var b validationBuilder

	validateCreateIssueCore(&b, r)
	validateCreateIssueLimits(&b, r)
	validateCreateIssueTimes(&b, r)

	return b.build()
}

func validateCreateIssueCore(b *validationBuilder, r *CreateIssueRequest) {
	if err := workitems.ValidateTitle(r.Title); err != nil {
		kind, _ := workitems.TitleValidationKindOf(err)
		switch kind {
		case workitems.TitleRequired:
			b.add("title", "is required")
		case workitems.TitleTooLong:
			canonical := workitems.CanonicalTitle(r.Title)
			b.add("title", fmt.Sprintf("must be %d characters or less (got %d)", MaxTitleLength, len(canonical)))
		}
	}
	if r.IssueType == "" {
		b.add("issue_type", "is required")
	} else if !workitems.IssueType(r.IssueType).IsBuiltIn() {
		b.add("issue_type", "must be one of: bug, feature, task, epic, chore")
	}
	if r.Priority < 0 || r.Priority > 4 {
		b.add("priority", fmt.Sprintf("must be between 0 and 4 (got %d)", r.Priority))
	}
	if !workitems.Status(r.Status).IsCreateStatus() {
		b.add("status", "must be open or deferred")
	}
}

func validateCreateIssueLimits(b *validationBuilder, r *CreateIssueRequest) {
	if len(r.Labels) > MaxLabels {
		b.add("labels", fmt.Sprintf("too many (max %d, got %d)", MaxLabels, len(r.Labels)))
	}
	if len(r.Dependencies) > MaxDependencies {
		b.add("dependencies", fmt.Sprintf("too many (max %d, got %d)", MaxDependencies, len(r.Dependencies)))
	}
	if r.EstimatedMinutes != nil && *r.EstimatedMinutes < 0 {
		b.add("estimated_minutes", "cannot be negative")
	}
}

func validateCreateIssueTimes(b *validationBuilder, r *CreateIssueRequest) {
	validateRFC3339Field(b, "due_at", r.DueAt)
	validateRFC3339Field(b, "defer_until", r.DeferUntil)
}

func validateRFC3339Field(b *validationBuilder, field, value string) {
	if value == "" {
		return
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		b.add(field, "must be a valid RFC 3339 timestamp (e.g., 2024-01-15T10:30:00Z)")
	}
}

// Validate checks that the PatchIssueRequest is well-formed.
// Returns nil if valid (including all-nil fields = no-op update),
// or *ValidationError with all field errors collected.
//
//nolint:funlen // The consolidation layer immediately above this stack base splits these validation groups.
func (r *PatchIssueRequest) Validate() error {
	if r == nil {
		return &ValidationError{Errors: []FieldError{{Field: "request", Message: "is nil"}}}
	}

	var b validationBuilder

	// title: if set, must be non-empty and within length (checked on trimmed value)
	if r.Title != nil {
		if err := workitems.ValidateTitle(*r.Title); err != nil {
			kind, _ := workitems.TitleValidationKindOf(err)
			switch kind {
			case workitems.TitleRequired:
				b.add("title", "cannot be empty")
			case workitems.TitleTooLong:
				canonical := workitems.CanonicalTitle(*r.Title)
				b.add("title", fmt.Sprintf("must be %d characters or less (got %d)", MaxTitleLength, len(canonical)))
			}
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
		} else if !workitems.IssueType(*r.IssueType).IsBuiltIn() {
			b.add("issue_type", "must be one of: bug, feature, task, epic, chore")
		}
	}

	// estimated_minutes: if set, non-negative
	if r.EstimatedMinutes != nil && *r.EstimatedMinutes < 0 {
		b.add("estimated_minutes", "cannot be negative")
	}

	// agent_state: if set, must be valid
	if r.AgentState != nil {
		if !workitems.AgentState(*r.AgentState).IsValid() {
			b.add("agent_state", "must be one of: idle, spawning, running, working, stuck, done, stopped, dead (or empty to clear)")
		}
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
