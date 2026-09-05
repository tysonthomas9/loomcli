package store

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// SkillMaterializationLeaseAcquire is the input for acquiring one target's
// short-lived materialization lease.
type SkillMaterializationLeaseAcquire struct {
	WorkspaceKey  string
	Holder        string
	TargetKey     string
	TreeRevisions []string
	TTL           time.Duration
}

// SkillMaterializationLeaseStore serializes writers of one host-local skill
// target. Missing and expired leases are idempotent successes on Release.
type SkillMaterializationLeaseStore interface {
	Acquire(ctx context.Context, in SkillMaterializationLeaseAcquire) (*domain.SkillMaterializationLease, error)
	Renew(ctx context.Context, workspaceKey, targetKey, token string, ttl time.Duration) (time.Time, error)
	Release(ctx context.Context, workspaceKey, targetKey, token string) error
}
