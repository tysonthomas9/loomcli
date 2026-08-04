// Package authmode defines the two authentication modes a loom server advertises
// — "open" (none) and "oidc" — plus validation of the wire string. Shared so that
// internal/httpclient (client side: decides whether to attach a bearer token and
// re-auth on 401) and the server's GET /api/config handler
// (internal/webui/handlers/misc) agree on one spelling.
package authmode

// ModeOpen means the server requires no authentication.
const ModeOpen = "open"

// ModeOIDC means the server requires OIDC-based authentication.
const ModeOIDC = "oidc"

// Valid reports whether mode is a recognized auth mode value.
func Valid(mode string) bool {
	return mode == ModeOpen || mode == ModeOIDC
}
