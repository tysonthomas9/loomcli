package app

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/localredis"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// TestServer_BuildModules_ZeroValue verifies that calling buildModules on a
// zero-value Server populates wsModules with the 4 always-constructed modules
// plus the non-store pull-request-list fallback, and does not panic.
func TestServer_BuildModules_ZeroValue(t *testing.T) {
	var app Server
	app.buildModules()

	if got := len(app.wsModules); got != 5 {
		t.Fatalf("len(wsModules) = %d, want 5", got)
	}

	// Verify concrete types in order. The gh-backed PR list fallback is
	// registered whenever there is no store (its Register no-ops without an
	// agent service).
	wantTypes := []string{
		"*app.WorkspaceOpsModule",
		"*issues.IssueModule",
		"*issues.SessionModule",
		"*app.LogModule",
		"*git.PullRequestListModule",
	}
	for i, mod := range app.wsModules {
		got := fmt.Sprintf("%T", mod)
		if got != wantTypes[i] {
			t.Errorf("wsModules[%d] type = %s, want %s", i, got, wantTypes[i])
		}
	}
}

// TestServer_BuildModules_AllDeps verifies that when every optional dependency
// is non-nil without a store, buildModules produces the non-store module set.
func TestServer_BuildModules_AllDeps(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(func() { hub.Stop() })

	app := Server{
		hub:           hub,
		termSvc:       &stubTerminalService{},
		issueTabStore: localredis.NewIssueTabStore(nil, nil),
		fleetRegistry: &fleet.StoreRegistry{},
		diffSvc:       &stubDiffService{},
		fileSvc:       &stubFileService{},
		claimMetrics:  fleet.NewClaimMetrics(),
	}

	app.buildModules()

	// 4 always + SSE(hub) + TerminalTab(termSvc) + IssueTab(issueTabStore) +
	// Terminal(termSvc) + Fleet(fleetRegistry) + Git(diffSvc) + File(fileSvc) +
	// gh-backed PR list fallback (non-store) = 12
	if got := len(app.wsModules); got != 12 {
		t.Fatalf("len(wsModules) = %d, want 12", got)
	}
}

func TestServer_BuildModules_StoreBacked(t *testing.T) {
	app := Server{}
	app.config.Store = memstore.New()

	app.buildModules()

	if got := len(app.wsModules); got != 18 {
		t.Fatalf("len(wsModules) = %d, want 18", got)
	}
	wantTypes := []string{
		"*app.WorkspaceOpsModule",
		"*issues.IssueModule",
		"*issues.SessionModule",
		"*app.LogModule",
		"*agents.Module",
		"*agentsmanagement.Module",
		"*interactionmanagement.Module",
		"*onboarding.Module",
		"*workflows.Module",
		"*executionmanagement.Module",
		"*webhooks.Module",
		"*roles.Module",
		"*triggerbindings.Module",
		"*connectors.Module",
		"*approvals.Module",
		"*taskrunapi.Module",
		"*driverapi.Module",
		"*prreview.Module",
	}
	for i, mod := range app.wsModules {
		got := fmt.Sprintf("%T", mod)
		if got != wantTypes[i] {
			t.Errorf("wsModules[%d] type = %s, want %s", i, got, wantTypes[i])
		}
	}
}

// TestRegisterWorkspaceRoutes_IteratesModules verifies that registerWorkspaceRoutes
// calls Register on every module in wsModules.
func TestRegisterWorkspaceRoutes_IteratesModules(t *testing.T) {
	var app Server
	app.mux = http.NewServeMux()
	// buildHandlers sets up shared infrastructure (frontend handler, etc.)
	// required by registerRoutes.
	app.buildHandlers()
	t.Cleanup(func() {
		app.handlers.ClientErrLimiter.Stop()
		app.handlers.AuthCfgLimiter.Stop()
	})

	mock := &recordingModule{}
	app.wsModules = []wsModule{mock}

	// wsExistsFn is required by registerWorkspaceRoutes (used in middleware).
	app.wsExistsFn = func(string) bool { return true }

	app.registerRoutes()

	if !mock.called {
		t.Fatal("expected Register to be called on mock module, but it was not")
	}
}

// --- helpers ---

// recordingModule implements Module and records whether Register was called.
type recordingModule struct {
	called bool
}

func (m *recordingModule) Register(_ *http.ServeMux) {
	m.called = true
}
