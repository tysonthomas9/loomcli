package epics

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

func newMux(st store.Store, hub *realtime.Hub, ib backend.IssueBackend) *http.ServeMux {
	mux := http.NewServeMux()
	NewModule(st, hub).
		WithIssueBackendFn(func(context.Context) backend.IssueBackend { return ib }).
		WithBackgroundRuns(false).
		Register(mux)
	return mux
}

func postRun(t *testing.T, mux *http.ServeMux, ws, epicID string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws+"/epics/"+epicID+"/run", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Actor", "tester")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	var got map[string]any
	if rr.Body.Len() > 0 {
		_ = json.Unmarshal(rr.Body.Bytes(), &got)
	}
	return rr, got
}

func TestRunEpicBindsLead(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createRunnableWorkspace(t, st, "WS")
	createLead(t, st, "WS", "nova", "", "session-1")

	rr, body := postRun(t, newMux(st, nil, newRunBackend("EPIC-1", nil)), "WS", "EPIC-1", map[string]string{"lead": "nova"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rr.Code, body)
	}
	if body["state"] != "assigned" || body["delivery_state"] != "pending" || body["run_state"] != "drained" || body["orchestrator_session_id"] != "session-1" {
		t.Fatalf("body = %#v, want assigned pending/drained session-1", body)
	}
	got, err := st.Agents().Get(ctx, "WS", "nova")
	if err != nil {
		t.Fatalf("get lead: %v", err)
	}
	if got.Parent != "EPIC-1" {
		t.Fatalf("lead parent = %q, want EPIC-1", got.Parent)
	}
}

func TestRunEpicBroadcastsAgentRefresh(t *testing.T) {
	st := newTestStore(t)
	createRunnableWorkspace(t, st, "WS")
	createLead(t, st, "WS", "nova", "", "")
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	client := realtime.NewClient(1, 8, "", nil, "WS")
	hub.RegisterClient(client)
	waitForHubClient(t, hub)

	rr, body := postRun(t, newMux(st, hub, newRunBackend("EPIC-1", nil)), "WS", "EPIC-1", map[string]string{"lead": "nova"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rr.Code, body)
	}
	expectAgentRefresh(t, client.Send(), "WS", "nova")
}

func TestRunEpicRejectsLeadAlreadyOnDifferentEpic(t *testing.T) {
	st := newTestStore(t)
	createRunnableWorkspace(t, st, "WS")
	createLead(t, st, "WS", "nova", "EPIC-X", "")

	rr, body := postRun(t, newMux(st, nil, newRunBackend("EPIC-1", nil)), "WS", "EPIC-1", map[string]string{"lead": "nova"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%v", rr.Code, body)
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "already running epic EPIC-X") {
		t.Fatalf("error = %q, want it to mention the existing epic", msg)
	}
}

func TestRunEpicRejectsEpicAlreadyClaimedByOtherLead(t *testing.T) {
	st := newTestStore(t)
	createRunnableWorkspace(t, st, "WS")
	createLead(t, st, "WS", "atlas", "EPIC-1", "")
	createLead(t, st, "WS", "nova", "", "")

	rr, body := postRun(t, newMux(st, nil, newRunBackend("EPIC-1", nil)), "WS", "EPIC-1", map[string]string{"lead": "nova"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%v", rr.Code, body)
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "already claimed by lead atlas") {
		t.Fatalf("error = %q, want claim conflict mentioning atlas", msg)
	}
}

func TestRunEpicIdempotentWhenAlreadyBound(t *testing.T) {
	st := newTestStore(t)
	createRunnableWorkspace(t, st, "WS")
	createLead(t, st, "WS", "nova", "EPIC-1", "")

	rr, body := postRun(t, newMux(st, nil, newRunBackend("EPIC-1", nil)), "WS", "EPIC-1", map[string]string{"lead": "nova"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rr.Code, body)
	}
	if body["state"] != "resumed" || body["delivery_state"] != "pending" || body["run_state"] != "drained" {
		t.Fatalf("body = %#v, want resumed pending/drained", body)
	}
}

func TestRunEpicRejectsNonLeadAgent(t *testing.T) {
	st := newTestStore(t)
	createRunnableWorkspace(t, st, "WS")
	if _, err := st.Agents().Create(context.Background(), store.AgentCreate{
		WorkspaceKey: "WS",
		Name:         "worker",
		RoleName:     "task",
	}); err != nil {
		t.Fatalf("create task agent: %v", err)
	}

	rr, body := postRun(t, newMux(st, nil, newRunBackend("EPIC-1", nil)), "WS", "EPIC-1", map[string]string{"lead": "worker"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%v", rr.Code, body)
	}
}

func TestRunEpicRejectsMissingLead(t *testing.T) {
	st := newTestStore(t)
	createRunnableWorkspace(t, st, "WS")

	rr, body := postRun(t, newMux(st, nil, newRunBackend("EPIC-1", nil)), "WS", "EPIC-1", map[string]string{"lead": "ghost"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%v", rr.Code, body)
	}
}

func TestRunEpicRejectsBlankBody(t *testing.T) {
	st := newTestStore(t)

	rr, body := postRun(t, newMux(st, nil, newRunBackend("EPIC-1", nil)), "WS", "EPIC-1", map[string]string{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%v", rr.Code, body)
	}
}

func TestRunEpicRejectsWorkspaceWithoutRepoBeforeBinding(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createLead(t, st, "WS", "nova", "", "")

	rr, body := postRun(t, newMux(st, nil, newRunBackend("EPIC-1", nil)), "WS", "EPIC-1", map[string]string{"lead": "nova"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%v", rr.Code, body)
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "has no repos attached") {
		t.Fatalf("error = %q, want no repos message", msg)
	}
	lead, err := st.Agents().Get(ctx, "WS", "nova")
	if err != nil {
		t.Fatalf("get lead: %v", err)
	}
	if lead.Parent != "" {
		t.Fatalf("lead parent = %q, want empty after failed preflight", lead.Parent)
	}
}

func TestRunEpicDispatchesReadyTask(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createRunnableWorkspace(t, st, "WS")
	createLead(t, st, "WS", "nova", "", "lead-session")
	task := backend.IssueData{ID: "EPIC-2", Title: "ready task", Status: "open", Parent: "EPIC-1"}

	rr, body := postRun(t, newMux(st, nil, newRunBackend("EPIC-1", []backend.IssueData{task})), "WS", "EPIC-1", map[string]string{"lead": "nova"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rr.Code, body)
	}
	if body["run_state"] != "reconciled" {
		t.Fatalf("run_state = %v, want reconciled", body["run_state"])
	}
	workerName := "epic-1-epic-2"
	agents, err := st.Agents().List(ctx, "WS")
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	var worker string
	for _, agent := range agents {
		if agent != nil && agent.Mode == domain.AgentModeEphemeral {
			worker = agent.Name
			break
		}
	}
	if worker == "" || !strings.HasPrefix(worker, workerName) {
		t.Fatalf("worker = %q, want deterministic prefix %q", worker, workerName)
	}
	cmds, err := st.AgentCommands().List(ctx, "WS", store.AgentCommandFilter{TargetAgentID: worker})
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Payload["task_id"] != "EPIC-2" || cmds[0].Payload["parent_session_id"] != "lead-session" || cmds[0].TargetNodeID != "node-1" {
		t.Fatalf("commands = %#v, want start command for EPIC-2 on node-1 with lead session", cmds)
	}
}

func newTestStore(t *testing.T) store.Store {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	return memstore.New()
}

func createRunnableWorkspace(t *testing.T, st store.Store, workspace string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  workspace,
		Name:          "app",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    workspace,
		NodeID:          "node-1",
		RuntimeProvider: domain.RuntimeProviderLocal,
		DrainState:      domain.NodeDrainActive,
		TTL:             time.Minute,
	}); err != nil {
		t.Fatalf("create node: %v", err)
	}
}

type runBackend struct {
	epicID   string
	children []backend.IssueData
}

func newRunBackend(epicID string, children []backend.IssueData) *runBackend {
	return &runBackend{epicID: epicID, children: children}
}

func (b *runBackend) Get(_ context.Context, _ string) (*backend.IssueDetailData, error) {
	return &backend.IssueDetailData{IssueData: backend.IssueData{ID: b.epicID, IssueType: "epic"}}, nil
}

func (b *runBackend) List(_ context.Context, _ backend.ListOpts) ([]backend.IssueData, error) {
	return b.children, nil
}

func (b *runBackend) Ready(_ context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) {
	return b.children, nil
}

func (b *runBackend) Blocked(_ context.Context, _ backend.BlockedOpts) ([]backend.IssueData, error) {
	return nil, nil
}

func (b *runBackend) Stats(_ context.Context) (*backend.StatsData, error) { return nil, nil }

func (b *runBackend) Count(_ context.Context, _ backend.CountOpts) (int, error) { return 0, nil }

func (b *runBackend) GetChildren(_ context.Context, _ string) ([]backend.IssueData, error) {
	return nil, nil
}

func (b *runBackend) SearchIssues(_ context.Context, _ string, _ int) ([]backend.IssueData, error) {
	return nil, nil
}

func (b *runBackend) Create(_ context.Context, _ backend.CreateParams) (*backend.IssueData, error) {
	return nil, nil
}

func (b *runBackend) Update(_ context.Context, _ string, _ backend.UpdateParams) error { return nil }

func (b *runBackend) ClaimIssue(_ context.Context, _ string, _ time.Duration) error { return nil }

func (b *runBackend) ReleaseIssueLock(_ context.Context, _ string, _ string) error { return nil }

func (b *runBackend) DeferIssue(_ context.Context, _ string, _ time.Time) error { return nil }

func (b *runBackend) UndeferIssue(_ context.Context, _ string) error { return nil }

func (b *runBackend) Close(_ context.Context, _ string, _ backend.CloseParams) (*backend.CloseResult, error) {
	return nil, nil
}

func (b *runBackend) Reopen(_ context.Context, _ string, _ backend.ReopenParams) error { return nil }

func (b *runBackend) Delete(_ context.Context, _ backend.DeleteParams) error { return nil }

func (b *runBackend) AddDependency(_ context.Context, _ backend.DepAddParams) error { return nil }

func (b *runBackend) RemoveDependency(_ context.Context, _ backend.DepRemoveParams) error { return nil }

func (b *runBackend) AddLabel(_ context.Context, _, _ string) error { return nil }

func (b *runBackend) RemoveLabel(_ context.Context, _, _ string) error { return nil }

func (b *runBackend) ListComments(_ context.Context, _ string) ([]backend.CommentData, error) {
	return nil, nil
}

func (b *runBackend) AddComment(_ context.Context, _ backend.CommentAddParams) (*backend.CommentData, error) {
	return nil, nil
}

func (b *runBackend) ListEvents(_ context.Context, _ string, _ int) ([]backend.EventData, error) {
	return nil, nil
}

func (b *runBackend) Batch(_ context.Context, _ []backend.BatchOp) ([]backend.BatchResult, error) {
	return nil, nil
}

func (b *runBackend) GetMutations(_ context.Context, _ int64) ([]backend.MutationData, error) {
	return nil, nil
}

func (b *runBackend) WaitForMutations(_ context.Context, _, _ int64) ([]backend.MutationData, error) {
	return nil, nil
}

func (b *runBackend) BackendName() string {
	return "run-backend"
}

func createLead(t *testing.T, st store.Store, workspace, name, parent, orchestrator string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: workspace,
		Name:         name,
		RoleName:     "lead",
		Parent:       parent,
	}); err != nil {
		t.Fatalf("create lead: %v", err)
	}
	if orchestrator == "" {
		return
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: workspace,
		SessionID:    orchestrator,
		AgentID:      name,
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create orchestrator session: %v", err)
	}
}

func waitForHubClient(t *testing.T, hub *realtime.Hub) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if hub.ClientCount() == 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("SSE client was not registered")
		case <-ticker.C:
		}
	}
}

func expectAgentRefresh(t *testing.T, events <-chan *realtime.MutationPayload, workspace, agentName string) {
	t.Helper()
	select {
	case event := <-events:
		if event == nil {
			t.Fatal("SSE event channel closed")
		}
		if event.Type != "refresh" || event.WorkspaceID != workspace || event.EntityType != "agent" || event.EntityID != agentName {
			t.Fatalf("unexpected refresh event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for agent refresh event for %s", agentName)
	}
}
