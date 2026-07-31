package svcimpl

import (
	"errors"
	"regexp"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	// maxRequestBody is the maximum request body size (1MB) to prevent DoS attacks.
	maxRequestBody = 1 << 20
)

// validGitRef matches safe git ref names: alphanumeric, hyphens, underscores, dots, slashes.
var validGitRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_./-]*$`)

// validTaskID matches task IDs (e.g., "loomcli-5y1sd.1").
var validTaskID = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// validSessionID matches session IDs produced by GenerateSessionID:
// alphanumeric, dots, underscores, hyphens.
var validSessionID = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// validateAgentName checks that the agent name is non-empty and matches the allowed pattern.
func validateAgentName(name string) error {
	if name == "" {
		return service.ErrValidation("missing agent name")
	}
	if !service.IsValidAgentName(name) {
		return service.ErrValidation("invalid agent name")
	}
	return nil
}

// normalizeStoredAgentName lowercases and trims agent names before persistence.
func normalizeStoredAgentName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// validateStoredAgentName checks a normalized agent name against fleet-db rules.
func validateStoredAgentName(name string) error {
	if name == "" {
		return service.ErrValidation("missing agent name")
	}
	if !service.ValidStoredAgentName.MatchString(name) {
		return service.ErrValidation("invalid agent name: use 1-100 lowercase letters, numbers, hyphens, dots, or underscores (cannot start or end with punctuation)")
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
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, agents.ErrNotFound):
		return service.ErrNotFound(op + ": " + err.Error())
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, agents.ErrAlreadyExists):
		return service.ErrConflict(op + ": " + err.Error())
	case errors.Is(err, domain.ErrInvalid), errors.Is(err, agents.ErrInvalid):
		return service.ErrValidation(op + ": " + err.Error())
	case errors.Is(err, domain.ErrConflict), errors.Is(err, agents.ErrConflict),
		errors.Is(err, agents.ErrInvalidTransition):
		return service.ErrConflict(op + ": " + err.Error())
	case errors.Is(err, agents.ErrUnavailable):
		return service.ErrUnavailable(op + ": " + err.Error())
	default:
		return service.ErrInternal(op, err)
	}
}
