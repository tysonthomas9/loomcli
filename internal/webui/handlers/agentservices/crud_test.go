package agentservices

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func TestCreateAgentServiceInstance(t *testing.T) {
	st, mux := newAgentServiceCRUDHarness(t)
	rec := serveAgentServiceMutation(mux, http.MethodPost, "/api/workspaces/WS/agent-services", `{
		"id":"scout-west","name":"Scout West","role":"scout",
		"binding":{"schedule":"0 9 * * 1-5","timezone":"America/Los_Angeles","enabled":true}
	}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var response struct {
		Success bool            `json:"success"`
		Data    agentServiceDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success || response.Data.ID != "scout-west" || response.Data.Behavior.RoleName != "scout" ||
		response.Data.TriggerKind != "cron" || len(response.Data.Bindings) != 1 {
		t.Fatalf("response = %#v", response)
	}
	binding := response.Data.Bindings[0]
	if binding.ID != "binding-cron-scout-west" || binding.RouteKey != "cron.scout-west" ||
		binding.Schedule != "0 9 * * 1-5" || binding.Timezone != "America/Los_Angeles" || !binding.Enabled {
		t.Fatalf("binding = %#v", binding)
	}
	svc, err := st.AgentServices().Get(t.Context(), "WS", "scout-west")
	if err != nil || svc.RoleName != "scout" || svc.DriverID != "" || svc.DriverVersionID != "" ||
		svc.DesiredState != domain.AgentServiceDesiredRunning {
		t.Fatalf("stored service = %#v, err %v", svc, err)
	}
}

func TestCreateAgentServiceRejectsInvalidIDs(t *testing.T) {
	for _, id := range []string{"", "Scout", "-scout", "scout_2", "scout/2", strings.Repeat("a", 65)} {
		t.Run(id, func(t *testing.T) {
			_, mux := newAgentServiceCRUDHarness(t)
			body := `{"id":` + strconv.Quote(id) + `,"role":"scout","binding":{"schedule":"@daily"}}`
			rec := serveAgentServiceMutation(mux, http.MethodPost, "/api/workspaces/WS/agent-services", body)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "must match") {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestCreateAgentServiceRejectsPlainPromptRole(t *testing.T) {
	st, mux := newAgentServiceCRUDHarness(t)
	seedPlainRole(t, st, "reviewer")
	rec := serveAgentServiceMutation(mux, http.MethodPost, "/api/workspaces/WS/agent-services",
		`{"id":"reviewer-one","role":"reviewer","binding":{"schedule":"@daily"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not a scripted role") {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateAgentServiceRejectsDuplicateAndArchivedTombstone(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		_, mux := newAgentServiceCRUDHarness(t)
		body := `{"id":"scout-west","role":"scout","binding":{"schedule":"@daily"}}`
		if first := serveAgentServiceMutation(mux, http.MethodPost, "/api/workspaces/WS/agent-services", body); first.Code != http.StatusCreated {
			t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
		}
		second := serveAgentServiceMutation(mux, http.MethodPost, "/api/workspaces/WS/agent-services", body)
		if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), "already exists") {
			t.Fatalf("second status = %d body=%s", second.Code, second.Body.String())
		}
	})

	t.Run("archived tombstone", func(t *testing.T) {
		st, mux := newAgentServiceCRUDHarness(t)
		body := `{"id":"scout-west","role":"scout","binding":{"schedule":"@daily"}}`
		if first := serveAgentServiceMutation(mux, http.MethodPost, "/api/workspaces/WS/agent-services", body); first.Code != http.StatusCreated {
			t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
		}
		if err := st.TriggerBindings().Delete(t.Context(), "WS", "binding-cron-scout-west"); err != nil {
			t.Fatalf("delete binding: %v", err)
		}
		if err := st.AgentServices().Delete(t.Context(), "WS", "scout-west"); err != nil {
			t.Fatalf("archive service: %v", err)
		}
		second := serveAgentServiceMutation(mux, http.MethodPost, "/api/workspaces/WS/agent-services", body)
		if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), "archived tombstone") {
			t.Fatalf("second status = %d body=%s", second.Code, second.Body.String())
		}
	})
}

func TestPatchAgentServiceInstanceMatrix(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		check func(*testing.T, store.Store)
	}{
		{
			name: "name", body: `{"name":"Renamed scout"}`,
			check: func(t *testing.T, st store.Store) {
				svc, _ := st.AgentServices().Get(t.Context(), "WS", "scout-west")
				if svc.Name != "Renamed scout" {
					t.Fatalf("name = %q", svc.Name)
				}
			},
		},
		{
			name: "desired state", body: `{"desiredState":"stopped"}`,
			check: func(t *testing.T, st store.Store) {
				svc, _ := st.AgentServices().Get(t.Context(), "WS", "scout-west")
				if svc.DesiredState != domain.AgentServiceDesiredStopped {
					t.Fatalf("desired state = %q", svc.DesiredState)
				}
			},
		},
		{
			name: "binding schedule", body: `{"binding":{"schedule":"*/15 * * * *"}}`,
			check: func(t *testing.T, st store.Store) {
				binding, _ := st.TriggerBindings().Get(t.Context(), "WS", "binding-cron-scout-west")
				if binding.Schedule != "*/15 * * * *" {
					t.Fatalf("schedule = %q", binding.Schedule)
				}
			},
		},
		{
			name: "binding timezone", body: `{"binding":{"timezone":"America/New_York"}}`,
			check: func(t *testing.T, st store.Store) {
				binding, _ := st.TriggerBindings().Get(t.Context(), "WS", "binding-cron-scout-west")
				if binding.ScheduleTimezone != "America/New_York" {
					t.Fatalf("timezone = %q", binding.ScheduleTimezone)
				}
			},
		},
		{
			name: "binding enabled", body: `{"binding":{"enabled":false}}`,
			check: func(t *testing.T, st store.Store) {
				binding, _ := st.TriggerBindings().Get(t.Context(), "WS", "binding-cron-scout-west")
				if binding.Enabled {
					t.Fatal("binding remained enabled")
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, mux := newAgentServiceCRUDHarness(t)
			createCRUDScout(t, mux)
			rec := serveAgentServiceMutation(mux, http.MethodPatch, "/api/workspaces/WS/agent-services/scout-west", tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			tc.check(t, st)
		})
	}
}

func TestPatchAgentServiceRejectsImmutableIdentityAndInvalidState(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "id", body: `{"id":"other"}`, want: "id is immutable"},
		{name: "role", body: `{"role":"epic-runner"}`, want: "role is immutable"},
		{name: "paused", body: `{"desiredState":"paused"}`, want: "running or stopped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, mux := newAgentServiceCRUDHarness(t)
			createCRUDScout(t, mux)
			rec := serveAgentServiceMutation(mux, http.MethodPatch, "/api/workspaces/WS/agent-services/scout-west", tc.body)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDeleteAgentServiceIsOrderedAndIdempotent(t *testing.T) {
	st, mux := newAgentServiceCRUDHarness(t)
	createCRUDScout(t, mux)
	for attempt := 1; attempt <= 2; attempt++ {
		rec := serveAgentServiceMutation(mux, http.MethodDelete, "/api/workspaces/WS/agent-services/scout-west", "")
		if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
			t.Fatalf("attempt %d status = %d body=%s", attempt, rec.Code, rec.Body.String())
		}
	}
	svc, err := st.AgentServices().Get(t.Context(), "WS", "scout-west")
	if err != nil || svc.DeletedAt == nil || svc.DesiredState != domain.AgentServiceDesiredStopped {
		t.Fatalf("deleted service = %#v, err %v", svc, err)
	}
	if _, err := st.TriggerBindings().Get(t.Context(), "WS", "binding-cron-scout-west"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted binding err = %v, want ErrNotFound", err)
	}
}

func TestAgentServiceMutationsAreFileAccessGated(t *testing.T) {
	st := memstore.New()
	mux := http.NewServeMux()
	NewModule(st).Register(mux)
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: "/api/workspaces/WS/agent-services", body: `{"id":"scout-west","role":"scout","binding":{"schedule":"@daily"}}`},
		{method: http.MethodPatch, path: "/api/workspaces/WS/agent-services/scout-west", body: `{"name":"renamed"}`},
		{method: http.MethodDelete, path: "/api/workspaces/WS/agent-services/scout-west"},
	} {
		rec := serveAgentServiceMutation(mux, tc.method, tc.path, tc.body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s status = %d body=%s, want 403", tc.method, rec.Code, rec.Body.String())
		}
	}
}

func createCRUDScout(t *testing.T, mux *http.ServeMux) {
	t.Helper()
	rec := serveAgentServiceMutation(mux, http.MethodPost, "/api/workspaces/WS/agent-services",
		`{"id":"scout-west","role":"scout","binding":{"schedule":"@daily","enabled":true}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create fixture status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func newAgentServiceCRUDHarness(t *testing.T) (*memstore.Store, *http.ServeMux) {
	t.Helper()
	installAgentServiceFakeFlueBuild(t)
	workspaceDir := t.TempDir()
	t.Chdir(workspaceDir)
	st := memstore.New()
	mux := http.NewServeMux()
	NewModuleWithAccess(st, workspaceDir, middleware.FileAccessConfig{FrontendOrigins: []string{"http://localhost"}}).Register(mux)
	return st, mux
}

func serveAgentServiceMutation(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func installAgentServiceFakeFlueBuild(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-flue")
	body := `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output" ]; then
    shift
    out="$1"
  fi
  shift
done
mkdir -p "$out"
cat > "$out/server.mjs" <<'EOF'
export async function run() { return {}; }
EOF
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake flue: %v", err)
	}
	sdkRoot := filepath.Join(dir, "sdk")
	runtimeRoot := filepath.Join(dir, "runtime")
	for _, dep := range []string{sdkRoot, runtimeRoot, filepath.Join(runtimeRoot, "node_modules", "@hono", "node-server"), filepath.Join(runtimeRoot, "node_modules", "hono")} {
		if err := os.MkdirAll(dep, 0o755); err != nil {
			t.Fatalf("create fake dependency %s: %v", dep, err)
		}
	}
	if err := os.WriteFile(filepath.Join(sdkRoot, "package.json"), []byte(`{"name":"@loom/sdk"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write fake sdk package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "package.json"), []byte(`{"name":"@flue/runtime"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write fake runtime package: %v", err)
	}
	t.Setenv("LOOM_REAL_FLUE_CMD", script)
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", "")
	t.Setenv("LOOM_SDK_ROOT", sdkRoot)
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", runtimeRoot)
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")
}

func seedPlainRole(t *testing.T, st store.Store, name string) {
	t.Helper()
	if _, err := st.Roles().Create(t.Context(), store.RoleCreate{WorkspaceKey: "WS", Name: name, Kind: string(domain.RoleKindWorker)}); err != nil {
		t.Fatalf("create plain role: %v", err)
	}
}
