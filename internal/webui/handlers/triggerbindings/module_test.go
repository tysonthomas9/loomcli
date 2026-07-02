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

// TestCreateBinding_RunInputStoredOnSourceConfigRef pins ITEM D's plumbing: a
// prompt-agent binding created with a run_input object stores it on the binding's
// source_config_ref, where the dispatch source (CronScheduler) merges it into the
// fired run payload.
func TestCreateBinding_RunInputStoredOnSourceConfigRef(t *testing.T) {
	mux, _ := seededMux(t)
	rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","schedule":"*/10 * * * *","binding_id":"docs-agent","enabled":true,"run_input":{"roleName":"docs-assistant","backend":"codex"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var b domain.TriggerBinding
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode binding: %v", err)
	}
	var runInput map[string]string
	if err := json.Unmarshal([]byte(b.SourceConfigRef), &runInput); err != nil {
		t.Fatalf("source_config_ref %q is not the run-input JSON: %v", b.SourceConfigRef, err)
	}
	if runInput["roleName"] != "docs-assistant" || runInput["backend"] != "codex" {
		t.Fatalf("run-input round-trip = %v, want roleName+backend", runInput)
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

// --- Phase 3: PATCH / DELETE / failure health ---

// createCronBinding is a test helper that creates an enabled cron binding under
// driver-1 and returns its id.
func createCronBinding(t *testing.T, mux *http.ServeMux, bindingID, schedule string) {
	t.Helper()
	body := `{"driver_id":"driver-1","driver_version_id":"version-1","source_kind":"cron","schedule":"` +
		schedule + `","binding_id":"` + bindingID + `","name":"` + bindingID + `","enabled":true}`
	if rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings", body); rec.Code != http.StatusCreated {
		t.Fatalf("create cron binding %s: status = %d; body=%s", bindingID, rec.Code, rec.Body.String())
	}
}

// TestPatchBinding_RenameAndReschedule pins the PATCH happy path: name +
// schedule change apply, and next_fire_at is recomputed from the new schedule.
func TestPatchBinding_RenameAndReschedule(t *testing.T) {
	mux, _ := seededMux(t)
	createCronBinding(t, mux, "s1", "*/10 * * * *")

	rec := do(t, mux, http.MethodPatch, "/api/workspaces/WS/trigger-bindings/s1",
		`{"name":"renamed","schedule":"0 9 * * *","schedule_timezone":"UTC"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Name       string     `json:"name"`
		Schedule   string     `json:"schedule"`
		NextFireAt *time.Time `json:"next_fire_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Name != "renamed" || out.Schedule != "0 9 * * *" {
		t.Fatalf("patch did not apply: %+v", out)
	}
	if out.NextFireAt == nil || !out.NextFireAt.After(time.Now()) {
		t.Fatalf("next_fire_at not recomputed to a future instant: %v", out.NextFireAt)
	}
}

// TestPatchBinding_ScheduleOnNonCron400 rejects a schedule change on a non-cron
// binding: an http binding fires by route, not schedule.
func TestPatchBinding_ScheduleOnNonCron400(t *testing.T) {
	mux, _ := seededMux(t)
	if rec := do(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings",
		`{"driver_id":"driver-1","driver_version_id":"version-1","route_key":"github.pr.opened","source_kind":"http","binding_id":"h1","enabled":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("create http binding: %d; body=%s", rec.Code, rec.Body.String())
	}
	rec := do(t, mux, http.MethodPatch, "/api/workspaces/WS/trigger-bindings/h1", `{"schedule":"*/5 * * * *"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch schedule on http binding status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestPatchBinding_InvalidSchedule400 rejects a malformed cron expression.
func TestPatchBinding_InvalidSchedule400(t *testing.T) {
	mux, _ := seededMux(t)
	createCronBinding(t, mux, "s1", "*/10 * * * *")
	rec := do(t, mux, http.MethodPatch, "/api/workspaces/WS/trigger-bindings/s1", `{"schedule":"not a cron"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch invalid schedule status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestPatchBinding_Errors covers the not-found and empty-patch guards.
func TestPatchBinding_Errors(t *testing.T) {
	mux, _ := seededMux(t)
	createCronBinding(t, mux, "s1", "*/10 * * * *")

	if rec := do(t, mux, http.MethodPatch, "/api/workspaces/WS/trigger-bindings/missing", `{"name":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("patch missing binding status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(t, mux, http.MethodPatch, "/api/workspaces/WS/trigger-bindings/s1", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestDeleteBinding_GoneAndGrantsRevoked pins Decision 6: deleting a binding
// removes it AND revokes its connector grants (no orphaned credentials).
func TestDeleteBinding_GoneAndGrantsRevoked(t *testing.T) {
	mux, st := seededMux(t)
	ctx := context.Background()
	createCronBinding(t, mux, "s2", "*/10 * * * *")

	// Seed two active grants for the binding (memstore grants need no connector FK).
	for i, action := range []string{"github.pull_request.read", "github.compare.read"} {
		if _, err := st.ConnectorGrants().Create(ctx, store.ConnectorGrantCreate{
			WorkspaceKey:    "WS",
			GrantID:         "grant-" + string(rune('a'+i)),
			ConnectorID:     "github",
			BindingID:       "s2",
			Action:          action,
			ResourcePattern: "repo:o/r",
		}); err != nil {
			t.Fatalf("seed grant %d: %v", i, err)
		}
	}

	rec := do(t, mux, http.MethodDelete, "/api/workspaces/WS/trigger-bindings/s2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Deleted       bool `json:"deleted"`
		GrantsRevoked int  `json:"grants_revoked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode delete: %v", err)
	}
	if !out.Deleted || out.GrantsRevoked != 2 {
		t.Fatalf("unexpected delete result: %+v", out)
	}
	// Binding is gone.
	if _, err := st.TriggerBindings().Get(ctx, "WS", "s2"); err == nil {
		t.Fatalf("binding s2 still present after delete")
	}
	// Grants are revoked (ListByBinding excludes revoked grants).
	grants, err := st.ConnectorGrants().ListByBinding(ctx, "WS", "s2")
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("expected 0 active grants after delete, got %d", len(grants))
	}
}

// TestDeleteBinding_NotFound404 returns 404 for a missing binding.
func TestDeleteBinding_NotFound404(t *testing.T) {
	mux, _ := seededMux(t)
	rec := do(t, mux, http.MethodDelete, "/api/workspaces/WS/trigger-bindings/missing", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestListBindings_FailureHealth pins Decision 7 inputs: consecutive_failures
// counts failed runs from newest until a clean run, skipping in-flight runs,
// and last_run_status is the newest run's status.
func TestListBindings_FailureHealth(t *testing.T) {
	mux, st := seededMux(t)
	ctx := context.Background()
	createCronBinding(t, mux, "s1", "*/10 * * * *")

	// Claim order stamps strictly increasing StartedAt, so newest-first is
	// D(running) > C(failed) > B(failed) > A(completed).
	seed := func(runID string, status domain.DriverRunStatus, finish bool) {
		t.Helper()
		if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
			WorkspaceKey: "WS", RunID: runID, DriverID: "driver-1", DriverVersionID: "version-1",
		}); err != nil {
			t.Fatalf("create run %s: %v", runID, err)
		}
		run, err := st.DriverRuns().Claim(ctx, "WS", runID, "node-1", "lease-"+runID)
		if err != nil {
			t.Fatalf("claim run %s: %v", runID, err)
		}
		if !finish {
			return
		}
		if _, err := st.DriverRuns().Finish(ctx, "WS", runID, store.DriverRunFinish{
			NodeID: run.NodeID, LeaseID: run.LeaseID, FencingToken: run.FencingToken, Status: status,
		}); err != nil {
			t.Fatalf("finish run %s: %v", runID, err)
		}
	}
	seed("A", domain.DriverRunCompleted, true)
	seed("B", domain.DriverRunFailed, true)
	seed("C", domain.DriverRunFailed, true)
	seed("D", domain.DriverRunRunning, false) // in-flight, must be skipped

	rec := do(t, mux, http.MethodGet, "/api/workspaces/WS/trigger-bindings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Bindings []struct {
			BindingID           string `json:"binding_id"`
			LastRunStatus       string `json:"last_run_status"`
			ConsecutiveFailures int    `json:"consecutive_failures"`
		} `json:"bindings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var found bool
	for _, b := range resp.Bindings {
		if b.BindingID != "s1" {
			continue
		}
		found = true
		if b.ConsecutiveFailures != 2 {
			t.Fatalf("consecutive_failures = %d, want 2 (D running skipped, C+B failed, A completed breaks)", b.ConsecutiveFailures)
		}
		if b.LastRunStatus != string(domain.DriverRunRunning) {
			t.Fatalf("last_run_status = %q, want running", b.LastRunStatus)
		}
	}
	if !found {
		t.Fatalf("binding s1 not in list response")
	}
}
