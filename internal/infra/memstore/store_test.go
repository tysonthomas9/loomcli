package memstore

import (
	"context"
	"errors"
	"testing"

	agentsowner "github.com/tysonthomas9/loomcli/internal/modules/agents"
	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// TestWorkspaceCRUD smoke-tests the Create/Get/List/Update/Delete cycle
// for the workspace store. Failures here indicate the in-memory store
// has diverged from the WorkspaceStore contract — the contract is also
// what the production fleetdb store must satisfy.
func TestWorkspaceCRUD(t *testing.T) {
	ctx := context.Background()
	s := New()

	created, err := s.Workspaces().Create(ctx, workspaceowner.WorkspaceCreate{Key: "MYWS", Name: "My Workspace"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Key != "MYWS" || created.Name != "My Workspace" {
		t.Fatalf("Create returned wrong fields: %+v", created)
	}
	if created.State != workspaceowner.StateReady {
		t.Errorf("expected default state=ready, got %q", created.State)
	}

	// Duplicate Create → ErrAlreadyExists.
	if _, err := s.Workspaces().Create(ctx, workspaceowner.WorkspaceCreate{Key: "MYWS", Name: "dup"}); !errors.Is(err, persistence.ErrAlreadyExists) {
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
	if _, err := s.Workspaces().Get(ctx, "NOPE"); !errors.Is(err, persistence.ErrNotFound) {
		t.Errorf("Get unknown: want ErrNotFound, got %v", err)
	}

	// Update name.
	newName := "Renamed"
	if _, err := s.Workspaces().Update(ctx, "MYWS", workspaceowner.WorkspaceUpdate{Name: &newName}); err != nil {
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
	if _, err := s.Workspaces().Get(ctx, "MYWS"); !errors.Is(err, persistence.ErrNotFound) {
		t.Errorf("after Delete: want ErrNotFound, got %v", err)
	}
}

// TestWorkspaceDesignFormatRoundTrip verifies design_format survives the
// Create/Get/Update cycle, including clearing it via a pointer to "".
func TestWorkspaceDesignFormatRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := New()

	created, err := s.Workspaces().Create(ctx, workspaceowner.WorkspaceCreate{Key: "MYWS", Name: "My Workspace", DesignFormat: "html"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.DesignFormat != "html" {
		t.Errorf("Create DesignFormat = %q, want html", created.DesignFormat)
	}

	got, err := s.Workspaces().Get(ctx, "MYWS")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DesignFormat != "html" {
		t.Errorf("Get DesignFormat = %q, want html", got.DesignFormat)
	}

	// Update to markdown.
	markdown := "markdown"
	if _, err := s.Workspaces().Update(ctx, "MYWS", workspaceowner.WorkspaceUpdate{DesignFormat: &markdown}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = s.Workspaces().Get(ctx, "MYWS")
	if got.DesignFormat != "markdown" {
		t.Errorf("after Update DesignFormat = %q, want markdown", got.DesignFormat)
	}

	// Update with nil pointer leaves the value untouched.
	newName := "Renamed"
	if _, err := s.Workspaces().Update(ctx, "MYWS", workspaceowner.WorkspaceUpdate{Name: &newName}); err != nil {
		t.Fatalf("Update name: %v", err)
	}
	got, _ = s.Workspaces().Get(ctx, "MYWS")
	if got.DesignFormat != "markdown" {
		t.Errorf("DesignFormat after unrelated update = %q, want markdown", got.DesignFormat)
	}

	// Clear via pointer-to-"".
	empty := ""
	if _, err := s.Workspaces().Update(ctx, "MYWS", workspaceowner.WorkspaceUpdate{DesignFormat: &empty}); err != nil {
		t.Fatalf("Update clear: %v", err)
	}
	got, _ = s.Workspaces().Get(ctx, "MYWS")
	if got.DesignFormat != "" {
		t.Errorf("cleared DesignFormat = %q, want empty", got.DesignFormat)
	}
}

// TestRepoCRUD smoke-tests the workspace-scoped repo store. Most of the
// interesting bugs live in the workspace+name compound key — make sure
// repos under different workspaces don't collide.
func TestRepoCRUD(t *testing.T) {
	ctx := context.Background()
	s := New()

	r1, err := s.Repos().Create(ctx, workspaceowner.RepoCreate{
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
	if _, err := s.Repos().Create(ctx, workspaceowner.RepoCreate{WorkspaceKey: "WSB", Name: "backend"}); err != nil {
		t.Errorf("Create WSB/backend: should succeed (different workspace), got %v", err)
	}

	// Same name in the same workspace must error.
	if _, err := s.Repos().Create(ctx, workspaceowner.RepoCreate{WorkspaceKey: "WSA", Name: "backend"}); !errors.Is(err, persistence.ErrAlreadyExists) {
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

	role, err := s.Roles().Create(ctx, agentsowner.RoleRecordCreate{
		WorkspaceKey: "WS",
		Name:         "operator",
		Kind:         string(agentsowner.RoleKindInteractive),
		Prompt:       "literal inline prompt",
	})
	if err != nil {
		t.Fatalf("Create role: %v", err)
	}
	if role.Kind != agentsowner.RoleKindInteractive {
		t.Fatalf("created kind = %q, want interactive", role.Kind)
	}
	if role.Prompt != "literal inline prompt" {
		t.Fatalf("created prompt = %q, want literal inline prompt", role.Prompt)
	}

	nextPrompt := "updated inline prompt"
	role, err = s.Roles().Update(ctx, "WS", "operator", agentsowner.RoleRecordUpdate{Prompt: &nextPrompt})
	if err != nil {
		t.Fatalf("Update role prompt: %v", err)
	}
	if role.Prompt != nextPrompt {
		t.Fatalf("updated prompt = %q, want %q", role.Prompt, nextPrompt)
	}

	worker := string(agentsowner.RoleKindWorker)
	role, err = s.Roles().Update(ctx, "WS", "operator", agentsowner.RoleRecordUpdate{Kind: &worker})
	if err != nil {
		t.Fatalf("Update role kind: %v", err)
	}
	if role.Kind != agentsowner.RoleKindWorker {
		t.Fatalf("updated kind = %q, want worker", role.Kind)
	}

	clear := ""
	role, err = s.Roles().Update(ctx, "WS", "operator", agentsowner.RoleRecordUpdate{Kind: &clear})
	if err != nil {
		t.Fatalf("Clear role kind: %v", err)
	}
	if role.Kind != "" {
		t.Fatalf("cleared kind = %q, want empty", role.Kind)
	}
}
