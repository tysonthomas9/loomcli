package dto

import "time"

// IssueResponse is the typed API response for a single issue.
// Uses string (not entity types) for Status/IssueType to decouple the wire
// format from domain types.
type IssueResponse struct {
	// Core
	ID                 string `json:"id"`
	Title              string `json:"title"`
	Description        string `json:"description,omitempty"`
	Design             string `json:"design,omitempty"`
	DesignArtifactID   string `json:"design_artifact_id,omitempty"`
	DesignFormat       string `json:"design_format,omitempty"`
	HasDesign          bool   `json:"has_design"`
	AcceptanceCriteria string `json:"acceptance_criteria,omitempty"`
	Notes              string `json:"notes,omitempty"`

	// Status/workflow
	Status    string `json:"status"`
	Priority  int    `json:"priority"` // No omitempty: 0 is valid (P0/critical)
	IssueType string `json:"issue_type"`

	// Assignment
	Assignee         string `json:"assignee,omitempty"`
	Owner            string `json:"owner,omitempty"`
	EstimatedMinutes *int   `json:"estimated_minutes,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	CloseReason string     `json:"close_reason,omitempty"`

	// Scheduling
	DueAt      *time.Time `json:"due_at,omitempty"`
	DeferUntil *time.Time `json:"defer_until,omitempty"`

	// External
	ExternalRef       *string  `json:"external_ref,omitempty"`
	SourceRepo        string   `json:"source_repo,omitempty"`
	InheritsFrom      string   `json:"inherits_from,omitempty"`
	IntegrationInputs []string `json:"integration_inputs,omitempty"`

	// Labels — no omitempty: serializes as [] when empty, not omitted.
	// Mapping function (moom5.3) must initialize to []string{} to avoid null.
	Labels []string `json:"labels"`

	// Relational (populated on detail view) — no omitempty: same reason as Labels.
	Dependencies []DependencyRef   `json:"dependencies"`
	Dependents   []DependencyRef   `json:"dependents"`
	Comments     []CommentResponse `json:"comments"`
	Parent       *string           `json:"parent,omitempty"`
	ParentTitle  *string           `json:"parent_title,omitempty"`

	// Counts (populated on list view)
	DependencyCount int `json:"dependency_count"`
	DependentCount  int `json:"dependent_count"`

	// Context
	Pinned bool `json:"pinned"`
}

// DependencyRef is a slim reference to a dependency/dependent issue.
type DependencyRef struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Priority  int    `json:"priority"`
	Type      string `json:"type"` // Dependency type (e.g., "blocks")
	IssueType string `json:"issue_type"`
}

// CommentResponse is the API representation of a comment.
type CommentResponse struct {
	ID        int64     `json:"id"`
	Author    string    `json:"author"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`

	// Optional
	ParentID *int64     `json:"parent_id,omitempty"`
	EditedAt *time.Time `json:"edited_at,omitempty"`
}
