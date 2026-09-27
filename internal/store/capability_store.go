package store

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// CapabilityStore reads the server's self-reported route inventory.
//
// It is the one sub-store that describes the *server* rather than a workspace
// entity, which is why it takes no workspace key. Boot-time preflight uses it
// to decide whether the deployed fleet-db serves what this loom build needs.
//
// Implementations must return an error wrapping
// domain.ErrCapabilityEndpointUnsupported when the capability route itself is
// absent, so callers can tell "this server predates capability reporting"
// apart from "this server is missing a route loom needs".
type CapabilityStore interface {
	Get(ctx context.Context) (domain.FleetDBCapabilities, error)
}
