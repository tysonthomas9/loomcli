package leadapi

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
)

const (
	dispatchMountBase = "/api/workspaces/{ws}/lead/dispatch"
	// occupantRunSourceKind stamps driver runs created through the occupant
	// dispatch surface. SourceRef adds the verified occupant subject.
	occupantRunSourceKind = domain.DriverRunSourceLeadOccupant
)

// dispatchRoutePatterns is the complete occupant dispatch allowlist. The
// general /workflows/ surface is deliberately unreachable from this mount.
var dispatchRoutePatterns = []struct {
	method, suffix, label string
	mutating              bool
	pick                  func(*Module) occupantHandler
}{
	{http.MethodPost, "/epic-run", "dispatch/epic-run", true,
		func(m *Module) occupantHandler { return m.epicRunDispatch }},
	{http.MethodGet, "/runs/{runId}", "dispatch/run-status", false,
		func(m *Module) occupantHandler { return m.epicRunStatus }},
}

func (m *Module) registerDispatchRoutes(mux *http.ServeMux) {
	if m.issueBackend == nil || m.store == nil {
		return
	}
	for _, route := range dispatchRoutePatterns {
		mux.HandleFunc(route.method+" "+dispatchMountBase+route.suffix,
			m.occupantRoute(occupantRouteSpec{
				label: route.label, capability: leadtoken.CapLeadDispatch,
				mutating: route.mutating, handle: route.pick(m),
			}))
	}
	mux.HandleFunc(dispatchMountBase+"/", m.occupantRoute(occupantRouteSpec{
		label: "dispatch", capability: leadtoken.CapLeadDispatch, mutating: false,
		handle: func(w http.ResponseWriter, r *http.Request, _ occupantIdentity) {
			denyDataRoute(w, r)
		},
	}))
}
