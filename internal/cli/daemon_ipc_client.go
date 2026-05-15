package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// AgentIPCClient is the IPC client for agent subprocesses to communicate with
// the daemon's agent IPC socket. Each method dials the socket, sends one JSON
// request, reads one JSON response, and disconnects (stateless, one-shot).
// Safe for concurrent use — no mutable state.
type AgentIPCClient struct {
	SocketPath string
	AgentName  string
	SessionID  string
	LeaseID    string
	LeaseToken string
}

// NewAgentIPCClient returns an IPC client that will connect to the given socket
// path and identify as the given agent. It does NOT read environment variables.
func NewAgentIPCClient(socketPath, agentName string) *AgentIPCClient {
	return &AgentIPCClient{SocketPath: socketPath, AgentName: agentName}
}

// Claim atomically claims an issue for this agent. Pass lockTTL=0 to use the
// server's default TTL. Returns *backend.BackendError with KindConflict if
// already claimed, KindNotFound if issue missing.
func (c *AgentIPCClient) Claim(issueID string, lockTTL time.Duration) error {
	req := AgentIPCRequest{
		Operation:  IPCOpClaim,
		AgentName:  c.AgentName,
		IssueID:    issueID,
		SessionID:  c.SessionID,
		LeaseID:    c.LeaseID,
		LeaseToken: c.LeaseToken,
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
		Operation:  IPCOpUpdate,
		AgentName:  c.AgentName,
		IssueID:    issueID,
		SessionID:  c.SessionID,
		LeaseID:    c.LeaseID,
		LeaseToken: c.LeaseToken,
		Args:       args,
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
		Operation:  IPCOpComplete,
		AgentName:  c.AgentName,
		IssueID:    issueID,
		SessionID:  c.SessionID,
		LeaseID:    c.LeaseID,
		LeaseToken: c.LeaseToken,
		Args:       args,
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
