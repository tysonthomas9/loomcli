package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/steveyegge/beads/internal/rpc"
	"github.com/steveyegge/beads/internal/types"
)

// Contract test helpers

// assertJSONResponse decodes the response body into a generic map and returns it.
func assertJSONResponse(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	return result
}

// assertEnvelopeSuccess checks that the response has {success: true} and no error field.
func assertEnvelopeSuccess(t *testing.T, body map[string]interface{}) {
	t.Helper()

	// success must be true
	success, ok := body["success"]
	if !ok {
		t.Fatal("missing 'success' field in response")
	}
	if success != true {
		t.Errorf("success = %v, want true", success)
	}

	// error field must be absent (omitempty)
	if errVal, ok := body["error"]; ok {
		if str, isStr := errVal.(string); isStr && str != "" {
			t.Errorf("unexpected 'error' field in success response: %v", errVal)
		}
	}
}

// assertEnvelopeSuccessWithData checks that the response has {success: true, <dataFieldName>: <present>} and no error field.
func assertEnvelopeSuccessWithData(t *testing.T, body map[string]interface{}, dataFieldName string) {
	t.Helper()
	assertEnvelopeSuccess(t, body)

	// data field must be present
	if _, ok := body[dataFieldName]; !ok {
		t.Errorf("missing '%s' field in success response", dataFieldName)
	}
}

// assertEnvelopeError checks that the response has {success: false, error: <string>} and no data field.
func assertEnvelopeError(t *testing.T, body map[string]interface{}, dataFieldName string) {
	t.Helper()

	// success must be false
	success, ok := body["success"]
	if !ok {
		t.Fatal("missing 'success' field in response")
	}
	if success != false {
		t.Errorf("success = %v, want false", success)
	}

	// error field must be present and a string
	errVal, ok := body["error"]
	if !ok {
		t.Fatal("missing 'error' field in error response")
	}
	if _, ok := errVal.(string); !ok {
		t.Errorf("'error' field is %T, want string", errVal)
	}

	// data field must be absent (omitempty)
	if dataVal, ok := body[dataFieldName]; ok && dataVal != nil {
		t.Errorf("unexpected '%s' field in error response: %v", dataFieldName, dataVal)
	}
}

// assertPlainError checks that the response has {error: <string>} without envelope fields.
func assertPlainError(t *testing.T, body map[string]interface{}) {
	t.Helper()

	// error field must be present and a string
	errVal, ok := body["error"]
	if !ok {
		t.Fatal("missing 'error' field in plain error response")
	}
	if _, ok := errVal.(string); !ok {
		t.Errorf("'error' field is %T, want string", errVal)
	}

	// success field must be absent (plain error responses don't use envelope)
	if _, ok := body["success"]; ok {
		t.Error("unexpected 'success' field in plain error response")
	}
}

// --- Mock types for contract tests ---

// contractMockStatsClient implements statsClient for contract tests.
type contractMockStatsClient struct {
	statsFunc func() (*rpc.Response, error)
}

func (m *contractMockStatsClient) Stats() (*rpc.Response, error) {
	return m.statsFunc()
}

// contractMockStatsPool implements statsConnectionGetter.
type contractMockStatsPool struct {
	client *contractMockStatsClient
}

func (p *contractMockStatsPool) Get(ctx context.Context) (statsClient, error) {
	return p.client, nil
}

func (p *contractMockStatsPool) Put(client statsClient) {}

// contractMockGraphClient implements graphClient.
type contractMockGraphClient struct {
	getGraphDataFunc func(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error)
}

func (m *contractMockGraphClient) GetGraphData(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error) {
	return m.getGraphDataFunc(args)
}

// contractMockGraphPool implements graphConnectionGetter.
type contractMockGraphPool struct {
	client *contractMockGraphClient
}

func (p *contractMockGraphPool) Get(ctx context.Context) (graphClient, error) {
	return p.client, nil
}

func (p *contractMockGraphPool) Put(client graphClient) {}

// contractMockBlockedClient implements blockedClient.
type contractMockBlockedClient struct {
	blockedFunc func(args *rpc.BlockedArgs) (*rpc.Response, error)
}

func (m *contractMockBlockedClient) Blocked(args *rpc.BlockedArgs) (*rpc.Response, error) {
	return m.blockedFunc(args)
}

// contractMockBlockedPool implements blockedConnectionGetter.
type contractMockBlockedPool struct {
	client *contractMockBlockedClient
}

func (p *contractMockBlockedPool) Get(ctx context.Context) (blockedClient, error) {
	return p.client, nil
}

func (p *contractMockBlockedPool) Put(client blockedClient) {}

// contractMockCloseClient implements issueCloser.
type contractMockCloseClient struct {
	closeFunc func(args *rpc.CloseArgs) (*rpc.Response, error)
}

func (m *contractMockCloseClient) CloseIssue(args *rpc.CloseArgs) (*rpc.Response, error) {
	return m.closeFunc(args)
}

// contractMockClosePool implements closeConnectionGetter.
type contractMockClosePool struct {
	client *contractMockCloseClient
}

func (p *contractMockClosePool) Get(ctx context.Context) (issueCloser, error) {
	return p.client, nil
}

func (p *contractMockClosePool) Put(client issueCloser) {}

// contractMockCommentClient implements commentAdder.
type contractMockCommentClient struct {
	addCommentFunc func(args *rpc.CommentAddArgs) (*rpc.Response, error)
}

func (m *contractMockCommentClient) AddComment(args *rpc.CommentAddArgs) (*rpc.Response, error) {
	return m.addCommentFunc(args)
}

// contractMockCommentPool implements commentConnectionGetter.
type contractMockCommentPool struct {
	client *contractMockCommentClient
}

func (p *contractMockCommentPool) Get(ctx context.Context) (commentAdder, error) {
	return p.client, nil
}

func (p *contractMockCommentPool) Put(client commentAdder) {}

// contractMockDepClient implements dependencyManager.
type contractMockDepClient struct {
	addFunc    func(args *rpc.DepAddArgs) (*rpc.Response, error)
	removeFunc func(args *rpc.DepRemoveArgs) (*rpc.Response, error)
}

func (m *contractMockDepClient) AddDependency(args *rpc.DepAddArgs) (*rpc.Response, error) {
	return m.addFunc(args)
}

func (m *contractMockDepClient) RemoveDependency(args *rpc.DepRemoveArgs) (*rpc.Response, error) {
	return m.removeFunc(args)
}

// contractMockDepPool implements dependencyConnectionGetter.
type contractMockDepPool struct {
	client *contractMockDepClient
}

func (p *contractMockDepPool) Get(ctx context.Context) (dependencyManager, error) {
	return p.client, nil
}

func (p *contractMockDepPool) Put(client dependencyManager) {}

// contractMockPatchClient implements issueUpdater.
type contractMockPatchClient struct {
	updateFunc func(args *rpc.UpdateArgs) (*rpc.Response, error)
}

func (m *contractMockPatchClient) Update(args *rpc.UpdateArgs) (*rpc.Response, error) {
	return m.updateFunc(args)
}

// contractMockPatchPool implements patchConnectionGetter.
type contractMockPatchPool struct {
	client *contractMockPatchClient
}

func (p *contractMockPatchPool) Get(ctx context.Context) (issueUpdater, error) {
	return p.client, nil
}

func (p *contractMockPatchPool) Put(client issueUpdater) {}

// contractMockCreateClient implements issueCreator.
type contractMockCreateClient struct {
	createFunc func(args *rpc.CreateArgs) (*rpc.Response, error)
}

func (m *contractMockCreateClient) Create(args *rpc.CreateArgs) (*rpc.Response, error) {
	return m.createFunc(args)
}

// contractMockCreatePool implements createConnectionGetter.
type contractMockCreatePool struct {
	client *contractMockCreateClient
}

func (p *contractMockCreatePool) Get(ctx context.Context) (issueCreator, error) {
	return p.client, nil
}

func (p *contractMockCreatePool) Put(client issueCreator) {}

// --- Error pool mocks (return error from Get) ---

type errorStatsPool struct{}

func (p *errorStatsPool) Get(ctx context.Context) (statsClient, error) {
	return nil, errors.New("pool unavailable")
}
func (p *errorStatsPool) Put(client statsClient) {}

type errorGraphPool struct{}

func (p *errorGraphPool) Get(ctx context.Context) (graphClient, error) {
	return nil, errors.New("pool unavailable")
}
func (p *errorGraphPool) Put(client graphClient) {}

type errorBlockedPool struct{}

func (p *errorBlockedPool) Get(ctx context.Context) (blockedClient, error) {
	return nil, errors.New("pool unavailable")
}
func (p *errorBlockedPool) Put(client blockedClient) {}

type errorClosePool struct{}

func (p *errorClosePool) Get(ctx context.Context) (issueCloser, error) {
	return nil, errors.New("pool unavailable")
}
func (p *errorClosePool) Put(client issueCloser) {}

type errorCommentPool struct{}

func (p *errorCommentPool) Get(ctx context.Context) (commentAdder, error) {
	return nil, errors.New("pool unavailable")
}
func (p *errorCommentPool) Put(client commentAdder) {}

type errorDepPool struct{}

func (p *errorDepPool) Get(ctx context.Context) (dependencyManager, error) {
	return nil, errors.New("pool unavailable")
}
func (p *errorDepPool) Put(client dependencyManager) {}

type errorPatchPool struct{}

func (p *errorPatchPool) Get(ctx context.Context) (issueUpdater, error) {
	return nil, errors.New("pool unavailable")
}
func (p *errorPatchPool) Put(client issueUpdater) {}

type errorCreatePool struct{}

func (p *errorCreatePool) Get(ctx context.Context) (issueCreator, error) {
	return nil, errors.New("pool unavailable")
}
func (p *errorCreatePool) Put(client issueCreator) {}

// =============================================================================
// Contract Tests: Success Responses
// =============================================================================

func TestContractEnvelope_SuccessResponses(t *testing.T) {
	t.Parallel()

	// Prepare common test data
	issueJSON, _ := json.Marshal(map[string]interface{}{
		"id": "test-1", "title": "Test Issue", "status": "open",
	})
	statsJSON, _ := json.Marshal(types.Statistics{
		TotalIssues: 10, OpenIssues: 5, ClosedIssues: 5,
	})
	blockedListJSON, _ := json.Marshal([]*types.BlockedIssue{})
	commentJSON, _ := json.Marshal(types.Comment{
		Text: "hello", Author: "web-ui",
	})

	tests := []struct {
		name          string
		handler       http.HandlerFunc
		method        string
		path          string
		body          string
		wantStatus    int
		dataFieldName string // "data" or "issues" for graph
		expectData    bool   // whether data/issues field is expected to be present
	}{
		{
			name: "GET /api/issues/{id} success",
			handler: handleGetIssueWithPool(&mockPool{
				getFunc: func(ctx context.Context) (issueGetter, error) {
					return &mockClient{
						showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
							return &rpc.Response{Success: true, Data: issueJSON}, nil
						},
					}, nil
				},
				putFunc: func(client issueGetter) {},
			}),
			method:        http.MethodGet,
			path:          "/api/issues/test-1",
			wantStatus:    http.StatusOK,
			dataFieldName: "data",
			expectData:    true,
		},
		{
			name: "GET /api/stats success",
			handler: handleStatsWithPool(&contractMockStatsPool{
				client: &contractMockStatsClient{
					statsFunc: func() (*rpc.Response, error) {
						return &rpc.Response{Success: true, Data: statsJSON}, nil
					},
				},
			}),
			method:        http.MethodGet,
			path:          "/api/stats",
			wantStatus:    http.StatusOK,
			dataFieldName: "data",
			expectData:    true,
		},
		{
			name: "GET /api/blocked success (empty list)",
			handler: handleBlockedWithPool(&contractMockBlockedPool{
				client: &contractMockBlockedClient{
					blockedFunc: func(args *rpc.BlockedArgs) (*rpc.Response, error) {
						return &rpc.Response{Success: true, Data: blockedListJSON}, nil
					},
				},
			}),
			method:        http.MethodGet,
			path:          "/api/blocked",
			wantStatus:    http.StatusOK,
			dataFieldName: "data",
			expectData:    false, // empty slice + omitempty = field absent
		},
		{
			name: "GET /api/issues/graph success",
			handler: handleGraphWithPool(&contractMockGraphPool{
				client: &contractMockGraphClient{
					getGraphDataFunc: func(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error) {
						return &rpc.GetGraphDataResponse{
							Issues: []rpc.GraphIssueSummary{
								{ID: "test-1", Title: "Test", Status: "open", Priority: 2, IssueType: "task"},
							},
						}, nil
					},
				},
			}),
			method:        http.MethodGet,
			path:          "/api/issues/graph",
			wantStatus:    http.StatusOK,
			dataFieldName: "issues",
			expectData:    true,
		},
		{
			name: "PATCH /api/issues/{id} success",
			handler: handlePatchIssueWithPool(&contractMockPatchPool{
				client: &contractMockPatchClient{
					updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
						return &rpc.Response{Success: true, Data: issueJSON}, nil
					},
				},
			}),
			method:        http.MethodPatch,
			path:          "/api/issues/test-1",
			body:          `{"title":"Updated"}`,
			wantStatus:    http.StatusOK,
			dataFieldName: "data",
			expectData:    true,
		},
		{
			name: "POST /api/issues/{id}/close success",
			handler: handleCloseIssueWithPool(&contractMockClosePool{
				client: &contractMockCloseClient{
					closeFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
						return &rpc.Response{Success: true, Data: issueJSON}, nil
					},
				},
			}),
			method:        http.MethodPost,
			path:          "/api/issues/test-1/close",
			wantStatus:    http.StatusOK,
			dataFieldName: "data",
			expectData:    true,
		},
		{
			name: "POST /api/issues/{id}/comments success",
			handler: handleAddCommentWithPool(&contractMockCommentPool{
				client: &contractMockCommentClient{
					addCommentFunc: func(args *rpc.CommentAddArgs) (*rpc.Response, error) {
						return &rpc.Response{Success: true, Data: commentJSON}, nil
					},
				},
			}),
			method:        http.MethodPost,
			path:          "/api/issues/test-1/comments",
			body:          `{"text":"hello"}`,
			wantStatus:    http.StatusCreated,
			dataFieldName: "data",
			expectData:    true,
		},
		{
			name: "POST /api/issues/{id}/dependencies success",
			handler: handleAddDependencyWithPool(&contractMockDepPool{
				client: &contractMockDepClient{
					addFunc: func(args *rpc.DepAddArgs) (*rpc.Response, error) {
						return &rpc.Response{Success: true}, nil
					},
				},
			}),
			method:        http.MethodPost,
			path:          "/api/issues/test-1/dependencies",
			body:          `{"depends_on_id":"test-2"}`,
			wantStatus:    http.StatusOK,
			dataFieldName: "data",
			expectData:    false, // dependency success returns Data: nil (omitted)
		},
		{
			name: "DELETE /api/issues/{id}/dependencies/{depId} success",
			handler: handleRemoveDependencyWithPool(&contractMockDepPool{
				client: &contractMockDepClient{
					removeFunc: func(args *rpc.DepRemoveArgs) (*rpc.Response, error) {
						return &rpc.Response{Success: true}, nil
					},
				},
			}),
			method:        http.MethodDelete,
			path:          "/api/issues/test-1/dependencies/test-2",
			wantStatus:    http.StatusOK,
			dataFieldName: "data",
			expectData:    false, // dependency success returns Data: nil (omitted)
		},
		{
			name: "POST /api/issues (create) success",
			handler: handleCreateIssueWithPool(&contractMockCreatePool{
				client: &contractMockCreateClient{
					createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
						return &rpc.Response{Success: true, Data: issueJSON}, nil
					},
				},
			}),
			method:        http.MethodPost,
			path:          "/api/issues",
			body:          `{"title":"New Issue","issue_type":"task","priority":2}`,
			wantStatus:    http.StatusCreated,
			dataFieldName: "data",
			expectData:    true,
		},
	}

	// Note: handleReady, handleListIssues, and handleAPIHealth do not have WithPool
	// variants, so they cannot be tested with mocks here. They use *daemon.ConnectionPool
	// directly. Their envelope shapes are tested indirectly through the response types.
	// The health endpoints have dedicated tests in TestContractEnvelope_HealthEndpoints.

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var bodyReader *bytes.Reader
			if tt.body != "" {
				bodyReader = bytes.NewReader([]byte(tt.body))
			} else {
				bodyReader = bytes.NewReader(nil)
			}

			req := httptest.NewRequest(tt.method, tt.path, bodyReader)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			// Set path values for handlers that need them
			req.SetPathValue("id", "test-1")
			req.SetPathValue("depId", "test-2")

			w := httptest.NewRecorder()
			tt.handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			body := assertJSONResponse(t, w)
			if tt.expectData {
				assertEnvelopeSuccessWithData(t, body, tt.dataFieldName)
			} else {
				assertEnvelopeSuccess(t, body)
			}
		})
	}
}

// =============================================================================
// Contract Tests: Error Responses
// =============================================================================

func TestContractEnvelope_ErrorResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		handler       http.HandlerFunc
		method        string
		path          string
		body          string
		wantStatus    int
		dataFieldName string
		isPlainError  bool // true = {error} only, false = {success, error} envelope
	}{
		// --- Envelope-style errors ({success: false, error: ...}) ---
		{
			name:          "GET /api/stats pool unavailable (envelope)",
			handler:       handleStatsWithPool(&errorStatsPool{}),
			method:        http.MethodGet,
			path:          "/api/stats",
			wantStatus:    http.StatusServiceUnavailable,
			dataFieldName: "data",
			isPlainError:  false,
		},
		{
			name:          "GET /api/stats nil pool (envelope)",
			handler:       handleStatsWithPool(nil),
			method:        http.MethodGet,
			path:          "/api/stats",
			wantStatus:    http.StatusServiceUnavailable,
			dataFieldName: "data",
			isPlainError:  false,
		},
		{
			name:          "GET /api/blocked nil pool (envelope)",
			handler:       handleBlockedWithPool(nil),
			method:        http.MethodGet,
			path:          "/api/blocked",
			wantStatus:    http.StatusServiceUnavailable,
			dataFieldName: "data",
			isPlainError:  false,
		},
		{
			name:          "GET /api/blocked pool unavailable (envelope)",
			handler:       handleBlockedWithPool(&errorBlockedPool{}),
			method:        http.MethodGet,
			path:          "/api/blocked",
			wantStatus:    http.StatusServiceUnavailable,
			dataFieldName: "data",
			isPlainError:  false,
		},
		{
			name:          "GET /api/issues/graph nil pool (envelope)",
			handler:       handleGraphWithPool(nil),
			method:        http.MethodGet,
			path:          "/api/issues/graph",
			wantStatus:    http.StatusServiceUnavailable,
			dataFieldName: "issues",
			isPlainError:  false,
		},
		{
			name:          "GET /api/issues/graph pool unavailable (envelope)",
			handler:       handleGraphWithPool(&errorGraphPool{}),
			method:        http.MethodGet,
			path:          "/api/issues/graph",
			wantStatus:    http.StatusServiceUnavailable,
			dataFieldName: "issues",
			isPlainError:  false,
		},
		{
			name:          "PATCH /api/issues/{id} nil pool (envelope)",
			handler:       handlePatchIssueWithPool(nil),
			method:        http.MethodPatch,
			path:          "/api/issues/test-1",
			body:          `{"title":"x"}`,
			wantStatus:    http.StatusServiceUnavailable,
			dataFieldName: "data",
			isPlainError:  false,
		},
		{
			name:          "POST /api/issues/{id}/comments nil pool (envelope)",
			handler:       handleAddCommentWithPool(nil),
			method:        http.MethodPost,
			path:          "/api/issues/test-1/comments",
			body:          `{"text":"hello"}`,
			wantStatus:    http.StatusServiceUnavailable,
			dataFieldName: "data",
			isPlainError:  false,
		},
		{
			name:          "POST /api/issues/{id}/dependencies nil pool (envelope)",
			handler:       handleAddDependencyWithPool(nil),
			method:        http.MethodPost,
			path:          "/api/issues/test-1/dependencies",
			body:          `{"depends_on_id":"test-2"}`,
			wantStatus:    http.StatusServiceUnavailable,
			dataFieldName: "data",
			isPlainError:  false,
		},
		{
			name:          "DELETE /api/issues/{id}/dependencies/{depId} nil pool (envelope)",
			handler:       handleRemoveDependencyWithPool(nil),
			method:        http.MethodDelete,
			path:          "/api/issues/test-1/dependencies/test-2",
			wantStatus:    http.StatusServiceUnavailable,
			dataFieldName: "data",
			isPlainError:  false,
		},

		// --- Plain-style errors ({error: ...}) from writeErrorResponse ---
		{
			name: "GET /api/issues/{id} not found (plain error)",
			handler: handleGetIssueWithPool(&mockPool{
				getFunc: func(ctx context.Context) (issueGetter, error) {
					return &mockClient{
						showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
							return nil, errors.New("not found")
						},
					}, nil
				},
				putFunc: func(client issueGetter) {},
			}),
			method:       http.MethodGet,
			path:         "/api/issues/nonexistent",
			wantStatus:   http.StatusNotFound,
			isPlainError: true,
		},
		{
			name: "GET /api/issues/{id} pool unavailable (plain error)",
			handler: handleGetIssueWithPool(&mockPool{
				getFunc: func(ctx context.Context) (issueGetter, error) {
					return nil, errors.New("pool unavailable")
				},
			}),
			method:       http.MethodGet,
			path:         "/api/issues/test-1",
			wantStatus:   http.StatusServiceUnavailable,
			isPlainError: true,
		},
		{
			name: "POST /api/issues/{id}/close not found (plain error)",
			handler: handleCloseIssueWithPool(&contractMockClosePool{
				client: &contractMockCloseClient{
					closeFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
						return nil, errors.New("not found")
					},
				},
			}),
			method:       http.MethodPost,
			path:         "/api/issues/nonexistent/close",
			wantStatus:   http.StatusNotFound,
			isPlainError: true,
		},
		{
			name:         "POST /api/issues/{id}/close nil pool (plain error)",
			handler:      handleCloseIssueWithPool(nil),
			method:       http.MethodPost,
			path:         "/api/issues/test-1/close",
			wantStatus:   http.StatusServiceUnavailable,
			isPlainError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var bodyReader *bytes.Reader
			if tt.body != "" {
				bodyReader = bytes.NewReader([]byte(tt.body))
			} else {
				bodyReader = bytes.NewReader(nil)
			}

			req := httptest.NewRequest(tt.method, tt.path, bodyReader)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			req.SetPathValue("id", "test-1")
			req.SetPathValue("depId", "test-2")

			w := httptest.NewRecorder()
			tt.handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			body := assertJSONResponse(t, w)

			if tt.isPlainError {
				assertPlainError(t, body)
			} else {
				assertEnvelopeError(t, body, tt.dataFieldName)
			}
		})
	}
}

// =============================================================================
// Contract Tests: Graph Deviation (issues instead of data)
// =============================================================================

func TestContractEnvelope_GraphDeviation(t *testing.T) {
	t.Parallel()

	// The /api/issues/graph endpoint uses "issues" instead of "data" in its response.
	// This test explicitly documents and validates this deviation.
	handler := handleGraphWithPool(&contractMockGraphPool{
		client: &contractMockGraphClient{
			getGraphDataFunc: func(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error) {
				return &rpc.GetGraphDataResponse{
					Issues: []rpc.GraphIssueSummary{
						{ID: "test-1", Title: "Test", Status: "open", Priority: 2, IssueType: "task"},
					},
				}, nil
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/issues/graph", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	body := assertJSONResponse(t, w)

	// Verify it uses "issues" NOT "data"
	if _, ok := body["issues"]; !ok {
		t.Error("GraphResponse must use 'issues' field, not 'data'")
	}
	if _, ok := body["data"]; ok {
		t.Error("GraphResponse should NOT have 'data' field — it uses 'issues' instead")
	}

	// Verify standard envelope fields
	success, ok := body["success"]
	if !ok {
		t.Fatal("missing 'success' field")
	}
	if success != true {
		t.Errorf("success = %v, want true", success)
	}

	// Verify issues is an array
	issues, ok := body["issues"].([]interface{})
	if !ok {
		t.Fatalf("'issues' field is %T, want array", body["issues"])
	}
	if len(issues) != 1 {
		t.Errorf("len(issues) = %d, want 1", len(issues))
	}
}

// =============================================================================
// Contract Tests: Health Endpoints (custom shapes, not standard envelope)
// =============================================================================

func TestContractEnvelope_HealthEndpoints(t *testing.T) {
	t.Parallel()

	t.Run("GET /health returns plain {status} object", func(t *testing.T) {
		t.Parallel()
		handler := handleHealth(nil)
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}

		body := assertJSONResponse(t, w)

		// Must have "status" field
		if _, ok := body["status"]; !ok {
			t.Error("missing 'status' field")
		}

		// Must NOT use standard envelope
		if _, ok := body["success"]; ok {
			t.Error("/health should not use the standard envelope (no 'success' field)")
		}
		if _, ok := body["data"]; ok {
			t.Error("/health should not have a 'data' field")
		}
		if _, ok := body["error"]; ok {
			t.Error("/health should not have an 'error' field")
		}
	})

	t.Run("GET /api/health returns {status, daemon} object (no pool)", func(t *testing.T) {
		t.Parallel()
		handler := handleAPIHealth(nil)
		req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		// With nil pool, returns degraded status
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
		}

		body := assertJSONResponse(t, w)

		// Must have "status" and "daemon" fields
		if _, ok := body["status"]; !ok {
			t.Error("missing 'status' field")
		}
		if _, ok := body["daemon"]; !ok {
			t.Error("missing 'daemon' field")
		}

		// daemon must be an object with "connected" field
		daemon, ok := body["daemon"].(map[string]interface{})
		if !ok {
			t.Fatalf("'daemon' field is %T, want object", body["daemon"])
		}
		if _, ok := daemon["connected"]; !ok {
			t.Error("missing 'daemon.connected' field")
		}

		// Must NOT use standard envelope
		if _, ok := body["success"]; ok {
			t.Error("/api/health should not use the standard envelope (no 'success' field)")
		}
		if _, ok := body["data"]; ok {
			t.Error("/api/health should not have a 'data' field")
		}
	})
}

// =============================================================================
// Contract Tests: Metrics Endpoint
// =============================================================================

func TestContractEnvelope_Metrics(t *testing.T) {
	t.Parallel()

	t.Run("GET /api/metrics nil hub returns envelope error", func(t *testing.T) {
		t.Parallel()
		handler := handleMetrics(nil)
		req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
		}

		body := assertJSONResponse(t, w)
		assertEnvelopeError(t, body, "data")
	})
}

// =============================================================================
// Contract Tests: IssuesResponse Code field
// =============================================================================

func TestContractEnvelope_IssuesResponseCodeField(t *testing.T) {
	t.Parallel()

	// The IssuesResponse includes an optional "code" field for error categorization.
	// This field should only appear on error responses from list/create endpoints.

	t.Run("POST /api/issues validation error includes code field", func(t *testing.T) {
		t.Parallel()
		handler := handleCreateIssueWithPool(&contractMockCreatePool{
			client: &contractMockCreateClient{
				createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
					return &rpc.Response{Success: true}, nil
				},
			},
		})

		// Send invalid request (missing required fields)
		req := httptest.NewRequest(http.MethodPost, "/api/issues", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}

		body := assertJSONResponse(t, w)

		// Must have success: false
		if body["success"] != false {
			t.Errorf("success = %v, want false", body["success"])
		}

		// Must have error field
		if _, ok := body["error"]; !ok {
			t.Error("missing 'error' field")
		}

		// Must have code field for create errors
		codeVal, ok := body["code"]
		if !ok {
			t.Error("missing 'code' field in IssuesResponse error")
		}
		if code, ok := codeVal.(string); !ok || code == "" {
			t.Errorf("'code' field = %v, want non-empty string", codeVal)
		}
	})
}

// =============================================================================
// Contract Tests: Omitempty behavior
// =============================================================================

func TestContractEnvelope_OmitemptyBehavior(t *testing.T) {
	t.Parallel()

	issueJSON, _ := json.Marshal(map[string]interface{}{
		"id": "test-1", "title": "Test", "status": "open",
	})

	t.Run("success response omits error field entirely", func(t *testing.T) {
		t.Parallel()

		handler := handleStatsWithPool(&contractMockStatsPool{
			client: &contractMockStatsClient{
				statsFunc: func() (*rpc.Response, error) {
					statsJSON, _ := json.Marshal(types.Statistics{TotalIssues: 1})
					return &rpc.Response{Success: true, Data: statsJSON}, nil
				},
			},
		})

		req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		// Decode into raw map to check exact keys present
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatalf("failed to decode raw JSON: %v", err)
		}

		// "error" key should be absent (not present with empty string)
		if _, ok := raw["error"]; ok {
			t.Error("'error' key should be omitted from success response (omitempty), but it was present")
		}
	})

	t.Run("error response omits data field entirely", func(t *testing.T) {
		t.Parallel()

		handler := handleStatsWithPool(nil)

		req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatalf("failed to decode raw JSON: %v", err)
		}

		// "data" key should be absent (not present with null/empty)
		if _, ok := raw["data"]; ok {
			t.Error("'data' key should be omitted from error response (omitempty), but it was present")
		}
	})

	t.Run("success response with data omits code field", func(t *testing.T) {
		t.Parallel()

		handler := handleGetIssueWithPool(&mockPool{
			getFunc: func(ctx context.Context) (issueGetter, error) {
				return &mockClient{
					showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
						return &rpc.Response{Success: true, Data: issueJSON}, nil
					},
				}, nil
			},
			putFunc: func(client issueGetter) {},
		})

		req := httptest.NewRequest(http.MethodGet, "/api/issues/test-1", nil)
		req.SetPathValue("id", "test-1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatalf("failed to decode raw JSON: %v", err)
		}

		// IssuesResponse has an optional "code" field — should be absent on success
		if _, ok := raw["code"]; ok {
			t.Error("'code' key should be omitted from success response (omitempty), but it was present")
		}
	})
}

// =============================================================================
// Contract Tests: Field Type Assertions
// =============================================================================

func TestContractEnvelope_FieldTypes(t *testing.T) {
	t.Parallel()

	issueJSON, _ := json.Marshal(map[string]interface{}{
		"id": "test-1", "title": "Test", "status": "open",
	})

	t.Run("success field is boolean true", func(t *testing.T) {
		t.Parallel()

		handler := handleGetIssueWithPool(&mockPool{
			getFunc: func(ctx context.Context) (issueGetter, error) {
				return &mockClient{
					showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
						return &rpc.Response{Success: true, Data: issueJSON}, nil
					},
				}, nil
			},
			putFunc: func(client issueGetter) {},
		})

		req := httptest.NewRequest(http.MethodGet, "/api/issues/test-1", nil)
		req.SetPathValue("id", "test-1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		body := assertJSONResponse(t, w)

		// success must be a bool
		success := body["success"]
		if _, ok := success.(bool); !ok {
			t.Errorf("'success' field is %T, want bool", success)
		}
	})

	t.Run("success field is boolean false on error", func(t *testing.T) {
		t.Parallel()

		handler := handleStatsWithPool(nil)

		req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		body := assertJSONResponse(t, w)

		success := body["success"]
		if val, ok := success.(bool); !ok {
			t.Errorf("'success' field is %T, want bool", success)
		} else if val != false {
			t.Errorf("success = %v, want false", val)
		}
	})

	t.Run("error field is string type", func(t *testing.T) {
		t.Parallel()

		handler := handleBlockedWithPool(nil)

		req := httptest.NewRequest(http.MethodGet, "/api/blocked", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		body := assertJSONResponse(t, w)

		errVal := body["error"]
		if _, ok := errVal.(string); !ok {
			t.Errorf("'error' field is %T, want string", errVal)
		}
	})

	t.Run("data field is object for single-item responses", func(t *testing.T) {
		t.Parallel()

		handler := handleGetIssueWithPool(&mockPool{
			getFunc: func(ctx context.Context) (issueGetter, error) {
				return &mockClient{
					showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
						return &rpc.Response{Success: true, Data: issueJSON}, nil
					},
				}, nil
			},
			putFunc: func(client issueGetter) {},
		})

		req := httptest.NewRequest(http.MethodGet, "/api/issues/test-1", nil)
		req.SetPathValue("id", "test-1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		body := assertJSONResponse(t, w)

		// For GET /api/issues/{id}, data should be an object (map)
		dataVal := body["data"]
		if _, ok := dataVal.(map[string]interface{}); !ok {
			t.Errorf("'data' field is %T, want object (map)", dataVal)
		}
	})

	t.Run("data field is array for list responses", func(t *testing.T) {
		t.Parallel()

		handler := handleBlockedWithPool(&contractMockBlockedPool{
			client: &contractMockBlockedClient{
				blockedFunc: func(args *rpc.BlockedArgs) (*rpc.Response, error) {
					data, _ := json.Marshal([]*types.BlockedIssue{
						{Issue: types.Issue{ID: "blk-1", Title: "Blocked", Status: "open"}, BlockedByCount: 1, BlockedBy: []string{"dep-1"}},
					})
					return &rpc.Response{Success: true, Data: data}, nil
				},
			},
		})

		req := httptest.NewRequest(http.MethodGet, "/api/blocked", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		body := assertJSONResponse(t, w)

		// For list endpoints, data should be an array
		dataVal := body["data"]
		if _, ok := dataVal.([]interface{}); !ok {
			t.Errorf("'data' field is %T, want array", dataVal)
		}
	})
}
