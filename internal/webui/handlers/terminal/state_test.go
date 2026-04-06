package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupTerminalStateTest(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return rdb
}

// TestHandleGetTerminalState_Empty tests that GET /api/terminal/state returns
// an empty active_tab when no state has been set.
func TestHandleGetTerminalState_Empty(t *testing.T) {
	rdb := setupTerminalStateTest(t)
	handler := handleGetTerminalState(NewTerminalService(nil, nil, nil, nil, nil, nil, rdb))

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/state", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// When no state has been set, active_tab should be nil (JSON null) or empty string.
	activeTab := resp["active_tab"]
	if activeTab != nil && activeTab != "" {
		t.Errorf("expected active_tab to be nil or empty, got %v", activeTab)
	}
}

// TestHandlePatchTerminalState_WriteAndRead tests that PATCH writes state and
// a subsequent GET reads back the same value.
func TestHandlePatchTerminalState_WriteAndRead(t *testing.T) {
	rdb := setupTerminalStateTest(t)
	getHandler := handleGetTerminalState(NewTerminalService(nil, nil, nil, nil, nil, nil, rdb))
	patchHandler := handlePatchTerminalState(NewTerminalService(nil, nil, nil, nil, nil, nil, rdb))

	// PATCH to set active_tab.
	body := `{"active_tab": "session-abc"}`
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/terminal/state", strings.NewReader(body))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRR := httptest.NewRecorder()
	patchHandler(patchRR, patchReq)

	if patchRR.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want %d, body: %s", patchRR.Code, http.StatusOK, patchRR.Body.String())
	}

	var patchResp map[string]interface{}
	if err := json.NewDecoder(patchRR.Body).Decode(&patchResp); err != nil {
		t.Fatalf("decode PATCH response: %v", err)
	}
	if patchResp["active_tab"] != "session-abc" {
		t.Errorf("PATCH response active_tab = %v, want 'session-abc'", patchResp["active_tab"])
	}

	// GET to verify the value was persisted.
	getReq := httptest.NewRequest(http.MethodGet, "/api/terminal/state", nil)
	getRR := httptest.NewRecorder()
	getHandler(getRR, getReq)

	if getRR.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", getRR.Code, http.StatusOK)
	}

	var getResp map[string]interface{}
	if err := json.NewDecoder(getRR.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if getResp["active_tab"] != "session-abc" {
		t.Errorf("GET active_tab = %v, want 'session-abc'", getResp["active_tab"])
	}
}

// TestHandlePatchTerminalState_InvalidBody tests that PATCH returns 400 for
// invalid JSON body.
func TestHandlePatchTerminalState_InvalidBody(t *testing.T) {
	rdb := setupTerminalStateTest(t)
	handler := handlePatchTerminalState(NewTerminalService(nil, nil, nil, nil, nil, nil, rdb))

	body := `{not valid json}`
	req := httptest.NewRequest(http.MethodPatch, "/api/terminal/state", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["error"] != "invalid request body" {
		t.Errorf("expected error 'invalid request body', got %q", resp["error"])
	}
}

// TestHandlePatchTerminalState_Overwrite tests that a second PATCH overwrites
// the previous value.
func TestHandlePatchTerminalState_Overwrite(t *testing.T) {
	rdb := setupTerminalStateTest(t)
	getHandler := handleGetTerminalState(NewTerminalService(nil, nil, nil, nil, nil, nil, rdb))
	patchHandler := handlePatchTerminalState(NewTerminalService(nil, nil, nil, nil, nil, nil, rdb))

	// First PATCH.
	body1 := `{"active_tab": "first-tab"}`
	req1 := httptest.NewRequest(http.MethodPatch, "/api/terminal/state", strings.NewReader(body1))
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	patchHandler(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("first PATCH status = %d, want %d", rr1.Code, http.StatusOK)
	}

	// Second PATCH with a different value.
	body2 := `{"active_tab": "second-tab"}`
	req2 := httptest.NewRequest(http.MethodPatch, "/api/terminal/state", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	patchHandler(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("second PATCH status = %d, want %d", rr2.Code, http.StatusOK)
	}

	// GET should return the second value.
	getReq := httptest.NewRequest(http.MethodGet, "/api/terminal/state", nil)
	getRR := httptest.NewRecorder()
	getHandler(getRR, getReq)

	if getRR.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", getRR.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(getRR.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["active_tab"] != "second-tab" {
		t.Errorf("active_tab = %v, want 'second-tab'", resp["active_tab"])
	}
}

// TestHandlePatchTerminalState_EmptyString tests that active_tab can be set
// to an empty string (clearing the state).
func TestHandlePatchTerminalState_EmptyString(t *testing.T) {
	rdb := setupTerminalStateTest(t)
	getHandler := handleGetTerminalState(NewTerminalService(nil, nil, nil, nil, nil, nil, rdb))
	patchHandler := handlePatchTerminalState(NewTerminalService(nil, nil, nil, nil, nil, nil, rdb))

	// First set a value.
	body1 := `{"active_tab": "some-tab"}`
	req1 := httptest.NewRequest(http.MethodPatch, "/api/terminal/state", strings.NewReader(body1))
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	patchHandler(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("first PATCH status = %d", rr1.Code)
	}

	// Then clear it.
	body2 := `{"active_tab": ""}`
	req2 := httptest.NewRequest(http.MethodPatch, "/api/terminal/state", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	patchHandler(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("second PATCH status = %d", rr2.Code)
	}

	// GET should return empty.
	getReq := httptest.NewRequest(http.MethodGet, "/api/terminal/state", nil)
	getRR := httptest.NewRecorder()
	getHandler(getRR, getReq)

	var resp map[string]interface{}
	if err := json.NewDecoder(getRR.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["active_tab"] != "" {
		t.Errorf("expected active_tab to be empty, got %v", resp["active_tab"])
	}
}
