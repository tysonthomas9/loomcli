package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mockMonitorData creates a sample MonitorData for testing
func mockMonitorData() *MonitorData {
	return &MonitorData{
		Timestamp: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		Agents: []AgentStatus{
			{Name: "falcon", Branch: "falcon", Status: "ready", Ahead: 0, Behind: 0},
			{Name: "nova", Branch: "nova", Status: "working: bd-123 (5m)", Ahead: 2, Behind: 1},
		},
		Tasks: TaskSummary{
			NeedsPlanning:    3,
			ReadyToImplement: 5,
			InProgress:       2,
			NeedReview:       1,
			Blocked:          0,
		},
		NeedsPlanningTasks: []TaskInfo{
			{ID: "bd-001", Title: "Add feature X", Priority: 1},
		},
		ReadyToImplement: []TaskInfo{
			{ID: "bd-002", Title: "Fix bug Y", Priority: 2},
		},
		ReviewTasks: []TaskInfo{
			{ID: "bd-003", Title: "[Need Review] Review task Z", Priority: 1},
		},
		InProgressTasks: []TaskInfo{
			{ID: "bd-123", Title: "Current task", Priority: 1, Status: "in_progress"},
		},
		BlockedTasks: []TaskInfo{},
		AgentTasks: map[string]TaskInfo{
			"nova": {ID: "bd-123", Title: "Current task", Priority: 1, Status: "in_progress"},
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

func TestCorsMiddleware(t *testing.T) {
	tests := []struct {
		name           string
		corsOrigin     string
		requestMethod  string
		wantOrigin     string
		wantStatusCode int
		handlerCalled  bool
	}{
		{
			name:           "empty origin defaults to wildcard",
			corsOrigin:     "",
			requestMethod:  "GET",
			wantOrigin:     "*",
			wantStatusCode: http.StatusOK,
			handlerCalled:  true,
		},
		{
			name:           "specific origin is used",
			corsOrigin:     "http://localhost:3000",
			requestMethod:  "GET",
			wantOrigin:     "http://localhost:3000",
			wantStatusCode: http.StatusOK,
			handlerCalled:  true,
		},
		{
			name:           "OPTIONS preflight returns 200 without calling handler",
			corsOrigin:     "http://example.com",
			requestMethod:  "OPTIONS",
			wantOrigin:     "http://example.com",
			wantStatusCode: http.StatusOK,
			handlerCalled:  false,
		},
		{
			name:           "GET request calls handler",
			corsOrigin:     "*",
			requestMethod:  "GET",
			wantOrigin:     "*",
			wantStatusCode: http.StatusOK,
			handlerCalled:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlerCalled := false
			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerCalled = true
				w.WriteHeader(http.StatusOK)
			})

			middleware := corsMiddleware(tt.corsOrigin, nextHandler)

			req := httptest.NewRequest(tt.requestMethod, "/test", nil)
			rr := httptest.NewRecorder()

			middleware.ServeHTTP(rr, req)

			// Check CORS headers
			origin := rr.Header().Get("Access-Control-Allow-Origin")
			if origin != tt.wantOrigin {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", origin, tt.wantOrigin)
			}

			methods := rr.Header().Get("Access-Control-Allow-Methods")
			if methods != "GET, OPTIONS" {
				t.Errorf("Access-Control-Allow-Methods = %q, want %q", methods, "GET, OPTIONS")
			}

			headers := rr.Header().Get("Access-Control-Allow-Headers")
			if headers != "Content-Type" {
				t.Errorf("Access-Control-Allow-Headers = %q, want %q", headers, "Content-Type")
			}

			// Check status code
			if rr.Code != tt.wantStatusCode {
				t.Errorf("status code = %d, want %d", rr.Code, tt.wantStatusCode)
			}

			// Check if handler was called
			if handlerCalled != tt.handlerCalled {
				t.Errorf("handler called = %v, want %v", handlerCalled, tt.handlerCalled)
			}
		})
	}
}

func TestHandleHealth(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	handleHealth(rr, req)

	// Check status code
	if rr.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
	}

	// Check Content-Type
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	// Parse response
	var resp HealthResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Verify status
	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}

	// Verify timestamp is present and reasonable
	if resp.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}

	// Timestamp should be recent (within last minute)
	if time.Since(resp.Timestamp) > time.Minute {
		t.Error("timestamp should be recent")
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
				BlockedTasks:       []TaskInfo{},
			},
			wantNeedsPlanningLen:  0,
			wantReadyToImplLen:    0,
			wantNeedsReviewLen:    0,
			wantInProgressLen:     0,
			wantBacklogLen:        0,
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
				if resp.Summary.Blocked != tt.wantSummaryBacklog {
					t.Errorf("Summary.Blocked = %d, want %d", resp.Summary.Blocked, tt.wantSummaryBacklog)
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

func TestCorsMiddlewareAllHeaders(t *testing.T) {
	// Verify all expected CORS headers are set correctly
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := corsMiddleware("http://test.local", nextHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	middleware.ServeHTTP(rr, req)

	expectedHeaders := map[string]string{
		"Access-Control-Allow-Origin":  "http://test.local",
		"Access-Control-Allow-Methods": "GET, OPTIONS",
		"Access-Control-Allow-Headers": "Content-Type",
	}

	for header, expected := range expectedHeaders {
		actual := rr.Header().Get(header)
		if actual != expected {
			t.Errorf("%s = %q, want %q", header, actual, expected)
		}
	}
}

func TestServeFlags_Defaults(t *testing.T) {
	// Verify the three new flags exist with correct defaults
	f := serveCmd.Flags()

	webuiPort, err := f.GetInt("webui-port")
	if err != nil {
		t.Fatalf("failed to get webui-port flag: %v", err)
	}
	if webuiPort != 8080 {
		t.Errorf("webui-port default = %d, want %d", webuiPort, 8080)
	}

	webuiSocket, err := f.GetString("webui-socket")
	if err != nil {
		t.Fatalf("failed to get webui-socket flag: %v", err)
	}
	if webuiSocket != "" {
		t.Errorf("webui-socket default = %q, want %q", webuiSocket, "")
	}

	noWebUI, err := f.GetBool("no-webui")
	if err != nil {
		t.Fatalf("failed to get no-webui flag: %v", err)
	}
	if noWebUI != false {
		t.Errorf("no-webui default = %v, want %v", noWebUI, false)
	}
}

func TestServeFlags_WebUIPort(t *testing.T) {
	f := serveCmd.Flags().Lookup("webui-port")
	if f == nil {
		t.Fatal("webui-port flag not registered on serveCmd")
	}

	if f.DefValue != "8080" {
		t.Errorf("webui-port DefValue = %q, want %q", f.DefValue, "8080")
	}

	if f.Value.Type() != "int" {
		t.Errorf("webui-port type = %q, want %q", f.Value.Type(), "int")
	}
}

func TestServeFlags_NoWebUI(t *testing.T) {
	f := serveCmd.Flags().Lookup("no-webui")
	if f == nil {
		t.Fatal("no-webui flag not registered on serveCmd")
	}

	if f.DefValue != "false" {
		t.Errorf("no-webui DefValue = %q, want %q", f.DefValue, "false")
	}

	if f.Value.Type() != "bool" {
		t.Errorf("no-webui type = %q, want %q", f.Value.Type(), "bool")
	}
}

func TestGetWorkspaceInfo_LegacyMode(t *testing.T) {
	// No config file -> legacy mode
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	info := getWorkspaceInfo()

	if info.Mode != "legacy" {
		t.Errorf("Mode = %q, want %q", info.Mode, "legacy")
	}
	if info.Name != "" {
		t.Errorf("Name = %q, want empty", info.Name)
	}
	if info.Workspaces != nil {
		t.Errorf("Workspaces = %v, want nil", info.Workspaces)
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

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

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
			name: "all legacy agents",
			agents: []AgentStatus{
				{Name: "falcon", Workspace: ""},
				{Name: "nova", Workspace: ""},
			},
			expected: map[string]int{"(legacy)": 2},
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
			name: "mixed workspace and legacy agents",
			agents: []AgentStatus{
				{Name: "falcon", Workspace: ""},
				{Name: "repo1", Workspace: "myws"},
				{Name: "repo2", Workspace: "otherws"},
				{Name: "nova", Workspace: ""},
			},
			expected: map[string]int{"(legacy)": 2, "myws": 1, "otherws": 1},
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

func TestHandleWorkspaces_LegacyMode(t *testing.T) {
	// No config file -> legacy mode
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

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

	if resp.Mode != "legacy" {
		t.Errorf("Mode = %q, want %q", resp.Mode, "legacy")
	}
	if resp.Default != "" {
		t.Errorf("Default = %q, want empty", resp.Default)
	}
	if len(resp.Workspaces) != 0 {
		t.Errorf("len(Workspaces) = %d, want 0", len(resp.Workspaces))
	}
	if resp.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
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

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

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

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	// Create mock data with agents that have workspace set
	mockData := &MonitorData{
		Timestamp: time.Now(),
		Agents: []AgentStatus{
			{Name: "repo1", Branch: "main", Status: "ready", Workspace: "myws"},
			{Name: "repo2", Branch: "feature", Status: "working", Workspace: "myws"},
			{Name: "legacy-agent", Branch: "dev", Status: "ready", Workspace: ""},
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
			t.Errorf("len(ByWorkspace) = %d, want 2 (myws and (legacy))", len(resp.ByWorkspace))
		}

		// Verify myws group
		mywsAgents, ok := resp.ByWorkspace["myws"]
		if !ok {
			t.Fatal("missing 'myws' in ByWorkspace")
		}
		if len(mywsAgents) != 2 {
			t.Errorf("len(ByWorkspace[myws]) = %d, want 2", len(mywsAgents))
		}

		// Verify legacy group
		legacyAgents, ok := resp.ByWorkspace["(legacy)"]
		if !ok {
			t.Fatal("missing '(legacy)' in ByWorkspace")
		}
		if len(legacyAgents) != 1 {
			t.Errorf("len(ByWorkspace[(legacy)]) = %d, want 1", len(legacyAgents))
		}

		if resp.Timestamp.IsZero() {
			t.Error("Timestamp should not be zero")
		}
	})
}

func TestHandleAgents_LegacyMode_NoByWorkspace(t *testing.T) {
	// No config file -> legacy mode
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	mockData := &MonitorData{
		Timestamp: time.Now(),
		Agents: []AgentStatus{
			{Name: "falcon", Branch: "main", Status: "ready"},
			{Name: "nova", Branch: "feature", Status: "working"},
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

		// Verify legacy mode
		if resp.Workspace.Mode != "legacy" {
			t.Errorf("Workspace.Mode = %q, want %q", resp.Workspace.Mode, "legacy")
		}

		// Verify flat agents list is present
		if len(resp.Agents) != 2 {
			t.Errorf("len(Agents) = %d, want 2", len(resp.Agents))
		}

		// ByWorkspace should be nil in legacy mode
		if resp.ByWorkspace != nil {
			t.Errorf("ByWorkspace = %v, want nil in legacy mode", resp.ByWorkspace)
		}
	})
}
