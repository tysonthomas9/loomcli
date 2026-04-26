package fleet

import "net/http"

// Config holds the configuration for connecting to a fleet server.
type Config struct {
	// BaseURL is the fleet server's base URL (e.g., "http://localhost:8080").
	// Required.
	BaseURL string

	// WorkspaceID identifies which workspace to operate on. Required.
	WorkspaceID string

	// AuthToken is a JWT bearer token for authenticated requests.
	AuthToken string //nolint:gosec // field name describes its purpose; value comes from caller config

	// APIKey is sent as X-Fleet-API-Key for registration endpoints.
	APIKey string //nolint:gosec // field name describes its purpose; value comes from caller config

	// Actor is sent as the X-Actor header on every request. fleet-db's
	// --auth-dev-mode treats this header as the authenticated identity when
	// no JWT bearer token is configured; production deployments should
	// prefer AuthToken.
	Actor string

	// HTTPClient is an optional override for the HTTP client.
	// If nil, the process-wide singleton from SharedHTTPClient() is used,
	// which is backed by a tuned *http.Transport (MaxIdleConnsPerHost=128)
	// and a 65s Timeout that decouples from the fleet-db long-poll
	// deadline. See SharedHTTPClient docstring for details.
	HTTPClient *http.Client
}
