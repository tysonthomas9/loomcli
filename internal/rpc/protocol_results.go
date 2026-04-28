package rpc

import (
	"encoding/json"

	"github.com/tysonthomas9/loomcli/internal/types"
)

type CloseResult struct {
	Closed    *types.Issue   `json:"closed"`              // The issue that was closed
	Unblocked []*types.Issue `json:"unblocked,omitempty"` // Issues newly unblocked by closing
}

// DeleteArgs represents arguments for the delete operation

type PingResponse struct {
	Message string `json:"message"`
	Version string `json:"version"`
}

// StatusResponse represents the daemon status metadata

type StatusResponse struct {
	Version             string  `json:"version"`                         // Server/daemon version
	WorkspacePath       string  `json:"workspace_path"`                  // Absolute path to workspace root
	DatabasePath        string  `json:"database_path"`                   // Absolute path to database file
	SocketPath          string  `json:"socket_path"`                     // Path to Unix socket
	PID                 int     `json:"pid"`                             // Process ID
	UptimeSeconds       float64 `json:"uptime_seconds"`                  // Time since daemon started
	LastActivityTime    string  `json:"last_activity_time"`              // ISO 8601 timestamp of last request
	ExclusiveLockActive bool    `json:"exclusive_lock_active"`           // Whether an exclusive lock is held
	ExclusiveLockHolder string  `json:"exclusive_lock_holder,omitempty"` // Lock holder name if active
	// Daemon configuration
	AutoCommit   bool   `json:"auto_commit"`   // Whether auto-commit is enabled
	AutoPush     bool   `json:"auto_push"`     // Whether auto-push is enabled
	AutoPull     bool   `json:"auto_pull"`     // Whether auto-pull is enabled (periodic remote sync)
	LocalMode    bool   `json:"local_mode"`    // Whether running in local-only mode (no git)
	SyncInterval string `json:"sync_interval"` // Sync interval (e.g., "5s")
	DaemonMode   string `json:"daemon_mode"`   // Sync mode: see DaemonMode* constants
}

// DaemonMode values for StatusResponse.DaemonMode.
const (
	DaemonModePoll   = "poll"   // bd daemon polls for changes on SyncInterval
	DaemonModeEvents = "events" // bd daemon receives push events
	DaemonModeFleet  = "fleet"  // fleet client mode — no local bd daemon
)

// HealthResponse is the response for a health check operation

type HealthResponse struct {
	Status         string  `json:"status"`                   // "healthy", "degraded", "unhealthy"
	Version        string  `json:"version"`                  // Server/daemon version
	ClientVersion  string  `json:"client_version,omitempty"` // Client version from request
	Compatible     bool    `json:"compatible"`               // Whether versions are compatible
	Uptime         float64 `json:"uptime_seconds"`
	DBResponseTime float64 `json:"db_response_ms"`
	ActiveConns    int32   `json:"active_connections"`
	MaxConns       int     `json:"max_connections"`
	MemoryAllocMB  uint64  `json:"memory_alloc_mb"`
	Error          string  `json:"error,omitempty"`
}

// BatchArgs represents arguments for batch operations

type BatchResponse struct {
	Results []BatchResult `json:"results"`
}

// BatchResult represents the result of a single operation in a batch

type BatchResult struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// CompactArgs represents arguments for the compact operation

type CompactResponse struct {
	Success       bool              `json:"success"`
	IssueID       string            `json:"issue_id,omitempty"`
	Results       []CompactResult   `json:"results,omitempty"` // For batch operations
	Stats         *CompactStatsData `json:"stats,omitempty"`   // For stats operation
	OriginalSize  int               `json:"original_size,omitempty"`
	CompactedSize int               `json:"compacted_size,omitempty"`
	Reduction     string            `json:"reduction,omitempty"`
	Duration      string            `json:"duration,omitempty"`
	DryRun        bool              `json:"dry_run,omitempty"`
}

// CompactResult represents the result of compacting a single issue

type CompactResult struct {
	IssueID       string `json:"issue_id"`
	Success       bool   `json:"success"`
	Error         string `json:"error,omitempty"`
	OriginalSize  int    `json:"original_size,omitempty"`
	CompactedSize int    `json:"compacted_size,omitempty"`
	Reduction     string `json:"reduction,omitempty"`
}

// CompactStatsData represents compaction statistics

type CompactStatsData struct {
	Tier1Candidates  int    `json:"tier1_candidates"`
	Tier2Candidates  int    `json:"tier2_candidates"`
	TotalClosed      int    `json:"total_closed"`
	Tier1MinAge      string `json:"tier1_min_age"`
	Tier2MinAge      string `json:"tier2_min_age"`
	EstimatedSavings string `json:"estimated_savings,omitempty"`
}

// ExportArgs represents arguments for the export operation

type GateCreateResult struct {
	ID string `json:"id"` // Created gate ID
}

// GateListArgs represents arguments for listing gates

type GateWaitResult struct {
	AddedCount int `json:"added_count"` // Number of new waiters added
}

// GetWorkerStatusArgs represents arguments for retrieving worker status

type WorkerStatus struct {
	Assignee      string `json:"assignee"`                 // Worker identifier
	MoleculeID    string `json:"molecule_id,omitempty"`    // Parent molecule/epic ID (if working on a step)
	MoleculeTitle string `json:"molecule_title,omitempty"` // Parent molecule/epic title
	CurrentStep   int    `json:"current_step,omitempty"`   // Current step number (1-indexed)
	TotalSteps    int    `json:"total_steps,omitempty"`    // Total number of steps in molecule
	StepID        string `json:"step_id,omitempty"`        // Current step issue ID
	StepTitle     string `json:"step_title,omitempty"`     // Current step issue title
	LastActivity  string `json:"last_activity"`            // ISO 8601 timestamp of last update
	Status        string `json:"status"`                   // Current work status (in_progress, blocked, etc.)
}

// GetWorkerStatusResponse is the response for get_worker_status operation

type GetWorkerStatusResponse struct {
	Workers []WorkerStatus `json:"workers"`
}

// GetMoleculeProgressArgs represents arguments for the get_molecule_progress operation

type MoleculeStep struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Status    string  `json:"status"`     // "done", "current", "ready", "blocked"
	StartTime *string `json:"start_time"` // ISO 8601 timestamp when step was created
	CloseTime *string `json:"close_time"` // ISO 8601 timestamp when step was closed (if done)
}

// MoleculeProgress represents the progress of a molecule (parent issue with steps)

type MoleculeProgress struct {
	MoleculeID string         `json:"molecule_id"`
	Title      string         `json:"title"`
	Assignee   string         `json:"assignee"`
	Steps      []MoleculeStep `json:"steps"`
}

// GetConfigArgs represents arguments for getting daemon config

type GetConfigResponse struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// MolStaleArgs represents arguments for the mol stale operation

type StaleMolecule struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	TotalChildren  int      `json:"total_children"`
	ClosedChildren int      `json:"closed_children"`
	Assignee       string   `json:"assignee,omitempty"`
	BlockingIssues []string `json:"blocking_issues,omitempty"`
	BlockingCount  int      `json:"blocking_count"`
}

// MolStaleResponse holds the result of the mol stale operation

type MolStaleResponse struct {
	StaleMolecules []*StaleMolecule `json:"stale_molecules"`
	TotalCount     int              `json:"total_count"`
	BlockingCount  int              `json:"blocking_count"`
}

// GetParentIDsArgs represents arguments for the get_parent_ids operation

type ParentInfo struct {
	ParentID    string `json:"parent_id"`
	ParentTitle string `json:"parent_title"`
}

// GetParentIDsResponse represents the response from get_parent_ids operation

type GetParentIDsResponse struct {
	Parents map[string]*ParentInfo `json:"parents"` // childID -> ParentInfo
}

// GetGraphDataArgs represents arguments for the get_graph_data operation.
// This fetches all issues with their dependencies and labels in a single RPC call,
// avoiding the N+1 pattern of List + N×Show.

type GraphIssueSummary struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Status       string            `json:"status"`
	Priority     int               `json:"priority"`
	IssueType    string            `json:"issue_type"`
	Labels       []string          `json:"labels,omitempty"`
	Dependencies []GraphDependency `json:"dependencies,omitempty"`
	DeferUntil   string            `json:"defer_until,omitempty"`
	DueAt        string            `json:"due_at,omitempty"`
}

// GraphDependency represents a dependency relationship for graph rendering.

type GraphDependency struct {
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
}

// GetGraphDataResponse represents the response from get_graph_data operation.

type GetGraphDataResponse struct {
	Issues []GraphIssueSummary `json:"issues"`
}

// KanbanIssueRPC is the per-issue response from OpListKanban.
type KanbanIssueRPC struct {
	*types.IssueWithCounts
	ParentID         string             `json:"parent_id,omitempty"`
	ParentTitle      string             `json:"parent_title,omitempty"`
	Repo             string             `json:"repo,omitempty"`
	IsBlocked        bool               `json:"is_blocked,omitempty"`
	BlockedByCount   int                `json:"blocked_by_count,omitempty"`
	BlockedBy        []string           `json:"blocked_by,omitempty"`
	BlockedByDetails []types.BlockerRef `json:"blocked_by_details,omitempty"`
}

// ListKanbanResponse is the response from OpListKanban.
type ListKanbanResponse struct {
	Issues []*KanbanIssueRPC `json:"issues"`
}
