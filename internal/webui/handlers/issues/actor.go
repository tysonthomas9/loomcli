package issues

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// defaultAuthor is the attribution used when no mount injected a principal
// (direct handler construction in tests, and any not-yet-migrated caller).
const defaultAuthor = "web-ui"

// actorAttribution resolves the author/creator string for a write. reject
// is true only for an invalid principal (occupant with empty ID) — the
// caller must 403 and must NOT fall back to web-ui attribution.
func actorAttribution(ctx context.Context) (attribution string, reject bool) {
	a, ok := middleware.ActorFromContext(ctx)
	if !ok {
		return defaultAuthor, false
	}
	if err := a.Validate(); err != nil {
		return "", true
	}
	return a.Attribution(), false
}
