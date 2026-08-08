package fleet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFleetRegisterRejectsTrailingJSONBeforeMutation(t *testing.T) {
	called := false
	store := &mockWorkerRegistrar{registerFunc: func(context.Context, *Worker) error {
		called = true
		return nil
	}}
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/fleet/register",
		strings.NewReader(`{"worker_id":"worker-1"} {"worker_id":"worker-2"}`),
	)
	setFleetAPIKeyHeader(req)
	rec := httptest.NewRecorder()

	handleFleetRegisterWithStore(store, testTokenConfig(), testFleetRegCfg()).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("RegisterWorker was called for a request with trailing JSON")
	}
}
