package memstore

import (
	"errors"
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// newSkillFixture builds a store with the roles a role-scoped skill needs.
func newSkillFixture(t *testing.T, roles ...string) *Store {
	t.Helper()
	s := New()
	for _, role := range roles {
		if _, err := s.Roles().Create(t.Context(), store.RoleCreate{WorkspaceKey: "WS", Name: role}); err != nil {
			t.Fatalf("seed role %q: %v", role, err)
		}
	}
	return s
}

func mustCreateSkill(t *testing.T, s *Store, ref domain.SkillRef, content string) *domain.Skill {
	t.Helper()
	sk, err := s.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: ref, Description: "does a thing", Content: content, Source: "manual",
	})
	if err != nil {
		t.Fatalf("Create %v: %v", ref, err)
	}
	return sk
}

func TestSkillStoreCRUD(t *testing.T) {
	s := newSkillFixture(t, "lead")
	skills := s.Skills()
	ref := domain.WorkspaceSkillRef("pr-review")

	created := mustCreateSkill(t, s, ref, "body")
	if created.CreatedBy != defaultSkillActor {
		t.Errorf("created_by = %q, want the authenticated actor", created.CreatedBy)
	}
	if created.ContentRevision == "" {
		t.Errorf("content_revision was not stamped on the way out")
	}

	if _, err := skills.Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: ref, Description: "dup",
	}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("duplicate Create = %v, want ErrAlreadyExists", err)
	}

	got, err := skills.Get(t.Context(), "WS", ref)
	if err != nil || got.Content != "body" {
		t.Fatalf("Get = %+v, %v", got, err)
	}

	if _, err := skills.Get(t.Context(), "WS", domain.WorkspaceSkillRef("nope")); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get unknown = %v, want ErrNotFound", err)
	}

	description := "edited"
	updated, err := skills.Update(t.Context(), "WS", ref, store.SkillUpdate{Description: &description})
	if err != nil || updated.Description != "edited" {
		t.Fatalf("Update = %+v, %v", updated, err)
	}

	if err := skills.Delete(t.Context(), "WS", ref, store.SkillDelete{}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := skills.Get(t.Context(), "WS", ref); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
}

// The two scopes must be able to carry the same name — shadowing is not
// possible otherwise, and a store keyed on the bare name would silently
// collapse them.
func TestSkillStore_ScopesShareANameAndResolveThroughTheChain(t *testing.T) {
	s := newSkillFixture(t, "lead", "task")
	mustCreateSkill(t, s, domain.WorkspaceSkillRef("review"), "workspace copy")
	mustCreateSkill(t, s, domain.RoleSkillRef("lead", "review"), "lead copy")
	mustCreateSkill(t, s, domain.RoleSkillRef("task", "review"), "task copy")
	mustCreateSkill(t, s, domain.WorkspaceSkillRef("shared"), "everyone")

	all, err := s.Skills().List(t.Context(), "WS", store.SkillFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("List returned %d skills, want 4", len(all))
	}

	resolved := domain.ResolveSkillChain(all, "lead")
	if len(resolved) != 2 {
		t.Fatalf("lead resolved %d skills, want 2", len(resolved))
	}
	if resolved[0].Name != "review" || resolved[0].Content != "lead copy" {
		t.Errorf("review resolved to %q, want the lead copy", resolved[0].Content)
	}
	if resolved[1].Content != "everyone" {
		t.Errorf("shared resolved to %q", resolved[1].Content)
	}
}

func TestSkillStore_ListFilters(t *testing.T) {
	s := newSkillFixture(t, "lead", "task")
	mustCreateSkill(t, s, domain.WorkspaceSkillRef("shared"), "w")
	mustCreateSkill(t, s, domain.RoleSkillRef("lead", "review"), "r")
	mustCreateSkill(t, s, domain.RoleSkillRef("task", "triage"), "r")

	role, err := s.Skills().List(t.Context(), "WS", store.SkillFilter{
		Scope: domain.SkillScopeRole, RoleName: "lead",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(role) != 1 || role[0].Name != "review" {
		t.Errorf("role listing = %+v, want just lead's review", role)
	}

	workspace, err := s.Skills().List(t.Context(), "WS", store.SkillFilter{Scope: domain.SkillScopeWorkspace})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(workspace) != 1 || workspace[0].Name != "shared" {
		t.Errorf("workspace listing = %+v", workspace)
	}
}

// A role-scoped skill pointing at a role that is not there is a 404 on the
// server, not a record with a dangling reference.
func TestSkillStore_RoleScopedSkillNeedsItsRole(t *testing.T) {
	s := newSkillFixture(t)
	_, err := s.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: domain.RoleSkillRef("ghost", "alpha"), Description: "d",
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Create against a missing role = %v, want ErrNotFound", err)
	}
}

// Deleting a role that still owns skills is refused rather than cascading: a
// cascade would destroy hand-written documents that may be the only copy.
func TestRoleStore_DeleteRefusedWhileItOwnsSkills(t *testing.T) {
	s := newSkillFixture(t, "lead")
	mustCreateSkill(t, s, domain.RoleSkillRef("lead", "review"), "r")

	err := s.Roles().Delete(t.Context(), "WS", "lead")
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Delete role = %v, want ErrInvalidTransition", err)
	}

	if err := s.Skills().Delete(t.Context(), "WS", domain.RoleSkillRef("lead", "review"), store.SkillDelete{}); err != nil {
		t.Fatalf("Delete skill: %v", err)
	}
	if err := s.Roles().Delete(t.Context(), "WS", "lead"); err != nil {
		t.Errorf("Delete role after its skills went = %v, want nil", err)
	}
}

func TestSkillStore_UpsertCreatesThenUpdates(t *testing.T) {
	s := newSkillFixture(t)
	in := store.SkillUpsert{Skill: store.SkillCreate{
		WorkspaceKey: "WS", Ref: domain.WorkspaceSkillRef("alpha"), Description: "first", Content: "one",
	}}

	sk, created, err := s.Skills().Upsert(t.Context(), in)
	if err != nil || !created || sk.Content != "one" {
		t.Fatalf("first Upsert = %+v, created=%v, %v", sk, created, err)
	}

	in.Skill.Content = "two"
	sk, created, err = s.Skills().Upsert(t.Context(), in)
	if err != nil || created || sk.Content != "two" {
		t.Fatalf("second Upsert = %+v, created=%v, %v", sk, created, err)
	}
}

// A skill is owned by its created_by. Only that actor — or a forcing caller —
// may overwrite or delete it, and source is not consulted, because source
// arrives from the caller and every read discloses it.
func TestSkillStore_OwnershipGuard(t *testing.T) {
	s := newSkillFixture(t)
	ref := domain.WorkspaceSkillRef("alpha")
	s.SetSkillActor("alice")
	mustCreateSkill(t, s, ref, "alice's")

	s.SetSkillActor("bob")
	upsert := store.SkillUpsert{Skill: store.SkillCreate{
		WorkspaceKey: "WS", Ref: ref, Description: "bob's", Content: "bob's",
		// Copying the owner's source must not help: it is a claim, not a
		// credential.
		Source: "manual",
	}}

	_, _, err := s.Skills().Upsert(t.Context(), upsert)
	if !errors.Is(err, domain.ErrSkillProvenanceConflict) {
		t.Fatalf("Upsert as another actor = %v, want ErrSkillProvenanceConflict", err)
	}
	var conflict *domain.SkillProvenanceConflictError
	if !errors.As(err, &conflict) || conflict.ExistingCreatedBy != "alice" || conflict.IncomingCreatedBy != "bob" {
		t.Errorf("conflict detail = %+v, want alice/bob", conflict)
	}

	// A delete is guarded too, because delete-then-recreate is an overwrite in
	// two steps and would otherwise transfer ownership for free.
	if err := s.Skills().Delete(t.Context(), "WS", ref, store.SkillDelete{}); !errors.Is(err, domain.ErrSkillProvenanceConflict) {
		t.Errorf("Delete as another actor = %v, want ErrSkillProvenanceConflict", err)
	}

	// Force gets through — and does NOT move the title, so the next overwrite
	// costs the same permission again.
	upsert.Force = true
	forced, created, err := s.Skills().Upsert(t.Context(), upsert)
	if err != nil || created {
		t.Fatalf("forced Upsert = created:%v, %v", created, err)
	}
	if forced.CreatedBy != "alice" {
		t.Errorf("created_by = %q after a force, want it to stay with alice", forced.CreatedBy)
	}
	if forced.UpdatedBy != "bob" {
		t.Errorf("updated_by = %q, want bob", forced.UpdatedBy)
	}
	if _, _, err := s.Skills().Upsert(t.Context(), store.SkillUpsert{Skill: upsert.Skill}); !errors.Is(err, domain.ErrSkillProvenanceConflict) {
		t.Errorf("second unforced Upsert = %v, want the guard to hold again", err)
	}
}

func TestSkillStore_GetFile(t *testing.T) {
	s := newSkillFixture(t)
	ref := domain.WorkspaceSkillRef("alpha")
	if _, err := s.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: ref, Description: "d", Content: "the body",
		Files: []domain.SkillFile{{Path: "scripts/run.sh", Content: "#!/bin/sh", Executable: true}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// SKILL.md addresses the skill's own body through the same route shape a
	// bundled file uses.
	body, err := s.Skills().GetFile(t.Context(), "WS", ref, domain.SkillFileNameSKILLMD)
	if err != nil {
		t.Fatalf("GetFile SKILL.md: %v", err)
	}
	if body.Content != "the body" || body.Revision == "" || body.Ref != ref {
		t.Errorf("SKILL.md document = %+v", body)
	}

	script, err := s.Skills().GetFile(t.Context(), "WS", ref, "scripts/run.sh")
	if err != nil {
		t.Fatalf("GetFile script: %v", err)
	}
	if !script.Executable || script.Revision == body.Revision {
		t.Errorf("script document = %+v, want its own revision and the executable bit", script)
	}

	if _, err := s.Skills().GetFile(t.Context(), "WS", ref, "missing.md"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetFile missing = %v, want ErrNotFound", err)
	}
}

// A per-document write touches one document and leaves its siblings alone —
// the whole reason the lane exists.
func TestSkillStore_PutFileLeavesSiblingsAlone(t *testing.T) {
	s := newSkillFixture(t)
	ref := domain.WorkspaceSkillRef("alpha")
	if _, err := s.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: ref, Description: "d", Content: "body",
		Files: []domain.SkillFile{{Path: "a.md", Content: "A"}, {Path: "b.md", Content: "B"}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	a, err := s.Skills().GetFile(t.Context(), "WS", ref, "a.md")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	written, err := s.Skills().PutFile(t.Context(), "WS", ref, store.SkillFileWrite{
		Path: "a.md", Content: "A2", IfMatch: a.Revision,
	})
	if err != nil {
		t.Fatalf("PutFile: %v", err)
	}
	if written.Content != "A2" || written.Revision == a.Revision {
		t.Errorf("written document = %+v, want new content and a new revision", written)
	}

	sk, err := s.Skills().Get(t.Context(), "WS", ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if b, _ := sk.FindFile("b.md"); b.Content != "B" {
		t.Errorf("sibling b.md = %q, want it untouched", b.Content)
	}
	if sk.Content != "body" {
		t.Errorf("SKILL.md body = %q, want it untouched", sk.Content)
	}
	// Order is stable: an unrelated edit must not reshuffle the set.
	if len(sk.Files) != 2 || sk.Files[0].Path != "a.md" || sk.Files[1].Path != "b.md" {
		t.Errorf("file order = %+v, want a.md then b.md", sk.Files)
	}
}

func TestSkillStore_PreconditionFailures(t *testing.T) {
	s := newSkillFixture(t)
	ref := domain.WorkspaceSkillRef("alpha")
	if _, err := s.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: ref, Description: "d", Content: "body",
		Files: []domain.SkillFile{{Path: "a.md", Content: "A"}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	current, err := s.Skills().GetFile(t.Context(), "WS", ref, "a.md")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}

	tests := []struct {
		name  string
		write store.SkillFileWrite
	}{
		{
			name:  "stale revision",
			write: store.SkillFileWrite{Path: "a.md", Content: "x", IfMatch: "0000000000000000"},
		},
		{
			name:  "if-none-match on an existing document",
			write: store.SkillFileWrite{Path: "a.md", Content: "x", IfNoneMatchAny: true},
		},
		{
			name:  "if-match on a document that is not there",
			write: store.SkillFileWrite{Path: "gone.md", Content: "x", IfMatch: current.Revision},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Skills().PutFile(t.Context(), "WS", ref, tt.write)
			if !errors.Is(err, domain.ErrSkillPreconditionFailed) {
				t.Fatalf("PutFile = %v, want ErrSkillPreconditionFailed", err)
			}
			// The two failure modes must not blur into each other.
			if errors.Is(err, domain.ErrSkillProvenanceConflict) {
				t.Errorf("a stale revision must not read as an ownership refusal: %v", err)
			}
			var stale *domain.SkillPreconditionError
			if !errors.As(err, &stale) || stale.Path != tt.write.Path {
				t.Errorf("detail = %+v, want it to name %q", stale, tt.write.Path)
			}
		})
	}

	// An unconditional write still goes through — a caller with no revision to
	// offer has nothing else available to it.
	if _, err := s.Skills().PutFile(t.Context(), "WS", ref, store.SkillFileWrite{
		Path: "a.md", Content: "unconditional",
	}); err != nil {
		t.Errorf("unconditional PutFile = %v, want nil", err)
	}
}

func TestSkillStore_DeleteFile(t *testing.T) {
	s := newSkillFixture(t)
	ref := domain.WorkspaceSkillRef("alpha")
	if _, err := s.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: ref, Description: "d", Content: "body",
		Files: []domain.SkillFile{{Path: "a.md", Content: "A"}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// SKILL.md is not removable through the file lane — the body IS the skill.
	if err := s.Skills().DeleteFile(t.Context(), "WS", ref, store.SkillFileDelete{
		Path: domain.SkillFileNameSKILLMD,
	}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("DeleteFile SKILL.md = %v, want ErrInvalid", err)
	}

	if err := s.Skills().DeleteFile(t.Context(), "WS", ref, store.SkillFileDelete{
		Path: "a.md", IfMatch: "0000000000000000",
	}); !errors.Is(err, domain.ErrSkillPreconditionFailed) {
		t.Errorf("DeleteFile with a stale revision = %v, want ErrSkillPreconditionFailed", err)
	}

	doc, err := s.Skills().GetFile(t.Context(), "WS", ref, "a.md")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if err := s.Skills().DeleteFile(t.Context(), "WS", ref, store.SkillFileDelete{
		Path: "a.md", IfMatch: doc.Revision, Source: "manual",
	}); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if _, err := s.Skills().GetFile(t.Context(), "WS", ref, "a.md"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetFile after DeleteFile = %v, want ErrNotFound", err)
	}
	if err := s.Skills().DeleteFile(t.Context(), "WS", ref, store.SkillFileDelete{Path: "a.md"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second DeleteFile = %v, want ErrNotFound", err)
	}
}

// Handing out the stored pointer would let a caller edit the store's copy in
// place, skipping the ownership guard entirely.
func TestSkillStore_HandsOutCopies(t *testing.T) {
	s := newSkillFixture(t)
	ref := domain.WorkspaceSkillRef("alpha")
	if _, err := s.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: ref, Description: "d",
		Files: []domain.SkillFile{{Path: "a.md", Content: "A"}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Skills().Get(t.Context(), "WS", ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got.Files[0].Content = "tampered"
	got.Description = "tampered"

	again, err := s.Skills().Get(t.Context(), "WS", ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if again.Files[0].Content != "A" || again.Description != "d" {
		t.Errorf("stored skill was mutated through a returned copy: %+v", again)
	}
}

func TestSkillPackStoreCRUD(t *testing.T) {
	s := New()
	packs := s.SkillPacks()

	created, err := packs.Create(t.Context(), store.SkillPackCreate{
		WorkspaceKey: "WS", Name: "design", RepoURL: "https://example.test/design.git", Ref: "main",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Source() != "pack:design" {
		t.Errorf("Source() = %q, want pack:design", created.Source())
	}
	if created.LastSyncStatus != "" {
		t.Errorf("a new pack reported a sync status: %q", created.LastSyncStatus)
	}

	if _, err := packs.Create(t.Context(), store.SkillPackCreate{
		WorkspaceKey: "WS", Name: "design", RepoURL: "https://example.test/other.git",
	}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("duplicate Create = %v, want ErrAlreadyExists", err)
	}

	if _, err := packs.Create(t.Context(), store.SkillPackCreate{
		WorkspaceKey: "WS", Name: "Design Pack", RepoURL: "https://example.test/x.git",
	}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("Create with a bad name = %v, want ErrInvalid", err)
	}

	synced, err := packs.Update(t.Context(), "WS", "design", store.SkillPackUpdate{
		RecordSync: &domain.SkillPackSync{
			Status: domain.SkillPackSyncOK, Commit: "abc123", Skills: []string{"pr-review"},
		},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if synced.LastSyncedCommit != "abc123" || synced.LastSyncStatus != domain.SkillPackSyncOK {
		t.Errorf("sync record = %+v", synced)
	}
	if synced.LastSyncedAt.IsZero() {
		t.Errorf("last_synced_at was not stamped")
	}

	// A failed refresh must not rewrite what is installed: the commit on
	// record is still the one that actually landed.
	failed, err := packs.Update(t.Context(), "WS", "design", store.SkillPackUpdate{
		RecordSync: &domain.SkillPackSync{Status: domain.SkillPackSyncFailed, Error: "auth denied"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if failed.LastSyncedCommit != "abc123" || failed.LastSyncError != "auth denied" {
		t.Errorf("after a failed sync = %+v", failed)
	}

	list, err := packs.List(t.Context(), "WS")
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v", list, err)
	}

	if err := packs.Delete(t.Context(), "WS", "design"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := packs.Get(t.Context(), "WS", "design"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
}

// The in-memory double accepts the same revision forms the HTTP client does,
// so a caller that works against one works against the other. Without this a
// handler passing a browser's quoted If-Match would pass every memstore test
// and 412 in production.
func TestSkillStore_AcceptsEveryRevisionFormACallerHolds(t *testing.T) {
	s := newSkillFixture(t)
	ref := domain.WorkspaceSkillRef("alpha")
	if _, err := s.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: ref, Description: "d",
		Files: []domain.SkillFile{{Path: "a.md", Content: "A"}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	forms := []struct{ name, wrap string }{
		{name: "bare revision", wrap: "%s"},
		{name: "quoted entity-tag", wrap: `"%s"`},
		{name: "weak entity-tag", wrap: `W/"%s"`},
	}
	for i, form := range forms {
		t.Run(form.name, func(t *testing.T) {
			doc, err := s.Skills().GetFile(t.Context(), "WS", ref, "a.md")
			if err != nil {
				t.Fatalf("GetFile: %v", err)
			}
			if _, err := s.Skills().PutFile(t.Context(), "WS", ref, store.SkillFileWrite{
				Path: "a.md", Content: fmt.Sprintf("edit %d", i), IfMatch: fmt.Sprintf(form.wrap, doc.Revision),
			}); err != nil {
				t.Fatalf("conditional PutFile with a %s = %v, want it to succeed", form.name, err)
			}
		})
	}

	// A multi-tag header is refused rather than reduced to one of its tags.
	_, err := s.Skills().PutFile(t.Context(), "WS", ref, store.SkillFileWrite{
		Path: "a.md", Content: "x", IfMatch: `"aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"`,
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("multi-tag If-Match = %v, want ErrInvalid", err)
	}
}

// memstore stands in for fleet-db in tests, so a write it accepts that the real
// server refuses is a test passing on data production cannot produce. These
// pin the bundled-path rules on both write routes.
func TestSkillStoreRejectsBundledPathsFleetDBRefuses(t *testing.T) {
	ctx := t.Context()
	unsafe := []string{"../escape.md", "/absolute.md", "nested/../../escape.md", "skill.md"}

	for _, badPath := range unsafe {
		t.Run("create "+badPath, func(t *testing.T) {
			st := New()
			if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "WS"}); err != nil {
				t.Fatalf("create workspace: %v", err)
			}
			_, err := st.Skills().Create(ctx, store.SkillCreate{
				WorkspaceKey: "WS",
				Ref:          domain.WorkspaceSkillRef("alpha"),
				Description:  "alpha",
				Content:      "body\n",
				Files:        []domain.SkillFile{{Path: badPath, Content: "x"}},
			})
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("Create with bundled path %q = %v, want ErrInvalid", badPath, err)
			}
		})

		t.Run("put file "+badPath, func(t *testing.T) {
			st := New()
			if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "WS"}); err != nil {
				t.Fatalf("create workspace: %v", err)
			}
			ref := domain.WorkspaceSkillRef("alpha")
			if _, err := st.Skills().Create(ctx, store.SkillCreate{
				WorkspaceKey: "WS", Ref: ref, Description: "alpha", Content: "body\n",
			}); err != nil {
				t.Fatalf("create skill: %v", err)
			}
			_, err := st.Skills().PutFile(ctx, "WS", ref, store.SkillFileWrite{
				Path: badPath, Content: "x", IfNoneMatchAny: true,
			})
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("PutFile at %q = %v, want ErrInvalid", badPath, err)
			}
		})
	}
}
