package authority

const (
	TrustModeOpen = "open"
	TrustModeOIDC = "oidc"
)

// ValidTrustMode reports whether mode names a supported deployment trust
// boundary.
func ValidTrustMode(mode string) bool {
	return mode == TrustModeOpen || mode == TrustModeOIDC
}
