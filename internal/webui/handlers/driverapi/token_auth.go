package driverapi

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

// authenticate is the single credential gate for every driver-op route (the
// generic {op} POST, the explicitly registered two-segment ops, and the watch
// SSE GET). It resolves the request's auth in priority order:
//
//  1. Run-scoped token: when the Bearer credential parses as a run token
//     signed with the module's key, the request is authenticated by the token
//     ALONE — no shared API token, no header quad. The returned identity is
//     built from the token claims; a conflicting X-Loom-Driver-Run-Id header
//     or workspace path is a 401 identity_mismatch. An expired run token is a
//     hard 401 token_expired (jwt/v5 verifies the signature before expiry, so
//     the failure proves the token was ours).
//  2. Legacy path: any other credential (including none) falls through to the
//     optional shared static API token compare, exactly as before run tokens
//     existed, so the CLI/ops header-quad transport keeps working during the
//     transition. tokenID is nil: the caller resolves identity from headers.
//
// Revocation needs no denylist: callers pass the claims-derived identity
// through verifyParent (fenced heartbeat), which rejects terminal runs and
// superseded leases regardless of token expiry.
//
// On failure the structured error has been written and ok is false.
func (m *Module) authenticate(w http.ResponseWriter, r *http.Request) (tokenID *driverIdentity, ok bool) {
	bearer := bearerCredential(r)
	if bearer != "" && len(m.runTokenKey) > 0 {
		claims, err := driverpkg.ParseRunToken(bearer, m.runTokenKey)
		switch {
		case err == nil:
			return m.identityFromRunToken(w, r, claims)
		case driverpkg.IsRunTokenExpired(err):
			writeOpError(w, http.StatusUnauthorized, "token_expired", "run token expired (max run duration reached)", false)
			return nil, false
		}
		// Not parseable as our run token: fall through to the legacy
		// static-token path unchanged.
	}
	if m.apiToken != "" && subtle.ConstantTimeCompare([]byte(bearer), []byte(m.apiToken)) != 1 {
		writeOpError(w, http.StatusUnauthorized, "unauthenticated", "missing or invalid driver API token", false)
		return nil, false
	}
	return nil, true
}

// identityFromRunToken builds the request identity from validated run-token
// claims, rejecting requests whose path workspace or driver-run header
// disagrees with the token.
func (m *Module) identityFromRunToken(w http.ResponseWriter, r *http.Request, claims *driverpkg.RunTokenClaims) (*driverIdentity, bool) {
	if ws := r.PathValue("ws"); claims.WorkspaceKey != "" && ws != "" && claims.WorkspaceKey != ws {
		writeOpError(w, http.StatusUnauthorized, "identity_mismatch",
			fmt.Sprintf("run token is scoped to workspace %q, not %q", claims.WorkspaceKey, ws), false)
		return nil, false
	}
	if headerRunID := strings.TrimSpace(r.Header.Get(HeaderDriverRunID)); headerRunID != "" && headerRunID != claims.RunID {
		writeOpError(w, http.StatusUnauthorized, "identity_mismatch",
			fmt.Sprintf("%s header %q does not match run token run %q", HeaderDriverRunID, headerRunID, claims.RunID), false)
		return nil, false
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

// requestIdentity picks the per-request parent DriverRun identity: run-token
// claims when the request authenticated with a token, the legacy header quad
// otherwise. On failure the error has been written and ok is false.
func requestIdentity(w http.ResponseWriter, r *http.Request, tokenID *driverIdentity) (driverIdentity, bool) {
	if tokenID != nil {
		return *tokenID, true
	}
	id := driverIdentityFromHeaders(r)
	if id.RunID == "" {
		writeOpError(w, http.StatusUnauthorized, "unauthenticated", HeaderDriverRunID+" header required", false)
		return driverIdentity{}, false
	}
	if id.LeaseToken == "" {
		writeOpError(w, http.StatusUnauthorized, "unauthenticated", HeaderDriverLeaseToken+" header required", false)
		return driverIdentity{}, false
	}
	return id, true
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
