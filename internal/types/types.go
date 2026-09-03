// Package types defines core issue-tracking data structures.
package types

import "time"

// BlockerRef contains detailed information about a blocker issue
type BlockerRef struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Priority int    `json:"priority"`
}

// BlockedIssue extends Issue with blocking information
type BlockedIssue struct {
	Issue
	BlockedByCount   int          `json:"blocked_by_count"`
	BlockedBy        []string     `json:"blocked_by"`
	BlockedByDetails []BlockerRef `json:"blocked_by_details,omitempty"`
}

// TreeNode represents a node in a dependency tree
type TreeNode struct {
	Issue
	Depth     int    `json:"depth"`
	ParentID  string `json:"parent_id"`
	Truncated bool   `json:"truncated"`
}

// MoleculeProgressStats provides efficient progress info for large molecules.
// This uses indexed queries instead of loading all steps into memory.
type MoleculeProgressStats struct {
	MoleculeID    string     `json:"molecule_id"`
	MoleculeTitle string     `json:"molecule_title"`
	Total         int        `json:"total"`           // Total steps (direct children)
	Completed     int        `json:"completed"`       // Closed steps
	InProgress    int        `json:"in_progress"`     // Steps currently in progress
	CurrentStepID string     `json:"current_step_id"` // First in_progress step ID (if any)
	FirstClosed   *time.Time `json:"first_closed,omitempty"`
	LastClosed    *time.Time `json:"last_closed,omitempty"`
}

// Statistics provides aggregate metrics
type Statistics struct {
	TotalIssues      int `json:"total_issues"`
	OpenIssues       int `json:"open_issues"`
	InProgressIssues int `json:"in_progress_issues"`
	ClosedIssues     int `json:"closed_issues"`
	BlockedIssues    int `json:"blocked_issues"`
	DeferredIssues   int `json:"deferred_issues"` // Issues on ice
	ReadyIssues      int `json:"ready_issues"`
	// ReviewIssues and StatusBlockedIssues are per-STATUS counts. StatusBlockedIssues
	// is not BlockedIssues: the latter is the computed dependency-blocked view, whose
	// members mostly carry status "open", so the two legitimately differ.
	ReviewIssues            int     `json:"review_issues"`
	StatusBlockedIssues     int     `json:"status_blocked_issues"`
	TombstoneIssues         int     `json:"tombstone_issues"` // Soft-deleted issues
	PinnedIssues            int     `json:"pinned_issues"`    // Persistent issues
	EpicsEligibleForClosure int     `json:"epics_eligible_for_closure"`
	AverageLeadTime         float64 `json:"average_lead_time_hours"`
}

// IssueFilter is used to filter issue queries
type IssueFilter struct {
	Status      *Status
	Priority    *int
	IssueType   *IssueType
	Assignee    *string
	Labels      []string // AND semantics: issue must have ALL these labels
	LabelsAny   []string // OR semantics: issue must have AT LEAST ONE of these labels
	TitleSearch string
	IDs         []string // Filter by specific issue IDs
	IDPrefix    string   // Filter by ID prefix
	Limit       int

	// Pattern matching
	TitleContains       string
	DescriptionContains string
	NotesContains       string

	// Date ranges
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
	UpdatedAfter  *time.Time
	UpdatedBefore *time.Time
	ClosedAfter   *time.Time
	ClosedBefore  *time.Time

	// Empty/null checks
	EmptyDescription bool
	NoAssignee       bool
	NoLabels         bool

	// Numeric ranges
	PriorityMin *int
	PriorityMax *int

	// Tombstone filtering
	IncludeTombstones bool // If false (default), exclude tombstones from results

	// Ephemeral filtering
	Ephemeral *bool // Filter by ephemeral flag (nil = any, true = only ephemeral, false = only persistent)

	// Pinned filtering
	Pinned *bool // Filter by pinned flag (nil = any, true = only pinned, false = only non-pinned)

	// Template filtering
	IsTemplate *bool // Filter by template flag (nil = any, true = only templates, false = exclude templates)

	// Parent filtering: filter children by parent issue ID
	ParentID *string // Filter by parent issue (via parent-child dependency)

	// Molecule type filtering
	MolType *MolType // Filter by molecule type (nil = any, swarm/patrol/work)

	// Status exclusion (for default non-closed behavior)
	ExcludeStatus []Status // Exclude issues with these statuses

	// Type exclusion (for hiding internal types like gates)
	ExcludeTypes []IssueType // Exclude issues with these types

	// Time-based scheduling filters (GH#820)
	Deferred    bool       // Filter issues with defer_until set (any value)
	DeferAfter  *time.Time // Filter issues with defer_until > this time
	DeferBefore *time.Time // Filter issues with defer_until < this time
	DueAfter    *time.Time // Filter issues with due_at > this time
	DueBefore   *time.Time // Filter issues with due_at < this time
	Overdue     bool       // Filter issues where due_at < now AND status != closed
}

// WorkFilter is used to filter ready work queries
type WorkFilter struct {
	Status     Status
	Type       string // Filter by issue type (task, bug, feature, epic, merge-request, etc.)
	Priority   *int
	Assignee   *string
	Unassigned bool     // Filter for issues with no assignee
	Labels     []string // AND semantics: issue must have ALL these labels
	LabelsAny  []string // OR semantics: issue must have AT LEAST ONE of these labels
	Limit      int
	SortPolicy SortPolicy

	// Parent filtering: filter to descendants of a bead/epic (recursive)
	ParentID *string // Show all descendants of this issue

	// Molecule type filtering
	MolType *MolType // Filter by molecule type (nil = any, swarm/patrol/work)

	// Molecule step filtering
	// By default, GetReadyWork excludes mol/wisp steps (IDs containing -mol- or -wisp-)
	// Set to true for internal callers that need to see mol steps (e.g., findGateReadyMolecules)
	IncludeMolSteps bool
}

// StaleFilter is used to filter stale issue queries
type StaleFilter struct {
	Days   int    // Issues not updated in this many days
	Status string // Filter by status (open|in_progress|blocked), empty = all non-closed
	Limit  int    // Maximum issues to return
}

// EpicStatus represents an epic with its completion status
type EpicStatus struct {
	Epic             *Issue `json:"epic"`
	TotalChildren    int    `json:"total_children"`
	ClosedChildren   int    `json:"closed_children"`
	EligibleForClose bool   `json:"eligible_for_close"`
}
