package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// mockFleetClient implements fleetClaimClient for testing.
type mockFleetClient struct {
	updateFunc func(args *rpc.UpdateArgs) (*rpc.Response, error)
	readyFunc  func(args *rpc.ReadyArgs) (*rpc.Response, error)
}

func (m *mockFleetClient) Update(args *rpc.UpdateArgs) (*rpc.Response, error) {
	if m.updateFunc != nil {
		return m.updateFunc(args)
	}
	return nil, errors.New("updateFunc not implemented")
}

func (m *mockFleetClient) Ready(args *rpc.ReadyArgs) (*rpc.Response, error) {
	if m.readyFunc != nil {
		return m.readyFunc(args)
	}
	return nil, errors.New("readyFunc not implemented")
}

// mockFleetPool implements fleetClaimPoolGetter for testing.
type mockFleetPool struct {
	getFunc func(ctx context.Context) (fleetClaimClient, error)
	putFunc func(client fleetClaimClient)
}

func (m *mockFleetPool) Get(ctx context.Context) (fleetClaimClient, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *mockFleetPool) Put(client fleetClaimClient) {
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

func TestFleetClaim_SuccessSpecificIssue(t *testing.T) {
	issueData, _ := json.Marshal(types.Issue{
		ID:     "test-123",
		Title:  "Test Task",
		Status: "in_progress",
		Labels: []string{"fleet"},
	})

	client := &mockFleetClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			if args.ID != "test-123" {
				t.Errorf("Update called with ID %q, want %q", args.ID, "test-123")
			}
			if !args.Claim {
				t.Error("Update called without Claim flag")
			}
			return &rpc.Response{
				Success: true,
				Data:    issueData,
			}, nil
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool)

	body, _ := json.Marshal(FleetClaimRequest{IssueID: "test-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeSuccessWithData(t, result, "payload")

	// Verify payload contains issue
	payload, ok := result["payload"].(map[string]interface{})
	if !ok {
		t.Fatal("payload is not a map")
	}
	issue, ok := payload["issue"].(map[string]interface{})
	if !ok {
		t.Fatal("payload.issue is not a map")
	}
	if issue["id"] != "test-123" {
		t.Errorf("issue.id = %v, want %q", issue["id"], "test-123")
	}
}

func TestFleetClaim_SuccessFromReady(t *testing.T) {
	readyIssues := []*types.Issue{
		{ID: "task-1", Title: "First Task", Status: "open"},
	}
	readyData, _ := json.Marshal(readyIssues)

	claimedIssueData, _ := json.Marshal(types.Issue{
		ID:     "task-1",
		Title:  "First Task",
		Status: "in_progress",
	})

	client := &mockFleetClient{
		readyFunc: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: true,
				Data:    readyData,
			}, nil
		},
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			if args.ID != "task-1" {
				t.Errorf("Update called with ID %q, want %q", args.ID, "task-1")
			}
			if !args.Claim {
				t.Error("Update called without Claim flag")
			}
			return &rpc.Response{
				Success: true,
				Data:    claimedIssueData,
			}, nil
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeSuccessWithData(t, result, "payload")
}

func TestFleetClaim_NoWork(t *testing.T) {
	emptyIssues := []*types.Issue{}
	emptyData, _ := json.Marshal(emptyIssues)

	client := &mockFleetClient{
		readyFunc: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: true,
				Data:    emptyData,
			}, nil
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestFleetClaim_AlreadyClaimed(t *testing.T) {
	client := &mockFleetClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: false,
				Error:   "already claimed by other-worker",
			}, nil
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool)

	body, _ := json.Marshal(FleetClaimRequest{IssueID: "test-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")
}

func TestFleetClaim_PoolUnavailable(t *testing.T) {
	handler := handleFleetClaimWithPool(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")
}

func TestFleetClaim_Timeout(t *testing.T) {
	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return nil, context.DeadlineExceeded
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want %d", w.Code, http.StatusGatewayTimeout)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")
}

func TestFleetClaim_InvalidBody(t *testing.T) {
	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return &mockFleetClient{}, nil
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", bytes.NewReader([]byte("{invalid json")))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = 14 // Signal that body is present
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")
}

func TestFleetClaim_AllReadyTasksClaimed(t *testing.T) {
	readyIssues := []*types.Issue{
		{ID: "task-1", Title: "First", Status: "open"},
		{ID: "task-2", Title: "Second", Status: "open"},
	}
	readyData, _ := json.Marshal(readyIssues)

	client := &mockFleetClient{
		readyFunc: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: true,
				Data:    readyData,
			}, nil
		},
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			// All claims fail - already claimed by others
			return &rpc.Response{
				Success: false,
				Error:   "already claimed by other-worker",
			}, nil
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// When all tasks are already claimed, return 204 No Content
	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestFleetClaim_SkipsClaimedGetsSecond(t *testing.T) {
	readyIssues := []*types.Issue{
		{ID: "task-1", Title: "First", Status: "open"},
		{ID: "task-2", Title: "Second", Status: "open"},
	}
	readyData, _ := json.Marshal(readyIssues)

	claimedIssueData, _ := json.Marshal(types.Issue{
		ID:     "task-2",
		Title:  "Second",
		Status: "in_progress",
	})

	callCount := 0
	client := &mockFleetClient{
		readyFunc: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: true,
				Data:    readyData,
			}, nil
		},
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			callCount++
			if callCount == 1 {
				// First task already claimed
				return &rpc.Response{
					Success: false,
					Error:   "already claimed by other-worker",
				}, nil
			}
			// Second task succeeds
			return &rpc.Response{
				Success: true,
				Data:    claimedIssueData,
			}, nil
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeSuccessWithData(t, result, "payload")

	payload := result["payload"].(map[string]interface{})
	issue := payload["issue"].(map[string]interface{})
	if issue["id"] != "task-2" {
		t.Errorf("claimed issue id = %v, want %q", issue["id"], "task-2")
	}

	if callCount != 2 {
		t.Errorf("Update called %d times, want 2", callCount)
	}
}

func TestFleetClaim_SpecificIssueNotFound(t *testing.T) {
	client := &mockFleetClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			return nil, errors.New("issue not found: bad-id")
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool)

	body, _ := json.Marshal(FleetClaimRequest{IssueID: "bad-id"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")
}

func TestFleetClaim_ReadyRPCError(t *testing.T) {
	client := &mockFleetClient{
		readyFunc: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			return nil, errors.New("connection lost")
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")
}

func TestFleetClaim_EmptyBodyClaimsFromReady(t *testing.T) {
	// Verify that an empty body (no JSON) triggers the ready-then-claim flow
	readyIssues := []*types.Issue{
		{ID: "ready-1", Title: "Ready Task", Status: "open"},
	}
	readyData, _ := json.Marshal(readyIssues)

	claimedData, _ := json.Marshal(types.Issue{
		ID:     "ready-1",
		Title:  "Ready Task",
		Status: "in_progress",
	})

	readyCalled := false
	client := &mockFleetClient{
		readyFunc: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			readyCalled = true
			if args.Limit != 10 {
				t.Errorf("Ready limit = %d, want 10", args.Limit)
			}
			return &rpc.Response{
				Success: true,
				Data:    readyData,
			}, nil
		},
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: true,
				Data:    claimedData,
			}, nil
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !readyCalled {
		t.Error("Ready was not called for empty body request")
	}
}
