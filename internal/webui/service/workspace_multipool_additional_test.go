package service

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/store"
	webuidaemon "github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

func TestListWorkspacesIncludesMultiPoolStats(t *testing.T) {
	ctx := context.Background()
	mp := webuidaemon.NewMultiPool(func(context.Context) string { return "" }, 1)
	t.Cleanup(func() { _ = mp.Close() })
	pool := &workspaceServiceFakePool{stats: webuidaemon.PoolStats{Size: 3, Created: 2, Active: 1, Available: 1}}
	if err := mp.Register("WS", pool); err != nil {
		t.Fatalf("register pool: %v", err)
	}

	noStoreSvc := NewWorkspaceService(WorkspaceServiceConfig{MultiPool: mp})
	items, err := noStoreSvc.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces no store: %v", err)
	}
	if len(items) != 1 || items[0].ID != "WS" || items[0].Pool == nil || items[0].Pool.Active != 1 {
		t.Fatalf("no-store items = %+v", items)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	storeSvc := NewWorkspaceService(WorkspaceServiceConfig{Store: st, MultiPool: mp})
	items, err = storeSvc.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces store: %v", err)
	}
	if len(items) != 1 || items[0].Pool == nil || items[0].Pool.Size != 3 {
		t.Fatalf("store items = %+v", items)
	}
}

func TestWorkspaceBackendPatchExistingProfileAndResolveErrors(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Daemon().Upsert(ctx, &domain.DaemonProfile{WorkspaceKey: "ALPHA", AgentBackend: "claude"}); err != nil {
		t.Fatalf("upsert daemon: %v", err)
	}
	svc := NewWorkspaceService(WorkspaceServiceConfig{Store: st})

	data, err := svc.PatchWorkspaceBackend(ctx, "Alpha", "opencode")
	if err != nil {
		t.Fatalf("PatchWorkspaceBackend existing profile: %v", err)
	}
	if data.ID != "ALPHA" {
		t.Fatalf("patched data = %+v", data)
	}
	profile, err := st.Daemon().Get(ctx, "ALPHA")
	if err != nil {
		t.Fatalf("get daemon profile: %v", err)
	}
	if profile.AgentBackend != "opencode" {
		t.Fatalf("AgentBackend = %q, want opencode", profile.AgentBackend)
	}

	if _, serr := svc.RenameWorkspace(ctx, "ALPHA", " "); !serviceErrorKind(serr, KindValidation) {
		t.Fatalf("RenameWorkspace invalid name err = %v", serr)
	}
}

type workspaceServiceFakePool struct {
	stats webuidaemon.PoolStats
}

func (p *workspaceServiceFakePool) Get(context.Context) (*rpc.Client, error) { return nil, nil }
func (p *workspaceServiceFakePool) Put(*rpc.Client)                          {}
func (p *workspaceServiceFakePool) PutAfterError(*rpc.Client)                {}
func (p *workspaceServiceFakePool) Discard(*rpc.Client)                      {}
func (p *workspaceServiceFakePool) Stats() webuidaemon.PoolStats             { return p.stats }
func (p *workspaceServiceFakePool) Close() error                             { return nil }
