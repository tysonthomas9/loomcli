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

// ReleaseIssueAsActor releases the lock a specific worker holds, via
// POST /issues/{id}/release with the X-Actor header. Serve scopes the release
// to that actor, so releasing a lock held by a different worker comes back as
// KindConflict rather than silently un-claiming the sibling still running on
// it.
//
// This used to map onto ReleaseIssueLock's KindNotImplemented because serve
// exposed no release route. That was honest, but it made every release through
// LOOM_SERVER_URL a no-op: the supervisor took the not-implemented answer as
// "fall back to TTL expiry" and the claim sat at in_progress until the lock
// aged out.
//
// A serve older than the route answers 404 with a plain-text body, because an
// unregistered pattern is answered by net/http's mux and never reaches a
// handler. That arrives here as an envelope parse failure, and it is reported
// as KindNotImplemented: "this server cannot release" is what the caller needs
// to know, and it must not be mistaken for a transient failure. The
// distinction is load-bearing — a caller degrades to the unscoped status
// transition on not-implemented, but must leave the task claimed on a
// transient error, because an unscoped release can free a lock a live sibling
// still holds.
//
// A 404 that does carry the API envelope came from the release handler itself
// and means the issue is missing, so it stays KindNotFound.
func (b *APIBackend) ReleaseIssueAsActor(ctx context.Context, id string, actor string) error {
	if id == "" {
		return backend.ErrValidation("ReleaseIssue", "id must not be empty")
	}
	if actor == "" {
		return backend.ErrValidation("ReleaseIssue", "actor must not be empty")
	}

	path := "/issues/" + url.PathEscape(id) + "/release"
	resp, statusCode, err := b.doRequestHeaders(ctx, http.MethodPost, path, nil,
		map[string]string{actorHeader: actor})
	if err != nil {
		if statusCode == http.StatusNotFound {
			return backend.ErrNotImplemented("ReleaseIssue",
				"server exposes no issue release route; upgrade loom serve or rely on TTL expiry")
		}
		return classifyTransportError("ReleaseIssue", err)
	}
	return classifyHTTPError("ReleaseIssue", statusCode, *resp)
}
