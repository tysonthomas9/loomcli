package roles

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

func newRolesMux() *http.ServeMux {
	mux := http.NewServeMux()
	NewModule(memstore.New()).Register(mux)
	return mux
}

func postRole(t *testing.T, mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/roles", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestCreateRole_CreatesThenIsIdempotent(t *testing.T) {
	mux := newRolesMux()

	// No prompt body, so no filesystem dependency — exercises the store path.
	rec := postRole(t, mux, `{"name":"bug-triage","task_filter":"any","read_only":true,"description":"triage"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var role domain.Role
	if err := json.Unmarshal(rec.Body.Bytes(), &role); err != nil {
		t.Fatalf("decode role: %v", err)
	}
	if role.Name != "bug-triage" || role.TaskFilter != "any" || !role.ReadOnly {
		t.Fatalf("unexpected role: %+v", role)
	}

	// Second create of the same role is an idempotent 200, not a 409.
	rec2 := postRole(t, mux, `{"name":"bug-triage"}`)
	if rec2.Code != http.StatusOK {
		t.Fatalf("idempotent create status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestCreateRole_RequiresName(t *testing.T) {
	rec := postRole(t, newRolesMux(), `{"name":"   "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// doRole issues an arbitrary method/path against the roles mux.
func doRole(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// The Phase-B single-role endpoints below deliberately avoid a prompt body, so
// they never touch the filesystem (writeRolePrompt needs a machine-local
// workspace path) and exercise only the store-interaction logic. The
// prompt-file round-trip is covered by the live local-mode verification.

func TestGetRole_ReturnsRoleWithEmptyPrompt(t *testing.T) {
	mux := newRolesMux()
	if rec := postRole(t, mux, `{"name":"bug-triage","task_filter":"any"}`); rec.Code != http.StatusCreated {
		t.Fatalf("seed create status = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec := doRole(t, mux, http.MethodGet, "/api/workspaces/WS/roles/bug-triage", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got roleWithPrompt
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode roleWithPrompt: %v", err)
	}
	if got.Role == nil || got.Role.Name != "bug-triage" || got.Prompt != "" {
		t.Fatalf("unexpected get response: %+v (prompt=%q)", got.Role, got.Prompt)
	}
}

func TestGetRole_MissingIs404(t *testing.T) {
	rec := doRole(t, newRolesMux(), http.MethodGet, "/api/workspaces/WS/roles/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get missing status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateRole_PatchesFields(t *testing.T) {
	mux := newRolesMux()
	if rec := postRole(t, mux, `{"name":"bug-triage","description":"old","read_only":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("seed create status = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec := doRole(t, mux, http.MethodPatch, "/api/workspaces/WS/roles/bug-triage",
		`{"description":"new","read_only":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got roleWithPrompt
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Role.Description != "new" || got.Role.ReadOnly {
		t.Fatalf("patch did not apply: %+v", got.Role)
	}
}

func TestUpdateRole_MissingIs404(t *testing.T) {
	rec := doRole(t, newRolesMux(), http.MethodPatch, "/api/workspaces/WS/roles/nope", `{"description":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("patch missing status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCloneRole_DuplicatesConfig(t *testing.T) {
	mux := newRolesMux()
	if rec := postRole(t, mux, `{"name":"bug-triage","task_filter":"any","read_only":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("seed create status = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec := doRole(t, mux, http.MethodPost, "/api/workspaces/WS/roles/bug-triage/clone",
		`{"target_name":"bug-triage-2"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("clone status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var clone domain.Role
	if err := json.Unmarshal(rec.Body.Bytes(), &clone); err != nil {
		t.Fatalf("decode clone: %v", err)
	}
	if clone.Name != "bug-triage-2" || clone.TaskFilter != "any" || !clone.ReadOnly {
		t.Fatalf("clone did not copy config: %+v", clone)
	}

	// Cloning onto an existing name is a real conflict (409), not a silent ensure.
	if rec := doRole(t, mux, http.MethodPost, "/api/workspaces/WS/roles/bug-triage/clone",
		`{"target_name":"bug-triage-2"}`); rec.Code != http.StatusConflict {
		t.Fatalf("clone-collision status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	// Same-name and missing-source are 400 / 404 respectively.
	if rec := doRole(t, mux, http.MethodPost, "/api/workspaces/WS/roles/bug-triage/clone",
		`{"target_name":"bug-triage"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("clone-to-self status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if rec := doRole(t, mux, http.MethodPost, "/api/workspaces/WS/roles/nope/clone",
		`{"target_name":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("clone-missing-source status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteRole_RemovesAndGuardsBuiltin(t *testing.T) {
	mux := newRolesMux()
	if rec := postRole(t, mux, `{"name":"bug-triage"}`); rec.Code != http.StatusCreated {
		t.Fatalf("seed create status = %d; body=%s", rec.Code, rec.Body.String())
	}

	if rec := doRole(t, mux, http.MethodDelete, "/api/workspaces/WS/roles/bug-triage", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if rec := doRole(t, mux, http.MethodGet, "/api/workspaces/WS/roles/bug-triage", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want 404", rec.Code)
	}
	// Built-in roles are refused.
	if rec := doRole(t, mux, http.MethodDelete, "/api/workspaces/WS/roles/plan", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("delete builtin status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	// Deleting a missing role is a 404.
	if rec := doRole(t, mux, http.MethodDelete, "/api/workspaces/WS/roles/nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
