package app

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

// taskRunWriteAuth builds the TaskRun auth + fencing middleware for the session
// write routes (PRD Phase C). A token-bearing request is validated, bound to
// {workspace, session}, and fenced against the session's current active lease (a
// stale fencing token → HTTP 409).
//
// The mode is tied to whether a signing key is configured: when a key is present
// the supervisor mints + injects tokens, so the routes REQUIRE a token
// (fail-closed — a tokenless writer is rejected, not passed through). When no key
// is configured (keyless dev-mode, no minting), the middleware is token-optional
// and a tokenless request falls through to the existing dev-mode (X-Actor) auth.
func taskRunWriteAuth(st store.Store, signingKey []byte) func(http.Handler) http.Handler {
	fencing := func(ctx context.Context, ws, sessionID string) (int64, bool, error) {
		leases, err := st.AgentLeases().List(ctx, ws, store.AgentLeaseFilter{
			SessionID: sessionID,
			Status:    domain.AgentLeaseActive,
		})
		if err != nil {
			return 0, false, err
		}
		current, found := int64(0), false
		for _, l := range leases {
			if !found || l.FencingToken > current {
				current, found = l.FencingToken, true
			}
		}
		return current, found, nil
	}
	// validate against the configured signing key (nil when none → keyless
	// dev-mode). Swap for a SigningKeyManager.ValidateTaskRunTokenFromStore here
	// to tolerate key rotation once the app holds the key manager.
	var validate fleet.ValidateFunc
	if len(signingKey) > 0 {
		validate = func(token string) (*fleet.TaskRunClaims, error) {
			return fleet.ValidateTaskRunToken(token, signingKey)
		}
	}
	// token-optional only in keyless dev-mode; fail-closed when a key is configured.
	return fleet.NewTaskRunAuthMiddleware(validate, fencing, len(signingKey) == 0)
}
