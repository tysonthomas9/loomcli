package webui

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

func TestServerConfigWrappersAndBackendsHealth(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Port != DefaultPort || cfg.BindAddress != "127.0.0.1" || cfg.PoolSize != DefaultPoolSize || cfg.MaxPortAttempts != DefaultMaxPortAttempts {
		t.Fatalf("DefaultConfig = %+v", cfg)
	}
	if cwd, err := GetCwd(); err != nil || cwd == "" {
		t.Fatalf("GetCwd = %q err=%v", cwd, err)
	}

	listener, port, err := FindAvailablePort("127.0.0.1", 0, 1)
	if err != nil {
		t.Logf("FindAvailablePort success path skipped: %v", err)
	} else {
		if port != 0 {
			t.Fatalf("FindAvailablePort port = %d, want requested start port 0", port)
		}
		_ = listener.Close()
	}
	if listener, _, err := FindAvailablePort("127.0.0.1", 43210, 0); err == nil || listener != nil {
		t.Fatalf("FindAvailablePort zero attempts listener=%v err=%v", listener, err)
	}

	initLogger(nil)
	custom := slog.New(slog.NewTextHandler(testingWriter{t: t}, nil))
	initLogger(custom)
	if logger != custom {
		t.Fatal("initLogger did not install custom logger")
	}

	module := NewAgentControlModule(nil)
	if module == nil {
		t.Fatal("NewAgentControlModule returned nil")
	}
	outer, inner := TracingWithRouteName()
	traced := outer(inner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Pattern = "GET /traced"
		w.WriteHeader(http.StatusNoContent)
	})))
	rec := httptest.NewRecorder()
	traced.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/traced", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("traced status=%d", rec.Code)
	}

	rec = httptest.NewRecorder()
	HandleBackendsHealth(fakeBackendOps{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/backends/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("backends status=%d body=%s", rec.Code, rec.Body.String())
	}
	var okBody backendsHealthResp
	if err := json.Unmarshal(rec.Body.Bytes(), &okBody); err != nil {
		t.Fatalf("decode backends: %v", err)
	}
	if !okBody.Success || okBody.Data == nil || len(okBody.Data) != 0 {
		t.Fatalf("backends body = %#v", okBody)
	}

	rec = httptest.NewRecorder()
	HandleBackendsHealth(fakeBackendOps{err: errors.New("boom")}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/backends/health", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("backends error status=%d body=%s", rec.Code, rec.Body.String())
	}
}

type fakeBackendOps struct {
	err error
}

func (f fakeBackendOps) ListBackendsHealth() ([]ops.BackendHealth, error) {
	if f.err != nil {
		return nil, f.err
	}
	return nil, nil
}

type testingWriter struct {
	t *testing.T
}

func (w testingWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
