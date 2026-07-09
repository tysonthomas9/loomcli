package fileaccess

import (
	"context"
	"path/filepath"
	"strings"
)

// Capabilities are the effective permissions for one workspace file request.
type Capabilities struct {
	Read      bool `json:"read"`
	Write     bool `json:"write"`
	Sensitive bool `json:"sensitive"`
}

type capabilitiesContextKey struct{}

// WithCapabilities records the request's already-authorized file capabilities.
func WithCapabilities(ctx context.Context, capabilities Capabilities) context.Context {
	return context.WithValue(ctx, capabilitiesContextKey{}, capabilities)
}

// FromContext returns the capabilities installed by file-route middleware.
func FromContext(ctx context.Context) (Capabilities, bool) {
	capabilities, ok := ctx.Value(capabilitiesContextKey{}).(Capabilities)
	return capabilities, ok
}

// AllowsSensitivePath reports whether a path may be exposed to this request.
// Service calls made outside HTTP request middleware retain their historical behavior.
func AllowsSensitivePath(ctx context.Context, path string) bool {
	capabilities, ok := FromContext(ctx)
	return !ok || capabilities.Sensitive || !IsSensitivePath(path)
}

// IsSensitivePath classifies credential-like files independently of scope.
// Matching is case-insensitive so policy does not vary by filesystem behavior.
func IsSensitivePath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	for _, segment := range strings.Split(clean, "/") {
		if isSensitiveFileName(segment) {
			return true
		}
	}
	return false
}

func isSensitiveFileName(name string) bool {
	base := strings.ToLower(name)
	if base == ".netrc" || strings.HasPrefix(base, ".env") {
		return true
	}
	if isSSHPrivateKeyName(base) {
		return true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".key", ".pem", ".p12", ".pfx", ".crt", ".cer", ".der", ".jks":
		return true
	default:
		return false
	}
}

func isSSHPrivateKeyName(base string) bool {
	switch base {
	case "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "identity":
		return true
	default:
		return strings.HasPrefix(base, "id_rsa_") ||
			strings.HasPrefix(base, "id_dsa_") ||
			strings.HasPrefix(base, "id_ecdsa_") ||
			strings.HasPrefix(base, "id_ed25519_")
	}
}
