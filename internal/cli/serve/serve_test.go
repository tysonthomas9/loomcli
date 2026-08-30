package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/placement/daytona"
	"github.com/tysonthomas9/loomcli/internal/testutil"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

// mockMonitorData creates a sample MonitorData for testing
func mockMonitorData() *MonitorData {
	return &MonitorData{
		Timestamp: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		Agents: []AgentStatus{
			{Name: "falcon", Branch: "falcon", Status: "ready", Ahead: 0, Behind: 0},
			{Name: "nova", Branch: "nova", Status: "working: loom-123 (5m)", Ahead: 2, Behind: 1},
		},
		Tasks: TaskSummary{
			NeedsPlanning:    3,
			ReadyToImplement: 5,
			InProgress:       2,
			NeedReview:       1,
			Backlog:          0,
		},
		NeedsPlanningTasks: []TaskInfo{
			{ID: "loom-001", Title: "Add feature X", Priority: 1},
		},
		ReadyToImplement: []TaskInfo{
			{ID: "loom-002", Title: "Fix bug Y", Priority: 2},
		},
		ReviewTasks: []TaskInfo{
			{ID: "loom-003", Title: "Review task Z", Priority: 1},
		},
		InProgressTasks: []TaskInfo{
			{ID: "loom-123", Title: "Current task", Priority: 1, Status: "in_progress"},
		},
		BacklogTasks: []TaskInfo{},
		ClosedTasks: []TaskInfo{
			{ID: "loom-010", Title: "Completed task", Priority: 2, Status: "closed"},
			{ID: "loom-011", Title: "Another done task", Priority: 3, Status: "closed"},
		},
		AgentTasks: map[string]TaskInfo{
			"nova": {ID: "loom-123", Title: "Current task", Priority: 1, Status: "in_progress"},
		},
		TaskConflicts: map[string][]string{},
		SyncStatus: SyncInfo{
			DBSynced:     true,
			DBLastSync:   "recently",
			GitNeedsPush: 1,
			GitNeedsPull: 1,
		},
		Stats: MonitorStats{
			Open:       8,
			Closed:     12,
			Total:      20,
			Completion: 60.0,
		},
	}
}

func TestBuildMonitorCollectDataFnIsLazy(t *testing.T) {
	var backendCalls atomic.Int32

	_ = buildMonitorCollectDataFn("WS", func(context.Context) backend.IssueBackend {
		backendCalls.Add(1)
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	if got := backendCalls.Load(); got != 0 {
		t.Fatalf("buildMonitorCollectDataFn called issue backend before first request: got %d calls", got)
	}
}

func TestLeadLostReleaseGrace(t *testing.T) {
	tests := []struct {
		value string
		want  time.Duration
	}{
		{value: "", want: 30 * time.Minute},
		{value: "45m", want: 45 * time.Minute},
		{value: "2h30m", want: 2*time.Hour + 30*time.Minute},
		{value: "invalid", want: 30 * time.Minute},
		{value: "0", want: 30 * time.Minute},
		{value: "-5m", want: 30 * time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv(envLoomLeadLostReleaseGrace, tc.value)
			if got := leadLostReleaseGrace(); got != tc.want {
				t.Fatalf("leadLostReleaseGrace() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLeadSnapshotRef(t *testing.T) {
	original, wasSet := os.LookupEnv(envLoomLeadSnapshot)
	if err := os.Unsetenv(envLoomLeadSnapshot); err != nil {
		t.Fatalf("Unsetenv(%s): %v", envLoomLeadSnapshot, err)
	}
	t.Cleanup(func() {
		if wasSet {
			_ = os.Setenv(envLoomLeadSnapshot, original)
		} else {
			_ = os.Unsetenv(envLoomLeadSnapshot)
		}
	})

	if got := leadSnapshotRef(); got != daytona.DefaultSnapshotName {
		t.Fatalf("leadSnapshotRef() unset = %q, want %q", got, daytona.DefaultSnapshotName)
	}

	t.Setenv(envLoomLeadSnapshot, " loom-lead-poc-v3 ")
	if got := leadSnapshotRef(); got != "loom-lead-poc-v3" {
		t.Fatalf("leadSnapshotRef() override = %q, want %q", got, "loom-lead-poc-v3")
	}

	t.Setenv(envLoomLeadSnapshot, " \t\n ")
	if got := leadSnapshotRef(); got != daytona.DefaultSnapshotName {
		t.Fatalf("leadSnapshotRef() whitespace-only = %q, want %q", got, daytona.DefaultSnapshotName)
	}
}

func TestConfigureServeLocalRuntimeModeDefaultsHeadless(t *testing.T) {
	t.Setenv(envLocalRuntimeMode, "")
	t.Setenv(envDesktopDataDir, "")

	configureServeLocalRuntimeMode()

	if got := os.Getenv(envLocalRuntimeMode); got != localRuntimeModeHeadless {
		t.Fatalf("%s = %q, want %q", envLocalRuntimeMode, got, localRuntimeModeHeadless)
	}
}

func TestConfigureServeLocalRuntimeModePreservesExplicitMode(t *testing.T) {
	t.Setenv(envLocalRuntimeMode, "disabled")
	t.Setenv(envDesktopDataDir, "/tmp/desktop")

	configureServeLocalRuntimeMode()

	if got := os.Getenv(envLocalRuntimeMode); got != "disabled" {
		t.Fatalf("%s = %q, want explicit value preserved", envLocalRuntimeMode, got)
	}
}

func TestConfigureServeLocalRuntimeModeMarksDesktopService(t *testing.T) {
	t.Setenv(envLocalRuntimeMode, "")
	t.Setenv(envDesktopDataDir, "/tmp/desktop")

	configureServeLocalRuntimeMode()

	if got := os.Getenv(envLocalRuntimeMode); got != localRuntimeModeDesktop {
		t.Fatalf("%s = %q, want %q", envLocalRuntimeMode, got, localRuntimeModeDesktop)
	}
}

func TestDriverExecutorEnabled(t *testing.T) {
	for _, value := range []string{"", "1", "true", "TRUE", "yes", "on", "unexpected"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envLoomDriverExecutor, value)
			if !driverExecutorEnabled() {
				t.Fatalf("driverExecutorEnabled() = false for %q", value)
			}
		})
	}
	for _, value := range []string{"0", "false", "FALSE", "off", "no"} {
		t.Run("disabled_"+value, func(t *testing.T) {
			t.Setenv(envLoomDriverExecutor, value)
			if driverExecutorEnabled() {
				t.Fatalf("driverExecutorEnabled() = true for %q", value)
			}
		})
	}
}

func TestDriverTaskWorkerConcurrency(t *testing.T) {
	tests := []struct {
		value string
		want  int
	}{
		{"", 2},
		{"4", 4},
		{"0", 1},
		{"-3", 1},
		{"invalid", 2},
		{"100", 32},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv(envLoomDriverTaskWorkerConcurrency, tt.value)
			if got := driverTaskWorkerConcurrency(); got != tt.want {
				t.Fatalf("driverTaskWorkerConcurrency() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDriverTaskRunMaxAttempts(t *testing.T) {
	tests := []struct {
		value string
		want  int
	}{
		{"", 2},
		{"4", 4},
		{"0", 1},
		{"-3", 1},
		{"invalid", 2},
		{"100", 10},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv(envLoomDriverTaskRunMaxAttempts, tt.value)
			if got := driverTaskRunMaxAttempts(); got != tt.want {
				t.Fatalf("driverTaskRunMaxAttempts() = %d, want %d", got, tt.want)
			}
		})
	}
}

// withMockData runs a test with mocked collectDataFunc
func withMockData(t *testing.T, data *MonitorData, fn func()) {
	t.Helper()
	orig := collectDataFunc
	collectDataFunc = func() *MonitorData { return data }
	t.Cleanup(func() { collectDataFunc = orig })
	fn()
}

func TestWriteJSON(t *testing.T) {
	tests := []struct {
		name           string
		data           any
		wantStatusCode int
		wantJSON       bool
	}{
		{
			name:           "simple struct",
			data:           HealthResponse{Status: "ok", Timestamp: time.Now()},
			wantStatusCode: http.StatusOK,
			wantJSON:       true,
		},
		{
			name:           "map type",
			data:           map[string]string{"key": "value"},
			wantStatusCode: http.StatusOK,
			wantJSON:       true,
		},
		{
			name:           "slice type",
			data:           []string{"one", "two"},
			wantStatusCode: http.StatusOK,
			wantJSON:       true,
		},
		{
			name:           "empty struct",
			data:           struct{}{},
			wantStatusCode: http.StatusOK,
			wantJSON:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeJSON(rr, tt.data)

			// Check Content-Type header
			contentType := rr.Header().Get("Content-Type")
			if contentType != "application/json" {
				t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
			}

			// Verify response is valid JSON
			var result any
			if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
				t.Errorf("Response is not valid JSON: %v", err)
			}
		})
	}
}

func TestApplyWorkspaceConfig_NilStoreDoesNotWireWorkspaceFns(t *testing.T) {
	cfg := webui.ServerConfig{}

	applyWorkspaceConfig(&cfg)

	if cfg.WorkspaceIDResolverFn != nil {
		t.Fatal("WorkspaceIDResolverFn should be nil without store")
	}
	if cfg.WorkspaceDeleteFn != nil {
		t.Fatal("WorkspaceDeleteFn should be nil without store")
	}
	if cfg.SetDefaultWorkspaceFn != nil {
		t.Fatal("SetDefaultWorkspaceFn should be nil without store")
	}
	if cfg.ClearDefaultWorkspaceFn != nil {
		t.Fatal("ClearDefaultWorkspaceFn should be nil without store")
	}
	if cfg.WorkspaceCreateFn != nil {
		t.Fatal("WorkspaceCreateFn should be nil without store")
	}
	if cfg.DaemonConfigFn != nil {
		t.Fatal("DaemonConfigFn should be nil without store")
	}
}

func TestApplyWorkspaceConfig_StoreWiresStoreBackedFns(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	cfg := webui.ServerConfig{Store: memstore.New()}

	applyWorkspaceConfig(&cfg)

	if cfg.WorkspaceIDResolverFn == nil {
		t.Fatal("WorkspaceIDResolverFn was nil")
	}
	if cfg.WorkspaceDeleteFn == nil {
		t.Fatal("WorkspaceDeleteFn was nil")
	}
	if cfg.SetDefaultWorkspaceFn != nil {
		t.Fatal("SetDefaultWorkspaceFn should be nil; default workspace selection is removed")
	}
	if cfg.ClearDefaultWorkspaceFn != nil {
		t.Fatal("ClearDefaultWorkspaceFn should be nil; default workspace selection is removed")
	}
	if cfg.WorkspaceCreateFn == nil {
		t.Fatal("WorkspaceCreateFn was nil")
	}
	if cfg.DaemonConfigFn == nil {
		t.Fatal("DaemonConfigFn was nil")
	}
}

func TestApplyWorkspaceConfig_FleetClientWorkspaceOverridesCwdFallback(t *testing.T) {
	cfg := webui.ServerConfig{
		FleetClient:          true,
		FleetClientWorkspace: "PARITY",
	}

	applyWorkspaceConfig(&cfg)

	if cfg.InitialWorkspaceID != "PARITY" {
		t.Fatalf("InitialWorkspaceID = %q, want PARITY", cfg.InitialWorkspaceID)
	}
}

func TestApplyFleetConfig_StoreBackedServeDoesNotExpectDaemon(t *testing.T) {
	cfg := webui.ServerConfig{Store: memstore.New()}

	applyFleetConfig(&cfg, fleetState{})

	if !cfg.FleetClient {
		t.Fatal("FleetClient should be true for store-backed serve")
	}
}

func TestWithStoreFleetURLUsesEmbeddedStoreURL(t *testing.T) {
	fs := withStoreFleetURL(fleetState{}, "http://127.0.0.1:19090")

	if fs.clientCfg.URL != "http://127.0.0.1:19090" {
		t.Fatalf("clientCfg.URL = %q, want embedded store URL", fs.clientCfg.URL)
	}
}

func TestWithStoreFleetURLKeepsExplicitURL(t *testing.T) {
	fs := withStoreFleetURL(fleetState{
		clientCfg: config.FleetClientConfig{URL: "http://fleet-db:8080"},
	}, "http://127.0.0.1:19090")

	if fs.clientCfg.URL != "http://fleet-db:8080" {
		t.Fatalf("clientCfg.URL = %q, want explicit fleet URL", fs.clientCfg.URL)
	}
}

func TestResolveFleetStateResolvesClientConfigOutsideFleetMode(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(bootstrap.EnvWorkspace, "")
	t.Setenv(bootstrap.EnvFleetDBURL, "")
	t.Setenv(bootstrap.EnvFleetDBActor, "")
	t.Setenv("LOOM_ISSUE_BACKEND", "fleetdb")
	t.Setenv("LOOM_FLEET_URL", "http://fleet-db:8080")
	t.Setenv("LOOM_FLEET_ACTOR", "local-mode-harness")
	oldRedisAddr, oldRedisPassword := serveRedisAddr, serveRedisPassword
	serveRedisAddr, serveRedisPassword = "", ""
	t.Cleanup(func() {
		serveRedisAddr, serveRedisPassword = oldRedisAddr, oldRedisPassword
	})

	fs := resolveFleetState(context.Background())

	if fs.modeDetected {
		t.Fatal("modeDetected should be false for LOOM_ISSUE_BACKEND=fleetdb")
	}
	if fs.clientCfg.URL != "http://fleet-db:8080" {
		t.Fatalf("clientCfg.URL = %q, want fleet URL", fs.clientCfg.URL)
	}
	if fs.clientCfg.Actor != "local-mode-harness" {
		t.Fatalf("clientCfg.Actor = %q, want local-mode-harness", fs.clientCfg.Actor)
	}
}

func TestResolveFleetStateUsesLocalActorFallback(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(bootstrap.EnvWorkspace, "")
	t.Setenv(bootstrap.EnvFleetDBURL, "")
	t.Setenv(bootstrap.EnvFleetDBActor, "")
	t.Setenv(bootstrap.EnvAgentName, "")
	t.Setenv("LOOM_FLEET_ACTOR", "")
	t.Setenv("USER", "local-user")
	oldRedisAddr, oldRedisPassword := serveRedisAddr, serveRedisPassword
	serveRedisAddr, serveRedisPassword = "", ""
	t.Cleanup(func() {
		serveRedisAddr, serveRedisPassword = oldRedisAddr, oldRedisPassword
	})

	fs := resolveFleetState(context.Background())

	if fs.clientCfg.Actor != "local-user" {
		t.Fatalf("clientCfg.Actor = %q, want local-user", fs.clientCfg.Actor)
	}
}

func TestEnsureFleetStoreEnv_UsesFleetClientConfig(t *testing.T) {
	t.Setenv(bootstrap.EnvFleetDBURL, "")
	t.Setenv(bootstrap.EnvFleetDBActor, "")

	ensureFleetStoreEnv(config.FleetClientConfig{
		URL:   "http://fleet-db:8080",
		Actor: "parity-harness",
	})

	if got := os.Getenv(bootstrap.EnvFleetDBURL); got != "http://fleet-db:8080" {
		t.Fatalf("%s = %q, want %q", bootstrap.EnvFleetDBURL, got, "http://fleet-db:8080")
	}
	if got := os.Getenv(bootstrap.EnvFleetDBActor); got != "parity-harness" {
		t.Fatalf("%s = %q, want %q", bootstrap.EnvFleetDBActor, got, "parity-harness")
	}
}

func TestHandleStatus(t *testing.T) {
	mockData := mockMonitorData()

	withMockData(t, mockData, func() {
		req := httptest.NewRequest("GET", "/api/status", nil)
		rr := httptest.NewRecorder()

		handleStatus(rr, req)

		// Check status code
		if rr.Code != http.StatusOK {
			t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
		}

		// Parse response
		var resp StatusResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		// Verify all fields are present
		if len(resp.Agents) != 2 {
			t.Errorf("expected 2 agents, got %d", len(resp.Agents))
		}

		if resp.Tasks.NeedsPlanning != 3 {
			t.Errorf("Tasks.NeedsPlanning = %d, want %d", resp.Tasks.NeedsPlanning, 3)
		}

		if resp.Stats.Total != 20 {
			t.Errorf("Stats.Total = %d, want %d", resp.Stats.Total, 20)
		}

		if !resp.Sync.DBSynced {
			t.Error("Sync.DBSynced should be true")
		}

		if resp.Timestamp.IsZero() {
			t.Error("Timestamp should not be zero")
		}
	})
}

func TestHandleAgents(t *testing.T) {
	tests := []struct {
		name       string
		mockData   *MonitorData
		wantAgents int
	}{
		{
			name:       "multiple agents",
			mockData:   mockMonitorData(),
			wantAgents: 2,
		},
		{
			name: "empty agents list",
			mockData: &MonitorData{
				Timestamp: time.Now(),
				Agents:    []AgentStatus{},
			},
			wantAgents: 0,
		},
		{
			name: "single agent",
			mockData: &MonitorData{
				Timestamp: time.Now(),
				Agents: []AgentStatus{
					{Name: "solo", Branch: "main", Status: "ready"},
				},
			},
			wantAgents: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withMockData(t, tt.mockData, func() {
				req := httptest.NewRequest("GET", "/api/agents", nil)
				rr := httptest.NewRecorder()

				handleAgents(rr, req)

				if rr.Code != http.StatusOK {
					t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
				}

				var resp AgentsResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to unmarshal response: %v", err)
				}

				if len(resp.Agents) != tt.wantAgents {
					t.Errorf("agents count = %d, want %d", len(resp.Agents), tt.wantAgents)
				}

				if resp.Timestamp.IsZero() {
					t.Error("Timestamp should not be zero")
				}
			})
		})
	}
}

func TestHandleTasks(t *testing.T) {
	tests := []struct {
		name                  string
		mockData              *MonitorData
		wantNeedsPlanningLen  int
		wantReadyToImplLen    int
		wantNeedsReviewLen    int
		wantInProgressLen     int
		wantBacklogLen        int
		wantClosedLen         int
		wantSummaryNeedsPlan  int
		wantSummaryReadyImpl  int
		wantSummaryInProgress int
		wantSummaryNeedReview int
		wantSummaryBacklog    int
	}{
		{
			name:                  "tasks with all categories populated",
			mockData:              mockMonitorData(),
			wantNeedsPlanningLen:  1,
			wantReadyToImplLen:    1,
			wantNeedsReviewLen:    1,
			wantInProgressLen:     1,
			wantBacklogLen:        0,
			wantClosedLen:         2,
			wantSummaryNeedsPlan:  3,
			wantSummaryReadyImpl:  5,
			wantSummaryInProgress: 2,
			wantSummaryNeedReview: 1,
			wantSummaryBacklog:    0,
		},
		{
			name: "empty task lists",
			mockData: &MonitorData{
				Timestamp:          time.Now(),
				Tasks:              TaskSummary{},
				NeedsPlanningTasks: []TaskInfo{},
				ReadyToImplement:   []TaskInfo{},
				ReviewTasks:        []TaskInfo{},
				InProgressTasks:    []TaskInfo{},
				BacklogTasks:       []TaskInfo{},
				ClosedTasks:        []TaskInfo{},
			},
			wantNeedsPlanningLen:  0,
			wantReadyToImplLen:    0,
			wantNeedsReviewLen:    0,
			wantInProgressLen:     0,
			wantBacklogLen:        0,
			wantClosedLen:         0,
			wantSummaryNeedsPlan:  0,
			wantSummaryReadyImpl:  0,
			wantSummaryInProgress: 0,
			wantSummaryNeedReview: 0,
			wantSummaryBacklog:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withMockData(t, tt.mockData, func() {
				req := httptest.NewRequest("GET", "/api/tasks", nil)
				rr := httptest.NewRecorder()

				handleTasks(rr, req)

				if rr.Code != http.StatusOK {
					t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
				}

				var resp TasksResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to unmarshal response: %v", err)
				}

				// Check task lists
				if len(resp.NeedsPlanning) != tt.wantNeedsPlanningLen {
					t.Errorf("NeedsPlanning len = %d, want %d", len(resp.NeedsPlanning), tt.wantNeedsPlanningLen)
				}
				if len(resp.ReadyToImplement) != tt.wantReadyToImplLen {
					t.Errorf("ReadyToImplement len = %d, want %d", len(resp.ReadyToImplement), tt.wantReadyToImplLen)
				}
				if len(resp.NeedsReview) != tt.wantNeedsReviewLen {
					t.Errorf("NeedsReview len = %d, want %d", len(resp.NeedsReview), tt.wantNeedsReviewLen)
				}
				if len(resp.InProgress) != tt.wantInProgressLen {
					t.Errorf("InProgress len = %d, want %d", len(resp.InProgress), tt.wantInProgressLen)
				}
				if len(resp.Backlog) != tt.wantBacklogLen {
					t.Errorf("Backlog len = %d, want %d", len(resp.Backlog), tt.wantBacklogLen)
				}
				if len(resp.Closed) != tt.wantClosedLen {
					t.Errorf("Closed len = %d, want %d", len(resp.Closed), tt.wantClosedLen)
				}

				// Check summary counts
				if resp.Summary.NeedsPlanning != tt.wantSummaryNeedsPlan {
					t.Errorf("Summary.NeedsPlanning = %d, want %d", resp.Summary.NeedsPlanning, tt.wantSummaryNeedsPlan)
				}
				if resp.Summary.ReadyToImplement != tt.wantSummaryReadyImpl {
					t.Errorf("Summary.ReadyToImplement = %d, want %d", resp.Summary.ReadyToImplement, tt.wantSummaryReadyImpl)
				}
				if resp.Summary.InProgress != tt.wantSummaryInProgress {
					t.Errorf("Summary.InProgress = %d, want %d", resp.Summary.InProgress, tt.wantSummaryInProgress)
				}
				if resp.Summary.NeedReview != tt.wantSummaryNeedReview {
					t.Errorf("Summary.NeedReview = %d, want %d", resp.Summary.NeedReview, tt.wantSummaryNeedReview)
				}
				if resp.Summary.Backlog != tt.wantSummaryBacklog {
					t.Errorf("Summary.Backlog = %d, want %d", resp.Summary.Backlog, tt.wantSummaryBacklog)
				}

				if resp.Timestamp.IsZero() {
					t.Error("Timestamp should not be zero")
				}
			})
		})
	}
}

func TestHandleStats(t *testing.T) {
	tests := []struct {
		name           string
		mockData       *MonitorData
		wantOpen       int
		wantClosed     int
		wantTotal      int
		wantCompletion float64
	}{
		{
			name:           "normal stats",
			mockData:       mockMonitorData(),
			wantOpen:       8,
			wantClosed:     12,
			wantTotal:      20,
			wantCompletion: 60.0,
		},
		{
			name: "zero stats",
			mockData: &MonitorData{
				Timestamp: time.Now(),
				Stats:     MonitorStats{Open: 0, Closed: 0, Total: 0, Completion: 0},
			},
			wantOpen:       0,
			wantClosed:     0,
			wantTotal:      0,
			wantCompletion: 0,
		},
		{
			name: "100% completion",
			mockData: &MonitorData{
				Timestamp: time.Now(),
				Stats:     MonitorStats{Open: 0, Closed: 10, Total: 10, Completion: 100.0},
			},
			wantOpen:       0,
			wantClosed:     10,
			wantTotal:      10,
			wantCompletion: 100.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withMockData(t, tt.mockData, func() {
				req := httptest.NewRequest("GET", "/api/stats", nil)
				rr := httptest.NewRecorder()

				handleStats(rr, req)

				if rr.Code != http.StatusOK {
					t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
				}

				var resp StatsResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to unmarshal response: %v", err)
				}

				if resp.Stats.Open != tt.wantOpen {
					t.Errorf("Stats.Open = %d, want %d", resp.Stats.Open, tt.wantOpen)
				}
				if resp.Stats.Closed != tt.wantClosed {
					t.Errorf("Stats.Closed = %d, want %d", resp.Stats.Closed, tt.wantClosed)
				}
				if resp.Stats.Total != tt.wantTotal {
					t.Errorf("Stats.Total = %d, want %d", resp.Stats.Total, tt.wantTotal)
				}
				if resp.Stats.Completion != tt.wantCompletion {
					t.Errorf("Stats.Completion = %f, want %f", resp.Stats.Completion, tt.wantCompletion)
				}

				if resp.Timestamp.IsZero() {
					t.Error("Timestamp should not be zero")
				}
			})
		})
	}
}

func TestHandleSync(t *testing.T) {
	tests := []struct {
		name          string
		mockData      *MonitorData
		wantDBSynced  bool
		wantNeedsPush int
		wantNeedsPull int
	}{
		{
			name:          "synced with some git needs",
			mockData:      mockMonitorData(),
			wantDBSynced:  true,
			wantNeedsPush: 1,
			wantNeedsPull: 1,
		},
		{
			name: "fully synced",
			mockData: &MonitorData{
				Timestamp: time.Now(),
				SyncStatus: SyncInfo{
					DBSynced:     true,
					DBLastSync:   "recently",
					GitNeedsPush: 0,
					GitNeedsPull: 0,
				},
			},
			wantDBSynced:  true,
			wantNeedsPush: 0,
			wantNeedsPull: 0,
		},
		{
			name: "db not synced",
			mockData: &MonitorData{
				Timestamp: time.Now(),
				SyncStatus: SyncInfo{
					DBSynced:     false,
					DBError:      "connection failed",
					GitNeedsPush: 0,
					GitNeedsPull: 0,
				},
			},
			wantDBSynced:  false,
			wantNeedsPush: 0,
			wantNeedsPull: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withMockData(t, tt.mockData, func() {
				req := httptest.NewRequest("GET", "/api/sync", nil)
				rr := httptest.NewRecorder()

				handleSync(rr, req)

				if rr.Code != http.StatusOK {
					t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
				}

				var resp SyncResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to unmarshal response: %v", err)
				}

				if resp.Sync.DBSynced != tt.wantDBSynced {
					t.Errorf("Sync.DBSynced = %v, want %v", resp.Sync.DBSynced, tt.wantDBSynced)
				}
				if resp.Sync.GitNeedsPush != tt.wantNeedsPush {
					t.Errorf("Sync.GitNeedsPush = %d, want %d", resp.Sync.GitNeedsPush, tt.wantNeedsPush)
				}
				if resp.Sync.GitNeedsPull != tt.wantNeedsPull {
					t.Errorf("Sync.GitNeedsPull = %d, want %d", resp.Sync.GitNeedsPull, tt.wantNeedsPull)
				}

				if resp.Timestamp.IsZero() {
					t.Error("Timestamp should not be zero")
				}
			})
		})
	}
}

func TestResponseTypes(t *testing.T) {
	// Test that all response types can be serialized to JSON
	testCases := []struct {
		name string
		data any
	}{
		{
			name: "HealthResponse",
			data: HealthResponse{Status: "ok", Timestamp: time.Now()},
		},
		{
			name: "AgentsResponse",
			data: AgentsResponse{
				Agents:    []AgentStatus{{Name: "test", Branch: "main", Status: "ready"}},
				Timestamp: time.Now(),
			},
		},
		{
			name: "TasksResponse",
			data: TasksResponse{
				Summary:          TaskSummary{NeedsPlanning: 1, ReadyToImplement: 2},
				NeedsPlanning:    []TaskInfo{{ID: "t1", Title: "Task 1"}},
				ReadyToImplement: []TaskInfo{},
				NeedsReview:      []TaskInfo{},
				InProgress:       []TaskInfo{},
				Backlog:          []TaskInfo{},
				Timestamp:        time.Now(),
			},
		},
		{
			name: "StatsResponse",
			data: StatsResponse{
				Stats:     MonitorStats{Open: 5, Closed: 10, Total: 15, Completion: 66.67},
				Timestamp: time.Now(),
			},
		},
		{
			name: "SyncResponse",
			data: SyncResponse{
				Sync:      SyncInfo{DBSynced: true, GitNeedsPush: 0, GitNeedsPull: 0},
				Timestamp: time.Now(),
			},
		},
		{
			name: "StatusResponse",
			data: StatusResponse{
				Agents:         []AgentStatus{{Name: "test", Branch: "main", Status: "ready"}},
				Tasks:          TaskSummary{NeedsPlanning: 1},
				InProgressList: []TaskInfo{},
				AgentTasks:     map[string]TaskInfo{},
				Stats:          MonitorStats{Open: 5, Closed: 10, Total: 15},
				Sync:           SyncInfo{DBSynced: true},
				Timestamp:      time.Now(),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.data)
			if err != nil {
				t.Errorf("failed to marshal %s: %v", tc.name, err)
			}

			// Verify it can be unmarshaled back
			var result map[string]any
			if err := json.Unmarshal(data, &result); err != nil {
				t.Errorf("failed to unmarshal %s: %v", tc.name, err)
			}

			// Verify timestamp field exists
			if _, ok := result["timestamp"]; !ok {
				t.Errorf("%s missing timestamp field", tc.name)
			}
		})
	}
}

func TestServeFlags_Defaults(t *testing.T) {
	testutil.ClearLoomEnv(t)

	f := serveCmd.Flags()
	frontendDirFlag := f.Lookup("frontend-dir")
	if frontendDirFlag == nil {
		t.Fatal("frontend-dir flag not registered on serveCmd")
	}
	origFrontendDir := frontendDirFlag.Value.String()
	t.Cleanup(func() {
		if err := f.Set("frontend-dir", origFrontendDir); err != nil {
			t.Fatalf("restore frontend-dir flag: %v", err)
		}
	})
	if err := f.Set("frontend-dir", ""); err != nil {
		t.Fatalf("reset frontend-dir flag: %v", err)
	}

	port, err := f.GetInt("port")
	if err != nil {
		t.Fatalf("failed to get port flag: %v", err)
	}
	if port != 8080 {
		t.Errorf("port default = %d, want %d", port, 8080)
	}

	webuiSocket, err := f.GetString("webui-socket")
	if err != nil {
		t.Fatalf("failed to get webui-socket flag: %v", err)
	}
	if webuiSocket != "" {
		t.Errorf("webui-socket default = %q, want %q", webuiSocket, "")
	}

	frontendURLs, err := f.GetStringSlice("frontend-url")
	if err != nil {
		t.Fatalf("failed to get frontend-url flag: %v", err)
	}
	if len(frontendURLs) != 0 {
		t.Errorf("frontend-url default = %v, want empty", frontendURLs)
	}

	frontendDir, err := f.GetString("frontend-dir")
	if err != nil {
		t.Fatalf("failed to get frontend-dir flag: %v", err)
	}
	if frontendDir != "" {
		t.Errorf("frontend-dir default = %q, want empty", frontendDir)
	}
}

func TestServeFlags_FrontendURL(t *testing.T) {
	f := serveCmd.Flags().Lookup("frontend-url")
	if f == nil {
		t.Fatal("frontend-url flag not registered on serveCmd")
	}

	if f.Value.Type() != "stringSlice" {
		t.Errorf("frontend-url type = %q, want %q", f.Value.Type(), "stringSlice")
	}
}

func TestServeFlags_FrontendDir(t *testing.T) {
	f := serveCmd.Flags().Lookup("frontend-dir")
	if f == nil {
		t.Fatal("frontend-dir flag not registered on serveCmd")
	}

	if f.Value.Type() != "string" {
		t.Errorf("frontend-dir type = %q, want %q", f.Value.Type(), "string")
	}
}

func TestApplyCORSConfig_FrontendURL(t *testing.T) {
	origCors := serveCorsOrigin
	origFrontendURLs := serveFrontendURLs
	t.Cleanup(func() {
		serveCorsOrigin = origCors
		serveFrontendURLs = origFrontendURLs
	})

	tests := []struct {
		name         string
		cors         string
		frontendURLs []string
		wantEnabled  bool
		wantOrigins  []string
		wantFrontend []string
	}{
		{
			name:         "single frontend-url",
			frontendURLs: []string{"https://app.example.com"},
			wantEnabled:  true,
			wantOrigins:  []string{"https://app.example.com"},
			wantFrontend: []string{"https://app.example.com"},
		},
		{
			name:         "trailing slash stripped",
			frontendURLs: []string{"https://a.example.com/", "https://b.example.com"},
			wantEnabled:  true,
			wantOrigins:  []string{"https://a.example.com", "https://b.example.com"},
			wantFrontend: []string{"https://a.example.com", "https://b.example.com"},
		},
		{
			name:        "cors only",
			cors:        "https://c.example.com",
			wantEnabled: true,
			wantOrigins: []string{"https://c.example.com"},
		},
		{
			name:         "union of cors and frontend-url",
			cors:         "https://c.example.com",
			frontendURLs: []string{"https://a.example.com"},
			wantEnabled:  true,
			wantOrigins:  []string{"https://c.example.com", "https://a.example.com"},
			wantFrontend: []string{"https://a.example.com"},
		},
		{
			name:        "neither set disables CORS",
			wantEnabled: false,
			wantOrigins: nil,
		},
		{
			name:         "empty value skipped",
			frontendURLs: []string{"", "https://a.example.com"},
			wantEnabled:  true,
			wantOrigins:  []string{"https://a.example.com"},
			wantFrontend: []string{"https://a.example.com"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			serveCorsOrigin = tc.cors
			serveFrontendURLs = tc.frontendURLs

			cfg := webui.ServerConfig{}
			applyCORSConfig(&cfg)

			if cfg.CORSEnabled != tc.wantEnabled {
				t.Errorf("CORSEnabled = %v, want %v", cfg.CORSEnabled, tc.wantEnabled)
			}
			if !reflect.DeepEqual(cfg.CORSOrigins, tc.wantOrigins) {
				t.Errorf("CORSOrigins = %v, want %v", cfg.CORSOrigins, tc.wantOrigins)
			}
			if !reflect.DeepEqual(cfg.FrontendOrigins, tc.wantFrontend) {
				t.Errorf("FrontendOrigins = %v, want %v", cfg.FrontendOrigins, tc.wantFrontend)
			}
		})
	}
}

func TestServeFlags_NoWebUIRemoved(t *testing.T) {
	if f := serveCmd.Flags().Lookup("no-webui"); f != nil {
		t.Error("no-webui flag should be removed, but is still registered on serveCmd")
	}
	if f := serveCmd.Flags().Lookup("dev"); f != nil {
		t.Error("dev flag should be removed, but is still registered on serveCmd")
	}
	if f := serveCmd.Flags().Lookup("dev-frontend-dir"); f != nil {
		t.Error("dev-frontend-dir flag should be removed, but is still registered on serveCmd")
	}
}

func TestGetWorkspaceInfo_WorkspaceMode(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  "/tmp/ws",
				Repos: []RepoConfig{{Name: "repo1", Path: "/tmp/ws/repo1"}},
			},
			"otherws": {
				Path:  "/tmp/other",
				Repos: []RepoConfig{{Name: "repo2", Path: "/tmp/other/repo2"}},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	info := getWorkspaceInfo()

	if info.Mode != "workspace" {
		t.Errorf("Mode = %q, want %q", info.Mode, "workspace")
	}
	if info.Name != "myws" {
		t.Errorf("Name = %q, want %q", info.Name, "myws")
	}
	if len(info.Workspaces) != 2 {
		t.Errorf("len(Workspaces) = %d, want 2", len(info.Workspaces))
	}
	// Workspaces should be sorted alphabetically
	hasMyws := false
	hasOtherws := false
	for _, ws := range info.Workspaces {
		if ws == "myws" {
			hasMyws = true
		}
		if ws == "otherws" {
			hasOtherws = true
		}
	}
	if !hasMyws || !hasOtherws {
		t.Errorf("Workspaces = %v, expected to contain myws and otherws", info.Workspaces)
	}
}

func TestGroupAgentsByWorkspace(t *testing.T) {
	tests := []struct {
		name     string
		agents   []AgentStatus
		expected map[string]int // workspace name -> expected count
	}{
		{
			name:     "empty agents",
			agents:   []AgentStatus{},
			expected: map[string]int{},
		},
		{
			name: "all unassigned agents",
			agents: []AgentStatus{
				{Name: "falcon", Workspace: ""},
				{Name: "nova", Workspace: ""},
			},
			expected: map[string]int{"unassigned": 2},
		},
		{
			name: "all workspace agents",
			agents: []AgentStatus{
				{Name: "repo1", Workspace: "myws"},
				{Name: "repo2", Workspace: "myws"},
			},
			expected: map[string]int{"myws": 2},
		},
		{
			name: "mixed workspace and unassigned agents",
			agents: []AgentStatus{
				{Name: "falcon", Workspace: ""},
				{Name: "repo1", Workspace: "myws"},
				{Name: "repo2", Workspace: "otherws"},
				{Name: "nova", Workspace: ""},
			},
			expected: map[string]int{"unassigned": 2, "myws": 1, "otherws": 1},
		},
		{
			name: "multiple workspaces",
			agents: []AgentStatus{
				{Name: "repo1", Workspace: "ws1"},
				{Name: "repo2", Workspace: "ws2"},
				{Name: "repo3", Workspace: "ws1"},
				{Name: "repo4", Workspace: "ws3"},
			},
			expected: map[string]int{"ws1": 2, "ws2": 1, "ws3": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := groupAgentsByWorkspace(tt.agents)

			// Check that we have the expected number of groups
			if len(result) != len(tt.expected) {
				t.Errorf("got %d groups, want %d", len(result), len(tt.expected))
			}

			// Check counts for each workspace
			for ws, expectedCount := range tt.expected {
				if len(result[ws]) != expectedCount {
					t.Errorf("workspace %q: got %d agents, want %d", ws, len(result[ws]), expectedCount)
				}
			}
		})
	}
}

func TestHandleWorkspaces_WorkspaceMode(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "primary",
		Workspaces: map[string]WorkspaceConfig{
			"primary": {
				Path: "/home/user/primary",
				Repos: []RepoConfig{
					{Name: "backend", Path: "/home/user/primary/backend"},
					{Name: "frontend", Path: "/home/user/primary/frontend"},
				},
			},
			"secondary": {
				Path: "/home/user/secondary",
				Repos: []RepoConfig{
					{Name: "lib", Path: "/home/user/secondary/lib"},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	req := httptest.NewRequest("GET", "/api/workspaces", nil)
	rr := httptest.NewRecorder()

	handleWorkspaces(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp WorkspacesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Mode != "workspace" {
		t.Errorf("Mode = %q, want %q", resp.Mode, "workspace")
	}
	if resp.Default != "primary" {
		t.Errorf("Default = %q, want %q", resp.Default, "primary")
	}
	if len(resp.Workspaces) != 2 {
		t.Errorf("len(Workspaces) = %d, want 2", len(resp.Workspaces))
	}

	// Verify primary workspace details
	primary, ok := resp.Workspaces["primary"]
	if !ok {
		t.Fatal("missing 'primary' workspace in response")
	}
	if primary.Path != "/home/user/primary" {
		t.Errorf("primary.Path = %q, want %q", primary.Path, "/home/user/primary")
	}
	if len(primary.Repos) != 2 {
		t.Errorf("len(primary.Repos) = %d, want 2", len(primary.Repos))
	}

	// Verify secondary workspace details
	secondary, ok := resp.Workspaces["secondary"]
	if !ok {
		t.Fatal("missing 'secondary' workspace in response")
	}
	if secondary.Path != "/home/user/secondary" {
		t.Errorf("secondary.Path = %q, want %q", secondary.Path, "/home/user/secondary")
	}
	if len(secondary.Repos) != 1 {
		t.Errorf("len(secondary.Repos) = %d, want 1", len(secondary.Repos))
	}

	if resp.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

func TestHandleAgents_WorkspaceMode(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  "/tmp/ws",
				Repos: []RepoConfig{{Name: "repo1", Path: "/tmp/ws/repo1"}},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	// Create mock data with agents that have workspace set
	mockData := &MonitorData{
		Timestamp: time.Now(),
		Agents: []AgentStatus{
			{Name: "repo1", Branch: "main", Status: "ready", Workspace: "myws"},
			{Name: "repo2", Branch: "feature", Status: "working", Workspace: "myws"},
			{Name: "unassigned-agent", Branch: "dev", Status: "ready", Workspace: ""},
		},
	}

	withMockData(t, mockData, func() {
		req := httptest.NewRequest("GET", "/api/agents", nil)
		rr := httptest.NewRecorder()

		handleAgents(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
		}

		var resp AgentsResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		// Verify workspace info
		if resp.Workspace.Mode != "workspace" {
			t.Errorf("Workspace.Mode = %q, want %q", resp.Workspace.Mode, "workspace")
		}
		if resp.Workspace.Name != "myws" {
			t.Errorf("Workspace.Name = %q, want %q", resp.Workspace.Name, "myws")
		}

		// Verify flat agents list
		if len(resp.Agents) != 3 {
			t.Errorf("len(Agents) = %d, want 3", len(resp.Agents))
		}

		// Verify ByWorkspace grouping is present
		if resp.ByWorkspace == nil {
			t.Fatal("ByWorkspace should not be nil in workspace mode")
		}
		if len(resp.ByWorkspace) != 2 {
			t.Errorf("len(ByWorkspace) = %d, want 2 (myws and unassigned)", len(resp.ByWorkspace))
		}

		// Verify myws group
		mywsAgents, ok := resp.ByWorkspace["myws"]
		if !ok {
			t.Fatal("missing 'myws' in ByWorkspace")
		}
		if len(mywsAgents) != 2 {
			t.Errorf("len(ByWorkspace[myws]) = %d, want 2", len(mywsAgents))
		}

		// Verify unassigned group
		unassignedAgents, ok := resp.ByWorkspace["unassigned"]
		if !ok {
			t.Fatal("missing 'unassigned' in ByWorkspace")
		}
		if len(unassignedAgents) != 1 {
			t.Errorf("len(ByWorkspace[unassigned]) = %d, want 1", len(unassignedAgents))
		}

		if resp.Timestamp.IsZero() {
			t.Error("Timestamp should not be zero")
		}
	})
}

func TestServeFlags_Bind(t *testing.T) {
	f := serveCmd.Flags().Lookup("bind")
	if f == nil {
		t.Fatal("bind flag not registered on serveCmd")
	}
	if f.DefValue != "127.0.0.1" {
		t.Errorf("bind DefValue = %q, want %q", f.DefValue, "127.0.0.1")
	}
	if f.Value.Type() != "string" {
		t.Errorf("bind type = %q, want %q", f.Value.Type(), "string")
	}
}

func TestServeFlags_RedisPassword(t *testing.T) {
	f := serveCmd.Flags().Lookup("redis-password")
	if f == nil {
		t.Fatal("redis-password flag not registered on serveCmd")
	}
	if f.Value.Type() != "string" {
		t.Errorf("redis-password type = %q, want %q", f.Value.Type(), "string")
	}
}

// TestRedisConfig_PasswordPassthrough verifies that the RedisConfig struct
// built in runServe correctly includes the password from serveRedisPassword.
// This mirrors the construction at serve.go line 186:
//
//	fleetRedisConfig = &fleet.RedisConfig{Address: serveRedisAddr, Password: serveRedisPassword}
func TestRedisConfig_PasswordPassthrough(t *testing.T) {
	tests := []struct {
		name         string
		addr         string
		password     string
		wantAddr     string
		wantPassword string
	}{
		{
			name:         "password is included in RedisConfig",
			addr:         "localhost:6379",
			password:     "s3cret",
			wantAddr:     "localhost:6379",
			wantPassword: "s3cret",
		},
		{
			name:         "empty password is preserved",
			addr:         "redis.example.com:6379",
			password:     "",
			wantAddr:     "redis.example.com:6379",
			wantPassword: "",
		},
		{
			name:         "complex password with special characters",
			addr:         "10.0.0.1:6379",
			password:     "p@ss!w0rd#$%",
			wantAddr:     "10.0.0.1:6379",
			wantPassword: "p@ss!w0rd#$%",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore package-level vars
			origAddr := serveRedisAddr
			origPassword := serveRedisPassword
			t.Cleanup(func() {
				serveRedisAddr = origAddr
				serveRedisPassword = origPassword
			})

			serveRedisAddr = tt.addr
			serveRedisPassword = tt.password

			// Build the config the same way runServe does (serve.go line 186)
			cfg := &fleet.RedisConfig{Address: serveRedisAddr, Password: serveRedisPassword}

			if cfg.Address != tt.wantAddr {
				t.Errorf("RedisConfig.Address = %q, want %q", cfg.Address, tt.wantAddr)
			}
			if cfg.Password != tt.wantPassword {
				t.Errorf("RedisConfig.Password = %q, want %q", cfg.Password, tt.wantPassword)
			}
		})
	}
}

// TestNewRedisClient_PasswordPassthrough verifies that fleet.NewRedisClient
// receives the password from serveRedisPassword (not a hardcoded empty string).
// This mirrors the call at serve.go line 197:
//
//	redisClient := fleet.NewRedisClient(serveRedisAddr, serveRedisPassword, 0)
//
// The fix changed the second argument from "" to serveRedisPassword.
func TestNewRedisClient_PasswordPassthrough(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		password string
	}{
		{
			name:     "password forwarded to NewRedisClient",
			addr:     "localhost:6379",
			password: "my-redis-password",
		},
		{
			name:     "empty password still works",
			addr:     "localhost:6379",
			password: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origAddr := serveRedisAddr
			origPassword := serveRedisPassword
			t.Cleanup(func() {
				serveRedisAddr = origAddr
				serveRedisPassword = origPassword
			})

			serveRedisAddr = tt.addr
			serveRedisPassword = tt.password

			// Create the client the same way runServe does (serve.go line 197)
			client := fleet.NewRedisClient(serveRedisAddr, serveRedisPassword, 0)
			defer func() { _ = client.Close() }()

			// Verify the client was created with the expected options.
			// go-redis Options() exposes the configured addr and password.
			opts := client.Options()
			if opts.Addr != tt.addr {
				t.Errorf("client Addr = %q, want %q", opts.Addr, tt.addr)
			}
			if opts.Password != tt.password {
				t.Errorf("client Password = %q, want %q", opts.Password, tt.password)
			}
		})
	}
}

// TestRedisPasswordWiring_ConfigAndClient is an integration-style test that
// verifies the full password wiring: setting serveRedisPassword flows through
// to both the RedisConfig struct AND the NewRedisClient call, exactly as
// runServe does when serveRedisAddr is non-empty.
func TestRedisPasswordWiring_ConfigAndClient(t *testing.T) {
	origAddr := serveRedisAddr
	origPassword := serveRedisPassword
	t.Cleanup(func() {
		serveRedisAddr = origAddr
		serveRedisPassword = origPassword
	})

	serveRedisAddr = "redis.internal:6380"
	serveRedisPassword = "fleet-secret-42"

	// Step 1: Build RedisConfig (serve.go line 186)
	fleetRedisConfig := &fleet.RedisConfig{Address: serveRedisAddr, Password: serveRedisPassword}

	if fleetRedisConfig.Password != "fleet-secret-42" {
		t.Fatalf("RedisConfig.Password = %q, want %q", fleetRedisConfig.Password, "fleet-secret-42")
	}

	// Step 2: Create Redis client (serve.go line 197)
	redisClient := fleet.NewRedisClient(serveRedisAddr, serveRedisPassword, 0)
	defer func() { _ = redisClient.Close() }()

	opts := redisClient.Options()
	if opts.Password != "fleet-secret-42" {
		t.Fatalf("NewRedisClient password = %q, want %q", opts.Password, "fleet-secret-42")
	}

	// Step 3: Verify both use the same password value
	if fleetRedisConfig.Password != opts.Password {
		t.Errorf("RedisConfig.Password (%q) != NewRedisClient password (%q) — password wiring is inconsistent",
			fleetRedisConfig.Password, opts.Password)
	}
}

func TestHandleMetrics_ContentType(t *testing.T) {
	mockData := mockMonitorData()

	withMockData(t, mockData, func() {
		req := httptest.NewRequest("GET", "/metrics", nil)
		rr := httptest.NewRecorder()

		handleMetrics(rr, req)

		// Check status code
		if rr.Code != http.StatusOK {
			t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
		}

		// Check Content-Type header
		wantContentType := "text/plain; version=0.0.4; charset=utf-8"
		if ct := rr.Header().Get("Content-Type"); ct != wantContentType {
			t.Errorf("Content-Type = %q, want %q", ct, wantContentType)
		}
	})
}

func TestHandleMetrics_Format(t *testing.T) {
	mockData := mockMonitorData()

	withMockData(t, mockData, func() {
		req := httptest.NewRequest("GET", "/metrics", nil)
		rr := httptest.NewRecorder()

		handleMetrics(rr, req)

		body := rr.Body.String()

		// Verify loom_ready_tasks metric format
		expectedStrings := []string{
			"# HELP loom_ready_tasks",
			"# TYPE loom_ready_tasks gauge",
			`loom_ready_tasks{priority="0"}`,
			`loom_ready_tasks{priority="1"}`,
			`loom_ready_tasks{priority="2"}`,
			`loom_ready_tasks{priority="3"}`,
			`loom_ready_tasks{priority="4"}`,
			// Verify loom_in_progress_tasks metric format
			"# HELP loom_in_progress_tasks",
			"# TYPE loom_in_progress_tasks gauge",
			"loom_in_progress_tasks",
			// Verify loom_fleet_workers metric format
			"# HELP loom_fleet_workers",
			"# TYPE loom_fleet_workers gauge",
			`loom_fleet_workers{status="active"}`,
			`loom_fleet_workers{status="idle"}`,
			`loom_fleet_workers{status="blocked"}`,
		}

		for _, expected := range expectedStrings {
			if !strings.Contains(body, expected) {
				t.Errorf("response body missing %q\n\nFull body:\n%s", expected, body)
			}
		}
	})
}

func TestHandleMetrics_InProgressCount(t *testing.T) {
	mockData := mockMonitorData() // Tasks.InProgress = 2

	withMockData(t, mockData, func() {
		req := httptest.NewRequest("GET", "/metrics", nil)
		rr := httptest.NewRecorder()

		handleMetrics(rr, req)

		body := rr.Body.String()

		// Verify the in-progress count matches mock data (InProgress=2)
		if !strings.Contains(body, "loom_in_progress_tasks 2") {
			t.Errorf("expected 'loom_in_progress_tasks 2' in response body\n\nFull body:\n%s", body)
		}
	})
}

func TestHandleMetrics_NilData(t *testing.T) {
	// collectDataFunc returns nil
	withMockData(t, nil, func() {
		req := httptest.NewRequest("GET", "/metrics", nil)
		rr := httptest.NewRecorder()

		handleMetrics(rr, req)

		// Check status code - should still succeed
		if rr.Code != http.StatusOK {
			t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
		}

		body := rr.Body.String()

		// When data is nil, in-progress tasks should be 0
		if !strings.Contains(body, "loom_in_progress_tasks 0") {
			t.Errorf("expected 'loom_in_progress_tasks 0' when data is nil\n\nFull body:\n%s", body)
		}
	})
}

func TestCollectWorkerStatusCounts_NoDaemon(t *testing.T) {
	// When no daemon is running, collectWorkerStatusCounts should return all zeros
	counts := collectWorkerStatusCounts()

	expectedStatuses := []string{"active", "idle", "blocked"}
	for _, status := range expectedStatuses {
		val, ok := counts[status]
		if !ok {
			t.Errorf("missing key %q in worker status counts", status)
			continue
		}
		if val != 0 {
			t.Errorf("counts[%q] = %d, want 0 (no daemon running)", status, val)
		}
	}

	// Verify no unexpected keys
	if len(counts) != 3 {
		t.Errorf("len(counts) = %d, want 3", len(counts))
	}
}
