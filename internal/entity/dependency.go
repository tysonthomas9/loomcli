package entity

import (
	"strings"
	"time"
)

// Dependency represents a relationship between issues.
type Dependency struct {
	IssueID     string         `json:"issue_id"`
	DependsOnID string         `json:"depends_on_id"`
	Type        DependencyType `json:"type"`
	CreatedAt   time.Time      `json:"created_at"`
	CreatedBy   string         `json:"created_by,omitempty"`
	Metadata    string         `json:"metadata,omitempty"`
	ThreadID    string         `json:"thread_id,omitempty"`
}

// DependencyType categorizes the relationship between issues.
type DependencyType string

// Dependency type constants.
const (
	// Workflow types (affect ready work calculation)
	DepBlocks      DependencyType = "blocks"
	DepParentChild DependencyType = "parent-child"
	// NOT STORABLE IN FLEET-DB. These two describe semantics no storage
	// backend implements; fleet-db's vocabulary is blocks / parent-child /
	// related / duplicate-of / superseded-by, and it rejects anything else.
	// The fleet backend validates against that set (validateFleetDepType), so
	// creating either of these fails at the call site rather than at the
	// server. Kept as in-process vocabulary only.
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

	// Entity types (HOP foundation)
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

// WaitsForGate constants.
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
