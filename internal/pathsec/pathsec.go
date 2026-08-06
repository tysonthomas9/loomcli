// Package pathsec owns shared file path security validators.
package pathsec

import (
	"path/filepath"
	"strings"
)

// deniedExtensions lists file extensions that must not be read or written.
var deniedExtensions = map[string]bool{
	".key": true, ".pem": true, ".p12": true, ".pfx": true,
	".env": true, ".gpg": true, ".asc": true,
}

// deniedFilenames lists filenames (without path) that must not be read or written.
var deniedFilenames = map[string]bool{
	"id_rsa": true, "id_ed25519": true, "id_ecdsa": true, "id_dsa": true,
	".env": true, ".env.local": true, ".env.production": true, ".netrc": true,
}

// IsDeniedPath reports whether path refers to a sensitive file by extension or
// base filename (case-insensitive).
func IsDeniedPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if deniedExtensions[ext] {
		return true
	}
	base := strings.ToLower(filepath.Base(path))
	return deniedFilenames[base]
}

// ValidateDiffPath reports whether p is a safe relative path: non-empty, not
// absolute, and with no "." / ".." traversal after cleaning.
func ValidateDiffPath(p string) bool {
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "/") {
		return false
	}
	cleaned := filepath.Clean(p)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return false
	}
	return true
}
