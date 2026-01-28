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
		wantBlockedLen        int
		wantSummaryNeedsPlan  int
		wantSummaryReadyImpl  int
		wantSummaryInProgress int
		wantSummaryNeedReview int
		wantSummaryBlocked    int
	}{
		{
			name:                  "tasks with all categories populated",
			mockData:              mockMonitorData(),
			wantNeedsPlanningLen:  1,
			wantReadyToImplLen:    1,
			wantNeedsReviewLen:    1,
			wantInProgressLen:     1,
			wantBlockedLen:        0,
			wantSummaryNeedsPlan:  3,
			wantSummaryReadyImpl:  5,
			wantSummaryInProgress: 2,
			wantSummaryNeedReview: 1,
			wantSummaryBlocked:    0,
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
			wantBlockedLen:        0,
			wantSummaryNeedsPlan:  0,
			wantSummaryReadyImpl:  0,
			wantSummaryInProgress: 0,
			wantSummaryNeedReview: 0,
			wantSummaryBlocked:    0,
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
				if len(resp.Blocked) != tt.wantBlockedLen {
					t.Errorf("Blocked len = %d, want %d", len(resp.Blocked), tt.wantBlockedLen)
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
				if resp.Summary.Blocked != tt.wantSummaryBlocked {
					t.Errorf("Summary.Blocked = %d, want %d", resp.Summary.Blocked, tt.wantSummaryBlocked)
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
				Blocked:          []TaskInfo{},
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
