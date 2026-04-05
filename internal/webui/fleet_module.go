package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

// FleetModule registers the 4 workspace-scoped fleet orchestration routes
// on a [*http.ServeMux].
//
// The module is only constructed when fleetRegistry is non-nil.
// FleetAuthMiddleware is conditionally applied to claim, done, and heartbeat
// routes when a signing key is present.
type FleetModule struct {
	fleetStoreFn func(string) (*fleet.Store, bool)
	tokenCfg     *TokenConfig // may be nil — no auth middleware
	multiPool    daemon.Pool
	claimMetrics *fleet.ClaimMetrics
	fleetRegCfg  *FleetRegisterConfig
}

// NewFleetModule returns a FleetModule. tokenCfg may be nil — fleet auth
// middleware will not be applied.
func NewFleetModule(
	fleetStoreFn func(string) (*fleet.Store, bool),
	tokenCfg *TokenConfig,
	multiPool daemon.Pool,
	claimMetrics *fleet.ClaimMetrics,
	fleetRegCfg *FleetRegisterConfig,
) *FleetModule {
	return &FleetModule{
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
func (m *FleetModule) Register(mux *http.ServeMux) {
	// Register — no auth middleware (self-registration)
	mux.HandleFunc("POST /api/workspaces/{ws}/fleet/register",
		fleetWSHandler(m.fleetStoreFn, func(s *fleet.Store) http.HandlerFunc {
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
			fleetAuthDone(fleetWSHandler(m.fleetStoreFn, handleFleetDone)))
	} else {
		mux.HandleFunc("POST /api/workspaces/{ws}/fleet/done/{id}",
			fleetWSHandler(m.fleetStoreFn, handleFleetDone))
	}

	// Heartbeat — conditional FleetAuthMiddleware
	if m.tokenCfg != nil && len(m.tokenCfg.SigningKey) > 0 {
		fleetAuth := NewFleetAuthMiddleware(m.tokenCfg.SigningKey)
		mux.Handle("POST /api/workspaces/{ws}/fleet/heartbeat",
			fleetAuth(fleetWSHandler(m.fleetStoreFn, handleFleetHeartbeat)))
	} else {
		mux.HandleFunc("POST /api/workspaces/{ws}/fleet/heartbeat",
			fleetWSHandler(m.fleetStoreFn, handleFleetHeartbeat))
	}
}
