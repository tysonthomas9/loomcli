package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/notify"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// mockIPCBackend is a minimal IssueBackend implementation for IPC server tests.
// It records calls and returns configurable results/errors.
type mockIPCBackend struct {
	// Recorded calls
	getCalls     []string
	listCalls    []backend.ListOpts
	readyCalls   []backend.ReadyOpts
	blockedCalls []backend.BlockedOpts
	commentCalls []backend.CommentAddParams
	claimCalls   []mockClaimCall
	updateCalls  []mockUpdateCall
	closeCalls   []mockCloseCall
	releaseCalls []mockReleaseCall

	// Configurable returns
	getResult     *backend.IssueDetailData
	getErr        error
	listResult    []backend.IssueData
	listErr       error
	readyResult   []backend.IssueData
	readyErr      error
	blockedResult []backend.IssueData
	blockedErr    error
	commentResult *backend.CommentData
	commentErr    error
	claimErr      error
	updateErr     error
	closeErr      error
	closeResult   *backend.CloseResult
	releaseErr    error
}

// ReleaseClaim records the call so handleIPCReleaseClaim tests can assert the
// backend was reached. Implementing it makes *mockIPCBackend satisfy
// backend.ClaimReleaser (the capability the release handler type-asserts).
func (m *mockIPCBackend) ReleaseClaim(_ context.Context, id, actor string) error {
	m.releaseCalls = append(m.releaseCalls, mockReleaseCall{ID: id, Actor: actor})
	return m.releaseErr
}

type mockClaimCall struct {
	ID      string
	LockTTL time.Duration
	Actor   string
}

type mockUpdateCall struct {
	ID     string
	Params backend.UpdateParams
}

type mockCloseCall struct {
	ID     string
	Params backend.CloseParams
}

type mockReleaseCall struct {
	ID    string
	Actor string
}

func (m *mockIPCBackend) ClaimIssue(_ context.Context, id string, lockTTL time.Duration) error {
	m.claimCalls = append(m.claimCalls, mockClaimCall{ID: id, LockTTL: lockTTL})
	return m.claimErr
}

type actorClaimIPCBackend struct {
	*mockIPCBackend
}

func (m *actorClaimIPCBackend) ClaimIssueAsActor(_ context.Context, id string, lockTTL time.Duration, actor string) error {
	m.claimCalls = append(m.claimCalls, mockClaimCall{ID: id, LockTTL: lockTTL, Actor: actor})
	return m.claimErr
}

func (m *mockIPCBackend) ReleaseIssueLock(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockIPCBackend) Update(_ context.Context, id string, params backend.UpdateParams) error {
	m.updateCalls = append(m.updateCalls, mockUpdateCall{ID: id, Params: params})
	return m.updateErr
}

func (m *mockIPCBackend) Close(_ context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	m.closeCalls = append(m.closeCalls, mockCloseCall{ID: id, Params: params})
	if m.closeErr != nil {
		return nil, m.closeErr
	}
	if m.closeResult != nil {
		return m.closeResult, nil
	}
	return &backend.CloseResult{
		Closed: &backend.IssueData{ID: id, Title: "test", Status: "closed"},
	}, nil
}

func (m *mockIPCBackend) Get(_ context.Context, id string) (*backend.IssueDetailData, error) {
	m.getCalls = append(m.getCalls, id)
	return m.getResult, m.getErr
}
func (m *mockIPCBackend) List(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	m.listCalls = append(m.listCalls, opts)
	return m.listResult, m.listErr
}
func (m *mockIPCBackend) Ready(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	m.readyCalls = append(m.readyCalls, opts)
	return m.readyResult, m.readyErr
}
func (m *mockIPCBackend) Blocked(_ context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	m.blockedCalls = append(m.blockedCalls, opts)
	return m.blockedResult, m.blockedErr
}
func (m *mockIPCBackend) Stats(context.Context) (*backend.StatsData, error) {
	panic("not implemented")
}
func (m *mockIPCBackend) Count(context.Context, backend.CountOpts) (int, error) {
	panic("not implemented")
}
func (m *mockIPCBackend) GetChildren(context.Context, string) ([]backend.IssueData, error) {
	panic("not implemented")
}
func (m *mockIPCBackend) SearchIssues(context.Context, string, int) ([]backend.IssueData, error) {
	panic("not implemented")
}
func (m *mockIPCBackend) Create(context.Context, backend.CreateParams) (*backend.IssueData, error) {
	panic("not implemented")
}
func (m *mockIPCBackend) Reopen(context.Context, string, backend.ReopenParams) error {
	panic("not implemented")
}
func (m *mockIPCBackend) DeferIssue(context.Context, string, time.Time) error {
	panic("not implemented")
}
func (m *mockIPCBackend) UndeferIssue(context.Context, string) error {
	panic("not implemented")
}
func (m *mockIPCBackend) Delete(context.Context, backend.DeleteParams) error {
	panic("not implemented")
}
func (m *mockIPCBackend) AddDependency(context.Context, backend.DepAddParams) error {
	panic("not implemented")
}
func (m *mockIPCBackend) RemoveDependency(context.Context, backend.DepRemoveParams) error {
	panic("not implemented")
}
func (m *mockIPCBackend) AddLabel(context.Context, string, string) error {
	panic("not implemented")
}
func (m *mockIPCBackend) RemoveLabel(context.Context, string, string) error {
	panic("not implemented")
}
func (m *mockIPCBackend) ListComments(context.Context, string) ([]backend.CommentData, error) {
	panic("not implemented")
}
func (m *mockIPCBackend) AddComment(_ context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	m.commentCalls = append(m.commentCalls, params)
	return m.commentResult, m.commentErr
}
func (m *mockIPCBackend) ListEvents(context.Context, string, int) ([]backend.EventData, error) {
	panic("not implemented")
}
func (m *mockIPCBackend) Batch(context.Context, []backend.BatchOp) ([]backend.BatchResult, error) {
	panic("not implemented")
}
func (m *mockIPCBackend) GetMutations(context.Context, int64) ([]backend.MutationData, error) {
	panic("not implemented")
}
func (m *mockIPCBackend) WaitForMutations(context.Context, int64, int64) ([]backend.MutationData, error) {
	panic("not implemented")
}
func (m *mockIPCBackend) BackendName() string { return "mock" }

// newTestIPCDaemon constructs a Daemon with a mock backend for IPC server tests.
func newTestIPCDaemon(mb *mockIPCBackend) *Daemon {
	d := &Daemon{
		config:       makeDaemonConfig(nil, nil),
		issueBackend: mb,
	}
	d.sup = &supervisor.Supervisor{
		ConfigSnapshot: d.configSnapshot,
		Agents:         make([]*supervisor.AgentProcess, 0),
		StoppedAgents:  make(map[string]struct{}),
		Shutdown:       make(chan struct{}),
		Concurrency:    supervisor.NewConcurrencyTracker(nil),
		EmitEvent:      func(events.Event) {},
	}
	return d
}

func configureIPCTestAuth(d *Daemon, agentName, sessionID, token string) {
	d.sup.WorkspaceID = "WS"
	d.sup.Agents = []*supervisor.AgentProcess{{
		Entry:             config.AgentEntry{Worktree: agentName},
		AgentSessionID:    sessionID,
		AgentIPCAuthToken: token,
	}}
}

// sendIPCRequest connects to the IPC socket, sends a request, reads the response.
func sendIPCRequest(t *testing.T, socketPath string, req AgentIPCRequest) AgentIPCResponse {
	t.Helper()

	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial IPC socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	data, err := json.Marshal(req) // #nosec G117 -- test helper intentionally exercises local IPC token serialization.
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write request: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			t.Fatalf("read response: %v", err)
		}
		t.Fatal("empty response from IPC server")
	}

	var resp AgentIPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

func TestIPCServer_TaskQueriesAndCommentsRequireSessionCredential(t *testing.T) {
	mb := &mockIPCBackend{
		getResult:     &backend.IssueDetailData{IssueData: backend.IssueData{ID: "task-1"}},
		listResult:    []backend.IssueData{{ID: "task-1"}},
		readyResult:   []backend.IssueData{{ID: "task-2"}},
		blockedResult: []backend.IssueData{{ID: "task-3"}},
		commentResult: &backend.CommentData{IssueID: "task-1", Text: "review"},
	}
	d := newTestIPCDaemon(mb)
	configureIPCTestAuth(d, "planner", "sess-1", "token-1")

	bad := d.handleIPCGet(AgentIPCRequest{
		Operation: ipcOpGet, AgentName: "planner", IssueID: "task-1",
		SessionID: "sess-1", AuthToken: "wrong",
	})
	if bad.Success || len(mb.getCalls) != 0 {
		t.Fatalf("unauthenticated get reached backend: response=%+v calls=%v", bad, mb.getCalls)
	}

	requests := []AgentIPCRequest{
		{Operation: ipcOpGet, AgentName: "planner", IssueID: "task-1", SessionID: "sess-1", AuthToken: "token-1"},
		{Operation: ipcOpList, AgentName: "planner", SessionID: "sess-1", AuthToken: "token-1", Args: json.RawMessage(`{"limit":5}`)},
		{Operation: ipcOpReady, AgentName: "planner", SessionID: "sess-1", AuthToken: "token-1", Args: json.RawMessage(`{"limit":6}`)},
		{Operation: ipcOpBlocked, AgentName: "planner", SessionID: "sess-1", AuthToken: "token-1", Args: json.RawMessage(`{"limit":7}`)},
		{Operation: ipcOpAddComment, AgentName: "planner", IssueID: "task-1", SessionID: "sess-1", AuthToken: "token-1", Args: json.RawMessage(`{"issue_id":"task-1","text":"review"}`)},
	}
	for _, req := range requests {
		resp := d.dispatchIPCOperation(req)
		if !resp.Success {
			t.Fatalf("%s failed: %+v", req.Operation, resp)
		}
		if len(resp.Data) == 0 {
			t.Fatalf("%s returned no data", req.Operation)
		}
	}
	if len(mb.getCalls) != 1 || mb.listCalls[0].Limit != 5 ||
		mb.readyCalls[0].Limit != 6 || mb.blockedCalls[0].Limit != 7 ||
		len(mb.commentCalls) != 1 || mb.commentCalls[0].Author != "planner" {
		t.Fatalf("backend calls get=%v list=%v ready=%v blocked=%v comments=%v",
			mb.getCalls, mb.listCalls, mb.readyCalls, mb.blockedCalls, mb.commentCalls)
	}
}

func TestIPCServer_AddCommentRejectsMismatchedIssueID(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	resp := d.handleIPCAddComment(AgentIPCRequest{
		Operation: ipcOpAddComment,
		AgentName: "planner",
		IssueID:   "task-1",
		Args:      json.RawMessage(`{"issue_id":"task-2","text":"review"}`),
	})
	if resp.Success || resp.Kind != string(backend.KindValidation) || len(mb.commentCalls) != 0 {
		t.Fatalf("mismatched comment issue accepted: response=%+v calls=%v", resp, mb.commentCalls)
	}
}

func TestIPCServer_ClaimSuccess(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	resp := d.handleIPCClaim(AgentIPCRequest{
		Operation: ipcOpClaim,
		AgentName: "falcon",
		IssueID:   "abc-123",
	})

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if len(mb.claimCalls) != 1 {
		t.Fatalf("expected 1 claim call, got %d", len(mb.claimCalls))
	}
	if mb.claimCalls[0].ID != "abc-123" {
		t.Errorf("claim ID = %q, want %q", mb.claimCalls[0].ID, "abc-123")
	}
	if mb.claimCalls[0].LockTTL != 0 {
		t.Errorf("claim LockTTL = %v, want 0", mb.claimCalls[0].LockTTL)
	}
}

func TestIPCServer_ClaimUsesAuthenticatedAgentAsDelegatedActor(t *testing.T) {
	base := &mockIPCBackend{}
	backend := &actorClaimIPCBackend{mockIPCBackend: base}
	d := newTestIPCDaemon(base)
	d.issueBackend = backend
	configureIPCTestAuth(d, "phase5-repaired-planner", "sess-1", "token-1")

	resp := d.handleIPCClaim(AgentIPCRequest{
		Operation: ipcOpClaim, AgentName: "phase5-repaired-planner", IssueID: "PHASE5-19",
		SessionID: "sess-1", AuthToken: "token-1",
	})
	if !resp.Success {
		t.Fatalf("claim failed: %s", resp.Error)
	}
	if len(base.claimCalls) != 1 || base.claimCalls[0].Actor != "phase5-repaired-planner" {
		t.Fatalf("claim calls = %+v, want authenticated agent actor", base.claimCalls)
	}
}

func TestIPCServer_ClaimRequiresValidProcessCredential(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	configureIPCTestAuth(d, "falcon", "sess-1", "token-1")

	bad := d.handleIPCClaim(AgentIPCRequest{
		Operation: ipcOpClaim,
		AgentName: "falcon",
		IssueID:   "abc-123",
		SessionID: "sess-1",
		AuthToken: "wrong",
	})
	if bad.Success {
		t.Fatal("claim with bad IPC token succeeded")
	}
	if len(mb.claimCalls) != 0 {
		t.Fatalf("backend called for bad token: %d", len(mb.claimCalls))
	}

	good := d.handleIPCClaim(AgentIPCRequest{
		Operation: ipcOpClaim,
		AgentName: "falcon",
		IssueID:   "abc-123",
		SessionID: "sess-1",
		AuthToken: "token-1",
	})
	if !good.Success {
		t.Fatalf("claim with valid IPC credential failed: %s", good.Error)
	}
	if len(mb.claimCalls) != 1 {
		t.Fatalf("backend claim calls = %d, want 1", len(mb.claimCalls))
	}
}

func TestIPCServer_ReleaseClaimSuccess(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	resp := d.handleIPCReleaseClaim(AgentIPCRequest{
		Operation: ipcOpReleaseClaim,
		AgentName: "falcon",
		IssueID:   "abc-123",
	})

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if len(mb.releaseCalls) != 1 || mb.releaseCalls[0].ID != "abc-123" || mb.releaseCalls[0].Actor != "falcon" {
		t.Fatalf("backend ReleaseClaim calls = %+v, want id abc-123 actor falcon", mb.releaseCalls)
	}
}

func TestIPCServer_ReleaseClaimRequiresValidProcessCredential(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	configureIPCTestAuth(d, "falcon", "sess-1", "token-1")

	bad := d.handleIPCReleaseClaim(AgentIPCRequest{
		Operation: ipcOpReleaseClaim,
		AgentName: "falcon",
		IssueID:   "abc-123",
		SessionID: "sess-1",
		AuthToken: "wrong",
	})
	if bad.Success {
		t.Fatal("release with bad IPC token succeeded")
	}
	if len(mb.releaseCalls) != 0 {
		t.Fatalf("backend released for bad token: %d", len(mb.releaseCalls))
	}

	good := d.handleIPCReleaseClaim(AgentIPCRequest{
		Operation: ipcOpReleaseClaim,
		AgentName: "falcon",
		IssueID:   "abc-123",
		SessionID: "sess-1",
		AuthToken: "token-1",
	})
	if !good.Success {
		t.Fatalf("release with valid IPC credential failed: %s", good.Error)
	}
	if len(mb.releaseCalls) != 1 {
		t.Fatalf("backend release calls = %d, want 1", len(mb.releaseCalls))
	}
}

func TestIPCServer_ClaimWithTTL(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	resp := d.handleIPCClaim(AgentIPCRequest{
		Operation: ipcOpClaim,
		AgentName: "falcon",
		IssueID:   "abc-123",
		Args:      json.RawMessage(`{"lock_ttl_seconds": 300}`),
	})

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if mb.claimCalls[0].LockTTL != 300*time.Second {
		t.Errorf("claim LockTTL = %v, want %v", mb.claimCalls[0].LockTTL, 300*time.Second)
	}
}

func TestIPCServer_ClaimConflict(t *testing.T) {
	mb := &mockIPCBackend{
		claimErr: backend.ErrConflict("ClaimIssue", "already claimed by nova"),
	}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	resp := d.handleIPCClaim(AgentIPCRequest{
		Operation: ipcOpClaim,
		AgentName: "falcon",
		IssueID:   "abc-123",
	})

	if resp.Success {
		t.Fatal("expected failure for conflict")
	}
	if resp.Kind != string(backend.KindConflict) {
		t.Errorf("Kind = %q, want %q", resp.Kind, backend.KindConflict)
	}
	if !strings.Contains(resp.Error, "already claimed") {
		t.Errorf("Error = %q, want contains 'already claimed'", resp.Error)
	}
}

func TestIPCServer_ClaimNotFound(t *testing.T) {
	mb := &mockIPCBackend{
		claimErr: backend.ErrNotFound("ClaimIssue", "issue not found"),
	}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	resp := d.handleIPCClaim(AgentIPCRequest{
		Operation: ipcOpClaim,
		AgentName: "falcon",
		IssueID:   "abc-123",
	})

	if resp.Success {
		t.Fatal("expected failure for not found")
	}
	if resp.Kind != string(backend.KindNotFound) {
		t.Errorf("Kind = %q, want %q", resp.Kind, backend.KindNotFound)
	}
}

func TestIPCServer_UpdateSuccess(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	status := "in_progress"
	args, _ := json.Marshal(backend.UpdateParams{Status: &status})

	resp := d.handleIPCUpdate(AgentIPCRequest{
		Operation: ipcOpUpdate,
		AgentName: "falcon",
		IssueID:   "abc-123",
		Args:      args,
	})

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if len(mb.updateCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(mb.updateCalls))
	}
	if mb.updateCalls[0].ID != "abc-123" {
		t.Errorf("update ID = %q, want %q", mb.updateCalls[0].ID, "abc-123")
	}
	if mb.updateCalls[0].Params.Status == nil || *mb.updateCalls[0].Params.Status != "in_progress" {
		t.Errorf("update Status = %v, want %q", mb.updateCalls[0].Params.Status, "in_progress")
	}
}

func TestIPCServer_UpdateNotFound(t *testing.T) {
	mb := &mockIPCBackend{
		updateErr: backend.ErrNotFound("Update", "issue not found"),
	}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	resp := d.handleIPCUpdate(AgentIPCRequest{
		Operation: ipcOpUpdate,
		AgentName: "falcon",
		IssueID:   "abc-123",
		Args:      json.RawMessage(`{"status": "in_progress"}`),
	})

	if resp.Success {
		t.Fatal("expected failure for not found")
	}
	if resp.Kind != string(backend.KindNotFound) {
		t.Errorf("Kind = %q, want %q", resp.Kind, backend.KindNotFound)
	}
}

func TestIPCServer_CompleteSuccess(t *testing.T) {
	mb := &mockIPCBackend{
		closeResult: &backend.CloseResult{
			Closed:    &backend.IssueData{ID: "abc-123", Title: "test", Status: "closed"},
			Unblocked: []backend.IssueData{{ID: "def-456", Title: "unblocked task", Status: "open"}},
		},
	}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	args, _ := json.Marshal(backend.CloseParams{Reason: "implemented", Session: "sess-123"})

	resp := d.handleIPCComplete(AgentIPCRequest{
		Operation: ipcOpComplete,
		AgentName: "falcon",
		IssueID:   "abc-123",
		Args:      args,
	})

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if len(mb.closeCalls) != 1 {
		t.Fatalf("expected 1 close call, got %d", len(mb.closeCalls))
	}
	if mb.closeCalls[0].ID != "abc-123" {
		t.Errorf("close ID = %q, want %q", mb.closeCalls[0].ID, "abc-123")
	}
	if mb.closeCalls[0].Params.Reason != "implemented" {
		t.Errorf("close Reason = %q, want %q", mb.closeCalls[0].Params.Reason, "implemented")
	}

	// Verify Data contains CloseResult with unblocked
	var result backend.CloseResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		t.Fatalf("unmarshal CloseResult: %v", err)
	}
	if result.Closed.ID != "abc-123" {
		t.Errorf("result.Closed.ID = %q, want %q", result.Closed.ID, "abc-123")
	}
	if len(result.Unblocked) != 1 {
		t.Fatalf("len(result.Unblocked) = %d, want 1", len(result.Unblocked))
	}
	if result.Unblocked[0].ID != "def-456" {
		t.Errorf("result.Unblocked[0].ID = %q, want %q", result.Unblocked[0].ID, "def-456")
	}
}

func TestIPCServer_CompleteNotFound(t *testing.T) {
	mb := &mockIPCBackend{
		closeErr: backend.ErrNotFound("Close", "issue not found"),
	}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	resp := d.handleIPCComplete(AgentIPCRequest{
		Operation: ipcOpComplete,
		AgentName: "falcon",
		IssueID:   "abc-123",
	})

	if resp.Success {
		t.Fatal("expected failure for not found")
	}
	if resp.Kind != string(backend.KindNotFound) {
		t.Errorf("Kind = %q, want %q", resp.Kind, backend.KindNotFound)
	}
}

func TestIPCServer_UnknownOperation(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	if err := d.startIPCServer(socketPath); err != nil {
		t.Fatalf("startIPCServer: %v", err)
	}

	resp := sendIPCRequest(t, socketPath, AgentIPCRequest{
		Operation: "delete",
		AgentName: "falcon",
		IssueID:   "abc-123",
	})

	if resp.Success {
		t.Fatal("expected failure for unknown operation")
	}
	if !strings.Contains(resp.Error, "unknown operation") {
		t.Errorf("Error = %q, want contains 'unknown operation'", resp.Error)
	}
}

func TestIPCServer_MissingAgentName(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	if err := d.startIPCServer(socketPath); err != nil {
		t.Fatalf("startIPCServer: %v", err)
	}

	resp := sendIPCRequest(t, socketPath, AgentIPCRequest{
		Operation: ipcOpClaim,
		AgentName: "",
		IssueID:   "abc-123",
	})

	if resp.Success {
		t.Fatal("expected failure for missing agent_name")
	}
	if resp.Kind != string(backend.KindValidation) {
		t.Errorf("Kind = %q, want %q", resp.Kind, backend.KindValidation)
	}
	if !strings.Contains(resp.Error, "agent_name is required") {
		t.Errorf("Error = %q, want contains 'agent_name is required'", resp.Error)
	}

	// Backend should NOT have been called
	if len(mb.claimCalls) != 0 {
		t.Errorf("expected 0 backend calls, got %d claim calls", len(mb.claimCalls))
	}
}

func TestIPCServer_MissingIssueID(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	if err := d.startIPCServer(socketPath); err != nil {
		t.Fatalf("startIPCServer: %v", err)
	}

	resp := sendIPCRequest(t, socketPath, AgentIPCRequest{
		Operation: ipcOpClaim,
		AgentName: "falcon",
		IssueID:   "",
	})

	if resp.Success {
		t.Fatal("expected failure for missing issue_id")
	}
	if resp.Kind != string(backend.KindValidation) {
		t.Errorf("Kind = %q, want %q", resp.Kind, backend.KindValidation)
	}
	if !strings.Contains(resp.Error, "issue_id is required") {
		t.Errorf("Error = %q, want contains 'issue_id is required'", resp.Error)
	}
}

func TestIPCServer_InvalidJSON(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	if err := d.startIPCServer(socketPath); err != nil {
		t.Fatalf("startIPCServer: %v", err)
	}

	// Send garbage bytes
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	_, _ = fmt.Fprintln(conn, "not json at all{{{")
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatal("no response for invalid JSON")
	}

	var resp AgentIPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Success {
		t.Fatal("expected failure for invalid JSON")
	}
	if !strings.Contains(resp.Error, "invalid request") {
		t.Errorf("Error = %q, want contains 'invalid request'", resp.Error)
	}
}

func TestIPCServer_SocketRoundTrip(t *testing.T) {
	mb := &mockIPCBackend{
		closeResult: &backend.CloseResult{
			Closed: &backend.IssueData{ID: "abc-123", Title: "test", Status: "closed"},
		},
	}
	d := newTestIPCDaemon(mb)
	defer func() {
		close(d.sup.Shutdown)
		if d.ipcListener != nil {
			_ = d.ipcListener.Close()
		}
	}()

	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	if err := d.startIPCServer(socketPath); err != nil {
		t.Fatalf("startIPCServer: %v", err)
	}

	t.Run("claim via socket", func(t *testing.T) {
		resp := sendIPCRequest(t, socketPath, AgentIPCRequest{
			Operation: ipcOpClaim,
			AgentName: "falcon",
			IssueID:   "abc-123",
		})
		if !resp.Success {
			t.Fatalf("expected success, got error: %s (kind: %s)", resp.Error, resp.Kind)
		}
	})

	t.Run("update via socket", func(t *testing.T) {
		resp := sendIPCRequest(t, socketPath, AgentIPCRequest{
			Operation: ipcOpUpdate,
			AgentName: "falcon",
			IssueID:   "abc-123",
			Args:      json.RawMessage(`{"status": "in_progress"}`),
		})
		if !resp.Success {
			t.Fatalf("expected success, got error: %s (kind: %s)", resp.Error, resp.Kind)
		}
	})

	t.Run("complete via socket", func(t *testing.T) {
		resp := sendIPCRequest(t, socketPath, AgentIPCRequest{
			Operation: ipcOpComplete,
			AgentName: "falcon",
			IssueID:   "abc-123",
			Args:      json.RawMessage(`{"reason": "done"}`),
		})
		if !resp.Success {
			t.Fatalf("expected success, got error: %s (kind: %s)", resp.Error, resp.Kind)
		}

		var result backend.CloseResult
		if err := json.Unmarshal(resp.Data, &result); err != nil {
			t.Fatalf("unmarshal CloseResult: %v", err)
		}
		if result.Closed.ID != "abc-123" {
			t.Errorf("result.Closed.ID = %q, want %q", result.Closed.ID, "abc-123")
		}
	})
}

func TestIPCServer_InternalError(t *testing.T) {
	// Non-BackendError should produce Kind="internal"
	mb := &mockIPCBackend{
		claimErr: fmt.Errorf("unexpected database error"),
	}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	resp := d.handleIPCClaim(AgentIPCRequest{
		Operation: ipcOpClaim,
		AgentName: "falcon",
		IssueID:   "abc-123",
	})

	if resp.Success {
		t.Fatal("expected failure for internal error")
	}
	if resp.Kind != string(backend.KindInternal) {
		t.Errorf("Kind = %q, want %q", resp.Kind, backend.KindInternal)
	}
	if !strings.Contains(resp.Error, "unexpected database error") {
		t.Errorf("Error = %q, want contains 'unexpected database error'", resp.Error)
	}
}

func TestIPCServer_InvalidClaimArgs(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	resp := d.handleIPCClaim(AgentIPCRequest{
		Operation: ipcOpClaim,
		AgentName: "falcon",
		IssueID:   "abc-123",
		Args:      json.RawMessage(`{"lock_ttl_seconds": "not a number"}`),
	})

	if resp.Success {
		t.Fatal("expected failure for invalid claim args")
	}
	if resp.Kind != string(backend.KindValidation) {
		t.Errorf("Kind = %q, want %q", resp.Kind, backend.KindValidation)
	}
}

func TestResolveAgentIPCSocketPath(t *testing.T) {
	path := resolveAgentIPCSocketPath("/project", ".loom/daemon.pid")
	want := "/project/.loom/agent-ipc.sock"
	if path != want {
		t.Errorf("resolveAgentIPCSocketPath = %q, want %q", path, want)
	}
}

func TestIPCServer_ClaimPublishesMutation(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)
	configureIPCTestAuth(d, "falcon", "sess-publish", "token-publish")

	bus := notify.New()
	defer bus.Close()
	d.notifyBus = bus
	d.sup.WorkspaceID = "ws-test"

	sub := bus.Subscribe("ws-test", "issue")
	defer sub.Close()

	resp := d.handleIPCClaim(AgentIPCRequest{
		Operation: ipcOpClaim,
		AgentName: "falcon",
		IssueID:   "abc-123",
		SessionID: "sess-publish",
		AuthToken: "token-publish",
	})
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}

	select {
	case event := <-sub.Events():
		mut, ok := event.Payload.(backend.MutationData)
		if !ok {
			t.Fatalf("payload type = %T, want backend.MutationData", event.Payload)
		}
		if mut.Type != backend.MutationStatus {
			t.Errorf("Type = %q, want %q", mut.Type, backend.MutationStatus)
		}
		if mut.IssueID != "abc-123" {
			t.Errorf("IssueID = %q, want %q", mut.IssueID, "abc-123")
		}
		if mut.Actor != "falcon" {
			t.Errorf("Actor = %q, want %q", mut.Actor, "falcon")
		}
		if mut.NewStatus != "in_progress" {
			t.Errorf("NewStatus = %q, want %q", mut.NewStatus, "in_progress")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for mutation event")
	}
}

func TestIPCServer_UpdatePublishesMutation(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)
	configureIPCTestAuth(d, "falcon", "sess-publish", "token-publish")

	bus := notify.New()
	defer bus.Close()
	d.notifyBus = bus
	d.sup.WorkspaceID = "ws-test"

	sub := bus.Subscribe("ws-test", "issue")
	defer sub.Close()

	status := "in_progress"
	args, _ := json.Marshal(backend.UpdateParams{Status: &status})

	resp := d.handleIPCUpdate(AgentIPCRequest{
		Operation: ipcOpUpdate,
		AgentName: "falcon",
		IssueID:   "abc-123",
		Args:      args,
		SessionID: "sess-publish",
		AuthToken: "token-publish",
	})
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}

	select {
	case event := <-sub.Events():
		mut, ok := event.Payload.(backend.MutationData)
		if !ok {
			t.Fatalf("payload type = %T, want backend.MutationData", event.Payload)
		}
		if mut.Type != backend.MutationUpdate {
			t.Errorf("Type = %q, want %q", mut.Type, backend.MutationUpdate)
		}
		if mut.IssueID != "abc-123" {
			t.Errorf("IssueID = %q, want %q", mut.IssueID, "abc-123")
		}
		if mut.Actor != "falcon" {
			t.Errorf("Actor = %q, want %q", mut.Actor, "falcon")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for mutation event")
	}
}

func TestIPCServer_CompletePublishesMutation(t *testing.T) {
	mb := &mockIPCBackend{
		closeResult: &backend.CloseResult{
			Closed: &backend.IssueData{ID: "abc-123", Title: "my task", Parent: "epic-1", Status: "closed"},
		},
	}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)
	configureIPCTestAuth(d, "falcon", "sess-publish", "token-publish")

	bus := notify.New()
	defer bus.Close()
	d.notifyBus = bus
	d.sup.WorkspaceID = "ws-test"

	sub := bus.Subscribe("ws-test", "issue")
	defer sub.Close()

	args, _ := json.Marshal(backend.CloseParams{Reason: "implemented"})

	resp := d.handleIPCComplete(AgentIPCRequest{
		Operation: ipcOpComplete,
		AgentName: "falcon",
		IssueID:   "abc-123",
		Args:      args,
		SessionID: "sess-publish",
		AuthToken: "token-publish",
	})
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}

	select {
	case event := <-sub.Events():
		mut, ok := event.Payload.(backend.MutationData)
		if !ok {
			t.Fatalf("payload type = %T, want backend.MutationData", event.Payload)
		}
		if mut.Type != backend.MutationStatus {
			t.Errorf("Type = %q, want %q", mut.Type, backend.MutationStatus)
		}
		if mut.IssueID != "abc-123" {
			t.Errorf("IssueID = %q, want %q", mut.IssueID, "abc-123")
		}
		if mut.Actor != "falcon" {
			t.Errorf("Actor = %q, want %q", mut.Actor, "falcon")
		}
		if mut.OldStatus != "in_progress" {
			t.Errorf("OldStatus = %q, want %q", mut.OldStatus, "in_progress")
		}
		if mut.NewStatus != "closed" {
			t.Errorf("NewStatus = %q, want %q", mut.NewStatus, "closed")
		}
		if mut.Title != "my task" {
			t.Errorf("Title = %q, want %q", mut.Title, "my task")
		}
		if mut.ParentID != "epic-1" {
			t.Errorf("ParentID = %q, want %q", mut.ParentID, "epic-1")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for mutation event")
	}
}

func TestIPCServer_ClaimError_NoMutation(t *testing.T) {
	mb := &mockIPCBackend{
		claimErr: fmt.Errorf("database unavailable"),
	}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)

	bus := notify.New()
	defer bus.Close()
	d.notifyBus = bus
	d.sup.WorkspaceID = "ws-test"

	sub := bus.Subscribe("ws-test", "issue")
	defer sub.Close()

	resp := d.handleIPCClaim(AgentIPCRequest{
		Operation: ipcOpClaim,
		AgentName: "falcon",
		IssueID:   "abc-123",
	})
	if resp.Success {
		t.Fatal("expected failure, got success")
	}

	select {
	case event := <-sub.Events():
		t.Fatalf("expected no mutation event, got: %+v", event)
	case <-time.After(50 * time.Millisecond):
		// Good — no mutation published on error
	}
}

// A heartbeat authenticates against the active supervisor generation without
// writing Interaction-owned AgentSession/AgentLease state or invoking the
// issue backend.
func TestIPCServer_HeartbeatAuthenticatesWithoutInteractionWrite(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)
	st := memstore.New()
	d.store = st
	configureIPCTestAuth(d, "falcon", "sess-hb", "token-hb")

	resp := d.handleIPCHeartbeat(AgentIPCRequest{
		Operation: ipcOpHeartbeat,
		AgentName: "falcon",
		SessionID: "sess-hb",
		AuthToken: "token-hb",
	})
	if !resp.Success {
		t.Fatalf("heartbeat with valid credential failed: %s (kind=%s)", resp.Error, resp.Kind)
	}
	if len(mb.claimCalls) != 0 || len(mb.updateCalls) != 0 || len(mb.closeCalls) != 0 {
		t.Errorf("heartbeat must not call the issue backend, got claims=%d updates=%d closes=%d",
			len(mb.claimCalls), len(mb.updateCalls), len(mb.closeCalls))
	}

	if leases, err := st.AgentLeases().List(t.Context(), "WS", store.AgentLeaseFilter{}); err != nil || len(leases) != 0 {
		t.Fatalf("heartbeat wrote Interaction AgentLease state: leases=%v err=%v", leases, err)
	}
}

// TestIPCServer_Heartbeat_RejectsBadToken protects against an attacker (or
// stale session) impersonating an active supervised process.
func TestIPCServer_Heartbeat_RejectsBadToken(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)
	configureIPCTestAuth(d, "falcon", "sess-hb2", "token-hb2")

	resp := d.handleIPCHeartbeat(AgentIPCRequest{
		Operation: ipcOpHeartbeat,
		AgentName: "falcon",
		SessionID: "sess-hb2",
		AuthToken: "wrong-token",
	})
	if resp.Success {
		t.Fatal("heartbeat accepted a bad token")
	}
}

// TestIPCServer_Heartbeat_NoWorkspaceIdentity covers the local notification
// profile, where no control-plane workspace is configured and IPC credential
// fencing is therefore disabled.
func TestIPCServer_Heartbeat_NoWorkspaceIdentity(t *testing.T) {
	mb := &mockIPCBackend{}
	d := newTestIPCDaemon(mb)
	defer close(d.sup.Shutdown)
	// d.sup.WorkspaceID is deliberately left empty.

	resp := d.handleIPCHeartbeat(AgentIPCRequest{
		Operation: ipcOpHeartbeat,
		AgentName: "falcon",
	})
	if !resp.Success {
		t.Fatalf("heartbeat without workspace identity failed: %s", resp.Error)
	}
}
