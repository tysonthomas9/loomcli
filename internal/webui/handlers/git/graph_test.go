package git

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
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
							data := `[{"id":"bd-1","title":"Blocked task","status":"blocked","priority":1,"blocked_by_count":2,"blocked_by":["bd-a","bd-b"],"blocked_by_details":[{"id":"bd-a","title":"Blocker A","priority":0},{"id":"bd-b","title":"Blocker B","priority":1}]}]`
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

	req := httptest.NewRequest(http.MethodGet, "/api/blocked?assignee=alice&type=bug&parent_id=bd-root&priority=2&limit=50", nil)
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
	if capturedArgs.ParentID != "bd-root" {
		t.Errorf("ParentID = %q, want %q", capturedArgs.ParentID, "bd-root")
	}
	if capturedArgs.Priority == nil || *capturedArgs.Priority != 2 {
		t.Errorf("Priority = %v, want 2", capturedArgs.Priority)
	}
	if capturedArgs.Limit != 50 {
		t.Errorf("Limit = %d, want 50", capturedArgs.Limit)
	}
}

func TestHandleBlocked_ClientReturnedToPoolOnError(t *testing.T) {
	putCalled := false

	pool := &mockBlockedPool{
		getFunc: func(ctx context.Context) (BlockedClient, error) {
			return &mockBlockedClient{
				blockedFunc: func(args *rpc.BlockedArgs) (*rpc.Response, error) {
					return nil, errors.New("rpc failure")
				},
			}, nil
		},
		putFunc: func(c BlockedClient) {
			putCalled = true
		},
	}

	handler := handleBlockedWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/blocked", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !putCalled {
		t.Error("expected client to be returned to pool via Put, but Put was not called")
	}
}

func TestHandleBlocked_BlockerDetailsInResponse(t *testing.T) {
	data := `[{"id":"bd-1","title":"Task","status":"blocked","priority":1,"blocked_by_count":1,"blocked_by":["bd-x"],"blocked_by_details":[{"id":"bd-x","title":"Blocker X","priority":0}]}]`

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
	if issue.ID != "bd-1" {
		t.Errorf("ID = %q, want %q", issue.ID, "bd-1")
	}
	if issue.BlockedByCount != 1 {
		t.Errorf("BlockedByCount = %d, want 1", issue.BlockedByCount)
	}
	if len(issue.BlockedByDetails) != 1 {
		t.Fatalf("BlockedByDetails length = %d, want 1", len(issue.BlockedByDetails))
	}
	if issue.BlockedByDetails[0].ID != "bd-x" {
		t.Errorf("BlockedByDetails[0].ID = %q, want %q", issue.BlockedByDetails[0].ID, "bd-x")
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
										ID:        "bd-1",
										Title:     "Task A",
										Status:    "open",
										Priority:  1,
										IssueType: "task",
										Labels:    []string{"backend"},
										Dependencies: []rpc.GraphDependency{
											{DependsOnID: "bd-2", Type: "blocks"},
										},
									},
									{
										ID:        "bd-2",
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
			wantDataField: "issues",
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
								ID:         "bd-1",
								Title:      "Frontend",
								Status:     "open",
								Priority:   0,
								IssueType:  "epic",
								Labels:     []string{"ui", "p0"},
								DeferUntil: "2026-04-01",
								DueAt:      "2026-05-01",
								Dependencies: []rpc.GraphDependency{
									{DependsOnID: "bd-2", Type: "blocks"},
									{DependsOnID: "bd-3", Type: "depends_on"},
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
	if len(resp.Issues) != 1 {
		t.Fatalf("issues length = %d, want 1", len(resp.Issues))
	}
	issue := resp.Issues[0]
	if issue.ID != "bd-1" {
		t.Errorf("ID = %q, want %q", issue.ID, "bd-1")
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
	if issue.Dependencies[0].DependsOnID != "bd-2" {
		t.Errorf("Dependencies[0].DependsOnID = %q, want %q", issue.Dependencies[0].DependsOnID, "bd-2")
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
	putCalled := false

	pool := &mockGraphPool{
		getFunc: func(ctx context.Context) (GraphClient, error) {
			return &mockGraphClient{
				getGraphDataFunc: func(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error) {
					return nil, errors.New("rpc failure")
				},
			}, nil
		},
		putFunc: func(c GraphClient) {
			putCalled = true
		},
	}

	handler := handleGraphWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/graph", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !putCalled {
		t.Error("expected client to be returned to pool via Put, but Put was not called")
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
			url:  "/api/blocked?parent_id=bd-root&assignee=bob&type=feature&priority=3&limit=25",
			checkArgs: func(t *testing.T, args *rpc.BlockedArgs) {
				if args.ParentID != "bd-root" {
					t.Errorf("ParentID = %q, want %q", args.ParentID, "bd-root")
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
