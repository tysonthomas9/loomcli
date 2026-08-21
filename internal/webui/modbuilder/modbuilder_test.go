package modbuilder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuiterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

func TestTerminalModulesAttachDaytonaAgentWithoutConfiguredReviver(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "E2E",
		Name:         "lead",
		Backend:      "codex",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey:    "E2E",
		Name:            "nova",
		RoleName:        "lead",
		RuntimeProvider: domain.RuntimeProviderDaytona,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	now := time.Now().UTC()
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    "E2E",
		NodeID:          "placement-1",
		OwnerActor:      "agent:nova",
		RuntimeProvider: domain.RuntimeProviderDaytona,
		Placement: &domain.NodePlacement{
			SandboxID:            "sandbox-1",
			Generation:           1,
			State:                domain.PlacementStateActive,
			LeadProcessStartedAt: &now,
		},
		Labels:     []string{"loom-lead-placement", "loom-workspace=E2E", "loom-agent=nova"},
		DrainState: domain.NodeDrainActive,
		TTL:        time.Hour,
	}); err != nil {
		t.Fatalf("create placement: %v", err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	tabStore := tabmeta.NewStore(rdb, nil)
	termSvc := webuiterminal.NewTerminalService(nil, tabStore, nil, rdb, nil, time.Now().Add(-time.Second))

	mux := http.NewServeMux()
	for _, module := range NewTerminalModules(TerminalModuleDeps{
		TermSvc:      termSvc,
		Store:        st,
		TabMetaStore: tabStore,
	}) {
		module.Register(mux)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/E2E/agents/nova/terminal/session", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "E2E"))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
}
