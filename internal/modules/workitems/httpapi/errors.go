package httpapi

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// classifyHTTPError maps an HTTP status code and response body to a
// *workitems.OperationError. For 2xx responses with success=false, the error
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

	switch statusCode {
	case 400:
		return workitems.AdapterInvalid(op, msg)
	case 401, 403:
		return workitems.AdapterUnavailable(op, "authentication failed: "+msg, nil)
	case 404:
		return workitems.AdapterNotFound(op, msg)
	case 409:
		return workitems.AdapterConflict(op, msg)
	case 429:
		return workitems.AdapterUnavailable(op, "rate limited: "+msg, nil)
	case 503:
		return workitems.AdapterUnavailable(op, msg, nil)
	case 504:
		return workitems.AdapterTimeout(op, msg, nil)
	default:
		if statusCode >= 400 {
			return workitems.AdapterInternal(op, msg, nil)
		}
		return nil
	}
}

// classifyErrorString matches known error patterns from the response body.
func classifyErrorString(op, msg string) error {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "not found"):
		return workitems.AdapterNotFound(op, msg)
	case strings.Contains(lower, "already claimed"):
		return workitems.AdapterConflict(op, msg)
	case strings.Contains(lower, "validation") || strings.Contains(lower, "invalid"):
		return workitems.AdapterInvalid(op, msg)
	default:
		return workitems.AdapterInternal(op, msg, nil)
	}
}

// classifyTransportError maps Go HTTP transport errors to Work Items errors.
func classifyTransportError(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return workitems.AdapterCanceled(op, "operation canceled", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return workitems.AdapterTimeout(op, "operation timed out", err)
	}
	// Check for network errors (connection refused, DNS, etc.)
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return workitems.AdapterUnavailable(op, "server unreachable: "+netErr.Error(), err)
	}
	return workitems.AdapterUnavailable(op, "server communication failed: "+err.Error(), err)
}
