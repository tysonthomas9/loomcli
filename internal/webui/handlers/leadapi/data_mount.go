package leadapi

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/leadtoken"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const maxLeadDataBodyBytes = 1 << 20

// DataRoutes is the complete occupant data allowlist. Every field is a route
// that will be mounted; there is no other route into an issues handler through
// the occupant-authenticated data mount (open-mode general-origin reachability
// is the accepted ticket-27 POC exception). Adding a route to wsMux cannot add
// a field here.
type DataRoutes struct {
	ListIssues       http.HandlerFunc
	CreateIssue      http.HandlerFunc
	GetIssue         http.HandlerFunc
	PatchIssue       http.HandlerFunc
	CloseIssue       http.HandlerFunc
	ClaimIssue       http.HandlerFunc
	AddComment       http.HandlerFunc
	AddDependency    http.HandlerFunc
	RemoveDependency http.HandlerFunc
	Ready            http.HandlerFunc
	Blocked          http.HandlerFunc
	Stats            http.HandlerFunc
}

var dataRoutePatterns = []struct {
	method, suffix string
	mutating       bool
	pick           func(*DataRoutes) http.HandlerFunc
}{
	{http.MethodGet, "/issues", false, func(d *DataRoutes) http.HandlerFunc { return d.ListIssues }},
	{http.MethodPost, "/issues", true, func(d *DataRoutes) http.HandlerFunc { return d.CreateIssue }},
	{http.MethodGet, "/issues/{id}", false, func(d *DataRoutes) http.HandlerFunc { return d.GetIssue }},
	{http.MethodPatch, "/issues/{id}", true, func(d *DataRoutes) http.HandlerFunc { return d.PatchIssue }},
	{http.MethodPost, "/issues/{id}/close", true, func(d *DataRoutes) http.HandlerFunc { return d.CloseIssue }},
	{http.MethodPost, "/issues/{id}/claim", true, func(d *DataRoutes) http.HandlerFunc { return d.ClaimIssue }},
	{http.MethodPost, "/issues/{id}/comments", true, func(d *DataRoutes) http.HandlerFunc { return d.AddComment }},
	{http.MethodPost, "/issues/{id}/dependencies", true, func(d *DataRoutes) http.HandlerFunc { return d.AddDependency }},
	{http.MethodDelete, "/issues/{id}/dependencies/{depId}", true, func(d *DataRoutes) http.HandlerFunc { return d.RemoveDependency }},
	{http.MethodGet, "/ready", false, func(d *DataRoutes) http.HandlerFunc { return d.Ready }},
	{http.MethodGet, "/blocked", false, func(d *DataRoutes) http.HandlerFunc { return d.Blocked }},
	{http.MethodGet, "/stats", false, func(d *DataRoutes) http.HandlerFunc { return d.Stats }},
}

// registerDataRoutes mounts only the explicit issue-data allowlist. Literal
// routes for move, hard delete, reopen, relationship reads, graph, readyz,
// repos, backend config, workflows, agents, and monitor are absent. The literal
// /issues/search route is explicitly denied for every method. Reopen and search
// remain semantically reachable through PATCH status=open and GET /issues?q=;
// move and hard delete have no equivalent here.
//
// Placement state and generation are fenced at request admission. A mutation
// admitted immediately before a placement transition may finish afterward;
// effect-time linearizability requires transactional backend support.
func (m *Module) registerDataRoutes(mux *http.ServeMux) {
	if m.data == nil {
		return
	}
	if m.openAuthMode && !m.allowOpenAuthMode {
		slog.Error("lead data mount disabled in open auth mode; set the POC-only override explicitly")
		return
	}
	base := "/api/workspaces/{ws}/lead/data"
	mux.HandleFunc(base+"/issues/search", m.dataRoute(false, denyDataRoute))
	var getIssueRoute, patchIssueRoute http.HandlerFunc
	for _, route := range dataRoutePatterns {
		handler := m.dataRoute(route.mutating, route.pick(m.data))
		if route.suffix == "/issues/{id}" {
			switch route.method {
			case http.MethodGet:
				getIssueRoute = handler
			case http.MethodPatch:
				patchIssueRoute = handler
			}
			continue
		}
		mux.HandleFunc(route.method+" "+base+route.suffix, handler)
	}
	// ServeMux rejects the method-less literal search deny alongside
	// method-qualified {id} wildcards as incomparable patterns. Dispatching the
	// two {id} methods behind one method-less wildcard makes search strictly more
	// specific while retaining the table's distinct read/mutate wrappers.
	mux.HandleFunc(base+"/issues/{id}", getOrPatchOnly(getIssueRoute, patchIssueRoute))
}

func getOrPatchOnly(get, patch http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			get(w, r)
		case http.MethodPatch:
			patch(w, r)
		default:
			w.Header().Set("Allow", "GET, HEAD, PATCH")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	}
}

func (m *Module) dataRoute(mutating bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := m.authenticateRequest(r.Context(), r)
		if err != nil {
			writeDataStatusError(w, err)
			return
		}
		if err := hasCapOrError("data", leadtoken.CapLeadData, id.claims); err != nil {
			writeDataStatusError(w, err)
			return
		}
		ws := middleware.WorkspaceFromContext(r.Context())
		if ws == "" || ws != id.claims.WorkspaceKey {
			msg := fmt.Sprintf("occupant token workspace %q does not match canonical workspace %q", id.claims.WorkspaceKey, ws)
			writeDataStatusError(w, newStatusError(http.StatusUnauthorized, "identity_mismatch", msg, false))
			return
		}
		if h == nil {
			writeDataError(w, http.StatusServiceUnavailable, "unavailable", "occupant data handler unavailable")
			return
		}
		limiterKey := id.claims.WorkspaceKey + "\x00" + id.claims.PlacementID
		if !m.limiter.allow(limiterKey, mutating) {
			w.Header().Set("Retry-After", "1")
			writeDataError(w, http.StatusTooManyRequests, "rate_limited", "placement request rate exceeded")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxLeadDataBodyBytes)
		actor, err := middleware.OccupantActorFor(leadtoken.OccupantActor(id.claims.PlacementID))
		if err != nil {
			writeDataError(w, http.StatusForbidden, "invalid_principal", "invalid occupant principal")
			return
		}
		ctx := middleware.WithActor(r.Context(), actor)
		h(w, r.WithContext(ctx))
	}
}

func denyDataRoute(w http.ResponseWriter, _ *http.Request) {
	// Deliberately spend the placement lookup and limiter token before this 404:
	// authentication precedes denial per Phase A plan correction 5.
	writeDataError(w, http.StatusNotFound, "not_found", "not found")
}
