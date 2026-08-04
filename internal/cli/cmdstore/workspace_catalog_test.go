package cmdstore

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestWorkspaceCatalogComposesOwnerAPIAndResolvesCanonicalActiveKey(t *testing.T) {
	ctx := context.Background()
	backend := memstore.New()
	t.Cleanup(func() { _ = backend.Close() })
	if _, err := backend.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha"}); err != nil {
		t.Fatal(err)
	}
	api, err := WorkspaceCatalog(&bootstrap.StoreHandle{Store: backend})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(bootstrap.EnvWorkspace, "Alpha")
	key, err := ActiveWorkspaceCatalog(ctx, api)
	if err != nil || key != "ALPHA" {
		t.Fatalf("active key=%q err=%v", key, err)
	}
	if _, err := api.RegisterRepository(ctx, workspacemodule.RegisterRepositoryCommand{
		WorkspaceReference: key,
		Name:               "loom",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestActiveWorkspaceCatalogRequiresExplicitSelection(t *testing.T) {
	t.Setenv(bootstrap.EnvWorkspace, "")
	if _, err := ActiveWorkspaceCatalog(context.Background(), nil); !errors.Is(err, bootstrap.ErrNoActiveWorkspace) {
		t.Fatalf("error=%v, want ErrNoActiveWorkspace", err)
	}
}
