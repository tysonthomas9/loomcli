package memstore

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestWorkspaceCRUD smoke-tests the Create/Get/List/Update/Delete cycle
// for the workspace store. Failures here indicate the in-memory store
// has diverged from the WorkspaceStore contract — the contract is also
// what the production fleetdb store must satisfy.
func TestWorkspaceCRUD(t *testing.T) {
	ctx := context.Background()
	s := New()

	created, err := s.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "MYWS", Name: "My Workspace"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Key != "MYWS" || created.Name != "My Workspace" {
		t.Fatalf("Create returned wrong fields: %+v", created)
	}
	if created.State != domain.WorkspaceStateReady {
		t.Errorf("expected default state=ready, got %q", created.State)
	}

	// Duplicate Create → ErrAlreadyExists.
	if _, err := s.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "MYWS", Name: "dup"}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("duplicate Create: want ErrAlreadyExists, got %v", err)
	}

	// Get round-trip.
	got, err := s.Workspaces().Get(ctx, "MYWS")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Key != "MYWS" {
		t.Errorf("Get key: want MYWS, got %q", got.Key)
	}

	// Get unknown → ErrNotFound.
	if _, err := s.Workspaces().Get(ctx, "NOPE"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get unknown: want ErrNotFound, got %v", err)
	}

	// Update name.
	newName := "Renamed"
	if _, err := s.Workspaces().Update(ctx, "MYWS", store.WorkspaceUpdate{Name: &newName}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = s.Workspaces().Get(ctx, "MYWS")
	if got.Name != "Renamed" {
		t.Errorf("Update name: want Renamed, got %q", got.Name)
	}

	// List.
	list, err := s.Workspaces().List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Key != "MYWS" {
		t.Errorf("List: want [MYWS], got %+v", list)
	}

	// Delete.
	if err := s.Workspaces().Delete(ctx, "MYWS"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Workspaces().Get(ctx, "MYWS"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("after Delete: want ErrNotFound, got %v", err)
	}
}

// TestRepoCRUD smoke-tests the workspace-scoped repo store. Most of the
// interesting bugs live in the workspace+name compound key — make sure
// repos under different workspaces don't collide.
func TestRepoCRUD(t *testing.T) {
	ctx := context.Background()
	s := New()

	r1, err := s.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "WSA",
		Name:          "backend",
		RemoteURL:     "https://github.com/foo/backend.git",
		DefaultBranch: "main",
		Groups:        []string{"infra"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r1.SourceRepoID != "backend" {
		t.Errorf("SourceRepoID default: want 'backend', got %q", r1.SourceRepoID)
	}

	// Same name in a different workspace must succeed.
	if _, err := s.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "WSB", Name: "backend"}); err != nil {
		t.Errorf("Create WSB/backend: should succeed (different workspace), got %v", err)
	}

	// Same name in the same workspace must error.
	if _, err := s.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "WSA", Name: "backend"}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("dup Create: want ErrAlreadyExists, got %v", err)
	}

	// List is workspace-scoped.
	wsa, _ := s.Repos().List(ctx, "WSA")
	wsb, _ := s.Repos().List(ctx, "WSB")
	if len(wsa) != 1 || len(wsb) != 1 {
		t.Errorf("List scoping broken: WSA=%d, WSB=%d", len(wsa), len(wsb))
	}
}

func TestRoleStoreKindCreatePatchClear(t *testing.T) {
	ctx := context.Background()
	s := New()

	role, err := s.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "WS",
		Name:         "operator",
		Kind:         string(domain.RoleKindInteractive),
		Prompt:       "literal inline prompt",
	})
	if err != nil {
		t.Fatalf("Create role: %v", err)
	}
	if role.Kind != domain.RoleKindInteractive {
		t.Fatalf("created kind = %q, want interactive", role.Kind)
	}
	if role.Prompt != "literal inline prompt" {
		t.Fatalf("created prompt = %q, want literal inline prompt", role.Prompt)
	}

	nextPrompt := "updated inline prompt"
	role, err = s.Roles().Update(ctx, "WS", "operator", store.RoleUpdate{Prompt: &nextPrompt})
	if err != nil {
		t.Fatalf("Update role prompt: %v", err)
	}
	if role.Prompt != nextPrompt {
		t.Fatalf("updated prompt = %q, want %q", role.Prompt, nextPrompt)
	}

	worker := string(domain.RoleKindWorker)
	role, err = s.Roles().Update(ctx, "WS", "operator", store.RoleUpdate{Kind: &worker})
	if err != nil {
		t.Fatalf("Update role kind: %v", err)
	}
	if role.Kind != domain.RoleKindWorker {
		t.Fatalf("updated kind = %q, want worker", role.Kind)
	}

	clear := ""
	role, err = s.Roles().Update(ctx, "WS", "operator", store.RoleUpdate{Kind: &clear})
	if err != nil {
		t.Fatalf("Clear role kind: %v", err)
	}
	if role.Kind != "" {
		t.Fatalf("cleared kind = %q, want empty", role.Kind)
	}
}

// TestDaemonProfileGetReturnsDefaults verifies that Get on a workspace
// with no explicit Upsert returns sensible defaults rather than
// ErrNotFound — the API contract is "every workspace has a profile".
func TestDaemonProfileGetReturnsDefaults(t *testing.T) {
	ctx := context.Background()
	s := New()

	p, err := s.Daemon().Get(ctx, "MYWS")
	if err != nil {
		t.Fatalf("Get default: %v", err)
	}
	if p.WorkspaceKey != "MYWS" {
		t.Errorf("WorkspaceKey: want MYWS, got %q", p.WorkspaceKey)
	}
	if p.IssueBackend != "fleetdb" {
		t.Errorf("IssueBackend default: want fleetdb, got %q", p.IssueBackend)
	}
}
