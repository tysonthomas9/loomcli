package rpc

import "time"

// Mutation event types
const (
	MutationCreate  = "create"
	MutationUpdate  = "update"
	MutationDelete  = "delete"
	MutationComment = "comment"
	// Molecule-specific event types for activity feed
	MutationBonded   = "bonded"   // Molecule bonded to parent (dynamic bond)
	MutationSquashed = "squashed" // Wisp squashed to digest
	MutationBurned   = "burned"   // Wisp discarded without digest
	MutationStatus   = "status"   // Status change (in_progress, completed, failed)
)

// MutationEvent represents a database mutation for event-driven sync
type MutationEvent struct {
	Type      string    // One of the Mutation* constants
	IssueID   string    // e.g., "bd-42"
	Title     string    // Issue title for display context (may be empty for some operations)
	Assignee  string    // Issue assignee for display context (may be empty)
	Actor     string    // Who performed the action (may differ from assignee)
	Timestamp time.Time
	// Optional metadata for richer events (used by status, bonded, etc.)
	OldStatus string `json:"old_status,omitempty"` // Previous status (for status events)
	NewStatus string `json:"new_status,omitempty"` // New status (for status events)
	ParentID  string `json:"parent_id,omitempty"`  // Parent molecule (for bonded events)
	StepCount int    `json:"step_count,omitempty"` // Number of steps (for bonded events)
}
