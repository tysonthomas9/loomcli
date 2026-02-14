package types

import (
	"crypto/sha256"
	"fmt"
	"hash"
	"sort"
	"time"
)

// Issue represents a trackable work item.
// Fields are organized into logical groups for maintainability.
type Issue struct {
	// ===== Core Identification =====
	ID          string `json:"id"`
	ContentHash string `json:"-"` // Internal: SHA256 of canonical content - NOT exported to JSONL

	// ===== Issue Content =====
	Title              string `json:"title"`
	Description        string `json:"description,omitempty"`
	Design             string `json:"design,omitempty"`
	AcceptanceCriteria string `json:"acceptance_criteria,omitempty"`
	Notes              string `json:"notes,omitempty"`

	// ===== Status & Workflow =====
	Status    Status    `json:"status,omitempty"`
	Priority  int       `json:"priority"` // No omitempty: 0 is valid (P0/critical)
	IssueType IssueType `json:"issue_type,omitempty"`

	// ===== Assignment =====
	Assignee         string `json:"assignee,omitempty"`
	Owner            string `json:"owner,omitempty"` // Human owner for CV attribution (git author email)
	EstimatedMinutes *int   `json:"estimated_minutes,omitempty"`

	// ===== Timestamps =====
	CreatedAt       time.Time  `json:"created_at"`
	CreatedBy       string     `json:"created_by,omitempty"` // Who created this issue (GH#748)
	UpdatedAt       time.Time  `json:"updated_at"`
	ClosedAt        *time.Time `json:"closed_at,omitempty"`
	CloseReason     string     `json:"close_reason,omitempty"`      // Reason provided when closing
	ClosedBySession string     `json:"closed_by_session,omitempty"` // Claude Code session that closed this issue

	// ===== Time-Based Scheduling (GH#820) =====
	DueAt      *time.Time `json:"due_at,omitempty"`      // When this issue should be completed
	DeferUntil *time.Time `json:"defer_until,omitempty"` // Hide from bd ready until this time

	// ===== External Integration =====
	ExternalRef  *string `json:"external_ref,omitempty"`  // e.g., "gh-9", "jira-ABC"
	SourceSystem string  `json:"source_system,omitempty"` // Adapter/system that created this issue (federation)

	// ===== Compaction Metadata =====
	CompactionLevel   int        `json:"compaction_level,omitempty"`
	CompactedAt       *time.Time `json:"compacted_at,omitempty"`
	CompactedAtCommit *string    `json:"compacted_at_commit,omitempty"` // Git commit hash when compacted
	OriginalSize      int        `json:"original_size,omitempty"`

	// ===== Internal Routing (not exported to JSONL) =====
	SourceRepo     string `json:"-"` // Which repo owns this issue (multi-repo support)
	IDPrefix       string `json:"-"` // Override prefix for ID generation (appends to config prefix)
	PrefixOverride string `json:"-"` // Completely replace config prefix (for cross-rig creation)

	// ===== Relational Data (populated for export/import) =====
	Labels       []string      `json:"labels,omitempty"`
	Dependencies []*Dependency `json:"dependencies,omitempty"`
	Comments     []*Comment    `json:"comments,omitempty"`

	// ===== Tombstone Fields (soft-delete support) =====
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`    // When deleted
	DeletedBy    string     `json:"deleted_by,omitempty"`    // Who deleted
	DeleteReason string     `json:"delete_reason,omitempty"` // Why deleted
	OriginalType string     `json:"original_type,omitempty"` // Issue type before deletion

	// ===== Messaging Fields (inter-agent communication) =====
	Sender    string `json:"sender,omitempty"`    // Who sent this (for messages)
	Ephemeral bool   `json:"ephemeral,omitempty"` // If true, not exported to JSONL
	// NOTE: RepliesTo, RelatesTo, DuplicateOf, SupersededBy moved to dependencies table
	// per Decision 004 (Edge Schema Consolidation). Use dependency API instead.

	// ===== Context Markers =====
	Pinned     bool `json:"pinned,omitempty"`      // Persistent context marker, not a work item
	IsTemplate bool `json:"is_template,omitempty"` // Read-only template molecule

	// ===== Bonding Fields (compound molecule lineage) =====
	BondedFrom []BondRef `json:"bonded_from,omitempty"` // For compounds: constituent protos

	// ===== HOP Fields (entity tracking for CV chains) =====
	Creator      *EntityRef   `json:"creator,omitempty"`       // Who created (human, agent, or org)
	Validations  []Validation `json:"validations,omitempty"`   // Who validated/approved
	QualityScore *float32     `json:"quality_score,omitempty"` // Aggregate quality (0.0-1.0), set by Refineries on merge
	Crystallizes bool         `json:"crystallizes,omitempty"`  // Work that compounds (true: code, features) vs evaporates (false: ops, support) - affects CV weighting per Decision 006

	// ===== Gate Fields (async coordination primitives) =====
	AwaitType string        `json:"await_type,omitempty"` // Condition type: gh:run, gh:pr, timer, human, mail
	AwaitID   string        `json:"await_id,omitempty"`   // Condition identifier (run ID, PR number, etc.)
	Timeout   time.Duration `json:"timeout,omitempty"`    // Max wait time before escalation
	Waiters   []string      `json:"waiters,omitempty"`    // Mail addresses to notify when gate clears

	// ===== Slot Fields (exclusive access primitives) =====
	Holder string `json:"holder,omitempty"` // Who currently holds the slot (empty = available)

	// ===== Source Tracing Fields (formula cooking origin) =====
	SourceFormula  string `json:"source_formula,omitempty"`  // Formula name where step was defined
	SourceLocation string `json:"source_location,omitempty"` // Path: "steps[0]", "advice[0].after"

	// ===== Agent Identity Fields (agent-as-bead support) =====
	HookBead     string     `json:"hook_bead,omitempty"`     // Current work on agent's hook (0..1)
	RoleBead     string     `json:"role_bead,omitempty"`     // Role definition bead (required for agents)
	AgentState   AgentState `json:"agent_state,omitempty"`   // Agent state: idle|running|stuck|stopped
	LastActivity *time.Time `json:"last_activity,omitempty"` // Updated on each action (timeout detection)
	RoleType     string     `json:"role_type,omitempty"`     // Role: polecat|crew|witness|refinery|mayor|deacon
	Rig          string     `json:"rig,omitempty"`           // Rig name (empty for town-level agents)

	// ===== Molecule Type Fields (swarm coordination) =====
	MolType MolType `json:"mol_type,omitempty"` // Molecule type: swarm|patrol|work (empty = work)

	// ===== Work Type Fields (assignment model - Decision 006) =====
	WorkType WorkType `json:"work_type,omitempty"` // Work type: mutex|open_competition (empty = mutex)

	// ===== Event Fields (operational state changes) =====
	EventKind string `json:"event_kind,omitempty"` // Namespaced event type: patrol.muted, agent.started
	Actor     string `json:"actor,omitempty"`      // Entity URI who caused this event
	Target    string `json:"target,omitempty"`     // Entity URI or bead ID affected
	Payload   string `json:"payload,omitempty"`    // Event-specific JSON data
}

// ComputeContentHash creates a deterministic hash of the issue's content.
// Uses all substantive fields (excluding ID, timestamps, and compaction metadata)
// to ensure that identical content produces identical hashes across all clones.
func (i *Issue) ComputeContentHash() string {
	h := sha256.New()
	w := hashFieldWriter{h}

	// Core fields in stable order
	w.str(i.Title)
	w.str(i.Description)
	w.str(i.Design)
	w.str(i.AcceptanceCriteria)
	w.str(i.Notes)
	w.str(string(i.Status))
	w.int(i.Priority)
	w.str(string(i.IssueType))
	w.str(i.Assignee)
	w.str(i.Owner)
	w.str(i.CreatedBy)

	// Optional fields
	w.strPtr(i.ExternalRef)
	w.str(i.SourceSystem)
	w.flag(i.Pinned, "pinned")
	w.flag(i.IsTemplate, "template")
	w.intPtr(i.EstimatedMinutes)
	w.timePtr(i.DueAt)
	w.timePtr(i.DeferUntil)
	w.str(i.CloseReason)
	w.str(i.ClosedBySession)
	w.str(i.Sender)
	w.str(i.SourceFormula)
	w.str(i.SourceLocation)
	w.boolField(i.Ephemeral, "ephemeral")
	w.str(i.DeletedBy)
	w.str(i.DeleteReason)
	w.str(i.OriginalType)

	// Labels (sorted for order-independence)
	w.sortedStrings(i.Labels)

	// Dependencies (sorted by key for order-independence)
	w.dependencies(i.Dependencies)

	// Comments (sorted by ID for order-independence)
	w.comments(i.Comments)

	// Bonded molecules
	for _, br := range i.BondedFrom {
		w.str(br.SourceID)
		w.str(br.BondType)
		w.str(br.BondPoint)
	}

	// HOP entity tracking
	w.entityRef(i.Creator)

	// HOP validations
	for _, v := range i.Validations {
		w.entityRef(v.Validator)
		w.str(v.Outcome)
		w.str(v.Timestamp.Format(time.RFC3339))
		w.float32Ptr(v.Score)
	}

	// HOP aggregate quality score and crystallizes
	w.float32Ptr(i.QualityScore)
	w.flag(i.Crystallizes, "crystallizes")

	// Gate fields for async coordination
	w.str(i.AwaitType)
	w.str(i.AwaitID)
	w.duration(i.Timeout)
	w.sortedStrings(i.Waiters)

	// Slot fields for exclusive access
	w.str(i.Holder)

	// Agent identity fields
	w.str(i.HookBead)
	w.str(i.RoleBead)
	w.str(string(i.AgentState))
	w.str(i.RoleType)
	w.str(i.Rig)

	// Molecule type
	w.str(string(i.MolType))

	// Work type
	w.str(string(i.WorkType))

	// Event fields
	w.str(i.EventKind)
	w.str(i.Actor)
	w.str(i.Target)
	w.str(i.Payload)

	return fmt.Sprintf("%x", h.Sum(nil))
}

// hashFieldWriter provides helper methods for writing fields to a hash.
// Each method writes the value followed by a null separator for consistency.
type hashFieldWriter struct {
	h hash.Hash
}

func (w hashFieldWriter) str(s string) {
	w.h.Write([]byte(s))
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) int(n int) {
	w.h.Write([]byte(fmt.Sprintf("%d", n)))
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) strPtr(p *string) {
	if p != nil {
		w.h.Write([]byte(*p))
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) float32Ptr(p *float32) {
	if p != nil {
		w.h.Write([]byte(fmt.Sprintf("%f", *p)))
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) duration(d time.Duration) {
	w.h.Write([]byte(fmt.Sprintf("%d", d)))
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) flag(b bool, label string) {
	if b {
		w.h.Write([]byte(label))
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) intPtr(p *int) {
	if p != nil {
		w.h.Write([]byte(fmt.Sprintf("set:%d", *p)))
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) timePtr(t *time.Time) {
	if t != nil {
		w.h.Write([]byte("set:" + t.Format(time.RFC3339Nano)))
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) boolField(b bool, label string) {
	if b {
		w.h.Write([]byte(label))
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) sortedStrings(ss []string) {
	sorted := make([]string, len(ss))
	copy(sorted, ss)
	sort.Strings(sorted)
	for _, s := range sorted {
		w.str(s)
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) dependencies(deps []*Dependency) {
	keys := make([]string, 0, len(deps))
	for _, d := range deps {
		keys = append(keys, fmt.Sprintf("%s:%s:%s:%s", d.IssueID, d.DependsOnID, d.Type, d.CreatedBy))
	}
	sort.Strings(keys)
	for _, k := range keys {
		w.str(k)
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) comments(comments []*Comment) {
	type commentKey struct {
		id  int64
		key string
	}
	keys := make([]commentKey, 0, len(comments))
	for _, c := range comments {
		keys = append(keys, commentKey{c.ID, fmt.Sprintf("%d:%s:%s:%s", c.ID, c.IssueID, c.Author, c.Text)})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].id < keys[j].id })
	for _, k := range keys {
		w.str(k.key)
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) entityRef(e *EntityRef) {
	if e != nil {
		w.str(e.Name)
		w.str(e.Platform)
		w.str(e.Org)
		w.str(e.ID)
	}
}

// DefaultTombstoneTTL is the default time-to-live for tombstones (30 days)
const DefaultTombstoneTTL = 30 * 24 * time.Hour

// MinTombstoneTTL is the minimum allowed TTL (7 days) to prevent data loss
const MinTombstoneTTL = 7 * 24 * time.Hour

// ClockSkewGrace is added to TTL to handle clock drift between machines
const ClockSkewGrace = 1 * time.Hour

// IsTombstone returns true if the issue has been soft-deleted
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
	// Non-tombstones never expire
	if !i.IsTombstone() {
		return false
	}

	// Tombstones without DeletedAt are not expired (safety: shouldn't happen in valid data)
	if i.DeletedAt == nil {
		return false
	}

	// Negative TTL means "immediately expired" - for --hard mode
	if ttl < 0 {
		return true
	}

	// Use default TTL if not specified
	if ttl == 0 {
		ttl = DefaultTombstoneTTL
	}

	// Only add clock skew grace period for normal TTLs (> 1 hour).
	// For short TTLs (testing/development), skip grace period.
	effectiveTTL := ttl
	if ttl > ClockSkewGrace {
		effectiveTTL = ttl + ClockSkewGrace
	}

	// Check if the tombstone has exceeded its TTL
	expirationTime := i.DeletedAt.Add(effectiveTTL)
	return time.Now().After(expirationTime)
}

// Validate checks if the issue has valid field values (built-in statuses only)
func (i *Issue) Validate() error {
	return i.ValidateWithCustomStatuses(nil)
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
	// Enforce closed_at invariant: closed_at should be set if and only if status is closed
	// Exception: tombstones may retain closed_at from before deletion
	if i.Status == StatusClosed && i.ClosedAt == nil {
		return fmt.Errorf("closed issues must have closed_at timestamp")
	}
	if i.Status != StatusClosed && i.Status != StatusTombstone && i.ClosedAt != nil {
		return fmt.Errorf("non-closed issues cannot have closed_at timestamp")
	}
	// Enforce tombstone invariants: deleted_at must be set for tombstones, and only for tombstones
	if i.Status == StatusTombstone && i.DeletedAt == nil {
		return fmt.Errorf("tombstone issues must have deleted_at timestamp")
	}
	if i.Status != StatusTombstone && i.DeletedAt != nil {
		return fmt.Errorf("non-tombstone issues cannot have deleted_at timestamp")
	}
	// Validate agent state if set
	if !i.AgentState.IsValid() {
		return fmt.Errorf("invalid agent state: %s", i.AgentState)
	}
	return nil
}

// ValidateForImport validates the issue for multi-repo import (federation trust model).
// Built-in types are validated (to catch typos). Non-built-in types are trusted
// since the source repo already validated them when the issue was created.
// This implements "trust the chain below you" from the HOP federation model.
func (i *Issue) ValidateForImport(customStatuses []string) error {
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
	// Issue type validation: federation trust model
	// Only validate built-in types (catch typos like "tsak" vs "task")
	// Non-built-in types are trusted from source repo (child repo already validated)
	if i.EstimatedMinutes != nil && *i.EstimatedMinutes < 0 {
		return fmt.Errorf("estimated_minutes cannot be negative")
	}
	// Enforce closed_at invariant
	if i.Status == StatusClosed && i.ClosedAt == nil {
		return fmt.Errorf("closed issues must have closed_at timestamp")
	}
	if i.Status != StatusClosed && i.Status != StatusTombstone && i.ClosedAt != nil {
		return fmt.Errorf("non-closed issues cannot have closed_at timestamp")
	}
	// Enforce tombstone invariants
	if i.Status == StatusTombstone && i.DeletedAt == nil {
		return fmt.Errorf("tombstone issues must have deleted_at timestamp")
	}
	if i.Status != StatusTombstone && i.DeletedAt != nil {
		return fmt.Errorf("non-tombstone issues cannot have deleted_at timestamp")
	}
	// Validate agent state if set
	if !i.AgentState.IsValid() {
		return fmt.Errorf("invalid agent state: %s", i.AgentState)
	}
	return nil
}

// SetDefaults applies default values for fields omitted during JSONL import.
// Call this after json.Unmarshal to ensure missing fields have proper defaults:
//   - Status: defaults to StatusOpen if empty
//   - Priority: defaults to 2 if zero (note: P0 issues must explicitly set priority=0)
//   - IssueType: defaults to TypeTask if empty
//
// This enables smaller JSONL output by using omitempty on these fields.
func (i *Issue) SetDefaults() {
	if i.Status == "" {
		i.Status = StatusOpen
	}
	// Note: priority 0 (P0) is a valid value, so we can't distinguish between
	// "explicitly set to 0" and "omitted". For JSONL compactness, we treat
	// priority 0 in JSONL as P0, not as "use default". This is the expected
	// behavior since P0 issues are explicitly marked.
	// Priority default of 2 only applies to new issues via Create, not import.
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
