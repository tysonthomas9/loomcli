package memstore

import (
	"bytes"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

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

func publishSkillTree(t *testing.T, s *Store, name, body string, bundles ...domain.SkillFileTreeFile) string {
	return publishSkillTreeWithDescription(t, s, name, "does a thing", body, bundles...)
}

func publishSkillTreeWithDescription(t *testing.T, s *Store, name, description, body string, bundles ...domain.SkillFileTreeFile) string {
	t.Helper()
	snapshot, err := domain.BuildSkillFileTree(name, description, []byte(body), bundles)
	if err != nil {
		t.Fatalf("BuildSkillFileTree: %v", err)
	}
	inputs := make([]domain.WorkspaceFileInput, 0, len(snapshot.Files))
	for _, file := range snapshot.Files {
		inputs = append(inputs, domain.WorkspaceFileInput(file))
	}
	published, err := s.WorkspaceFiles().Publish(t.Context(), "WS", inputs)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	return published.Tree.Revision
}

func mustCreateSkill(t *testing.T, s *Store, ref domain.SkillRef, body string) *domain.Skill {
	t.Helper()
	revision := publishSkillTree(t, s, ref.Name, body)
	sk, err := s.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: ref, Description: "does a thing", FileTreeRevision: revision, Source: "manual",
	})
	if err != nil {
		t.Fatalf("Create %v: %v", ref, err)
	}
	return sk
}

func TestSkillStoreCRUDUsesImmutableTreePointer(t *testing.T) {
	s := newSkillFixture(t)
	ref := domain.WorkspaceSkillRef("pr-review")
	created := mustCreateSkill(t, s, ref, "body")
	if created.CreatedBy != defaultSkillActor || created.FileTreeRevision == "" {
		t.Fatalf("created = %+v", created)
	}
	if _, err := s.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: ref, Description: "does a thing", FileTreeRevision: created.FileTreeRevision,
	}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate Create = %v, want ErrAlreadyExists", err)
	}
	got, err := s.Skills().Get(t.Context(), "WS", ref)
	if err != nil || got.FileTreeRevision != created.FileTreeRevision {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	description := "edited"
	if _, err := s.Skills().Update(t.Context(), "WS", ref, store.SkillUpdate{Description: &description}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("description-only Update = %v, want ErrInvalid", err)
	}
	editedRevision := publishSkillTreeWithDescription(t, s, ref.Name, description, "body")
	updated, err := s.Skills().Update(t.Context(), "WS", ref, store.SkillUpdate{
		Description: &description, FileTreeRevision: &editedRevision,
		ExpectedFileTreeRevision: created.FileTreeRevision,
	})
	if err != nil || updated.Description != description || updated.FileTreeRevision != editedRevision {
		t.Fatalf("metadata Update = %+v, %v", updated, err)
	}
	if err := s.Skills().Delete(t.Context(), "WS", ref, store.SkillDelete{}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Skills().Get(t.Context(), "WS", ref); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
}

func TestSkillStoreRejectsMissingTree(t *testing.T) {
	s := newSkillFixture(t)
	_, err := s.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: domain.WorkspaceSkillRef("alpha"), Description: "d", FileTreeRevision: "missing",
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Create with missing tree = %v, want ErrNotFound", err)
	}
}

func TestSkillStoreRejectsInvalidOrMismatchedReferencedTree(t *testing.T) {
	s := newSkillFixture(t)
	published, err := s.WorkspaceFiles().Publish(t.Context(), "WS", []domain.WorkspaceFileInput{{Path: "notes.md", Bytes: []byte("not a skill")}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: domain.WorkspaceSkillRef("alpha"), Description: "d",
		FileTreeRevision: published.Tree.Revision,
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("Create with invalid tree = %v, want ErrInvalid", err)
	}
	mismatched := publishSkillTree(t, s, "other", "body")
	_, err = s.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: domain.WorkspaceSkillRef("alpha"), Description: "does a thing",
		FileTreeRevision: mismatched,
	})
	if !errors.Is(err, domain.ErrIntegrity) {
		t.Fatalf("Create with mismatched tree = %v, want ErrIntegrity", err)
	}
}

func TestSkillStoreTreeCASLeavesPublishedOrphanReadable(t *testing.T) {
	s := newSkillFixture(t)
	ref := domain.WorkspaceSkillRef("alpha")
	oldRevision := publishSkillTreeWithDescription(t, s, "alpha", "d", "old")
	if _, err := s.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: ref, Description: "d", FileTreeRevision: oldRevision,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	winningRevision := publishSkillTreeWithDescription(t, s, "alpha", "d", "winner")
	if _, err := s.Skills().Update(t.Context(), "WS", ref, store.SkillUpdate{
		FileTreeRevision: &winningRevision, ExpectedFileTreeRevision: oldRevision,
	}); err != nil {
		t.Fatalf("winning Update: %v", err)
	}
	orphanRevision := publishSkillTreeWithDescription(t, s, "alpha", "d", "loser")
	_, err := s.Skills().Update(t.Context(), "WS", ref, store.SkillUpdate{
		FileTreeRevision: &orphanRevision, ExpectedFileTreeRevision: oldRevision,
	})
	if !errors.Is(err, domain.ErrSkillPreconditionFailed) {
		t.Fatalf("stale Update = %v, want ErrSkillPreconditionFailed", err)
	}
	var stale *domain.SkillPreconditionError
	if !errors.As(err, &stale) || stale.Expected != oldRevision || stale.Stored != winningRevision {
		t.Fatalf("precondition detail = %+v", stale)
	}
	current, err := s.Skills().Get(t.Context(), "WS", ref)
	if err != nil || current.FileTreeRevision != winningRevision {
		t.Fatalf("current = %+v, %v", current, err)
	}
	if _, err := s.WorkspaceFiles().GetTree(t.Context(), "WS", orphanRevision); err != nil {
		t.Fatalf("orphan tree was not retained: %v", err)
	}
	body, err := s.WorkspaceFiles().Download(t.Context(), "WS", orphanRevision, domain.SkillFileNameSKILLMD)
	if err != nil || !bytes.Contains(body, []byte("loser")) {
		t.Fatalf("orphan SKILL.md = %q, %v", body, err)
	}
}

func TestSkillStoreScopesResolveThroughTheChain(t *testing.T) {
	s := newSkillFixture(t, "lead", "task")
	mustCreateSkill(t, s, domain.WorkspaceSkillRef("review"), "workspace copy")
	lead := mustCreateSkill(t, s, domain.RoleSkillRef("lead", "review"), "lead copy")
	mustCreateSkill(t, s, domain.RoleSkillRef("task", "review"), "task copy")
	mustCreateSkill(t, s, domain.WorkspaceSkillRef("shared"), "everyone")
	all, err := s.Skills().List(t.Context(), "WS", store.SkillFilter{})
	if err != nil || len(all) != 4 {
		t.Fatalf("List = %d, %v", len(all), err)
	}
	resolved := domain.ResolveSkillChain(all, "lead")
	if len(resolved) != 2 || resolved[0].FileTreeRevision != lead.FileTreeRevision || resolved[1].Name != "shared" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestSkillStoreListFilters(t *testing.T) {
	s := newSkillFixture(t, "lead", "task")
	mustCreateSkill(t, s, domain.WorkspaceSkillRef("shared"), "w")
	mustCreateSkill(t, s, domain.RoleSkillRef("lead", "review"), "r")
	mustCreateSkill(t, s, domain.RoleSkillRef("task", "triage"), "r")
	role, err := s.Skills().List(t.Context(), "WS", store.SkillFilter{Scope: domain.SkillScopeRole, RoleName: "lead"})
	if err != nil || len(role) != 1 || role[0].Name != "review" {
		t.Fatalf("role listing = %+v, %v", role, err)
	}
	workspace, err := s.Skills().List(t.Context(), "WS", store.SkillFilter{Scope: domain.SkillScopeWorkspace})
	if err != nil || len(workspace) != 1 || workspace[0].Name != "shared" {
		t.Fatalf("workspace listing = %+v, %v", workspace, err)
	}
}

func TestSkillStoreRoleAndOwnershipGuards(t *testing.T) {
	s := newSkillFixture(t, "lead")
	revision := publishSkillTree(t, s, "alpha", "body")
	if _, err := s.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: "WS", Ref: domain.RoleSkillRef("ghost", "alpha"), Description: "d", FileTreeRevision: revision,
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Create against missing role = %v, want ErrNotFound", err)
	}
	ref := domain.RoleSkillRef("lead", "alpha")
	s.SetSkillActor("alice")
	mustCreateSkill(t, s, ref, "alice")
	s.SetSkillActor("bob")
	bobRevision := publishSkillTreeWithDescription(t, s, "alpha", "bob", "bob")
	in := store.SkillUpsert{Skill: store.SkillCreate{
		WorkspaceKey: "WS", Ref: ref, Description: "bob", FileTreeRevision: bobRevision, Source: "manual",
	}}
	if _, _, err := s.Skills().Upsert(t.Context(), in); !errors.Is(err, domain.ErrSkillProvenanceConflict) {
		t.Fatalf("Upsert as bob = %v", err)
	}
	in.Force = true
	forced, created, err := s.Skills().Upsert(t.Context(), in)
	if err != nil || created || forced.CreatedBy != "alice" || forced.UpdatedBy != "bob" {
		t.Fatalf("forced Upsert = %+v, created=%v, %v", forced, created, err)
	}
}

func TestRoleDeleteRefusedWhileItOwnsSkills(t *testing.T) {
	s := newSkillFixture(t, "lead")
	ref := domain.RoleSkillRef("lead", "review")
	mustCreateSkill(t, s, ref, "r")
	if err := s.Roles().Delete(t.Context(), "WS", "lead"); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Delete role = %v, want ErrInvalidTransition", err)
	}
	if err := s.Skills().Delete(t.Context(), "WS", ref, store.SkillDelete{}); err != nil {
		t.Fatalf("Delete skill: %v", err)
	}
	if err := s.Roles().Delete(t.Context(), "WS", "lead"); err != nil {
		t.Fatalf("Delete role after skill = %v", err)
	}
}

func TestSkillStoreHandsOutCopies(t *testing.T) {
	s := newSkillFixture(t)
	ref := domain.WorkspaceSkillRef("alpha")
	mustCreateSkill(t, s, ref, "body")
	got, err := s.Skills().Get(t.Context(), "WS", ref)
	if err != nil {
		t.Fatal(err)
	}
	got.Description = "tampered"
	again, err := s.Skills().Get(t.Context(), "WS", ref)
	if err != nil || again.Description != "does a thing" {
		t.Fatalf("stored skill mutated through copy: %+v, %v", again, err)
	}
}

func TestSkillPackStoreCRUD(t *testing.T) {
	s := New()
	packs := s.SkillPacks()
	created, err := packs.Create(t.Context(), store.SkillPackCreate{
		WorkspaceKey: "WS", Name: "design", RepoURL: "https://example.test/design.git", Ref: "main",
	})
	if err != nil || created.Source() != "pack:design" {
		t.Fatalf("Create = %+v, %v", created, err)
	}
	synced, err := packs.Update(t.Context(), "WS", "design", store.SkillPackUpdate{RecordSync: &domain.SkillPackSync{
		Status: domain.SkillPackSyncOK, Commit: "abc123", Skills: []string{"pr-review"},
	}})
	if err != nil || synced.LastSyncedCommit != "abc123" || synced.LastSyncedAt.IsZero() {
		t.Fatalf("Update = %+v, %v", synced, err)
	}
	if err := packs.Delete(t.Context(), "WS", "design"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := packs.Get(t.Context(), "WS", "design"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get after Delete = %v", err)
	}
}
