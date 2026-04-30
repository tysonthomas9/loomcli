package svcimpl

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	// maxRequestBody is the maximum request body size (1MB) to prevent DoS attacks.
	maxRequestBody = 1 << 20
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

// validateAgentName checks that the agent name is non-empty and matches the allowed pattern.
func validateAgentName(name string) error {
	if name == "" {
		return service.ErrValidation("missing agent name")
	}
	if !service.ValidAgentName.MatchString(name) {
		return service.ErrValidation("invalid agent name: must match [a-zA-Z0-9_-]+")
	}
	return nil
}

// classifyStoreError translates a domain.Err* sentinel into the
// equivalent *service.ServiceError so HTTP handlers can map it to the
// right status code. op is a short verb describing the operation
// ("create agent", "delete role", etc.) used for logs and the wrapped
// error chain.
func classifyStoreError(op string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return service.ErrNotFound(op + ": " + err.Error())
	case errors.Is(err, domain.ErrAlreadyExists):
		return service.ErrConflict(op + ": " + err.Error())
	case errors.Is(err, domain.ErrInvalid):
		return service.ErrValidation(op + ": " + err.Error())
	case errors.Is(err, domain.ErrConflict):
		return service.ErrConflict(op + ": " + err.Error())
	default:
		return service.ErrInternal(op, err)
	}
}
