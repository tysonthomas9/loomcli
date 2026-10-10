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
// ones, and a value that is neither a string nor a boolean is refused. An
// unlisted model passes flagged model_unverified; a malformed one is a 400.
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
	status, out = call(t, srv, "PATCH", "ws/v1/agents/a1", "u5", `{"model":"nope"}`) // MCS1: passes, unverified
	if status != 200 || out["model"] != "nope" || out["model_unverified"] != true {
		t.Fatalf("patch unlisted model = %d %v; want 200 with model_unverified", status, out)
	}
	status, out = call(t, srv, "PATCH", "ws/v1/agents/a1", "u7", `{"model":"openai/"}`)
	want(t, "malformed model", status, out, 400, "preset_invalid")
	status, out = call(t, srv, "PATCH", "ws/v1/agents/a1", "u6", `{"options":[{"id":"effort","value":3}]}`)
	want(t, "numeric value", status, out, 400, "")
}

// TestCustomModelsRoute (MCS3): PUT sets the workspace's custom model ids,
// GET reads them, the catalog lists them under "custom" with source custom,
// a PATCH to one is not model_unverified, and a PUT without it removes it.
func TestCustomModelsRoute(t *testing.T) {
	srv := modelServer(t)
	status, out := call(t, srv, "GET", "ws/v1/harnesses/opencode/custom", "", "")
	if ms, _ := out["models"].([]any); status != 200 || ms == nil || len(ms) != 0 {
		t.Fatalf("no custom models = %d %v", status, out)
	}
	status, out = call(t, srv, "PUT", "ws/v1/harnesses/opencode/custom", "", `{"models":["openai/mine","openai/mine"]}`)
	if ms, _ := out["models"].([]any); status != 200 || len(ms) != 1 || ms[0] != "openai/mine" {
		t.Fatalf("put = %d %v", status, out)
	}
	status, out = call(t, srv, "PUT", "ws/v1/harnesses/opencode/custom", "", `{"models":["bad id"]}`)
	want(t, "malformed custom id", status, out, 400, "preset_invalid")
	status, out = call(t, srv, "GET", "ws/v1/harnesses/opencode/models", "", "")
	providers, _ := out["providers"].([]any)
	if status != 200 || len(providers) != 2 {
		t.Fatalf("catalog = %d %v", status, out)
	}
	p := providers[1].(map[string]any)
	models, _ := p["models"].([]any)
	if p["id"] != "custom" || len(models) != 1 {
		t.Fatalf("custom provider = %v", p)
	}
	if m := models[0].(map[string]any); m["id"] != "openai/mine" || m["source"] != "custom" || len(m["option_descriptors"].([]any)) != 1 {
		t.Fatalf("custom model = %v", m)
	}
	if m := providers[0].(map[string]any)["models"].([]any)[0].(map[string]any); m["source"] != "harness" {
		t.Fatalf("harness model = %v", m)
	}
	status, out = call(t, srv, "PATCH", "ws/v1/agents/a1", "u1", `{"model":"openai/mine","effort":"high"}`)
	if status != 200 || out["model"] != "openai/mine" || out["model_unverified"] == true {
		t.Fatalf("patch custom model = %d %v; want 200 without model_unverified", status, out)
	}
	status, out = call(t, srv, "PUT", "ws/v1/harnesses/opencode/custom", "", `{"models":[]}`)
	want(t, "remove", status, out, 200, "")
	status, out = call(t, srv, "GET", "ws/v1/harnesses/opencode/models", "", "")
	if providers, _ := out["providers"].([]any); status != 200 || len(providers) != 1 {
		t.Fatalf("catalog after remove = %d %v", status, out)
	}
}

// TestLimitResumeRoute (OR7): usage-limit auto-resume is off until a PUT
// turns it on for the workspace; GET reads it.
func TestLimitResumeRoute(t *testing.T) {
	srv := modelServer(t)
	status, out := call(t, srv, "GET", "ws/v1/settings/limit-resume", "", "")
	if status != 200 || out["enabled"] != false {
		t.Fatalf("default = %d %v; want off", status, out)
	}
	for _, on := range []bool{true, false} {
		body := `{"enabled":false}`
		if on {
			body = `{"enabled":true}`
		}
		if status, out = call(t, srv, "PUT", "ws/v1/settings/limit-resume", "", body); status != 200 || out["enabled"] != on {
			t.Fatalf("put %s = %d %v", body, status, out)
		}
		if status, out = call(t, srv, "GET", "ws/v1/settings/limit-resume", "", ""); status != 200 || out["enabled"] != on {
			t.Fatalf("get after put %s = %d %v", body, status, out)
		}
	}
}
