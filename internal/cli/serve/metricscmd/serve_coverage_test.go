package metricscmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/usage"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func TestGroupAgentsByWorkspace_Coverage(t *testing.T) {
	agents := []AgentStatus{
		{Name: "alpha", Workspace: "ws1"},
		{Name: "beta", Workspace: "ws1"},
		{Name: "gamma", Workspace: "ws2"},
		{Name: "delta", Workspace: ""},
	}

	groups := groupAgentsByWorkspace(agents)

	if len(groups["ws1"]) != 2 {
		t.Errorf("ws1 count = %d, want 2", len(groups["ws1"]))
	}
	if len(groups["ws2"]) != 1 {
		t.Errorf("ws2 count = %d, want 1", len(groups["ws2"]))
	}
	if len(groups["unassigned"]) != 1 {
		t.Errorf("unassigned count = %d, want 1", len(groups["unassigned"]))
	}
}

func TestGroupAgentsByWorkspace_Empty(t *testing.T) {
	groups := groupAgentsByWorkspace([]AgentStatus{})
	if len(groups) != 0 {
		t.Errorf("expected empty map, got %d entries", len(groups))
	}
}

func TestGroupAgentsByWorkspace_AllSameWorkspace(t *testing.T) {
	agents := []AgentStatus{
		{Name: "a", Workspace: "ws"},
		{Name: "b", Workspace: "ws"},
		{Name: "c", Workspace: "ws"},
	}

	groups := groupAgentsByWorkspace(agents)
	if len(groups) != 1 {
		t.Errorf("expected 1 group, got %d", len(groups))
	}
	if len(groups["ws"]) != 3 {
		t.Errorf("ws count = %d, want 3", len(groups["ws"]))
	}
}

func TestHandleAgents_UsesStoreAgentsAsSourceOfTruth(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "WS1")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	st := memstore.New()
	wsRoot := t.TempDir()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "WS1",
		Name:          "repo-a",
		DefaultBranch: "main",
		Remote:        "origin",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS1",
		Name:         "falcon",
		RoleName:     "task",
		Repos:        []string{"repo-a"},
		Parent:       "EPIC-1",
		Mode:         domain.AgentModeEphemeral,
		DesiredState: domain.AgentDesiredStopped,
	}); err != nil {
		t.Fatal(err)
	}
	// Seed the orchestration AgentSession row — the monitor data source
	// reads attribution via OrchestrationSessionIDFor.
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS1",
		SessionID:    "lead-session",
		AgentID:      "falcon",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS1",
		Name:         "nova",
		RoleName:     "plan",
		CrossRepo:    true,
	}); err != nil {
		t.Fatal(err)
	}
	active := domain.AgentStateActive
	if _, err := st.Agents().Update(ctx, "WS1", "falcon", store.AgentUpdate{State: &active}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS1",
		SessionID:    "session-falcon",
		AgentID:      "falcon",
		TaskID:       "TASK-1",
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatal(err)
	}
	falconWorktree := filepath.Join(wsRoot, "worktrees", "repo-a", "falcon")
	if err := runGitForMetricsTest(t, falconWorktree, "init", "-b", "feature/falcon"); err != nil {
		t.Fatalf("init falcon worktree: %v", err)
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.LastWorkspace = "WS1"
		sc.Workspaces["WS1"] = bootstrap.WorkspaceLocalState{
			Path:  wsRoot,
			Repos: map[string]string{"repo-a": filepath.Join(wsRoot, "repo-a")},
		}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	lastActivity := time.Unix(1700000000, 123456000).UTC()
	data := &monitor.MonitorData{
		Timestamp: time.Unix(1, 0).UTC(),
		Agents: []monitor.AgentStatus{
			{
				Name:           "falcon",
				Branch:         "runtime/stale",
				Status:         "planning: HELLO-WORLD-1",
				Ahead:          1,
				DaemonManaged:  true,
				CurrentTaskID:  "HELLO-WORLD-1",
				LastActivityAt: &lastActivity,
			},
			{Name: "stray", Branch: "feature/stray", Status: "ready", Workspace: "Test"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/monitor/agents", nil)
	rr := httptest.NewRecorder()

	HandleAgents(func() *monitor.MonitorData { return data }, st).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp AgentsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	byName := make(map[string]monitor.AgentStatus, len(resp.Agents))
	for _, agent := range resp.Agents {
		byName[agent.Name] = agent
	}
	if len(byName) != 2 {
		t.Fatalf("agents = %+v, want only store assignments", resp.Agents)
	}
	if _, exists := byName["stray"]; exists {
		t.Fatalf("unregistered runtime agent leaked into response: %+v", resp.Agents)
	}
	falcon := byName["falcon"]
	if falcon.Role != "task" || falcon.Repo != "repo-a" || falcon.Workspace != "Test" || falcon.Status != "planning: HELLO-WORLD-1" || falcon.Branch != "feature/falcon" {
		t.Fatalf("falcon not sourced from store: %+v", falcon)
	}
	if !falcon.DaemonManaged {
		t.Fatalf("falcon DaemonManaged = false, want true")
	}
	if falcon.CurrentTaskID != "HELLO-WORLD-1" {
		t.Fatalf("falcon CurrentTaskID = %q, want HELLO-WORLD-1", falcon.CurrentTaskID)
	}
	if falcon.LastActivityAt == nil || !falcon.LastActivityAt.Equal(lastActivity) {
		t.Fatalf("falcon LastActivityAt = %v, want %v", falcon.LastActivityAt, lastActivity)
	}
	if got := byName["falcon"]; got.Parent != "EPIC-1" || got.OrchestratorSessionID != "lead-session" || got.Mode != "ephemeral" || got.DesiredState != "stopped" {
		t.Fatalf("falcon orchestration fields not sourced from store: %+v", got)
	}
	if got := byName["falcon"]; got.TaskID != "TASK-1" || got.SessionID != "session-falcon" {
		t.Fatalf("falcon session fields not sourced from agent sessions: %+v", got)
	}
	if got := byName["nova"]; got.Role != "plan" || got.Status != "idle" || got.Workspace != "Test" {
		t.Fatalf("nova not sourced from store: %+v", got)
	}
	if len(resp.ByWorkspace["Test"]) != 2 {
		t.Fatalf("by_workspace[Test] = %+v, want both agents", resp.ByWorkspace["Test"])
	}
}

func TestHandleStatus_ActiveStoreAgentWithoutWorkIsReady(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "WS1")
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS1",
		Name:         "planner",
		RoleName:     "plan",
	}); err != nil {
		t.Fatal(err)
	}
	active := domain.AgentStateActive
	if _, err := st.Agents().Update(ctx, "WS1", "planner", store.AgentUpdate{State: &active}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/monitor/status", nil)
	rr := httptest.NewRecorder()
	HandleStatus(func() *monitor.MonitorData {
		return &monitor.MonitorData{Timestamp: time.Unix(1, 0).UTC()}
	}, st).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp StatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(resp.Agents) != 1 {
		t.Fatalf("agents = %+v, want one planner", resp.Agents)
	}
	if got := resp.Agents[0].Status; got != "ready" {
		t.Fatalf("planner status = %q, want ready", got)
	}
}

func TestHandleStatus_DerivesPlanningFromInProgressTaskWithoutRuntimeAgent(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "WS1")
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS1",
		Name:         "planner",
		RoleName:     "plan",
	}); err != nil {
		t.Fatal(err)
	}
	active := domain.AgentStateActive
	if _, err := st.Agents().Update(ctx, "WS1", "planner", store.AgentUpdate{State: &active}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/monitor/status", nil)
	rr := httptest.NewRecorder()
	HandleStatus(func() *monitor.MonitorData {
		return &monitor.MonitorData{
			Timestamp: time.Unix(1, 0).UTC(),
			AgentTasks: map[string]monitor.TaskInfo{
				"planner": {ID: "HELLO-WORLD-1", Title: "Explore", Status: "in_progress"},
			},
		}
	}, st).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp StatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(resp.Agents) != 1 {
		t.Fatalf("agents = %+v, want one planner", resp.Agents)
	}
	if got := resp.Agents[0].Status; got != "planning: HELLO-WORLD-1" {
		t.Fatalf("planner status = %q, want planning task", got)
	}
}

func runGitForMetricsTest(t *testing.T, dir string, args ...string) error {
	t.Helper()
	if len(args) > 0 && args[0] == "init" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	cmd := exec.Command("git", args...) //nolint:norawexec // test helper shells out to git with fixed args from tests.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("git %v output: %s", args, out)
	}
	return err
}

func TestHandleAgents_EmptyWorkspaceDoesNotLeakRuntimeAgents(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "WS1")
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Test"}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/monitor/agents?workspace=WS1", nil)
	rr := httptest.NewRecorder()
	HandleAgents(func() *monitor.MonitorData {
		return &monitor.MonitorData{
			Timestamp: time.Unix(1, 0).UTC(),
			Agents: []monitor.AgentStatus{
				{Name: "local-only", Branch: "main", Status: "ready", Workspace: "Test"},
			},
		}
	}, st).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp AgentsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(resp.Agents) != 0 {
		t.Fatalf("agents = %+v, want no agents for empty store workspace", resp.Agents)
	}
}

func TestHandleAgents_UsesWorkspaceQueryOverActiveWorkspace(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "WS1")
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "First"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS2", Name: "Second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS2",
		Name:         "nova",
		RoleName:     "task",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/monitor/agents?workspace=WS2", nil)
	rr := httptest.NewRecorder()
	HandleAgents(func() *monitor.MonitorData {
		return &monitor.MonitorData{Timestamp: time.Unix(1, 0).UTC()}
	}, st).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp AgentsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if resp.Workspace.Name != "Second" {
		t.Fatalf("workspace name = %q, want Second", resp.Workspace.Name)
	}
	if len(resp.Agents) != 1 || resp.Agents[0].Name != "nova" || resp.Agents[0].Workspace != "Second" {
		t.Fatalf("agents = %+v, want WS2 agent", resp.Agents)
	}
}

func TestHandleStatusWithBackend_UsesWorkspaceScopedIssueBackend(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "WS1")
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "First"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS2", Name: "Second"}); err != nil {
		t.Fatal(err)
	}

	scopedBackend := clitest.NewMockIssueBackend()
	scopedBackend.ReadyResult = []backend.IssueData{
		{ID: "T-1", Title: "Scoped task", Status: "open", Design: ""},
	}
	scopedBackend.StatsResult = &backend.StatsData{TotalIssues: 7, OpenIssues: 6, ClosedIssues: 1}
	backendFn := func(ctx context.Context) backend.IssueBackend {
		if got := middleware.WorkspaceFromContext(ctx); got != "WS2" {
			t.Fatalf("workspace context = %q, want WS2", got)
		}
		return scopedBackend
	}
	cachedData := &monitor.MonitorData{
		Timestamp: time.Unix(1, 0).UTC(),
		Stats:     monitor.MonitorStats{Total: 99, Open: 99},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/monitor/status?workspace=WS2", nil)
	rr := httptest.NewRecorder()
	HandleStatusWithBackend(func() *monitor.MonitorData { return cachedData }, st, backendFn).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp StatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if resp.Workspace.Name != "Second" {
		t.Fatalf("workspace name = %q, want Second", resp.Workspace.Name)
	}
	if resp.Stats.Total != 7 || resp.Stats.Open != 6 || resp.Stats.Closed != 1 {
		t.Fatalf("stats = %+v, want scoped backend stats", resp.Stats)
	}
	if resp.Tasks.NeedsPlanning != 1 {
		t.Fatalf("needs_planning = %d, want scoped ready task", resp.Tasks.NeedsPlanning)
	}
	if len(resp.NeedsPlanning) != 1 || resp.NeedsPlanning[0].ID != "T-1" {
		t.Fatalf("needs_planning list = %+v, want scoped ready task", resp.NeedsPlanning)
	}
	if resp.ReadyToImplement == nil || resp.Backlog == nil || resp.Closed == nil {
		t.Fatalf("empty task buckets must serialize as arrays, got ready=%v backlog=%v closed=%v", resp.ReadyToImplement, resp.Backlog, resp.Closed)
	}
}

func TestMonitorDataSource_CachesWorkspaceCollectionAcrossEndpoints(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "WS1")
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "First"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS2", Name: "Second"}); err != nil {
		t.Fatal(err)
	}

	scopedBackend := clitest.NewMockIssueBackend()
	scopedBackend.ReadyResult = []backend.IssueData{
		{ID: "T-1", Title: "Scoped task", Status: "open", Design: ""},
	}
	scopedBackend.StatsResult = &backend.StatsData{TotalIssues: 7, OpenIssues: 6, ClosedIssues: 1}

	backendFnCalls := 0
	backendFn := func(ctx context.Context) backend.IssueBackend {
		backendFnCalls++
		if got := middleware.WorkspaceFromContext(ctx); got != "WS2" {
			t.Fatalf("workspace context = %q, want WS2", got)
		}
		return scopedBackend
	}
	dataSource := NewMonitorDataSourceWithTTL(func() *monitor.MonitorData {
		t.Fatal("fallback collector should not be used for workspace request")
		return nil
	}, backendFn, time.Minute)

	for _, handler := range []http.HandlerFunc{
		HandleStatusWithDataSource(dataSource, st),
		HandleTasksWithDataSource(dataSource),
		HandleStatsWithDataSource(dataSource),
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/monitor/test?workspace=WS2", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rr.Code, rr.Body.String())
		}
	}

	if backendFnCalls != 1 {
		t.Fatalf("backendFn calls = %d, want 1 shared workspace collection", backendFnCalls)
	}
	if got := scopedBackend.CallCount("Ready"); got != 1 {
		t.Fatalf("Ready calls = %d, want 1 shared workspace collection", got)
	}
	if got := scopedBackend.CallCount("Stats"); got != 1 {
		t.Fatalf("Stats calls = %d, want 1 shared workspace collection", got)
	}
}

func TestMonitorDataSource_DefaultWorkspaceUsesWarmCollector(t *testing.T) {
	collectCalls := 0
	backendFnCalls := 0
	dataSource := NewMonitorDataSourceWithDefaultWorkspace(func() *monitor.MonitorData {
		collectCalls++
		return &monitor.MonitorData{Timestamp: time.Unix(1, 0).UTC()}
	}, func(ctx context.Context) backend.IssueBackend {
		backendFnCalls++
		return nil
	}, "WS2")

	req := httptest.NewRequest(http.MethodGet, "/api/monitor/status?workspace=WS2", nil)
	if data := dataSource.Resolve(req); data == nil {
		t.Fatal("Resolve returned nil")
	}

	if collectCalls != 1 {
		t.Fatalf("collect calls = %d, want 1 warm collector read", collectCalls)
	}
	if backendFnCalls != 0 {
		t.Fatalf("backendFn calls = %d, want 0 duplicate workspace collection", backendFnCalls)
	}
}

func TestMonitorStoreDataSource_CachesWorkspaceMetadataAcrossEndpoints(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "WS1")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	base := memstore.New()
	if _, err := base.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "First"}); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS2", Name: "Second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "WS1", Name: "repo-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "WS2", Name: "repo-b", Groups: []string{"backend"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS2",
		Name:         "nova",
		RoleName:     "task",
		RepoGroups:   []string{"backend"},
	}); err != nil {
		t.Fatal(err)
	}

	counted := newCountingStore(base)
	dataSource := NewMonitorDataSourceWithTTL(func() *monitor.MonitorData {
		return &monitor.MonitorData{Timestamp: time.Unix(1, 0).UTC()}
	}, nil, time.Minute)
	storeDataSource := NewMonitorStoreDataSourceWithTTL(counted, time.Minute)

	for _, handler := range []http.HandlerFunc{
		HandleStatusWithSources(dataSource, storeDataSource),
		HandleAgentsWithSources(dataSource, storeDataSource),
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/monitor/test?workspace=WS2", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rr.Code, rr.Body.String())
		}
	}

	if got := counted.workspaces.listCalls; got != 1 {
		t.Fatalf("workspace List calls = %d, want 1 shared store metadata read", got)
	}
	if got := counted.workspaces.getCalls; got != 0 {
		t.Fatalf("workspace Get calls = %d, want 0 when request workspace is resolved from List", got)
	}
	if got := counted.agents.listCalls; got != 1 {
		t.Fatalf("agent List calls = %d, want 1 shared store metadata read", got)
	}
	if got := counted.repos.listCalls; got != 1 {
		t.Fatalf("repo List calls = %d, want only selected workspace repos once", got)
	}
	if got := counted.repos.listByWorkspace["WS1"]; got != 0 {
		t.Fatalf("WS1 repo List calls = %d, want no cross-workspace repo reads", got)
	}
	if got := counted.repos.listByWorkspace["WS2"]; got != 1 {
		t.Fatalf("WS2 repo List calls = %d, want 1", got)
	}
	if got := counted.daemon.getCalls; got != 0 {
		t.Fatalf("daemon Get calls = %d, want no workspace summary daemon reads", got)
	}
}

func TestMonitorStoreDataSourcePopulatesRoleKind(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Workspace"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "WS1",
		Name:         "operator",
		Kind:         string(domain.RoleKindInteractive),
	}); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []store.AgentCreate{
		{WorkspaceKey: "WS1", Name: "lead-a", RoleName: "lead"},
		{WorkspaceKey: "WS1", Name: "operator-a", RoleName: "operator"},
		{WorkspaceKey: "WS1", Name: "task-a", RoleName: "task"},
	} {
		if _, err := st.Agents().Create(ctx, agent); err != nil {
			t.Fatal(err)
		}
	}

	data := collectMonitorStoreData(ctx, st, "WS1")
	got := map[string]string{}
	for _, agent := range data.Agents {
		got[agent.Name] = agent.RoleKind
	}
	if got["lead-a"] != string(domain.RoleKindInteractive) {
		t.Fatalf("lead-a role_kind = %q, want interactive", got["lead-a"])
	}
	if got["operator-a"] != string(domain.RoleKindInteractive) {
		t.Fatalf("operator-a role_kind = %q, want interactive", got["operator-a"])
	}
	if got["task-a"] != string(domain.RoleKindWorker) {
		t.Fatalf("task-a role_kind = %q, want worker", got["task-a"])
	}
}

type countingStore struct {
	store.Store
	workspaces *countingWorkspaceStore
	repos      *countingRepoStore
	agents     *countingAgentStore
	daemon     *countingDaemonStore
}

func newCountingStore(base store.Store) *countingStore {
	return &countingStore{
		Store:      base,
		workspaces: &countingWorkspaceStore{WorkspaceStore: base.Workspaces()},
		repos:      &countingRepoStore{RepoStore: base.Repos(), listByWorkspace: make(map[string]int)},
		agents:     &countingAgentStore{AgentStore: base.Agents()},
		daemon:     &countingDaemonStore{DaemonProfileStore: base.Daemon()},
	}
}

func (s *countingStore) Workspaces() store.WorkspaceStore { return s.workspaces }
func (s *countingStore) Repos() store.RepoStore           { return s.repos }
func (s *countingStore) Agents() store.AgentStore         { return s.agents }
func (s *countingStore) Daemon() store.DaemonProfileStore { return s.daemon }

type countingWorkspaceStore struct {
	store.WorkspaceStore
	getCalls       int
	getByNameCalls int
	listCalls      int
}

func (s *countingWorkspaceStore) Get(ctx context.Context, key string) (*domain.Workspace, error) {
	s.getCalls++
	return s.WorkspaceStore.Get(ctx, key)
}

func (s *countingWorkspaceStore) GetByName(ctx context.Context, name string) (*domain.Workspace, error) {
	s.getByNameCalls++
	return s.WorkspaceStore.GetByName(ctx, name)
}

func (s *countingWorkspaceStore) List(ctx context.Context) ([]*domain.Workspace, error) {
	s.listCalls++
	return s.WorkspaceStore.List(ctx)
}

type countingRepoStore struct {
	store.RepoStore
	listCalls       int
	listByWorkspace map[string]int
}

func (s *countingRepoStore) List(ctx context.Context, workspaceKey string) ([]*domain.Repo, error) {
	s.listCalls++
	s.listByWorkspace[workspaceKey]++
	return s.RepoStore.List(ctx, workspaceKey)
}

type countingAgentStore struct {
	store.AgentStore
	listCalls int
}

func (s *countingAgentStore) List(ctx context.Context, workspaceKey string) ([]*domain.Agent, error) {
	s.listCalls++
	return s.AgentStore.List(ctx, workspaceKey)
}

type countingDaemonStore struct {
	store.DaemonProfileStore
	getCalls int
}

func (s *countingDaemonStore) Get(ctx context.Context, workspaceKey string) (*domain.DaemonProfile, error) {
	s.getCalls++
	return s.DaemonProfileStore.Get(ctx, workspaceKey)
}

func TestWriteJSON_Coverage(t *testing.T) {
	rr := httptest.NewRecorder()

	data := map[string]string{"key": "value"}
	writeJSON(rr, data)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	body := rr.Body.String()
	if body == "" {
		t.Error("response body should not be empty")
	}
}

func TestHandleUsage_NoStore(t *testing.T) {
	orig := usageStoreInstance
	usageStoreInstance = nil
	t.Cleanup(func() { usageStoreInstance = orig })

	req := httptest.NewRequest("GET", "/api/usage", nil)
	rr := httptest.NewRecorder()
	handleUsage(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rr.Code)
	}
}

func TestHandleUsage_EmptyStore(t *testing.T) {
	dir := t.TempDir()
	store, err := usage.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	orig := usageStoreInstance
	usageStoreInstance = store
	t.Cleanup(func() { usageStoreInstance = orig })

	req := httptest.NewRequest("GET", "/api/usage", nil)
	rr := httptest.NewRecorder()
	handleUsage(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp UsageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if resp.SessionCount != 0 {
		t.Errorf("expected 0 sessions, got %d", resp.SessionCount)
	}
	if len(resp.Sessions) != 0 {
		t.Errorf("expected empty sessions, got %d", len(resp.Sessions))
	}
}

func TestHandleUsage_WithData(t *testing.T) {
	dir := t.TempDir()
	store, err := usage.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	rec := usage.SessionUsage{
		AgentName:        "nova",
		Backend:          "claude",
		TaskID:           "kv31p.4",
		InputTokens:      100000,
		OutputTokens:     50000,
		CacheReadTokens:  10000,
		CacheWriteTokens: 5000,
		EstimatedCostUSD: 1.50,
		StartedAt:        now.Add(-10 * time.Minute),
		EndedAt:          now,
		ExitCode:         0,
	}
	if err := store.Append(rec); err != nil {
		t.Fatal(err)
	}

	orig := usageStoreInstance
	usageStoreInstance = store
	t.Cleanup(func() { usageStoreInstance = orig })

	req := httptest.NewRequest("GET", "/api/usage", nil)
	rr := httptest.NewRecorder()
	handleUsage(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp UsageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if resp.SessionCount != 1 {
		t.Errorf("SessionCount = %d, want 1", resp.SessionCount)
	}
	if resp.TotalInputTokens != 100000 {
		t.Errorf("TotalInputTokens = %d, want 100000", resp.TotalInputTokens)
	}
	if resp.TotalCost != 1.50 {
		t.Errorf("TotalCost = %f, want 1.50", resp.TotalCost)
	}
	if len(resp.ByAgent) != 1 || resp.ByAgent[0].Name != "nova" {
		t.Errorf("ByAgent unexpected: %+v", resp.ByAgent)
	}
}

func TestHandleUsage_QueryFilters(t *testing.T) {
	dir := t.TempDir()
	store, err := usage.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	recs := []usage.SessionUsage{
		{AgentName: "nova", Backend: "claude", EstimatedCostUSD: 1.0, StartedAt: now, EndedAt: now},
		{AgentName: "falcon", Backend: "codex", EstimatedCostUSD: 2.0, StartedAt: now, EndedAt: now},
	}
	for _, r := range recs {
		if err := store.Append(r); err != nil {
			t.Fatal(err)
		}
	}

	orig := usageStoreInstance
	usageStoreInstance = store
	t.Cleanup(func() { usageStoreInstance = orig })

	// Filter by agent
	req := httptest.NewRequest("GET", "/api/usage?agent=nova", nil)
	rr := httptest.NewRecorder()
	handleUsage(rr, req)

	var resp UsageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if resp.SessionCount != 1 {
		t.Errorf("filtered SessionCount = %d, want 1", resp.SessionCount)
	}
	if resp.TotalCost != 1.0 {
		t.Errorf("filtered TotalCost = %f, want 1.0", resp.TotalCost)
	}
}

func TestHandleUsage_InvalidDate(t *testing.T) {
	dir := t.TempDir()
	store, err := usage.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	orig := usageStoreInstance
	usageStoreInstance = store
	t.Cleanup(func() { usageStoreInstance = orig })

	req := httptest.NewRequest("GET", "/api/usage?since=not-a-date", nil)
	rr := httptest.NewRecorder()
	handleUsage(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid date, got %d", rr.Code)
	}
}

func TestHandleUsage_InvalidUntilDate(t *testing.T) {
	dir := t.TempDir()
	store, err := usage.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	orig := usageStoreInstance
	usageStoreInstance = store
	t.Cleanup(func() { usageStoreInstance = orig })

	req := httptest.NewRequest("GET", "/api/usage?until=invalid", nil)
	rr := httptest.NewRecorder()
	handleUsage(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid until date, got %d", rr.Code)
	}
}

func TestHandleStaleDetector_NotEnabled(t *testing.T) {
	// Ensure staleDetectorInstance is nil
	origInstance := staleDetectorInstance
	staleDetectorInstance = nil
	t.Cleanup(func() { staleDetectorInstance = origInstance })

	req := httptest.NewRequest("GET", "/api/stale-detector", nil)
	rr := httptest.NewRecorder()

	handleStaleDetector(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if body == "" {
		t.Error("response body should not be empty")
	}
}
