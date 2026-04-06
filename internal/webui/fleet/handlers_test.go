package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

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

	handler := handleFleetClaimWithPool(pool, nil)

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

	handler := handleFleetClaimWithPool(pool, nil)

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

	handler := handleFleetClaimWithPool(pool, nil)

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

	handler := handleFleetClaimWithPool(pool, nil)

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
	handler := handleFleetClaimWithPool(nil, nil)

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

	handler := handleFleetClaimWithPool(pool, nil)

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

	handler := handleFleetClaimWithPool(pool, nil)

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

	handler := handleFleetClaimWithPool(pool, nil)

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

	handler := handleFleetClaimWithPool(pool, nil)

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

	handler := handleFleetClaimWithPool(pool, nil)

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

	handler := handleFleetClaimWithPool(pool, nil)

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

	handler := handleFleetClaimWithPool(pool, nil)

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

// =============================================================================
// Fleet Registration Handler Tests
// =============================================================================

// mockWorkerRegistrar implements workerRegistrar for testing.
type mockWorkerRegistrar struct {
	registerFunc func(ctx context.Context, worker *Worker) error
}

func (m *mockWorkerRegistrar) RegisterWorker(ctx context.Context, worker *Worker) error {
	if m.registerFunc != nil {
		return m.registerFunc(ctx, worker)
	}
	return nil
}

// testTokenConfig returns a TokenConfig suitable for testing.
func testTokenConfig() *TokenConfig {
	return &TokenConfig{
		SigningKey: []byte("test-secret-key-for-jwt-signing!"),
		Expiry:     time.Hour,
	}
}

// testFleetRegCfg returns a RegisterConfig with a known API key for testing.
const testFleetAPIKey = "test-fleet-api-key-secret"

func testFleetRegCfg() *RegisterConfig {
	return &RegisterConfig{
		APIKey: testFleetAPIKey,
	}
}

// setFleetAPIKeyHeader sets the X-Fleet-API-Key header with the test API key.
func setFleetAPIKeyHeader(req *http.Request) {
	req.Header.Set("X-Fleet-API-Key", testFleetAPIKey)
}

func TestFleetRegister_Success(t *testing.T) {
	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, testFleetRegCfg())

	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: "worker-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeSuccessWithData(t, result, "token")

	// Verify the token is non-empty
	token, ok := result["token"].(string)
	if !ok || token == "" {
		t.Fatal("expected non-empty token in response")
	}

	// Verify the token is a valid JWT
	claims, err := ValidateWorkerToken(token, tokenCfg.SigningKey)
	if err != nil {
		t.Fatalf("returned token is not valid: %v", err)
	}
	if claims.WorkerID != "worker-1" {
		t.Errorf("token worker_id = %q, want %q", claims.WorkerID, "worker-1")
	}
}

func TestFleetRegister_EmptyWorkerID(t *testing.T) {
	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, testFleetRegCfg())

	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: "",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "token")
}

func TestFleetRegister_MissingWorkerID(t *testing.T) {
	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, testFleetRegCfg())

	// Send a body without the worker_id field at all
	body, _ := json.Marshal(map[string]interface{}{
		"repos": []string{"repo-a"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "token")
}

func TestFleetRegister_WorkerIDTooLong(t *testing.T) {
	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, testFleetRegCfg())

	longID := strings.Repeat("x", 257) // exceeds maxWorkerIDLength (256)
	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: longID,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "token")
}

func TestFleetRegister_DuplicateRegistration_Succeeds(t *testing.T) {
	registerCount := 0
	store := &mockWorkerRegistrar{
		registerFunc: func(ctx context.Context, worker *Worker) error {
			registerCount++
			return nil
		},
	}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, testFleetRegCfg())

	// Register twice with the same worker_id
	for i := 0; i < 2; i++ {
		body, _ := json.Marshal(FleetRegisterRequest{
			WorkerID: "worker-dup",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		setFleetAPIKeyHeader(req)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Errorf("attempt %d: status = %d, want %d", i+1, w.Code, http.StatusCreated)
		}

		result := assertJSONResponse(t, w)
		token, ok := result["token"].(string)
		if !ok || token == "" {
			t.Fatalf("attempt %d: expected non-empty token", i+1)
		}
	}

	if registerCount != 2 {
		t.Errorf("RegisterWorker called %d times, want 2", registerCount)
	}
}

func TestFleetRegister_StoreNil_Returns503(t *testing.T) {
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(nil, tokenCfg, testFleetRegCfg())

	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: "worker-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "token")
}

func TestFleetRegister_TokenConfigNil_Returns503(t *testing.T) {
	store := &mockWorkerRegistrar{}
	handler := handleFleetRegisterWithStore(store, nil, testFleetRegCfg())

	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: "worker-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "token")
}

func TestFleetRegister_StoreError_Returns500(t *testing.T) {
	store := &mockWorkerRegistrar{
		registerFunc: func(ctx context.Context, worker *Worker) error {
			return errors.New("redis connection refused")
		},
	}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, testFleetRegCfg())

	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: "worker-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "token")
}

func TestFleetRegister_InvalidJSON_Returns400(t *testing.T) {
	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, testFleetRegCfg())

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader([]byte("{invalid json")))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "token")
}

func TestFleetRegister_BodyTooLarge_Returns413(t *testing.T) {
	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, testFleetRegCfg())

	// Create a body larger than 1MB (maxRequestBody)
	largeBody := make([]byte, 1<<20+100)
	// Make it valid-looking JSON start so MaxBytesReader triggers, not JSON parse
	copy(largeBody, []byte(`{"worker_id":"`))
	for i := 14; i < len(largeBody)-2; i++ {
		largeBody[i] = 'x'
	}
	copy(largeBody[len(largeBody)-2:], []byte(`"}`))

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(largeBody))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "token")
}

func TestFleetRegister_TokenContainsCorrectClaims(t *testing.T) {
	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, testFleetRegCfg())

	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: "my-worker",
		Repos:    []string{"repo-x", "repo-y"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	result := assertJSONResponse(t, w)
	token, ok := result["token"].(string)
	if !ok || token == "" {
		t.Fatal("expected non-empty token in response")
	}

	claims, err := ValidateWorkerToken(token, tokenCfg.SigningKey)
	if err != nil {
		t.Fatalf("ValidateWorkerToken failed: %v", err)
	}

	if claims.WorkerID != "my-worker" {
		t.Errorf("claims.WorkerID = %q, want %q", claims.WorkerID, "my-worker")
	}
	if len(claims.Repos) != 2 {
		t.Fatalf("len(claims.Repos) = %d, want 2", len(claims.Repos))
	}
	if claims.Repos[0] != "repo-x" {
		t.Errorf("claims.Repos[0] = %q, want %q", claims.Repos[0], "repo-x")
	}
	if claims.Repos[1] != "repo-y" {
		t.Errorf("claims.Repos[1] = %q, want %q", claims.Repos[1], "repo-y")
	}
}

func TestFleetRegister_WithRepos_TokenScopedToRepos(t *testing.T) {
	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, testFleetRegCfg())

	repos := []string{"org/repo-1", "org/repo-2", "org/repo-3"}
	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: "scoped-worker",
		Repos:    repos,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	result := assertJSONResponse(t, w)
	token := result["token"].(string)

	claims, err := ValidateWorkerToken(token, tokenCfg.SigningKey)
	if err != nil {
		t.Fatalf("ValidateWorkerToken failed: %v", err)
	}

	if len(claims.Repos) != len(repos) {
		t.Fatalf("len(claims.Repos) = %d, want %d", len(claims.Repos), len(repos))
	}
	for i, want := range repos {
		if claims.Repos[i] != want {
			t.Errorf("claims.Repos[%d] = %q, want %q", i, claims.Repos[i], want)
		}
	}
}

func TestFleetRegister_WithoutRepos_TokenHasNilRepos(t *testing.T) {
	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, testFleetRegCfg())

	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: "no-repos-worker",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	result := assertJSONResponse(t, w)
	token := result["token"].(string)

	claims, err := ValidateWorkerToken(token, tokenCfg.SigningKey)
	if err != nil {
		t.Fatalf("ValidateWorkerToken failed: %v", err)
	}

	if claims.Repos != nil {
		t.Errorf("claims.Repos = %v, want nil", claims.Repos)
	}
}

func TestFleetRegister_StoreReceivesCorrectWorker(t *testing.T) {
	var capturedWorker *Worker
	store := &mockWorkerRegistrar{
		registerFunc: func(ctx context.Context, worker *Worker) error {
			capturedWorker = worker
			return nil
		},
	}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, testFleetRegCfg())

	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: "captured-worker",
		Repos:    []string{"repo-a"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	if capturedWorker == nil {
		t.Fatal("store.RegisterWorker was not called")
	}
	if capturedWorker.WorkerID != "captured-worker" {
		t.Errorf("worker.WorkerID = %q, want %q", capturedWorker.WorkerID, "captured-worker")
	}
	if len(capturedWorker.Repos) != 1 || capturedWorker.Repos[0] != "repo-a" {
		t.Errorf("worker.Repos = %v, want [repo-a]", capturedWorker.Repos)
	}
	if capturedWorker.RegisteredAt == 0 {
		t.Error("worker.RegisteredAt should be non-zero")
	}
}

// =============================================================================
// Fleet Register API Key Authentication Tests
// =============================================================================

func TestFleetRegister_MissingAPIKey_Returns401(t *testing.T) {
	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, testFleetRegCfg())

	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: "worker-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No X-Fleet-API-Key header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "token")
	if errMsg, ok := result["error"].(string); ok {
		if errMsg != "missing X-Fleet-API-Key header" {
			t.Errorf("error = %q, want %q", errMsg, "missing X-Fleet-API-Key header")
		}
	}
}

func TestFleetRegister_WrongAPIKey_Returns401(t *testing.T) {
	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, testFleetRegCfg())

	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: "worker-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fleet-API-Key", "wrong-key")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "token")
	if errMsg, ok := result["error"].(string); ok {
		if errMsg != "invalid API key" {
			t.Errorf("error = %q, want %q", errMsg, "invalid API key")
		}
	}
}

func TestFleetRegister_NilRegCfg_Returns503(t *testing.T) {
	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, nil)

	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: "worker-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fleet-API-Key", testFleetAPIKey)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "token")
	if errMsg, ok := result["error"].(string); ok {
		if errMsg != "fleet authentication not configured" {
			t.Errorf("error = %q, want %q", errMsg, "fleet authentication not configured")
		}
	}
}

func TestFleetRegister_EmptyAPIKeyConfig_Returns503(t *testing.T) {
	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, &RegisterConfig{APIKey: ""})

	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: "worker-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fleet-API-Key", "some-key")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestFleetRegister_RateLimitExceeded_Returns429(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	regCfg := &RegisterConfig{
		APIKey:      testFleetAPIKey,
		RateLimiter: NewRateLimiter(client, 2, time.Minute),
	}
	handler := handleFleetRegisterWithStore(store, tokenCfg, regCfg)

	// First two requests should succeed
	for i := 0; i < 2; i++ {
		body, _ := json.Marshal(FleetRegisterRequest{WorkerID: "worker-rl"})
		req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		setFleetAPIKeyHeader(req)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("request %d: status = %d, want %d", i+1, w.Code, http.StatusCreated)
		}
	}

	// Third request should be rate limited
	body, _ := json.Marshal(FleetRegisterRequest{WorkerID: "worker-rl"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("rate limited request: status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "token")
}

func TestFleetRegister_CorrectAPIKey_Succeeds(t *testing.T) {
	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	handler := handleFleetRegisterWithStore(store, tokenCfg, testFleetRegCfg())

	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: "authed-worker",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeSuccessWithData(t, result, "token")
}

// =============================================================================
// Fleet Done Handler Tests
// =============================================================================

// mockFleetDoneStore implements fleetDoneStore for testing.
type mockFleetDoneStore struct {
	getWorkerFunc      func(ctx context.Context, workerID string) (*Worker, error)
	getWorkerClaimFunc func(ctx context.Context, workerID string) (*ClaimResponse, error)
	recordResultFunc   func(ctx context.Context, result *TaskResult) error
	releaseClaimFunc   func(ctx context.Context, taskID string) error
	clearClaimFunc     func(ctx context.Context, workerID string) error
}

func (m *mockFleetDoneStore) GetWorker(ctx context.Context, workerID string) (*Worker, error) {
	if m.getWorkerFunc != nil {
		return m.getWorkerFunc(ctx, workerID)
	}
	return nil, errors.New("getWorkerFunc not implemented")
}

func (m *mockFleetDoneStore) GetWorkerClaim(ctx context.Context, workerID string) (*ClaimResponse, error) {
	if m.getWorkerClaimFunc != nil {
		return m.getWorkerClaimFunc(ctx, workerID)
	}
	return nil, errors.New("getWorkerClaimFunc not implemented")
}

func (m *mockFleetDoneStore) RecordTaskResult(ctx context.Context, result *TaskResult) error {
	if m.recordResultFunc != nil {
		return m.recordResultFunc(ctx, result)
	}
	return errors.New("recordResultFunc not implemented")
}

func (m *mockFleetDoneStore) ReleaseClaim(ctx context.Context, taskID string) error {
	if m.releaseClaimFunc != nil {
		return m.releaseClaimFunc(ctx, taskID)
	}
	return errors.New("releaseClaimFunc not implemented")
}

func (m *mockFleetDoneStore) ClearWorkerClaim(ctx context.Context, workerID string) error {
	if m.clearClaimFunc != nil {
		return m.clearClaimFunc(ctx, workerID)
	}
	return errors.New("clearClaimFunc not implemented")
}

func TestFleetDone_StoreNil_Returns503(t *testing.T) {
	handler := handleFleetDoneWithStore(nil)

	body, _ := json.Marshal(FleetDoneRequest{Success: true})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/workers/worker-1/done", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "worker-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "task_id")
}

func TestFleetDone_MissingWorkerID_Returns400(t *testing.T) {
	store := &mockFleetDoneStore{}
	handler := handleFleetDoneWithStore(store)

	body, _ := json.Marshal(FleetDoneRequest{Success: true})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/workers//done", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// Do NOT set path value for "id" to simulate missing worker ID
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "task_id")
}

func TestFleetDone_InvalidJSON_Returns400(t *testing.T) {
	store := &mockFleetDoneStore{}
	handler := handleFleetDoneWithStore(store)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/workers/worker-1/done", bytes.NewReader([]byte("{invalid json")))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "worker-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "task_id")
}

func TestFleetDone_BodyTooLarge_Returns413(t *testing.T) {
	store := &mockFleetDoneStore{}
	handler := handleFleetDoneWithStore(store)

	// Create a body larger than 1MB (maxRequestBody)
	largeBody := make([]byte, 1<<20+100)
	copy(largeBody, []byte(`{"success":true,"`))
	for i := 17; i < len(largeBody)-2; i++ {
		largeBody[i] = 'x'
	}
	copy(largeBody[len(largeBody)-2:], []byte(`"}`))

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/workers/worker-1/done", bytes.NewReader(largeBody))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "worker-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "task_id")
}

func TestFleetDone_WorkerNotFound(t *testing.T) {
	store := &mockFleetDoneStore{
		getWorkerFunc: func(ctx context.Context, workerID string) (*Worker, error) {
			return nil, nil // worker not found
		},
	}
	handler := handleFleetDoneWithStore(store)

	body, _ := json.Marshal(FleetDoneRequest{Success: true})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/workers/unknown-worker/done", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "unknown-worker")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "task_id")
}

func TestFleetDone_GetWorkerError_Returns500(t *testing.T) {
	store := &mockFleetDoneStore{
		getWorkerFunc: func(ctx context.Context, workerID string) (*Worker, error) {
			return nil, errors.New("redis connection refused")
		},
	}
	handler := handleFleetDoneWithStore(store)

	body, _ := json.Marshal(FleetDoneRequest{Success: true})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/workers/worker-1/done", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "worker-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "task_id")
}

func TestFleetDone_GetWorkerClaimError_Returns500(t *testing.T) {
	store := &mockFleetDoneStore{
		getWorkerFunc: func(ctx context.Context, workerID string) (*Worker, error) {
			return &Worker{WorkerID: workerID, RegisteredAt: time.Now().Unix()}, nil
		},
		getWorkerClaimFunc: func(ctx context.Context, workerID string) (*ClaimResponse, error) {
			return nil, errors.New("redis timeout")
		},
	}
	handler := handleFleetDoneWithStore(store)

	body, _ := json.Marshal(FleetDoneRequest{Success: true})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/workers/worker-1/done", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "worker-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "task_id")
}

func TestFleetDone_NoClaim_IdempotentSuccess(t *testing.T) {
	store := &mockFleetDoneStore{
		getWorkerFunc: func(ctx context.Context, workerID string) (*Worker, error) {
			return &Worker{WorkerID: workerID, RegisteredAt: time.Now().Unix()}, nil
		},
		getWorkerClaimFunc: func(ctx context.Context, workerID string) (*ClaimResponse, error) {
			return nil, nil // no active claim
		},
	}
	handler := handleFleetDoneWithStore(store)

	body, _ := json.Marshal(FleetDoneRequest{Success: true})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/workers/worker-1/done", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "worker-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeSuccess(t, result)

	// Verify worker_id is returned
	workerID, ok := result["worker_id"].(string)
	if !ok || workerID != "worker-1" {
		t.Errorf("worker_id = %v, want %q", result["worker_id"], "worker-1")
	}

	// Verify task_id is absent (no claim was active)
	if _, ok := result["task_id"]; ok {
		t.Error("task_id should be absent when no claim was active")
	}
}

func TestFleetDone_RecordTaskResultError_Returns500(t *testing.T) {
	store := &mockFleetDoneStore{
		getWorkerFunc: func(ctx context.Context, workerID string) (*Worker, error) {
			return &Worker{WorkerID: workerID, RegisteredAt: time.Now().Unix()}, nil
		},
		getWorkerClaimFunc: func(ctx context.Context, workerID string) (*ClaimResponse, error) {
			return &ClaimResponse{TaskID: "task-42", Success: true}, nil
		},
		recordResultFunc: func(ctx context.Context, result *TaskResult) error {
			return errors.New("write failed")
		},
	}
	handler := handleFleetDoneWithStore(store)

	body, _ := json.Marshal(FleetDoneRequest{Success: true, CommitSHA: "abc123"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/workers/worker-1/done", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "worker-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "task_id")
}

func TestFleetDone_ReleaseClaimError_Returns500(t *testing.T) {
	store := &mockFleetDoneStore{
		getWorkerFunc: func(ctx context.Context, workerID string) (*Worker, error) {
			return &Worker{WorkerID: workerID, RegisteredAt: time.Now().Unix()}, nil
		},
		getWorkerClaimFunc: func(ctx context.Context, workerID string) (*ClaimResponse, error) {
			return &ClaimResponse{TaskID: "task-42", Success: true}, nil
		},
		recordResultFunc: func(ctx context.Context, result *TaskResult) error {
			return nil
		},
		releaseClaimFunc: func(ctx context.Context, taskID string) error {
			return errors.New("redis connection lost")
		},
	}
	handler := handleFleetDoneWithStore(store)

	body, _ := json.Marshal(FleetDoneRequest{Success: true, CommitSHA: "abc123"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/workers/worker-1/done", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "worker-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "task_id")
}

func TestFleetDone_SuccessfulCompletion(t *testing.T) {
	var capturedResult *TaskResult
	var capturedReleaseTaskID string
	var capturedClearWorkerID string

	store := &mockFleetDoneStore{
		getWorkerFunc: func(ctx context.Context, workerID string) (*Worker, error) {
			return &Worker{WorkerID: workerID, RegisteredAt: time.Now().Unix()}, nil
		},
		getWorkerClaimFunc: func(ctx context.Context, workerID string) (*ClaimResponse, error) {
			return &ClaimResponse{TaskID: "task-42", Success: true}, nil
		},
		recordResultFunc: func(ctx context.Context, result *TaskResult) error {
			capturedResult = result
			return nil
		},
		releaseClaimFunc: func(ctx context.Context, taskID string) error {
			capturedReleaseTaskID = taskID
			return nil
		},
		clearClaimFunc: func(ctx context.Context, workerID string) error {
			capturedClearWorkerID = workerID
			return nil
		},
	}
	handler := handleFleetDoneWithStore(store)

	body, _ := json.Marshal(FleetDoneRequest{Success: true, CommitSHA: "abc123def"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/workers/worker-1/done", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "worker-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeSuccessWithData(t, result, "task_id")

	// Verify response fields
	taskID, ok := result["task_id"].(string)
	if !ok || taskID != "task-42" {
		t.Errorf("task_id = %v, want %q", result["task_id"], "task-42")
	}
	workerID, ok := result["worker_id"].(string)
	if !ok || workerID != "worker-1" {
		t.Errorf("worker_id = %v, want %q", result["worker_id"], "worker-1")
	}

	// Verify RecordTaskResult was called with correct data
	if capturedResult == nil {
		t.Fatal("RecordTaskResult was not called")
	}
	if capturedResult.WorkerID != "worker-1" {
		t.Errorf("result.WorkerID = %q, want %q", capturedResult.WorkerID, "worker-1")
	}
	if capturedResult.TaskID != "task-42" {
		t.Errorf("result.TaskID = %q, want %q", capturedResult.TaskID, "task-42")
	}
	if !capturedResult.Success {
		t.Error("result.Success = false, want true")
	}
	if capturedResult.CommitSHA != "abc123def" {
		t.Errorf("result.CommitSHA = %q, want %q", capturedResult.CommitSHA, "abc123def")
	}
	if capturedResult.CompletedAt.IsZero() {
		t.Error("result.CompletedAt should be non-zero")
	}

	// Verify ReleaseClaim was called with correct taskID
	if capturedReleaseTaskID != "task-42" {
		t.Errorf("ReleaseClaim taskID = %q, want %q", capturedReleaseTaskID, "task-42")
	}

	// Verify ClearWorkerClaim was called with correct workerID
	if capturedClearWorkerID != "worker-1" {
		t.Errorf("ClearWorkerClaim workerID = %q, want %q", capturedClearWorkerID, "worker-1")
	}
}

func TestFleetDone_FailedTask_WithError(t *testing.T) {
	var capturedResult *TaskResult

	store := &mockFleetDoneStore{
		getWorkerFunc: func(ctx context.Context, workerID string) (*Worker, error) {
			return &Worker{WorkerID: workerID, RegisteredAt: time.Now().Unix()}, nil
		},
		getWorkerClaimFunc: func(ctx context.Context, workerID string) (*ClaimResponse, error) {
			return &ClaimResponse{TaskID: "task-99", Success: true}, nil
		},
		recordResultFunc: func(ctx context.Context, result *TaskResult) error {
			capturedResult = result
			return nil
		},
		releaseClaimFunc: func(ctx context.Context, taskID string) error {
			return nil
		},
		clearClaimFunc: func(ctx context.Context, workerID string) error {
			return nil
		},
	}
	handler := handleFleetDoneWithStore(store)

	body, _ := json.Marshal(FleetDoneRequest{Success: false, Error: "build failed: exit code 1"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/workers/worker-2/done", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "worker-2")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeSuccessWithData(t, result, "task_id")

	// Verify response fields
	taskID, ok := result["task_id"].(string)
	if !ok || taskID != "task-99" {
		t.Errorf("task_id = %v, want %q", result["task_id"], "task-99")
	}
	workerID, ok := result["worker_id"].(string)
	if !ok || workerID != "worker-2" {
		t.Errorf("worker_id = %v, want %q", result["worker_id"], "worker-2")
	}

	// Verify RecordTaskResult captured failure details
	if capturedResult == nil {
		t.Fatal("RecordTaskResult was not called")
	}
	if capturedResult.Success {
		t.Error("result.Success = true, want false")
	}
	if capturedResult.Error != "build failed: exit code 1" {
		t.Errorf("result.Error = %q, want %q", capturedResult.Error, "build failed: exit code 1")
	}
	if capturedResult.CommitSHA != "" {
		t.Errorf("result.CommitSHA = %q, want empty string", capturedResult.CommitSHA)
	}
}

func TestFleetDone_ClearWorkerClaimFailure_BestEffort(t *testing.T) {
	// ClearWorkerClaim failure should not affect the overall success of the operation.
	clearClaimCalled := false

	store := &mockFleetDoneStore{
		getWorkerFunc: func(ctx context.Context, workerID string) (*Worker, error) {
			return &Worker{WorkerID: workerID, RegisteredAt: time.Now().Unix()}, nil
		},
		getWorkerClaimFunc: func(ctx context.Context, workerID string) (*ClaimResponse, error) {
			return &ClaimResponse{TaskID: "task-77", Success: true}, nil
		},
		recordResultFunc: func(ctx context.Context, result *TaskResult) error {
			return nil
		},
		releaseClaimFunc: func(ctx context.Context, taskID string) error {
			return nil
		},
		clearClaimFunc: func(ctx context.Context, workerID string) error {
			clearClaimCalled = true
			return errors.New("cache eviction failed")
		},
	}
	handler := handleFleetDoneWithStore(store)

	body, _ := json.Marshal(FleetDoneRequest{Success: true, CommitSHA: "def456"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/workers/worker-1/done", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "worker-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Should still return 200 OK even though ClearWorkerClaim failed
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeSuccessWithData(t, result, "task_id")

	if !clearClaimCalled {
		t.Error("ClearWorkerClaim was not called")
	}

	// Verify the response still has correct data
	taskID, ok := result["task_id"].(string)
	if !ok || taskID != "task-77" {
		t.Errorf("task_id = %v, want %q", result["task_id"], "task-77")
	}
	workerID, ok := result["worker_id"].(string)
	if !ok || workerID != "worker-1" {
		t.Errorf("worker_id = %v, want %q", result["worker_id"], "worker-1")
	}
}

// =============================================================================
// Fleet Heartbeat Handler Tests
// =============================================================================

// mockHeartbeatStore implements heartbeatStore for testing.
type mockHeartbeatStore struct {
	updateHeartbeatFunc func(ctx context.Context, workerID string) (time.Time, error)
}

func (m *mockHeartbeatStore) UpdateHeartbeat(ctx context.Context, workerID string) (time.Time, error) {
	if m.updateHeartbeatFunc != nil {
		return m.updateHeartbeatFunc(ctx, workerID)
	}
	return time.Time{}, errors.New("updateHeartbeatFunc not implemented")
}

func TestFleetHeartbeat_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store := &mockHeartbeatStore{
		updateHeartbeatFunc: func(ctx context.Context, workerID string) (time.Time, error) {
			if workerID != "worker-1" {
				t.Errorf("UpdateHeartbeat called with workerID %q, want %q", workerID, "worker-1")
			}
			return now, nil
		},
	}
	handler := handleFleetHeartbeatWithStore(store)

	body, _ := json.Marshal(HeartbeatRequest{WorkerID: "worker-1"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/heartbeat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeSuccessWithData(t, result, "last_heartbeat")

	// Verify last_heartbeat is a valid RFC3339 timestamp
	lastHB, ok := result["last_heartbeat"].(string)
	if !ok || lastHB == "" {
		t.Fatal("expected non-empty last_heartbeat in response")
	}
	parsed, err := time.Parse(time.RFC3339, lastHB)
	if err != nil {
		t.Fatalf("last_heartbeat is not valid RFC3339: %v", err)
	}
	if !parsed.Equal(now) {
		t.Errorf("last_heartbeat = %v, want %v", parsed, now)
	}
}

func TestFleetHeartbeat_WorkerNotFound(t *testing.T) {
	store := &mockHeartbeatStore{
		updateHeartbeatFunc: func(ctx context.Context, workerID string) (time.Time, error) {
			return time.Time{}, ErrWorkerNotFound
		},
	}
	handler := handleFleetHeartbeatWithStore(store)

	body, _ := json.Marshal(HeartbeatRequest{WorkerID: "unknown-worker"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/heartbeat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "last_heartbeat")
}

func TestFleetHeartbeat_MissingWorkerID(t *testing.T) {
	store := &mockHeartbeatStore{}
	handler := handleFleetHeartbeatWithStore(store)

	body, _ := json.Marshal(map[string]interface{}{})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/heartbeat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "last_heartbeat")
}

func TestFleetHeartbeat_EmptyWorkerID(t *testing.T) {
	store := &mockHeartbeatStore{}
	handler := handleFleetHeartbeatWithStore(store)

	body, _ := json.Marshal(HeartbeatRequest{WorkerID: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/heartbeat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "last_heartbeat")
}

func TestFleetHeartbeat_NilStore_Returns503(t *testing.T) {
	handler := handleFleetHeartbeatWithStore(nil)

	body, _ := json.Marshal(HeartbeatRequest{WorkerID: "worker-1"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/heartbeat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "last_heartbeat")
}

func TestFleetHeartbeat_InvalidJSON_Returns400(t *testing.T) {
	store := &mockHeartbeatStore{}
	handler := handleFleetHeartbeatWithStore(store)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/heartbeat", bytes.NewReader([]byte("{invalid json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "last_heartbeat")
}

func TestFleetHeartbeat_BodyTooLarge_Returns413(t *testing.T) {
	store := &mockHeartbeatStore{}
	handler := handleFleetHeartbeatWithStore(store)

	// Create a body larger than 1MB (maxRequestBody)
	largeBody := make([]byte, 1<<20+100)
	copy(largeBody, []byte(`{"worker_id":"`))
	for i := 14; i < len(largeBody)-2; i++ {
		largeBody[i] = 'x'
	}
	copy(largeBody[len(largeBody)-2:], []byte(`"}`))

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/heartbeat", bytes.NewReader(largeBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "last_heartbeat")
}

func TestFleetHeartbeat_WorkerIDTooLong(t *testing.T) {
	store := &mockHeartbeatStore{}
	handler := handleFleetHeartbeatWithStore(store)

	longID := strings.Repeat("x", 257) // exceeds maxWorkerIDLength (256)
	body, _ := json.Marshal(HeartbeatRequest{WorkerID: longID})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/heartbeat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "last_heartbeat")
}

func TestFleetHeartbeat_StoreError_Returns500(t *testing.T) {
	store := &mockHeartbeatStore{
		updateHeartbeatFunc: func(ctx context.Context, workerID string) (time.Time, error) {
			return time.Time{}, errors.New("redis connection refused")
		},
	}
	handler := handleFleetHeartbeatWithStore(store)

	body, _ := json.Marshal(HeartbeatRequest{WorkerID: "worker-1"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/heartbeat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "last_heartbeat")
}

// =============================================================================
// Fleet Claim Handler Edge Case Tests (Metrics, Errors, Filters)
// =============================================================================

func TestFleetClaim_Timeout_RecordsMetric(t *testing.T) {
	metrics := NewClaimMetrics()

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return nil, context.DeadlineExceeded
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool, metrics)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want %d", w.Code, http.StatusGatewayTimeout)
	}

	snap := metrics.Snapshot()
	if snap.Timeout != 1 {
		t.Errorf("timeout count = %d, want 1", snap.Timeout)
	}
}

func TestFleetClaim_SpecificIssue_CollisionRecordsMetric(t *testing.T) {
	metrics := NewClaimMetrics()

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

	handler := handleFleetClaimWithPool(pool, metrics)

	body, _ := json.Marshal(FleetClaimRequest{IssueID: "test-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}

	snap := metrics.Snapshot()
	if snap.Collision != 1 {
		t.Errorf("collision count = %d, want 1", snap.Collision)
	}
}

func TestFleetClaim_SuccessRecordsMetric(t *testing.T) {
	metrics := NewClaimMetrics()

	issueData, _ := json.Marshal(types.Issue{
		ID:     "test-123",
		Title:  "Test Task",
		Status: "in_progress",
	})

	client := &mockFleetClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
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

	handler := handleFleetClaimWithPool(pool, metrics)

	body, _ := json.Marshal(FleetClaimRequest{IssueID: "test-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	snap := metrics.Snapshot()
	if snap.Success != 1 {
		t.Errorf("success count = %d, want 1", snap.Success)
	}
}

func TestFleetClaim_ReadyThenClaim_CollisionsRecordMetrics(t *testing.T) {
	metrics := NewClaimMetrics()

	readyIssues := []*types.Issue{
		{ID: "task-1", Title: "First", Status: "open"},
		{ID: "task-2", Title: "Second", Status: "open"},
		{ID: "task-3", Title: "Third", Status: "open"},
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

	handler := handleFleetClaimWithPool(pool, metrics)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	snap := metrics.Snapshot()
	if snap.Collision != 3 {
		t.Errorf("collision count = %d, want 3", snap.Collision)
	}
}

func TestFleetClaim_PoolGetNonTimeoutError_Returns503(t *testing.T) {
	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return nil, errors.New("connection refused")
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")
}

func TestFleetClaim_ReadyResponseNotSuccess_Returns500(t *testing.T) {
	client := &mockFleetClient{
		readyFunc: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: false,
				Error:   "internal daemon error",
			}, nil
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")

	// Verify the daemon error is propagated
	if errMsg, ok := result["error"].(string); ok {
		if errMsg != "internal daemon error" {
			t.Errorf("error = %q, want %q", errMsg, "internal daemon error")
		}
	}
}

func TestFleetClaim_ReadyResponseMalformedData_Returns500(t *testing.T) {
	client := &mockFleetClient{
		readyFunc: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: true,
				Data:    []byte("not json"),
			}, nil
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")
}

func TestFleetClaim_SpecificIssue_NonCollisionUpdateFailure_Returns500(t *testing.T) {
	client := &mockFleetClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: false,
				Error:   "database locked",
			}, nil
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool, nil)

	body, _ := json.Marshal(FleetClaimRequest{IssueID: "test-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")

	// Verify the error message is propagated (not "already claimed")
	if errMsg, ok := result["error"].(string); ok {
		if errMsg != "database locked" {
			t.Errorf("error = %q, want %q", errMsg, "database locked")
		}
	}
}

func TestFleetClaim_SpecificIssue_MalformedResponseData_Returns500(t *testing.T) {
	client := &mockFleetClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: true,
				Data:    []byte("not json"),
			}, nil
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool, nil)

	body, _ := json.Marshal(FleetClaimRequest{IssueID: "test-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeError(t, result, "payload")
}

func TestFleetClaim_ReadyThenClaim_RPCError_SkipsToNext(t *testing.T) {
	readyIssues := []*types.Issue{
		{ID: "task-1", Title: "First", Status: "open"},
		{ID: "task-2", Title: "Second", Status: "open"},
	}
	readyData, _ := json.Marshal(readyIssues)

	claimedData, _ := json.Marshal(types.Issue{
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
				// First attempt: RPC error
				return nil, errors.New("connection reset")
			}
			// Second attempt: success
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

	handler := handleFleetClaimWithPool(pool, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	if callCount != 2 {
		t.Errorf("Update called %d times, want 2", callCount)
	}

	result := assertJSONResponse(t, w)
	payload := result["payload"].(map[string]interface{})
	issue := payload["issue"].(map[string]interface{})
	if issue["id"] != "task-2" {
		t.Errorf("claimed issue id = %v, want %q", issue["id"], "task-2")
	}
}

func TestFleetClaim_ReadyThenClaim_MalformedClaimedData_SkipsToNext(t *testing.T) {
	readyIssues := []*types.Issue{
		{ID: "task-1", Title: "First", Status: "open"},
		{ID: "task-2", Title: "Second", Status: "open"},
	}
	readyData, _ := json.Marshal(readyIssues)

	claimedData, _ := json.Marshal(types.Issue{
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
				// First attempt: success but malformed data
				return &rpc.Response{
					Success: true,
					Data:    []byte("bad json"),
				}, nil
			}
			// Second attempt: success with valid data
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

	handler := handleFleetClaimWithPool(pool, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	if callCount != 2 {
		t.Errorf("Update called %d times, want 2", callCount)
	}

	result := assertJSONResponse(t, w)
	payload := result["payload"].(map[string]interface{})
	issue := payload["issue"].(map[string]interface{})
	if issue["id"] != "task-2" {
		t.Errorf("claimed issue id = %v, want %q", issue["id"], "task-2")
	}
}

func TestFleetClaim_WithFilters_PassedToReady(t *testing.T) {
	maxPri := 1
	var capturedArgs *rpc.ReadyArgs

	emptyIssues, _ := json.Marshal([]*types.Issue{})

	client := &mockFleetClient{
		readyFunc: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			capturedArgs = args
			return &rpc.Response{
				Success: true,
				Data:    emptyIssues,
			}, nil
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
		putFunc: func(c fleetClaimClient) {},
	}

	handler := handleFleetClaimWithPool(pool, nil)

	body, _ := json.Marshal(FleetClaimRequest{
		IssueType:   "bug",
		MaxPriority: &maxPri,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if capturedArgs == nil {
		t.Fatal("Ready was not called")
	}
	if capturedArgs.Type != "bug" {
		t.Errorf("readyArgs.Type = %q, want %q", capturedArgs.Type, "bug")
	}
	if capturedArgs.Priority == nil || *capturedArgs.Priority != 1 {
		t.Errorf("readyArgs.Priority = %v, want 1", capturedArgs.Priority)
	}
	if capturedArgs.Limit != 10 {
		t.Errorf("readyArgs.Limit = %d, want 10", capturedArgs.Limit)
	}
}

// =============================================================================
// Fleet Register Handler Edge Case Tests
// =============================================================================

func TestFleetRegister_RateLimiterNil_Bypassed(t *testing.T) {
	store := &mockWorkerRegistrar{}
	tokenCfg := testTokenConfig()
	regCfg := &RegisterConfig{
		APIKey:      testFleetAPIKey,
		RateLimiter: nil, // Explicitly nil
	}
	handler := handleFleetRegisterWithStore(store, tokenCfg, regCfg)

	body, _ := json.Marshal(FleetRegisterRequest{
		WorkerID: "worker-no-rl",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setFleetAPIKeyHeader(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeSuccessWithData(t, result, "token")
}

// =============================================================================
// Fleet Done Handler Edge Case Tests
// =============================================================================

func TestFleetDone_ClaimWithEmptyTaskID_StillProcesses(t *testing.T) {
	var capturedResult *TaskResult
	var capturedReleaseTaskID string

	store := &mockFleetDoneStore{
		getWorkerFunc: func(ctx context.Context, workerID string) (*Worker, error) {
			return &Worker{WorkerID: workerID, RegisteredAt: time.Now().Unix()}, nil
		},
		getWorkerClaimFunc: func(ctx context.Context, workerID string) (*ClaimResponse, error) {
			return &ClaimResponse{TaskID: "", Success: true}, nil
		},
		recordResultFunc: func(ctx context.Context, result *TaskResult) error {
			capturedResult = result
			return nil
		},
		releaseClaimFunc: func(ctx context.Context, taskID string) error {
			capturedReleaseTaskID = taskID
			return nil
		},
		clearClaimFunc: func(ctx context.Context, workerID string) error {
			return nil
		},
	}
	handler := handleFleetDoneWithStore(store)

	body, _ := json.Marshal(FleetDoneRequest{Success: true, CommitSHA: "abc123"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/workers/worker-1/done", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "worker-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	result := assertJSONResponse(t, w)
	assertEnvelopeSuccess(t, result)

	// Verify RecordTaskResult was called with empty task ID
	if capturedResult == nil {
		t.Fatal("RecordTaskResult was not called")
	}
	if capturedResult.TaskID != "" {
		t.Errorf("result.TaskID = %q, want empty string", capturedResult.TaskID)
	}
	if capturedResult.WorkerID != "worker-1" {
		t.Errorf("result.WorkerID = %q, want %q", capturedResult.WorkerID, "worker-1")
	}

	// Verify ReleaseClaim was called with empty task ID
	if capturedReleaseTaskID != "" {
		t.Errorf("ReleaseClaim taskID = %q, want empty string", capturedReleaseTaskID)
	}
}
