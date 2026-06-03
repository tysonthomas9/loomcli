package app

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

// taskRunWriteAuth builds the token-optional TaskRun auth + fencing middleware
// for the session write routes (PRD Phase C). A request with no bearer token
// falls through to the existing (dev-mode) auth; a token-bearing request is
// validated, bound to {workspace, session}, and fenced against the session's
// current active lease (a stale fencing token → HTTP 409). Once supervisor
// token-minting is the default, flip the final arg to false to require a token.
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
	return fleet.NewTaskRunAuthMiddleware(signingKey, fencing, true) // token-optional
}
