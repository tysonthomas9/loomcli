package webui

import (
	"path/filepath"
	"regexp"
	"strings"
)

// validGitRef matches safe git ref names: alphanumeric, hyphens, underscores, dots, slashes.
var validGitRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_./-]*$`)

// validTaskID matches task IDs (e.g., "bd-abc123", "loomcli-5y1sd.1").
var validTaskID = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// validSessionID matches session IDs produced by GenerateSessionID:
// alphanumeric, dots, underscores, hyphens.
var validSessionID = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

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

// isDeniedPath checks if a path refers to a sensitive file by extension or filename.
func isDeniedPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if deniedExtensions[ext] {
		return true
	}
	base := strings.ToLower(filepath.Base(path))
	return deniedFilenames[base]
}

// validateDiffPath checks that a file path is safe (no traversal, not absolute, not empty).
func validateDiffPath(p string) bool {
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
