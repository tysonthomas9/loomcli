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
	LeaseID    string
	LeaseToken string

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
		LeaseID:        c.LeaseID,
		LeaseToken:     c.LeaseToken,
		LastActivityAt: c.snapshotActivity(),
	}
	resp, err := sendAgentIPCRequest(c.SocketPath, req)
	if err != nil {
		return err
	}
	return ipcResponseToError(resp, "ipc.heartbeat")
}

// InputWait tells the daemon this agent has parked on an interactive prompt
// (phase IPCInputWaitBegin) or that the prompt is resolved (IPCInputWaitEnd),
// so the daemon can suspend — and later resume — its output-timeout idle kill.
//
// It rides the heartbeat operation: the daemon reads the phase off the request
// exactly as it already reads LastActivityAt off every op, so no new operation,
// socket or lease fence is involved.
//
// It deliberately does NOT invent an activity timestamp. The whole premise of
// the signal is that the agent is producing no output; claiming otherwise would
// corrupt the liveness tier the watchdog reads. The daemon stamps the edge with
// its own clock instead.
func (c *AgentIPCClient) InputWait(phase string) error {
	req := AgentIPCRequest{
		Operation:      IPCOpHeartbeat,
		AgentName:      c.AgentName,
		SessionID:      c.SessionID,
		LeaseID:        c.LeaseID,
		LeaseToken:     c.LeaseToken,
		LastActivityAt: c.snapshotActivity(),
		InputWait:      phase,
	}
	resp, err := sendAgentIPCRequest(c.SocketPath, req)
	if err != nil {
		return err
	}
	return ipcResponseToError(resp, "ipc.input_wait")
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
		LeaseID:        c.LeaseID,
		LeaseToken:     c.LeaseToken,
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
		LeaseID:        c.LeaseID,
		LeaseToken:     c.LeaseToken,
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
		Operation:  IPCOpReleaseLock,
		AgentName:  c.AgentName,
		IssueID:    issueID,
		SessionID:  c.SessionID,
		LeaseID:    c.LeaseID,
		LeaseToken: c.LeaseToken,
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
		LeaseID:        c.LeaseID,
		LeaseToken:     c.LeaseToken,
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
// agent's lease (the same fence as Claim/Update/Complete) and uses AgentName as
// the release actor, so no args are carried. Idempotent on the server side:
// releasing an unheld lock is not an error.
func (c *AgentIPCClient) Release(issueID string) error {
	req := AgentIPCRequest{
		Operation:  IPCOpReleaseClaim,
		AgentName:  c.AgentName,
		IssueID:    issueID,
		SessionID:  c.SessionID,
		LeaseID:    c.LeaseID,
		LeaseToken: c.LeaseToken,
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

	data, err := json.Marshal(req)
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
