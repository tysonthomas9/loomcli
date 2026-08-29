// Package backend defines the pluggable data access interface for issue tracking.
//
// The backend package sits between the service layer (which owns business logic)
// and the underlying data stores (fleet-db, future Redis). It defines
// wire types for data transfer, option/param structs for queries and mutations,
// and a structured error type for classified failures.
//
// The package is a leaf dependency: it imports only stdlib types and has no
// internal package dependencies.
package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// ---------------------------------------------------------------------------
// Section 1: Data types (return values)
// ---------------------------------------------------------------------------

// IssueData is the slim issue projection returned by List, Ready, and Blocked.
// It contains the fields needed for list views, kanban boards, and summary displays.
type IssueData struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Status     string   `json:"status"`
	Priority   int      `json:"priority"`
	IssueType  string   `json:"issue_type,omitempty"`
	Assignee   string   `json:"assignee,omitempty"`
	Owner      string   `json:"owner,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	SourceRepo string   `json:"source_repo,omitempty"`
	Parent     string   `json:"parent,omitempty"`
	// Collection responses may omit a large design body while retaining its
	// stable presence flag and managed-artifact reference.
	Design           string `json:"design,omitempty"`
	DesignArtifactID string `json:"design_artifact_id,omitempty"`
	DesignFormat     string `json:"design_format,omitempty"`
	HasDesign        bool   `json:"has_design"`
	// Notes is in the slim list projection (not detail-only) so kanban/filter
	// UIs can categorize a blocked issue that carries an external-blocker note
	// (the "blocked with notes" needs-attention state) without a detail fetch.
	Notes      string     `json:"notes,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DueAt      *time.Time `json:"due_at,omitempty"`
	DeferUntil *time.Time `json:"defer_until,omitempty"`
	// Lifecycle fields used by kanban and filter UIs.
	CreatedBy   string     `json:"created_by,omitempty"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	CloseReason string     `json:"close_reason,omitempty"`
	// ExternalRef is in the slim projection so kanban/list views can surface
	// a linked PR (the card renders a "PR ↗" link from it).
	ExternalRef string `json:"external_ref,omitempty"`
	// Counts for list display (populated by backends that support them).
	DependencyCount int `json:"dependency_count,omitempty"`
	DependentCount  int `json:"dependent_count,omitempty"`
	// Blocker metadata for blocked-list projections.
	BlockedByCount int      `json:"blocked_by_count,omitempty"`
	BlockedBy      []string `json:"blocked_by,omitempty"`
}

// IssueDetailData is the full issue projection returned by Get.
// It embeds IssueData and adds content fields and relational data.
//
// CreatedBy / ClosedAt / CloseReason live on the embedded IssueData (the
// slim list projection now carries them too) — accessing them via
// d.CreatedBy still works through Go's field promotion.
type IssueDetailData struct {
	IssueData

	// Content fields. Notes is promoted from the embedded IssueData (it is in
	// the slim list projection so the kanban "blocked with notes" state works).
	Description        string `json:"description,omitempty"`
	AcceptanceCriteria string `json:"acceptance_criteria,omitempty"`

	// Lifecycle (detail-only — IssueData carries the others).
	ClosedBySession string `json:"closed_by_session,omitempty"`

	// External integration. ExternalRef is promoted from the embedded IssueData
	// (it is in the slim list projection so kanban/list can render a PR link).
	EstimatedMinutes *int `json:"estimated_minutes,omitempty"`

	// Relational data.
	Dependencies []DependencyData `json:"dependencies,omitempty"`
	Dependents   []DependencyData `json:"dependents,omitempty"`
	Comments     []CommentData    `json:"comments,omitempty"`
}

// DependencyData represents a dependency relationship with inline details
// about the linked issue (for display without extra lookups).
type DependencyData struct {
	IssueID     string    `json:"issue_id"`
	DependsOnID string    `json:"depends_on_id"`
	Type        string    `json:"type"`
	Title       string    `json:"title,omitempty"`
	Status      string    `json:"status,omitempty"`
	Priority    int       `json:"priority,omitempty"`
	IssueType   string    `json:"issue_type,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	CreatedBy   string    `json:"created_by,omitempty"`
}

// CommentData represents a comment on an issue.
type CommentData struct {
	ID        int64      `json:"id"`
	IssueID   string     `json:"issue_id"`
	Author    string     `json:"author"`
	Text      string     `json:"text"`
	CreatedAt time.Time  `json:"created_at"`
	ParentID  *int64     `json:"parent_id,omitempty"`
	EditedAt  *time.Time `json:"edited_at,omitempty"`
}

// EventData represents an event/history entry for an issue.
type EventData struct {
	ID        string    `json:"id"`
	IssueID   string    `json:"issue_id"`
	Kind      string    `json:"kind"`
	Actor     string    `json:"actor,omitempty"`
	Target    string    `json:"target,omitempty"`
	Payload   string    `json:"payload,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// StatsData contains aggregate issue statistics.
// Fields mirror types.Statistics so no data is dropped during mapping.
type StatsData struct {
	TotalIssues             int     `json:"total_issues"`
	OpenIssues              int     `json:"open_issues"`
	InProgressIssues        int     `json:"in_progress_issues"`
	ClosedIssues            int     `json:"closed_issues"`
	BlockedIssues           int     `json:"blocked_issues"`
	DeferredIssues          int     `json:"deferred_issues"`
	ReadyIssues             int     `json:"ready_issues"`
	TombstoneIssues         int     `json:"tombstone_issues"`
	PinnedIssues            int     `json:"pinned_issues"`
	EpicsEligibleForClosure int     `json:"epics_eligible_for_closure"`
	AverageLeadTime         float64 `json:"average_lead_time_hours"`
}

// CloseResult is returned by Close. It contains the closed issue
// and any issues that became unblocked as a result.
type CloseResult struct {
	Closed    *IssueData  `json:"closed"`
	Unblocked []IssueData `json:"unblocked,omitempty"`
}

// MutationData represents a mutation event for real-time subscriptions.
// Used by GetMutations and WaitForMutations.
//
// MutationData mirrors rpc.MutationEvent but is backend-agnostic. The
// BeadsBackend subscription layer (task .11) maps rpc.MutationEvent to
// MutationData. Other backends produce MutationData directly.
type MutationData struct {
	Cursor     string    `json:"cursor,omitempty"`
	Type       string    `json:"type"`
	EntityType string    `json:"entity_type,omitempty"`
	EntityID   string    `json:"entity_id,omitempty"`
	Action     string    `json:"action,omitempty"`
	IssueID    string    `json:"issue_id,omitempty"`
	Title      string    `json:"title,omitempty"`
	Assignee   string    `json:"assignee,omitempty"`
	Actor      string    `json:"actor,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
	OldStatus  string    `json:"old_status,omitempty"`
	NewStatus  string    `json:"new_status,omitempty"`
	ParentID   string    `json:"parent_id,omitempty"`
	SourceRepo string    `json:"source_repo,omitempty"`
	StepCount  int       `json:"step_count,omitempty"`
}

// CursorMutationBackend is an optional IssueBackend extension for durable
// stream cursors. Backends that implement it can round-trip opaque event IDs
// instead of lossy millisecond timestamps for reconnect catch-up.
type CursorMutationBackend interface {
	GetMutationsAfter(ctx context.Context, since string) ([]MutationData, error)
	WaitForMutationsAfter(ctx context.Context, since string, timeoutMs int64) ([]MutationData, error)
}

// ---------------------------------------------------------------------------
// Section 2: Option structs (query parameters)
// ---------------------------------------------------------------------------

// ListOpts configures the List query. Zero-value fields mean "no filter".
// Fields marked "fleet-db: unsupported" are not handled by the fleet-db
// server; FleetBackend.List returns ErrFilterNotSupported if they are set.
type ListOpts struct {
	// Basic filters.
	Status    string   `json:"status,omitempty"`
	Priority  *int     `json:"priority,omitempty"` // fleet-db: unsupported (fleet-qx9c)
	IssueType string   `json:"issue_type,omitempty"`
	Assignee  string   `json:"assignee,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	LabelsAny []string `json:"labels_any,omitempty"` // fleet-db: unsupported (fleet-qx9c)
	IDs       []string `json:"ids,omitempty"`        // fleet-db: unsupported (fleet-qx9c)
	ParentID  string   `json:"parent_id,omitempty"`
	Limit     int      `json:"limit,omitempty"`

	// Full-text search.
	Query               string `json:"query,omitempty"`                // fleet-db: unsupported (fleet-qx9c)
	TitleContains       string `json:"title_contains,omitempty"`       // fleet-db: unsupported (fleet-qx9c)
	DescriptionContains string `json:"description_contains,omitempty"` // fleet-db: unsupported (fleet-qx9c)
	NotesContains       string `json:"notes_contains,omitempty"`       // fleet-db: unsupported (fleet-qx9c)

	// Date range filters (ISO 8601 strings).
	CreatedAfter  string `json:"created_after,omitempty"`  // fleet-db: unsupported (fleet-qx9c)
	CreatedBefore string `json:"created_before,omitempty"` // fleet-db: unsupported (fleet-qx9c)
	UpdatedAfter  string `json:"updated_after,omitempty"`
	UpdatedBefore string `json:"updated_before,omitempty"`
	ClosedAfter   string `json:"closed_after,omitempty"`  // fleet-db: unsupported (fleet-qx9c)
	ClosedBefore  string `json:"closed_before,omitempty"` // fleet-db: unsupported (fleet-qx9c)

	// Empty/null checks.
	EmptyDescription bool `json:"empty_description,omitempty"` // fleet-db: unsupported (fleet-qx9c)
	NoAssignee       bool `json:"no_assignee,omitempty"`       // fleet-db: unsupported (fleet-qx9c)
	NoLabels         bool `json:"no_labels,omitempty"`         // fleet-db: unsupported (fleet-qx9c)

	// Priority range.
	PriorityMin *int `json:"priority_min,omitempty"` // fleet-db: unsupported (fleet-qx9c)
	PriorityMax *int `json:"priority_max,omitempty"` // fleet-db: unsupported (fleet-qx9c)

	// Special filters.
	Pinned           *bool  `json:"pinned,omitempty"`            // fleet-db: unsupported (fleet-qx9c)
	Ephemeral        *bool  `json:"ephemeral,omitempty"`         // fleet-db: unsupported (fleet-qx9c)
	IncludeTemplates bool   `json:"include_templates,omitempty"` // fleet-db: unsupported (fleet-qx9c)
	MolType          string `json:"mol_type,omitempty"`          // fleet-db: unsupported (fleet-qx9c)

	// Exclusion filters.
	ExcludeStatus []string `json:"exclude_status,omitempty"` // fleet-db: unsupported (fleet-qx9c)
	ExcludeTypes  []string `json:"exclude_types,omitempty"`  // fleet-db: unsupported (fleet-qx9c)

	// Scheduling filters.
	Deferred    bool   `json:"deferred,omitempty"`     // fleet-db: unsupported (fleet-qx9c)
	DeferAfter  string `json:"defer_after,omitempty"`  // fleet-db: unsupported (fleet-qx9c)
	DeferBefore string `json:"defer_before,omitempty"` // fleet-db: unsupported (fleet-qx9c)
	DueAfter    string `json:"due_after,omitempty"`    // fleet-db: unsupported (fleet-qx9c)
	DueBefore   string `json:"due_before,omitempty"`   // fleet-db: unsupported (fleet-qx9c)
	Overdue     bool   `json:"overdue,omitempty"`      // fleet-db: unsupported (fleet-qx9c)

	// Multi-repo.
	SourceRepos []string `json:"source_repos,omitempty"`

	// Performance hints.
	AllowStale bool `json:"allow_stale,omitempty"` // fleet-db: unsupported (fleet-qx9c)
}

// ReadyOpts configures the canonical Ready query.
type ReadyOpts struct {
	Assignee    string   `json:"assignee,omitempty"`
	Unassigned  bool     `json:"unassigned,omitempty"`
	Priority    *int     `json:"priority,omitempty"`
	Type        string   `json:"type,omitempty"`
	ParentID    string   `json:"parent_id,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	SortPolicy  string   `json:"sort_policy,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	LabelsAny   []string `json:"labels_any,omitempty"`
	MolType     string   `json:"mol_type,omitempty"`
	SourceRepos []string `json:"source_repos,omitempty"`
}

// DeferredOpts configures the canonical Deferred query. Backends may apply
// narrowing filters client-side when the upstream deferred view is unfiltered.
type DeferredOpts struct {
	Assignee    string   `json:"assignee,omitempty"`
	Priority    *int     `json:"priority,omitempty"`
	Type        string   `json:"type,omitempty"`
	ParentID    string   `json:"parent_id,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	SourceRepos []string `json:"source_repos,omitempty"`
	Limit       int      `json:"limit,omitempty"`
}

// BlockedOpts configures the canonical Blocked query.
type BlockedOpts struct {
	ParentID    string   `json:"parent_id,omitempty"`
	Assignee    string   `json:"assignee,omitempty"`
	Priority    *int     `json:"priority,omitempty"`
	Type        string   `json:"type,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	SourceRepos []string `json:"source_repos,omitempty"`
	Limit       int      `json:"limit,omitempty"`
}

// CountOpts configures the Count query.
type CountOpts struct {
	// Supports the same filters as ListOpts for scoping the count.
	Status    string   `json:"status,omitempty"`
	Priority  *int     `json:"priority,omitempty"`
	IssueType string   `json:"issue_type,omitempty"`
	Assignee  string   `json:"assignee,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	LabelsAny []string `json:"labels_any,omitempty"`
	IDs       []string `json:"ids,omitempty"`

	// Pattern matching.
	TitleContains       string `json:"title_contains,omitempty"`
	DescriptionContains string `json:"description_contains,omitempty"`
	NotesContains       string `json:"notes_contains,omitempty"`

	// Date ranges.
	CreatedAfter  string `json:"created_after,omitempty"`
	CreatedBefore string `json:"created_before,omitempty"`
	UpdatedAfter  string `json:"updated_after,omitempty"`
	UpdatedBefore string `json:"updated_before,omitempty"`
	ClosedAfter   string `json:"closed_after,omitempty"`
	ClosedBefore  string `json:"closed_before,omitempty"`

	// Empty/null checks.
	EmptyDescription bool `json:"empty_description,omitempty"`
	NoAssignee       bool `json:"no_assignee,omitempty"`
	NoLabels         bool `json:"no_labels,omitempty"`

	// Priority range.
	PriorityMin *int `json:"priority_min,omitempty"`
	PriorityMax *int `json:"priority_max,omitempty"`

	// Grouping (returns grouped counts when set).
	GroupBy string `json:"group_by,omitempty"`

	// Multi-repo.
	SourceRepos []string `json:"source_repos,omitempty"`
}

// ---------------------------------------------------------------------------
// Section 3: Param structs (mutation inputs)
// ---------------------------------------------------------------------------

// CreateParams contains fields for creating a new issue.
type CreateParams struct {
	ID                 string   `json:"id,omitempty"`
	Parent             string   `json:"parent,omitempty"`
	Title              string   `json:"title"`
	Description        string   `json:"description,omitempty"`
	Status             string   `json:"status,omitempty"`
	IssueType          string   `json:"issue_type"`
	Priority           int      `json:"priority"`
	Design             string   `json:"design,omitempty"`
	AcceptanceCriteria string   `json:"acceptance_criteria,omitempty"`
	Notes              string   `json:"notes,omitempty"`
	Assignee           string   `json:"assignee,omitempty"`
	Owner              string   `json:"owner,omitempty"`
	CreatedBy          string   `json:"created_by,omitempty"`
	ExternalRef        string   `json:"external_ref,omitempty"`
	EstimatedMinutes   *int     `json:"estimated_minutes,omitempty"`
	Labels             []string `json:"labels,omitempty"`
	Dependencies       []string `json:"dependencies,omitempty"`
	SourceRepo         string   `json:"source_repo,omitempty"`
	DueAt              string   `json:"due_at,omitempty"`
	DeferUntil         string   `json:"defer_until,omitempty"`

	// IdempotencyKey rides as the X-Idempotency-Key HTTP header on create
	// requests (fleet-db's strict JSON decode rejects unknown body fields,
	// so it must never serialize into any wire body — hence json:"-").
	IdempotencyKey string `json:"-"`
	// Force bypasses fleet-db's soft-duplicate create guard
	// (X-Idempotency-Force header). Header-only for the same reason.
	Force bool `json:"-"`
}

// FleetCreateBody converts CreateParams to the POST /issues body shape
// fleet-db's CreateIssueRequest expects. fleet-db's strict JSON validation
// rejects unknown fields, so loom-only fields are dropped rather than
// shipped as-is.
// FleetBackend.Create retries without external_ref for deployed fleet-dbs
// whose create schema predates that field, then applies it via PATCH.
//
// Field renames vs CreateParams:
//   - "issue_type"  → "type"
//   - "parent"      → "parent_id"
//   - "owner" stays "owner" but fleet-db expects *string (we send the
//     scalar value directly; omitempty handles the unset case)
//   - "source_repo" → "repo"
//
// Dropped (no equivalent on fleet-db's CreateIssueRequest):
//   - id, acceptance_criteria, created_by,
//     estimated_minutes, dependencies
//
// If any of those need round-tripping, file a fleet-db ticket to extend
// the CreateIssueRequest schema rather than smuggling them through here.
//
// This lives on CreateParams (not in the fleet package) because it is shared
// by two consumers that must agree byte-for-byte: the fleet backend builds
// the wire request from it, and cli/data hashes it (plus a date bucket) into
// the default X-Idempotency-Key — fleet-db 409s when a key is reused with a
// different body, so the key must be derived from the exact wire bytes.
// cli/data is depguard-banned from importing the fleet package.
func (p CreateParams) FleetCreateBody() map[string]interface{} {
	req := make(map[string]interface{})
	setNonEmptyMapStr(req, "title", p.Title)
	setNonEmptyMapStr(req, "description", p.Description)
	setNonEmptyMapStr(req, "status", p.Status)
	if p.Priority != 0 {
		req["priority"] = p.Priority
	}
	setNonEmptyMapStr(req, "type", p.IssueType)
	setNonEmptyMapStr(req, "assignee", p.Assignee)
	setNonEmptyMapStr(req, "owner", p.Owner)
	if len(p.Labels) > 0 {
		req["labels"] = p.Labels
	}
	setNonEmptyMapStr(req, "parent_id", p.Parent)
	setNonEmptyMapStr(req, "repo", p.SourceRepo)
	setNonEmptyMapStr(req, "design", p.Design)
	setNonEmptyMapStr(req, "notes", p.Notes)
	setNonEmptyMapStr(req, "external_ref", p.ExternalRef)
	setNonEmptyMapStr(req, "defer_until", p.DeferUntil)
	setNonEmptyMapStr(req, "due_at", p.DueAt)
	return req
}

// FleetCreateIdempotencyKey derives the default create key from a UTC date
// bucket and the exact fleet-db request body.
func (p CreateParams) FleetCreateIdempotencyKey(now time.Time) (string, error) {
	body, err := json.Marshal(p.FleetCreateBody())
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(now.UTC().Format("20060102")))
	h.Write([]byte{0})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// setNonEmptyMapStr sets m[key] = val if val is non-empty.
func setNonEmptyMapStr(m map[string]interface{}, key, val string) {
	if val != "" {
		m[key] = val
	}
}

// IdempotencyHeaders returns the idempotency HTTP headers for a create
// request; empty map when neither is set. Shared by the API and fleet
// backends so the wire header names live in one place.
func (p CreateParams) IdempotencyHeaders() map[string]string {
	h := map[string]string{}
	if p.IdempotencyKey != "" {
		h["X-Idempotency-Key"] = p.IdempotencyKey
	}
	if p.Force {
		h["X-Idempotency-Force"] = "true"
	}
	return h
}

// UpdateParams contains fields for updating an issue. Pointer fields
// distinguish "set to value" from "don't change" (nil = don't change).
// When Claim is true, the atomic claim operation takes precedence over
// an explicit Status field in the same request.
type UpdateParams struct {
	Title              *string  `json:"title,omitempty"`
	Description        *string  `json:"description,omitempty"`
	Status             *string  `json:"status,omitempty"`
	Priority           *int     `json:"priority,omitempty"`
	Design             *string  `json:"design,omitempty"`
	DesignFormat       *string  `json:"design_format,omitempty"`
	AcceptanceCriteria *string  `json:"acceptance_criteria,omitempty"`
	Notes              *string  `json:"notes,omitempty"`
	Assignee           *string  `json:"assignee,omitempty"`
	Owner              *string  `json:"owner,omitempty"`
	IssueType          *string  `json:"issue_type,omitempty"`
	ExternalRef        *string  `json:"external_ref,omitempty"`
	EstimatedMinutes   *int     `json:"estimated_minutes,omitempty"`
	AddLabels          []string `json:"add_labels,omitempty"`
	RemoveLabels       []string `json:"remove_labels,omitempty"`
	SetLabels          []string `json:"set_labels,omitempty"`
	Parent             *string  `json:"parent,omitempty"`
	AgentState         *string  `json:"agent_state,omitempty"`
	DueAt              *string  `json:"due_at,omitempty"`
	DeferUntil         *string  `json:"defer_until,omitempty"`
	Claim              bool     `json:"claim,omitempty"`
}

// ClaimIssueParams contains fields for atomically claiming an issue.
// OwnerActor is the actor that should own the claim lock. When empty,
// backends use their configured caller identity. Backends that cannot
// represent a non-empty OwnerActor must return KindNotImplemented or
// KindValidation before mutating.
type ClaimIssueParams struct {
	ID         string        `json:"id,omitempty"`
	LockTTL    time.Duration `json:"-"`
	OwnerActor string        `json:"owner_actor,omitempty"`
}

// CloseParams contains fields for closing an issue.
type CloseParams struct {
	Reason      string `json:"reason,omitempty"`
	Session     string `json:"session,omitempty"`
	SuggestNext bool   `json:"suggest_next,omitempty"`
	Force       bool   `json:"force,omitempty"`
}

// ReopenParams contains fields for reopening a closed issue.
type ReopenParams struct {
	Reason string `json:"reason,omitempty"`
}

// DeleteParams contains fields for deleting issue(s).
// At least one ID is required; implementations return a validation error
// if IDs is empty.
type DeleteParams struct {
	IDs     []string `json:"ids"`
	Reason  string   `json:"reason,omitempty"`
	Force   bool     `json:"force,omitempty"`
	Cascade bool     `json:"cascade,omitempty"`
}

// DepAddParams contains fields for adding a dependency between issues.
type DepAddParams struct {
	FromID  string `json:"from_id"`
	ToID    string `json:"to_id"`
	DepType string `json:"dep_type"`
}

// DepRemoveParams contains fields for removing a dependency between issues.
type DepRemoveParams struct {
	FromID  string `json:"from_id"`
	ToID    string `json:"to_id"`
	DepType string `json:"dep_type,omitempty"`
}

// CommentAddParams contains fields for adding a comment to an issue.
type CommentAddParams struct {
	IssueID string `json:"issue_id"`
	Author  string `json:"author"`
	Text    string `json:"text"`
}

// BatchOp represents a single operation in a batch request.
// Operation is the method name (e.g., "create", "update", "close").
// Args is the JSON-encoded operation-specific parameters.
type BatchOp struct {
	Operation string          `json:"operation"`
	Args      json.RawMessage `json:"args"`
}

// BatchResult represents the outcome of a single operation in a batch.
// The method-level error from Batch is reserved for transport failures;
// individual operation failures are recorded in each BatchResult.
type BatchResult struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// Section 4: Mutation type constants
// ---------------------------------------------------------------------------

// Mutation event type constants. These mirror rpc.Mutation* values
// so that backends producing MutationData use consistent labels.
const (
	MutationCreate        = "create"
	MutationUpdate        = "update"
	MutationDelete        = "delete"
	MutationComment       = "comment"
	MutationBonded        = "bonded"
	MutationSquashed      = "squashed"
	MutationBurned        = "burned"
	MutationStatus        = "status"
	MutationRefresh       = "refresh"
	MutationSessionChange = "session_change"
)
