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
