package daemon

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// AgentIPCRequest is sent by an agent subprocess to the daemon IPC socket.
type AgentIPCRequest struct {
	Operation      string          `json:"operation"`                  // authenticated task query or mutation operation
	AgentName      string          `json:"agent_name"`                 // LOOM_AGENT_NAME identity (required)
	IssueID        string          `json:"issue_id"`                   // target issue (required except for "heartbeat")
	SessionID      string          `json:"session_id,omitempty"`       // supervisor-local transcript session id
	AuthToken      string          `json:"auth_token,omitempty"`       //nolint:gosec // G117: process-local credential must cross the daemon IPC wire.
	Args           json.RawMessage `json:"args,omitempty"`             // operation-specific params
	LastActivityAt time.Time       `json:"last_activity_at,omitempty"` // most recent wrapper PTY-output observation; piggybacked on every op
}

// AgentIPCResponse is sent by the daemon back to the agent subprocess.
type AgentIPCResponse struct {
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
	Kind    string          `json:"kind,omitempty"` // backend.ErrorKind for typed error handling
	Data    json.RawMessage `json:"data,omitempty"`
}

// Operation name constants for agent IPC.
// These string values must stay in sync with the cli package's IPCOp* constants.
const (
	ipcOpGet          = "get"
	ipcOpList         = "list"
	ipcOpReady        = "ready"
	ipcOpBlocked      = "blocked"
	ipcOpAddComment   = "add_comment"
	ipcOpClaim        = "claim"
	ipcOpUpdate       = "update"
	ipcOpComplete     = "complete"
	ipcOpHeartbeat    = "heartbeat"
	ipcOpReleaseLock  = "release_lock"
	ipcOpReleaseClaim = "release_claim"
)

// ipcClaimArgs are the optional arguments for the claim operation.
type ipcClaimArgs struct {
	LockTTLSeconds int `json:"lock_ttl_seconds,omitempty"`
}

// startIPCServer creates a Unix domain socket listener for agent IPC.
// The server accepts connections in a goroutine and dispatches to handlers.
// It closes cleanly when the daemon shuts down.
func (d *Daemon) startIPCServer(socketPath string) error {
	// Remove stale socket from a previous crash (safe because daemon.lock prevents
	// concurrent startup — any existing socket file is orphaned).
	os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("agent IPC socket listen: %w", err)
	}

	// Restrict socket permissions to owner-only (the daemon process)
	if err := os.Chmod(socketPath, 0600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("agent IPC socket chmod: %w", err)
	}

	d.ipcListener = ln
	slog.Info("agent IPC server started", "socket", socketPath)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				// Listener closed (shutdown) — exit silently
				select {
				case <-d.sup.Shutdown:
					return
				default:
				}
				slog.Warn("agent IPC socket accept error", "err", err)
				return
			}
			go d.handleIPCConnection(conn)
		}
	}()

	return nil
}

// handleIPCConnection reads one JSON request, dispatches, writes one JSON response.
func (d *Daemon) handleIPCConnection(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	// Read timeout: 5 seconds for the request
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}

	var req AgentIPCRequest
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		writeIPCResponse(conn, AgentIPCResponse{Error: "invalid request: " + err.Error()})
		return
	}

	if resp, ok := validateIPCRequest(req); !ok {
		writeIPCResponse(conn, resp)
		return
	}

	// Write timeout: 10 seconds for the response (backend calls may hit storage)
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

	resp := d.dispatchIPCOperation(req)

	slog.Debug("agent IPC request handled",
		"operation", req.Operation,
		"agent", req.AgentName,
		"issue", req.IssueID,
		"success", resp.Success,
	)

	writeIPCResponse(conn, resp)
}

// validateIPCRequest checks required fields. Returns (response, false) on failure.
// Workspace-scoped list views and heartbeat requests are exempt from the
// IssueID requirement.
func validateIPCRequest(req AgentIPCRequest) (AgentIPCResponse, bool) {
	if req.AgentName == "" {
		return AgentIPCResponse{
			Error: "agent_name is required",
			Kind:  string(backend.KindValidation),
		}, false
	}
	if ipcOperationRequiresIssueID(req.Operation) && req.IssueID == "" {
		return AgentIPCResponse{
			Error: "issue_id is required",
			Kind:  string(backend.KindValidation),
		}, false
	}
	return AgentIPCResponse{}, true
}

func ipcOperationRequiresIssueID(operation string) bool {
	switch operation {
	case ipcOpHeartbeat, ipcOpList, ipcOpReady, ipcOpBlocked:
		return false
	default:
		return true
	}
}

// dispatchIPCOperation routes to the appropriate handler based on operation.
//
// Method-not-found cases short-circuit BEFORE a tracing span is started:
// emitting one span per unknown-operation attempt would inflate the
// rpc.method label cardinality with arbitrary attacker-supplied values.
// Known methods are wrapped in an OTel span (see startIPCSpan); the
// returned ctx is currently unused by the per-method handlers (which
// build their own context.WithTimeout) but is set up so future refactors
// can thread it through to inherit the span as parent.
func (d *Daemon) dispatchIPCOperation(req AgentIPCRequest) AgentIPCResponse {
	switch req.Operation {
	case ipcOpGet, ipcOpList, ipcOpReady, ipcOpBlocked, ipcOpAddComment,
		ipcOpClaim, ipcOpUpdate, ipcOpComplete, ipcOpHeartbeat, ipcOpReleaseLock, ipcOpReleaseClaim:
		// known method — fall through to traced dispatch below
	default:
		return AgentIPCResponse{Error: fmt.Sprintf("unknown operation: %q", req.Operation)}
	}

	ctx, span := d.startIPCSpan(context.Background(), req.Operation, req.AgentName)
	defer span.End()
	_ = ctx // reserved for future use; per-method handlers build their own timeout ctx

	var resp AgentIPCResponse
	switch req.Operation {
	case ipcOpGet:
		resp = d.handleIPCGet(req)
	case ipcOpList:
		resp = d.handleIPCList(req)
	case ipcOpReady:
		resp = d.handleIPCReady(req)
	case ipcOpBlocked:
		resp = d.handleIPCBlocked(req)
	case ipcOpAddComment:
		resp = d.handleIPCAddComment(req)
	case ipcOpClaim:
		resp = d.handleIPCClaim(req)
	case ipcOpUpdate:
		resp = d.handleIPCUpdate(req)
	case ipcOpComplete:
		resp = d.handleIPCComplete(req)
	case ipcOpHeartbeat:
		resp = d.handleIPCHeartbeat(req)
	case ipcOpReleaseLock:
		resp = d.handleIPCReleaseLock(req)
	case ipcOpReleaseClaim:
		resp = d.handleIPCReleaseClaim(req)
	}
	if resp.Success {
		// Every successful op also advances per-agent liveness when a
		// timestamp was attached. Done last so a half-applied mutation
		// (claim succeeded but heartbeat write failed — impossible
		// today, but defensive) cannot mask the mutation outcome.
		d.recordIPCActivity(req)
	}
	recordIPCErr(span, resp)
	return resp
}

func (d *Daemon) handleIPCGet(req AgentIPCRequest) AgentIPCResponse {
	if resp, ok := d.validateIPCSession(req); !ok {
		return resp
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := d.issueBackend.Get(ctx, req.IssueID)
	if err != nil {
		return ipcErrorResponse(err)
	}
	return marshalIPCResult(result)
}

func (d *Daemon) handleIPCList(req AgentIPCRequest) AgentIPCResponse {
	var opts backend.ListOpts
	if resp, ok := decodeIPCArgs(req.Args, &opts); !ok {
		return resp
	}
	if resp, ok := d.validateIPCSession(req); !ok {
		return resp
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := d.issueBackend.List(ctx, opts)
	if err != nil {
		return ipcErrorResponse(err)
	}
	return marshalIPCResult(result)
}

func (d *Daemon) handleIPCReady(req AgentIPCRequest) AgentIPCResponse {
	var opts backend.ReadyOpts
	if resp, ok := decodeIPCArgs(req.Args, &opts); !ok {
		return resp
	}
	if resp, ok := d.validateIPCSession(req); !ok {
		return resp
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := d.issueBackend.Ready(ctx, opts)
	if err != nil {
		return ipcErrorResponse(err)
	}
	return marshalIPCResult(result)
}

func (d *Daemon) handleIPCBlocked(req AgentIPCRequest) AgentIPCResponse {
	var opts backend.BlockedOpts
	if resp, ok := decodeIPCArgs(req.Args, &opts); !ok {
		return resp
	}
	if resp, ok := d.validateIPCSession(req); !ok {
		return resp
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := d.issueBackend.Blocked(ctx, opts)
	if err != nil {
		return ipcErrorResponse(err)
	}
	return marshalIPCResult(result)
}

func (d *Daemon) handleIPCAddComment(req AgentIPCRequest) AgentIPCResponse {
	var params backend.CommentAddParams
	if resp, ok := decodeIPCArgs(req.Args, &params); !ok {
		return resp
	}
	if params.IssueID != req.IssueID {
		return AgentIPCResponse{
			Error: "comment issue_id must match request issue_id",
			Kind:  string(backend.KindValidation),
		}
	}
	if resp, ok := d.validateIPCSession(req); !ok {
		return resp
	}
	// The actor is the authenticated daemon session, never caller-controlled
	// comment JSON.
	params.Author = req.AgentName
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := d.issueBackend.AddComment(ctx, params)
	if err != nil {
		return ipcErrorResponse(err)
	}
	return marshalIPCResult(result)
}

func decodeIPCArgs(raw json.RawMessage, target any) (AgentIPCResponse, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return AgentIPCResponse{}, true
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return AgentIPCResponse{
			Error: "invalid operation args: " + err.Error(),
			Kind:  string(backend.KindValidation),
		}, false
	}
	return AgentIPCResponse{}, true
}

func marshalIPCResult(result any) AgentIPCResponse {
	data, err := json.Marshal(result)
	if err != nil {
		return AgentIPCResponse{
			Error: "failed to marshal operation result: " + err.Error(),
			Kind:  string(backend.KindInternal),
		}
	}
	return AgentIPCResponse{Success: true, Data: data}
}

// recordIPCActivity forwards req.LastActivityAt to the supervisor's per-agent
// liveness sink. No-op when the timestamp is zero or the supervisor is unset.
func (d *Daemon) recordIPCActivity(req AgentIPCRequest) {
	if d.sup == nil || req.LastActivityAt.IsZero() {
		return
	}
	d.sup.RecordAgentActivity(req.AgentName, req.LastActivityAt)
}

// handleIPCHeartbeat handles the "heartbeat" operation. Its only side
// effect is updating per-agent liveness via recordIPCActivity (called
// from dispatchIPCOperation on Success). It still validates the active
// subprocess's process-local credential so unauthenticated callers cannot
// poke at agent state.
func (d *Daemon) handleIPCHeartbeat(req AgentIPCRequest) AgentIPCResponse {
	if resp, ok := d.validateIPCSession(req); !ok {
		return resp
	}
	return AgentIPCResponse{Success: true}
}

// handleIPCClaim handles the "claim" operation.
func (d *Daemon) handleIPCClaim(req AgentIPCRequest) AgentIPCResponse {
	var lockTTL time.Duration
	if len(req.Args) > 0 && string(req.Args) != "null" {
		var args ipcClaimArgs
		if err := json.Unmarshal(req.Args, &args); err != nil {
			return AgentIPCResponse{
				Error: "invalid claim args: " + err.Error(),
				Kind:  string(backend.KindValidation),
			}
		}
		if args.LockTTLSeconds > 0 {
			lockTTL = time.Duration(args.LockTTLSeconds) * time.Second
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if resp, ok := d.validateIPCSession(req); !ok {
		return resp
	}
	var err error
	if actorBackend, ok := d.issueBackend.(interface {
		ClaimIssueAsActor(context.Context, string, time.Duration, string) error
	}); ok {
		err = actorBackend.ClaimIssueAsActor(ctx, req.IssueID, lockTTL, req.AgentName)
	} else {
		err = d.issueBackend.ClaimIssue(ctx, req.IssueID, lockTTL)
	}
	if err != nil {
		return ipcErrorResponse(err)
	}

	d.publishMutation(backend.MutationData{
		Type:      backend.MutationStatus,
		IssueID:   req.IssueID,
		Actor:     req.AgentName,
		NewStatus: "in_progress",
	})

	return AgentIPCResponse{Success: true}
}

// handleIPCUpdate handles the "update" operation.
func (d *Daemon) handleIPCUpdate(req AgentIPCRequest) AgentIPCResponse {
	var params backend.UpdateParams
	if len(req.Args) > 0 && string(req.Args) != "null" {
		if err := json.Unmarshal(req.Args, &params); err != nil {
			return AgentIPCResponse{
				Error: "invalid update args: " + err.Error(),
				Kind:  string(backend.KindValidation),
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if resp, ok := d.validateIPCSession(req); !ok {
		return resp
	}
	if err := d.issueBackend.Update(ctx, req.IssueID, params); err != nil {
		return ipcErrorResponse(err)
	}

	d.publishMutation(backend.MutationData{
		Type:    backend.MutationUpdate,
		IssueID: req.IssueID,
		Actor:   req.AgentName,
	})

	return AgentIPCResponse{Success: true}
}

// handleIPCComplete handles the "complete" operation.
func (d *Daemon) handleIPCComplete(req AgentIPCRequest) AgentIPCResponse {
	var params backend.CloseParams
	if len(req.Args) > 0 && string(req.Args) != "null" {
		if err := json.Unmarshal(req.Args, &params); err != nil {
			return AgentIPCResponse{
				Error: "invalid complete args: " + err.Error(),
				Kind:  string(backend.KindValidation),
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if resp, ok := d.validateIPCSession(req); !ok {
		return resp
	}
	result, err := d.issueBackend.Close(ctx, req.IssueID, params)
	if err != nil {
		return ipcErrorResponse(err)
	}

	data, err := json.Marshal(result)
	if err != nil {
		return AgentIPCResponse{
			Error: "failed to marshal close result: " + err.Error(),
			Kind:  string(backend.KindInternal),
		}
	}

	mut := backend.MutationData{
		Type:      backend.MutationStatus,
		IssueID:   req.IssueID,
		Actor:     req.AgentName,
		OldStatus: "in_progress",
		NewStatus: "closed",
	}
	if result.Closed != nil {
		mut.Title = result.Closed.Title
		mut.ParentID = result.Closed.Parent
	}
	d.publishMutation(mut)

	return AgentIPCResponse{Success: true, Data: data}
}

// handleIPCReleaseLock handles the "release_lock" operation: drop only the
// fleet-db claim lock for the agent's issue without changing its status.
// Idempotent: a missing or already-released lock returns success.
func (d *Daemon) handleIPCReleaseLock(req AgentIPCRequest) AgentIPCResponse {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if resp, ok := d.validateIPCSession(req); !ok {
		return resp
	}
	if err := d.issueBackend.ReleaseIssueLock(ctx, req.IssueID, req.AgentName); err != nil {
		return ipcErrorResponse(err)
	}
	return AgentIPCResponse{Success: true}
}

// handleIPCReleaseClaim handles the "release_claim" operation behind the same
// active-subprocess credential check as the other mutations. The daemon
// supplies the authenticated agent name as the release actor; the backend
// must not derive the actor from mutable issue state. Backends without an
// explicit claim lock (non-fleet) report success without acting — release is a
// no-op there.
func (d *Daemon) handleIPCReleaseClaim(req AgentIPCRequest) AgentIPCResponse {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if resp, ok := d.validateIPCSession(req); !ok {
		return resp
	}
	releaser, ok := d.issueBackend.(backend.ClaimReleaser)
	if !ok {
		return AgentIPCResponse{Success: true}
	}
	if err := releaser.ReleaseClaim(ctx, req.IssueID, req.AgentName); err != nil {
		return ipcErrorResponse(err)
	}

	// Do not publish a synthetic daemon mutation here. The backend either wrote
	// the real in_progress→open release mutation, or it only dropped an
	// operational lock for an issue whose status was already changed.
	return AgentIPCResponse{Success: true}
}

func (d *Daemon) validateIPCSession(req AgentIPCRequest) (AgentIPCResponse, bool) {
	if d.sup == nil || d.sup.WorkspaceID == "" {
		return AgentIPCResponse{}, true
	}
	if req.SessionID == "" || req.AuthToken == "" {
		return AgentIPCResponse{
			Error: "session_id and auth_token are required for fenced daemon IPC operations",
			Kind:  string(backend.KindValidation),
		}, false
	}

	d.sup.AgentsMu.RLock()
	agents := append([]*supervisor.AgentProcess(nil), d.sup.Agents...)
	d.sup.AgentsMu.RUnlock()
	for _, agent := range agents {
		if agent == nil || agent.Entry.Worktree != req.AgentName {
			continue
		}
		agent.Mu.Lock()
		sessionID := agent.AgentSessionID
		authToken := agent.AgentIPCAuthToken
		agent.Mu.Unlock()
		if sessionID != req.SessionID ||
			subtle.ConstantTimeCompare([]byte(authToken), []byte(req.AuthToken)) != 1 {
			return AgentIPCResponse{
				Error: "IPC credential does not match the active agent session",
				Kind:  string(backend.KindConflict),
			}, false
		}
		return AgentIPCResponse{}, true
	}
	return AgentIPCResponse{
		Error: "active agent session not found",
		Kind:  string(backend.KindConflict),
	}, false
}

// ipcErrorResponse converts a backend error into an AgentIPCResponse.
func ipcErrorResponse(err error) AgentIPCResponse {
	var be *backend.BackendError
	if errors.As(err, &be) {
		return AgentIPCResponse{
			Error: be.Message,
			Kind:  string(be.Kind),
		}
	}
	return AgentIPCResponse{
		Error: err.Error(),
		Kind:  string(backend.KindInternal),
	}
}

// resolveAgentIPCSocketPath returns the IPC socket path adjacent to the PID file.
func resolveAgentIPCSocketPath(projectDir, pidFile string) string {
	pidFilePath := supervisor.ResolveDaemonPath(projectDir, pidFile)
	return filepath.Join(filepath.Dir(pidFilePath), "agent-ipc.sock")
}

// writeIPCResponse writes a JSON response line to the connection.
func writeIPCResponse(conn net.Conn, resp AgentIPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = conn.Write(data)
}
