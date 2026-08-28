package fleetdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

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

func writeSkillError(w http.ResponseWriter, status int, code, message string, meta map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message, "meta": meta},
	})
}

func testSkillWire(ref domain.SkillRef, revision string) skillWire {
	return skillWire{
		WorkspaceKey: "WS", Name: ref.Name, Scope: string(ref.Scope), RoleName: ref.RoleName,
		Description: "does a thing", FileTreeRevision: revision,
	}
}

func writeTestSkill(w http.ResponseWriter, status int, ref domain.SkillRef, revision string) {
	w.Header().Set(etagHeader, strconv.Quote(revision))
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(testSkillWire(ref, revision))
}

func TestSkillStoreCreateUsesExactScopeRoutesAndTreeBody(t *testing.T) {
	for _, tt := range []struct {
		name, wantPath string
		ref            domain.SkillRef
	}{
		{name: "workspace", ref: domain.WorkspaceSkillRef("pr-review"), wantPath: "/api/v1/WS/skills"},
		{name: "role", ref: domain.RoleSkillRef("lead", "pr-review"), wantPath: "/api/v1/WS/roles/lead/skills"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var body map[string]any
			client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != tt.wantPath {
					t.Errorf("request = %s %s, want POST %s", r.Method, r.URL.Path, tt.wantPath)
				}
				if strings.Contains(r.URL.Path, "/files/") {
					t.Errorf("legacy per-file endpoint reached: %s", r.URL.Path)
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				writeTestSkill(w, http.StatusCreated, tt.ref, "tree-one")
			})
			defer closeFn()
			got, err := client.Skills().Create(t.Context(), store.SkillCreate{
				WorkspaceKey: "WS", Ref: tt.ref, Description: "does a thing",
				FileTreeRevision: "tree-one", Source: "manual", SourceRef: "local",
			})
			if err != nil || got.FileTreeRevision != "tree-one" {
				t.Fatalf("Create = %+v, %v", got, err)
			}
			if body["name"] != tt.ref.Name || body["file_tree_revision"] != "tree-one" {
				t.Fatalf("body = %#v", body)
			}
			if _, ok := body["content"]; ok {
				t.Fatalf("legacy content field sent: %#v", body)
			}
			if _, ok := body["files"]; ok {
				t.Fatalf("legacy files field sent: %#v", body)
			}
		})
	}
}

func TestSkillStoreGetAndListValidateIdentityAndTreePointer(t *testing.T) {
	ref := domain.WorkspaceSkillRef("alpha")
	t.Run("get requires matching etag", func(t *testing.T) {
		client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/WS/skills/alpha" {
				t.Errorf("path = %q", r.URL.Path)
			}
			writeTestSkill(w, http.StatusOK, ref, "tree-one")
		})
		defer closeFn()
		got, err := client.Skills().Get(t.Context(), "WS", ref)
		if err != nil || got.FileTreeRevision != "tree-one" {
			t.Fatalf("Get = %+v, %v", got, err)
		}
	})
	t.Run("get rejects unquoted response etag", func(t *testing.T) {
		client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(etagHeader, "tree-one")
			_ = json.NewEncoder(w).Encode(testSkillWire(ref, "tree-one"))
		})
		defer closeFn()
		if _, err := client.Skills().Get(t.Context(), "WS", ref); !errors.Is(err, domain.ErrIntegrity) {
			t.Fatalf("Get with unquoted ETag = %v, want ErrIntegrity", err)
		}
	})

	for _, tt := range []struct {
		name string
		wire skillWire
	}{
		{name: "malformed ref", wire: skillWire{WorkspaceKey: "WS", Name: "../bad", Scope: "workspace", Description: "d", FileTreeRevision: "tree"}},
		{name: "wrong workspace", wire: skillWire{WorkspaceKey: "OTHER", Name: "alpha", Scope: "workspace", Description: "d", FileTreeRevision: "tree"}},
		{name: "missing pointer", wire: skillWire{WorkspaceKey: "WS", Name: "alpha", Scope: "workspace", Description: "d"}},
	} {
		t.Run("list rejects "+tt.name, func(t *testing.T) {
			client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"skills": []skillWire{tt.wire}})
			})
			defer closeFn()
			if _, err := client.Skills().List(t.Context(), "WS", store.SkillFilter{}); err == nil {
				t.Fatalf("List accepted %s", tt.name)
			}
		})
	}
}

func TestSkillStoreRejectsInvalidPointerAndIfMatchBeforeNetwork(t *testing.T) {
	requests := 0
	client, closeFn := newSkillTestClient(t, func(http.ResponseWriter, *http.Request) { requests++ })
	defer closeFn()
	ref := domain.WorkspaceSkillRef("alpha")
	if _, err := client.Skills().Create(t.Context(), store.SkillCreate{WorkspaceKey: "WS", Ref: ref, Description: "d"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("Create empty pointer = %v", err)
	}
	if _, _, err := client.Skills().Upsert(t.Context(), store.SkillUpsert{Skill: store.SkillCreate{WorkspaceKey: "WS", Ref: ref, Description: "d"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("Upsert empty pointer = %v", err)
	}
	description := "changed"
	if _, err := client.Skills().Update(t.Context(), "WS", ref, store.SkillUpdate{Description: &description}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("description-only Update = %v, want ErrInvalid", err)
	}
	newRevision := "tree-two"
	for _, revision := range []string{"", "*", `W/"tree-one"`, `"tree-one", "tree-two"`, `""`} {
		if _, err := client.Skills().Update(t.Context(), "WS", ref, store.SkillUpdate{
			FileTreeRevision: &newRevision, ExpectedFileTreeRevision: revision,
		}); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("Update If-Match %q = %v, want ErrInvalid", revision, err)
		}
	}
	if requests != 0 {
		t.Fatalf("invalid input reached fleet-db %d time(s)", requests)
	}
}

func TestSkillStoreUpdateSendsStrongWholeTreeCAS(t *testing.T) {
	ref := domain.RoleSkillRef("lead", "alpha")
	var body map[string]any
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/WS/roles/lead/skills/alpha" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get(ifMatchHeader); got != `"tree-one"` {
			t.Errorf("If-Match = %q, want quoted strong tag", got)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeTestSkill(w, http.StatusOK, ref, "tree-two")
	})
	defer closeFn()
	revision := "tree-two"
	got, err := client.Skills().Update(t.Context(), "WS", ref, store.SkillUpdate{
		FileTreeRevision: &revision, ExpectedFileTreeRevision: `"tree-one"`, Source: "manual",
	})
	if err != nil || got.FileTreeRevision != revision {
		t.Fatalf("Update = %+v, %v", got, err)
	}
	if body["file_tree_revision"] != revision {
		t.Fatalf("body = %#v", body)
	}
}

func TestSkillStorePreconditionMapsExpectedAndStoredRevision(t *testing.T) {
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSkillError(w, http.StatusPreconditionFailed, preconditionFailedCode, "stale", map[string]string{
			"ref": "workspace:alpha", "expected_revision": "tree-old", "stored_revision": "tree-current",
		})
	})
	defer closeFn()
	newRevision := "tree-new"
	_, err := client.Skills().Update(t.Context(), "WS", domain.WorkspaceSkillRef("alpha"), store.SkillUpdate{
		FileTreeRevision: &newRevision, ExpectedFileTreeRevision: "tree-old",
	})
	if !errors.Is(err, domain.ErrSkillPreconditionFailed) {
		t.Fatalf("Update = %v", err)
	}
	var stale *domain.SkillPreconditionError
	if !errors.As(err, &stale) || stale.Expected != "tree-old" || stale.Stored != "tree-current" {
		t.Fatalf("detail = %+v", stale)
	}
}

func TestSkillStoreUpsertAndForceUseExactRoutes(t *testing.T) {
	ref := domain.WorkspaceSkillRef("alpha")
	for _, tt := range []struct {
		name, method, path string
		force              bool
	}{
		{name: "upsert", method: http.MethodPut, path: "/api/v1/WS/skills/alpha"},
		{name: "force", method: http.MethodPost, path: "/api/v1/WS/skills/alpha/force-upsert", force: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tt.method || r.URL.Path != tt.path {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				writeTestSkill(w, http.StatusOK, ref, "tree")
			})
			defer closeFn()
			_, _, err := client.Skills().Upsert(t.Context(), store.SkillUpsert{Force: tt.force, Skill: store.SkillCreate{
				WorkspaceKey: "WS", Ref: ref, Description: "does a thing", FileTreeRevision: "tree",
			}})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSkillStoreMutationDoesNotFollowRedirect(t *testing.T) {
	redirectTargetReached := false
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/WS/skills/alpha", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/target", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("PATCH /target", func(http.ResponseWriter, *http.Request) { redirectTargetReached = true })
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	revision := "tree-two"
	if _, err := client.Skills().Update(t.Context(), "WS", domain.WorkspaceSkillRef("alpha"), store.SkillUpdate{
		FileTreeRevision: &revision, ExpectedFileTreeRevision: "tree-one",
	}); err == nil {
		t.Fatal("Update followed redirect or accepted redirect")
	}
	if redirectTargetReached {
		t.Fatal("mutation redirect target was reached")
	}
}

func TestSkillStoreProvenanceConflictIsTyped(t *testing.T) {
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSkillError(w, http.StatusConflict, skillProvenanceConflictCode, "owned", map[string]string{
			"scope": "workspace", "name": "alpha", "existing_created_by": "alice", "incoming_created_by": "bob",
		})
	})
	defer closeFn()
	_, _, err := client.Skills().Upsert(t.Context(), store.SkillUpsert{Skill: store.SkillCreate{
		WorkspaceKey: "WS", Ref: domain.WorkspaceSkillRef("alpha"), Description: "d", FileTreeRevision: "tree",
	}})
	if !errors.Is(err, domain.ErrSkillProvenanceConflict) {
		t.Fatalf("Upsert = %v", err)
	}
	var conflict *domain.SkillProvenanceConflictError
	if !errors.As(err, &conflict) || conflict.ExistingCreatedBy != "alice" || conflict.IncomingCreatedBy != "bob" {
		t.Fatalf("detail = %+v", conflict)
	}
}

func TestParseETagAndIfMatchHeaderValue(t *testing.T) {
	if got := parseETag(`"opaque/tree"`); got != "opaque/tree" {
		t.Fatalf("parseETag = %q", got)
	}
	if got := parseETag(`W/"tree"`); got != "" {
		t.Fatalf("weak parseETag = %q", got)
	}
	for _, input := range []string{"tree", `"tree"`} {
		got, err := ifMatchHeaderValue(input)
		if err != nil || got != `"tree"` {
			t.Fatalf("ifMatchHeaderValue(%q) = %q, %v", input, got, err)
		}
	}
}

func TestSkillPackStoreCRUD(t *testing.T) {
	var gotMethod, gotPath string
	client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/skill-packs" {
			_ = json.NewEncoder(w).Encode(map[string]any{"skill_packs": []skillPackWire{{WorkspaceKey: "WS", Name: "design", RepoURL: "https://x/y.git"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(skillPackWire{WorkspaceKey: "WS", Name: "design", RepoURL: "https://x/y.git"})
	})
	defer closeFn()
	packs := client.SkillPacks()
	if _, err := packs.Create(t.Context(), store.SkillPackCreate{WorkspaceKey: "WS", Name: "design", RepoURL: "https://x/y.git"}); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/WS/skill-packs" {
		t.Fatalf("Create hit %s %s", gotMethod, gotPath)
	}
	if list, err := packs.List(t.Context(), "WS"); err != nil || len(list) != 1 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if err := packs.Delete(t.Context(), "WS", "design"); err != nil {
		t.Fatal(err)
	}
}
