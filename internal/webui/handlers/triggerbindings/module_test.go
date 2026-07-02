package triggerbindings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// seededMux returns a mux wired to a memstore that already has a workflow
// driver ("driver-1") with a validated active version ("version-1").
func seededMux(t *testing.T) (*http.ServeMux, store.Store) {
	t.Helper()
	s := memstore.New()
	ctx := context.Background()
	if _, err := s.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "WS"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     "driver-1",
		Name:         "epic-runner",
		OwnerType:    domain.DriverOwnerSystem,
		Status:       domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "WS",
		VersionID:        "version-1",
		DriverID:         "driver-1",
		Version:          1,
		SourceDigest:     "sha256:source",
		BundleDigest:     "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
	mux := http.NewServeMux()
	NewModule(s).Register(mux)
	return mux, s
}

func do(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestCreateBinding_CreatesThenDisables(t *testing.T) {
	mux, _ := seededMux(t)

	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{"driver_id":"driver-1","driver_version_id":"version-1","route_key":"epics.runs.create","source_kind":"http","name":"epic-runner-binding","enabled":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var binding domain.TriggerBinding
	if err := json.Unmarshal(rec.Body.Bytes(), &binding); err != nil {
		t.Fatalf("decode binding: %v", err)
	}
	if binding.RouteKey != "epics.runs.create" || binding.DriverID != "driver-1" || !binding.Enabled {
		t.Fatalf("unexpected binding: %+v", binding)
	}

	// Disabling flips Enabled without a request body.
	rec2 := do(t, mux, http.MethodPost,
		"/api/workspaces/WS/trigger-bindings/"+binding.BindingID+"/disable", "")
	if rec2.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
	var disabled domain.TriggerBinding
	if err := json.Unmarshal(rec2.Body.Bytes(), &disabled); err != nil {
		t.Fatalf("decode disabled: %v", err)
	}
	if disabled.Enabled {
		t.Fatalf("binding should be disabled after /disable")
	}
}

// TestCreateBinding_IsIdempotent pins the ensure contract the create-agent
// gallery relies on: re-activating the same template (same binding_id) returns
// 200 with the existing binding rather than a 409 that would fail activation
// before it reaches the connector/grant steps.
func TestCreateBinding_IsIdempotent(t *testing.T) {
	mux, _ := seededMux(t)
	body := `{"driver_id":"driver-1","driver_version_id":"version-1","route_key":"epics.runs.create","source_kind":"http","binding_id":"b-fixed","enabled":true}`

	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	rec2 := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", body)
	if rec2.Code != http.StatusOK {
		t.Fatalf("re-create status = %d, want 200 ensure; body=%s", rec2.Code, rec2.Body.String())
	}
	var binding domain.TriggerBinding
	if err := json.Unmarshal(rec2.Body.Bytes(), &binding); err != nil {
		t.Fatalf("decode ensure binding: %v", err)
	}
	if binding.BindingID != "b-fixed" {
		t.Fatalf("ensure returned binding_id = %q, want b-fixed", binding.BindingID)
	}
}

// TestCreateBinding_CronDerivesRouteKey pins the Phase-A fix: a cron binding
// needs no route_key — it is derived from the unique binding_id — so two
// scheduled workflows coexist in one workspace instead of colliding on a shared
// hand-picked route string.
func TestCreateBinding_CronDerivesRouteKey(t *testing.T) {
	mux, _ := seededMux(t)
	base := `"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","schedule":"*/10 * * * *","enabled":true`

	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{`+base+`,"binding_id":"s1-bug-fix"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("cron create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var b domain.TriggerBinding
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode binding: %v", err)
	}
	if b.RouteKey != "cron:s1-bug-fix" {
		t.Fatalf("derived route_key = %q, want cron:s1-bug-fix", b.RouteKey)
	}

	// A second scheduled workflow (different binding_id, no route_key) must
	// coexist — distinct derived routes, no 409 on the shared route.
	rec2 := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{`+base+`,"binding_id":"s2-review-loop"}`)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second cron create status = %d, want 201 (coexist); body=%s", rec2.Code, rec2.Body.String())
	}

	// Neither binding_id nor route_key is a 400 (nothing to address the binding).
	rec3 := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", `{`+base+`}`)
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("cron without binding_id status = %d, want 400; body=%s", rec3.Code, rec3.Body.String())
	}
}

// TestListBindings_NextFireAt pins the Phase-1 computed field: an enabled cron
// binding carries a future next_fire_at, while disabled cron and non-cron
// bindings omit it.
func TestListBindings_NextFireAt(t *testing.T) {
	mux, _ := seededMux(t)

	create := func(body string) {
		t.Helper()
		if rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", body); rec.Code != http.StatusCreated {
			t.Fatalf("create status = %d; body=%s", rec.Code, rec.Body.String())
		}
	}
	create(`{"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","schedule":"*/5 * * * *","binding_id":"cron-enabled","enabled":true}`)
	create(`{"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","schedule":"*/5 * * * *","binding_id":"cron-disabled","enabled":false}`)
	create(`{"driver_id":"driver-1","driver_version_id":"version-1","route_key":"github.pr.opened","source_kind":"http","binding_id":"http-enabled","enabled":true}`)

	rec := do(t, mux, http.MethodGet, "/api/workspaces/WS/trigger-bindings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Bindings []struct {
			BindingID  string     `json:"binding_id"`
			NextFireAt *time.Time `json:"next_fire_at"`
		} `json:"bindings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	byID := map[string]*time.Time{}
	for _, b := range resp.Bindings {
		byID[b.BindingID] = b.NextFireAt
	}
	if len(byID) != 3 {
		t.Fatalf("listed bindings = %d, want 3 (%+v)", len(byID), resp.Bindings)
	}
	if next := byID["cron-enabled"]; next == nil || !next.After(time.Now()) {
		t.Fatalf("cron-enabled next_fire_at = %v, want a future instant", next)
	}
	if next := byID["cron-disabled"]; next != nil {
		t.Fatalf("cron-disabled next_fire_at = %v, want absent", next)
	}
	if next := byID["http-enabled"]; next != nil {
		t.Fatalf("http-enabled next_fire_at = %v, want absent", next)
	}
}

func TestCreateBinding_RequiresRouteKey(t *testing.T) {
	mux, _ := seededMux(t)
	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{"driver_id":"driver-1","driver_version_id":"version-1"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateBinding_GithubRequiresSecret(t *testing.T) {
	mux, _ := seededMux(t)
	// Enabled github binding with no secret must be rejected before it is stored.
	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{"workflow":"github-review-agent","route_key":"github.pull_request.opened","source_kind":"github","enabled":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("expected secret-required error, got %s", rec.Body.String())
	}
}
