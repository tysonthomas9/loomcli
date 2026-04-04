// Package beads implements backend.IssueBackend by wrapping the beads daemon RPC client.
//
// BeadsBackend translates between the backend-layer wire types and the beads daemon
// RPC protocol. It converts backend.*Opts/*Params to rpc.*Args, calls the RPC client,
// unmarshals responses into types.* structs, converts those to backend.*Data wire types,
// and classifies RPC errors into *backend.BackendError.
package beads

import (
	"context"
	"errors"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// classifyError inspects a transport error and/or RPC response to produce
// a *backend.BackendError with the appropriate ErrorKind.
//
// Classification rules (checked in order):
//  1. err != nil and errors.Is(err, context.Canceled) → KindCanceled
//  2. err != nil and errors.Is(err, context.DeadlineExceeded) → KindTimeout
//  3. err != nil (other transport error) → KindUnavailable
//  4. resp is nil (daemon not available) → KindUnavailable
//  5. resp != nil and !resp.Success → classify from resp.Error string:
//     a. Contains "not found" (case-insensitive) → KindNotFound
//     b. Contains "already claimed" (case-insensitive) → KindConflict
//     c. Contains "validation" or "invalid" (case-insensitive) → KindValidation
//     d. All others → KindInternal
//  6. No error → return nil
//
// NOTE: This string-matching approach is a pragmatic workaround because the
// beads daemon RPC protocol does not currently return structured error codes.
// When the daemon adds error codes, switch to code-based classification.
func classifyError(op string, err error, resp *rpc.Response) error {
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return backend.ErrCanceled(op, "operation canceled", err)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return backend.ErrTimeout(op, "operation timed out", err)
		}
		return backend.ErrUnavailable(op, "daemon communication failed", err)
	}
	if resp == nil {
		return backend.ErrUnavailable(op, "daemon not available", nil)
	}
	if !resp.Success {
		msg := resp.Error
		lower := strings.ToLower(msg)
		switch {
		case strings.Contains(lower, "not found"):
			return backend.ErrNotFound(op, msg)
		case strings.Contains(lower, "already claimed"):
			return backend.ErrConflict(op, msg)
		case strings.Contains(lower, "validation") || strings.Contains(lower, "invalid"):
			return backend.ErrValidation(op, msg)
		default:
			return backend.ErrInternal(op, msg, nil)
		}
	}
	return nil
}
