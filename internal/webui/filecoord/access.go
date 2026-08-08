package filecoord

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/webui/fileaccess"
)

// FileCapabilities are the effective permissions for one workspace file request.
type FileCapabilities = fileaccess.Capabilities

// WithFileCapabilities records the request's already-authorized file capabilities.
func WithFileCapabilities(ctx context.Context, capabilities FileCapabilities) context.Context {
	return fileaccess.WithCapabilities(ctx, capabilities)
}

// FileCapabilitiesFromContext returns the capabilities installed by file-route middleware.
func FileCapabilitiesFromContext(ctx context.Context) (FileCapabilities, bool) {
	return fileaccess.FromContext(ctx)
}

// FilePathAllowsSensitive reports whether a path may be exposed to this request.
// Service calls made outside HTTP request middleware retain their historical behavior.
func FilePathAllowsSensitive(ctx context.Context, path string) bool {
	return fileaccess.AllowsSensitivePath(ctx, path)
}

// IsSensitiveFilePath classifies credential-like files independently of scope.
// Matching is case-insensitive so policy does not vary by filesystem behavior.
func IsSensitiveFilePath(path string) bool {
	return fileaccess.IsSensitivePath(path)
}
