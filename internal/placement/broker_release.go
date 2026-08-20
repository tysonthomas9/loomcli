package placement

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func (b *Broker) releaseLocked(ctx context.Context, workspaceKey, placementID string, fence ReleaseFence) (*domain.Node, error) {
	releaseCtx, cancel := detachedTimeout(ctx, detachedCreateTimeout)
	defer cancel()
	node, err := b.Get(releaseCtx, workspaceKey, placementID)
	if err != nil {
		return nil, err
	}
	if err := validateReleaseFence(node, fence); err != nil {
		return nil, err
	}
	if node.Placement.State == domain.PlacementStateReleased {
		return node, nil
	}
	if fence.Force {
		return b.forceReleasePlacement(releaseCtx, node)
	}
	sandboxID := strings.TrimSpace(node.Placement.SandboxID)
	if sandboxID == "" {
		return b.releaseUnknownSandboxID(releaseCtx, node, fence, false)
	}
	staged, err := b.markReleasing(releaseCtx, node)
	if err != nil {
		return nil, err
	}
	staged, err = b.appendAbandonedSandboxID(releaseCtx, staged, sandboxID)
	if err != nil {
		return nil, err
	}
	if err := b.deleteAndConfirmSandbox(releaseCtx, sandboxID); err != nil {
		return staged, err
	}
	return b.markReleased(releaseCtx, workspaceKey, placementID, fence, domain.PlacementReleaseReasonUnspecified)
}

func validateReleaseFence(node *domain.Node, fence ReleaseFence) error {
	if node == nil || node.Placement == nil {
		return fmt.Errorf("placement record required: %w", domain.ErrInvalid)
	}
	if fence.Generation > 0 && node.Placement.Generation != fence.Generation {
		return fmt.Errorf("placement %q generation %d does not match fence generation %d: %w", node.NodeID, node.Placement.Generation, fence.Generation, domain.ErrConflict)
	}
	if fence.SandboxID != "" && strings.TrimSpace(node.Placement.SandboxID) != fence.SandboxID {
		return fmt.Errorf("placement %q sandbox %q does not match fence sandbox %q: %w", node.NodeID, strings.TrimSpace(node.Placement.SandboxID), fence.SandboxID, domain.ErrConflict)
	}
	return nil
}

func (b *Broker) markReleasing(ctx context.Context, node *domain.Node) (*domain.Node, error) {
	if node == nil || node.Placement == nil {
		return nil, fmt.Errorf("placement record required: %w", domain.ErrInvalid)
	}
	if node.Placement.State == domain.PlacementStateReleased {
		return node, nil
	}
	placement := clonePlacement(node.Placement)
	placement.State = domain.PlacementStateReleasing
	placementPtr := &placement
	updated, err := b.store.Nodes().Update(ctx, node.WorkspaceKey, node.NodeID, store.NodeUpdate{Placement: &placementPtr})
	if err != nil {
		return nil, err
	}
	if updated == nil || updated.Placement == nil {
		return nil, fmt.Errorf("mark releasing placement %q returned no placement: %w", node.NodeID, domain.ErrInvalid)
	}
	return updated, nil
}

func (b *Broker) markReleased(ctx context.Context, workspaceKey, placementID string, fence ReleaseFence, reason domain.PlacementReleaseReason) (*domain.Node, error) {
	node, err := b.Get(ctx, workspaceKey, placementID)
	if err != nil {
		return nil, err
	}
	if err := validateReleaseFence(node, fence); err != nil {
		return nil, err
	}
	if node.Placement.State == domain.PlacementStateReleased {
		return node, nil
	}
	placement := clonePlacement(node.Placement)
	placement.State = domain.PlacementStateReleased
	placement.ReleaseReason = reason
	placement.LeadProcessStartedAt = nil
	placement.ProvisioningDeadlineAt = nil
	placementPtr := &placement
	updated, err := b.store.Nodes().Update(ctx, node.WorkspaceKey, node.NodeID, store.NodeUpdate{Placement: &placementPtr})
	if err != nil {
		return nil, err
	}
	if updated == nil || updated.Placement == nil {
		return nil, fmt.Errorf("mark released placement %q returned no placement: %w", placementID, domain.ErrInvalid)
	}
	return updated, nil
}

func (b *Broker) markLost(ctx context.Context, node *domain.Node) error {
	if node == nil || node.Placement == nil {
		return fmt.Errorf("placement record required: %w", domain.ErrInvalid)
	}
	placement := clonePlacement(node.Placement)
	placement.State = domain.PlacementStateLost
	now := b.now().UTC()
	placement.LostAt = &now
	placement.AbsenceConfirmedAt = nil
	placement.LeadProcessStartedAt = nil
	placement.ProvisioningDeadlineAt = nil
	placementPtr := &placement
	writeCtx, cancel := detachedTimeout(ctx, detachedStoreWriteTimeout)
	defer cancel()
	updated, err := b.store.Nodes().Update(writeCtx, node.WorkspaceKey, node.NodeID, store.NodeUpdate{Placement: &placementPtr})
	if err != nil {
		return err
	}
	if updated == nil || updated.Placement == nil {
		return fmt.Errorf("mark lost placement %q returned no placement: %w", node.NodeID, domain.ErrInvalid)
	}
	return nil
}

func (b *Broker) markLostAbsenceConfirmed(ctx context.Context, node *domain.Node) error {
	if node == nil || node.Placement == nil {
		return fmt.Errorf("placement record required: %w", domain.ErrInvalid)
	}
	placement := clonePlacement(node.Placement)
	now := b.now().UTC()
	placement.AbsenceConfirmedAt = &now
	placementPtr := &placement
	writeCtx, cancel := detachedTimeout(ctx, detachedStoreWriteTimeout)
	defer cancel()
	updated, err := b.store.Nodes().Update(writeCtx, node.WorkspaceKey, node.NodeID, store.NodeUpdate{Placement: &placementPtr})
	if err != nil {
		return err
	}
	if updated == nil || updated.Placement == nil {
		return fmt.Errorf("mark lost absence confirmed for placement %q returned no placement: %w", node.NodeID, domain.ErrInvalid)
	}
	return nil
}

// provisionFailureReasonMaxLen bounds the provider error text stamped onto the
// placement so an unbounded provider string (or stack trace) cannot bloat the
// node record or the attach-error surface.
const provisionFailureReasonMaxLen = 500

// markProvisionFailed flips a placement whose sandbox create never produced a
// sandbox out of provisioning into the terminal released state, recording the
// cause in LastDeleteError so the attach path can surface why. released (not
// lost) is deliberate: it frees the MaxLive reservation and is the only terminal
// state agent-create will reprovision over. Fence-free like markLost — callers
// hold the per-agent placement lock, so no generation race is possible.
func (b *Broker) markProvisionFailed(ctx context.Context, node *domain.Node, reason string) error {
	if node == nil || node.Placement == nil {
		return fmt.Errorf("placement record required: %w", domain.ErrInvalid)
	}
	if node.Placement.State == domain.PlacementStateReleased {
		return nil
	}
	placement := clonePlacement(node.Placement)
	placement.State = domain.PlacementStateReleased
	placement.LeadProcessStartedAt = nil
	placement.ProvisioningDeadlineAt = nil
	placement.LastDeleteError = truncateProvisionFailureReason(reason)
	placementPtr := &placement
	writeCtx, cancel := detachedTimeout(ctx, detachedStoreWriteTimeout)
	defer cancel()
	updated, err := b.store.Nodes().Update(writeCtx, node.WorkspaceKey, node.NodeID, store.NodeUpdate{Placement: &placementPtr})
	if err != nil {
		return err
	}
	if updated == nil || updated.Placement == nil {
		return fmt.Errorf("mark provision-failed placement %q returned no placement: %w", node.NodeID, domain.ErrInvalid)
	}
	return nil
}

func truncateProvisionFailureReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) <= provisionFailureReasonMaxLen {
		return reason
	}
	return reason[:provisionFailureReasonMaxLen] + "…"
}

func (b *Broker) appendAbandonedSandboxID(ctx context.Context, node *domain.Node, sandboxID string) (*domain.Node, error) {
	if node == nil || node.Placement == nil {
		return nil, fmt.Errorf("placement record required: %w", domain.ErrInvalid)
	}
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		sandboxID = unknownSandboxIDMarker
	}
	current, err := b.Get(ctx, node.WorkspaceKey, node.NodeID)
	if err != nil {
		return nil, err
	}
	placement := clonePlacement(current.Placement)
	placement.AbandonedSandboxIDs = append(append([]string(nil), placement.AbandonedSandboxIDs...), sandboxID)
	placementPtr := &placement
	writeCtx, cancel := detachedTimeout(ctx, detachedStoreWriteTimeout)
	defer cancel()
	updated, err := b.store.Nodes().Update(writeCtx, current.WorkspaceKey, current.NodeID, store.NodeUpdate{Placement: &placementPtr})
	if err != nil {
		return nil, err
	}
	if updated == nil || updated.Placement == nil {
		return nil, fmt.Errorf("append abandoned sandbox id for placement %q returned no placement: %w", node.NodeID, domain.ErrInvalid)
	}
	return updated, nil
}

func (b *Broker) appendAbandonedSandboxIDBestEffort(ctx context.Context, node *domain.Node, sandboxID string) *domain.Node {
	updated, err := b.appendAbandonedSandboxID(ctx, node, sandboxID)
	if err != nil {
		slog.WarnContext(ctx, "record abandoned sandbox id failed",
			"workspace", node.WorkspaceKey,
			"placement", node.NodeID,
			"sandbox", sandboxID,
			"error", err)
		return node
	}
	return updated
}

func (b *Broker) adoptSandboxForRelease(ctx context.Context, node *domain.Node, sandboxID string) (*domain.Node, error) {
	sandboxID = strings.TrimSpace(sandboxID)
	current, err := b.Get(ctx, node.WorkspaceKey, node.NodeID)
	if err != nil {
		return nil, err
	}
	currentSandboxID := strings.TrimSpace(current.Placement.SandboxID)
	if currentSandboxID != "" && currentSandboxID != sandboxID {
		return nil, fmt.Errorf("placement %q sandbox id is write-once: existing %q new %q: %w", node.NodeID, currentSandboxID, sandboxID, domain.ErrConflict)
	}
	placement := clonePlacement(current.Placement)
	placement.SandboxID = sandboxID
	placement.State = domain.PlacementStateReleasing
	placementPtr := &placement
	updated, err := b.store.Nodes().Update(ctx, current.WorkspaceKey, current.NodeID, store.NodeUpdate{Placement: &placementPtr})
	if err != nil {
		return nil, err
	}
	if updated == nil || updated.Placement == nil {
		return nil, fmt.Errorf("adopt sandbox %q for placement %q returned no placement: %w", sandboxID, node.NodeID, domain.ErrInvalid)
	}
	return updated, nil
}

func (b *Broker) forceReleasePlacement(ctx context.Context, node *domain.Node) (*domain.Node, error) {
	if node == nil || node.Placement == nil {
		return nil, fmt.Errorf("placement record required: %w", domain.ErrInvalid)
	}
	sandboxID := strings.TrimSpace(node.Placement.SandboxID)
	if sandboxID == "" {
		return b.releaseUnknownSandboxID(ctx, node, ReleaseFence{Generation: node.Placement.Generation}, true)
	}
	staged, err := b.markReleasing(ctx, node)
	if err != nil {
		return nil, err
	}
	ledgered, err := b.appendAbandonedSandboxID(ctx, staged, sandboxID)
	if err != nil {
		return nil, err
	}
	if err := b.deleteAndConfirmSandbox(ctx, sandboxID); err != nil {
		return ledgered, fmt.Errorf("force release sandbox %q deletion unconfirmed: %w", sandboxID, err)
	}
	return b.markReleased(ctx, ledgered.WorkspaceKey, ledgered.NodeID, ReleaseFence{
		Generation: ledgered.Placement.Generation,
		SandboxID:  sandboxID,
	}, domain.PlacementReleaseReasonUnspecified)
}

func (b *Broker) releaseUnknownSandboxID(ctx context.Context, node *domain.Node, fence ReleaseFence, forceClean bool) (*domain.Node, error) {
	if node == nil || node.Placement == nil {
		return nil, fmt.Errorf("placement record required: %w", domain.ErrInvalid)
	}
	matches, err := b.providerSandboxesForPlacement(ctx, node.NodeID)
	if err != nil {
		return nil, err
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("placement %q has %d provider sandboxes with label %s: %w", node.NodeID, len(matches), PlacementLabelKey, domain.ErrConflict)
	}
	if len(matches) == 1 {
		sandboxID := strings.TrimSpace(matches[0].ID)
		if sandboxID == "" {
			return nil, fmt.Errorf("provider sandbox labeled for placement %q has empty id: %w", node.NodeID, domain.ErrInvalid)
		}
		adopted, err := b.adoptSandboxForRelease(ctx, node, sandboxID)
		if err != nil {
			return nil, err
		}
		adopted, err = b.appendAbandonedSandboxID(ctx, adopted, sandboxID)
		if err != nil {
			return nil, err
		}
		if err := b.deleteAndConfirmSandbox(ctx, sandboxID); err != nil {
			return adopted, err
		}
		return b.markReleased(ctx, node.WorkspaceKey, node.NodeID, fence, domain.PlacementReleaseReasonUnspecified)
	}
	if !forceClean && !b.provisioningDeadlineExpired(node) {
		return node, fmt.Errorf("placement %q has no sandbox id and provider list has no positive match before provisioning deadline: %w", node.NodeID, domain.ErrConflict)
	}
	return b.markReleased(ctx, node.WorkspaceKey, node.NodeID, fence, domain.PlacementReleaseReasonUnspecified)
}

func (b *Broker) deleteSandbox(ctx context.Context, sandboxID string) error {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return fmt.Errorf("sandbox id required: %w", domain.ErrInvalid)
	}
	if err := b.provider.Delete(ctx, sandboxID); err != nil && !errors.Is(err, ErrSandboxNotFound) {
		return fmt.Errorf("delete sandbox %q: %w", sandboxID, err)
	}
	return nil
}

func (b *Broker) deleteAndConfirmSandbox(ctx context.Context, sandboxID string) error {
	if err := b.deleteSandbox(ctx, sandboxID); err != nil {
		return err
	}
	return b.confirmSandboxDeleted(ctx, sandboxID)
}

// confirmSandboxDeleted polls until the provider proves the sandbox is gone.
//
// A single read is not enough: provider deletion is asynchronous -- Daytona's
// Delete returns success while an immediate Get still reports the sandbox --
// so one read would never confirm, and every release would leave its record
// stuck in `releasing`.
//
// Only a not-found or a terminal absent state confirms. A transitional state
// deliberately does NOT, because stamping `released` on a sandbox that is
// still alive severs the only link anything has to a resource that is still
// billing. Failing to confirm is safe: the record stays `releasing` and the
// delete is re-driven.
func (b *Broker) confirmSandboxDeleted(ctx context.Context, sandboxID string) error {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return fmt.Errorf("sandbox id required: %w", domain.ErrInvalid)
	}
	var lastState ProviderSandboxState
	for attempt := range deleteConfirmAttempts {
		sandbox, err := b.provider.Get(ctx, sandboxID)
		if errors.Is(err, ErrSandboxNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get sandbox %q after delete: %w", sandboxID, err)
		}
		if sandbox.State == ProviderSandboxAbsent {
			return nil
		}
		lastState = sandbox.State
		if attempt == deleteConfirmAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("confirm sandbox %q deleted: %w", sandboxID, ctx.Err())
		case <-time.After(b.deleteConfirmBackoff << attempt):
		}
	}
	return fmt.Errorf("provider still reports sandbox %q as %q after delete: %w", sandboxID, lastState, domain.ErrConflict)
}
