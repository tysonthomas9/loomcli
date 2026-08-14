package terminal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/infra/localredis"
	webuterminal "github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func newTestTerminalService(
	_ any,
	store webuterminal.TabMetadataStore,
	_ any,
	_ any,
	runtime webuterminal.TerminalRuntime,
	startedAt time.Time,
) webuterminal.TerminalTabs {
	return webuterminal.NewTerminalTabs(store, runtime, startedAt, webuterminal.TerminalDependencies{})
}

func newTerminalTabHTTPTestService(t *testing.T) webuterminal.TerminalTabs {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return newTestTerminalService(
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

func TestPutTerminalTabPersistsOwnerDerivedLaunchEnvelope(t *testing.T) {
	svc := newTerminalTabHTTPTestService(t)
	rec := httptest.NewRecorder()

	HandlePutTerminalTab(svc).ServeHTTP(rec, terminalTabPutRequest(
		t,
		`{"backend":"codex","label":"Codex","sort_order":0,"notes":"","pinned":false}`,
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"launch"`) || strings.Contains(rec.Body.String(), `"argv"`) {
		t.Fatalf("PUT response leaked private launch envelope: %s", rec.Body.String())
	}

	meta, err := svc.GetTab(t.Context(), "E2E", "lead-codex-1")
	if err != nil {
		t.Fatalf("GetTab: %v", err)
	}
	if meta.Backend != "codex" || meta.Launch == nil || len(meta.Launch.Argv) != 2 {
		t.Fatalf("persisted metadata = %#v", meta)
	}
	if len(meta.Launch.Env) != 0 {
		t.Fatalf("delivery unexpectedly supplied launch environment: %#v", meta.Launch.Env)
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
