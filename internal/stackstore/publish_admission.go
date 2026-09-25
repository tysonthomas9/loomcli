package stackstore

import (
	"context"
	"time"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
)

// PublishAdmittable is implemented by stores that can admit a GitHub-mutating
// publish/restack session for one (workspace, stack_id). FleetDBStore uses the
// FleetDB publish-lease API and fails closed when unavailable. LocalStore uses
// a per-stack host flock shared by Publish, Restack, and epic reconcile.
//
// Never borrow DeliveryGroup membership or node expected_revision as a mutex.
type PublishAdmittable interface {
	AcquirePublishAdmission(ctx context.Context, ws string, id sl.StackID, holder string) (*PublishAdmission, error)
	RenewPublishAdmission(ctx context.Context, ws string, id sl.StackID, token string) (*PublishAdmission, error)
	// ReleasePublishAdmission is a clean release after remote effects are known
	// settled. FleetDB clears reuse_after grace; LocalStore writes a past-reuse
	// generation tombstone and drops the flock.
	ReleasePublishAdmission(ctx context.Context, ws string, id sl.StackID, token string) error
	// AbandonPublishAdmission ends the local hold without a clean release.
	// FleetDB is a no-op (leave TTL+grace for the successor). LocalStore drops
	// the flock fd but persists reuse_after so a later same-host process cannot
	// enter before grace — including after crash.
	AbandonPublishAdmission(ctx context.Context, ws string, id sl.StackID, token string) error
}

// PublishAdmission is one live admission grant. ReuseAfter is meaningful after
// expiry (FleetDB grace); a clean Release clears active grace once prior calls
// settle (LocalStore keeps a past-reuse generation tombstone).
type PublishAdmission struct {
	Token      string
	Holder     string
	Generation int64
	ExpiresAt  time.Time
	ReuseAfter time.Time
}
