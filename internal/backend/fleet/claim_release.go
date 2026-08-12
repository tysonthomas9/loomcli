package fleet

import (
	"context"
	"net/url"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// Actor-scoped claim/release operations. Split out of fleet.go to keep that
// file under the 1000-line LOC ceiling after the release features landed.

// ClaimAsActor atomically claims an issue while overriding the configured
// FleetDB actor for this request.
func (b *FleetBackend) ClaimAsActor(ctx context.Context, id string, lockTTL time.Duration, actor string) error {
	if id == "" {
		return workitems.AdapterInvalid("Claim", "id must not be empty")
	}
	if actor == "" {
		return workitems.AdapterInvalid("Claim", "actor must not be empty")
	}
	body, err := claimIssueBody(lockTTL)
	if err != nil {
		return err
	}
	return b.execAsActor(ctx, "ClaimIssue", "/issues/"+url.PathEscape(id)+"/claim", body, actor)
}

// RenewClaimAsActor refreshes the actor's existing issue claim without
// allowing the heartbeat to change issue status or assignee.
func (b *FleetBackend) RenewClaimAsActor(ctx context.Context, id string, lockTTL time.Duration, actor string) error {
	if id == "" {
		return workitems.AdapterInvalid("RenewClaim", "id must not be empty")
	}
	if actor == "" {
		return workitems.AdapterInvalid("RenewClaim", "actor must not be empty")
	}
	body, err := renewIssueClaimBody(lockTTL)
	if err != nil {
		return err
	}
	return b.execAsActor(ctx, "RenewIssueClaim", "/issues/"+url.PathEscape(id)+"/claim", body, actor)
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
		return workitems.AdapterInvalid(op, "id must not be empty")
	}
	if actor == "" && useConfiguredActor {
		b.mu.RLock()
		actor = b.actor
		b.mu.RUnlock()
	}
	if actor == "" {
		return workitems.AdapterInvalid(op, "actor must not be empty")
	}
	return b.execAsActor(ctx, op, "/issues/"+url.PathEscape(id)+"/release-lock", nil, actor)
}

// ReleaseIssueAsActor releases the claim lock on an issue, overriding the
// configured FleetDB actor for this request. Used by the Execution runtime
// to symmetrically free a claim it acquired in claimIssueForAgent when the
// agent process exits, rather than waiting for the lock's TTL to expire.
func (b *FleetBackend) ReleaseIssueAsActor(ctx context.Context, id string, actor string) error {
	return b.ReleaseIssueLock(ctx, id, actor)
}

func claimIssueBody(lockTTL time.Duration) (interface{}, error) {
	if lockTTL < 0 {
		return nil, workitems.AdapterInvalid("Claim", "lockTTL must not be negative")
	}
	if lockTTL == 0 {
		return nil, nil
	}
	seconds := int((lockTTL + time.Second - time.Nanosecond) / time.Second)
	return struct {
		LockTTL int `json:"lock_ttl"`
	}{LockTTL: seconds}, nil
}

func renewIssueClaimBody(lockTTL time.Duration) (interface{}, error) {
	if lockTTL < 0 {
		return nil, workitems.AdapterInvalid("RenewClaim", "lockTTL must not be negative")
	}
	seconds := 0
	if lockTTL > 0 {
		seconds = int((lockTTL + time.Second - time.Nanosecond) / time.Second)
	}
	return struct {
		LockTTL   int  `json:"lock_ttl,omitempty"`
		RenewOnly bool `json:"renew_only"`
	}{LockTTL: seconds, RenewOnly: true}, nil
}
