package agentsv1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// modelServer serves workspace "ws" with the fake harness wired as
// "opencode" and an idle agent a1 on it.
func modelServer(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	st, err := loomstore.Open(ctx, filepath.Join(t.TempDir(), "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := testAgent("a1", loomagent.StateIdle)
	a.Harness = "opencode"
	if err := st.InsertAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	svc := loomagent.New(loomagent.ServiceConfig{Store: st, WorkspaceID: "ws",
		Harnesses: map[string]loomharness.Harness{"opencode": fake.New()}})
	mux := http.NewServeMux()
	ws := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.WithWorkspace(r.Context(), r.PathValue("ws"))))
		})
	}
	New(func(string) *loomagent.Service { return svc }, nil).Register(mux, ws, nil)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestHarnessModelsRoute: the catalog groups models by provider, in the
// option-descriptor shape, and an unwired harness is harness_unavailable.
func TestHarnessModelsRoute(t *testing.T) {
	srv := modelServer(t)
	status, out := call(t, srv, "GET", "ws/v1/harnesses/opencode/models", "", "")
	providers, _ := out["providers"].([]any)
	if status != 200 || out["harness"] != "opencode" || len(providers) != 1 {
		t.Fatalf("catalog = %d %v", status, out)
	}
	p := providers[0].(map[string]any)
	models, _ := p["models"].([]any)
	if p["id"] != "fake" || p["name"] != "Fake" || len(models) != 1 {
		t.Fatalf("provider = %v", p)
	}
	m := models[0].(map[string]any)
	ds, _ := m["option_descriptors"].([]any)
	if m["id"] != "fake-model" || m["is_default"] != true || m["context_limit"] != 1000.0 || len(ds) != 1 {
		t.Fatalf("model = %v", m)
	}
	d := ds[0].(map[string]any)
	choices, _ := d["options"].([]any)
	if d["id"] != "effort" || d["type"] != "select" || d["current_value"] != "medium" || len(choices) != 3 ||
		choices[1].(map[string]any)["is_default"] != true {
		t.Fatalf("effort descriptor = %v", d)
	}
	status, out = call(t, srv, "GET", "ws/v1/harnesses/zz/models", "", "")
	want(t, "unwired harness", status, out, 503, "harness_unavailable")
}

// TestUpdateEffortRoute: PATCH effort or options is saved and checked
// against the catalog; an unknown value is a 400 that names the allowed
// ones, and a value that is neither a string nor a boolean is refused.
func TestUpdateEffortRoute(t *testing.T) {
	srv := modelServer(t)
	status, out := call(t, srv, "PATCH", "ws/v1/agents/a1", "u1", `{"effort":"high"}`)
	if status != 200 || out["spec_version"] != 2.0 || !strings.Contains(out["spec_json"].(string), `"Options":[{"ID":"effort","Value":"high"}]`) {
		t.Fatalf("patch effort = %d %v", status, out)
	}
	status, out = call(t, srv, "PATCH", "ws/v1/agents/a1", "u2", `{"options":[{"id":"effort","value":"low"}]}`)
	if status != 200 || !strings.Contains(out["spec_json"].(string), `"Value":"low"`) {
		t.Fatalf("patch options = %d %v", status, out)
	}
	status, out = call(t, srv, "PATCH", "ws/v1/agents/a1", "u3", `{"effort":"ultra"}`)
	want(t, "unknown effort", status, out, 400, "preset_invalid")
	if allowed, _ := out["allowed"].([]any); len(allowed) != 3 || !strings.Contains(out["error"].(string), `"ultra"`) {
		t.Fatalf("unknown effort error = %v", out)
	}
	status, out = call(t, srv, "PATCH", "ws/v1/agents/a1", "u4", `{"options":[{"id":"speed","value":true}]}`)
	want(t, "unknown option", status, out, 400, "preset_invalid")
	status, out = call(t, srv, "PATCH", "ws/v1/agents/a1", "u5", `{"model":"nope"}`)
	want(t, "unknown model", status, out, 400, "preset_invalid")
	status, out = call(t, srv, "PATCH", "ws/v1/agents/a1", "u6", `{"options":[{"id":"effort","value":3}]}`)
	want(t, "numeric value", status, out, 400, "")
}
