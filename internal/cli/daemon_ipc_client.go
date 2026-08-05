package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// AgentIPCClient is the IPC client for agent subprocesses to communicate with
// the daemon's agent IPC socket. Each method dials the socket, sends one JSON
// request, reads one JSON response, and disconnects (stateless, one-shot).
// Safe for concurrent use — the lastActivity field is only written through
// SetLastActivity (sync.Mutex), and the other fields are set once at
// construction.
type AgentIPCClient struct {
	SocketPath string
	AgentName  string
	SessionID  string
	AuthToken  string

	activityMu     sync.Mutex
	lastActivityAt time.Time
}

// NewAgentIPCClient returns an IPC client that will connect to the given socket
// path and identify as the given agent. It does NOT read environment variables.
func NewAgentIPCClient(socketPath, agentName string) *AgentIPCClient {
	return &AgentIPCClient{SocketPath: socketPath, AgentName: agentName}
}

// SetLastActivity records the most recent wrapper PTY-output timestamp the
// caller has observed. Subsequent IPC requests carry it so the daemon can
// update per-agent liveness without a dedicated heartbeat round trip.
// Zero timestamps are accepted and not propagated.
func (c *AgentIPCClient) SetLastActivity(at time.Time) {
	c.activityMu.Lock()
	if at.After(c.lastActivityAt) {
		c.lastActivityAt = at
	}
	c.activityMu.Unlock()
}

func (c *AgentIPCClient) snapshotActivity() time.Time {
	c.activityMu.Lock()
	defer c.activityMu.Unlock()
	return c.lastActivityAt
}

// Heartbeat sends a "I am alive" ping carrying the latest LastActivityAt to
// the daemon. It is safe to call between mutations; idle agents that aren't
// mutating still need this to keep the daemon's per-agent liveness fresh.
// Failure to send is returned but is not fatal — the caller typically logs
// and continues.
func (c *AgentIPCClient) Heartbeat(at time.Time) error {
	c.SetLastActivity(at)
	req := AgentIPCRequest{
		Operation:      IPCOpHeartbeat,
		AgentName:      c.AgentName,
		SessionID:      c.SessionID,
		AuthToken:      c.AuthToken,
		LastActivityAt: c.snapshotActivity(),
	}
	resp, err := sendAgentIPCRequest(c.SocketPath, req)
	if err != nil {
		return err
	}
	return ipcResponseToError(resp, "ipc.heartbeat")
}

// Get loads one task through the daemon-owned issue backend. Controlled
// agents use this path so FleetDB's service credential remains in the daemon.
func (c *AgentIPCClient) Get(issueID string) (*backend.IssueDetailData, error) {
	var result backend.IssueDetailData
	if err := c.query(IPCOpGet, issueID, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// List returns the workspace task list visible to the authenticated agent.
func (c *AgentIPCClient) List(opts backend.ListOpts) ([]backend.IssueData, error) {
	var result []backend.IssueData
	if err := c.query(IPCOpList, "", opts, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// Ready returns the canonical ready view through the daemon-owned backend.
func (c *AgentIPCClient) Ready(opts backend.ReadyOpts) ([]backend.IssueData, error) {
	var result []backend.IssueData
	if err := c.query(IPCOpReady, "", opts, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// Blocked returns the canonical blocked view through the daemon-owned backend.
func (c *AgentIPCClient) Blocked(opts backend.BlockedOpts) ([]backend.IssueData, error) {
	var result []backend.IssueData
	if err := c.query(IPCOpBlocked, "", opts, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// AddComment adds a task comment through the same fenced daemon session as
// Update and Complete.
func (c *AgentIPCClient) AddComment(params backend.CommentAddParams) (*backend.CommentData, error) {
	var result backend.CommentData
	if err := c.query(IPCOpAddComment, params.IssueID, params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *AgentIPCClient) query(operation, issueID string, args, result any) error {
	var raw json.RawMessage
	if args != nil {
		encoded, err := json.Marshal(args)
		if err != nil {
			return backend.ErrInternal("ipc."+operation, "marshal args", err)
		}
		raw = encoded
	}
	req := AgentIPCRequest{
		Operation:      operation,
		AgentName:      c.AgentName,
		IssueID:        issueID,
		SessionID:      c.SessionID,
		AuthToken:      c.AuthToken,
		Args:           raw,
		LastActivityAt: c.snapshotActivity(),
	}
	resp, err := sendAgentIPCRequest(c.SocketPath, req)
	if err != nil {
		return err
	}
	if err := ipcResponseToError(resp, "ipc."+operation); err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Data, result); err != nil {
		return backend.ErrInternal("ipc."+operation, "failed to decode result", err)
	}
	return nil
}

// Claim atomically claims an issue for this agent. Pass lockTTL=0 to use the
// server's default TTL. Returns *backend.BackendError with KindConflict if
// already claimed, KindNotFound if issue missing.
func (c *AgentIPCClient) Claim(issueID string, lockTTL time.Duration) error {
	req := AgentIPCRequest{
		Operation:      IPCOpClaim,
		AgentName:      c.AgentName,
		IssueID:        issueID,
		SessionID:      c.SessionID,
		AuthToken:      c.AuthToken,
		LastActivityAt: c.snapshotActivity(),
	}

	if lockTTL > 0 {
		args, err := json.Marshal(IPCClaimArgs{LockTTLSeconds: int(lockTTL.Seconds())})
		if err != nil {
			return backend.ErrInternal("ipc.claim", "marshal args", err)
		}
		req.Args = args
	}

	resp, err := sendAgentIPCRequest(c.SocketPath, req)
	if err != nil {
		return err
	}
	return ipcResponseToError(resp, "ipc.claim")
}

// Update applies partial updates to an issue.
func (c *AgentIPCClient) Update(issueID string, params backend.UpdateParams) error {
	args, err := json.Marshal(params)
	if err != nil {
		return backend.ErrInternal("ipc.update", "marshal args", err)
	}

	req := AgentIPCRequest{
		Operation:      IPCOpUpdate,
		AgentName:      c.AgentName,
		IssueID:        issueID,
		SessionID:      c.SessionID,
		AuthToken:      c.AuthToken,
		Args:           args,
		LastActivityAt: c.snapshotActivity(),
	}

	resp, err := sendAgentIPCRequest(c.SocketPath, req)
	if err != nil {
		return err
	}
	return ipcResponseToError(resp, "ipc.update")
}

// ReleaseLock drops only the operational claim lock for an issue without
// changing its status or assignee. Idempotent: missing lock returns nil.
func (c *AgentIPCClient) ReleaseLock(issueID string) error {
	req := AgentIPCRequest{
		Operation: IPCOpReleaseLock,
		AgentName: c.AgentName,
		IssueID:   issueID,
		SessionID: c.SessionID,
		AuthToken: c.AuthToken,
	}

	resp, err := sendAgentIPCRequest(c.SocketPath, req)
	if err != nil {
		return err
	}
	return ipcResponseToError(resp, "ipc.release_lock")
}

// Complete closes an issue and returns the CloseResult (including unblocked issues).
func (c *AgentIPCClient) Complete(issueID string, params backend.CloseParams) (*backend.CloseResult, error) {
	args, err := json.Marshal(params)
	if err != nil {
		return nil, backend.ErrInternal("ipc.complete", "marshal args", err)
	}

	req := AgentIPCRequest{
		Operation:      IPCOpComplete,
		AgentName:      c.AgentName,
		IssueID:        issueID,
		SessionID:      c.SessionID,
		AuthToken:      c.AuthToken,
		Args:           args,
		LastActivityAt: c.snapshotActivity(),
	}

	resp, err := sendAgentIPCRequest(c.SocketPath, req)
	if err != nil {
		return nil, err
	}
	if err := ipcResponseToError(resp, "ipc.complete"); err != nil {
		return nil, err
	}

	var result backend.CloseResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, backend.ErrInternal("ipc.complete", "failed to decode close result", err)
	}
	return &result, nil
}

// Release completes this agent's claim on an issue. The daemon validates this
// agent's process-local credential (the same fence as Claim/Update/Complete)
// and uses AgentName as
// the release actor, so no args are carried. Idempotent on the server side:
// releasing an unheld lock is not an error.
func (c *AgentIPCClient) Release(issueID string) error {
	req := AgentIPCRequest{
		Operation: IPCOpReleaseClaim,
		AgentName: c.AgentName,
		IssueID:   issueID,
		SessionID: c.SessionID,
		AuthToken: c.AuthToken,
	}

	resp, err := sendAgentIPCRequest(c.SocketPath, req)
	if err != nil {
		return err
	}
	return ipcResponseToError(resp, "ipc.release")
}

// sendAgentIPCRequest dials the Unix socket, sends one JSON-line request, reads
// one JSON-line response, and disconnects. All transport errors are returned as
// backend.ErrUnavailable.
func sendAgentIPCRequest(socketPath string, req AgentIPCRequest) (*AgentIPCResponse, error) {
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return nil, backend.ErrUnavailable("ipc."+req.Operation, "daemon is not running", err)
	}
	defer func() { _ = conn.Close() }()

	data, err := json.Marshal(req) // #nosec G117 -- AuthToken is intentionally serialized over the owner-only local daemon socket.
	if err != nil {
		return nil, backend.ErrInternal("ipc."+req.Operation, "marshal request", err)
	}
	data = append(data, '\n')

	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(data); err != nil {
		return nil, backend.ErrUnavailable("ipc."+req.Operation, "send request", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if scanErr := scanner.Err(); scanErr != nil {
			return nil, backend.ErrUnavailable("ipc."+req.Operation, "connection lost", scanErr)
		}
		return nil, backend.ErrUnavailable("ipc."+req.Operation, "empty response from daemon", nil)
	}

	var resp AgentIPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, backend.ErrUnavailable("ipc."+req.Operation, "invalid response", err)
	}
	return &resp, nil
}

// ipcResponseToError converts a non-success AgentIPCResponse into the
// appropriate error type. Returns nil when resp.Success is true.
func ipcResponseToError(resp *AgentIPCResponse, op string) error {
	if resp.Success {
		return nil
	}
	if resp.Kind != "" {
		return backend.NewBackendError(backend.ErrorKind(resp.Kind), op, resp.Error, nil)
	}
	return fmt.Errorf("ipc %s: %s", op, resp.Error)
}
