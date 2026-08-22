package placement

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Test-only exports for the live create-seam test. That test must live in
// package placement_test (it imports placement/daytona, which imports this
// package), so the reconcile internals it drives are re-exported here.

func (b *Broker) ReconcileProviderIdentityForTest(ctx context.Context, node *domain.Node) (ProviderSandbox, bool, error) {
	return b.reconcileProviderIdentity(ctx, node)
}

func (b *Broker) RecordSandboxIDForTest(ctx context.Context, node *domain.Node, sandboxID string) (*domain.Node, error) {
	return b.recordSandboxID(ctx, node, sandboxID)
}
