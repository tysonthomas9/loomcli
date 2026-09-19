package api

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// Actor-scoped claim/release for the serve-mediated path.
//
// fleet-db arbitrates issue locks BY ACTOR. In local mode each worker talks to
// fleet-db directly and the fleet backend forwards its own identity, so
// arbitration works. Through `loom serve` (LOOM_SERVER_URL — the production
// config) it did not: this client had no actor-scoped claim, so
// driver.claimIssue's capability check fell through to the plain ClaimIssue
// and every sibling worker claimed as serve's single configured actor.
// Observed live on a 3-worker fan-out: all three received grants for the same
// issue within 7ms, a held issue could be re-claimed by a sibling, and
// stopping one duplicate released the lock out from under the worker still
// running on it.
//
// The identity travels in the X-Actor header — the same convention the rest of
// the fleet-db plumbing already uses — so serve can attribute the claim to the
// worker rather than to itself.

// actorHeader is the request header carrying the claiming worker's identity.
const actorHeader = "X-Actor"

// ClaimIssueAsActor claims an issue on behalf of a specific worker. Satisfying
// this method is what makes the driver's claim path stop collapsing siblings
// onto serve's own actor.
func (b *APIBackend) ClaimIssueAsActor(ctx context.Context, id string, lockTTL time.Duration, actor string) error {
	if id == "" {
		return backend.ErrValidation("ClaimIssue", "id must not be empty")
	}
	if actor == "" {
		return backend.ErrValidation("ClaimIssue", "actor must not be empty")
	}
	body, err := claimIssueBody(lockTTL)
	if err != nil {
		return err
	}
	path := "/issues/" + url.PathEscape(id) + "/claim"
	_, err = b.execHeaders(ctx, "ClaimIssue", http.MethodPost, path, body,
		map[string]string{actorHeader: actor})
	return err
}

// ReleaseIssueAsActor releases the lock a specific worker holds. It maps onto
// the same not-implemented answer as ReleaseIssueLock until serve exposes a
// lock-only release route: an honest KindNotImplemented lets the supervisor
// fall back to TTL expiry, whereas a silent success would report that a lock
// was freed when it was not.
func (b *APIBackend) ReleaseIssueAsActor(ctx context.Context, id string, actor string) error {
	return b.ReleaseIssueLock(ctx, id, actor)
}
