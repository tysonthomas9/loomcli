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

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type claimTestBackend struct {
	backend.IssueBackend
	ready      []backend.IssueData
	details    map[string]*backend.IssueDetailData
	claimErr   map[string]error
	claimCalls []string
}

func (b *claimTestBackend) Ready(_ context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) {
	return b.ready, nil
}

func (b *claimTestBackend) ClaimIssue(_ context.Context, id string, _ time.Duration) error {
	b.claimCalls = append(b.claimCalls, id)
	return b.claimErr[id]
}

func (b *claimTestBackend) Get(_ context.Context, id string) (*backend.IssueDetailData, error) {
	return b.details[id], nil
}

func TestFleetClaim_DirectBackendExplicitIssue(t *testing.T) {
	be := &claimTestBackend{details: map[string]*backend.IssueDetailData{
		"TASK-1": {
			IssueData: backend.IssueData{
				ID:          "TASK-1",
				Title:       "Direct claim",
				Status:      "in_progress",
				IssueType:   "task",
				Labels:      []string{"phase6"},
				SourceRepo:  "loomcli",
				ExternalRef: "local-branch:phase9@abc123",
			},
			Description: "claimed through IssueBackend",
		},
	}}
	metrics := NewClaimMetrics()
	body := bytes.NewBufferString(`{"issue_id":"TASK-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", body)
	w := httptest.NewRecorder()

	handleFleetClaim(func(context.Context) backend.IssueBackend { return be }, metrics).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	responseBody := append([]byte(nil), w.Body.Bytes()...)
	var response FleetClaimResponse
	if err := json.NewDecoder(bytes.NewReader(responseBody)).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success || response.Payload == nil || response.Payload.Issue == nil {
		t.Fatalf("unexpected response: %+v", response)
	}
	if got := response.Payload.Issue.ID; got != "TASK-1" {
		t.Fatalf("issue ID = %q, want TASK-1", got)
	}
	if got := response.Payload.Issue.Description; got != "claimed through IssueBackend" {
		t.Fatalf("description = %q", got)
	}
	var raw map[string]any
	if err := json.Unmarshal(responseBody, &raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	payload := raw["payload"].(map[string]any)
	issue := payload["issue"].(map[string]any)
	if issue["source_repo"] != "loomcli" || issue["external_ref"] != "local-branch:phase9@abc123" {
		t.Fatalf("compatibility issue identity = %#v", issue)
	}
	for _, forbidden := range []string{"repo", "dependencies", "dependents", "comments"} {
		if _, exists := issue[forbidden]; exists {
			t.Fatalf("compatibility issue unexpectedly added %q: %#v", forbidden, issue)
		}
	}
	if len(be.claimCalls) != 1 || be.claimCalls[0] != "TASK-1" {
		t.Fatalf("claim calls = %v, want [TASK-1]", be.claimCalls)
	}
	if got := metrics.Snapshot(); got.Success != 1 || got.Total != 1 {
		t.Fatalf("metrics = %+v, want one success", got)
	}
}

func TestFleetClaim_DirectBackendSkipsContendedReadyIssue(t *testing.T) {
	be := &claimTestBackend{
		ready: []backend.IssueData{{ID: "TASK-1"}, {ID: "TASK-2"}},
		details: map[string]*backend.IssueDetailData{
			"TASK-2": {IssueData: backend.IssueData{ID: "TASK-2", Title: "Winner", Status: "in_progress", IssueType: "task"}},
		},
		claimErr: map[string]error{
			"TASK-1": backend.ErrConflict("ClaimIssue", "already claimed"),
		},
	}
	metrics := NewClaimMetrics()
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()

	handleFleetClaim(func(context.Context) backend.IssueBackend { return be }, metrics).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	var response FleetClaimResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Payload == nil || response.Payload.Issue == nil || response.Payload.Issue.ID != "TASK-2" {
		t.Fatalf("unexpected response: %+v", response)
	}
	if got := strings.Join(be.claimCalls, ","); got != "TASK-1,TASK-2" {
		t.Fatalf("claim calls = %q, want TASK-1,TASK-2", got)
	}
	if got := metrics.Snapshot(); got.Collision != 1 || got.Success != 1 || got.Total != 2 {
		t.Fatalf("metrics = %+v, want one collision and one success", got)
	}
}

func TestFleetClaim_DirectBackendExplicitConflict(t *testing.T) {
	be := &claimTestBackend{claimErr: map[string]error{
		"TASK-1": backend.ErrConflict("ClaimIssue", "already claimed"),
	}}
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", bytes.NewBufferString(`{"issue_id":"TASK-1"}`))
	w := httptest.NewRecorder()

	handleFleetClaim(func(context.Context) backend.IssueBackend { return be }, nil).ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusConflict, w.Body.String())
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
