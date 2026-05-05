package metricscmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
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

func TestHandleAgents_MergesStoreAgentAssignments(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "WS1")
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS1",
		Name:         "falcon",
		RoleName:     "task",
		Repos:        []string{"repo-a"},
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

	data := &monitor.MonitorData{
		Timestamp: time.Unix(1, 0).UTC(),
		Agents: []monitor.AgentStatus{{
			Name:   "falcon",
			Branch: "feature/falcon",
			Status: "ready",
			Ahead:  1,
		}},
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
		t.Fatalf("agents = %+v, want runtime + store assignment", resp.Agents)
	}
	if got := byName["falcon"]; got.Role != "task" || got.Repo != "repo-a" || got.Workspace != "Test" || got.Status != "ready" {
		t.Fatalf("falcon not enriched from store: %+v", got)
	}
	if got := byName["nova"]; got.Role != "plan" || got.Status != "idle" || got.Workspace != "Test" {
		t.Fatalf("nova not synthesized from store: %+v", got)
	}
	if len(resp.ByWorkspace["Test"]) != 2 {
		t.Fatalf("by_workspace[Test] = %+v, want both agents", resp.ByWorkspace["Test"])
	}
}

func TestHandleAgents_SynthesizesStoreAgentBranchFromLocalWorktree(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "WS1")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS1",
		Name:         "cobalt",
		RoleName:     "task",
		Repos:        []string{"repo-a"},
	}); err != nil {
		t.Fatal(err)
	}

	wsRoot := t.TempDir()
	wtGitDir := filepath.Join(wsRoot, "worktrees", "repo-a", "cobalt", ".git")
	if err := os.MkdirAll(wtGitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtGitDir, "HEAD"), []byte("ref: refs/heads/feature/cobalt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Version:       1,
		LastWorkspace: "WS1",
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS1": {Path: wsRoot},
		},
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/monitor/agents?workspace=WS1", nil)
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
	if len(resp.Agents) != 1 {
		t.Fatalf("agents = %+v, want one synthesized agent", resp.Agents)
	}
	if got := resp.Agents[0]; got.Name != "cobalt" || got.Branch != "feature/cobalt" {
		t.Fatalf("agent = %+v, want cobalt on feature/cobalt", got)
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
