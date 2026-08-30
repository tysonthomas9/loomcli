package placement

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// sandboxNameForPlacement is the deterministic provider-side sandbox name for
// a placement row. The placement id is unique per generation, so a create
// retried for the same row either adopts the sandbox it already made or hits
// the provider's name-uniqueness guard — never a silent second sandbox.
func sandboxNameForPlacement(placementID string) string {
	return strings.TrimSpace(placementID)
}

// findSandboxByPlacementName performs the authoritative point read for the
// placement's deterministic sandbox name. Absent-state results are folded into
// ErrSandboxNotFound so callers see one "definitely not there" signal.
func (b *Broker) findSandboxByPlacementName(ctx context.Context, prov providerHandle, placementID string) (ProviderSandbox, error) {
	name := sandboxNameForPlacement(placementID)
	if name == "" {
		return ProviderSandbox{}, fmt.Errorf("placement id required: %w", domain.ErrInvalid)
	}
	getCtx, cancel := detachedTimeout(ctx, detachedProviderOperationTimeout)
	defer cancel()
	sandbox, err := prov.adapter.FindByName(getCtx, name)
	if err != nil {
		return ProviderSandbox{}, err
	}
	if sandbox.State == ProviderSandboxAbsent {
		return ProviderSandbox{}, ErrSandboxNotFound
	}
	return sandbox, nil
}

// reconcileProviderIdentity resolves which provider sandbox, if any, belongs
// to this placement: first the deterministic-name point read (authoritative),
// then the label list (each match point-Get-confirmed) for sandboxes created
// before deterministic naming. found=false means the provider positively
// reported zero — a lookup failure is returned as an error, never as absence.
func (b *Broker) reconcileProviderIdentity(ctx context.Context, node *domain.Node) (ProviderSandbox, bool, error) {
	prov, err := b.providerForNode(node)
	if err != nil {
		return ProviderSandbox{}, false, err
	}
	if node == nil || node.Placement == nil {
		return ProviderSandbox{}, false, fmt.Errorf("placement record required: %w", domain.ErrInvalid)
	}
	sandbox, err := b.findSandboxByPlacementName(ctx, prov, node.NodeID)
	if err == nil {
		if !providerSandboxMatchesPlacement(sandbox, node.NodeID) {
			return ProviderSandbox{}, false, fmt.Errorf(
				"sandbox %q holds placement %q's name but its %s label does not match: %w",
				sandbox.ID, node.NodeID, PlacementLabelKey, domain.ErrConflict)
		}
		return sandbox, true, nil
	}
	if !errors.Is(err, ErrSandboxNotFound) {
		return ProviderSandbox{}, false, fmt.Errorf("find sandbox by name for placement %q: %w", node.NodeID, err)
	}
	matches, err := b.providerSandboxesForPlacement(ctx, prov, node.NodeID)
	if err != nil {
		return ProviderSandbox{}, false, err
	}
	if len(matches) == 1 {
		return matches[0], true, nil
	}
	return ProviderSandbox{}, false, nil
}

// handleCreateFailure maps a failed provider create onto the placement record.
// A returned id gets the compensating delete; a provably-not-dispatched
// failure releases the row immediately; everything else is ambiguous and goes
// through reconciliation.
func (b *Broker) handleCreateFailure(ctx context.Context, node *domain.Node, created CreateResult, createErr error) (adopted bool, err error) {
	prov, err := b.providerForNode(node)
	if err != nil {
		return false, err
	}
	sandboxID := strings.TrimSpace(created.SandboxID)
	wrapped := fmt.Errorf("create sandbox for placement %q: %w", node.NodeID, createErr)
	if sandboxID != "" {
		node = b.appendAbandonedSandboxIDBestEffort(ctx, node, sandboxID)
		if deleteErr := b.deleteSandbox(ctx, prov, sandboxID); deleteErr != nil {
			return false, fmt.Errorf("create sandbox for placement %q returned sandbox %q but failed: %v; compensating delete failed, leaked sandbox id %q: %w", node.NodeID, sandboxID, createErr, sandboxID, deleteErr)
		}
		return false, wrapped
	}
	if created.ProvablyNotDispatched() {
		// The provider asserted no sandbox can exist, so this placement can
		// never boot: flip it out of provisioning to a terminal released state
		// with the cause recorded, rather than leaving it stuck until the
		// reaper's deadline (which would also leak the MaxLive reservation).
		// Best effort: never mask the original create error.
		if markErr := b.markProvisionFailed(ctx, node, createErr.Error()); markErr != nil {
			slog.WarnContext(ctx, "mark lead placement provision-failed failed",
				"workspace", node.WorkspaceKey,
				"placement", node.NodeID,
				"error", markErr)
		}
		return false, wrapped
	}
	// Ambiguous outcome: the request may have created a sandbox whose response
	// was lost. Releasing here would sever the only record of a possibly-
	// billing sandbox, so reconcile instead — adopt the sandbox if the
	// provider confirms it, otherwise stay provisioning and leave release to
	// the two-pass absence protocol.
	adopted, ambErr := b.reconcileAmbiguousCreate(ctx, node, createErr)
	if ambErr != nil {
		return false, ambErr
	}
	if adopted {
		return true, nil
	}
	return false, wrapped
}

// reconcileAmbiguousCreate handles a create that errored without proving no
// sandbox exists. It durably stamps the ambiguity, then tries to resolve it:
// adopting the placement's sandbox when the provider confirms exactly one
// (retry=true tells Provision to loop into the resume path), durably blocking
// on an identity conflict, and otherwise leaving the row provisioning so the
// two-pass absence protocol is the only route to release.
func (b *Broker) reconcileAmbiguousCreate(ctx context.Context, node *domain.Node, createErr error) (retry bool, err error) {
	if markErr := b.markProvisionAmbiguous(ctx, node, createErr.Error()); markErr != nil {
		slog.WarnContext(ctx, "mark lead placement create-ambiguous failed",
			"workspace", node.WorkspaceKey,
			"placement", node.NodeID,
			"error", markErr)
	}
	sandbox, found, reconcileErr := b.reconcileProviderIdentity(ctx, node)
	if reconcileErr != nil {
		if errors.Is(reconcileErr, domain.ErrConflict) {
			b.markAttentionReasonBestEffort(ctx, node, reconcileErr.Error())
		}
		return false, errors.Join(fmt.Errorf("create sandbox for placement %q: %w", node.NodeID, createErr), reconcileErr)
	}
	if !found {
		return false, nil
	}
	if _, recordErr := b.recordSandboxID(ctx, node, sandbox.ID); recordErr != nil {
		return false, errors.Join(fmt.Errorf("create sandbox for placement %q: %w", node.NodeID, createErr), recordErr)
	}
	slog.InfoContext(ctx, "adopted sandbox after ambiguous create",
		"workspace", node.WorkspaceKey,
		"placement", node.NodeID,
		"sandbox", sandbox.ID)
	return true, nil
}

// advanceCreateAbsence advances the two-pass absence protocol for an
// empty-sandbox-id provisioning row whose provider lookups returned a
// confirmed zero. The first confirmed zero past the provisioning deadline is
// durably recorded; release is authorized only by a second confirmed zero at
// least the reconfirm interval later. A single observation is never enough:
// the provider's list is eventually consistent and a create response can be
// lost after dispatch.
func (b *Broker) advanceCreateAbsence(ctx context.Context, node *domain.Node) (authorized bool, err error) {
	if node == nil || node.Placement == nil {
		return false, fmt.Errorf("placement record required: %w", domain.ErrInvalid)
	}
	confirmedAt := node.Placement.CreateAbsenceConfirmedAt
	if confirmedAt == nil {
		if err := b.markCreateAbsenceConfirmed(ctx, node); err != nil {
			return false, err
		}
		return false, nil
	}
	if b.now().UTC().Sub(confirmedAt.UTC()) < b.createAbsenceReconfirm {
		return false, nil
	}
	return true, nil
}

// ensureNoNewerPlacement rejects activating a placement row that a newer live
// generation has superseded — a race possible when a row sat ambiguous or
// released while another path (or another serve instance) admitted a
// successor. Rows without an agent label predate the label scheme and keep the
// generation-fence-only behavior.
func (b *Broker) ensureNoNewerPlacement(ctx context.Context, current *domain.Node) error {
	agentName := placementAgentName(current)
	if agentName == "" {
		return nil
	}
	nodes, err := b.store.Nodes().List(ctx, current.WorkspaceKey)
	if err != nil {
		return err
	}
	for _, other := range nodes {
		if other == nil || other.Placement == nil || other.NodeID == current.NodeID {
			continue
		}
		if !nodeMatchesAgent(other, agentName) {
			continue
		}
		if other.Placement.Generation > current.Placement.Generation && isLivePlacementState(other.Placement.State) {
			return fmt.Errorf(
				"placement %q generation %d is superseded by live placement %q generation %d: %w",
				current.NodeID, current.Placement.Generation, other.NodeID, other.Placement.Generation, domain.ErrConflict)
		}
	}
	return nil
}

// errCreateAbsenceAwaitingReconfirm formats the conflict returned while the
// two-pass absence window is still open.
func errCreateAbsenceAwaitingReconfirm(placementID string) error {
	return fmt.Errorf(
		"placement %q has no sandbox id and absence is not yet reconfirmed; retry after the reconfirm interval: %w",
		placementID, domain.ErrConflict)
}

func (b *Broker) markProvisionAmbiguous(ctx context.Context, node *domain.Node, detail string) error {
	return b.updatePlacement(ctx, node, "mark provision-ambiguous", func(placement *domain.NodePlacement) bool {
		if placement.ProvisionAmbiguousAt != nil {
			return false
		}
		now := b.now().UTC()
		placement.ProvisionAmbiguousAt = &now
		placement.ProvisionAmbiguityDetail = truncateProvisionFailureReason(detail)
		return true
	})
}

func (b *Broker) markCreateAbsenceConfirmed(ctx context.Context, node *domain.Node) error {
	return b.updatePlacement(ctx, node, "mark create-absence-confirmed", func(placement *domain.NodePlacement) bool {
		now := b.now().UTC()
		placement.CreateAbsenceConfirmedAt = &now
		return true
	})
}

func (b *Broker) markAttentionReason(ctx context.Context, node *domain.Node, reason string) error {
	return b.updatePlacement(ctx, node, "mark attention-reason", func(placement *domain.NodePlacement) bool {
		placement.AttentionReason = truncateProvisionFailureReason(reason)
		return true
	})
}

func (b *Broker) markAttentionReasonBestEffort(ctx context.Context, node *domain.Node, reason string) {
	if err := b.markAttentionReason(ctx, node, reason); err != nil {
		slog.WarnContext(ctx, "mark lead placement attention-reason failed",
			"workspace", node.WorkspaceKey,
			"placement", node.NodeID,
			"error", err)
	}
}

// updatePlacement refetches the node and applies mutate to a clone of its
// placement; mutate returning false skips the write.
func (b *Broker) updatePlacement(ctx context.Context, node *domain.Node, operation string, mutate func(*domain.NodePlacement) bool) error {
	if node == nil || node.Placement == nil {
		return fmt.Errorf("placement record required: %w", domain.ErrInvalid)
	}
	current, err := b.Get(ctx, node.WorkspaceKey, node.NodeID)
	if err != nil {
		return err
	}
	placement := clonePlacement(current.Placement)
	if !mutate(&placement) {
		return nil
	}
	placementPtr := &placement
	writeCtx, cancel := detachedTimeout(ctx, detachedStoreWriteTimeout)
	defer cancel()
	updated, err := b.store.Nodes().Update(writeCtx, current.WorkspaceKey, current.NodeID, store.NodeUpdate{Placement: &placementPtr})
	if err != nil {
		return err
	}
	if updated == nil || updated.Placement == nil {
		return fmt.Errorf("%s for placement %q returned no placement: %w", operation, node.NodeID, domain.ErrInvalid)
	}
	return nil
}
