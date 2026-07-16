package issues

import (
	"context"
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	storepkg "github.com/tysonthomas9/loomcli/internal/store"
)

type getFailWorkspaceStore struct {
	storepkg.WorkspaceStore
}

func (s *getFailWorkspaceStore) Get(_ context.Context, key string) (*domain.Workspace, error) {
	return nil, fmt.Errorf("fleetdb: GET /workspaces/%s: HTTP 403: forbidden", key)
}

type workspaceStoreOverride struct {
	storepkg.Store
	ws storepkg.WorkspaceStore
}

func (s *workspaceStoreOverride) Workspaces() storepkg.WorkspaceStore {
	return s.ws
}

func TestResolveWorkspaceRef_FallsBackToNameWhenGetFails(t *testing.T) {
	base := memstore.New()
	if _, err := base.Workspaces().Create(context.Background(), storepkg.WorkspaceCreate{
		Key:  "LOOMCLI",
		Name: "loomcli",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	st := &workspaceStoreOverride{
		Store: base,
		ws:    &getFailWorkspaceStore{WorkspaceStore: base.Workspaces()},
	}
	key, name, err := resolveWorkspaceRef(context.Background(), st, "loomcli")
	if err != nil {
		t.Fatalf("resolveWorkspaceRef() err = %v", err)
	}
	if key != "LOOMCLI" || name != "loomcli" {
		t.Fatalf("resolveWorkspaceRef() = (%q, %q), want (LOOMCLI, loomcli)", key, name)
	}
}

func TestResolveWorkspaceRef_ResolvesByName(t *testing.T) {
	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), storepkg.WorkspaceCreate{
		Key:  "LOOMCLI",
		Name: "loomcli",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	key, name, err := resolveWorkspaceRef(context.Background(), st, "loomcli")
	if err != nil {
		t.Fatalf("resolveWorkspaceRef() err = %v", err)
	}
	if key != "LOOMCLI" || name != "loomcli" {
		t.Fatalf("resolveWorkspaceRef() = (%q, %q), want (LOOMCLI, loomcli)", key, name)
	}
}
