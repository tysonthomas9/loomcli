package types

import (
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// Dependency represents a relationship between issues
type Dependency struct {
	IssueID     string         `json:"issue_id"`
	DependsOnID string         `json:"depends_on_id"`
	Type        DependencyType `json:"type"`
	CreatedAt   time.Time      `json:"created_at"`
	CreatedBy   string         `json:"created_by,omitempty"`
	// Metadata contains type-specific edge data (JSON blob)
	// Examples: similarity scores, approval details, skill proficiency
	Metadata string `json:"metadata,omitempty"`
	// ThreadID groups conversation edges for efficient thread queries
	// For replies-to edges, this identifies the conversation root
	ThreadID string `json:"thread_id,omitempty"`
}

// DependencyCounts holds counts for dependencies and dependents
type DependencyCounts struct {
	DependencyCount int `json:"dependency_count"` // Number of issues this issue depends on
	DependentCount  int `json:"dependent_count"`  // Number of issues that depend on this issue
}

// ParentInfo contains parent issue information for child issues.
// Used by GetParentIDs to return parent info for batch lookups.
type ParentInfo struct {
	ParentID    string `json:"parent_id"`
	ParentTitle string `json:"parent_title"`
}

// IssueWithDependencyMetadata extends Issue with dependency relationship type
// Note: We explicitly include all Issue fields to ensure proper JSON marshaling
type IssueWithDependencyMetadata struct {
	Issue
	DependencyType DependencyType `json:"dependency_type"`
}

// IssueWithCounts extends Issue with dependency relationship counts
type IssueWithCounts struct {
	*Issue
	DependencyCount int `json:"dependency_count"`
	DependentCount  int `json:"dependent_count"`
}

// IssueDetails extends Issue with labels, dependencies, dependents, and comments.
// Used for JSON serialization in detail and RPC responses.
// Note: Labels, Dependencies, Dependents, and Comments do NOT use omitempty
// to ensure consistent JSON structure for frontend type guards.
type IssueDetails struct {
	Issue
	Labels       []string                       `json:"labels"`
	Dependencies []*IssueWithDependencyMetadata `json:"dependencies"`
	Dependents   []*IssueWithDependencyMetadata `json:"dependents"`
	Comments     []*Comment                     `json:"comments"`
	Parent       *string                        `json:"parent,omitempty"`
}

// DependencyType categorizes the relationship
type DependencyType string

// Dependency type constants
const (
	// Workflow types (affect ready work calculation)
	DepBlocks            DependencyType = "blocks"
	DepParentChild       DependencyType = "parent-child"
	DepConditionalBlocks DependencyType = "conditional-blocks" // B runs only if A fails
	DepWaitsFor          DependencyType = "waits-for"          // Fanout gate: wait for dynamic children

	// Association types
	DepRelated        DependencyType = "related"
	DepDiscoveredFrom DependencyType = "discovered-from"

	// Graph link types
	DepRepliesTo  DependencyType = "replies-to" // Conversation threading
	DepRelatesTo  DependencyType = "relates-to" // Loose knowledge graph edges
	DepDuplicates DependencyType = "duplicates" // Deduplication link
	DepSupersedes DependencyType = "supersedes" // Version chain link

	// Entity types (HOP foundation - Decision 004)
	DepAuthoredBy DependencyType = "authored-by" // Creator relationship
	DepAssignedTo DependencyType = "assigned-to" // Assignment relationship
	DepApprovedBy DependencyType = "approved-by" // Approval relationship
	DepAttests    DependencyType = "attests"     // Skill attestation: X attests Y has skill Z

	// Convoy tracking (non-blocking cross-project references)
	DepTracks DependencyType = "tracks" // Convoy → issue tracking (non-blocking)

	// Reference types (cross-referencing without blocking)
	DepUntil     DependencyType = "until"     // Active until target closes (e.g., muted until issue resolved)
	DepCausedBy  DependencyType = "caused-by" // Triggered by target (audit trail)
	DepValidates DependencyType = "validates" // Approval/validation relationship

	// Delegation types (work delegation chains)
	DepDelegatedFrom DependencyType = "delegated-from" // Work delegated from parent; completion cascades up
)

// IsValid checks if the dependency type value is valid.
// Accepts any non-empty string up to 50 characters.
// Use IsWellKnown() to check if it's a built-in type.
func (d DependencyType) IsValid() bool {
	return len(d) > 0 && len(d) <= 50
}

// IsWellKnown checks if the dependency type is a well-known constant.
// Returns false for custom/user-defined types (which are still valid).
func (d DependencyType) IsWellKnown() bool {
	switch d {
	case DepBlocks, DepParentChild, DepConditionalBlocks, DepWaitsFor, DepRelated, DepDiscoveredFrom,
		DepRepliesTo, DepRelatesTo, DepDuplicates, DepSupersedes,
		DepAuthoredBy, DepAssignedTo, DepApprovedBy, DepAttests, DepTracks,
		DepUntil, DepCausedBy, DepValidates, DepDelegatedFrom:
		return true
	}
	return false
}

// AffectsReadyWork returns true if this dependency type blocks work.
// Only blocking types affect the ready work calculation.
func (d DependencyType) AffectsReadyWork() bool {
	return d == DepBlocks || d == DepParentChild || d == DepConditionalBlocks || d == DepWaitsFor
}

// IsDirectBlocker returns true if this dependency type directly creates blockage.
// Unlike AffectsReadyWork(), this excludes parent-child which only propagates
// existing blockage transitively (via blocked_issues_cache SQL) but does not
// itself create a blocking relationship.
func (d DependencyType) IsDirectBlocker() bool {
	return d == DepBlocks || d == DepConditionalBlocks || d == DepWaitsFor
}

// WaitsForMeta holds metadata for waits-for dependencies (fanout gates).
// Stored as JSON in the Dependency.Metadata field.
type WaitsForMeta struct {
	// Gate type: "all-children" (wait for all), "any-children" (wait for first)
	Gate string `json:"gate"`
	// SpawnerID identifies which step/issue spawns the children to wait for.
	// If empty, waits for all direct children of the depends_on_id issue.
	SpawnerID string `json:"spawner_id,omitempty"`
}

// WaitsForGate constants
const (
	WaitsForAllChildren = "all-children" // Wait for all dynamic children to complete
	WaitsForAnyChildren = "any-children" // Proceed when first child completes (future)
)

// AttestsMeta holds metadata for attests dependencies (skill attestations).
// Stored as JSON in the Dependency.Metadata field.
// Enables: Entity X attests that Entity Y has skill Z at level N.
type AttestsMeta struct {
	// Skill is the identifier of the skill being attested (e.g., "go", "rust", "code-review")
	Skill string `json:"skill"`
	// Level is the proficiency level (e.g., "beginner", "intermediate", "expert", or numeric 1-5)
	Level string `json:"level"`
	// Date is when the attestation was made (RFC3339 format)
	Date string `json:"date"`
	// Evidence is optional reference to supporting evidence (e.g., issue ID, commit, PR)
	Evidence string `json:"evidence,omitempty"`
	// Notes is optional free-form notes about the attestation
	Notes string `json:"notes,omitempty"`
}

// FailureCloseKeywords are keywords that indicate an issue was closed due to failure.
// Used by conditional-blocks dependencies to determine if the condition is met.
var FailureCloseKeywords = []string{
	"failed",
	"rejected",
	"wontfix",
	"won't fix",
	"canceled",
	"cancelled", //nolint:misspell // British spelling intentionally included
	"abandoned",
	"blocked",
	"error",
	"timeout",
	"aborted",
}

// IsFailureClose returns true if the close reason indicates the issue failed.
// This is used by conditional-blocks dependencies: B runs only if A fails.
// A "failure" close reason contains one of the FailureCloseKeywords (case-insensitive).
func IsFailureClose(closeReason string) bool {
	if closeReason == "" {
		return false
	}
	lower := strings.ToLower(closeReason)
	for _, keyword := range FailureCloseKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// Label represents a tag on an issue
type Label struct {
	IssueID string `json:"issue_id"`
	Label   string `json:"label"`
}

// Comment is a compatibility alias for the Work Items-owned comment model.
// New capability callers import internal/modules/workitems directly.
type Comment = workitems.Comment

// Event represents an audit trail entry
type Event struct {
	ID        int64     `json:"id"`
	IssueID   string    `json:"issue_id"`
	EventType EventType `json:"event_type"`
	Actor     string    `json:"actor"`
	OldValue  *string   `json:"old_value,omitempty"`
	NewValue  *string   `json:"new_value,omitempty"`
	Comment   *string   `json:"comment,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// EventType categorizes audit trail events
type EventType string

// Event type constants for audit trail
const (
	EventCreated           EventType = "issue.created"
	EventUpdated           EventType = "issue.updated"
	EventStatusChanged     EventType = "issue.status_changed"
	EventCommented         EventType = "issue.commented"
	EventClosed            EventType = "issue.closed"
	EventReopened          EventType = "issue.reopened"
	EventDependencyAdded   EventType = "issue.dependency_added"
	EventDependencyRemoved EventType = "issue.dependency_removed"
	EventLabelAdded        EventType = "issue.label_added"
	EventLabelRemoved      EventType = "issue.label_removed"
	EventCompacted         EventType = "issue.compacted"
)

// BondRef tracks compound molecule lineage.
// When protos or molecules are bonded together, BondRefs record
// which sources were combined and how they were attached.
type BondRef struct {
	SourceID  string `json:"source_id"`            // Source proto or molecule ID
	BondType  string `json:"bond_type"`            // sequential, parallel, conditional
	BondPoint string `json:"bond_point,omitempty"` // Attachment site (issue ID or empty for root)
}

// Bond type constants for compound molecules
const (
	BondTypeSequential  = "sequential"  // B runs after A completes
	BondTypeParallel    = "parallel"    // B runs alongside A
	BondTypeConditional = "conditional" // B runs only if A fails
	BondTypeRoot        = "root"        // Marks the primary/root component
)

// ID prefix constants for molecule/wisp instantiation.
// These prefixes are inserted into issue IDs: <project>-<prefix>-<id>
// Exclusion from ready queues is config-driven via ready.exclude_id_patterns.
const (
	IDPrefixMol  = "mol"  // Persistent molecules
	IDPrefixWisp = "wisp" // Ephemeral wisps
)

// EntityRef is a structured reference to an entity (human, agent, or org).
// This is the foundation for HOP entity tracking and CV chains.
// Can be rendered as a URI: entity://hop/<platform>/<org>/<id>
//
// Example usage:
//
//	ref := &EntityRef{
//	    Name:     "polecat/Nux",
//	    Platform: "gastown",
//	    Org:      "steveyegge",
//	    ID:       "polecat-nux",
//	}
//	uri := ref.URI() // "entity://hop/gastown/steveyegge/polecat-nux"
type EntityRef struct {
	// Name is the human-readable identifier (e.g., "polecat/Nux", "mayor")
	Name string `json:"name,omitempty"`

	// Platform identifies the execution context (e.g., "gastown", "github")
	Platform string `json:"platform,omitempty"`

	// Org identifies the organization (e.g., "steveyegge", "anthropics")
	Org string `json:"org,omitempty"`

	// ID is the unique identifier within the platform/org (e.g., "polecat-nux")
	ID string `json:"id,omitempty"`
}

// IsEmpty returns true if all fields are empty.
func (e *EntityRef) IsEmpty() bool {
	if e == nil {
		return true
	}
	return e.Name == "" && e.Platform == "" && e.Org == "" && e.ID == ""
}

// URI returns the entity as a HOP URI.
// Format: entity://hop/<platform>/<org>/<id>
// Returns empty string if Platform, Org, or ID is missing.
func (e *EntityRef) URI() string {
	if e == nil || e.Platform == "" || e.Org == "" || e.ID == "" {
		return ""
	}
	return fmt.Sprintf("entity://hop/%s/%s/%s", e.Platform, e.Org, e.ID)
}

// String returns a human-readable representation.
// Prefers Name if set, otherwise returns URI or ID.
func (e *EntityRef) String() string {
	if e == nil {
		return ""
	}
	if e.Name != "" {
		return e.Name
	}
	if uri := e.URI(); uri != "" {
		return uri
	}
	return e.ID
}

// Validation records who validated/approved work completion.
// This is core to HOP's proof-of-stake concept - validators stake
// their reputation on approvals.
type Validation struct {
	// Validator is who approved/rejected the work
	Validator *EntityRef `json:"validator"`

	// Outcome is the validation result: accepted, rejected, revision_requested
	Outcome string `json:"outcome"`

	// Timestamp is when the validation occurred
	Timestamp time.Time `json:"timestamp"`

	// Score is an optional quality score (0.0-1.0)
	Score *float32 `json:"score,omitempty"`
}

// Validation outcome constants
const (
	ValidationAccepted          = "accepted"
	ValidationRejected          = "rejected"
	ValidationRevisionRequested = "revision_requested"
)

// IsValidOutcome checks if the outcome is a known validation outcome.
func (v *Validation) IsValidOutcome() bool {
	switch v.Outcome {
	case ValidationAccepted, ValidationRejected, ValidationRevisionRequested:
		return true
	}
	return false
}

// ParseEntityURI parses a HOP entity URI into an EntityRef.
// Format: entity://hop/<platform>/<org>/<id>
// Returns nil and error if the URI is invalid.
func ParseEntityURI(uri string) (*EntityRef, error) {
	const prefix = "entity://hop/"
	if !strings.HasPrefix(uri, prefix) {
		return nil, fmt.Errorf("invalid entity URI: must start with %q", prefix)
	}

	rest := uri[len(prefix):]
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, fmt.Errorf("invalid entity URI: expected entity://hop/<platform>/<org>/<id>, got %q", uri)
	}

	return &EntityRef{
		Platform: parts[0],
		Org:      parts[1],
		ID:       parts[2],
	}, nil
}
