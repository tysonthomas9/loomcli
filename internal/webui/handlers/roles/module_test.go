package roles

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/roleprompts"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func TestGetRoleSourceMatrix(t *testing.T) {
	st := memstore.New()
	workspaceDir := t.TempDir()
	workerPath, err := roleprompts.Publish(workspaceDir, "custom-worker", "worker body")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	createRole(t, st, store.RoleCreate{WorkspaceKey: "WS", Name: "plan", Description: "Planner"})
	createRole(t, st, store.RoleCreate{WorkspaceKey: "WS", Name: "task", Description: "Coder"})
	createRole(t, st, store.RoleCreate{WorkspaceKey: "WS", Name: managedReviewerRole, Kind: string(domain.RoleKindInteractive), PromptFile: "builtin:pr-review-checkout"})
	createRole(t, st, store.RoleCreate{WorkspaceKey: "WS", Name: "custom-worker", Kind: string(domain.RoleKindWorker), PromptFile: workerPath})
	createRole(t, st, store.RoleCreate{WorkspaceKey: "WS", Name: "operator", Kind: string(domain.RoleKindInteractive), Prompt: "inline body"})
	createRole(t, st, store.RoleCreate{WorkspaceKey: "WS", Name: "selector", Kind: string(domain.RoleKindInteractive), PromptFile: "builtin:lead"})
	mux := testMux(t, st, workspaceDir)

	tests := []struct {
		name           string
		wantSourceKind string
		wantBody       string
		wantEditable   bool
		wantReason     string
	}{
		{name: "plan", wantSourceKind: sourceBuiltinTemplate, wantBody: "software architect", wantReason: reasonBuiltin},
		{name: "task", wantSourceKind: sourceBuiltinTemplate, wantBody: "implementation task", wantReason: reasonBuiltin},
		{name: managedReviewerRole, wantSourceKind: sourceManaged, wantBody: "read-only pr reviewer", wantReason: reasonManaged},
		{name: "custom-worker", wantSourceKind: sourceFile, wantBody: "worker body", wantEditable: true},
		{name: "operator", wantSourceKind: sourceInline, wantBody: "inline body", wantEditable: true},
		{name: "selector", wantSourceKind: sourceBuiltinSelector, wantBody: "builtin:lead", wantEditable: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := serveRoleRequest(mux, http.MethodGet, "/api/workspaces/WS/roles/"+tc.name, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			var response itemResponse[roleDetailDTO]
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode: %v", err)
			}
			got := response.Data
			if !response.Success || got.SourceKind != tc.wantSourceKind || got.Editable != tc.wantEditable || got.EditableReason != tc.wantReason {
				t.Fatalf("detail = %#v", got)
			}
			if !strings.Contains(strings.ToLower(got.SourceBody), strings.ToLower(tc.wantBody)) {
				t.Fatalf("sourceBody does not contain %q: %q", tc.wantBody, got.SourceBody)
			}
			if got.Revision == "" {
				t.Fatalf("detail is missing its revision: %#v", got)
			}
		})
	}
}

func TestGetRoleSurfacesUnreadableAndExternalFiles(t *testing.T) {
	st := memstore.New()
	workspaceDir := t.TempDir()
	createRole(t, st, store.RoleCreate{WorkspaceKey: "WS", Name: "missing", Kind: string(domain.RoleKindWorker), PromptFile: filepath.Join(workspaceDir, "missing.md")})
	external := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(external, []byte("must not leak"), 0o600); err != nil {
		t.Fatalf("write external: %v", err)
	}
	createRole(t, st, store.RoleCreate{WorkspaceKey: "WS", Name: "external", Kind: string(domain.RoleKindWorker), PromptFile: external})
	mux := testMux(t, st, workspaceDir)
	for _, tc := range []struct {
		name, reason string
	}{
		{name: "missing", reason: reasonUnreadable},
		{name: "external", reason: reasonExternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := serveRoleRequest(mux, http.MethodGet, "/api/workspaces/WS/roles/"+tc.name, nil)
			var response itemResponse[roleDetailDTO]
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			got := response.Data
			if got.SourceBody != "" || got.SourceError == "" || got.Editable || got.EditableReason != tc.reason {
				t.Fatalf("detail = %#v", got)
			}
			if strings.Contains(rec.Body.String(), "must not leak") {
				t.Fatalf("external file contents leaked: %s", rec.Body.String())
			}
		})
	}
}

func TestPatchRolePromptMatrix(t *testing.T) {
	st := memstore.New()
	workspaceDir := t.TempDir()
	plan := createRole(t, st, store.RoleCreate{WorkspaceKey: "WS", Name: "plan"})
	managed := createRole(t, st, store.RoleCreate{WorkspaceKey: "WS", Name: managedReviewerRole, Kind: string(domain.RoleKindInteractive), PromptFile: "builtin:pr-review-checkout"})
	oldWorkerPath, err := roleprompts.Publish(workspaceDir, "worker", "old worker")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	worker := createRole(t, st, store.RoleCreate{WorkspaceKey: "WS", Name: "worker", Kind: string(domain.RoleKindWorker), Prompt: "stale inline", PromptFile: oldWorkerPath})
	interactive := createRole(t, st, store.RoleCreate{WorkspaceKey: "WS", Name: "lead", Kind: string(domain.RoleKindInteractive), PromptFile: "builtin:lead"})
	mux := testMux(t, st, workspaceDir)

	rec := patchRole(mux, "plan", "nope", plan.UpdatedAt)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("builtin patch status=%d allow=%q body=%s", rec.Code, rec.Header().Get("Allow"), rec.Body.String())
	}
	rec = patchRole(mux, managedReviewerRole, "nope", managed.UpdatedAt)
	assertErrorCode(t, rec, http.StatusConflict, "managed_role")

	rec = patchRole(mux, "worker", "new worker body", worker.UpdatedAt)
	if rec.Code != http.StatusOK {
		t.Fatalf("worker patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	workerAfter, err := st.Roles().Get(t.Context(), "WS", "worker")
	if err != nil || workerAfter.Prompt != "" || workerAfter.PromptFile == oldWorkerPath || !filepath.IsAbs(workerAfter.PromptFile) {
		t.Fatalf("worker after = %+v, err=%v", workerAfter, err)
	}
	workerBody, err := roleprompts.ReadValidated(workspaceDir, workerAfter.PromptFile)
	if err != nil || workerBody != "new worker body" {
		t.Fatalf("worker body = %q, %v", workerBody, err)
	}

	rec = patchRole(mux, "lead", "new inline body", interactive.UpdatedAt)
	if rec.Code != http.StatusOK {
		t.Fatalf("interactive patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	interactiveAfter, err := st.Roles().Get(t.Context(), "WS", "lead")
	if err != nil || interactiveAfter.Prompt != "new inline body" || interactiveAfter.PromptFile != "" {
		t.Fatalf("interactive after = %+v, err=%v", interactiveAfter, err)
	}
}

func TestPatchRolePromptCASMismatchAndStrictBody(t *testing.T) {
	st := memstore.New()
	role := createRole(t, st, store.RoleCreate{WorkspaceKey: "WS", Name: "lead", Kind: string(domain.RoleKindInteractive), Prompt: "original"})
	mux := testMux(t, st, t.TempDir())

	stale := role.UpdatedAt.Add(-time.Second)
	rec := patchRole(mux, "lead", "draft", stale)
	assertErrorCode(t, rec, http.StatusConflict, "stale_revision")
	after, _ := st.Roles().Get(t.Context(), "WS", "lead")
	if after.Prompt != "original" {
		t.Fatalf("stale patch mutated prompt to %q", after.Prompt)
	}

	body := []byte(`{"prompt":"draft","expectedRevision":"` + revisionString(role.UpdatedAt) + `","description":"must reject"}`)
	rec = serveRoleRequest(mux, http.MethodPatch, "/api/workspaces/WS/roles/lead", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPatchRolePromptUsesFileWriteAccessGuard(t *testing.T) {
	st := memstore.New()
	role := createRole(t, st, store.RoleCreate{WorkspaceKey: "WS", Name: "lead", Kind: string(domain.RoleKindInteractive)})
	m := NewModule(st, middleware.FileAccessConfig{FrontendOrigins: []string{"http://localhost"}})
	m.workspaceDir = func(context.Context, store.Store, string) string { return t.TempDir() }
	mux := http.NewServeMux()
	m.Register(mux)
	body := []byte(`{"prompt":"draft","expectedRevision":"` + revisionString(role.UpdatedAt) + `"}`)
	req := roleRequest(http.MethodPatch, "/api/workspaces/WS/roles/lead", body)
	req.Host = "attacker.example"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unguarded PATCH status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func createRole(t *testing.T, st store.Store, input store.RoleCreate) *domain.Role {
	t.Helper()
	role, err := st.Roles().Create(t.Context(), input)
	if err != nil {
		t.Fatalf("Create role %s: %v", input.Name, err)
	}
	return role
}

func testMux(t *testing.T, st store.Store, workspaceDir string) *http.ServeMux {
	t.Helper()
	m := NewModule(st, middleware.FileAccessConfig{FrontendOrigins: []string{"http://localhost"}})
	m.workspaceDir = func(context.Context, store.Store, string) string { return workspaceDir }
	mux := http.NewServeMux()
	m.Register(mux)
	return mux
}

func serveRoleRequest(mux *http.ServeMux, method, path string, body []byte) *httptest.ResponseRecorder {
	req := roleRequest(method, path, body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func roleRequest(method, path string, body []byte) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Host = "localhost"
	return req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
}

func patchRole(mux *http.ServeMux, name, prompt string, revision time.Time) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"prompt": prompt, "expectedRevision": revisionString(revision)})
	return serveRoleRequest(mux, http.MethodPatch, "/api/workspaces/WS/roles/"+name, body)
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, status, rec.Body.String())
	}
	var response struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.Code != code {
		t.Fatalf("error response = %#v, err=%v body=%s", response, err, rec.Body.String())
	}
}
