package rpc

import (
	"encoding/json"
)

// Operation constants for daemon RPC commands.
const (
	OpPing        = "ping"
	OpStatus      = "status"
	OpHealth      = "health"
	OpMetrics     = "metrics"
	OpCreate      = "create"
	OpUpdate      = "update"
	OpClose       = "close"
	OpList        = "list"
	OpCount       = "count"
	OpShow        = "show"
	OpReady       = "ready"
	OpBlocked     = "blocked"
	OpStale       = "stale"
	OpStats       = "stats"
	OpDepAdd      = "dep_add"
	OpDepRemove   = "dep_remove"
	OpDepTree     = "dep_tree"
	OpLabelAdd    = "label_add"
	OpLabelRemove = "label_remove"
	OpCommentList = "comment_list"
	OpCommentAdd  = "comment_add"
	OpEventList   = "event_list"
	OpBatch       = "batch"
	OpResolveID   = "resolve_id"

	OpCompact             = "compact"
	OpCompactStats        = "compact_stats"
	OpExport              = "export"
	OpImport              = "import"
	OpEpicStatus          = "epic_status"
	OpGetMutations        = "get_mutations"
	OpGetMoleculeProgress = "get_molecule_progress"
	OpShutdown            = "shutdown"
	OpDelete              = "delete"
	OpGetWorkerStatus     = "get_worker_status"
	OpGetConfig           = "get_config"
	OpMolStale            = "mol_stale"
	OpGetParentIDs        = "get_parent_ids"
	OpGetGraphData        = "get_graph_data"
	OpListKanban          = "list_kanban"
	OpWaitForMutations    = "wait_for_mutations" //nolint:gosec // G101: not a credential, RPC operation name

	// Gate operations
	OpGateCreate = "gate_create"
	OpGateList   = "gate_list"
	OpGateShow   = "gate_show"
	OpGateClose  = "gate_close"
	OpGateWait   = "gate_wait"
)

// Request represents an RPC request from client to daemon
type Request struct {
	Operation     string          `json:"operation"`
	Args          json.RawMessage `json:"args"`
	Actor         string          `json:"actor,omitempty"`
	RequestID     string          `json:"request_id,omitempty"`
	Cwd           string          `json:"cwd,omitempty"`            // Working directory for database discovery
	ClientVersion string          `json:"client_version,omitempty"` // Client version for protocol checks
	ExpectedDB    string          `json:"expected_db,omitempty"`    // Expected database path for validation (absolute)
	AuthToken     string          `json:"auth_token,omitempty"`     //nolint:gosec // G117 — must serialize for RPC wire protocol
}

// Response represents an RPC response from daemon to client
type Response struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}
