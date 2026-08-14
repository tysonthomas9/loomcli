package fleetdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func newSkillTestClient(t *testing.T, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()
	ts := httptest.NewServer(handler)
	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		ts.Close()
		t.Fatalf("New: %v", err)
	}
	return client, ts.Close
}

// writeSkillError renders fleet-db's error envelope, meta included — the meta
// is the part the client has to unpack, so a test that omitted it would prove
// nothing.
func writeSkillError(w http.ResponseWriter, status int, code, message string, meta map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message, "meta": meta},
	})
}

// The scope decides the route family, and the route is what the server
// authorizes against: the role lane carries skill.create, the workspace lane
// carries skill.workspace_write. A ref sent down the wrong lane is a
// permission bug, not a cosmetic one.
func TestSkillStore_ScopeSelectsTheRouteFamily(t *testing.T) {
	tests := []struct {
		name     string
		ref      domain.SkillRef
		wantPath string
	}{
		{name: "workspace", ref: domain.WorkspaceSkillRef("pr-review"), wantPath: "/api/v1/WS/skills"},
		{name: "role", ref: domain.RoleSkillRef("lead", "pr-review"), wantPath: "/api/v1/WS/roles/lead/skills"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath, gotMethod string
			var body map[string]any
			client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotMethod = r.URL.Path, r.Method
				_ = json.NewDecoder(r.Body).Decode(&body)
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(skillWire{
					WorkspaceKey: "WS", Name: tt.ref.Name,
					Scope: string(tt.ref.Scope), RoleName: tt.ref.RoleName,
					Description: "does a thing",
				})
			})
			defer closeFn()

			got, err := client.Skills().Create(t.Context(), store.SkillCreate{
				WorkspaceKey: "WS", Ref: tt.ref, Description: "does a thing",
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if gotMethod != http.MethodPost || gotPath != tt.wantPath {
				t.Errorf("Create hit %s %s, want POST %s", gotMethod, gotPath, tt.wantPath)
			}
			// name is required on POST, where the path does not carry it.
			if body["name"] != tt.ref.Name {
				t.Errorf("create body name = %v, want %q", body["name"], tt.ref.Name)
			}
			if got.Ref() != tt.ref {
				t.Errorf("decoded ref = %+v, want %+v", got.Ref(), tt.ref)
			}
		})
	}
}

// A malformed ref must fail before it reaches the network, because a
// role-scoped ref with no role would otherwise build "/roles//skills" and hit
// whatever that happens to route to.
func TestSkillStore_RejectsMalformedRefBeforeAnyRequest(t *testing.T) {
	called := false
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	defer closeFn()

	_, err := client.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS",
		Ref:          domain.SkillRef{Scope: domain.SkillScopeRole, Name: "alpha"},
		Description:  "x",
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("Create with no role name = %v, want ErrInvalid", err)
	}
	if called {
		t.Errorf("a malformed ref reached the server")
	}
}

// An upsert reports whether it created or updated, which is the line an import
// or a sync summary is built from. 201 and 200 are the only signal.
func TestSkillStore_UpsertReportsCreatedFromStatus(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		wantCreated bool
	}{
		{name: "201 is a create", status: http.StatusCreated, wantCreated: true},
		{name: "200 is an update", status: http.StatusOK, wantCreated: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod, gotPath string
			var body map[string]any
			client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				_ = json.NewDecoder(r.Body).Decode(&body)
				w.WriteHeader(tt.status)
				_ = json.NewEncoder(w).Encode(skillWire{WorkspaceKey: "WS", Name: "pr-review", Scope: "workspace"})
			})
			defer closeFn()

			_, created, err := client.Skills().Upsert(t.Context(), store.SkillUpsert{
				Skill: store.SkillCreate{
					WorkspaceKey: "WS", Ref: domain.WorkspaceSkillRef("pr-review"), Description: "d",
				},
			})
			if err != nil {
				t.Fatalf("Upsert: %v", err)
			}
			if created != tt.wantCreated {
				t.Errorf("created = %v, want %v", created, tt.wantCreated)
			}
			if gotMethod != http.MethodPut || gotPath != "/api/v1/WS/skills/pr-review" {
				t.Errorf("Upsert hit %s %s, want PUT /api/v1/WS/skills/pr-review", gotMethod, gotPath)
			}
			// The path names the skill. Sending a name too is the only way the
			// two can disagree, so it is not sent.
			if _, ok := body["name"]; ok {
				t.Errorf("upsert body carried a name: %v", body["name"])
			}
		})
	}
}

// Force is a separate route rather than a flag, because the server binds a
// permission to a route pattern and cannot read a body or a query string.
func TestSkillStore_ForceUsesItsOwnRoutes(t *testing.T) {
	tests := []struct {
		name       string
		call       func(*testing.T, store.SkillStore) error
		wantMethod string
		wantPath   string
		wantQuery  string
	}{
		{
			name: "force upsert",
			call: func(t *testing.T, s store.SkillStore) error {
				_, _, err := s.Upsert(t.Context(), store.SkillUpsert{
					Skill: store.SkillCreate{WorkspaceKey: "WS", Ref: domain.WorkspaceSkillRef("alpha"), Description: "d"},
					Force: true,
				})
				return err
			},
			wantMethod: http.MethodPost,
			wantPath:   "/api/v1/WS/skills/alpha/force-upsert",
		},
		{
			name: "force delete",
			call: func(t *testing.T, s store.SkillStore) error {
				return s.Delete(t.Context(), "WS", domain.RoleSkillRef("lead", "alpha"),
					store.SkillDelete{Force: true, Source: "manual"})
			},
			wantMethod: http.MethodPost,
			wantPath:   "/api/v1/WS/roles/lead/skills/alpha/force-delete",
			wantQuery:  "source=manual",
		},
		{
			name: "ordinary delete",
			call: func(t *testing.T, s store.SkillStore) error {
				return s.Delete(t.Context(), "WS", domain.WorkspaceSkillRef("alpha"), store.SkillDelete{Source: "pack:design"})
			},
			wantMethod: http.MethodDelete,
			wantPath:   "/api/v1/WS/skills/alpha",
			wantQuery:  "source=pack%3Adesign",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod, gotPath, gotQuery string
			client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(skillWire{WorkspaceKey: "WS", Name: "alpha", Scope: "workspace"})
			})
			defer closeFn()

			if err := tt.call(t, client.Skills()); err != nil {
				t.Fatalf("call: %v", err)
			}
			if gotMethod != tt.wantMethod || gotPath != tt.wantPath {
				t.Errorf("hit %s %s, want %s %s", gotMethod, gotPath, tt.wantMethod, tt.wantPath)
			}
			if gotQuery != tt.wantQuery {
				t.Errorf("query = %q, want %q", gotQuery, tt.wantQuery)
			}
		})
	}
}

// An unfiltered listing returns both scopes in one call, which is exactly what
// resolving an agent's scope chain needs. This test walks the whole seam:
// listing → resolution → the skills an agent in that role loads.
func TestSkillStore_ListFeedsTheScopeChain(t *testing.T) {
	var gotQuery string
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{
			"skills": []skillWire{
				{WorkspaceKey: "WS", Name: "shared", Scope: "workspace", Content: "w"},
				{WorkspaceKey: "WS", Name: "review", Scope: "workspace", Content: "w"},
				{WorkspaceKey: "WS", Name: "review", Scope: "role", RoleName: "lead", Content: "r"},
				{WorkspaceKey: "WS", Name: "triage", Scope: "role", RoleName: "task", Content: "r"},
			},
			"count": 4,
		})
	})
	defer closeFn()

	all, err := client.Skills().List(t.Context(), "WS", store.SkillFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if gotQuery != "" {
		t.Errorf("unfiltered List sent query %q, want none", gotQuery)
	}

	resolved := domain.ResolveSkillChain(all, "lead")
	if len(resolved) != 2 {
		t.Fatalf("resolved %d skills, want 2 (shared + the role's review)", len(resolved))
	}
	if resolved[0].Name != "review" || resolved[0].Content != "r" {
		t.Errorf("review resolved to %+v, want the role-scoped copy", resolved[0])
	}
	if resolved[1].Name != "shared" {
		t.Errorf("second resolved skill = %q, want shared", resolved[1].Name)
	}
}

func TestSkillStore_ListFilterNarrowsToOneRole(t *testing.T) {
	var gotQuery string
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Encode()
		_ = json.NewEncoder(w).Encode(map[string]any{"skills": []skillWire{}, "count": 0})
	})
	defer closeFn()

	if _, err := client.Skills().List(t.Context(), "WS", store.SkillFilter{
		Scope: domain.SkillScopeRole, RoleName: "lead",
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if gotQuery != "role=lead&scope=role" {
		t.Errorf("query = %q, want role=lead&scope=role", gotQuery)
	}
}

// Files replaces the whole set, so "drop every bundled file" and "leave them
// alone" must not serialize the same way.
func TestSkillStore_UpdateDistinguishesEmptyFilesFromAbsent(t *testing.T) {
	tests := []struct {
		name  string
		patch store.SkillUpdate
		want  any
	}{
		{name: "absent", patch: store.SkillUpdate{}, want: nil},
		{name: "empty drops every file", patch: store.SkillUpdate{Files: &[]domain.SkillFile{}}, want: []any{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body map[string]any
			client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&body)
				_ = json.NewEncoder(w).Encode(skillWire{WorkspaceKey: "WS", Name: "alpha", Scope: "workspace"})
			})
			defer closeFn()

			if _, err := client.Skills().Update(t.Context(), "WS", domain.WorkspaceSkillRef("alpha"), tt.patch); err != nil {
				t.Fatalf("Update: %v", err)
			}
			got, present := body["files"]
			if tt.want == nil {
				if present {
					t.Errorf("files was sent as %v, want it omitted", got)
				}
				return
			}
			list, ok := got.([]any)
			if !ok || len(list) != 0 {
				t.Errorf("files = %#v, want an empty array", got)
			}
		})
	}
}

// The revision travels twice — unquoted in the body, quoted in the ETag — and
// the two forms must never be confused. This is the client-side twin of the
// server bug that made every conditional write fail: a caller holding the
// quoted form compares it against the unquoted revision the listing gives it,
// never matches, and sees a permanent conflict that looks entirely genuine.
func TestSkillStore_RevisionRoundTripsUnquotedThroughTheETag(t *testing.T) {
	const revision = "0123456789abcdef"
	var sentIfMatch string
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			sentIfMatch = r.Header.Get(ifMatchHeader)
		}
		// Exactly what fleet-db writes: a quoted ETag beside an unquoted
		// revision field.
		w.Header().Set(etagHeader, strconv.Quote(revision))
		_ = json.NewEncoder(w).Encode(skillDocumentWire{
			Path: "SKILL.md", Content: "body", Revision: revision, SkillRef: "workspace:alpha",
		})
	})
	defer closeFn()

	doc, err := client.Skills().GetFile(t.Context(), "WS", domain.WorkspaceSkillRef("alpha"), domain.SkillFileNameSKILLMD)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if doc.Revision != revision {
		t.Fatalf("revision = %q, want the bare %q — the quotes must not leak upward", doc.Revision, revision)
	}
	if doc.Ref != domain.WorkspaceSkillRef("alpha") {
		t.Errorf("skill_ref decoded to %+v, want workspace:alpha", doc.Ref)
	}

	// Handing that revision straight back must produce the quoted entity-tag
	// the RFC requires and the server parses.
	if _, err := client.Skills().PutFile(t.Context(), "WS", domain.WorkspaceSkillRef("alpha"), store.SkillFileWrite{
		Path: domain.SkillFileNameSKILLMD, Content: "edited", IfMatch: doc.Revision,
	}); err != nil {
		t.Fatalf("PutFile: %v", err)
	}
	if sentIfMatch != strconv.Quote(revision) {
		t.Errorf("If-Match = %q, want the quoted %q", sentIfMatch, strconv.Quote(revision))
	}
	// And it must round-trip: the header we sent, parsed the way the server
	// parses it, has to be the revision we were given.
	if parseETag(sentIfMatch) != revision {
		t.Errorf("If-Match %q does not parse back to %q", sentIfMatch, revision)
	}
}

func TestParseETag(t *testing.T) {
	tests := []struct{ in, want string }{
		{in: `"abc"`, want: "abc"},
		{in: `W/"abc"`, want: "abc"},
		{in: `  "abc"  `, want: "abc"},
		{in: "abc", want: "abc"},
		{in: "", want: ""},
		{in: `""`, want: ""},
	}
	for _, tt := range tests {
		if got := parseETag(tt.in); got != tt.want {
			t.Errorf("parseETag(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// If a response ever omits the body revision, the ETag is the fallback — but
// unquoted, on the way through.
func TestSkillStore_FallsBackToTheETagWhenTheBodyOmitsTheRevision(t *testing.T) {
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(etagHeader, `W/"deadbeefdeadbeef"`)
		_ = json.NewEncoder(w).Encode(skillDocumentWire{
			Path: "notes.md", Content: "x", SkillRef: "role:lead:alpha",
		})
	})
	defer closeFn()

	doc, err := client.Skills().GetFile(t.Context(), "WS", domain.RoleSkillRef("lead", "alpha"), "notes.md")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if doc.Revision != "deadbeefdeadbeef" {
		t.Errorf("revision = %q, want deadbeefdeadbeef", doc.Revision)
	}
}

// A nested document path keeps its separators: the server route ends in a
// {path...} wildcard, so escaping the slashes would address nothing.
func TestSkillStore_DocumentPathKeepsItsSeparators(t *testing.T) {
	var gotPath string
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set(etagHeader, `"rev0000000000000"`)
		_ = json.NewEncoder(w).Encode(skillDocumentWire{
			Path: "references/api docs.md", Content: "x", Revision: "rev0000000000000", SkillRef: "workspace:alpha",
		})
	})
	defer closeFn()

	if _, err := client.Skills().GetFile(t.Context(), "WS", domain.WorkspaceSkillRef("alpha"),
		"references/api docs.md"); err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	// Separators literal, the space inside a segment escaped.
	if gotPath != "/api/v1/WS/skills/alpha/files/references/api docs.md" {
		t.Errorf("path = %q, want the separators kept and only the segment escaped", gotPath)
	}
}

func TestSkillStore_PutFileSendsIfNoneMatchForACreate(t *testing.T) {
	var gotIfNoneMatch, gotIfMatch string
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotIfNoneMatch = r.Header.Get(ifNoneMatchHeader)
		gotIfMatch = r.Header.Get(ifMatchHeader)
		w.Header().Set(etagHeader, `"rev0000000000000"`)
		_ = json.NewEncoder(w).Encode(skillDocumentWire{
			Path: "new.md", Content: "x", Revision: "rev0000000000000", SkillRef: "workspace:alpha",
		})
	})
	defer closeFn()

	if _, err := client.Skills().PutFile(t.Context(), "WS", domain.WorkspaceSkillRef("alpha"), store.SkillFileWrite{
		Path: "new.md", Content: "x", IfNoneMatchAny: true,
	}); err != nil {
		t.Fatalf("PutFile: %v", err)
	}
	if gotIfNoneMatch != "*" {
		t.Errorf("If-None-Match = %q, want *", gotIfNoneMatch)
	}
	if gotIfMatch != "" {
		t.Errorf("If-Match = %q, want none on an unconditional create", gotIfMatch)
	}
}

func TestSkillStore_DeleteFileCarriesIfMatchAndSource(t *testing.T) {
	var gotIfMatch, gotQuery, gotMethod string
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotIfMatch, gotQuery, gotMethod = r.Header.Get(ifMatchHeader), r.URL.RawQuery, r.Method
		w.WriteHeader(http.StatusNoContent)
	})
	defer closeFn()

	if err := client.Skills().DeleteFile(t.Context(), "WS", domain.WorkspaceSkillRef("alpha"), store.SkillFileDelete{
		Path: "notes.md", IfMatch: "0123456789abcdef", Source: "manual",
	}); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %s, want DELETE", gotMethod)
	}
	if gotIfMatch != `"0123456789abcdef"` {
		t.Errorf("If-Match = %q, want the quoted revision", gotIfMatch)
	}
	// Source travels in the query because DELETE has no body.
	if gotQuery != "source=manual" {
		t.Errorf("query = %q, want source=manual", gotQuery)
	}
}

// The two failure modes a caller must tell apart. 412 is "you were working
// from an older copy" and a re-read fixes it; 409 is "you do not own this" and
// nothing the caller does fixes it.
func TestSkillStore_PreconditionFailureIsItsOwnError(t *testing.T) {
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSkillError(w, http.StatusPreconditionFailed, "precondition_failed",
			`skill workspace:alpha file "notes.md" changed`, map[string]string{
				"ref":               "workspace:alpha",
				"path":              "notes.md",
				"expected_revision": "0123456789abcdef",
				"stored_revision":   "fedcba9876543210",
			})
	})
	defer closeFn()

	_, err := client.Skills().PutFile(t.Context(), "WS", domain.WorkspaceSkillRef("alpha"), store.SkillFileWrite{
		Path: "notes.md", Content: "x", IfMatch: "0123456789abcdef",
	})
	if !errors.Is(err, domain.ErrSkillPreconditionFailed) {
		t.Fatalf("PutFile error = %v, want ErrSkillPreconditionFailed", err)
	}
	if errors.Is(err, domain.ErrSkillProvenanceConflict) || errors.Is(err, domain.ErrConflict) {
		t.Errorf("a 412 must not read as an ownership refusal or a plain conflict: %v", err)
	}

	var stale *domain.SkillPreconditionError
	if !errors.As(err, &stale) {
		t.Fatalf("errors.As did not recover the detail from %v", err)
	}
	if stale.Expected != "0123456789abcdef" || stale.Stored != "fedcba9876543210" {
		t.Errorf("revisions = %q/%q, want the meta values", stale.Expected, stale.Stored)
	}
	if stale.Path != "notes.md" || stale.Ref != domain.WorkspaceSkillRef("alpha") {
		t.Errorf("document = %+v %q, want workspace:alpha notes.md", stale.Ref, stale.Path)
	}
}

func TestSkillStore_ProvenanceConflictIsItsOwnError(t *testing.T) {
	updatedAt := time.Now().UTC().Truncate(time.Second)
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSkillError(w, http.StatusConflict, "skill_provenance_conflict",
			"skill role:lead:alpha is owned by alice", map[string]string{
				"ref":                 "role:lead:alpha",
				"scope":               "role",
				"role_name":           "lead",
				"name":                "alpha",
				"existing_created_by": "alice",
				"existing_source":     "manual",
				"existing_updated_at": updatedAt.Format(time.RFC3339Nano),
				"incoming_created_by": "bob",
				"incoming_source":     "pack:design",
			})
	})
	defer closeFn()

	_, _, err := client.Skills().Upsert(t.Context(), store.SkillUpsert{
		Skill: store.SkillCreate{WorkspaceKey: "WS", Ref: domain.RoleSkillRef("lead", "alpha"), Description: "d"},
	})
	if !errors.Is(err, domain.ErrSkillProvenanceConflict) {
		t.Fatalf("Upsert error = %v, want ErrSkillProvenanceConflict", err)
	}
	// Before this mapping every 409 without a known code became
	// ErrAlreadyExists, which would have told a CLI to report "it exists" for
	// a refusal that has nothing to do with existence.
	if errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrSkillPreconditionFailed) {
		t.Errorf("an ownership refusal must not read as already-exists or a stale revision: %v", err)
	}

	var conflict *domain.SkillProvenanceConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("errors.As did not recover the detail from %v", err)
	}
	if conflict.ExistingCreatedBy != "alice" || conflict.IncomingCreatedBy != "bob" {
		t.Errorf("owners = %q/%q, want alice/bob", conflict.ExistingCreatedBy, conflict.IncomingCreatedBy)
	}
	if !conflict.ExistingUpdatedAt.Equal(updatedAt) {
		t.Errorf("existing_updated_at = %v, want %v", conflict.ExistingUpdatedAt, updatedAt)
	}
	if conflict.Ref != domain.RoleSkillRef("lead", "alpha") {
		t.Errorf("ref = %+v, want role:lead:alpha", conflict.Ref)
	}
}

// A 409 with no meta at all still has to classify, or a client that upgrades
// ahead of the server loses the distinction entirely.
func TestSkillStore_ProvenanceConflictSurvivesMissingMeta(t *testing.T) {
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSkillError(w, http.StatusConflict, "skill_provenance_conflict", "owned by someone else", nil)
	})
	defer closeFn()

	err := client.Skills().Delete(t.Context(), "WS", domain.WorkspaceSkillRef("alpha"), store.SkillDelete{})
	if !errors.Is(err, domain.ErrSkillProvenanceConflict) {
		t.Fatalf("Delete error = %v, want ErrSkillProvenanceConflict", err)
	}
	if err.Error() == "" {
		t.Errorf("error rendered empty")
	}
}

// Every other 409 keeps behaving as it did.
func TestSkillStore_OtherConflictsAreUnchanged(t *testing.T) {
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSkillError(w, http.StatusConflict, "already_exists", "skill exists", nil)
	})
	defer closeFn()

	_, err := client.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: domain.WorkspaceSkillRef("alpha"), Description: "d",
	})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("Create error = %v, want ErrAlreadyExists", err)
	}
	if errors.Is(err, domain.ErrSkillProvenanceConflict) {
		t.Errorf("a plain 409 must not read as an ownership refusal: %v", err)
	}
}

// --- skill packs ---

func TestSkillPackStore_CRUD(t *testing.T) {
	var gotMethod, gotPath string
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/skill-packs" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"skill_packs": []skillPackWire{{WorkspaceKey: "WS", Name: "design", RepoURL: "https://x/y.git"}},
				"count":       1,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(skillPackWire{
			WorkspaceKey: "WS", Name: "design", RepoURL: "https://x/y.git", Ref: "main",
		})
	})
	defer closeFn()

	packs := client.SkillPacks()
	if _, err := packs.Create(t.Context(), store.SkillPackCreate{
		WorkspaceKey: "WS", Name: "design", RepoURL: "https://x/y.git",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/WS/skill-packs" {
		t.Errorf("Create hit %s %s", gotMethod, gotPath)
	}

	list, err := packs.List(t.Context(), "WS")
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v (%d packs)", err, len(list))
	}

	if _, err := packs.Get(t.Context(), "WS", "design"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotPath != "/api/v1/WS/skill-packs/design" {
		t.Errorf("Get path = %q", gotPath)
	}

	if err := packs.Delete(t.Context(), "WS", "design"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("Delete method = %s", gotMethod)
	}
}

// The last-sync fields travel as one block, because a record asserting a
// status with no time and no commit behind it is not a record of anything.
func TestSkillPackStore_RecordSyncTravelsAsOneBlock(t *testing.T) {
	var body map[string]any
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(skillPackWire{WorkspaceKey: "WS", Name: "design"})
	})
	defer closeFn()

	if _, err := client.SkillPacks().Update(t.Context(), "WS", "design", store.SkillPackUpdate{
		RecordSync: &domain.SkillPackSync{
			Status: domain.SkillPackSyncOK,
			Commit: "abc123",
			Skills: []string{"pr-review"},
		},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	sync, ok := body["record_sync"].(map[string]any)
	if !ok {
		t.Fatalf("record_sync = %#v, want an object", body["record_sync"])
	}
	if sync["status"] != domain.SkillPackSyncOK || sync["commit"] != "abc123" {
		t.Errorf("record_sync = %#v", sync)
	}
	if _, present := body["repo_url"]; present {
		t.Errorf("an untouched field was sent: repo_url")
	}
}

// --- conditional-write round trip against a server that behaves like fleet-db ---

// serverParseETagList is a transcription of fleet-db's parseETagList (RFC 9110
// §8.8.3). It is copied rather than approximated on purpose: a fake that
// parsed loosely would accept a header the real server rejects, and this whole
// test exists to catch exactly that difference.
func serverParseETagList(header string) []string {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}
	if header == "*" {
		return []string{"*"}
	}
	var tags []string
	for _, raw := range strings.Split(header, ",") {
		tag := strings.TrimSpace(raw)
		tag = strings.TrimPrefix(tag, "W/")
		if len(tag) >= 2 && strings.HasPrefix(tag, `"`) && strings.HasSuffix(tag, `"`) {
			tag = tag[1 : len(tag)-1]
		}
		if tag == "" {
			continue
		}
		tags = append(tags, tag)
	}
	return tags
}

// conditionalDocServer is a fake fleet-db per-document lane: it stamps a quoted
// ETag on every response, parses If-Match the way the server does, and answers
// 412 when the precondition does not hold. A double-quoted header therefore
// fails here exactly as it would in production.
type conditionalDocServer struct {
	content  string
	lastETag string
}

func (d *conditionalDocServer) revision() string {
	// Any deterministic function of the content will do — the test cares that
	// the token changes when the document does, not what it is.
	return "rev" + strconv.Itoa(len(d.content)) + "0000000000"
}

func (d *conditionalDocServer) handler(w http.ResponseWriter, r *http.Request) {
	stored := d.revision()
	if r.Method == http.MethodPut {
		if tags := serverParseETagList(r.Header.Get(ifMatchHeader)); len(tags) > 0 {
			matched := false
			for _, tag := range tags {
				if tag == "*" || tag == stored {
					matched = true
					break
				}
			}
			if !matched {
				writeSkillError(w, http.StatusPreconditionFailed, "precondition_failed",
					"skill file changed since it was read", map[string]string{
						"ref": "workspace:alpha", "path": "notes.md",
						"expected_revision": strings.Join(tags, ", "), "stored_revision": stored,
					})
				return
			}
		}
		var body putSkillFileBody
		_ = json.NewDecoder(r.Body).Decode(&body)
		d.content = body.Content
		stored = d.revision()
	}
	d.lastETag = strconv.Quote(stored)
	w.Header().Set(etagHeader, d.lastETag)
	_ = json.NewEncoder(w).Encode(skillDocumentWire{
		Path: "notes.md", Content: d.content, Revision: stored, SkillRef: "workspace:alpha",
	})
}

// The whole point of the ticket's If-Match requirement, end to end: whatever
// form a caller is holding the revision in, the conditional write must SUCCEED
// against a server that parses the header the way fleet-db parses it.
//
// Asserting against our own revision field instead would pass while the header
// form failed — which is precisely how the original server-side bug survived
// its first test.
func TestSkillStore_ConditionalWriteSucceedsForEveryFormACallerHolds(t *testing.T) {
	doc := &conditionalDocServer{content: "original"}
	client, closeFn := newSkillTestClient(t, doc.handler)
	defer closeFn()

	ref := domain.WorkspaceSkillRef("alpha")
	read, err := client.Skills().GetFile(t.Context(), "WS", ref, "notes.md")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	// The header the server actually sent — what a web handler receives and
	// hands back verbatim as If-Match.
	rawETag := doc.lastETag
	if rawETag != strconv.Quote(read.Revision) {
		t.Fatalf("fake server ETag %q disagrees with the revision field %q", rawETag, read.Revision)
	}

	forms := []struct {
		name    string
		ifMatch string
	}{
		{name: "bare revision from the domain type", ifMatch: read.Revision},
		{name: "the quoted ETag header verbatim", ifMatch: rawETag},
		{name: "a weak entity-tag", ifMatch: `W/` + rawETag},
		{name: "an entity-tag with surrounding whitespace", ifMatch: "  " + rawETag + "  "},
	}
	for _, form := range forms {
		t.Run(form.name, func(t *testing.T) {
			doc.content = "original" // reset so every form writes from the same revision
			written, err := client.Skills().PutFile(t.Context(), "WS", ref, store.SkillFileWrite{
				Path: "notes.md", Content: "edited by " + form.name, IfMatch: form.ifMatch,
			})
			if err != nil {
				t.Fatalf("conditional PutFile with %s = %v, want it to succeed", form.name, err)
			}
			if written.Revision == read.Revision {
				t.Errorf("revision did not move after a write: %q", written.Revision)
			}
			if written.Revision != parseETag(doc.lastETag) {
				t.Errorf("returned revision %q disagrees with the ETag %q", written.Revision, doc.lastETag)
			}
		})
	}

	t.Run("a stale revision still fails as a precondition", func(t *testing.T) {
		doc.content = "original"
		_, err := client.Skills().PutFile(t.Context(), "WS", ref, store.SkillFileWrite{
			Path: "notes.md", Content: "x", IfMatch: "rev9999999999999",
		})
		if !errors.Is(err, domain.ErrSkillPreconditionFailed) {
			t.Fatalf("stale PutFile = %v, want ErrSkillPreconditionFailed", err)
		}
	})
}

// A multi-tag header is rejected here rather than forwarded, because reducing
// it to one tag would quietly change what the caller asked for.
func TestSkillStore_RejectsAMultiTagIfMatchWithoutSendingIt(t *testing.T) {
	called := false
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	defer closeFn()

	_, err := client.Skills().PutFile(t.Context(), "WS", domain.WorkspaceSkillRef("alpha"), store.SkillFileWrite{
		Path: "notes.md", Content: "x", IfMatch: `"aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"`,
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("multi-tag If-Match = %v, want ErrInvalid", err)
	}
	if called {
		t.Errorf("a malformed If-Match reached the server")
	}

	if err := client.Skills().DeleteFile(t.Context(), "WS", domain.WorkspaceSkillRef("alpha"), store.SkillFileDelete{
		Path: "notes.md", IfMatch: `"aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"`,
	}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("multi-tag If-Match on DeleteFile = %v, want ErrInvalid", err)
	}
}

// Quoting is idempotent: the header the client sends is the same whichever
// form it was handed, so nothing double-quotes.
func TestIfMatchHeaderValueIsIdempotent(t *testing.T) {
	const bare = "0123456789abcdef"
	forms := []string{bare, `"` + bare + `"`, `W/"` + bare + `"`, "  " + bare + "  "}
	for _, form := range forms {
		got, err := ifMatchHeaderValue(form)
		if err != nil {
			t.Fatalf("ifMatchHeaderValue(%q): %v", form, err)
		}
		if got != strconv.Quote(bare) {
			t.Errorf("ifMatchHeaderValue(%q) = %q, want %q", form, got, strconv.Quote(bare))
		}
	}
	// The wildcard is not an entity-tag and must not be quoted.
	if got, err := ifMatchHeaderValue("*"); err != nil || got != "*" {
		t.Errorf("ifMatchHeaderValue(*) = %q, %v; want * and no error", got, err)
	}
}
