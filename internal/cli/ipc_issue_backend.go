// IPC-aware IssueBackend decorator.
//
// When a daemon-spawned subprocess has LOOM_DAEMON_SOCKET set, defaultIssueBackend()
// returns an ipcIssueBackend that routes the three daemon-supported mutation operations
// (Update, ClaimIssue, Close) through the AgentIPCClient while delegating all other
// operations to the underlying direct backend.

package cli

import (
	"context"
	"encoding/json"
	"log/slog"
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
// Update, ClaimIssue, and Close are routed through the IPC client. On
// KindUnavailable, the decorator falls back to the direct backend. All other
// methods delegate directly to the fallback.
type ipcIssueBackend struct {
	ipc      ipcMutator
	fallback backend.IssueBackend
}

// Compile-time interface check.
var _ backend.IssueBackend = (*ipcIssueBackend)(nil)

// newIPCIssueBackend returns an IPC-aware decorator.
func newIPCIssueBackend(ipc ipcMutator, fallback backend.IssueBackend) *ipcIssueBackend {
	return &ipcIssueBackend{ipc: ipc, fallback: fallback}
}

// --- IPC-routed mutations ---

// Update routes through IPC. Falls back to direct backend on KindUnavailable.
func (b *ipcIssueBackend) Update(ctx context.Context, id string, params backend.UpdateParams) error {
	err := b.ipc.Update(id, params)
	if err != nil && backend.IsKind(err, backend.KindUnavailable) {
		slog.Warn("IPC mutation failed, falling back to direct backend", "op", "Update", "err", err)
		return b.fallback.Update(ctx, id, params)
	}
	return err
}

// ClaimIssue routes through IPC. Falls back to direct backend on KindUnavailable.
func (b *ipcIssueBackend) ClaimIssue(ctx context.Context, id string, lockTTL time.Duration) error {
	err := b.ipc.Claim(id, lockTTL)
	if err != nil && backend.IsKind(err, backend.KindUnavailable) {
		slog.Warn("IPC mutation failed, falling back to direct backend", "op", "ClaimIssue", "err", err)
		return b.fallback.ClaimIssue(ctx, id, lockTTL)
	}
	return err
}

// Close routes through IPC. Falls back to direct backend on KindUnavailable.
func (b *ipcIssueBackend) Close(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	result, err := b.ipc.Complete(id, params)
	if err != nil && backend.IsKind(err, backend.KindUnavailable) {
		slog.Warn("IPC mutation failed, falling back to direct backend", "op", "Close", "err", err)
		return b.fallback.Close(ctx, id, params)
	}
	return result, err
}

// --- Fallback-delegated methods ---

func (b *ipcIssueBackend) Get(ctx context.Context, id string) (*backend.IssueDetailData, error) {
	return b.fallback.Get(ctx, id)
}

func (b *ipcIssueBackend) List(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	return b.fallback.List(ctx, opts)
}

func (b *ipcIssueBackend) Ready(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	return b.fallback.Ready(ctx, opts)
}

func (b *ipcIssueBackend) Blocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	return b.fallback.Blocked(ctx, opts)
}

func (b *ipcIssueBackend) Stats(ctx context.Context) (*backend.StatsData, error) {
	return b.fallback.Stats(ctx)
}

func (b *ipcIssueBackend) Count(ctx context.Context, opts backend.CountOpts) (int, error) {
	return b.fallback.Count(ctx, opts)
}

func (b *ipcIssueBackend) GetChildren(ctx context.Context, id string) ([]backend.IssueData, error) {
	return b.fallback.GetChildren(ctx, id)
}

func (b *ipcIssueBackend) Create(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	return b.fallback.Create(ctx, params)
}

func (b *ipcIssueBackend) Reopen(ctx context.Context, id string, params backend.ReopenParams) error {
	return b.fallback.Reopen(ctx, id, params)
}

func (b *ipcIssueBackend) DeferIssue(ctx context.Context, id string, until time.Time) error {
	return b.fallback.DeferIssue(ctx, id, until)
}

func (b *ipcIssueBackend) UndeferIssue(ctx context.Context, id string) error {
	return b.fallback.UndeferIssue(ctx, id)
}

func (b *ipcIssueBackend) Delete(ctx context.Context, params backend.DeleteParams) error {
	return b.fallback.Delete(ctx, params)
}

func (b *ipcIssueBackend) AddDependency(ctx context.Context, params backend.DepAddParams) error {
	return b.fallback.AddDependency(ctx, params)
}

func (b *ipcIssueBackend) RemoveDependency(ctx context.Context, params backend.DepRemoveParams) error {
	return b.fallback.RemoveDependency(ctx, params)
}

func (b *ipcIssueBackend) AddLabel(ctx context.Context, id string, label string) error {
	return b.fallback.AddLabel(ctx, id, label)
}

func (b *ipcIssueBackend) RemoveLabel(ctx context.Context, id string, label string) error {
	return b.fallback.RemoveLabel(ctx, id, label)
}

func (b *ipcIssueBackend) ListComments(ctx context.Context, id string) ([]backend.CommentData, error) {
	return b.fallback.ListComments(ctx, id)
}

func (b *ipcIssueBackend) AddComment(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	return b.fallback.AddComment(ctx, params)
}

func (b *ipcIssueBackend) ListEvents(ctx context.Context, id string, limit int) ([]backend.EventData, error) {
	return b.fallback.ListEvents(ctx, id, limit)
}

func (b *ipcIssueBackend) Batch(ctx context.Context, ops []backend.BatchOp) ([]backend.BatchResult, error) {
	return b.fallback.Batch(ctx, ops)
}

func (b *ipcIssueBackend) GetMutations(ctx context.Context, sinceMs int64) ([]backend.MutationData, error) {
	return b.fallback.GetMutations(ctx, sinceMs)
}

func (b *ipcIssueBackend) WaitForMutations(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
	return b.fallback.WaitForMutations(ctx, sinceMs, timeoutMs)
}

// BackendName returns "ipc:<fallback-name>" to signal IPC routing is active.
func (b *ipcIssueBackend) BackendName() string {
	return "ipc:" + b.fallback.BackendName()
}

// resolveFallbackBackend encapsulates the defaultDeps.IssueBackend ?? newCliBeadsAdapter() logic.
func resolveFallbackBackend() backend.IssueBackend {
	if t := defaultDeps.IssueBackend; t != nil {
		return t
	}
	return newCliBeadsAdapter(defaultBDRunnerImpl{}, GetBeadsDir())
}

// --- IPC types (merged from ipc_types.go) ---

// AgentIPCRequest is sent by an agent subprocess to the daemon IPC socket.
type AgentIPCRequest struct {
	Operation string          `json:"operation"`      // "claim", "update", "complete"
	AgentName string          `json:"agent_name"`     // BD_ACTOR identity (required)
	IssueID   string          `json:"issue_id"`       // target issue (required)
	Args      json.RawMessage `json:"args,omitempty"` // operation-specific params
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
	IPCOpClaim    = "claim"
	IPCOpUpdate   = "update"
	IPCOpComplete = "complete"
)

// IPCClaimArgs are the optional arguments for the claim operation.
type IPCClaimArgs struct {
	LockTTLSeconds int `json:"lock_ttl_seconds,omitempty"`
}
