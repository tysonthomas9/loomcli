package entity

import (
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/types"
)

// Issue represents a trackable work item.
// Fields are organized into logical groups for maintainability.
type Issue struct {
	// ===== Core Identification =====
	ID string `json:"id"`

	// ===== Issue Content =====
	Title              string `json:"title"`
	Description        string `json:"description,omitempty"`
	Design             string `json:"design,omitempty"`
	DesignArtifactID   string `json:"design_artifact_id,omitempty"`
	DesignFormat       string `json:"design_format,omitempty"`
	HasDesign          bool   `json:"has_design"`
	AcceptanceCriteria string `json:"acceptance_criteria,omitempty"`
	Notes              string `json:"notes,omitempty"`

	// ===== Status & Workflow =====
	Status    IssueStatus `json:"status,omitempty"`
	Priority  int         `json:"priority"` // No omitempty: 0 is valid (P0/critical)
	IssueType IssueType   `json:"issue_type,omitempty"`

	// ===== Assignment =====
	Assignee         string `json:"assignee,omitempty"`
	Owner            string `json:"owner,omitempty"`
	EstimatedMinutes *int   `json:"estimated_minutes,omitempty"`

	// ===== Timestamps =====
	CreatedAt       time.Time  `json:"created_at"`
	CreatedBy       string     `json:"created_by,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ClosedAt        *time.Time `json:"closed_at,omitempty"`
	CloseReason     string     `json:"close_reason,omitempty"`
	ClosedBySession string     `json:"closed_by_session,omitempty"`

	// ===== Time-Based Scheduling =====
	DueAt      *time.Time `json:"due_at,omitempty"`
	DeferUntil *time.Time `json:"defer_until,omitempty"`

	// ===== External Integration =====
	ExternalRef          *string  `json:"external_ref,omitempty"`
	SourceSystem         string   `json:"source_system,omitempty"`
	SourceRepo           string   `json:"source_repo,omitempty"`
	PrimaryRepository    string   `json:"primary_repository,omitempty"`
	SelectedRepositories []string `json:"selected_repositories,omitempty"`

	// ===== Relational Data =====
	Labels       []string      `json:"labels,omitempty"`
	Dependencies []*Dependency `json:"dependencies,omitempty"`
	Comments     []*Comment    `json:"comments,omitempty"`

	// ===== Tombstone Fields =====
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	DeletedBy    string     `json:"deleted_by,omitempty"`
	DeleteReason string     `json:"delete_reason,omitempty"`
	OriginalType string     `json:"original_type,omitempty"`

	// ===== Messaging Fields =====
	Sender    string `json:"sender,omitempty"`
	Ephemeral bool   `json:"ephemeral,omitempty"`

	// ===== Context Markers =====
	Pinned     bool `json:"pinned,omitempty"`
	IsTemplate bool `json:"is_template,omitempty"`

	// ===== Bonding Fields =====
	BondedFrom []BondRef `json:"bonded_from,omitempty"`

	// ===== HOP Fields =====
	Creator      *EntityRef   `json:"creator,omitempty"`
	Validations  []Validation `json:"validations,omitempty"`
	QualityScore *float32     `json:"quality_score,omitempty"`
	Crystallizes bool         `json:"crystallizes,omitempty"`

	// ===== Gate Fields =====
	AwaitType string        `json:"await_type,omitempty"`
	AwaitID   string        `json:"await_id,omitempty"`
	Timeout   time.Duration `json:"timeout,omitempty"`
	Waiters   []string      `json:"waiters,omitempty"`

	// ===== Slot Fields =====
	Holder string `json:"holder,omitempty"`

	// ===== Source Tracing Fields =====
	SourceFormula  string `json:"source_formula,omitempty"`
	SourceLocation string `json:"source_location,omitempty"`

	// ===== Agent Identity Fields =====
	AgentState   AgentState `json:"agent_state,omitempty"`
	LastActivity *time.Time `json:"last_activity,omitempty"`
	RoleType     string     `json:"role_type,omitempty"`
	Rig          string     `json:"rig,omitempty"`

	// ===== Molecule Type Fields =====
	MolType MolType `json:"mol_type,omitempty"`

	// ===== Work Type Fields =====
	WorkType WorkType `json:"work_type,omitempty"`

	// ===== Event Fields =====
	EventKind string `json:"event_kind,omitempty"`
	Actor     string `json:"actor,omitempty"`
	Target    string `json:"target,omitempty"`
	Payload   string `json:"payload,omitempty"`
}

// DefaultTombstoneTTL is the default time-to-live for tombstones (30 days).
const DefaultTombstoneTTL = 30 * 24 * time.Hour

// MinTombstoneTTL is the minimum allowed TTL (7 days) to prevent data loss.
const MinTombstoneTTL = 7 * 24 * time.Hour

// ClockSkewGrace is added to TTL to handle clock drift between machines.
const ClockSkewGrace = 1 * time.Hour

// IsTombstone returns true if the issue has been soft-deleted.
func (i *Issue) IsTombstone() bool {
	return i.Status == StatusTombstone
}

// IsExpired returns true if the tombstone has exceeded its TTL.
// Non-tombstone issues always return false.
// ttl is the configured TTL duration:
//   - If zero, DefaultTombstoneTTL (30 days) is used
//   - If negative, the tombstone is immediately expired (for --hard mode)
//   - If positive, ClockSkewGrace is added only for TTLs > 1 hour
func (i *Issue) IsExpired(ttl time.Duration) bool {
	if !i.IsTombstone() {
		return false
	}
	if i.DeletedAt == nil {
		return false
	}
	if ttl < 0 {
		return true
	}
	if ttl == 0 {
		ttl = DefaultTombstoneTTL
	}
	effectiveTTL := ttl
	if ttl > ClockSkewGrace {
		effectiveTTL = ttl + ClockSkewGrace
	}
	expirationTime := i.DeletedAt.Add(effectiveTTL)
	return time.Now().After(expirationTime)
}

// Validate checks if the issue has valid field values (built-in statuses only).
func (i *Issue) Validate() error {
	return i.ValidateWithCustom(nil, nil)
}

// ValidateWithCustomStatuses checks if the issue has valid field values,
// allowing custom statuses in addition to built-in ones.
func (i *Issue) ValidateWithCustomStatuses(customStatuses []string) error {
	return i.ValidateWithCustom(customStatuses, nil)
}

// ValidateWithCustom checks if the issue has valid field values,
// allowing custom statuses and types in addition to built-in ones.
func (i *Issue) ValidateWithCustom(customStatuses, customTypes []string) error {
	if len(i.Title) == 0 {
		return fmt.Errorf("title is required")
	}
	if len(i.Title) > 500 {
		return fmt.Errorf("title must be 500 characters or less (got %d)", len(i.Title))
	}
	if i.Priority < 0 || i.Priority > 4 {
		return fmt.Errorf("priority must be between 0 and 4 (got %d)", i.Priority)
	}
	if !i.Status.IsValidWithCustom(customStatuses) {
		return fmt.Errorf("invalid status: %s", i.Status)
	}
	if !i.IssueType.IsValidWithCustom(customTypes) {
		return fmt.Errorf("invalid issue type: %s", i.IssueType)
	}
	if i.EstimatedMinutes != nil && *i.EstimatedMinutes < 0 {
		return fmt.Errorf("estimated_minutes cannot be negative")
	}
	if i.Status == StatusClosed && i.ClosedAt == nil {
		return fmt.Errorf("closed issues must have closed_at timestamp")
	}
	if i.Status != StatusClosed && i.Status != StatusTombstone && i.ClosedAt != nil {
		return fmt.Errorf("non-closed issues cannot have closed_at timestamp")
	}
	if i.Status == StatusTombstone && i.DeletedAt == nil {
		return fmt.Errorf("tombstone issues must have deleted_at timestamp")
	}
	if i.Status != StatusTombstone && i.DeletedAt != nil {
		return fmt.Errorf("non-tombstone issues cannot have deleted_at timestamp")
	}
	if !i.AgentState.IsValid() {
		return fmt.Errorf("invalid agent state: %s", i.AgentState)
	}
	return nil
}

// SetDefaults applies default values for fields omitted during JSONL import.
func (i *Issue) SetDefaults() {
	if i.Status == "" {
		i.Status = StatusOpen
	}
	if i.IssueType == "" {
		i.IssueType = TypeTask
	}
}

// IsCompound returns true if this issue is a compound (bonded from multiple sources).
func (i *Issue) IsCompound() bool {
	return len(i.BondedFrom) > 0
}

// GetConstituents returns the BondRefs for this compound's constituent protos.
// Returns nil for non-compound issues.
func (i *Issue) GetConstituents() []BondRef {
	return i.BondedFrom
}

// IssueStatus represents the current state of an issue. It is types.Status under
// a domain-local name: the same nine values, spelled the same way on the wire.
// Declaring it in terms of types.Status keeps one vocabulary in the repo, so a
// tenth status is added in exactly one place.
type IssueStatus types.Status

// Issue status constants, derived from the canonical vocabulary rather than
// respelled, so the two names cannot drift apart.
const (
	StatusOpen       = IssueStatus(types.StatusOpen)
	StatusInProgress = IssueStatus(types.StatusInProgress)
	StatusBlocked    = IssueStatus(types.StatusBlocked)
	StatusDeferred   = IssueStatus(types.StatusDeferred)
	StatusReview     = IssueStatus(types.StatusReview)
	StatusClosed     = IssueStatus(types.StatusClosed)
	StatusTombstone  = IssueStatus(types.StatusTombstone)
	StatusPinned     = IssueStatus(types.StatusPinned)
	StatusHooked     = IssueStatus(types.StatusHooked)
)

// builtinStatuses mirrors types.builtinStatuses, in the same order. It exists so
// the parity test can walk entity's side of the vocabulary instead of re-listing
// it and then quietly missing the next status somebody adds here by hand.
var builtinStatuses = [...]IssueStatus{
	StatusOpen, StatusInProgress, StatusBlocked, StatusDeferred,
	StatusReview, StatusClosed, StatusTombstone, StatusPinned, StatusHooked,
}

// IsValid checks if the status value is valid (built-in statuses only).
// Empty string is considered valid (caller handles defaulting).
//
// That empty arm is the one deliberate difference from types.Status.IsValid,
// which rejects "" — do not "fix" it away in either direction. An entity value
// is a domain record mid-construction: JSONL import and the DTO layer both build
// an Issue before SetDefaults fills the status in, so "" means "not stated yet"
// and Validate has to let it through. types.Status guards the fleet wire path and
// whitelists built on top of it, most of all types.ValidateSettableStatus, where
// "" is a caller mistake — a whitelist that admitted it would pass the validity
// guard, match no case, and report the empty status as settable.
//
// The nine real values are checked by types.Status.IsValid, not by a second
// switch here, so this stays one visible line of divergence instead of a copy
// that can rot.
func (s IssueStatus) IsValid() bool {
	return s == "" || types.Status(s).IsValid()
}

// IsValidWithCustom checks if the status is valid, including custom statuses.
func (s IssueStatus) IsValidWithCustom(customStatuses []string) bool {
	if s.IsValid() {
		return true
	}
	for _, custom := range customStatuses {
		if string(s) == custom {
			return true
		}
	}
	return false
}

// IssueType categorizes the kind of work.
type IssueType string

// Core work type constants.
const (
	TypeBug     IssueType = "bug"
	TypeFeature IssueType = "feature"
	TypeTask    IssueType = "task"
	TypeEpic    IssueType = "epic"
	TypeChore   IssueType = "chore"
)

// IsValid checks if the issue type is a core work type.
// Empty string is considered valid (caller handles defaulting).
func (t IssueType) IsValid() bool {
	switch t {
	case TypeBug, TypeFeature, TypeTask, TypeEpic, TypeChore, "":
		return true
	}
	return false
}

// IsBuiltIn returns true if the type is a built-in type (same as IsValid for non-empty values).
func (t IssueType) IsBuiltIn() bool {
	return t.IsValid()
}

// IsValidWithCustom checks if the issue type is valid, including custom types.
func (t IssueType) IsValidWithCustom(customTypes []string) bool {
	if t.IsValid() {
		return true
	}
	for _, custom := range customTypes {
		if string(t) == custom {
			return true
		}
	}
	return false
}

// Normalize maps issue type aliases to their canonical form.
// Case-insensitive: "enhancement" and "feat" map to TypeFeature.
func (t IssueType) Normalize() IssueType {
	switch strings.ToLower(string(t)) {
	case "enhancement", "feat":
		return TypeFeature
	default:
		return t
	}
}

// AgentState represents the self-reported state of an agent.
type AgentState string

// Agent state constants.
const (
	StateIdle     AgentState = "idle"
	StateSpawning AgentState = "spawning"
	StateRunning  AgentState = "running"
	StateWorking  AgentState = "working"
	StateStuck    AgentState = "stuck"
	StateDone     AgentState = "done"
	StateStopped  AgentState = "stopped"
	StateDead     AgentState = "dead"
)

// IsValid checks if the agent state value is valid.
// Empty string is valid for non-agent records.
func (s AgentState) IsValid() bool {
	switch s {
	case StateIdle, StateSpawning, StateRunning, StateWorking, StateStuck,
		StateDone, StateStopped, StateDead, "":
		return true
	}
	return false
}

// MolType categorizes the molecule type for swarm coordination.
type MolType string

// MolType constants.
const (
	MolTypeSwarm  MolType = "swarm"
	MolTypePatrol MolType = "patrol"
	MolTypeWork   MolType = "work"
)

// IsValid checks if the mol type value is valid.
// Empty string is valid (defaults to work).
func (m MolType) IsValid() bool {
	switch m {
	case MolTypeSwarm, MolTypePatrol, MolTypeWork, "":
		return true
	}
	return false
}

// WorkType categorizes how work assignment operates for a bead.
type WorkType string

// WorkType constants.
const (
	WorkTypeMutex           WorkType = "mutex"
	WorkTypeOpenCompetition WorkType = "open_competition"
)

// IsValid checks if the work type value is valid.
// Empty string is valid (defaults to mutex).
func (w WorkType) IsValid() bool {
	switch w {
	case WorkTypeMutex, WorkTypeOpenCompetition, "":
		return true
	}
	return false
}

// EntityRef is a structured reference to an entity (human, agent, or org).
// Can be rendered as a URI: entity://hop/<platform>/<org>/<id>
type EntityRef struct {
	Name     string `json:"name,omitempty"`
	Platform string `json:"platform,omitempty"`
	Org      string `json:"org,omitempty"`
	ID       string `json:"id,omitempty"`
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

// ParseEntityURI parses a HOP entity URI into an EntityRef.
// Format: entity://hop/<platform>/<org>/<id>
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

// Validation records who validated/approved work completion.
type Validation struct {
	Validator *EntityRef `json:"validator"`
	Outcome   string     `json:"outcome"`
	Timestamp time.Time  `json:"timestamp"`
	Score     *float32   `json:"score,omitempty"`
}

// Validation outcome constants.
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

// BondRef tracks compound molecule lineage.
type BondRef struct {
	SourceID  string `json:"source_id"`
	BondType  string `json:"bond_type"`
	BondPoint string `json:"bond_point,omitempty"`
}

// Bond type constants for compound molecules.
const (
	BondTypeSequential  = "sequential"
	BondTypeParallel    = "parallel"
	BondTypeConditional = "conditional"
	BondTypeRoot        = "root"
)
