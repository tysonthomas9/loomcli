// IPC-aware IssueBackend decorator.
//
// When a daemon-spawned subprocess has LOOM_DAEMON_SOCKET set, defaultIssueBackend()
// returns an ipcIssueBackend that routes the three daemon-supported mutation operations
// (Update, ClaimIssue, Close) through the AgentIPCClient while reading through the
// underlying direct backend.

package cli

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// ipcMutator is the subset of AgentIPCClient methods used for IPC-routed mutations.
// The decorator depends on this interface (not the concrete client) for testability.
type ipcMutator interface {
	Claim(issueID string, lockTTL time.Duration) error
	Update(issueID string, params backend.UpdateParams) error
	Complete(issueID string, params backend.CloseParams) (*backend.CloseResult, error)
}

// ipcIssueBackend decorates an IssueBackend with IPC routing for mutations.
// Update, ClaimIssue, and Close are routed through the IPC client. All other
// methods read directly from the backend.
type ipcIssueBackend struct {
	ipc    ipcMutator
	direct backend.IssueBackend
}

// Compile-time interface check.
var _ backend.IssueBackend = (*ipcIssueBackend)(nil)

// newIPCIssueBackend returns an IPC-aware decorator.
func newIPCIssueBackend(ipc ipcMutator, direct backend.IssueBackend) *ipcIssueBackend {
	return &ipcIssueBackend{ipc: ipc, direct: direct}
}

// --- IPC-routed mutations ---

// Update routes through IPC.
func (b *ipcIssueBackend) Update(ctx context.Context, id string, params backend.UpdateParams) error {
	return b.ipc.Update(id, params)
}

// ClaimIssue routes through IPC.
func (b *ipcIssueBackend) ClaimIssue(ctx context.Context, id string, lockTTL time.Duration) error {
	return b.ipc.Claim(id, lockTTL)
}

// Close routes through IPC.
func (b *ipcIssueBackend) Close(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	return b.ipc.Complete(id, params)
}

// --- Direct backend methods ---

func (b *ipcIssueBackend) Get(ctx context.Context, id string) (*backend.IssueDetailData, error) {
	return b.direct.Get(ctx, id)
}

func (b *ipcIssueBackend) List(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	return b.direct.List(ctx, opts)
}

func (b *ipcIssueBackend) Ready(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	return b.direct.Ready(ctx, opts)
}

func (b *ipcIssueBackend) Blocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	return b.direct.Blocked(ctx, opts)
}

func (b *ipcIssueBackend) Stats(ctx context.Context) (*backend.StatsData, error) {
	return b.direct.Stats(ctx)
}

func (b *ipcIssueBackend) Count(ctx context.Context, opts backend.CountOpts) (int, error) {
	return b.direct.Count(ctx, opts)
}

func (b *ipcIssueBackend) GetChildren(ctx context.Context, id string) ([]backend.IssueData, error) {
	return b.direct.GetChildren(ctx, id)
}

func (b *ipcIssueBackend) SearchIssues(ctx context.Context, query string, limit int) ([]backend.IssueData, error) {
	return b.direct.SearchIssues(ctx, query, limit)
}

func (b *ipcIssueBackend) Create(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	return b.direct.Create(ctx, params)
}

func (b *ipcIssueBackend) Reopen(ctx context.Context, id string, params backend.ReopenParams) error {
	return b.direct.Reopen(ctx, id, params)
}

func (b *ipcIssueBackend) DeferIssue(ctx context.Context, id string, until time.Time) error {
	return b.direct.DeferIssue(ctx, id, until)
}

func (b *ipcIssueBackend) UndeferIssue(ctx context.Context, id string) error {
	return b.direct.UndeferIssue(ctx, id)
}

func (b *ipcIssueBackend) Delete(ctx context.Context, params backend.DeleteParams) error {
	return b.direct.Delete(ctx, params)
}

func (b *ipcIssueBackend) AddDependency(ctx context.Context, params backend.DepAddParams) error {
	return b.direct.AddDependency(ctx, params)
}

func (b *ipcIssueBackend) RemoveDependency(ctx context.Context, params backend.DepRemoveParams) error {
	return b.direct.RemoveDependency(ctx, params)
}

func (b *ipcIssueBackend) AddLabel(ctx context.Context, id string, label string) error {
	return b.direct.AddLabel(ctx, id, label)
}

func (b *ipcIssueBackend) RemoveLabel(ctx context.Context, id string, label string) error {
	return b.direct.RemoveLabel(ctx, id, label)
}

func (b *ipcIssueBackend) ListComments(ctx context.Context, id string) ([]backend.CommentData, error) {
	return b.direct.ListComments(ctx, id)
}

func (b *ipcIssueBackend) AddComment(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	return b.direct.AddComment(ctx, params)
}

func (b *ipcIssueBackend) ListEvents(ctx context.Context, id string, limit int) ([]backend.EventData, error) {
	return b.direct.ListEvents(ctx, id, limit)
}

func (b *ipcIssueBackend) Batch(ctx context.Context, ops []backend.BatchOp) ([]backend.BatchResult, error) {
	return b.direct.Batch(ctx, ops)
}

func (b *ipcIssueBackend) GetMutations(ctx context.Context, sinceMs int64) ([]backend.MutationData, error) {
	return b.direct.GetMutations(ctx, sinceMs)
}

func (b *ipcIssueBackend) WaitForMutations(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
	return b.direct.WaitForMutations(ctx, sinceMs, timeoutMs)
}

// BackendName returns "ipc:<backend-name>" to signal IPC routing is active.
func (b *ipcIssueBackend) BackendName() string {
	return "ipc:" + b.direct.BackendName()
}

// resolveDirectIssueBackend returns the active default issue backend.
func resolveDirectIssueBackend() backend.IssueBackend {
	if t := ensureDefaultDeps().IssueBackend; t != nil {
		return t
	}
	return newFleetDBIssueBackend()
}

// --- IPC types (merged from ipc_types.go) ---

// AgentIPCRequest is sent by an agent subprocess to the daemon IPC socket.
type AgentIPCRequest struct {
	Operation  string          `json:"operation"`             // "claim", "update", "complete"
	AgentName  string          `json:"agent_name"`            // LOOM_AGENT_NAME identity (required)
	IssueID    string          `json:"issue_id"`              // target issue (required)
	SessionID  string          `json:"session_id,omitempty"`  // fleet-db AgentSession id
	LeaseID    string          `json:"lease_id,omitempty"`    // fleet-db AgentLease id
	LeaseToken string          `json:"lease_token,omitempty"` // fleet-db AgentLease token
	Args       json.RawMessage `json:"args,omitempty"`        // operation-specific params
}

// AgentIPCResponse is sent by the daemon back to the agent subprocess.
type AgentIPCResponse struct {
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
	Kind    string          `json:"kind,omitempty"` // backend.ErrorKind for typed error handling
	Data    json.RawMessage `json:"data,omitempty"`
}

// IPC operation name constants.
const (
	IPCOpClaim     = "claim"
	IPCOpUpdate    = "update"
	IPCOpComplete  = "complete"
	IPCOpHeartbeat = "heartbeat"
)

// IPCClaimArgs are the optional arguments for the claim operation.
type IPCClaimArgs struct {
	LockTTLSeconds int `json:"lock_ttl_seconds,omitempty"`
}
