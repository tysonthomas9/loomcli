package driverapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

// authenticate is the single credential gate for every driver-op route (the
// generic {op} POST, the explicitly registered two-segment ops, and the watch
// SSE GET). A signed run-scoped bearer is the only accepted credential. The
// returned identity is built from its claims; no static bearer or request
// header can mint owner authority.
//
// Revocation needs no denylist: callers pass the claims-derived identity
// through verifyParent (fenced heartbeat), which rejects terminal runs and
// superseded leases regardless of token expiry.
//
// On failure the structured error has been written and ok is false.
func (m *Module) authenticate(w http.ResponseWriter, r *http.Request) (tokenID *driverIdentity, ok bool) {
	bearer := bearerCredential(r)
	if bearer == "" || len(m.runTokenKey) == 0 {
		writeOpError(w, http.StatusUnauthorized, "unauthenticated", "valid run token required", false)
		return nil, false
	}
	claims, err := driverpkg.ParseRunToken(bearer, m.runTokenKey)
	if err == nil {
		return m.identityFromRunToken(w, r, claims)
	}
	if driverpkg.IsRunTokenExpired(err) {
		writeOpError(w, http.StatusUnauthorized, "token_expired", "run token expired (max run duration reached)", false)
		return nil, false
	}
	writeOpError(w, http.StatusUnauthorized, "unauthenticated", "missing or invalid run token", false)
	return nil, false
}

// identityFromRunToken builds the request identity from validated run-token
// claims, rejecting requests whose path workspace disagrees with the token or
// which carry any retired caller-supplied Driver identity header.
func (m *Module) identityFromRunToken(w http.ResponseWriter, r *http.Request, claims *driverpkg.RunTokenClaims) (*driverIdentity, bool) {
	if ws := r.PathValue("ws"); claims.WorkspaceKey != "" && ws != "" && claims.WorkspaceKey != ws {
		writeOpError(w, http.StatusUnauthorized, "identity_mismatch",
			fmt.Sprintf("run token is scoped to workspace %q, not %q", claims.WorkspaceKey, ws), false)
		return nil, false
	}
	for headerName := range r.Header {
		if strings.HasPrefix(strings.ToLower(headerName), "x-loom-driver-") {
			writeOpError(w, http.StatusUnauthorized, "identity_mismatch",
				"caller-supplied Driver identity headers are not accepted", false)
			return nil, false
		}
	}
	id := driverIdentity{
		RunID:   claims.RunID,
		NodeID:  claims.NodeID,
		LeaseID: claims.LeaseID,
	}
	leaseToken, err := driverpkg.DeriveDriverRunLeaseToken(m.runTokenKey, claims.WorkspaceKey, claims.RunID, claims.NodeID, claims.LeaseID)
	if err != nil {
		writeOpError(w, http.StatusUnauthorized, "identity_mismatch", "run token owner identity is incomplete", false)
		return nil, false
	}
	id.LeaseToken = leaseToken
	if claims.FencingToken > 0 {
		id.fence = strconv.FormatInt(claims.FencingToken, 10)
	}
	return &id, true
}

// requestIdentity returns the claims-derived parent DriverRun identity.
func requestIdentity(w http.ResponseWriter, _ *http.Request, tokenID *driverIdentity) (driverIdentity, bool) {
	if tokenID != nil {
		return *tokenID, true
	}
	writeOpError(w, http.StatusUnauthorized, "unauthenticated", "valid run token required", false)
	return driverIdentity{}, false
}

// bearerCredential extracts the Bearer credential, "" when absent.
func bearerCredential(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(token)
}
