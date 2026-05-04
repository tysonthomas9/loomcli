package git

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// ---------------------------------------------------------------------------
// handleBlocked tests
// ---------------------------------------------------------------------------

func TestHandleBlocked(t *testing.T) {
	tests := []struct {
		name           string
		pool           BlockedConnectionGetter
		url            string
		wantStatus     int
		wantSuccess    bool
		wantDataField  string // "data" or "" to skip
		wantErrorField bool
	}{
		{
			name:           "nil pool returns 503",
			pool:           nil,
			url:            "/api/blocked",
			wantStatus:     http.StatusServiceUnavailable,
			wantSuccess:    false,
			wantErrorField: true,
		},
		{
			name: "pool connection error returns 503",
			pool: &mockBlockedPool{
				getFunc: func(ctx context.Context) (BlockedClient, error) {
					return nil, errors.New("pool unavailable")
				},
			},
			url:            "/api/blocked",
			wantStatus:     http.StatusServiceUnavailable,
			wantSuccess:    false,
			wantErrorField: true,
		},
		{
			name: "pool timeout returns 504",
			pool: &mockBlockedPool{
				getFunc: func(ctx context.Context) (BlockedClient, error) {
					return nil, context.DeadlineExceeded
				},
			},
			url:            "/api/blocked",
			wantStatus:     http.StatusGatewayTimeout,
			wantSuccess:    false,
			wantErrorField: true,
		},
		{
			name: "RPC error returns 500",
			pool: &mockBlockedPool{
				getFunc: func(ctx context.Context) (BlockedClient, error) {
					return &mockBlockedClient{
						blockedFunc: func(args *rpc.BlockedArgs) (*rpc.Response, error) {
							return nil, errors.New("connection reset")
						},
					}, nil
				},
				putFunc: func(c BlockedClient) {},
			},
			url:            "/api/blocked",
			wantStatus:     http.StatusInternalServerError,
			wantSuccess:    false,
			wantErrorField: true,
		},
		{
			name: "daemon returns success=false",
			pool: &mockBlockedPool{
				getFunc: func(ctx context.Context) (BlockedClient, error) {
					return &mockBlockedClient{
						blockedFunc: func(args *rpc.BlockedArgs) (*rpc.Response, error) {
							return &rpc.Response{
								Success: false,
								Error:   "database locked",
							}, nil
						},
					}, nil
				},
				putFunc: func(c BlockedClient) {},
			},
			url:            "/api/blocked",
			wantStatus:     http.StatusInternalServerError,
			wantSuccess:    false,
			wantErrorField: true,
		},
		{
			name: "empty blocked list returns success",
			pool: &mockBlockedPool{
				getFunc: func(ctx context.Context) (BlockedClient, error) {
					return &mockBlockedClient{
						blockedFunc: func(args *rpc.BlockedArgs) (*rpc.Response, error) {
							return &rpc.Response{
								Success: true,
								Data:    json.RawMessage(`[]`),
							}, nil
						},
					}, nil
				},
				putFunc: func(c BlockedClient) {},
			},
			url:         "/api/blocked",
			wantStatus:  http.StatusOK,
			wantSuccess: true,
			// data field omitted by omitempty when slice is empty
		},
		{
			name: "returns blocked issues with blocker details",
			pool: &mockBlockedPool{
				getFunc: func(ctx context.Context) (BlockedClient, error) {
					return &mockBlockedClient{
						blockedFunc: func(args *rpc.BlockedArgs) (*rpc.Response, error) {
							data := `[{"id":"loom-1","title":"Blocked task","status":"blocked","priority":1,"blocked_by_count":2,"blocked_by":["loom-a","loom-b"],"blocked_by_details":[{"id":"loom-a","title":"Blocker A","priority":0},{"id":"loom-b","title":"Blocker B","priority":1}]}]`
							return &rpc.Response{
								Success: true,
								Data:    json.RawMessage(data),
							}, nil
						},
					}, nil
				},
				putFunc: func(c BlockedClient) {},
			},
			url:           "/api/blocked",
			wantStatus:    http.StatusOK,
			wantSuccess:   true,
			wantDataField: "data",
		},
		{
			name: "malformed RPC data returns 500",
			pool: &mockBlockedPool{
				getFunc: func(ctx context.Context) (BlockedClient, error) {
					return &mockBlockedClient{
						blockedFunc: func(args *rpc.BlockedArgs) (*rpc.Response, error) {
							return &rpc.Response{
								Success: true,
								Data:    json.RawMessage(`{not valid json`),
							}, nil
						},
					}, nil
				},
				putFunc: func(c BlockedClient) {},
			},
			url:            "/api/blocked",
			wantStatus:     http.StatusInternalServerError,
			wantSuccess:    false,
			wantErrorField: true,
		},
		{
			name: "invalid priority param returns 400",
			pool: &mockBlockedPool{
				getFunc: func(ctx context.Context) (BlockedClient, error) {
					return &mockBlockedClient{}, nil
				},
				putFunc: func(c BlockedClient) {},
			},
			url:            "/api/blocked?priority=abc",
			wantStatus:     http.StatusBadRequest,
			wantSuccess:    false,
			wantErrorField: true,
		},
		{
			name: "priority out of range returns 400",
			pool: &mockBlockedPool{
				getFunc: func(ctx context.Context) (BlockedClient, error) {
					return &mockBlockedClient{}, nil
				},
				putFunc: func(c BlockedClient) {},
			},
			url:            "/api/blocked?priority=9",
			wantStatus:     http.StatusBadRequest,
			wantSuccess:    false,
			wantErrorField: true,
		},
		{
			name: "invalid limit param returns 400",
			pool: &mockBlockedPool{
				getFunc: func(ctx context.Context) (BlockedClient, error) {
					return &mockBlockedClient{}, nil
				},
				putFunc: func(c BlockedClient) {},
			},
			url:            "/api/blocked?limit=notanumber",
			wantStatus:     http.StatusBadRequest,
			wantSuccess:    false,
			wantErrorField: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := handleBlockedWithPool(tt.pool)

			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}

			body := assertJSONResponse(t, rr)

			if got, _ := body["success"].(bool); got != tt.wantSuccess {
				t.Errorf("success = %v, want %v", got, tt.wantSuccess)
			}

			if tt.wantSuccess {
				assertEnvelopeSuccess(t, body)
			}

			if tt.wantDataField != "" {
				assertEnvelopeSuccessWithData(t, body, tt.wantDataField)
			}

			if tt.wantErrorField {
				assertEnvelopeError(t, body, "data")
			}
		})
	}
}

func TestHandleBlocked_QueryParamsPassed(t *testing.T) {
	var capturedArgs *rpc.BlockedArgs

	pool := &mockBlockedPool{
		getFunc: func(ctx context.Context) (BlockedClient, error) {
			return &mockBlockedClient{
				blockedFunc: func(args *rpc.BlockedArgs) (*rpc.Response, error) {
					capturedArgs = args
					return &rpc.Response{
						Success: true,
						Data:    json.RawMessage(`[]`),
					}, nil
				},
			}, nil
		},
		putFunc: func(c BlockedClient) {},
	}

	handler := handleBlockedWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/blocked?assignee=alice&type=bug&parent_id=loom-root&priority=2&limit=50", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	if capturedArgs == nil {
		t.Fatal("expected capturedArgs to be set")
	}
	if capturedArgs.Assignee != "alice" {
		t.Errorf("Assignee = %q, want %q", capturedArgs.Assignee, "alice")
	}
	if capturedArgs.Type != "bug" {
		t.Errorf("Type = %q, want %q", capturedArgs.Type, "bug")
	}
	if capturedArgs.ParentID != "loom-root" {
		t.Errorf("ParentID = %q, want %q", capturedArgs.ParentID, "loom-root")
	}
	if capturedArgs.Priority == nil || *capturedArgs.Priority != 2 {
		t.Errorf("Priority = %v, want 2", capturedArgs.Priority)
	}
	if capturedArgs.Limit != 50 {
		t.Errorf("Limit = %d, want 50", capturedArgs.Limit)
	}
}

func TestHandleBlocked_ClientReturnedToPoolOnError(t *testing.T) {
	discardCalled := false

	pool := &mockBlockedPool{
		getFunc: func(ctx context.Context) (BlockedClient, error) {
			return &mockBlockedClient{
				blockedFunc: func(args *rpc.BlockedArgs) (*rpc.Response, error) {
					return nil, errors.New("rpc failure")
				},
			}, nil
		},
		discardFunc: func(c BlockedClient) {
			discardCalled = true
		},
	}

	handler := handleBlockedWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/blocked", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !discardCalled {
		t.Error("expected Discard to be called on RPC error")
	}
}

func TestHandleBlocked_BlockerDetailsInResponse(t *testing.T) {
	data := `[{"id":"loom-1","title":"Task","status":"blocked","priority":1,"blocked_by_count":1,"blocked_by":["loom-x"],"blocked_by_details":[{"id":"loom-x","title":"Blocker X","priority":0}]}]`

	pool := &mockBlockedPool{
		getFunc: func(ctx context.Context) (BlockedClient, error) {
			return &mockBlockedClient{
				blockedFunc: func(args *rpc.BlockedArgs) (*rpc.Response, error) {
					return &rpc.Response{Success: true, Data: json.RawMessage(data)}, nil
				},
			}, nil
		},
		putFunc: func(c BlockedClient) {},
	}

	handler := handleBlockedWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/blocked", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp BlockedResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	if len(resp.Data) != 1 {
		t.Fatalf("data length = %d, want 1", len(resp.Data))
	}
	issue := resp.Data[0]
	if issue.ID != "loom-1" {
		t.Errorf("ID = %q, want %q", issue.ID, "loom-1")
	}
	if issue.BlockedByCount != 1 {
		t.Errorf("BlockedByCount = %d, want 1", issue.BlockedByCount)
	}
	if len(issue.BlockedByDetails) != 1 {
		t.Fatalf("BlockedByDetails length = %d, want 1", len(issue.BlockedByDetails))
	}
	if issue.BlockedByDetails[0].ID != "loom-x" {
		t.Errorf("BlockedByDetails[0].ID = %q, want %q", issue.BlockedByDetails[0].ID, "loom-x")
	}
}

// ---------------------------------------------------------------------------
// handleGraph tests
// ---------------------------------------------------------------------------

func TestHandleGraph(t *testing.T) {
	tests := []struct {
		name           string
		pool           GraphConnectionGetter
		url            string
		wantStatus     int
		wantSuccess    bool
		wantDataField  string // "issues" or "" to skip
		wantErrorField bool
	}{
		{
			name:           "nil pool returns 503",
			pool:           nil,
			url:            "/api/issues/graph",
			wantStatus:     http.StatusServiceUnavailable,
			wantSuccess:    false,
			wantErrorField: true,
		},
		{
			name: "pool connection error returns 503",
			pool: &mockGraphPool{
				getFunc: func(ctx context.Context) (GraphClient, error) {
					return nil, errors.New("pool unavailable")
				},
			},
			url:            "/api/issues/graph",
			wantStatus:     http.StatusServiceUnavailable,
			wantSuccess:    false,
			wantErrorField: true,
		},
		{
			name: "pool timeout returns 504",
			pool: &mockGraphPool{
				getFunc: func(ctx context.Context) (GraphClient, error) {
					return nil, context.DeadlineExceeded
				},
			},
			url:            "/api/issues/graph",
			wantStatus:     http.StatusGatewayTimeout,
			wantSuccess:    false,
			wantErrorField: true,
		},
		{
			name: "invalid status param returns 400",
			pool: &mockGraphPool{
				getFunc: func(ctx context.Context) (GraphClient, error) {
					return &mockGraphClient{}, nil
				},
				putFunc: func(c GraphClient) {},
			},
			url:            "/api/issues/graph?status=invalid",
			wantStatus:     http.StatusBadRequest,
			wantSuccess:    false,
			wantErrorField: true,
		},
		{
			name: "RPC error returns 500",
			pool: &mockGraphPool{
				getFunc: func(ctx context.Context) (GraphClient, error) {
					return &mockGraphClient{
						getGraphDataFunc: func(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error) {
							return nil, errors.New("connection reset")
						},
					}, nil
				},
				putFunc: func(c GraphClient) {},
			},
			url:            "/api/issues/graph",
			wantStatus:     http.StatusInternalServerError,
			wantSuccess:    false,
			wantErrorField: true,
		},
		{
			name: "empty graph returns success",
			pool: &mockGraphPool{
				getFunc: func(ctx context.Context) (GraphClient, error) {
					return &mockGraphClient{
						getGraphDataFunc: func(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error) {
							return &rpc.GetGraphDataResponse{
								Issues: []rpc.GraphIssueSummary{},
							}, nil
						},
					}, nil
				},
				putFunc: func(c GraphClient) {},
			},
			url:         "/api/issues/graph",
			wantStatus:  http.StatusOK,
			wantSuccess: true,
			// issues field omitted by omitempty when slice is empty
		},
		{
			name: "returns graph with dependencies",
			pool: &mockGraphPool{
				getFunc: func(ctx context.Context) (GraphClient, error) {
					return &mockGraphClient{
						getGraphDataFunc: func(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error) {
							return &rpc.GetGraphDataResponse{
								Issues: []rpc.GraphIssueSummary{
									{
										ID:        "loom-1",
										Title:     "Task A",
										Status:    "open",
										Priority:  1,
										IssueType: "task",
										Labels:    []string{"backend"},
										Dependencies: []rpc.GraphDependency{
											{DependsOnID: "loom-2", Type: "blocks"},
										},
									},
									{
										ID:        "loom-2",
										Title:     "Task B",
										Status:    "open",
										Priority:  2,
										IssueType: "task",
									},
								},
							}, nil
						},
					}, nil
				},
				putFunc: func(c GraphClient) {},
			},
			url:           "/api/issues/graph",
			wantStatus:    http.StatusOK,
			wantSuccess:   true,
			wantDataField: "data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := handleGraphWithPool(tt.pool)

			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}

			body := assertJSONResponse(t, rr)

			if got, _ := body["success"].(bool); got != tt.wantSuccess {
				t.Errorf("success = %v, want %v", got, tt.wantSuccess)
			}

			if tt.wantSuccess {
				assertEnvelopeSuccess(t, body)
			}

			if tt.wantDataField != "" {
				assertEnvelopeSuccessWithData(t, body, tt.wantDataField)
			}

			if tt.wantErrorField {
				assertEnvelopeError(t, body, "issues")
			}
		})
	}
}

func TestHandleGraph_ResponseStructure(t *testing.T) {
	pool := &mockGraphPool{
		getFunc: func(ctx context.Context) (GraphClient, error) {
			return &mockGraphClient{
				getGraphDataFunc: func(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error) {
					return &rpc.GetGraphDataResponse{
						Issues: []rpc.GraphIssueSummary{
							{
								ID:         "loom-1",
								Title:      "Frontend",
								Status:     "open",
								Priority:   0,
								IssueType:  "epic",
								Labels:     []string{"ui", "p0"},
								DeferUntil: "2026-04-01",
								DueAt:      "2026-05-01",
								Dependencies: []rpc.GraphDependency{
									{DependsOnID: "loom-2", Type: "blocks"},
									{DependsOnID: "loom-3", Type: "depends_on"},
								},
							},
						},
					}, nil
				},
			}, nil
		},
		putFunc: func(c GraphClient) {},
	}

	handler := handleGraphWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/graph", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp GraphResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	if len(resp.Data) != 1 {
		t.Fatalf("issues length = %d, want 1", len(resp.Data))
	}
	issue := resp.Data[0]
	if issue.ID != "loom-1" {
		t.Errorf("ID = %q, want %q", issue.ID, "loom-1")
	}
	if issue.Title != "Frontend" {
		t.Errorf("Title = %q, want %q", issue.Title, "Frontend")
	}
	if issue.Status != "open" {
		t.Errorf("Status = %q, want %q", issue.Status, "open")
	}
	if issue.Priority != 0 {
		t.Errorf("Priority = %d, want 0", issue.Priority)
	}
	if issue.IssueType != "epic" {
		t.Errorf("IssueType = %q, want %q", issue.IssueType, "epic")
	}
	if len(issue.Labels) != 2 {
		t.Fatalf("Labels length = %d, want 2", len(issue.Labels))
	}
	if issue.DeferUntil != "2026-04-01" {
		t.Errorf("DeferUntil = %q, want %q", issue.DeferUntil, "2026-04-01")
	}
	if issue.DueAt != "2026-05-01" {
		t.Errorf("DueAt = %q, want %q", issue.DueAt, "2026-05-01")
	}
	if len(issue.Dependencies) != 2 {
		t.Fatalf("Dependencies length = %d, want 2", len(issue.Dependencies))
	}
	if issue.Dependencies[0].DependsOnID != "loom-2" {
		t.Errorf("Dependencies[0].DependsOnID = %q, want %q", issue.Dependencies[0].DependsOnID, "loom-2")
	}
	if issue.Dependencies[0].Type != "blocks" {
		t.Errorf("Dependencies[0].Type = %q, want %q", issue.Dependencies[0].Type, "blocks")
	}
}

func TestHandleGraph_StatusFilterArgs(t *testing.T) {
	tests := []struct {
		name              string
		url               string
		wantStatus        string
		wantExcludeStatus []string
	}{
		{
			name:              "default (all) excludes tombstone",
			url:               "/api/issues/graph",
			wantExcludeStatus: []string{"tombstone"},
		},
		{
			name:              "status=open excludes closed and tombstone",
			url:               "/api/issues/graph?status=open",
			wantExcludeStatus: []string{"closed", "tombstone"},
		},
		{
			name:       "status=closed sets Status field",
			url:        "/api/issues/graph?status=closed",
			wantStatus: "closed",
		},
		{
			name:              "include_closed=false with status=all excludes closed and tombstone",
			url:               "/api/issues/graph?status=all&include_closed=false",
			wantExcludeStatus: []string{"tombstone", "closed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedArgs *rpc.GetGraphDataArgs

			pool := &mockGraphPool{
				getFunc: func(ctx context.Context) (GraphClient, error) {
					return &mockGraphClient{
						getGraphDataFunc: func(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error) {
							capturedArgs = args
							return &rpc.GetGraphDataResponse{
								Issues: []rpc.GraphIssueSummary{},
							}, nil
						},
					}, nil
				},
				putFunc: func(c GraphClient) {},
			}

			handler := handleGraphWithPool(pool)

			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rr.Code)
			}
			if capturedArgs == nil {
				t.Fatal("expected capturedArgs to be set")
			}
			if tt.wantStatus != "" && capturedArgs.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", capturedArgs.Status, tt.wantStatus)
			}
			if tt.wantExcludeStatus != nil {
				if len(capturedArgs.ExcludeStatus) != len(tt.wantExcludeStatus) {
					t.Fatalf("ExcludeStatus = %v, want %v", capturedArgs.ExcludeStatus, tt.wantExcludeStatus)
				}
				for i, v := range tt.wantExcludeStatus {
					if capturedArgs.ExcludeStatus[i] != v {
						t.Errorf("ExcludeStatus[%d] = %q, want %q", i, capturedArgs.ExcludeStatus[i], v)
					}
				}
			}
		})
	}
}

func TestHandleGraph_ClientReturnedToPoolOnRPCError(t *testing.T) {
	discardCalled := false

	pool := &mockGraphPool{
		getFunc: func(ctx context.Context) (GraphClient, error) {
			return &mockGraphClient{
				getGraphDataFunc: func(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error) {
					return nil, errors.New("rpc failure")
				},
			}, nil
		},
		discardFunc: func(c GraphClient) {
			discardCalled = true
		},
	}

	handler := handleGraphWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/graph", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !discardCalled {
		t.Error("expected Discard to be called on RPC error")
	}
}

// ---------------------------------------------------------------------------
// parseBlockedParams tests
// ---------------------------------------------------------------------------

func TestParseBlockedParams(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantErr   bool
		checkArgs func(t *testing.T, args *rpc.BlockedArgs)
	}{
		{
			name: "no params returns defaults",
			url:  "/api/blocked",
			checkArgs: func(t *testing.T, args *rpc.BlockedArgs) {
				if args.ParentID != "" {
					t.Errorf("ParentID = %q, want empty", args.ParentID)
				}
				if args.Assignee != "" {
					t.Errorf("Assignee = %q, want empty", args.Assignee)
				}
				if args.Priority != nil {
					t.Errorf("Priority = %v, want nil", args.Priority)
				}
				if args.Limit != 0 {
					t.Errorf("Limit = %d, want 0", args.Limit)
				}
			},
		},
		{
			name:    "invalid priority (not a number)",
			url:     "/api/blocked?priority=high",
			wantErr: true,
		},
		{
			name:    "priority out of range (negative)",
			url:     "/api/blocked?priority=-1",
			wantErr: true,
		},
		{
			name:    "priority out of range (too high)",
			url:     "/api/blocked?priority=5",
			wantErr: true,
		},
		{
			name:    "negative limit",
			url:     "/api/blocked?limit=-10",
			wantErr: true,
		},
		{
			name: "limit exceeding MaxListLimit is capped",
			url:  "/api/blocked?limit=5000",
			checkArgs: func(t *testing.T, args *rpc.BlockedArgs) {
				if args.Limit != MaxListLimit {
					t.Errorf("Limit = %d, want %d (capped at MaxListLimit)", args.Limit, MaxListLimit)
				}
			},
		},
		{
			name: "all valid params",
			url:  "/api/blocked?parent_id=loom-root&assignee=bob&type=feature&priority=3&limit=25",
			checkArgs: func(t *testing.T, args *rpc.BlockedArgs) {
				if args.ParentID != "loom-root" {
					t.Errorf("ParentID = %q, want %q", args.ParentID, "loom-root")
				}
				if args.Assignee != "bob" {
					t.Errorf("Assignee = %q, want %q", args.Assignee, "bob")
				}
				if args.Type != "feature" {
					t.Errorf("Type = %q, want %q", args.Type, "feature")
				}
				if args.Priority == nil || *args.Priority != 3 {
					t.Errorf("Priority = %v, want 3", args.Priority)
				}
				if args.Limit != 25 {
					t.Errorf("Limit = %d, want 25", args.Limit)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			args, err := parseBlockedParams(req)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.checkArgs != nil {
				tt.checkArgs(t, args)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseGraphParams tests
// ---------------------------------------------------------------------------

func TestParseGraphParams_TableDriven(t *testing.T) {
	tests := []struct {
		name              string
		url               string
		wantStatus        string
		wantIncludeClosed bool
		wantSourceRepos   []string
	}{
		{
			name:              "defaults",
			url:               "/api/issues/graph",
			wantStatus:        "all",
			wantIncludeClosed: true,
			wantSourceRepos:   nil,
		},
		{
			name:              "status=open",
			url:               "/api/issues/graph?status=open",
			wantStatus:        "open",
			wantIncludeClosed: true,
		},
		{
			name:              "include_closed=false",
			url:               "/api/issues/graph?include_closed=false",
			wantStatus:        "all",
			wantIncludeClosed: false,
		},
		{
			name:              "source_repos param",
			url:               "/api/issues/graph?source_repos=repo-a,repo-b",
			wantStatus:        "all",
			wantIncludeClosed: true,
			wantSourceRepos:   []string{"repo-a", "repo-b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			status, includeClosed, sourceRepos := parseGraphParams(req)

			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
			if includeClosed != tt.wantIncludeClosed {
				t.Errorf("includeClosed = %v, want %v", includeClosed, tt.wantIncludeClosed)
			}
			if tt.wantSourceRepos != nil {
				if len(sourceRepos) != len(tt.wantSourceRepos) {
					t.Fatalf("sourceRepos = %v, want %v", sourceRepos, tt.wantSourceRepos)
				}
				for i, v := range tt.wantSourceRepos {
					if sourceRepos[i] != v {
						t.Errorf("sourceRepos[%d] = %q, want %q", i, sourceRepos[i], v)
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Backend tests — HandleGraphWithBackend
// ---------------------------------------------------------------------------

func TestHandleGraph_BackendWhenNoPool(t *testing.T) {
	be := &stubGraphBackend{
		list: []backend.IssueData{
			{ID: "PARITY-1", Title: "Child A", Status: "open", Priority: 1, IssueType: "bug", Parent: "EPIC-1"},
			{ID: "EPIC-1", Title: "Epic", Status: "open", Priority: 0, IssueType: "epic"},
		},
		details: map[string]*backend.IssueDetailData{
			"PARITY-1": {
				IssueData: backend.IssueData{ID: "PARITY-1"},
				Dependencies: []backend.DependencyData{
					{IssueID: "PARITY-1", DependsOnID: "BLOCKER-1", Type: "blocks"},
				},
			},
			"EPIC-1": {IssueData: backend.IssueData{ID: "EPIC-1"}},
		},
	}
	handler := HandleGraphWithBackend(nil, func(_ context.Context) backend.IssueBackend { return be })

	req := httptest.NewRequest(http.MethodGet, "/api/issues/graph", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp GraphResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true; error=%q", resp.Error)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("issues = %d, want 2", len(resp.Data))
	}
	var found *GraphIssue
	for _, gi := range resp.Data {
		if gi.ID == "PARITY-1" {
			found = gi
		}
	}
	if found == nil {
		t.Fatal("missing PARITY-1 node")
	}
	// Should contain BOTH the backend-reported "blocks" dependency AND the
	// synthesized parent-child edge (Parent field).
	sawBlocker, sawParent := false, false
	for _, dep := range found.Dependencies {
		if dep.DependsOnID == "BLOCKER-1" && dep.Type == "blocks" {
			sawBlocker = true
		}
		if dep.DependsOnID == "EPIC-1" && dep.Type == "parent-child" {
			sawParent = true
		}
	}
	if !sawBlocker {
		t.Error("expected blocks dependency in child node")
	}
	if !sawParent {
		t.Error("expected synthesized parent-child edge to EPIC-1")
	}
}

func TestHandleGraph_BackendStatusFilter(t *testing.T) {
	be := &stubGraphBackend{
		list: []backend.IssueData{
			{ID: "OPEN-1", Status: "open"},
			{ID: "CLOSED-1", Status: "closed"},
			{ID: "TOMB-1", Status: "tombstone"},
		},
		details: map[string]*backend.IssueDetailData{},
	}
	handler := HandleGraphWithBackend(nil, func(_ context.Context) backend.IssueBackend { return be })

	req := httptest.NewRequest(http.MethodGet, "/api/issues/graph?status=open", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var resp GraphResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != "OPEN-1" {
		ids := make([]string, 0, len(resp.Data))
		for _, gi := range resp.Data {
			ids = append(ids, gi.ID)
		}
		t.Errorf("status=open returned IDs %v; want [OPEN-1]", ids)
	}
}

func TestHandleGraph_NoPoolNoBackendReturns503(t *testing.T) {
	handler := HandleGraphWithBackend(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/issues/graph", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Backend tests — HandleBlockedWithBackend
// ---------------------------------------------------------------------------

// stubBlockedBackend implements backend.IssueBackend with just enough surface
// to exercise the HandleBlockedWithBackend path in unit tests. Only
// Blocked is functional; every other method returns a sentinel error so
// unintended use shows up as a test failure rather than silent success.
type stubBlockedBackend struct {
	*stubGraphBackend
	blocked []backend.IssueData
	err     error
}

func (s *stubBlockedBackend) Blocked(_ context.Context, _ backend.BlockedOpts) ([]backend.IssueData, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.blocked, nil
}

// errorDaemonPool implements daemon.Pool and returns a fixed error from Get.
type errorDaemonPool struct{ err error }

func (p *errorDaemonPool) Get(_ context.Context) (*rpc.Client, error) { return nil, p.err }
func (p *errorDaemonPool) Put(_ *rpc.Client)                          {}
func (p *errorDaemonPool) PutAfterError(_ *rpc.Client)                {}
func (p *errorDaemonPool) Discard(_ *rpc.Client)                      {}
func (p *errorDaemonPool) Stats() daemon.PoolStats                    { return daemon.PoolStats{} }
func (p *errorDaemonPool) Close() error                               { return nil }

func TestHandleBlocked_PoolErrorDoesNotUseBackend(t *testing.T) {
	dp := &errorDaemonPool{err: errors.New("pool unavailable")}
	be := &stubBlockedBackend{
		stubGraphBackend: &stubGraphBackend{},
		blocked: []backend.IssueData{
			{ID: "BE-1", Title: "Via backend", Status: "blocked", Priority: 1},
		},
	}
	handler := HandleBlockedWithBackend(dp, func(_ context.Context) backend.IssueBackend { return be })

	req := httptest.NewRequest(http.MethodGet, "/api/blocked", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleBlocked_PoolPathPreservedWhenBackendPresent(t *testing.T) {
	// When backendFn is non-nil but a daemon pool is configured, the pool path
	// remains authoritative and client errors are surfaced directly.
	dp := &errorDaemonPool{err: errors.New("should not be invoked")}
	be := &stubBlockedBackend{
		stubGraphBackend: &stubGraphBackend{},
		blocked:          []backend.IssueData{{ID: "SHOULD-NOT-APPEAR"}},
	}
	handler := HandleBlockedWithBackend(dp, func(_ context.Context) backend.IssueBackend { return be })

	req := httptest.NewRequest(http.MethodGet, "/api/blocked?priority=abc", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (client-error preserved); body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleBlocked_BackendOnlyWhenNoPool(t *testing.T) {
	be := &stubBlockedBackend{
		stubGraphBackend: &stubGraphBackend{},
		blocked: []backend.IssueData{
			{ID: "FLEET-1", Title: "fleet-only"},
		},
	}
	handler := HandleBlockedWithBackend(nil, func(_ context.Context) backend.IssueBackend { return be })

	req := httptest.NewRequest(http.MethodGet, "/api/blocked", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Success bool                `json:"success"`
		Data    []backend.IssueData `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !env.Success || len(env.Data) != 1 || env.Data[0].ID != "FLEET-1" {
		t.Errorf("expected fleet-only data (FLEET-1), got %+v", env)
	}
}

func TestHandleBlocked_NoPoolNoBackendReturns503(t *testing.T) {
	handler := HandleBlockedWithBackend(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/blocked", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}
