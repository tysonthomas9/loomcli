package app

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// TestServer_BuildModules_ZeroValue verifies that calling buildModules on a
// zero-value Server populates wsModules with exactly the 4 always-constructed
// modules (IssueModule, WorkspaceOpsModule, LogModule, SessionModule) and does
// not panic.
func TestServer_BuildModules_ZeroValue(t *testing.T) {
	var app Server
	app.buildModules()

	if got := len(app.wsModules); got != 4 {
		t.Fatalf("len(wsModules) = %d, want 4", got)
	}

	// Verify concrete types in order.
	wantTypes := []string{
		"*handlermux.WorkspaceOpsModule",
		"*issues.IssueModule",
		"*issues.SessionModule",
		"*log.Module",
	}
	for i, mod := range app.wsModules {
		got := fmt.Sprintf("%T", mod)
		if got != wantTypes[i] {
			t.Errorf("wsModules[%d] type = %s, want %s", i, got, wantTypes[i])
		}
	}
}

// TestServer_BuildModules_AllDeps verifies that when every optional dependency
// is non-nil, buildModules produces all 11 modules (4 always + 7 conditional).
func TestServer_BuildModules_AllDeps(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(func() { hub.Stop() })

	app := Server{
		hub:           hub,
		termSvc:       &stubTerminalService{},
		issueTabStore: issuetabs.NewStore(nil, nil),
		fleetRegistry: &fleet.StoreRegistry{},
		diffSvc:       &stubDiffService{},
		fileSvc:       &stubFileService{},
		claimMetrics:  fleet.NewClaimMetrics(),
	}

	app.buildModules()

	// 4 always + SSE(hub) + TerminalTab(termSvc) + IssueTab(issueTabStore) +
	// Terminal(termSvc) + Fleet(fleetRegistry) + Git(diffSvc) + File(fileSvc) = 11
	if got := len(app.wsModules); got != 11 {
		t.Fatalf("len(wsModules) = %d, want 11", got)
	}
}

// TestRegisterWorkspaceRoutes_IteratesModules verifies that registerWorkspaceRoutes
// calls Register on every module in wsModules.
func TestRegisterWorkspaceRoutes_IteratesModules(t *testing.T) {
	var app Server
	app.mux = http.NewServeMux()
	app.multiPool = daemon.NewMultiPool(middleware.WorkspaceFromContext, 1)
	t.Cleanup(func() { _ = app.multiPool.Close() })

	// buildHandlers sets up shared infrastructure (frontend handler, etc.)
	// required by registerRoutes.
	app.buildHandlers()
	t.Cleanup(func() {
		app.handlers.ClientErrLimiter.Stop()
		app.handlers.CSPLimiter.Stop()
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
