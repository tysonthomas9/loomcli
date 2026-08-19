package fleet

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// classifyHTTPError maps an HTTP status code and response body to a
// *backend.BackendError. For 2xx responses with success=false, the error
// string in the body is matched against known patterns. Every non-nil result
// carries the server's machine-readable code in Meta — see attachCode.
func classifyHTTPError(op string, statusCode int, body apiResponse) error {
	return attachCode(classifyHTTPErrorKind(op, statusCode, body), body.Code)
}

// attachCode records the server's machine-readable error code on the
// BackendError so callers can distinguish semantics that collapse into one
// Kind (e.g. "not_claimable" vs "already_claimed", both KindConflict).
func attachCode(err error, code string) error {
	if err == nil || code == "" {
		return err
	}
	var be *backend.BackendError
	if !errors.As(err, &be) {
		return err
	}
	if be.Meta == nil {
		be.Meta = make(map[string]string, 1)
	}
	be.Meta[backend.MetaErrorCode] = code
	return err
}

// classifyHTTPErrorKind carries the Kind-mapping logic. It is kept separate
// from classifyHTTPError so the many return paths below all flow through the
// single attachCode seam.
func classifyHTTPErrorKind(op string, statusCode int, body apiResponse) error {
	// 2xx with success=true: no error.
	if statusCode >= 200 && statusCode < 300 && body.Success {
		return nil
	}

	msg := body.Error
	if msg == "" {
		msg = "unknown error"
	}
	if classified := classifyErrorCode(op, body.Code, msg); classified != nil {
		return attachMeta(classified, body.Meta)
	}

	// 2xx with success=false: classify from error string.
	if statusCode >= 200 && statusCode < 300 {
		return attachMeta(classifyErrorString(op, msg), body.Meta)
	}

	// Non-2xx: fleet-db uses a mix of status codes for semantic errors
	// (e.g. 422 for "invalid_transition: issue is already closed", 500
	// for generic internal errors, 404 for missing resources). Status
	// alone can't distinguish "transient server error" from "client
	// precondition failed", so run the string matcher first and promote
	// its verdict when it's non-default. Genuine internal errors fall
	// through to the status-based switch below.
	if classified := classifyErrorString(op, msg); !isInternalBucket(classified) &&
		!(statusCode == 422 && backend.IsKind(classified, backend.KindNotFound)) {
		return attachMeta(classified, body.Meta)
	}

	switch statusCode {
	case 400, 422:
		return attachMeta(backend.ErrValidation(op, msg), body.Meta)
	case 401, 403:
		return attachMeta(backend.ErrUnavailable(op, "authentication failed: "+msg, nil), body.Meta)
	case 404:
		return attachMeta(backend.ErrNotFound(op, msg), body.Meta)
	case 409:
		return attachMeta(backend.ErrConflict(op, msg), body.Meta)
	case 429:
		return attachMeta(backend.ErrUnavailable(op, "rate limited: "+msg, nil), body.Meta)
	case 503:
		return attachMeta(backend.ErrUnavailable(op, msg, nil), body.Meta)
	case 504:
		return attachMeta(backend.ErrTimeout(op, msg, nil), body.Meta)
	default:
		if statusCode >= 400 {
			return attachMeta(backend.ErrInternal(op, msg, nil), body.Meta)
		}
		return nil
	}
}

func classifyErrorCode(op, code, msg string) error {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "validation_failed", "invalid_json", "invalid_parameter", "missing_content_type",
		"unsupported_media_type", "body_too_large", "batch_too_large":
		return backend.ErrValidation(op, msg)
	case "not_claimable", "conflict", "already_exists", "already_claimed",
		"invalid_transition", "idempotency_in_progress":
		return backend.ErrConflict(op, msg)
	case "not_found":
		return backend.ErrNotFound(op, msg)
	default:
		return nil
	}
}

// attachMeta copies the response meta map onto the BackendError so callers
// can read structured fields like "existing_owner" without parsing message
// strings. No-op when meta is empty or err is not a *backend.BackendError.
func attachMeta(err error, meta map[string]string) error {
	if err == nil || len(meta) == 0 {
		return err
	}
	var be *backend.BackendError
	if !errors.As(err, &be) {
		return err
	}
	if be.Meta == nil {
		be.Meta = make(map[string]string, len(meta))
	}
	for k, v := range meta {
		be.Meta[k] = v
	}
	return err
}

// isInternalBucket reports whether err is the string matcher's default
// verdict (ErrInternal). Used to decide whether the status-based fallback
// should overrule the matcher.
func isInternalBucket(err error) bool {
	var be *backend.BackendError
	if !errors.As(err, &be) {
		return false
	}
	return be.Kind == backend.KindInternal
}

// classifyErrorString matches known error patterns from the response body.
func classifyErrorString(op, msg string) error {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "not found"):
		return backend.ErrNotFound(op, msg)
	case strings.Contains(lower, "already claimed"),
		strings.Contains(lower, "already closed"),
		strings.Contains(lower, "is closed"):
		return backend.ErrConflict(op, msg)
	case strings.Contains(lower, "validation"),
		strings.Contains(lower, "invalid"),
		strings.Contains(lower, "unknown field"):
		return backend.ErrValidation(op, msg)
	default:
		return backend.ErrInternal(op, msg, nil)
	}
}

// classifyTransportError maps Go HTTP transport errors to BackendError.
func classifyTransportError(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return backend.ErrCanceled(op, "operation canceled", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return backend.ErrTimeout(op, "operation timed out", err)
	}
	// Check for network errors (connection refused, DNS, etc.)
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return backend.ErrUnavailable(op, "fleet server unreachable: "+netErr.Error(), err)
	}
	return backend.ErrUnavailable(op, "fleet server communication failed: "+err.Error(), err)
}
