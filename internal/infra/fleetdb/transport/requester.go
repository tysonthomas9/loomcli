// Package transport defines the narrow HTTP execution seam shared by
// capability-specific FleetDB transports.
package transport

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

const FleetDelegatedActorHeader = "X-Fleet-Delegated-Actor"

var (
	ErrInvalidDelegatedActor = errors.New("fleetdb: invalid delegated actor")
	// ErrRevisionConflict preserves the historical Workflow Catalog sentinel
	// identity while allowing other extracted transports to recognize the
	// shared FleetDB revision_conflict classification without importing the
	// root package.
	ErrRevisionConflict = errors.New("fleetdb: workflow catalog revision conflict")
)

// Requester executes authenticated JSON requests using the process-wide
// FleetDB client's credentials, tracing, retry policy, and connection pool.
// Capability packages remain responsible for constructing their exact routes,
// request bodies, and credential headers.
type Requester interface {
	Do(context.Context, string, string, any, any) error
	DoWithHeaders(context.Context, string, string, any, any, map[string]string) error
}

func PathEscape(value string) string {
	return url.PathEscape(value)
}

func WithQuery(path string, query url.Values) string {
	if len(query) == 0 {
		return path
	}
	return path + "?" + query.Encode()
}

// DelegatedActorHeaders validates an audit identity before placing it in the
// FleetDB-only delegated-actor header. It must never be serialized in a
// command body.
func DelegatedActorHeaders(actor string) (map[string]string, error) {
	trimmed := strings.TrimSpace(actor)
	if trimmed == "" || actor != trimmed || len(actor) > 256 {
		return nil, ErrInvalidDelegatedActor
	}
	for _, character := range actor {
		if character < 0x20 || character == 0x7f {
			return nil, ErrInvalidDelegatedActor
		}
	}
	return map[string]string{FleetDelegatedActorHeader: actor}, nil
}
