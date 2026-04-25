package agentstatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// envelope is the success-shape envelope returned by HandleAgentStatus.
type envelope struct {
	Success bool                `json:"success"`
	Data    AgentStatusResponse `json:"data"`
}

// errEnvelope is the error-shape envelope returned by HandleAgentStatus.
type errEnvelope struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
}

// newTestRequest builds a request with the workspace id injected via
// middleware.WithWorkspace, mirroring how the production middleware sets it.
func newTestRequest(wsID string) *http.Request {
	r := httptest.NewRequest("GET", "/api/workspaces/"+wsID+"/agents/status", nil)
	if wsID != "" {
		r = r.WithContext(middleware.WithWorkspace(r.Context(), wsID))
	}
	return r
}

// decodeOK decodes a 200 JSON envelope.
func decodeOK(t *testing.T, rec *httptest.ResponseRecorder) envelope {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v body=%s", err, rec.Body.String())
	}
	if !env.Success {
		t.Fatalf("envelope success=false: %s", rec.Body.String())
	}
	return env
}

// decodeErr decodes an error JSON envelope.
func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) errEnvelope {
	t.Helper()
	var env errEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal err envelope: %v body=%s", err, rec.Body.String())
	}
	return env
}

// makeStatePath creates an empty daemon-agents.json file (just the path) so the
// handler can derive the agent-ipc.sock location from filepath.Dir.
func makeStatePath(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "daemon-agents.json")
	if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	return p
}

// makeIPCSocket creates a placeholder agent-ipc.sock file in dir so statExists
// returns true. We don't need a real listening socket — only file presence.
func makeIPCSocket(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "agent-ipc.sock"), []byte{}, 0o644); err != nil {
		t.Fatalf("write ipc sock: %v", err)
	}
}

func TestHandleAgentStatus_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	wtA := filepath.Join(tmp, "wt-a")
	wtB := filepath.Join(tmp, "wt-b")
	if err := os.MkdirAll(wtA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wtB, 0o755); err != nil {
		t.Fatal(err)
	}

	statePath := makeStatePath(t, tmp)
	makeIPCSocket(t, tmp)

	startedAt := time.Now().Add(-time.Hour).UTC()
	supFn := func(wsID string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{
			PID:       1234,
			StartedAt: startedAt,
			Agents: []webui.DaemonAgentEntry{
				{
					Worktree:     "alpha",
					Role:         "implement",
					Repo:         "frontend",
					PID:          11,
					Status:       "running",
					WorktreePath: wtA,
				},
				{
					Worktree:     "beta",
					Role:         "plan",
					Repo:         "backend",
					PID:          22,
					Status:       "running",
					WorktreePath: wtB,
				},
			},
		}, nil
	}
	resolverFn := func(wsID string) (*webui.WorkspaceDaemonPaths, error) {
		return &webui.WorkspaceDaemonPaths{StatePath: statePath, WorkDir: tmp}, nil
	}
	wsCfgFn := func(string) (*ops.WorkspaceData, error) {
		return &ops.WorkspaceData{
			ID:   "ws-1",
			Name: "myworkspace",
			Repos: []ops.WorkspaceRepo{
				{Name: "frontend", DefaultBranch: "main"},
				{Name: "backend", DefaultBranch: "develop"},
			},
			Agents: []ops.WorkspaceAgentInfo{
				{Name: "alpha", CrossRepo: true},
			},
		}, nil
	}
	collectFn := func(in webui.AgentStatusCollectInput) *webui.AgentGitStatus {
		return &webui.AgentGitStatus{
			Status:  "ready",
			Branch:  "feature/" + in.AgentName,
			Ahead:   1,
			Behind:  2,
			Changes: 3,
			TaskID:  "task-" + in.AgentName,
		}
	}

	rec := httptest.NewRecorder()
	HandleAgentStatus(supFn, resolverFn, wsCfgFn, collectFn).ServeHTTP(rec, newTestRequest("ws-1"))
	env := decodeOK(t, rec)

	if env.Data.WorkspaceName != "myworkspace" {
		t.Errorf("workspace_name = %q, want myworkspace", env.Data.WorkspaceName)
	}
	if !env.Data.IPCSocketActive {
		t.Errorf("ipc_socket_active = false, want true")
	}
	if env.Data.DaemonPID != 1234 {
		t.Errorf("daemon_pid = %d, want 1234", env.Data.DaemonPID)
	}
	if !env.Data.DaemonStartedAt.Equal(startedAt) {
		t.Errorf("daemon_started_at = %v, want %v", env.Data.DaemonStartedAt, startedAt)
	}
	if len(env.Data.Agents) != 2 {
		t.Fatalf("agents len = %d, want 2: %+v", len(env.Data.Agents), env.Data.Agents)
	}

	byName := map[string]AgentStatusEntry{}
	for _, a := range env.Data.Agents {
		byName[a.Worktree] = a
	}

	a := byName["alpha"]
	if a.SupervisorStatus != "running" || a.Status != "ready" {
		t.Errorf("alpha statuses sup=%q status=%q", a.SupervisorStatus, a.Status)
	}
	if a.Branch != "feature/alpha" || a.Ahead != 1 || a.Behind != 2 || a.Changes != 3 {
		t.Errorf("alpha git fields wrong: %+v", a)
	}
	if a.TaskID != "task-alpha" {
		t.Errorf("alpha task_id = %q", a.TaskID)
	}
	if a.Workspace != "myworkspace" {
		t.Errorf("alpha workspace = %q", a.Workspace)
	}
	if !a.CrossRepo {
		t.Errorf("alpha cross_repo = false, want true")
	}
	if a.PID != 11 {
		t.Errorf("alpha pid = %d", a.PID)
	}
	if a.Path != wtA || a.WorktreePath != wtA {
		t.Errorf("alpha paths path=%q wt=%q want %q", a.Path, a.WorktreePath, wtA)
	}
	if a.Role != "implement" {
		t.Errorf("alpha role = %q", a.Role)
	}
	if a.Repo != "frontend" {
		t.Errorf("alpha repo = %q", a.Repo)
	}

	b := byName["beta"]
	if b.CrossRepo {
		t.Errorf("beta cross_repo = true, want false")
	}
	if b.Path != wtB {
		t.Errorf("beta path = %q want %q", b.Path, wtB)
	}
	if b.Path != b.WorktreePath {
		t.Errorf("beta path != worktree_path: %q vs %q", b.Path, b.WorktreePath)
	}
}

func TestHandleAgentStatus_MissingWorkspaceContext(t *testing.T) {
	h := HandleAgentStatus(
		func(string) (*webui.DaemonSupervisorData, error) { return nil, nil },
		func(string) (*webui.WorkspaceDaemonPaths, error) { return nil, nil },
		nil,
		func(webui.AgentStatusCollectInput) *webui.AgentGitStatus { return &webui.AgentGitStatus{} },
	)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/x", nil) // no WithWorkspace
	r = r.WithContext(context.Background())
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if env := decodeErr(t, rec); env.Code != "bad_request" {
		t.Errorf("code = %q want bad_request", env.Code)
	}
}

func TestHandleAgentStatus_ResolverError(t *testing.T) {
	h := HandleAgentStatus(
		func(string) (*webui.DaemonSupervisorData, error) { return nil, nil },
		func(string) (*webui.WorkspaceDaemonPaths, error) { return nil, errors.New("no workspace") },
		nil,
		func(webui.AgentStatusCollectInput) *webui.AgentGitStatus { return &webui.AgentGitStatus{} },
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newTestRequest("ws-1"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if env := decodeErr(t, rec); env.Code != "daemon_unavailable" {
		t.Errorf("code = %q want daemon_unavailable", env.Code)
	}
}

func TestHandleAgentStatus_DaemonNotRunning(t *testing.T) {
	h := HandleAgentStatus(
		func(string) (*webui.DaemonSupervisorData, error) { return nil, os.ErrNotExist },
		func(string) (*webui.WorkspaceDaemonPaths, error) {
			return &webui.WorkspaceDaemonPaths{}, nil
		},
		nil,
		func(webui.AgentStatusCollectInput) *webui.AgentGitStatus { return &webui.AgentGitStatus{} },
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newTestRequest("ws-1"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if env := decodeErr(t, rec); env.Code != "daemon_not_running" {
		t.Errorf("code = %q want daemon_not_running", env.Code)
	}
}

func TestHandleAgentStatus_DaemonGenericError(t *testing.T) {
	h := HandleAgentStatus(
		func(string) (*webui.DaemonSupervisorData, error) { return nil, fmt.Errorf("corrupt") },
		func(string) (*webui.WorkspaceDaemonPaths, error) {
			return &webui.WorkspaceDaemonPaths{}, nil
		},
		nil,
		func(webui.AgentStatusCollectInput) *webui.AgentGitStatus { return &webui.AgentGitStatus{} },
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newTestRequest("ws-1"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if env := decodeErr(t, rec); env.Code != "internal_error" {
		t.Errorf("code = %q want internal_error", env.Code)
	}
}

func TestHandleAgentStatus_CollectErrorPerAgent(t *testing.T) {
	tmp := t.TempDir()
	wtA := filepath.Join(tmp, "a")
	wtB := filepath.Join(tmp, "b")
	if err := os.MkdirAll(wtA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wtB, 0o755); err != nil {
		t.Fatal(err)
	}

	supFn := func(string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{
			Agents: []webui.DaemonAgentEntry{
				{Worktree: "alpha", Status: "running", WorktreePath: wtA},
				{Worktree: "beta", Status: "starting", WorktreePath: wtB},
			},
		}, nil
	}
	resolverFn := func(string) (*webui.WorkspaceDaemonPaths, error) {
		return &webui.WorkspaceDaemonPaths{}, nil
	}
	collectFn := func(in webui.AgentStatusCollectInput) *webui.AgentGitStatus {
		if in.AgentName == "alpha" {
			return &webui.AgentGitStatus{Err: errors.New("collect failed")}
		}
		return &webui.AgentGitStatus{
			Status: "ready", Branch: "main", Ahead: 0, Behind: 1, Changes: 2, TaskID: "t-2",
		}
	}

	rec := httptest.NewRecorder()
	HandleAgentStatus(supFn, resolverFn, nil, collectFn).ServeHTTP(rec, newTestRequest("ws-1"))
	env := decodeOK(t, rec)

	byName := map[string]AgentStatusEntry{}
	for _, a := range env.Data.Agents {
		byName[a.Worktree] = a
	}

	a := byName["alpha"]
	if a.Status != "running" {
		t.Errorf("alpha status = %q want supervisor_status running", a.Status)
	}
	if a.Branch != "" || a.Ahead != 0 || a.Behind != 0 || a.Changes != 0 || a.TaskID != "" {
		t.Errorf("alpha should be zero-valued git fields: %+v", a)
	}
	if a.Error != "collect failed" {
		t.Errorf("alpha error = %q", a.Error)
	}

	b := byName["beta"]
	if b.Error != "" {
		t.Errorf("beta should not have error: %q", b.Error)
	}
	if b.Status != "ready" || b.Behind != 1 || b.Changes != 2 || b.TaskID != "t-2" {
		t.Errorf("beta unexpected: %+v", b)
	}
}

func TestHandleAgentStatus_EmptyAgentsList(t *testing.T) {
	supFn := func(string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{Agents: []webui.DaemonAgentEntry{}}, nil
	}
	resolverFn := func(string) (*webui.WorkspaceDaemonPaths, error) {
		return &webui.WorkspaceDaemonPaths{}, nil
	}
	collectFn := func(webui.AgentStatusCollectInput) *webui.AgentGitStatus { return &webui.AgentGitStatus{} }

	rec := httptest.NewRecorder()
	HandleAgentStatus(supFn, resolverFn, nil, collectFn).ServeHTTP(rec, newTestRequest("ws-1"))
	env := decodeOK(t, rec)

	if len(env.Data.Agents) != 0 {
		t.Errorf("agents len = %d want 0", len(env.Data.Agents))
	}
	// Verify wire JSON renders []agents and not null.
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	data := raw["data"].(map[string]any)
	if a, ok := data["agents"].([]any); !ok || a == nil {
		t.Errorf("agents must be JSON array, got %T", data["agents"])
	}
}

func TestHandleAgentStatus_EmptyWorktreePathSkipped(t *testing.T) {
	tmp := t.TempDir()
	wtB := filepath.Join(tmp, "b")
	if err := os.MkdirAll(wtB, 0o755); err != nil {
		t.Fatal(err)
	}
	supFn := func(string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{
			Agents: []webui.DaemonAgentEntry{
				{Worktree: "no-path", Status: "running", WorktreePath: ""},
				{Worktree: "ok", Status: "running", WorktreePath: wtB},
			},
		}, nil
	}
	resolverFn := func(string) (*webui.WorkspaceDaemonPaths, error) {
		return &webui.WorkspaceDaemonPaths{}, nil
	}
	collectFn := func(webui.AgentStatusCollectInput) *webui.AgentGitStatus {
		return &webui.AgentGitStatus{Status: "ready"}
	}

	rec := httptest.NewRecorder()
	HandleAgentStatus(supFn, resolverFn, nil, collectFn).ServeHTTP(rec, newTestRequest("ws-1"))
	env := decodeOK(t, rec)

	if len(env.Data.Agents) != 1 {
		t.Fatalf("expected 1 agent (empty-path skipped), got %d", len(env.Data.Agents))
	}
	if env.Data.Agents[0].Worktree != "ok" {
		t.Errorf("expected ok agent, got %q", env.Data.Agents[0].Worktree)
	}
}

func TestHandleAgentStatus_YieldFilePresent(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	yReq := time.Now().Add(-5 * time.Minute).UTC().Truncate(time.Second)
	yieldData, _ := json.Marshal(map[string]any{
		"reason":       "deadline",
		"requested_at": yReq.Format(time.RFC3339),
		"requested_by": "scheduler",
	})
	if err := os.WriteFile(filepath.Join(wt, ".agent.yield"), yieldData, 0o644); err != nil {
		t.Fatal(err)
	}

	supFn := func(string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{
			Agents: []webui.DaemonAgentEntry{{Worktree: "alpha", Status: "running", WorktreePath: wt}},
		}, nil
	}
	resolverFn := func(string) (*webui.WorkspaceDaemonPaths, error) {
		return &webui.WorkspaceDaemonPaths{}, nil
	}
	collectFn := func(webui.AgentStatusCollectInput) *webui.AgentGitStatus {
		return &webui.AgentGitStatus{Status: "ready"}
	}

	rec := httptest.NewRecorder()
	HandleAgentStatus(supFn, resolverFn, nil, collectFn).ServeHTTP(rec, newTestRequest("ws-1"))
	env := decodeOK(t, rec)

	if len(env.Data.Agents) != 1 {
		t.Fatalf("agents len = %d", len(env.Data.Agents))
	}
	a := env.Data.Agents[0]
	if !a.YieldRequested {
		t.Errorf("yield_requested = false")
	}
	if a.YieldReason != "deadline" {
		t.Errorf("yield_reason = %q", a.YieldReason)
	}
	if !a.YieldRequestedAt.Equal(yReq) {
		t.Errorf("yield_requested_at = %v want %v", a.YieldRequestedAt, yReq)
	}
}

func TestHandleAgentStatus_YieldFileMalformed(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".agent.yield"), []byte("not json {{{"), 0o644); err != nil {
		t.Fatal(err)
	}

	supFn := func(string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{
			Agents: []webui.DaemonAgentEntry{{Worktree: "alpha", Status: "running", WorktreePath: wt}},
		}, nil
	}
	resolverFn := func(string) (*webui.WorkspaceDaemonPaths, error) {
		return &webui.WorkspaceDaemonPaths{}, nil
	}
	collectFn := func(webui.AgentStatusCollectInput) *webui.AgentGitStatus {
		return &webui.AgentGitStatus{Status: "ready"}
	}

	rec := httptest.NewRecorder()
	HandleAgentStatus(supFn, resolverFn, nil, collectFn).ServeHTTP(rec, newTestRequest("ws-1"))
	env := decodeOK(t, rec)
	if len(env.Data.Agents) != 1 {
		t.Fatalf("agents len = %d", len(env.Data.Agents))
	}
	a := env.Data.Agents[0]
	if a.YieldRequested {
		t.Errorf("yield_requested = true on malformed file")
	}
	if a.Error != "" {
		t.Errorf("malformed yield should not surface error, got %q", a.Error)
	}
}

func TestHandleAgentStatus_IPCSocketAbsent(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := makeStatePath(t, tmp) // no IPC socket created

	supFn := func(string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{
			Agents: []webui.DaemonAgentEntry{{Worktree: "alpha", Status: "running", WorktreePath: wt}},
		}, nil
	}
	resolverFn := func(string) (*webui.WorkspaceDaemonPaths, error) {
		return &webui.WorkspaceDaemonPaths{StatePath: statePath}, nil
	}
	collectFn := func(webui.AgentStatusCollectInput) *webui.AgentGitStatus {
		return &webui.AgentGitStatus{Status: "ready"}
	}

	rec := httptest.NewRecorder()
	HandleAgentStatus(supFn, resolverFn, nil, collectFn).ServeHTTP(rec, newTestRequest("ws-1"))
	env := decodeOK(t, rec)
	if env.Data.IPCSocketActive {
		t.Errorf("ipc_socket_active = true when socket file absent")
	}
}

func TestHandleAgentStatus_NilWsConfigFn(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}

	supFn := func(string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{
			Agents: []webui.DaemonAgentEntry{{Worktree: "alpha", Repo: "rA", Status: "running", WorktreePath: wt}},
		}, nil
	}
	resolverFn := func(string) (*webui.WorkspaceDaemonPaths, error) {
		return &webui.WorkspaceDaemonPaths{}, nil
	}
	var seenDefault string
	collectFn := func(in webui.AgentStatusCollectInput) *webui.AgentGitStatus {
		seenDefault = in.DefaultBranch
		return &webui.AgentGitStatus{Status: "ready"}
	}

	rec := httptest.NewRecorder()
	HandleAgentStatus(supFn, resolverFn, nil, collectFn).ServeHTTP(rec, newTestRequest("ws-1"))
	env := decodeOK(t, rec)
	if len(env.Data.Agents) != 1 {
		t.Fatalf("len = %d", len(env.Data.Agents))
	}
	a := env.Data.Agents[0]
	if a.Workspace != "" {
		t.Errorf("workspace should be empty when wsCfgFn nil, got %q", a.Workspace)
	}
	if a.CrossRepo {
		t.Errorf("cross_repo should be false when wsCfgFn nil")
	}
	if seenDefault != "main" {
		t.Errorf("default branch should fall back to main, got %q", seenDefault)
	}
}

func TestHandleAgentStatus_WsConfigFnError(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}

	supFn := func(string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{
			Agents: []webui.DaemonAgentEntry{{Worktree: "alpha", Repo: "rA", Status: "running", WorktreePath: wt}},
		}, nil
	}
	resolverFn := func(string) (*webui.WorkspaceDaemonPaths, error) {
		return &webui.WorkspaceDaemonPaths{}, nil
	}
	wsCfgFn := func(string) (*ops.WorkspaceData, error) { return nil, errors.New("config lookup failed") }

	var seenDefault string
	collectFn := func(in webui.AgentStatusCollectInput) *webui.AgentGitStatus {
		seenDefault = in.DefaultBranch
		return &webui.AgentGitStatus{Status: "ready"}
	}

	rec := httptest.NewRecorder()
	HandleAgentStatus(supFn, resolverFn, wsCfgFn, collectFn).ServeHTTP(rec, newTestRequest("ws-1"))
	env := decodeOK(t, rec)
	a := env.Data.Agents[0]
	if a.Workspace != "" {
		t.Errorf("workspace should be empty on cfg err, got %q", a.Workspace)
	}
	if a.CrossRepo {
		t.Errorf("cross_repo should be false on cfg err")
	}
	if seenDefault != "main" {
		t.Errorf("default branch should fall back to main, got %q", seenDefault)
	}
}

func TestHandleAgentStatus_PerRepoDefaultBranch(t *testing.T) {
	tmp := t.TempDir()
	wtA := filepath.Join(tmp, "a")
	wtB := filepath.Join(tmp, "b")
	if err := os.MkdirAll(wtA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wtB, 0o755); err != nil {
		t.Fatal(err)
	}

	supFn := func(string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{
			Agents: []webui.DaemonAgentEntry{
				{Worktree: "alpha", Repo: "frontend", Status: "running", WorktreePath: wtA},
				{Worktree: "beta", Repo: "backend", Status: "running", WorktreePath: wtB},
			},
		}, nil
	}
	resolverFn := func(string) (*webui.WorkspaceDaemonPaths, error) {
		return &webui.WorkspaceDaemonPaths{}, nil
	}
	wsCfgFn := func(string) (*ops.WorkspaceData, error) {
		return &ops.WorkspaceData{
			Name: "ws",
			Repos: []ops.WorkspaceRepo{
				{Name: "frontend", DefaultBranch: "trunk"},
				{Name: "backend", DefaultBranch: "release"},
			},
		}, nil
	}
	branchByAgent := map[string]string{}
	collectFn := func(in webui.AgentStatusCollectInput) *webui.AgentGitStatus {
		branchByAgent[in.AgentName] = in.DefaultBranch
		return &webui.AgentGitStatus{Status: "ready"}
	}

	rec := httptest.NewRecorder()
	HandleAgentStatus(supFn, resolverFn, wsCfgFn, collectFn).ServeHTTP(rec, newTestRequest("ws-1"))
	_ = decodeOK(t, rec)

	if branchByAgent["alpha"] != "trunk" {
		t.Errorf("alpha default branch = %q want trunk", branchByAgent["alpha"])
	}
	if branchByAgent["beta"] != "release" {
		t.Errorf("beta default branch = %q want release", branchByAgent["beta"])
	}
}

func TestHandleAgentStatus_UnknownRepoFallsBack(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}

	supFn := func(string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{
			Agents: []webui.DaemonAgentEntry{
				{Worktree: "alpha", Repo: "ghost", Status: "running", WorktreePath: wt},
			},
		}, nil
	}
	resolverFn := func(string) (*webui.WorkspaceDaemonPaths, error) {
		return &webui.WorkspaceDaemonPaths{}, nil
	}
	wsCfgFn := func(string) (*ops.WorkspaceData, error) {
		return &ops.WorkspaceData{
			Name: "ws",
			Repos: []ops.WorkspaceRepo{
				{Name: "frontend", DefaultBranch: "trunk"},
			},
		}, nil
	}
	var seenDefault string
	collectFn := func(in webui.AgentStatusCollectInput) *webui.AgentGitStatus {
		seenDefault = in.DefaultBranch
		return &webui.AgentGitStatus{Status: "ready"}
	}

	rec := httptest.NewRecorder()
	HandleAgentStatus(supFn, resolverFn, wsCfgFn, collectFn).ServeHTTP(rec, newTestRequest("ws-1"))
	env := decodeOK(t, rec)
	a := env.Data.Agents[0]
	if a.Error == "" || !contains(a.Error, "unknown repo: ghost") {
		t.Errorf("expected error containing 'unknown repo: ghost', got %q", a.Error)
	}
	if seenDefault != "trunk" {
		t.Errorf("default branch fallback = %q want trunk (Repos[0])", seenDefault)
	}
}

func TestHandleAgentStatus_CrossRepoFlagMatching(t *testing.T) {
	tmp := t.TempDir()
	wtA := filepath.Join(tmp, "a")
	wtB := filepath.Join(tmp, "b")
	if err := os.MkdirAll(wtA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wtB, 0o755); err != nil {
		t.Fatal(err)
	}

	supFn := func(string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{
			Agents: []webui.DaemonAgentEntry{
				{Worktree: "alpha", Status: "running", WorktreePath: wtA},
				{Worktree: "beta", Status: "running", WorktreePath: wtB},
			},
		}, nil
	}
	resolverFn := func(string) (*webui.WorkspaceDaemonPaths, error) {
		return &webui.WorkspaceDaemonPaths{}, nil
	}
	wsCfgFn := func(string) (*ops.WorkspaceData, error) {
		return &ops.WorkspaceData{
			Name: "ws",
			Agents: []ops.WorkspaceAgentInfo{
				{Name: "alpha", CrossRepo: true},
				{Name: "beta", CrossRepo: false},
			},
		}, nil
	}
	collectFn := func(webui.AgentStatusCollectInput) *webui.AgentGitStatus {
		return &webui.AgentGitStatus{Status: "ready"}
	}

	rec := httptest.NewRecorder()
	HandleAgentStatus(supFn, resolverFn, wsCfgFn, collectFn).ServeHTTP(rec, newTestRequest("ws-1"))
	env := decodeOK(t, rec)

	by := map[string]AgentStatusEntry{}
	for _, a := range env.Data.Agents {
		by[a.Worktree] = a
	}
	if !by["alpha"].CrossRepo {
		t.Errorf("alpha cross_repo should be true")
	}
	if by["beta"].CrossRepo {
		t.Errorf("beta cross_repo should be false")
	}
}

func TestHandleAgentStatus_PathEqualsWorktreePath(t *testing.T) {
	tmp := t.TempDir()
	wtA := filepath.Join(tmp, "a")
	wtB := filepath.Join(tmp, "b")
	if err := os.MkdirAll(wtA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wtB, 0o755); err != nil {
		t.Fatal(err)
	}

	supFn := func(string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{
			Agents: []webui.DaemonAgentEntry{
				{Worktree: "alpha", Status: "running", WorktreePath: wtA},
				{Worktree: "beta", Status: "running", WorktreePath: wtB},
			},
		}, nil
	}
	resolverFn := func(string) (*webui.WorkspaceDaemonPaths, error) {
		return &webui.WorkspaceDaemonPaths{}, nil
	}
	collectFn := func(webui.AgentStatusCollectInput) *webui.AgentGitStatus {
		return &webui.AgentGitStatus{Status: "ready"}
	}

	rec := httptest.NewRecorder()
	HandleAgentStatus(supFn, resolverFn, nil, collectFn).ServeHTTP(rec, newTestRequest("ws-1"))
	env := decodeOK(t, rec)
	for _, a := range env.Data.Agents {
		if a.Path != a.WorktreePath {
			t.Errorf("agent %s: path %q != worktree_path %q", a.Worktree, a.Path, a.WorktreePath)
		}
	}
}

// TestHandleAgentStatus_UnknownRepoAndCollectError covers the combined branch
// where collectFn fails AND the agent's Repo is not in the workspace config.
// Both error messages must appear in the per-entry error field, separated by
// "; ", with the collect error first (handler concatenation order).
func TestHandleAgentStatus_UnknownRepoAndCollectError(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}

	supFn := func(string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{
			Agents: []webui.DaemonAgentEntry{
				{Worktree: "alpha", Repo: "ghost", Status: "running", WorktreePath: wt},
			},
		}, nil
	}
	resolverFn := func(string) (*webui.WorkspaceDaemonPaths, error) {
		return &webui.WorkspaceDaemonPaths{}, nil
	}
	wsCfgFn := func(string) (*ops.WorkspaceData, error) {
		return &ops.WorkspaceData{
			Name:  "ws",
			Repos: []ops.WorkspaceRepo{{Name: "frontend", DefaultBranch: "trunk"}},
		}, nil
	}
	collectFn := func(webui.AgentStatusCollectInput) *webui.AgentGitStatus {
		return &webui.AgentGitStatus{Err: errors.New("collect failed")}
	}

	rec := httptest.NewRecorder()
	HandleAgentStatus(supFn, resolverFn, wsCfgFn, collectFn).ServeHTTP(rec, newTestRequest("ws-1"))
	env := decodeOK(t, rec)
	a := env.Data.Agents[0]
	if !contains(a.Error, "collect failed") {
		t.Errorf("expected error to contain collect failure, got %q", a.Error)
	}
	if !contains(a.Error, "unknown repo: ghost") {
		t.Errorf("expected error to contain unknown-repo message, got %q", a.Error)
	}
	if !contains(a.Error, "; ") {
		t.Errorf("expected '; ' separator between concatenated errors, got %q", a.Error)
	}
	if a.Status != "running" {
		t.Errorf("status should fall back to supervisor_status on collect error; got %q", a.Status)
	}
}

// TestHandleAgentStatus_NilCollectResult guards the defensive nil-check at the
// collectFn call site. A nil return falls through the same path as a normal
// zero-value result — no per-agent error, status copied from supervisor (since
// the synthetic zero result also has empty Status).
func TestHandleAgentStatus_NilCollectResult(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}

	supFn := func(string) (*webui.DaemonSupervisorData, error) {
		return &webui.DaemonSupervisorData{
			Agents: []webui.DaemonAgentEntry{
				{Worktree: "alpha", Status: "running", WorktreePath: wt},
			},
		}, nil
	}
	resolverFn := func(string) (*webui.WorkspaceDaemonPaths, error) {
		return &webui.WorkspaceDaemonPaths{}, nil
	}
	collectFn := func(webui.AgentStatusCollectInput) *webui.AgentGitStatus { return nil }

	rec := httptest.NewRecorder()
	HandleAgentStatus(supFn, resolverFn, nil, collectFn).ServeHTTP(rec, newTestRequest("ws-1"))
	env := decodeOK(t, rec)
	if len(env.Data.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(env.Data.Agents))
	}
	a := env.Data.Agents[0]
	if a.Error != "" {
		t.Errorf("nil collect result should not produce a per-entry error, got %q", a.Error)
	}
	if a.SupervisorStatus != "running" {
		t.Errorf("supervisor_status should still be set, got %q", a.SupervisorStatus)
	}
}

// contains is a tiny strings.Contains shim to avoid importing strings just for one site.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (substr == "" || indexOf(s, substr) >= 0)
}

func indexOf(s, sub string) int {
outer:
	for i := 0; i+len(sub) <= len(s); i++ {
		for j := 0; j < len(sub); j++ {
			if s[i+j] != sub[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}
