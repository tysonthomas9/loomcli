package authmode

// ModeOpen means the server requires no authentication.
const ModeOpen = "open"

// ModeOIDC means the server requires OIDC-based authentication.
const ModeOIDC = "oidc"

// Valid reports whether mode is a recognized auth mode value.
func Valid(mode string) bool {
	return mode == ModeOpen || mode == ModeOIDC
}
