// Package agentipc implements backend.IssueBackend by proxying the three
// IPC-supported mutation operations (ClaimIssue, Update, Close) to the loom
// daemon's agent IPC Unix socket. All other operations return KindNotImplemented.
//
// This backend lives at the backend layer (not the CLI layer) so that any
// internal consumer — WebUI, tests, future daemons — can use it without
// importing internal/cli.
package agentipc

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// Wire types matching the canonical AgentIPCRequest/AgentIPCResponse from
// internal/cli/daemon_ipc.go (task .1). Unexported because they are internal
// transport details.
type ipcRequest struct {
	Operation string          `json:"operation"`
	AgentName string          `json:"agent_name"`
	IssueID   string          `json:"issue_id"`
	Args      json.RawMessage `json:"args,omitempty"`
}

type ipcResponse struct {
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
	Kind    string          `json:"kind,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Operation name constants matching the IPC server.
const (
	opClaim       = "claim"
	opUpdate      = "update"
	opComplete    = "complete"
	opReleaseLock = "release_lock"
)

// Timeout constants matching task .1 server-side deadlines and task .2 client.
const (
	dialTimeout = 5 * time.Second
	readTimeout = 10 * time.Second
)

// Backend implements backend.IssueBackend by proxying mutations to the daemon
// IPC socket. Safe for concurrent use — each method call creates a new connection.
type Backend struct {
	socketPath string
	agentName  string
}

// Compile-time interface check.
var _ backend.IssueBackend = (*Backend)(nil)

// New creates a Backend that connects to the given Unix socket path and
// identifies as the given agent. Panics if socketPath is empty (programming error).
func New(socketPath, agentName string) *Backend {
	if socketPath == "" {
		panic("agentipc.New: socketPath must not be empty")
	}
	return &Backend{socketPath: socketPath, agentName: agentName}
}

// BackendName returns "agent-ipc".
func (b *Backend) BackendName() string { return "agent-ipc" }

// --- IPC-routed mutations (3) ---

// ClaimIssue atomically claims an issue via the daemon IPC socket.
func (b *Backend) ClaimIssue(_ context.Context, params backend.ClaimIssueParams) error {
	const op = "AgentIPC.ClaimIssue"
	ownerActor := strings.TrimSpace(params.OwnerActor)
	if ownerActor != "" && ownerActor != b.agentName {
		return backend.ErrValidation(op, "owner actor override must match agent identity")
	}
	req := ipcRequest{
		Operation: opClaim,
		AgentName: b.agentName,
		IssueID:   params.ID,
	}
	if params.LockTTL > 0 || ownerActor != "" {
		args, err := json.Marshal(struct {
			LockTTLSeconds int    `json:"lock_ttl_seconds,omitempty"`
			OwnerActor     string `json:"owner_actor,omitempty"`
		}{LockTTLSeconds: int(params.LockTTL.Seconds()), OwnerActor: ownerActor})
		if err != nil {
			return backend.ErrInternal(op, "marshal args", err)
		}
		req.Args = args
	}
	resp, err := sendIPC(b.socketPath, req, dialTimeout, readTimeout)
	if err != nil {
		return err
	}
	return responseToError(resp, op)
}

// ReleaseIssueLock releases the operational lock on an issue via the daemon
// IPC socket. The actor argument is ignored: the daemon authoritatively uses
// the connected agent name.
func (b *Backend) ReleaseIssueLock(_ context.Context, id, _ string) error {
	const op = "AgentIPC.ReleaseIssueLock"
	req := ipcRequest{
		Operation: opReleaseLock,
		AgentName: b.agentName,
		IssueID:   id,
	}
	resp, err := sendIPC(b.socketPath, req, dialTimeout, readTimeout)
	if err != nil {
		return err
	}
	return responseToError(resp, op)
}

// Update applies partial updates to an issue via the daemon IPC socket.
func (b *Backend) Update(_ context.Context, id string, params backend.UpdateParams) error {
	const op = "AgentIPC.Update"
	args, err := json.Marshal(params)
	if err != nil {
		return backend.ErrInternal(op, "marshal args", err)
	}
	req := ipcRequest{
		Operation: opUpdate,
		AgentName: b.agentName,
		IssueID:   id,
		Args:      args,
	}
	resp, err := sendIPC(b.socketPath, req, dialTimeout, readTimeout)
	if err != nil {
		return err
	}
	return responseToError(resp, op)
}

// Close marks an issue as closed via the daemon IPC socket and returns the result.
func (b *Backend) Close(_ context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	const op = "AgentIPC.Close"
	args, err := json.Marshal(params)
	if err != nil {
		return nil, backend.ErrInternal(op, "marshal args", err)
	}
	req := ipcRequest{
		Operation: opComplete,
		AgentName: b.agentName,
		IssueID:   id,
		Args:      args,
	}
	resp, err := sendIPC(b.socketPath, req, dialTimeout, readTimeout)
	if err != nil {
		return nil, err
	}
	if err := responseToError(resp, op); err != nil {
		return nil, err
	}
	var result backend.CloseResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, backend.ErrInternal(op, "failed to decode close result", err)
	}
	return &result, nil
}

// --- Not-implemented methods ---

func (b *Backend) Get(_ context.Context, _ string) (*backend.IssueDetailData, error) {
	return nil, backend.ErrNotImplemented("AgentIPC.Get", "not supported by agent IPC backend")
}

func (b *Backend) List(_ context.Context, _ backend.ListOpts) ([]backend.IssueData, error) {
	return nil, backend.ErrNotImplemented("AgentIPC.List", "not supported by agent IPC backend")
}

func (b *Backend) Ready(_ context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) {
	return nil, backend.ErrNotImplemented("AgentIPC.Ready", "not supported by agent IPC backend")
}

func (b *Backend) Blocked(_ context.Context, _ backend.BlockedOpts) ([]backend.IssueData, error) {
	return nil, backend.ErrNotImplemented("AgentIPC.Blocked", "not supported by agent IPC backend")
}

func (b *Backend) Stats(_ context.Context) (*backend.StatsData, error) {
	return nil, backend.ErrNotImplemented("AgentIPC.Stats", "not supported by agent IPC backend")
}

func (b *Backend) Count(_ context.Context, _ backend.CountOpts) (int, error) {
	return 0, backend.ErrNotImplemented("AgentIPC.Count", "not supported by agent IPC backend")
}

func (b *Backend) GetChildren(_ context.Context, _ string) ([]backend.IssueData, error) {
	return nil, backend.ErrNotImplemented("AgentIPC.GetChildren", "not supported by agent IPC backend")
}

func (b *Backend) SearchIssues(_ context.Context, _ string, _ int) ([]backend.IssueData, error) {
	return nil, backend.ErrNotImplemented("AgentIPC.SearchIssues", "not supported by agent IPC backend")
}

func (b *Backend) Create(_ context.Context, _ backend.CreateParams) (*backend.IssueData, error) {
	return nil, backend.ErrNotImplemented("AgentIPC.Create", "not supported by agent IPC backend")
}

func (b *Backend) Reopen(_ context.Context, _ string, _ backend.ReopenParams) error {
	return backend.ErrNotImplemented("AgentIPC.Reopen", "not supported by agent IPC backend")
}

func (b *Backend) DeferIssue(_ context.Context, _ string, _ time.Time) error {
	return backend.ErrNotImplemented("AgentIPC.DeferIssue", "not supported by agent IPC backend")
}

func (b *Backend) UndeferIssue(_ context.Context, _ string) error {
	return backend.ErrNotImplemented("AgentIPC.UndeferIssue", "not supported by agent IPC backend")
}

func (b *Backend) Delete(_ context.Context, _ backend.DeleteParams) error {
	return backend.ErrNotImplemented("AgentIPC.Delete", "not supported by agent IPC backend")
}

func (b *Backend) AddDependency(_ context.Context, _ backend.DepAddParams) error {
	return backend.ErrNotImplemented("AgentIPC.AddDependency", "not supported by agent IPC backend")
}

func (b *Backend) RemoveDependency(_ context.Context, _ backend.DepRemoveParams) error {
	return backend.ErrNotImplemented("AgentIPC.RemoveDependency", "not supported by agent IPC backend")
}

func (b *Backend) AddLabel(_ context.Context, _ string, _ string) error {
	return backend.ErrNotImplemented("AgentIPC.AddLabel", "not supported by agent IPC backend")
}

func (b *Backend) RemoveLabel(_ context.Context, _ string, _ string) error {
	return backend.ErrNotImplemented("AgentIPC.RemoveLabel", "not supported by agent IPC backend")
}

func (b *Backend) ListComments(_ context.Context, _ string) ([]backend.CommentData, error) {
	return nil, backend.ErrNotImplemented("AgentIPC.ListComments", "not supported by agent IPC backend")
}

func (b *Backend) AddComment(_ context.Context, _ backend.CommentAddParams) (*backend.CommentData, error) {
	return nil, backend.ErrNotImplemented("AgentIPC.AddComment", "not supported by agent IPC backend")
}

func (b *Backend) ListEvents(_ context.Context, _ string, _ int) ([]backend.EventData, error) {
	return nil, backend.ErrNotImplemented("AgentIPC.ListEvents", "not supported by agent IPC backend")
}

func (b *Backend) Batch(_ context.Context, _ []backend.BatchOp) ([]backend.BatchResult, error) {
	return nil, backend.ErrNotImplemented("AgentIPC.Batch", "not supported by agent IPC backend")
}

func (b *Backend) GetMutations(_ context.Context, _ int64) ([]backend.MutationData, error) {
	return nil, backend.ErrNotImplemented("AgentIPC.GetMutations", "use daemon control socket get_mutations instead")
}

func (b *Backend) WaitForMutations(_ context.Context, _ int64, _ int64) ([]backend.MutationData, error) {
	return nil, backend.ErrNotImplemented("AgentIPC.WaitForMutations", "use daemon control socket wait_for_mutations instead")
}

// --- Transport helpers ---

// sendIPC dials the Unix socket, sends one JSON-line request, reads one JSON-line
// response, and disconnects. All transport errors are returned as KindUnavailable.
func sendIPC(socketPath string, req ipcRequest, dialTmout, readTmout time.Duration) (*ipcResponse, error) {
	op := "AgentIPC." + req.Operation

	conn, err := net.DialTimeout("unix", socketPath, dialTmout)
	if err != nil {
		return nil, backend.ErrUnavailable(op, "daemon is not running", err)
	}
	defer func() { _ = conn.Close() }()

	data, err := json.Marshal(req)
	if err != nil {
		return nil, backend.ErrInternal(op, "marshal request", err)
	}
	data = append(data, '\n')

	_ = conn.SetWriteDeadline(time.Now().Add(dialTmout))
	if _, err := conn.Write(data); err != nil {
		return nil, backend.ErrUnavailable(op, "send request", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(readTmout))
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if scanErr := scanner.Err(); scanErr != nil {
			return nil, backend.ErrUnavailable(op, "connection lost", scanErr)
		}
		return nil, backend.ErrUnavailable(op, "empty response from daemon", nil)
	}

	var resp ipcResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, backend.ErrUnavailable(op, "invalid response from daemon", err)
	}
	return &resp, nil
}

// responseToError converts a non-success ipcResponse into the appropriate error.
// Returns nil when resp.Success is true.
func responseToError(resp *ipcResponse, op string) error {
	if resp.Success {
		return nil
	}
	if resp.Kind != "" {
		return backend.NewBackendError(backend.ErrorKind(resp.Kind), op, resp.Error, nil)
	}
	return backend.ErrInternal(op, resp.Error, nil)
}
