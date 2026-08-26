package fleet

import (
	"context"
	"net/url"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// Actor-scoped claim/release operations. Split out of fleet.go to keep that
// file under the 1000-line LOC ceiling after the release features landed.

// ClaimIssueAsActor atomically claims an issue while overriding the configured
// FleetDB actor for this request.
func (b *FleetBackend) ClaimIssueAsActor(ctx context.Context, id string, lockTTL time.Duration, actor string) error {
	if id == "" {
		return backend.ErrValidation("ClaimIssue", "id must not be empty")
	}
	if actor == "" {
		return backend.ErrValidation("ClaimIssue", "actor must not be empty")
	}
	body, err := claimIssueBody(lockTTL)
	if err != nil {
		return err
	}
	return b.execAsActor(ctx, "ClaimIssue", "/issues/"+url.PathEscape(id)+"/claim", body, actor)
}

// ReleaseIssueLock releases only the operational lock on the issue without
// changing its status or assignee. Idempotent: a missing lock returns nil.
// Returns KindConflict if the lock is held by a different actor.
//
// When actor is empty, uses the configured backend actor. fleet-db requires a
// non-empty actor on the /release-lock endpoint to verify lock ownership.
func (b *FleetBackend) ReleaseIssueLock(ctx context.Context, id, actor string) error {
	return b.releaseIssueLock(ctx, "ReleaseIssueLock", id, actor, true)
}

func (b *FleetBackend) releaseIssueLock(ctx context.Context, op, id, actor string, useConfiguredActor bool) error {
	if id == "" {
		return backend.ErrValidation(op, "id must not be empty")
	}
	if actor == "" && useConfiguredActor {
		b.mu.RLock()
		actor = b.actor
		b.mu.RUnlock()
	}
	if actor == "" {
		return backend.ErrValidation(op, "actor must not be empty")
	}
	return b.execAsActor(ctx, op, "/issues/"+url.PathEscape(id)+"/release-lock", nil, actor)
}

// ReleaseIssueAsActor releases the full claim held by actor. In-progress tasks
// become open and unassigned; tasks that have already transitioned only drop
// their operational lock. This keeps a failed preflight claim retryable.
func (b *FleetBackend) ReleaseIssueAsActor(ctx context.Context, id string, actor string) error {
	return b.ReleaseClaim(ctx, id, actor)
}

func claimIssueBody(lockTTL time.Duration) (interface{}, error) {
	if lockTTL < 0 {
		return nil, backend.ErrValidation("ClaimIssue", "lockTTL must not be negative")
	}
	if lockTTL == 0 {
		return nil, nil
	}
	seconds := int((lockTTL + time.Second - time.Nanosecond) / time.Second)
	return struct {
		LockTTL int `json:"lock_ttl"`
	}{LockTTL: seconds}, nil
}
