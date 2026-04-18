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

	// HTTPClient is an optional override for the HTTP client.
	// If nil, a default client with 30s timeout is created.
	HTTPClient *http.Client
}
