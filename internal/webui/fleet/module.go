package fleet

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// Module registers the 4 workspace-scoped fleet orchestration routes
// on a [*http.ServeMux].
//
// The module is only constructed when fleetRegistry is non-nil.
// FleetAuthMiddleware is conditionally applied to claim, done, and heartbeat
// routes when a signing key is present.
type Module struct {
	fleetStoreFn func(string) (*Store, bool)
	tokenCfg     *TokenConfig // may be nil — no auth middleware
	multiPool    daemon.Pool
	claimMetrics *ClaimMetrics
	fleetRegCfg  *RegisterConfig
}

// NewModule returns a Module. tokenCfg may be nil — fleet auth
// middleware will not be applied.
func NewModule(
	fleetStoreFn func(string) (*Store, bool),
	tokenCfg *TokenConfig,
	multiPool daemon.Pool,
	claimMetrics *ClaimMetrics,
	fleetRegCfg *RegisterConfig,
) *Module {
	return &Module{
		fleetStoreFn: fleetStoreFn,
		tokenCfg:     tokenCfg,
		multiPool:    multiPool,
		claimMetrics: claimMetrics,
		fleetRegCfg:  fleetRegCfg,
	}
}

// Register implements [Module] by registering 4 fleet routes.
// Claim, done, and heartbeat routes are wrapped with FleetAuthMiddleware
// when tokenCfg has a signing key.
func (m *Module) Register(mux *http.ServeMux) {
	// Register — no auth middleware (self-registration)
	mux.HandleFunc("POST /api/workspaces/{ws}/fleet/register",
		FleetWSHandler(m.fleetStoreFn, func(s *Store) http.HandlerFunc {
			return handleFleetRegister(s, m.tokenCfg, m.fleetRegCfg)
		}))

	// Claim — conditional FleetAuthMiddleware
	if m.tokenCfg != nil && len(m.tokenCfg.SigningKey) > 0 {
		fleetAuth := NewFleetAuthMiddleware(m.tokenCfg.SigningKey)
		mux.Handle("POST /api/workspaces/{ws}/fleet/claim",
			fleetAuth(handleFleetClaim(m.multiPool, m.claimMetrics)))
	} else {
		mux.HandleFunc("POST /api/workspaces/{ws}/fleet/claim",
			handleFleetClaim(m.multiPool, m.claimMetrics))
	}

	// Done — conditional FleetAuthMiddleware
	if m.tokenCfg != nil && len(m.tokenCfg.SigningKey) > 0 {
		fleetAuthDone := NewFleetAuthMiddleware(m.tokenCfg.SigningKey)
		mux.Handle("POST /api/workspaces/{ws}/fleet/done/{id}",
			fleetAuthDone(FleetWSHandler(m.fleetStoreFn, handleFleetDone)))
	} else {
		mux.HandleFunc("POST /api/workspaces/{ws}/fleet/done/{id}",
			FleetWSHandler(m.fleetStoreFn, handleFleetDone))
	}

	// Heartbeat — conditional FleetAuthMiddleware
	if m.tokenCfg != nil && len(m.tokenCfg.SigningKey) > 0 {
		fleetAuth := NewFleetAuthMiddleware(m.tokenCfg.SigningKey)
		mux.Handle("POST /api/workspaces/{ws}/fleet/heartbeat",
			fleetAuth(FleetWSHandler(m.fleetStoreFn, handleFleetHeartbeat)))
	} else {
		mux.HandleFunc("POST /api/workspaces/{ws}/fleet/heartbeat",
			FleetWSHandler(m.fleetStoreFn, handleFleetHeartbeat))
	}
}

// FleetWSHandler resolves a per-workspace fleet Store via the provided lookup
// function and delegates to the given handler factory. Returns 503 if the
// workspace is not found in the fleet registry.
func FleetWSHandler(getStore func(string) (*Store, bool), makeHandler func(*Store) http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		store, ok := getStore(wsID)
		if !ok {
			handler.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
				"success": false,
				"error":   "fleet not configured for workspace",
			})
			return
		}
		makeHandler(store).ServeHTTP(w, r)
	}
}
