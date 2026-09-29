package store

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// DeliveryGroupListOpts filters and pages delivery groups. Count in the
// response is the page length only; clients must page on HasMore.
type DeliveryGroupListOpts struct {
	// State is active (default), archived, or all.
	State string
	// EpicID limits to groups linked to that Loom issue.
	EpicID string
	// Limit is the page size (FleetDB default 50, max 200; ≤0 uses default).
	Limit int
	// Cursor is an opaque exclusive cursor from a prior NextCursor.
	Cursor string
}

// DeliveryGroupPage is one ID-ordered page of delivery groups.
type DeliveryGroupPage struct {
	Groups     []*domain.DeliveryGroup
	Count      int
	HasMore    bool
	NextCursor string
}

// DeliveryGroupWriteResult is the outcome of a create/update/set/archive.
// Status is 200 (replay or update) or 201 (fresh create). Replayed is true
// when FleetDB returned X-Idempotency-Replayed: true.
type DeliveryGroupWriteResult struct {
	Group    *domain.DeliveryGroup
	Status   int
	ETag     string
	Replayed bool
}

// DeliveryGroupStore is the FleetDB-backed durable delivery-group API.
// Implementations never call GitHub.
type DeliveryGroupStore interface {
	List(ctx context.Context, ws string, opts DeliveryGroupListOpts) (*DeliveryGroupPage, error)
	Get(ctx context.Context, ws, groupID string) (*domain.DeliveryGroup, error)
	GetByPR(ctx context.Context, ws, prKey string) (*domain.DeliveryGroup, error)
	Create(ctx context.Context, ws, idempotencyKey string, in domain.DeliveryGroupCreate) (*DeliveryGroupWriteResult, error)
	Update(ctx context.Context, ws, groupID, ifMatch, idempotencyKey string, in domain.DeliveryGroupUpdate) (*DeliveryGroupWriteResult, error)
	SetMembers(ctx context.Context, ws, groupID, ifMatch, idempotencyKey string, members []domain.DeliveryGroupMemberInput) (*DeliveryGroupWriteResult, error)
	Archive(ctx context.Context, ws, groupID, ifMatch, idempotencyKey string) (*DeliveryGroupWriteResult, error)
}

// OptionalDeliveryGroups is implemented by stores that expose delivery groups
// (the production FleetDB client). Memstores and other fakes omit it; the
// Pull Requests facade treats a missing backend as "no durable groups".
type OptionalDeliveryGroups interface {
	DeliveryGroups() DeliveryGroupStore
}
