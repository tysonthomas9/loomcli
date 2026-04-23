package rpc

import (
	"encoding/json"
	"time"
)

// Mutation event types
const (
	MutationCreate  = "create"
	MutationUpdate  = "update"
	MutationDelete  = "delete"
	MutationComment = "comment"
	// Molecule-specific event types for activity feed
	MutationBonded        = "bonded"         // Molecule bonded to parent (dynamic bond)
	MutationSquashed      = "squashed"       // Wisp squashed to digest
	MutationBurned        = "burned"         // Wisp discarded without digest
	MutationStatus        = "status"         // Status change (in_progress, completed, failed)
	MutationRefresh       = "refresh"        // External DB change detected, clients should re-fetch
	MutationSessionChange = "session_change" // Agent session status change (started, completed, failed, etc.)
)

// MutationEvent represents a database mutation for event-driven sync
type MutationEvent struct {
	Type      string    `json:"type"`               // One of the Mutation* constants
	IssueID   string    `json:"issue_id"`           // e.g., "bd-42"
	Title     string    `json:"title,omitempty"`    // Issue title for display context (may be empty for some operations)
	Assignee  string    `json:"assignee,omitempty"` // Issue assignee for display context (may be empty)
	Actor     string    `json:"actor,omitempty"`    // Who performed the action (may differ from assignee)
	Timestamp time.Time `json:"timestamp"`
	// Optional metadata for richer events (used by status, bonded, etc.)
	OldStatus  string `json:"old_status,omitempty"`  // Previous status (for status events)
	NewStatus  string `json:"new_status,omitempty"`  // New status (for status events)
	ParentID   string `json:"parent_id,omitempty"`   // Parent molecule (for bonded events)
	StepCount  int    `json:"step_count,omitempty"`  // Number of steps (for bonded events)
	SourceRepo string `json:"source_repo,omitempty"` // Source repository for multi-repo workspaces
	// Issue carries the full lightweight issue JSON for create/update/status
	// mutations, letting consumers replace their stored issue wholesale instead
	// of hand-mapping individual fields. Absent for mutations that don't map
	// to a single issue (refresh, session_change, etc.) or when the daemon
	// didn't have the issue object readily available.
	Issue json.RawMessage `json:"issue,omitempty"`
}
