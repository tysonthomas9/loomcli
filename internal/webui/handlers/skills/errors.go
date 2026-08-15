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

//nolint:funlen // The domain-error → HTTP mapping is one exhaustive table.
func writeSkillError(w http.ResponseWriter, err error) {
	if isExpectedSkillClientError(err) {
		slog.Debug("skill handler client error", "err", err)
	} else {
		slog.Error("skill handler error", "err", err)
	}

	var stale *domain.SkillPreconditionError
	if errors.As(err, &stale) || errors.Is(err, domain.ErrSkillPreconditionFailed) {
		response := map[string]string{
			"code":  "precondition_failed",
			"error": "the skill document changed since it was read",
		}
		if stale != nil && stale.Stored != "" {
			response["revision"] = stale.Stored
		}
		handler.WriteJSON(w, http.StatusPreconditionFailed, response)
		return
	}

	var pathErr *skillRequestPathError
	if errors.As(err, &pathErr) {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"code": "skill_validation_failed", "error": "invalid skill request path", "detail": pathErr.Error(),
		})
		return
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
		handler.WriteJSON(w, http.StatusConflict, response)
		return
	}

	if errors.Is(err, domain.ErrSkillForbidden) {
		handler.WriteJSON(w, http.StatusForbidden, map[string]string{
			"code":   "skill_forbidden",
			"error":  "the backing skill service refused this operation",
			"detail": err.Error(),
		})
		return
	}

	if errors.Is(err, domain.ErrNotFound) {
		handler.WriteJSON(w, http.StatusNotFound, map[string]string{
			"code":  "skill_not_found",
			"error": "skill or skill file not found",
		})
		return
	}

	if errors.Is(err, domain.ErrInvalid) {
		writeSkillValidationError(w, err.Error())
		return
	}
	if errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrConflict) {
		handler.WriteJSON(w, http.StatusConflict, map[string]string{
			"code":  "skill_conflict",
			"error": "skill operation conflicted with current state",
		})
		return
	}

	var svcErr *service.ServiceError
	if errors.As(err, &svcErr) {
		switch svcErr.Kind {
		case service.KindValidation:
			writeSkillValidationError(w, svcErr.Message)
			return
		case service.KindForbidden:
			handler.WriteJSON(w, http.StatusForbidden, map[string]string{
				"code": "skill_forbidden", "error": svcErr.Message,
			})
			return
		case service.KindNotFound:
			handler.WriteJSON(w, http.StatusNotFound, map[string]string{
				"code": "skill_not_found", "error": svcErr.Message,
			})
			return
		}
	}

	handler.HandleServiceError(w, err)
}

type skillRequestPathError struct{ err error }

func (e *skillRequestPathError) Error() string { return e.err.Error() }
func (e *skillRequestPathError) Unwrap() error { return e.err }

func invalidSkillRequestPath(err error) error {
	return &skillRequestPathError{err: err}
}

func isExpectedSkillClientError(err error) bool {
	if errors.Is(err, domain.ErrSkillPreconditionFailed) ||
		errors.Is(err, domain.ErrSkillProvenanceConflict) ||
		errors.Is(err, domain.ErrSkillForbidden) ||
		errors.Is(err, domain.ErrNotFound) ||
		errors.Is(err, domain.ErrInvalid) ||
		errors.Is(err, domain.ErrAlreadyExists) ||
		errors.Is(err, domain.ErrConflict) {
		return true
	}
	var svcErr *service.ServiceError
	return errors.As(err, &svcErr) && svcErr.Kind != service.KindInternal
}

func invalidSkillf(format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, domain.ErrInvalid)...)
}

func writeSkillValidationError(w http.ResponseWriter, detail string) {
	handler.WriteJSON(w, http.StatusUnprocessableEntity, map[string]string{
		"code":   "skill_validation_failed",
		"error":  "skill validation failed",
		"detail": detail,
	})
}
