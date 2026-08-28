package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const testWorkspace = "SKILLS"

type skillsHarness struct {
	store *memstore.Store
	mux   *http.ServeMux
}

func newSkillsHarness(t *testing.T) *skillsHarness {
	t.Helper()
	ctx := t.Context()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: testWorkspace, Name: "Skills"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: testWorkspace, Name: "reviewer"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	st.SetSkillActor("alice")
	publishTestSkill(t, st, domain.WorkspaceSkillRef("workspace-guide"), "Workspace guidance", "CATALOG-BODY-SECRET", []domain.SkillFileTreeFile{{
		Path: "references/guide.md", Bytes: []byte("CATALOG-FILE-SECRET"), MediaType: "text/markdown",
	}}, "manual", "workspace-v1")
	publishTestSkill(t, st, domain.RoleSkillRef("reviewer", "review-code"), "Review code", "review body v1", []domain.SkillFileTreeFile{{
		Path: "scripts/run.sh", Bytes: []byte("#!/bin/sh\necho review\n"), MediaType: "text/x-shellscript", Executable: true,
	}}, "pack:team", "abc123")

	mux := http.NewServeMux()
	NewModule(st, middleware.FileAccessConfig{FrontendOrigins: []string{"http://localhost"}}).Register(mux)
	return &skillsHarness{store: st, mux: mux}
}

func publishTestSkill(t *testing.T, st *memstore.Store, ref domain.SkillRef, description, body string, bundled []domain.SkillFileTreeFile, source, sourceRef string) *domain.Skill {
	t.Helper()
	snapshot, err := domain.BuildSkillFileTree(ref.Name, description, []byte(body), bundled)
	if err != nil {
		t.Fatalf("build %s tree: %v", ref, err)
	}
	inputs := make([]domain.WorkspaceFileInput, 0, len(snapshot.Files))
	for _, file := range snapshot.Files {
		inputs = append(inputs, domain.WorkspaceFileInput(file))
	}
	published, err := st.WorkspaceFiles().Publish(t.Context(), testWorkspace, inputs)
	if err != nil {
		t.Fatalf("publish %s tree: %v", ref, err)
	}
	skill, err := st.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: testWorkspace, Ref: ref, Description: description,
		FileTreeRevision: published.Tree.Revision, Source: source, SourceRef: sourceRef,
	})
	if err != nil {
		t.Fatalf("create %s skill: %v", ref, err)
	}
	return skill
}

func (h *skillsHarness) request(t *testing.T, method, path string, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "localhost"
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rr := httptest.NewRecorder()
	h.mux.ServeHTTP(rr, req)
	return rr
}

func TestCatalogProjectionStripsContents(t *testing.T) {
	h := newSkillsHarness(t)
	rr := h.request(t, http.MethodGet, "/api/workspaces/SKILLS/skills", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rr.Code, rr.Body.String())
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("CATALOG-BODY-SECRET")) || bytes.Contains(rr.Body.Bytes(), []byte("CATALOG-FILE-SECRET")) {
		t.Fatalf("catalog leaked skill contents: %s", rr.Body.String())
	}
	var got catalogResponse
	decodeResponse(t, rr, &got)
	if len(got.Groups) != 2 {
		t.Fatalf("groups = %+v, want workspace and role groups", got.Groups)
	}
	workspace := got.Groups[0]
	if workspace.Scope != domain.SkillScopeWorkspace || len(workspace.Skills) != 1 {
		t.Fatalf("workspace group = %+v", workspace)
	}
	entry := workspace.Skills[0]
	if entry.Name != "workspace-guide" || entry.FileTreeRevision == "" || len(entry.Files) != 1 || entry.Files[0].Revision == "" {
		t.Fatalf("catalog entry = %+v, want revisions and file metadata", entry)
	}
}

func TestSkillDetailCarriesBodyButNotBundledContents(t *testing.T) {
	h := newSkillsHarness(t)
	rr := h.request(t, http.MethodGet, "/api/workspaces/SKILLS/skills/workspace-guide", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rr.Code, rr.Body.String())
	}
	var got skillDetailResponse
	decodeResponse(t, rr, &got)
	if got.Content != "CATALOG-BODY-SECRET" || got.FileTreeRevision == "" {
		t.Fatalf("detail = %+v, want SKILL.md body and revision", got)
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("CATALOG-FILE-SECRET")) {
		t.Fatalf("detail leaked bundled file content: %s", rr.Body.String())
	}
	if len(got.Files) != 1 || got.Files[0].Path != "references/guide.md" {
		t.Fatalf("detail files = %+v, want metadata", got.Files)
	}
}

func TestSkillFileGetSupportsBodyAndBundledLanes(t *testing.T) {
	h := newSkillsHarness(t)
	tests := []struct {
		path       string
		wantPath   string
		wantBody   string
		executable bool
	}{
		{
			path:     "/api/workspaces/SKILLS/skills/workspace-guide/files/SKILL.md",
			wantPath: "SKILL.md", wantBody: "CATALOG-BODY-SECRET",
		},
		{
			path:     "/api/workspaces/SKILLS/roles/reviewer/skills/review-code/files/scripts/run.sh",
			wantPath: "scripts/run.sh", wantBody: "#!/bin/sh\necho review\n", executable: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.wantPath, func(t *testing.T) {
			rr := h.request(t, http.MethodGet, tt.path, "", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s, want 200", rr.Code, rr.Body.String())
			}
			var got skillFileResponse
			decodeResponse(t, rr, &got)
			if got.Path != tt.wantPath || got.Content != tt.wantBody || got.Executable != tt.executable || got.Revision == "" {
				t.Fatalf("file = %+v", got)
			}
			if gotETag := rr.Header().Get("ETag"); gotETag != strconv.Quote(got.Revision) {
				t.Fatalf("ETag = %q, want %q", gotETag, strconv.Quote(got.Revision))
			}
		})
	}
}

func TestPutRoleSkillFileNormalizesQuotedETagAndAdvancesRevision(t *testing.T) {
	h := newSkillsHarness(t)
	path := "/api/workspaces/SKILLS/roles/reviewer/skills/review-code/files/SKILL.md"
	read := h.request(t, http.MethodGet, path, "", nil)
	oldETag := read.Header().Get("ETag")
	if oldETag == "" {
		t.Fatalf("GET missing ETag: %s", read.Body.String())
	}

	write := h.request(t, http.MethodPut, path, `{"content":"review body v2","executable":true}`, map[string]string{"If-Match": oldETag})
	if write.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", write.Code, write.Body.String())
	}
	var got skillFileResponse
	decodeResponse(t, write, &got)
	if got.Revision == "" || strconv.Quote(got.Revision) == oldETag {
		t.Fatalf("revision = %q, want advancement from %s", got.Revision, oldETag)
	}
	if got.Executable {
		t.Fatal("SKILL.md executable bit must remain false")
	}
	stored := readTestSkillFile(t, h.store, domain.RoleSkillRef("reviewer", "review-code"), domain.SkillFileNameSKILLMD)
	if stored != "review body v2" {
		t.Fatalf("stored body = %q, response = %+v", stored, got)
	}
}

func TestPutRoleSkillBodyPreservesVendorFrontmatter(t *testing.T) {
	h := newSkillsHarness(t)
	ref := domain.RoleSkillRef("reviewer", "review-code")
	skill, err := h.store.Skills().Get(t.Context(), testWorkspace, ref)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := (&Handler{Store: h.store}).loadTree(t.Context(), testWorkspace, skill.FileTreeRevision)
	if err != nil {
		t.Fatal(err)
	}
	prefix := []byte("---\nname: review-code\ndescription: Review code\nlicense: MIT\nmetadata:\n  vendor: acme\n---\n")
	vendorDocument := append(append([]byte(nil), prefix...), []byte("vendor body")...)
	files := replaceTreeFile(loaded.snapshot.Files, domain.SkillFileTreeFile{
		Path: domain.SkillFileNameSKILLMD, Bytes: vendorDocument, MediaType: "text/markdown",
	})
	if _, err := domain.ValidateSkillFileTree(files); err != nil {
		t.Fatalf("validate vendor tree: %v", err)
	}
	tree, err := (&Handler{Store: h.store}).publishTree(t.Context(), testWorkspace, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Skills().Update(t.Context(), testWorkspace, ref, store.SkillUpdate{
		FileTreeRevision: &tree.Revision, ExpectedFileTreeRevision: skill.FileTreeRevision,
	}); err != nil {
		t.Fatal(err)
	}

	path := "/api/workspaces/SKILLS/roles/reviewer/skills/review-code/files/SKILL.md"
	rr := h.request(t, http.MethodPut, path, `{"content":"edited body"}`, map[string]string{"If-Match": strconv.Quote(tree.Revision)})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	updated, err := h.store.Skills().Get(t.Context(), testWorkspace, ref)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := h.store.WorkspaceFiles().Download(t.Context(), testWorkspace, updated.FileTreeRevision, domain.SkillFileNameSKILLMD)
	want := append(append([]byte(nil), prefix...), []byte("edited body")...)
	if err != nil || !bytes.Equal(stored, want) {
		t.Fatalf("stored SKILL.md = %q, want %q, err=%v", stored, want, err)
	}
}

func TestPutAndDeleteBundledRoleSkillFile(t *testing.T) {
	h := newSkillsHarness(t)
	path := "/api/workspaces/SKILLS/roles/reviewer/skills/review-code/files/scripts/run.sh"
	read := h.request(t, http.MethodGet, path, "", nil)
	if read.Code != http.StatusOK {
		t.Fatalf("GET status = %d body = %s", read.Code, read.Body.String())
	}

	write := h.request(t, http.MethodPut, path, `{"content":"#!/bin/sh\necho updated\n","executable":true}`, map[string]string{
		"If-Match": read.Header().Get("ETag"),
	})
	if write.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body = %s", write.Code, write.Body.String())
	}
	var updated skillFileResponse
	decodeResponse(t, write, &updated)
	if !updated.Executable || updated.Content != "#!/bin/sh\necho updated\n" {
		t.Fatalf("updated file = %+v", updated)
	}

	deleted := h.request(t, http.MethodDelete, path, "", map[string]string{
		"If-Match": write.Header().Get("ETag"),
	})
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d body = %s, want 204", deleted.Code, deleted.Body.String())
	}
	skill, err := h.store.Skills().Get(context.Background(), testWorkspace, domain.RoleSkillRef("reviewer", "review-code"))
	if err != nil {
		t.Fatalf("get skill after delete: %v", err)
	}
	_, err = h.store.WorkspaceFiles().Stat(context.Background(), testWorkspace, skill.FileTreeRevision, "scripts/run.sh")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("stored file after delete error = %v, want ErrNotFound", err)
	}
}

func TestPutRoleSkillFileCreatesBundledFileWithIfNoneMatch(t *testing.T) {
	h := newSkillsHarness(t)
	path := "/api/workspaces/SKILLS/roles/reviewer/skills/review-code/files/references/new.md"
	body := `{"content":"new reference","executable":false}`

	created := h.request(t, http.MethodPut, path, body, map[string]string{"If-None-Match": "*"})
	if created.Code != http.StatusCreated {
		t.Fatalf("first PUT status = %d body = %s, want 201", created.Code, created.Body.String())
	}
	var doc skillFileResponse
	decodeResponse(t, created, &doc)
	if doc.Path != "references/new.md" || doc.Content != "new reference" || doc.Revision == "" {
		t.Fatalf("created document = %+v", doc)
	}
	if stored := readTestSkillFile(t, h.store, domain.RoleSkillRef("reviewer", "review-code"), "references/new.md"); stored != "new reference" {
		t.Fatalf("stored document = %q", stored)
	}

	collision := h.request(t, http.MethodPut, path, body, map[string]string{"If-None-Match": "*"})
	assertErrorCode(t, collision, http.StatusPreconditionFailed, "precondition_failed")
}

func TestRoleWholeSkillCRUD(t *testing.T) {
	h := newSkillsHarness(t)
	collection := "/api/workspaces/SKILLS/roles/reviewer/skills"
	created := h.request(t, http.MethodPost, collection,
		`{"name":"new-skill","description":"New description","content":"initial body","source_ref":"draft-1"}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("POST status = %d body = %s, want 201", created.Code, created.Body.String())
	}
	var createBody skillDetailResponse
	decodeResponse(t, created, &createBody)
	if createBody.Name != "new-skill" || createBody.Description != "New description" || createBody.Content != "initial body" || createBody.Source != webuiSkillSource {
		t.Fatalf("created skill = %+v", createBody)
	}
	if created.Header().Get("ETag") == "" {
		t.Fatal("POST response missing ETag")
	}

	item := collection + "/new-skill"
	patched := h.request(t, http.MethodPatch, item,
		`{"description":"Edited description","source_ref":"draft-2"}`,
		map[string]string{"If-Match": created.Header().Get("ETag")})
	if patched.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d body = %s, want 200", patched.Code, patched.Body.String())
	}
	var patchBody skillDetailResponse
	decodeResponse(t, patched, &patchBody)
	if patchBody.Description != "Edited description" || patchBody.SourceRef != "draft-2" || patchBody.Content != "initial body" {
		t.Fatalf("patched skill = %+v", patchBody)
	}

	deleted := h.request(t, http.MethodDelete, item, "", map[string]string{"If-Match": patched.Header().Get("ETag")})
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d body = %s, want 204", deleted.Code, deleted.Body.String())
	}
	_, err := h.store.Skills().Get(context.Background(), testWorkspace, domain.RoleSkillRef("reviewer", "new-skill"))
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted skill lookup = %v, want ErrNotFound", err)
	}
}

func TestRoleWholeSkillCreateExistingAndMutationsRequireIfMatch(t *testing.T) {
	h := newSkillsHarness(t)
	collection := "/api/workspaces/SKILLS/roles/reviewer/skills"
	existing := h.request(t, http.MethodPost, collection,
		`{"name":"review-code","description":"Duplicate"}`, nil)
	assertErrorCode(t, existing, http.StatusConflict, "skill_conflict")

	item := collection + "/review-code"
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			rr := h.request(t, method, item, `{"description":"No precondition"}`, nil)
			assertErrorCode(t, rr, http.StatusPreconditionRequired, "precondition_required")
		})
	}
}

func TestPatchRoleSkillWithStaleIfMatchReturnsCurrentRevision(t *testing.T) {
	h := newSkillsHarness(t)
	ref := domain.RoleSkillRef("reviewer", "review-code")
	path := "/api/workspaces/SKILLS/roles/reviewer/skills/review-code"
	read := h.request(t, http.MethodGet, path, "", nil)
	originalETag := read.Header().Get("ETag")
	current := advanceTestSkillFile(t, h.store, ref, domain.SkillFileNameSKILLMD, "concurrent body", false)

	patched := h.request(t, http.MethodPatch, path, `{"description":"stale edit"}`, map[string]string{"If-Match": originalETag})
	assertErrorCode(t, patched, http.StatusPreconditionFailed, "precondition_failed")
	var body map[string]string
	decodeResponse(t, patched, &body)
	if body["revision"] != current.FileTreeRevision {
		t.Fatalf("revision = %q, want %q", body["revision"], current.FileTreeRevision)
	}
}

func TestDeleteRoleSkillOnAnotherActorsSkillReturnsProvenance(t *testing.T) {
	h := newSkillsHarness(t)
	path := "/api/workspaces/SKILLS/roles/reviewer/skills/review-code"
	read := h.request(t, http.MethodGet, path, "", nil)
	h.store.SetSkillActor("bob")
	deleted := h.request(t, http.MethodDelete, path, "", map[string]string{"If-Match": read.Header().Get("ETag")})
	assertErrorCode(t, deleted, http.StatusConflict, "skill_provenance_conflict")
	var body map[string]string
	decodeResponse(t, deleted, &body)
	if body["owner"] != "alice" || body["source"] != "pack:team" {
		t.Fatalf("conflict body = %+v, want owner/source", body)
	}
}

func TestWorkspaceWholeSkillMutationsAreRegisteredReadOnly(t *testing.T) {
	h := newSkillsHarness(t)
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: "/api/workspaces/SKILLS/skills", body: `{}`},
		{method: http.MethodPatch, path: "/api/workspaces/SKILLS/skills/not-a-valid-name-", body: `{}`},
		{method: http.MethodDelete, path: "/api/workspaces/SKILLS/skills/not-a-valid-name-"},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			rr := h.request(t, tt.method, tt.path, tt.body, nil)
			assertErrorCode(t, rr, http.StatusForbidden, "workspace_scope_readonly")
			if !strings.Contains(rr.Body.String(), "loom skill update") {
				t.Fatalf("response lacks CLI guidance: %s", rr.Body.String())
			}
		})
	}
}

func TestPutWorkspaceSkillFileIsReadOnly(t *testing.T) {
	h := newSkillsHarness(t)
	path := "/api/workspaces/SKILLS/skills/workspace-guide/files/SKILL.md"
	read := h.request(t, http.MethodGet, path, "", nil)
	write := h.request(t, http.MethodPut, path, `{"content":"blocked"}`, map[string]string{"If-Match": read.Header().Get("ETag")})
	assertErrorCode(t, write, http.StatusForbidden, "workspace_scope_readonly")
	if !strings.Contains(write.Body.String(), "loom skill update") {
		t.Fatalf("response lacks CLI guidance: %s", write.Body.String())
	}
}

func TestWorkspaceFileMutationGuardRunsBeforeDocumentValidation(t *testing.T) {
	h := newSkillsHarness(t)
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name: "delete skill body", method: http.MethodDelete,
			path: "/api/workspaces/SKILLS/skills/workspace-guide/files/SKILL.md",
		},
		{
			name: "put invalid bundled path", method: http.MethodPut,
			path: "/api/workspaces/SKILLS/skills/workspace-guide/files/bad%5cpath", body: `{"content":"blocked"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := h.request(t, tt.method, tt.path, tt.body, nil)
			assertErrorCode(t, rr, http.StatusForbidden, "workspace_scope_readonly")
		})
	}
}

func TestRoleTraversalCannotReachWorkspaceSkillMutationLane(t *testing.T) {
	h := newSkillsHarness(t)
	workspaceRef := domain.WorkspaceSkillRef("workspace-guide")
	before, err := h.store.Skills().Get(context.Background(), testWorkspace, workspaceRef)
	if err != nil {
		t.Fatalf("read workspace skill before traversal attempts: %v", err)
	}

	for _, encodedRole := range []string{"%2e%2e", "a%2fb", "..%2f"} {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			t.Run(method+"_"+encodedRole, func(t *testing.T) {
				path := "/api/workspaces/SKILLS/roles/" + encodedRole + "/skills/workspace-guide/files/SKILL.md"
				rr := h.request(t, method, path, `{"content":"TRAVERSAL-OVERWRITE"}`, map[string]string{"If-Match": strconv.Quote(before.FileTreeRevision)})
				assertErrorCode(t, rr, http.StatusBadRequest, "skill_validation_failed")
			})
		}
	}

	after, err := h.store.Skills().Get(context.Background(), testWorkspace, workspaceRef)
	if err != nil {
		t.Fatalf("read workspace skill after traversal attempts: %v", err)
	}
	if after.FileTreeRevision != before.FileTreeRevision {
		t.Fatalf("workspace skill mutated through role traversal: before=%+v after=%+v", before, after)
	}
}

func TestPutRoleSkillFileStaleIfMatchReturnsCurrentRevision(t *testing.T) {
	h := newSkillsHarness(t)
	ref := domain.RoleSkillRef("reviewer", "review-code")
	path := "/api/workspaces/SKILLS/roles/reviewer/skills/review-code/files/SKILL.md"
	read := h.request(t, http.MethodGet, path, "", nil)
	current := advanceTestSkillFile(t, h.store, ref, "scripts/run.sh", "concurrent sibling", true)

	write := h.request(t, http.MethodPut, path, `{"content":"stale body"}`, map[string]string{"If-Match": read.Header().Get("ETag")})
	assertErrorCode(t, write, http.StatusPreconditionFailed, "precondition_failed")
	var body map[string]string
	decodeResponse(t, write, &body)
	if body["revision"] != current.FileTreeRevision {
		t.Fatalf("revision = %q, want current %q", body["revision"], current.FileTreeRevision)
	}
}

func TestPutRoleSkillFileOnAnotherActorsSkillReturnsProvenance(t *testing.T) {
	h := newSkillsHarness(t)
	h.store.SetSkillActor("bob")
	path := "/api/workspaces/SKILLS/roles/reviewer/skills/review-code/files/SKILL.md"
	read := h.request(t, http.MethodGet, path, "", nil)
	write := h.request(t, http.MethodPut, path, `{"content":"takeover"}`, map[string]string{"If-Match": read.Header().Get("ETag")})
	assertErrorCode(t, write, http.StatusConflict, "skill_provenance_conflict")
	var body map[string]string
	decodeResponse(t, write, &body)
	if body["owner"] != "alice" || body["source"] != "pack:team" {
		t.Fatalf("conflict body = %+v, want owner/source", body)
	}
}

func TestPutRoleSkillFileRequiresIfMatch(t *testing.T) {
	h := newSkillsHarness(t)
	path := "/api/workspaces/SKILLS/roles/reviewer/skills/review-code/files/SKILL.md"
	write := h.request(t, http.MethodPut, path, `{"content":"missing precondition"}`, nil)
	assertErrorCode(t, write, http.StatusPreconditionRequired, "precondition_required")
}

func TestPutRoleSkillFileRejectsMultiETagIfMatchClearly(t *testing.T) {
	h := newSkillsHarness(t)
	path := "/api/workspaces/SKILLS/roles/reviewer/skills/review-code/files/SKILL.md"
	write := h.request(t, http.MethodPut, path, `{"content":"ambiguous"}`, map[string]string{
		"If-Match": `"revision-a", "revision-b"`,
	})
	assertErrorCode(t, write, http.StatusBadRequest, "invalid_precondition")
	if !strings.Contains(write.Body.String(), "single ETag") {
		t.Fatalf("response lacks single-ETag guidance: %s", write.Body.String())
	}
}

func TestRoleSkillMutationsRejectWildcardIfMatch(t *testing.T) {
	h := newSkillsHarness(t)
	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPatch, path: "/api/workspaces/SKILLS/roles/reviewer/skills/review-code", body: `{"description":"ambiguous"}`},
		{method: http.MethodDelete, path: "/api/workspaces/SKILLS/roles/reviewer/skills/review-code"},
		{method: http.MethodPut, path: "/api/workspaces/SKILLS/roles/reviewer/skills/review-code/files/SKILL.md", body: `{"content":"ambiguous"}`},
	} {
		t.Run(request.method, func(t *testing.T) {
			rr := h.request(t, request.method, request.path, request.body, map[string]string{"If-Match": "*"})
			assertErrorCode(t, rr, http.StatusBadRequest, "invalid_precondition")
		})
	}
}

func TestDeleteSkillBodyIsRefused(t *testing.T) {
	h := newSkillsHarness(t)
	path := "/api/workspaces/SKILLS/roles/reviewer/skills/review-code/files/SKILL.md"
	read := h.request(t, http.MethodGet, path, "", nil)
	deleted := h.request(t, http.MethodDelete, path, "", map[string]string{"If-Match": read.Header().Get("ETag")})
	assertErrorCode(t, deleted, http.StatusUnprocessableEntity, "skill_validation_failed")
}

func TestSkillCapabilitiesReflectSessionWriteHint(t *testing.T) {
	t.Run("local writer", func(t *testing.T) {
		h := newSkillsHarness(t)
		rr := h.request(t, http.MethodGet, "/api/workspaces/SKILLS/skill-capabilities", "", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
		}
		var got capabilitiesResponse
		decodeResponse(t, rr, &got)
		if !got.CanEditRoleScope || got.WorkspaceScope != "read_only" {
			t.Fatalf("capabilities = %+v", got)
		}
	})

	t.Run("remote viewer", func(t *testing.T) {
		h := newSkillsHarness(t)
		mux := http.NewServeMux()
		NewModule(h.store, middleware.FileAccessConfig{
			RemoteAuth: true,
			ResolveRole: func(context.Context, string, middleware.UserIdentity) (string, error) {
				return "viewer", nil
			},
		}).Register(mux)
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/SKILLS/skill-capabilities", nil)
		ctx := middleware.WithWorkspace(req.Context(), testWorkspace)
		ctx = middleware.WithUserIdentity(ctx, middleware.UserIdentity{UserID: "viewer-1"})
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
		}
		var got capabilitiesResponse
		decodeResponse(t, rr, &got)
		if got.CanEditRoleScope || got.WorkspaceScope != "read_only" {
			t.Fatalf("capabilities = %+v", got)
		}
	})
}

func TestFileAccessGatesRoleWrites(t *testing.T) {
	h := newSkillsHarness(t)
	mux := http.NewServeMux()
	NewModule(h.store, middleware.FileAccessConfig{
		RemoteAuth: true,
		ResolveRole: func(context.Context, string, middleware.UserIdentity) (string, error) {
			return "viewer", nil
		},
	}).Register(mux)

	tests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: "/api/workspaces/SKILLS/roles/reviewer/skills", body: `{"name":"blocked","description":"blocked"}`},
		{method: http.MethodPatch, path: "/api/workspaces/SKILLS/roles/reviewer/skills/review-code", body: `{"description":"blocked"}`},
		{method: http.MethodDelete, path: "/api/workspaces/SKILLS/roles/reviewer/skills/review-code"},
		{method: http.MethodPut, path: "/api/workspaces/SKILLS/roles/reviewer/skills/review-code/files/SKILL.md", body: `{"content":"blocked"}`},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("If-Match", `"revision"`)
			ctx := middleware.WithWorkspace(req.Context(), testWorkspace)
			ctx = middleware.WithUserIdentity(ctx, middleware.UserIdentity{UserID: "viewer-1"})
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d body = %s, want FileAccess 403", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestSkillErrorBridgePreservesBackingForbidden(t *testing.T) {
	rr := httptest.NewRecorder()
	writeSkillError(rr, fmt.Errorf("fleetdb: role permission denies skill update: %w", domain.ErrSkillForbidden))
	assertErrorCode(t, rr, http.StatusForbidden, "skill_forbidden")
	var body map[string]string
	decodeResponse(t, rr, &body)
	if !strings.Contains(body["detail"], "role permission denies skill update") {
		t.Fatalf("forbidden detail = %q, want upstream reason", body["detail"])
	}
}

func TestBinarySkillFileReturnsMetadataWithoutJSONTextCoercion(t *testing.T) {
	h := newSkillsHarness(t)
	ref := domain.RoleSkillRef("reviewer", "review-code")
	binary := []byte{0x50, 0x4b, 0x03, 0x04, 0x00, 0xff}
	advanceTestSkillFileBytes(t, h.store, ref, "assets/archive.zip", binary, false, "application/zip")

	rr := h.request(t, http.MethodGet, "/api/workspaces/SKILLS/roles/reviewer/skills/review-code/files/assets/archive.zip", "", nil)
	assertErrorCode(t, rr, http.StatusUnsupportedMediaType, "binary_skill_file")
	if bytes.Contains(rr.Body.Bytes(), binary) {
		t.Fatalf("binary bytes were coerced into JSON text: %q", rr.Body.Bytes())
	}
	var body map[string]any
	decodeResponse(t, rr, &body)
	if body["path"] != "assets/archive.zip" || body["media_type"] != "application/zip" || body["size_bytes"] != float64(len(binary)) {
		t.Fatalf("binary metadata = %+v", body)
	}
}

// The 404 lane had no coverage at all: every other status the module can return
// is asserted somewhere in this file, but nothing reached skill_not_found, so a
// regression in the ErrNotFound mapping would have surfaced only in the UI.
func TestMissingSkillsAndFilesMapToNotFound(t *testing.T) {
	h := newSkillsHarness(t)
	etag := h.request(t, http.MethodGet,
		"/api/workspaces/SKILLS/roles/reviewer/skills/review-code", "", nil).Header().Get("ETag")

	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		headers map[string]string
	}{
		{
			name:   "get unknown role skill",
			method: http.MethodGet,
			path:   "/api/workspaces/SKILLS/roles/reviewer/skills/no-such-skill",
		},
		{
			name:   "get unknown workspace skill",
			method: http.MethodGet,
			path:   "/api/workspaces/SKILLS/skills/no-such-skill",
		},
		{
			name:   "get unknown bundled file on a known skill",
			method: http.MethodGet,
			path:   "/api/workspaces/SKILLS/roles/reviewer/skills/review-code/files/scripts/missing.sh",
		},
		{
			name:    "patch unknown role skill",
			method:  http.MethodPatch,
			path:    "/api/workspaces/SKILLS/roles/reviewer/skills/no-such-skill",
			body:    `{"description":"Edited"}`,
			headers: map[string]string{"If-Match": etag},
		},
		{
			name:    "delete unknown role skill",
			method:  http.MethodDelete,
			path:    "/api/workspaces/SKILLS/roles/reviewer/skills/no-such-skill",
			headers: map[string]string{"If-Match": etag},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := h.request(t, tt.method, tt.path, tt.body, tt.headers)
			assertErrorCode(t, rr, http.StatusNotFound, "skill_not_found")
		})
	}
}

func readTestSkillFile(t *testing.T, st *memstore.Store, ref domain.SkillRef, path string) string {
	t.Helper()
	skill, err := st.Skills().Get(t.Context(), testWorkspace, ref)
	if err != nil {
		t.Fatalf("get %s: %v", ref, err)
	}
	body, err := st.WorkspaceFiles().Download(t.Context(), testWorkspace, skill.FileTreeRevision, path)
	if err != nil {
		t.Fatalf("download %s %s: %v", ref, path, err)
	}
	if path != domain.SkillFileNameSKILLMD {
		return string(body)
	}
	loaded, err := (&Handler{Store: st}).loadTree(t.Context(), testWorkspace, skill.FileTreeRevision)
	if err != nil {
		t.Fatalf("load %s tree: %v", ref, err)
	}
	return string(loaded.snapshot.Body)
}

func advanceTestSkillFile(t *testing.T, st *memstore.Store, ref domain.SkillRef, path, content string, executable bool) *domain.Skill {
	t.Helper()
	return advanceTestSkillFileBytes(t, st, ref, path, []byte(content), executable, mediaTypeForPath(path))
}

func advanceTestSkillFileBytes(t *testing.T, st *memstore.Store, ref domain.SkillRef, path string, body []byte, executable bool, mediaType string) *domain.Skill {
	t.Helper()
	h := &Handler{Store: st}
	skill, err := st.Skills().Get(t.Context(), testWorkspace, ref)
	if err != nil {
		t.Fatalf("get %s: %v", ref, err)
	}
	loaded, err := h.loadTree(t.Context(), testWorkspace, skill.FileTreeRevision)
	if err != nil {
		t.Fatalf("load %s tree: %v", ref, err)
	}
	files := replaceTreeFile(loaded.snapshot.Files, domain.SkillFileTreeFile{
		Path: path, Bytes: body, Executable: executable, MediaType: mediaType,
	})
	if path == domain.SkillFileNameSKILLMD {
		snapshot, buildErr := domain.BuildSkillFileTree(skill.Name, skill.Description, body, bundledFiles(files))
		if buildErr != nil {
			t.Fatalf("rebuild %s: %v", ref, buildErr)
		}
		files = snapshot.Files
	} else if _, err := domain.ValidateSkillFileTree(files); err != nil {
		t.Fatalf("validate %s tree: %v", ref, err)
	}
	tree, err := h.publishTree(t.Context(), testWorkspace, files)
	if err != nil {
		t.Fatalf("publish %s tree: %v", ref, err)
	}
	updated, err := st.Skills().Update(t.Context(), testWorkspace, ref, store.SkillUpdate{
		FileTreeRevision: &tree.Revision, ExpectedFileTreeRevision: skill.FileTreeRevision, Source: "test",
	})
	if err != nil {
		t.Fatalf("advance %s tree: %v", ref, err)
	}
	return updated
}

func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode response %q: %v", rr.Body.String(), err)
	}
}

func assertErrorCode(t *testing.T, rr *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rr.Code != wantStatus {
		t.Fatalf("status = %d body = %s, want %d", rr.Code, rr.Body.String(), wantStatus)
	}
	var body map[string]any
	decodeResponse(t, rr, &body)
	if body["code"] != wantCode {
		t.Fatalf("error body = %+v, want code %q", body, wantCode)
	}
}
