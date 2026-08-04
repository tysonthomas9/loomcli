package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// decodeTerminalConfigResponse extracts the typed payload from the wrapped
// {success, data} envelope.
func decodeTerminalConfigResponse(t *testing.T, body []byte) (success bool, cfg TerminalLifecycleConfig) {
	t.Helper()
	var envelope struct {
		Success bool                    `json:"success"`
		Data    TerminalLifecycleConfig `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, body)
	}
	return envelope.Success, envelope.Data
}

func TestHandleGetTerminalConfig_ZeroValues(t *testing.T) {
	// Zero-value config: caller has no PTY manager wired.
	handler := HandleGetTerminalConfig(TerminalLifecycleConfig{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/config/terminal", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	ok, cfg := decodeTerminalConfigResponse(t, rec.Body.Bytes())
	if !ok {
		t.Errorf("success=false; body=%s", rec.Body.String())
	}
	if cfg.GracePeriodMS != 0 || cfg.IdleTimeoutMS != 0 || cfg.MaxSessions != 0 {
		t.Errorf("zero-value config should round-trip to zeros; got %+v", cfg)
	}
}

func TestHandleGetTerminalConfig_ServesSuppliedSnapshot(t *testing.T) {
	// Typical local-serve snapshot: grace/idle disabled, cap at 40.
	in := TerminalLifecycleConfig{GracePeriodMS: 0, IdleTimeoutMS: 0, MaxSessions: 40}
	handler := HandleGetTerminalConfig(in)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/terminal", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d; body=%s", rec.Code, rec.Body.String())
	}
	_, cfg := decodeTerminalConfigResponse(t, rec.Body.Bytes())
	if cfg != in {
		t.Errorf("snapshot not round-tripped: got %+v want %+v", cfg, in)
	}
}

func TestHandleGetTerminalConfig_ReflectsNonZeroSnapshot(t *testing.T) {
	in := TerminalLifecycleConfig{
		GracePeriodMS: 15 * 60 * 1000,
		IdleTimeoutMS: 45 * 60 * 1000,
		MaxSessions:   128,
	}
	handler := HandleGetTerminalConfig(in)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/terminal", nil))

	_, cfg := decodeTerminalConfigResponse(t, rec.Body.Bytes())
	if cfg != in {
		t.Errorf("non-zero snapshot not round-tripped: got %+v want %+v", cfg, in)
	}
}
