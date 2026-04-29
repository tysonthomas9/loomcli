package fleet

// Tests in this file verify the connection-pool cleanup discipline of the
// fleet-claim handler: a transport-level RPC failure (where the response
// frame was never fully consumed) must result in pool.Discard, while a clean
// logical failure or a parse error on a fully-received frame must result in
// pool.Put. See loomcli-67meg / loomcli-hzp7p for the convention.

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

// TestFleetClaim_SpecificIssue_TransportError_DiscardsClient verifies that when
// client.Update returns (nil, err) — a transport error where the response frame
// was never received — the deferred cleanup calls Discard, not Put. Regression
// guard for loomcli-hzp7p.
func TestFleetClaim_SpecificIssue_TransportError_DiscardsClient(t *testing.T) {
	client := &mockFleetClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			return nil, errors.New("connection reset by peer")
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
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

	put, discard := pool.counts()
	if discard != 1 || put != 0 {
		t.Errorf("pool calls = put=%d discard=%d, want put=0 discard=1", put, discard)
	}
}

// TestFleetClaim_SpecificIssue_LogicalFailure_PutsClient verifies that when
// client.Update returns (&resp{Success:false}, nil) — a clean logical failure
// with the response frame fully consumed — the deferred cleanup calls Put.
// Regression guard for loomcli-hzp7p.
func TestFleetClaim_SpecificIssue_LogicalFailure_PutsClient(t *testing.T) {
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

	put, discard := pool.counts()
	if put != 1 || discard != 0 {
		t.Errorf("pool calls = put=%d discard=%d, want put=1 discard=0", put, discard)
	}
}

// TestFleetClaim_SpecificIssue_Success_PutsClient verifies the happy path: a
// successful claim returns the connection to the pool via Put.
func TestFleetClaim_SpecificIssue_Success_PutsClient(t *testing.T) {
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

	handler := handleFleetClaimWithPool(pool, nil)

	body, _ := json.Marshal(FleetClaimRequest{IssueID: "test-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	put, discard := pool.counts()
	if put != 1 || discard != 0 {
		t.Errorf("pool calls = put=%d discard=%d, want put=1 discard=0", put, discard)
	}
}

// TestFleetClaim_SpecificIssue_ParseError_PutsClient verifies that a malformed
// JSON body in a fully-received response keeps the connection healthy: the
// frame was consumed before parsing failed, so Put (not Discard) is correct.
func TestFleetClaim_SpecificIssue_ParseError_PutsClient(t *testing.T) {
	client := &mockFleetClient{
		updateFunc: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: true,
				Data:    []byte("{not valid json"),
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

	put, discard := pool.counts()
	if put != 1 || discard != 0 {
		t.Errorf("pool calls = put=%d discard=%d, want put=1 discard=0", put, discard)
	}
}

// TestFleetClaim_SpecificIssue_LogicalFailure_NonCollision_PutsClient verifies
// that a non-"already claimed" Success=false response (e.g., database locked)
// still Puts the connection — the connection is intact even though the request
// failed logically. This guards against regressing the broader category of
// clean logical failures, not just collisions.
func TestFleetClaim_SpecificIssue_LogicalFailure_NonCollision_PutsClient(t *testing.T) {
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

	put, discard := pool.counts()
	if put != 1 || discard != 0 {
		t.Errorf("pool calls = put=%d discard=%d, want put=1 discard=0", put, discard)
	}
}

// =============================================================================
// Ready + tryClaimIssue loop pool-cleanup tests (loomcli-q4xms)
// =============================================================================

// TestFleetClaim_Ready_TransportError_DiscardsClient verifies that a transport
// error on client.Ready (resp == nil && err != nil) Discards the connection.
func TestFleetClaim_Ready_TransportError_DiscardsClient(t *testing.T) {
	client := &mockFleetClient{
		readyFunc: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			return nil, errors.New("connection reset")
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
	}

	handler := handleFleetClaimWithPool(pool, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	put, discard := pool.counts()
	if discard != 1 || put != 0 {
		t.Errorf("pool calls = put=%d discard=%d, want put=0 discard=1", put, discard)
	}
}

// TestFleetClaim_Ready_LogicalFailure_PutsClient verifies that a clean failure
// on Ready (Success=false, fully-received frame) Puts the connection.
func TestFleetClaim_Ready_LogicalFailure_PutsClient(t *testing.T) {
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

	put, discard := pool.counts()
	if put != 1 || discard != 0 {
		t.Errorf("pool calls = put=%d discard=%d, want put=1 discard=0", put, discard)
	}
}

// TestFleetClaim_Ready_ParseError_PutsClient verifies that a malformed JSON
// body in a successfully-received Ready response keeps the connection healthy
// (the frame was fully consumed before parse failed) so Put is correct.
func TestFleetClaim_Ready_ParseError_PutsClient(t *testing.T) {
	client := &mockFleetClient{
		readyFunc: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: true,
				Data:    []byte("{not valid json"),
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

	put, discard := pool.counts()
	if put != 1 || discard != 0 {
		t.Errorf("pool calls = put=%d discard=%d, want put=1 discard=0", put, discard)
	}
}

// TestFleetClaim_Ready_EmptyIssues_PutsClient verifies that a Ready response
// with an empty issues slice (StatusNoContent path) Puts the connection — the
// frame was fully received and there are simply no tasks to claim.
func TestFleetClaim_Ready_EmptyIssues_PutsClient(t *testing.T) {
	emptyData, _ := json.Marshal([]*types.Issue{})

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

	put, discard := pool.counts()
	if put != 1 || discard != 0 {
		t.Errorf("pool calls = put=%d discard=%d, want put=1 discard=0", put, discard)
	}
}

// TestFleetClaim_ReadyLoop_TransportError_DiscardsClient verifies that a
// transport error on tryClaimIssue's client.Update (resp == nil && err != nil)
// breaks the loop and Discards the connection — even when there are more
// issues to try. Continuing on a poisoned connection would compound damage.
func TestFleetClaim_ReadyLoop_TransportError_DiscardsClient(t *testing.T) {
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
			return nil, errors.New("EOF")
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
	}

	handler := handleFleetClaimWithPool(pool, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	put, discard := pool.counts()
	if discard != 1 || put != 0 {
		t.Errorf("pool calls = put=%d discard=%d, want put=0 discard=1", put, discard)
	}
}

// TestFleetClaim_ReadyLoop_CollisionThenSuccess_PutsClient verifies that the
// loop continues on a clean collision (!Success) and Puts on eventual success.
func TestFleetClaim_ReadyLoop_CollisionThenSuccess_PutsClient(t *testing.T) {
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
				return &rpc.Response{
					Success: false,
					Error:   "already claimed by other-worker",
				}, nil
			}
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

	put, discard := pool.counts()
	if put != 1 || discard != 0 {
		t.Errorf("pool calls = put=%d discard=%d, want put=1 discard=0", put, discard)
	}
}

// TestFleetClaim_ReadyLoop_AllCollisions_PutsClient verifies that when every
// loop iteration produces a clean collision, the handler returns
// StatusNoContent and Puts the (still healthy) connection.
func TestFleetClaim_ReadyLoop_AllCollisions_PutsClient(t *testing.T) {
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

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	put, discard := pool.counts()
	if put != 1 || discard != 0 {
		t.Errorf("pool calls = put=%d discard=%d, want put=1 discard=0", put, discard)
	}
}

// TestFleetClaim_ReadyLoop_TransportErrorAfterCollision_DiscardsClient
// verifies that a clean collision in iteration 1 followed by a transport error
// in iteration 2 still breaks the loop and Discards. The first iteration's
// healthy=true does NOT mask iteration 2's poisoned connection.
func TestFleetClaim_ReadyLoop_TransportErrorAfterCollision_DiscardsClient(t *testing.T) {
	readyIssues := []*types.Issue{
		{ID: "task-1", Title: "First", Status: "open"},
		{ID: "task-2", Title: "Second", Status: "open"},
	}
	readyData, _ := json.Marshal(readyIssues)

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
				return &rpc.Response{
					Success: false,
					Error:   "already claimed by other-worker",
				}, nil
			}
			return nil, errors.New("connection reset")
		},
	}

	pool := &mockFleetPool{
		getFunc: func(ctx context.Context) (fleetClaimClient, error) {
			return client, nil
		},
	}

	handler := handleFleetClaimWithPool(pool, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	put, discard := pool.counts()
	if discard != 1 || put != 0 {
		t.Errorf("pool calls = put=%d discard=%d, want put=0 discard=1", put, discard)
	}
}

// TestFleetClaim_ReadyLoop_Success_PutsClient verifies the happy-path: a
// successful claim on the first ready issue Puts the connection.
func TestFleetClaim_ReadyLoop_Success_PutsClient(t *testing.T) {
	readyIssues := []*types.Issue{
		{ID: "task-1", Title: "First", Status: "open"},
	}
	readyData, _ := json.Marshal(readyIssues)
	claimedData, _ := json.Marshal(types.Issue{
		ID:     "task-1",
		Title:  "First",
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

	put, discard := pool.counts()
	if put != 1 || discard != 0 {
		t.Errorf("pool calls = put=%d discard=%d, want put=1 discard=0", put, discard)
	}
}
