package fleet

import (
	"context"
	"net/url"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// Archive moves an issue to fleet-db's terminal "tombstone" status via the
// dedicated POST /issues/{id}/archive route.
//
// It is a dedicated route rather than PATCH status=tombstone because fleet-db's
// ValidateSettableStatus rejects tombstone as a settable status — the webui's
// Archive menu item used to PATCH it and 400'd on every click.
//
// fleet-db treats a repeated archive as success (it returns 200 for an
// already-archived issue), so no client-side idempotency handling is needed.
func (b *FleetBackend) Archive(ctx context.Context, id string, params backend.ArchiveParams) error {
	if id == "" {
		return backend.ErrValidation("Archive", "id must not be empty")
	}
	type archiveReq struct {
		Reason string `json:"reason,omitempty"`
	}
	// Release the agent claim first, as Close does: an archived issue is
	// terminal and cannot be re-assigned, so the assignee would otherwise
	// linger on a card nobody can act on. Best-effort — archiving is the
	// primary intent.
	_ = b.releaseClaim(ctx, id, params.Actor)
	return b.execAsActor(ctx, "Archive", "POST", "/issues/"+url.PathEscape(id)+"/archive", archiveReq{Reason: params.Reason}, params.Actor)
}

// Unarchive restores an archived issue via POST /issues/{id}/unarchive.
// fleet-db keeps strict semantics here: unarchiving an issue that was never
// archived is an error, not a no-op.
func (b *FleetBackend) Unarchive(ctx context.Context, id string) error {
	if id == "" {
		return backend.ErrValidation("Unarchive", "id must not be empty")
	}
	return b.execAsActor(ctx, "Unarchive", "POST", "/issues/"+url.PathEscape(id)+"/unarchive", map[string]interface{}{}, "")
}
