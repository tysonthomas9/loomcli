package journal

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

var ErrStale = loomgit.NewError(loomgit.Stale, "journal entry changed", nil)
var ErrNotFound = errors.New("journal entry not found")
var ErrLeaseHeld = loomgit.ErrLeaseHeld
var ErrNeedsReplay = errors.New("unfinished request needs journal replay")

// Execute returns a completed request's stored result without running effect.
// For an interrupted request, the caller must replay its recorded phase and
// make the external effect idempotent before using this helper again.
func Execute(ctx context.Context, store loomgit.Store, requestID, operation string, effect func(context.Context) ([]byte, error)) ([]byte, error) {
	entry, created, err := store.Begin(ctx, requestID, operation)
	if err != nil {
		return nil, err
	}
	if entry.Operation != operation {
		return nil, fmt.Errorf("request %q belongs to %q", requestID, entry.Operation)
	}
	if entry.Phase == "done" {
		return entry.Result, nil
	}
	if !created || entry.Phase != "started" {
		return nil, fmt.Errorf("request %q at phase %q: %w", requestID, entry.Phase, ErrNeedsReplay)
	}
	result, err := effect(ctx)
	if err != nil {
		return nil, err
	}
	entry, err = store.Advance(ctx, entry, "done", result, nil)
	if err != nil {
		return nil, err
	}
	return entry.Result, nil
}
