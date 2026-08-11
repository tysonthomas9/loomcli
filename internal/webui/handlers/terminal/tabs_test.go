package terminal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/webui/localredis"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

func newTerminalTabHTTPTestService(t *testing.T) webuterminal.TerminalService {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return webuterminal.NewTerminalService(
		nil,
		localredis.NewTabMetadataStore(rdb, nil),
		nil,
		rdb,
		nil,
		time.Now().UTC(),
	)
}

func terminalTabPutRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/E2E/terminal/tabs/lead-codex-1", strings.NewReader(body))
	req.SetPathValue("session", "lead-codex-1")
	return req.WithContext(middleware.WithWorkspace(req.Context(), "E2E"))
}

func TestPutTerminalTabPersistsServerDerivedLaunchEnvelope(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", "/trusted/loom-data")
	svc := newTerminalTabHTTPTestService(t)
	rec := httptest.NewRecorder()

	HandlePutTerminalTab(svc).ServeHTTP(rec, terminalTabPutRequest(
		t,
		`{"backend":"codex","label":"Codex","sort_order":0,"notes":"","pinned":false}`,
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	meta, err := svc.GetTab(t.Context(), "E2E", "lead-codex-1")
	if err != nil {
		t.Fatalf("GetTab: %v", err)
	}
	if meta.Backend != "codex" || meta.Launch == nil || len(meta.Launch.Argv) != 2 {
		t.Fatalf("persisted metadata = %#v", meta)
	}
	if got := meta.Launch.Env["LOOM_CONFIG_DIR"]; got != "/trusted/loom-data" {
		t.Fatalf("LOOM_CONFIG_DIR = %q", got)
	}
}

func TestPutTerminalTabRejectsMissingOrUnknownBackend(t *testing.T) {
	for name, body := range map[string]string{
		"missing": `{"label":"Codex","sort_order":0,"notes":"","pinned":false}`,
		"unknown": `{"backend":"codex-1","label":"Codex","sort_order":0,"notes":"","pinned":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			svc := newTerminalTabHTTPTestService(t)
			rec := httptest.NewRecorder()
			HandlePutTerminalTab(svc).ServeHTTP(rec, terminalTabPutRequest(t, body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			if _, err := svc.GetTab(t.Context(), "E2E", "lead-codex-1"); err == nil {
				t.Fatal("invalid create persisted tab metadata")
			}
		})
	}
}
