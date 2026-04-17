package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
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

func TestHandleGetTerminalConfig_NilManager(t *testing.T) {
	handler := HandleGetTerminalConfig(nil)

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
		t.Errorf("nil-manager should return zero values; got %+v", cfg)
	}
}

func TestHandleGetTerminalConfig_LocalDefaultsDisabled(t *testing.T) {
	mgr := webuterminal.NewPTYManager("", 0)
	t.Cleanup(func() { _ = mgr.Shutdown() })

	handler := HandleGetTerminalConfig(mgr)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/config/terminal", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d; body=%s", rec.Code, rec.Body.String())
	}
	_, cfg := decodeTerminalConfigResponse(t, rec.Body.Bytes())
	// Local `loom serve` defaults: both timeouts disabled (0), cap = manager's MaxSessions.
	if cfg.GracePeriodMS != 0 {
		t.Errorf("GracePeriodMS=%d want 0 (disabled for local)", cfg.GracePeriodMS)
	}
	if cfg.IdleTimeoutMS != 0 {
		t.Errorf("IdleTimeoutMS=%d want 0 (disabled for local)", cfg.IdleTimeoutMS)
	}
	if cfg.MaxSessions != mgr.MaxSessions() {
		t.Errorf("MaxSessions=%d want %d", cfg.MaxSessions, mgr.MaxSessions())
	}
}

func TestHandleGetTerminalConfig_ReflectsOverrides(t *testing.T) {
	mgr := webuterminal.NewPTYManager("", 0)
	t.Cleanup(func() { _ = mgr.Shutdown() })

	mgr.SetGracePeriod(15 * time.Minute)
	mgr.SetIdleTimeout(45 * time.Minute)

	handler := HandleGetTerminalConfig(mgr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/terminal", nil))

	_, cfg := decodeTerminalConfigResponse(t, rec.Body.Bytes())
	if got, want := cfg.GracePeriodMS, (15 * time.Minute).Milliseconds(); got != want {
		t.Errorf("GracePeriodMS=%d want %d", got, want)
	}
	if got, want := cfg.IdleTimeoutMS, (45 * time.Minute).Milliseconds(); got != want {
		t.Errorf("IdleTimeoutMS=%d want %d", got, want)
	}
}
