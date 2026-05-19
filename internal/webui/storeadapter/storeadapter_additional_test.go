package storeadapter

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestWorkspacePathHelpersAndNameResolution(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })

	if paths, err := ListWorkspacePaths(ctx, nil); err != nil || paths != nil {
		t.Fatalf("nil ListWorkspacePaths = %#v, %v", paths, err)
	}
	if _, err := ResolveWorkspaceKeyByName(ctx, nil, "x"); err == nil {
		t.Fatal("nil ResolveWorkspaceKeyByName error = nil")
	}
	if got := DefaultWorkspaceKey(); got != "" {
		t.Fatalf("DefaultWorkspaceKey = %q", got)
	}

	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS2", Name: "Beta"}); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Alpha"}); err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey: "WS1",
		Name:         "app",
		Groups:       []string{"core"},
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS1", Name: "task"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS1",
		Name:         "nova",
		RoleName:     "task",
		Repos:        []string{"app"},
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS1": {Path: "/workspace/alpha", Repos: map[string]string{"app": "/workspace/alpha/app"}},
			"WS2": {Path: "/workspace/beta"},
		},
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	paths, err := ListWorkspacePaths(ctx, st)
	if err != nil {
		t.Fatalf("ListWorkspacePaths: %v", err)
	}
	if paths["WS1"] != "/workspace/alpha" || paths["WS2"] != "/workspace/beta" {
		t.Fatalf("paths = %#v", paths)
	}
	if got, err := ResolveWorkspaceKeyByName(ctx, st, "Alpha"); err != nil || got != "WS1" {
		t.Fatalf("ResolveWorkspaceKeyByName Alpha = %q, %v", got, err)
	}
	if got, err := ResolveWorkspaceKeyByName(ctx, st, "WS2"); err != nil || got != "WS2" {
		t.Fatalf("ResolveWorkspaceKeyByName WS2 = %q, %v", got, err)
	}
	if got := ResolveWorkspacePath("WS1"); got != "/workspace/alpha" {
		t.Fatalf("ResolveWorkspacePath = %q", got)
	}

	data, err := BuildWorkspaceDataForKey(ctx, st, "WS1")
	if err != nil {
		t.Fatalf("BuildWorkspaceDataForKey: %v", err)
	}
	if data.Path != "/workspace/alpha" || len(data.Repos) != 1 || data.Repos[0].Path != "/workspace/alpha/app" {
		t.Fatalf("workspace data = %#v", data)
	}
}
