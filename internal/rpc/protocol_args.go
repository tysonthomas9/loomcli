package rpc

import (
	"encoding/json"
	"time"
)

type CreateArgs struct {
	ID                 string   `json:"id,omitempty"`
	Parent             string   `json:"parent,omitempty"` // Parent ID for hierarchical issues
	Title              string   `json:"title"`
	Description        string   `json:"description,omitempty"`
	IssueType          string   `json:"issue_type"`
	Priority           int      `json:"priority"`
	Design             string   `json:"design,omitempty"`
	AcceptanceCriteria string   `json:"acceptance_criteria,omitempty"`
	Notes              string   `json:"notes,omitempty"`
	Assignee           string   `json:"assignee,omitempty"`
	ExternalRef        string   `json:"external_ref,omitempty"`      // Link to external issue trackers
	EstimatedMinutes   *int     `json:"estimated_minutes,omitempty"` // Time estimate in minutes
	Labels             []string `json:"labels,omitempty"`
	Dependencies       []string `json:"dependencies,omitempty"`
	// Waits-for dependencies
	WaitsFor     string `json:"waits_for,omitempty"`      // Spawner issue ID to wait for
	WaitsForGate string `json:"waits_for_gate,omitempty"` // Gate type: all-children or any-children
	// Messaging fields
	Sender    string `json:"sender,omitempty"`     // Who sent this (for messages)
	Ephemeral bool   `json:"ephemeral,omitempty"`  // If true, not exported to JSONL; bulk-deleted when closed
	RepliesTo string `json:"replies_to,omitempty"` // Issue ID for conversation threading
	// ID generation
	IDPrefix  string `json:"id_prefix,omitempty"`  // Override prefix for ID generation (mol, eph, etc.)
	CreatedBy string `json:"created_by,omitempty"` // Who created the issue
	Owner     string `json:"owner,omitempty"`      // Human owner for CV attribution (git author email)
	// Molecule type (for swarm coordination)
	MolType string `json:"mol_type,omitempty"` // swarm, patrol, or work (default)
	// Agent identity fields (only valid when IssueType == "agent")
	RoleType string `json:"role_type,omitempty"` // polecat|crew|witness|refinery|mayor|deacon
	Rig      string `json:"rig,omitempty"`       // Rig name (empty for town-level agents)
	// Event fields (only valid when IssueType == "event")
	EventCategory string `json:"event_category,omitempty"` // Namespaced category (e.g., patrol.muted, agent.started)
	EventActor    string `json:"event_actor,omitempty"`    // Entity URI who caused this event
	EventTarget   string `json:"event_target,omitempty"`   // Entity URI or bead ID affected
	EventPayload  string `json:"event_payload,omitempty"`  // Event-specific JSON data
	// Time-based scheduling fields (GH#820)
	DueAt      string `json:"due_at,omitempty"`      // Relative or ISO format due date
	DeferUntil string `json:"defer_until,omitempty"` // Relative or ISO format defer date
}

// UpdateArgs represents arguments for the update operation

type UpdateArgs struct {
	ID                 string   `json:"id"`
	Title              *string  `json:"title,omitempty"`
	Description        *string  `json:"description,omitempty"`
	Status             *string  `json:"status,omitempty"`
	Priority           *int     `json:"priority,omitempty"`
	Design             *string  `json:"design,omitempty"`
	AcceptanceCriteria *string  `json:"acceptance_criteria,omitempty"`
	Notes              *string  `json:"notes,omitempty"`
	Assignee           *string  `json:"assignee,omitempty"`
	Owner              *string  `json:"owner,omitempty"`
	ExternalRef        *string  `json:"external_ref,omitempty"`      // Link to external issue trackers
	EstimatedMinutes   *int     `json:"estimated_minutes,omitempty"` // Time estimate in minutes
	IssueType          *string  `json:"issue_type,omitempty"`        // Issue type (bug|feature|task|epic|chore)
	AddLabels          []string `json:"add_labels,omitempty"`
	RemoveLabels       []string `json:"remove_labels,omitempty"`
	SetLabels          []string `json:"set_labels,omitempty"`
	// Messaging fields
	Sender    *string `json:"sender,omitempty"`     // Who sent this (for messages)
	Ephemeral *bool   `json:"ephemeral,omitempty"`  // If true, not exported to JSONL; bulk-deleted when closed
	RepliesTo *string `json:"replies_to,omitempty"` // Issue ID for conversation threading
	// Graph link fields
	RelatesTo    *string `json:"relates_to,omitempty"`    // JSON array of related issue IDs
	DuplicateOf  *string `json:"duplicate_of,omitempty"`  // Canonical issue ID if duplicate
	SupersededBy *string `json:"superseded_by,omitempty"` // Replacement issue ID if obsolete
	// Pinned field
	Pinned *bool `json:"pinned,omitempty"` // If true, issue is a persistent context marker
	// Reparenting field
	Parent *string `json:"parent,omitempty"` // New parent issue ID (reparents the issue)
	// Agent slot fields
	HookBead *string `json:"hook_bead,omitempty"` // Current work on agent's hook (0..1)
	RoleBead *string `json:"role_bead,omitempty"` // Role definition bead for agent
	// Agent state fields
	AgentState   *string `json:"agent_state,omitempty"`   // Agent state (idle|running|stuck|stopped|dead)
	LastActivity *bool   `json:"last_activity,omitempty"` // If true, update last_activity to now
	// Agent identity fields
	RoleType *string `json:"role_type,omitempty"` // polecat|crew|witness|refinery|mayor|deacon
	Rig      *string `json:"rig,omitempty"`       // Rig name (empty for town-level agents)
	// Event fields (only valid when IssueType == "event")
	EventCategory *string `json:"event_category,omitempty"` // Namespaced category (e.g., patrol.muted, agent.started)
	EventActor    *string `json:"event_actor,omitempty"`    // Entity URI who caused this event
	EventTarget   *string `json:"event_target,omitempty"`   // Entity URI or bead ID affected
	EventPayload  *string `json:"event_payload,omitempty"`  // Event-specific JSON data
	// Work queue claim operation
	Claim bool `json:"claim,omitempty"` // If true, atomically claim issue (set assignee+status, fail if already claimed)
	// Time-based scheduling fields (GH#820)
	DueAt      *string `json:"due_at,omitempty"`      // Relative or ISO format due date
	DeferUntil *string `json:"defer_until,omitempty"` // Relative or ISO format defer date
	// Gate fields
	AwaitID *string  `json:"await_id,omitempty"` // Condition identifier for gates (run ID, PR number, etc.)
	Waiters []string `json:"waiters,omitempty"`  // Mail addresses to notify when gate clears
	// Slot fields
	Holder *string `json:"holder,omitempty"` // Who currently holds the slot (for type=slot beads)
}

// CloseArgs represents arguments for the close operation

type CloseArgs struct {
	ID          string `json:"id"`
	Reason      string `json:"reason,omitempty"`
	Session     string `json:"session,omitempty"`      // Claude Code session ID that closed this issue
	SuggestNext bool   `json:"suggest_next,omitempty"` // Return newly unblocked issues (GH#679)
	Force       bool   `json:"force,omitempty"`        // Force close even with open blockers (GH#962)
}

// CloseResult is returned when SuggestNext is true (GH#679)
// When SuggestNext is false, just the closed issue is returned for backward compatibility

type DeleteArgs struct {
	IDs     []string `json:"ids"`               // Issue IDs to delete
	Force   bool     `json:"force,omitempty"`   // Force deletion without confirmation
	DryRun  bool     `json:"dry_run,omitempty"` // Preview mode
	Cascade bool     `json:"cascade,omitempty"` // Recursively delete dependents
	Reason  string   `json:"reason,omitempty"`  // Reason for deletion
}

// ListArgs represents arguments for the list operation

type ListArgs struct {
	Query     string   `json:"query,omitempty"`
	Status    string   `json:"status,omitempty"`
	Priority  *int     `json:"priority,omitempty"`
	IssueType string   `json:"issue_type,omitempty"`
	Assignee  string   `json:"assignee,omitempty"`
	Label     string   `json:"label,omitempty"`      // Deprecated: use Labels
	Labels    []string `json:"labels,omitempty"`     // AND semantics
	LabelsAny []string `json:"labels_any,omitempty"` // OR semantics
	IDs       []string `json:"ids,omitempty"`        // Filter by specific issue IDs
	Limit     int      `json:"limit,omitempty"`

	// Pattern matching
	TitleContains       string `json:"title_contains,omitempty"`
	DescriptionContains string `json:"description_contains,omitempty"`
	NotesContains       string `json:"notes_contains,omitempty"`

	// Date ranges (ISO 8601 format)
	CreatedAfter  string `json:"created_after,omitempty"`
	CreatedBefore string `json:"created_before,omitempty"`
	UpdatedAfter  string `json:"updated_after,omitempty"`
	UpdatedBefore string `json:"updated_before,omitempty"`
	ClosedAfter   string `json:"closed_after,omitempty"`
	ClosedBefore  string `json:"closed_before,omitempty"`

	// Empty/null checks
	EmptyDescription bool `json:"empty_description,omitempty"`
	NoAssignee       bool `json:"no_assignee,omitempty"`
	NoLabels         bool `json:"no_labels,omitempty"`

	// Priority range
	PriorityMin *int `json:"priority_min,omitempty"`
	PriorityMax *int `json:"priority_max,omitempty"`

	// Pinned filtering
	Pinned *bool `json:"pinned,omitempty"`

	// Template filtering
	IncludeTemplates bool `json:"include_templates,omitempty"`

	// Parent filtering
	ParentID string `json:"parent_id,omitempty"`

	// Ephemeral filtering
	Ephemeral *bool `json:"ephemeral,omitempty"`

	// Molecule type filtering
	MolType string `json:"mol_type,omitempty"`

	// Status exclusion (for default non-closed behavior, GH#788)
	ExcludeStatus []string `json:"exclude_status,omitempty"`

	// Type exclusion (for hiding internal types like gates, bd-7zka.2)
	ExcludeTypes []string `json:"exclude_types,omitempty"`

	// Time-based scheduling filters (GH#820)
	Deferred    bool   `json:"deferred,omitempty"`     // Filter issues with defer_until set
	DeferAfter  string `json:"defer_after,omitempty"`  // ISO 8601 format
	DeferBefore string `json:"defer_before,omitempty"` // ISO 8601 format
	DueAfter    string `json:"due_after,omitempty"`    // ISO 8601 format
	DueBefore   string `json:"due_before,omitempty"`   // ISO 8601 format
	Overdue     bool   `json:"overdue,omitempty"`      // Filter issues where due_at < now

	// Staleness control (bd-dpkdm)
	AllowStale bool `json:"allow_stale,omitempty"` // Skip staleness check, return potentially stale data

	// Multi-repo filtering
	SourceRepos []string `json:"source_repos,omitempty"` // Filter by source repository
}

// CountArgs represents arguments for the count operation

type CountArgs struct {
	// Supports all the same filters as ListArgs
	Query     string   `json:"query,omitempty"`
	Status    string   `json:"status,omitempty"`
	Priority  *int     `json:"priority,omitempty"`
	IssueType string   `json:"issue_type,omitempty"`
	Assignee  string   `json:"assignee,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	LabelsAny []string `json:"labels_any,omitempty"`
	IDs       []string `json:"ids,omitempty"`

	// Pattern matching
	TitleContains       string `json:"title_contains,omitempty"`
	DescriptionContains string `json:"description_contains,omitempty"`
	NotesContains       string `json:"notes_contains,omitempty"`

	// Date ranges
	CreatedAfter  string `json:"created_after,omitempty"`
	CreatedBefore string `json:"created_before,omitempty"`
	UpdatedAfter  string `json:"updated_after,omitempty"`
	UpdatedBefore string `json:"updated_before,omitempty"`
	ClosedAfter   string `json:"closed_after,omitempty"`
	ClosedBefore  string `json:"closed_before,omitempty"`

	// Empty/null checks
	EmptyDescription bool `json:"empty_description,omitempty"`
	NoAssignee       bool `json:"no_assignee,omitempty"`
	NoLabels         bool `json:"no_labels,omitempty"`

	// Priority range
	PriorityMin *int `json:"priority_min,omitempty"`
	PriorityMax *int `json:"priority_max,omitempty"`

	// Grouping option (only one can be specified)
	GroupBy string `json:"group_by,omitempty"` // "status", "priority", "type", "assignee", "label"

	// Multi-repo filtering
	SourceRepos []string `json:"source_repos,omitempty"` // Filter by source repository
}

// ShowArgs represents arguments for the show operation

type ShowArgs struct {
	ID string `json:"id"`
}

// ResolveIDArgs represents arguments for the resolve_id operation

type ResolveIDArgs struct {
	ID string `json:"id"`
}

// ReadyArgs represents arguments for the ready operation

type ReadyArgs struct {
	Assignee        string   `json:"assignee,omitempty"`
	Unassigned      bool     `json:"unassigned,omitempty"`
	Priority        *int     `json:"priority,omitempty"`
	Type            string   `json:"type,omitempty"`
	Limit           int      `json:"limit,omitempty"`
	SortPolicy      string   `json:"sort_policy,omitempty"`
	Labels          []string `json:"labels,omitempty"`
	LabelsAny       []string `json:"labels_any,omitempty"`
	ParentID        string   `json:"parent_id,omitempty"`        // Filter to descendants of this bead/epic
	MolType         string   `json:"mol_type,omitempty"`         // Filter by molecule type: swarm, patrol, or work
	IncludeDeferred bool     `json:"include_deferred,omitempty"` // Include issues with future defer_until (GH#820)
	SourceRepos     []string `json:"source_repos,omitempty"`     // Filter by source repository
}

// BlockedArgs represents arguments for the blocked operation

type BlockedArgs struct {
	ParentID string `json:"parent_id,omitempty"` // Filter to descendants of this bead/epic
	Assignee string `json:"assignee,omitempty"`  // Filter by assignee
	Priority *int   `json:"priority,omitempty"`  // Filter by priority (0-4)
	Type     string `json:"type,omitempty"`      // Filter by issue type
	Limit    int    `json:"limit,omitempty"`     // Max results to return
}

// StaleArgs represents arguments for the stale command

type StaleArgs struct {
	Days   int    `json:"days,omitempty"`
	Status string `json:"status,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// DepAddArgs represents arguments for adding a dependency

type DepAddArgs struct {
	FromID  string `json:"from_id"`
	ToID    string `json:"to_id"`
	DepType string `json:"dep_type"`
}

// DepRemoveArgs represents arguments for removing a dependency

type DepRemoveArgs struct {
	FromID  string `json:"from_id"`
	ToID    string `json:"to_id"`
	DepType string `json:"dep_type,omitempty"`
}

// DepTreeArgs represents arguments for the dep tree operation

type DepTreeArgs struct {
	ID       string `json:"id"`
	MaxDepth int    `json:"max_depth,omitempty"`
}

// LabelAddArgs represents arguments for adding a label

type LabelAddArgs struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// LabelRemoveArgs represents arguments for removing a label

type LabelRemoveArgs struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// CommentListArgs represents arguments for listing comments on an issue

type CommentListArgs struct {
	ID string `json:"id"`
}

// CommentAddArgs represents arguments for adding a comment to an issue

type CommentAddArgs struct {
	ID     string `json:"id"`
	Author string `json:"author"`
	Text   string `json:"text"`
}

// EventListArgs represents arguments for listing events on an issue

type EventListArgs struct {
	ID    string `json:"id"`
	Limit int    `json:"limit,omitempty"`
}

// EpicStatusArgs represents arguments for the epic status operation

type EpicStatusArgs struct {
	EligibleOnly bool `json:"eligible_only,omitempty"`
}

// PingResponse is the response for a ping operation

type BatchArgs struct {
	Operations []BatchOperation `json:"operations"`
}

// BatchOperation represents a single operation in a batch

type BatchOperation struct {
	Operation string          `json:"operation"`
	Args      json.RawMessage `json:"args"`
}

// BatchResponse contains the results of a batch operation

type CompactArgs struct {
	IssueID   string `json:"issue_id,omitempty"` // Empty for --all
	Tier      int    `json:"tier"`               // 1 or 2
	DryRun    bool   `json:"dry_run"`
	Force     bool   `json:"force"`
	All       bool   `json:"all"`
	APIKey    string `json:"api_key,omitempty"` //nolint:gosec // G117 — real external LLM API key; must serialize for local Unix socket IPC. Revisit if transport changes.
	Workers   int    `json:"workers,omitempty"`
	BatchSize int    `json:"batch_size,omitempty"`
}

// CompactStatsArgs represents arguments for compact stats operation

type CompactStatsArgs struct {
	Tier int `json:"tier,omitempty"`
}

// CompactResponse represents the response from a compact operation

type ExportArgs struct {
	JSONLPath string `json:"jsonl_path"` // Path to export JSONL file
}

// ImportArgs represents arguments for the import operation

type ImportArgs struct {
	JSONLPath string `json:"jsonl_path"` // Path to import JSONL file
}

// GetMutationsArgs represents arguments for retrieving recent mutations

type GetMutationsArgs struct {
	Since int64 `json:"since"` // Unix timestamp in milliseconds (0 for all recent)
}

// WaitForMutationsArgs represents arguments for waiting on mutations

type WaitForMutationsArgs struct {
	Since   int64 `json:"since"`   // Unix timestamp in ms - return mutations after this time
	Timeout int64 `json:"timeout"` // Max wait time in ms (0 = default 30s)
}

// Gate operations

// GateCreateArgs represents arguments for creating a gate

type GateCreateArgs struct {
	Title     string        `json:"title"`
	AwaitType string        `json:"await_type"` // gh:run, gh:pr, timer, human, mail
	AwaitID   string        `json:"await_id"`   // ID/value for the await type
	Timeout   time.Duration `json:"timeout"`    // Timeout duration
	Waiters   []string      `json:"waiters"`    // Mail addresses to notify when gate clears
}

// GateCreateResult represents the result of creating a gate

type GateListArgs struct {
	All bool `json:"all"` // Include closed gates
}

// GateShowArgs represents arguments for showing a gate

type GateShowArgs struct {
	ID string `json:"id"` // Gate ID (partial or full)
}

// GateCloseArgs represents arguments for closing a gate

type GateCloseArgs struct {
	ID     string `json:"id"`               // Gate ID (partial or full)
	Reason string `json:"reason,omitempty"` // Close reason
}

// GateWaitArgs represents arguments for adding waiters to a gate

type GateWaitArgs struct {
	ID      string   `json:"id"`      // Gate ID (partial or full)
	Waiters []string `json:"waiters"` // Additional waiters to add
}

// GateWaitResult represents the result of adding waiters

type GetWorkerStatusArgs struct {
	// Assignee filters to a specific worker (optional, empty = all workers)
	Assignee string `json:"assignee,omitempty"`
}

// WorkerStatus represents the status of a single worker and their current work

type GetMoleculeProgressArgs struct {
	MoleculeID string `json:"molecule_id"` // The ID of the molecule (parent issue)
}

// MoleculeStep represents a single step within a molecule

type GetConfigArgs struct {
	Key string `json:"key"` // Config key to retrieve (e.g., "issue_prefix")
}

// GetConfigResponse represents the response from get_config operation

type MolStaleArgs struct {
	BlockingOnly   bool `json:"blocking_only"`   // Only show molecules blocking other work
	UnassignedOnly bool `json:"unassigned_only"` // Only show unassigned molecules
	ShowAll        bool `json:"show_all"`        // Include molecules with 0 children
}

// StaleMolecule holds info about a stale molecule (for RPC response)

type GetParentIDsArgs struct {
	IssueIDs []string `json:"issue_ids"`
}

// ParentInfo contains parent issue information

type GetGraphDataArgs struct {
	Status        string   `json:"status,omitempty"`         // "open", "closed", or "all" (default: "all")
	ExcludeStatus []string `json:"exclude_status,omitempty"` // Statuses to exclude
	SourceRepos   []string `json:"source_repos,omitempty"`   // Filter by source repository
}

// GraphIssueSummary is a slim issue representation for graph visualization.
// Contains only the fields needed to render the dependency graph.
