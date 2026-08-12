package httpapi

import "net/http"

// Config holds the configuration for connecting to a loom server via HTTP REST.
type Config struct {
	// BaseURL is the server's base URL (e.g., "http://localhost:8080").
	// Required. Trailing slashes are stripped.
	BaseURL string

	// WorkspaceID identifies which workspace to operate on. Required.
	WorkspaceID string

	// HTTPClient is an optional override for the HTTP client used for
	// requests. If nil, a default client with a 30-second timeout is used.
	// Inject an http.Client with a custom Transport (e.g., AuthTransport)
	// to layer in authentication behavior.
	HTTPClient *http.Client
}
