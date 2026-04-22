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
//  3. resp != nil and !resp.Success → classify from resp.Error string:
//     a. Contains "not found" (case-insensitive) → KindNotFound
//     b. Contains "no issue found" (case-insensitive) → KindNotFound
//     c. Contains "does not exist" (case-insensitive) → KindNotFound
//     d. Contains "already claimed" (case-insensitive) → KindConflict
//     e. Contains "already exists" (case-insensitive) → KindConflict
//     f. Contains "validation" or "invalid" (case-insensitive) → KindValidation
//     g. All others → KindInternal
//  4. err != nil (other transport error) → KindUnavailable
//  5. resp is nil (daemon not available) → KindUnavailable
//  6. No error → return nil
//
// IMPORTANT ordering note: the rpc.Client returns BOTH a non-nil resp AND a
// non-nil err when the daemon responds with resp.Success=false (the client
// wraps resp.Error in a fmt.Errorf and hands the caller both). We MUST
// inspect resp.Success BEFORE treating err as a transport failure — otherwise
// semantic errors (e.g. "issue not found" from bd) get misclassified as
// KindUnavailable. Only cancel/deadline checks take priority over resp, since
// those cause the socket read itself to fail and resp will be nil.
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
	}
	// A resp with Success=false is a semantic error from the daemon, even if
	// the client also surfaced an err wrapping resp.Error. Inspect it before
	// falling back to the generic transport-error path below.
	if resp != nil && !resp.Success {
		msg := resp.Error
		lower := strings.ToLower(msg)
		switch {
		case strings.Contains(lower, "not found"),
			strings.Contains(lower, "no issue found"),
			strings.Contains(lower, "does not exist"):
			return backend.ErrNotFound(op, msg)
		case strings.Contains(lower, "already claimed"),
			strings.Contains(lower, "already exists"):
			return backend.ErrConflict(op, msg)
		case strings.Contains(lower, "validation"),
			strings.Contains(lower, "invalid"):
			return backend.ErrValidation(op, msg)
		default:
			return backend.ErrInternal(op, msg, nil)
		}
	}
	if err != nil {
		return backend.ErrUnavailable(op, "daemon communication failed", err)
	}
	if resp == nil {
		return backend.ErrUnavailable(op, "daemon not available", nil)
	}
	return nil
}
