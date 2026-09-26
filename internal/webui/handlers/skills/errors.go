package skills

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// writeSkillError renders one skill-handler failure.
//
// Errors this package maps itself are all 4xx — the client asked for something
// it may not have — so they are logged once at Debug. Everything else is handed
// to handler.HandleServiceError, which logs every error it writes at Error
// level; logging here as well would put the same failure in the log twice.
func writeSkillError(w http.ResponseWriter, err error) {
	status, body := mapSkillError(err)
	if body == nil {
		handler.HandleServiceError(w, err)
		return
	}
	slog.Debug("skill handler client error", "status", status, "err", err)
	handler.WriteJSON(w, status, body)
}

// mapSkillError translates a skill failure into the status and body this
// package answers with, or a nil body when it has no mapping for it.
//
//nolint:funlen // The domain-error → HTTP mapping is one exhaustive table.
func mapSkillError(err error) (int, map[string]string) {
	var stale *domain.SkillPreconditionError
	if errors.As(err, &stale) || errors.Is(err, domain.ErrSkillPreconditionFailed) {
		response := map[string]string{
			"code":  "precondition_failed",
			"error": "the skill document changed since it was read",
		}
		if stale != nil && stale.Stored != "" {
			response["revision"] = stale.Stored
		}
		return http.StatusPreconditionFailed, response
	}

	var pathErr *skillRequestPathError
	if errors.As(err, &pathErr) {
		return http.StatusBadRequest, map[string]string{
			"code": "skill_validation_failed", "error": "invalid skill request path", "detail": pathErr.Error(),
		}
	}

	var conflict *domain.SkillProvenanceConflictError
	if errors.As(err, &conflict) || errors.Is(err, domain.ErrSkillProvenanceConflict) {
		response := map[string]string{
			"code":   "skill_provenance_conflict",
			"error":  "the skill is owned by another actor",
			"owner":  "",
			"source": "",
		}
		if conflict != nil {
			response["owner"] = conflict.ExistingCreatedBy
			response["source"] = conflict.ExistingSource
		}
		return http.StatusConflict, response
	}

	if errors.Is(err, domain.ErrSkillForbidden) {
		return http.StatusForbidden, map[string]string{
			"code":   "skill_forbidden",
			"error":  "the backing skill service refused this operation",
			"detail": err.Error(),
		}
	}

	if errors.Is(err, domain.ErrNotFound) {
		return http.StatusNotFound, map[string]string{
			"code":  "skill_not_found",
			"error": "skill or skill file not found",
		}
	}

	if errors.Is(err, domain.ErrInvalid) {
		return skillValidationError(err.Error())
	}
	if errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrConflict) {
		return http.StatusConflict, map[string]string{
			"code":  "skill_conflict",
			"error": "skill operation conflicted with current state",
		}
	}

	var svcErr *service.ServiceError
	if errors.As(err, &svcErr) {
		switch svcErr.Kind {
		case service.KindValidation:
			return skillValidationError(svcErr.Message)
		case service.KindForbidden:
			return http.StatusForbidden, map[string]string{
				"code": "skill_forbidden", "error": svcErr.Message,
			}
		case service.KindNotFound:
			return http.StatusNotFound, map[string]string{
				"code": "skill_not_found", "error": svcErr.Message,
			}
		}
	}

	return 0, nil
}

type skillRequestPathError struct{ err error }

func (e *skillRequestPathError) Error() string { return e.err.Error() }
func (e *skillRequestPathError) Unwrap() error { return e.err }

func invalidSkillRequestPath(err error) error {
	return &skillRequestPathError{err: err}
}

func invalidSkillf(format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, domain.ErrInvalid)...)
}

func skillValidationError(detail string) (int, map[string]string) {
	return http.StatusUnprocessableEntity, map[string]string{
		"code":   "skill_validation_failed",
		"error":  "skill validation failed",
		"detail": detail,
	}
}
