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
// string in the body is matched against known patterns.
func classifyHTTPError(op string, statusCode int, body apiResponse) error {
	// 2xx with success=true: no error.
	if statusCode >= 200 && statusCode < 300 && body.Success {
		return nil
	}

	msg := body.Error
	if msg == "" {
		msg = "unknown error"
	}

	// 2xx with success=false: classify from error string.
	if statusCode >= 200 && statusCode < 300 {
		return classifyErrorString(op, msg)
	}

	// fleet-db surfaces semantic conflicts (e.g. "issue is already closed")
	// as 500 Internal Server Error rather than 409 Conflict. The status
	// code alone can't distinguish "transient server error" from "client
	// precondition failed"; give the string matcher a chance first. Only
	// promote when the match yields a non-default verdict, so we don't
	// re-bucket a genuine internal error.
	if statusCode >= 500 {
		if classified := classifyErrorString(op, msg); !isInternalBucket(classified) {
			return classified
		}
	}

	switch statusCode {
	case 400:
		return backend.ErrValidation(op, msg)
	case 401, 403:
		return backend.ErrUnavailable(op, "authentication failed: "+msg, nil)
	case 404:
		return backend.ErrNotFound(op, msg)
	case 409:
		return backend.ErrConflict(op, msg)
	case 429:
		return backend.ErrUnavailable(op, "rate limited: "+msg, nil)
	case 503:
		return backend.ErrUnavailable(op, msg, nil)
	case 504:
		return backend.ErrTimeout(op, msg, nil)
	default:
		if statusCode >= 400 {
			return backend.ErrInternal(op, msg, nil)
		}
		return nil
	}
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
	case strings.Contains(lower, "validation") || strings.Contains(lower, "invalid"):
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
