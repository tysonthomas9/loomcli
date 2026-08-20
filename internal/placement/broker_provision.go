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

func (b *Broker) createSandbox(ctx context.Context, req ProvisionRequest, node *domain.Node, bootPlan leadBootPlan) (*ProvisionResult, error) {
	token, caps, err := b.mintToken(node, req.Caps)
	if err != nil {
		return nil, err
	}
	// Detaching here covers caller cancellation only: if the tab closes after
	// the durable provisioning reservation, Create, id recording, and
	// compensating delete still get a bounded chance to finish. Crash
	// durability comes from record-before-create, the loom-placement label, and
	// a reaper; this does not make an in-process crash atomic.
	createCtx, cancel := detachedTimeout(ctx, detachedCreateTimeout)
	defer cancel()
	created, err := b.provider.Create(createCtx, providerCreateRequest(req, node.NodeID, token, b.deploymentID, b.leadAPIBaseURL, bootPlan))
	sandboxID := strings.TrimSpace(created.SandboxID)
	if err != nil {
		if sandboxID != "" {
			node = b.appendAbandonedSandboxIDBestEffort(createCtx, node, sandboxID)
			if deleteErr := b.deleteSandbox(createCtx, sandboxID); deleteErr != nil {
				return nil, fmt.Errorf("create sandbox for placement %q returned sandbox %q but failed: %v; compensating delete failed, leaked sandbox id %q: %w", node.NodeID, sandboxID, err, sandboxID, deleteErr)
			}
		} else {
			// Create produced no sandbox at all: this placement can never boot, so
			// flip it out of provisioning to a terminal released state with the
			// cause recorded, rather than leaving it stuck until the reaper's
			// deadline (which would also leak the MaxLive reservation). Only the
			// no-sandbox case is safe to release here; a returned-then-deleted
			// sandbox stays provisioning so the reaper confirm-deletes it. Best
			// effort: never mask the original create error.
			if markErr := b.markProvisionFailed(createCtx, node, err.Error()); markErr != nil {
				slog.WarnContext(createCtx, "mark lead placement provision-failed failed",
					"workspace", node.WorkspaceKey,
					"placement", node.NodeID,
					"error", markErr)
			}
		}
		return nil, fmt.Errorf("create sandbox for placement %q: %w", node.NodeID, err)
	}
	recorded, err := b.recordSandboxID(createCtx, node, sandboxID)
	if err != nil {
		if sandboxID != "" {
			node = b.appendAbandonedSandboxIDBestEffort(createCtx, node, sandboxID)
			if deleteErr := b.deleteSandbox(createCtx, sandboxID); deleteErr != nil {
				return nil, fmt.Errorf("record sandbox id %q for placement %q: %v; compensating delete failed, leaked sandbox id %q: %w", sandboxID, node.NodeID, err, sandboxID, deleteErr)
			}
		}
		return nil, err
	}
	if bootPlan.needsPrep() {
		prepCtx, prepCancel := detachedTimeout(ctx, b.effectiveLeadBootPrepTimeout())
		prepErr := b.provider.PrepareLeadBoot(prepCtx, sandboxID, bootPlan.prep)
		prepCancel()
		if prepErr != nil {
			if err := b.compensateLeadBootPrepFailure(createCtx, recorded, sandboxID, prepErr); err != nil {
				return nil, err
			}
			return nil, prepErr
		}
	}
	result := &ProvisionResult{Node: recorded, Token: token, Caps: caps, Created: true}
	b.populateLeadBootResult(ctx, req, result, token, bootPlan)
	return result, nil
}

func (b *Broker) effectiveLeadBootPrepTimeout() time.Duration {
	timeout := b.leadBootPrepTimeout
	if timeout <= 0 {
		timeout = defaultLeadBootPrepTimeout
	}
	if b.provisioningTimeout > 0 && b.provisioningTimeout < timeout {
		return b.provisioningTimeout
	}
	return timeout
}

// compensateLeadBootPrepFailure unwinds a create whose prep failed by driving
// the full release path: releasing-intent first, then a provider-confirmed
// delete, and `released` only after confirmation. Delegating to releaseLocked
// (the caller already holds the per-agent lock) rather than issuing a bare
// delete keeps the broker's invariant that `released` is never stamped over an
// unconfirmed delete; on confirmation failure the node stays `releasing`, which
// Provision's own re-drive branch recovers on the next call.
func (b *Broker) compensateLeadBootPrepFailure(ctx context.Context, node *domain.Node, sandboxID string, cause error) error {
	if _, releaseErr := b.releaseLocked(ctx, node.WorkspaceKey, node.NodeID, ReleaseFence{
		Generation: node.Placement.Generation,
		SandboxID:  sandboxID,
	}); releaseErr != nil {
		return fmt.Errorf(
			"prepare lead boot for placement %q in sandbox %q: %v; compensating release failed: %w",
			node.NodeID,
			sandboxID,
			cause,
			releaseErr,
		)
	}
	return fmt.Errorf("prepare lead boot for placement %q in sandbox %q: %w", node.NodeID, sandboxID, cause)
}

func (b *Broker) preparePredecessorForSuccessor(existing *domain.Node) (*domain.Node, error) {
	if existing == nil || existing.Placement == nil {
		return existing, nil
	}
	switch existing.Placement.State {
	case domain.PlacementStateReleased:
		return existing, nil
	case domain.PlacementStateLost:
		return nil, fmt.Errorf("placement %q is lost and blocks reprovision; resolve manually with force release before creating a successor: %w", existing.NodeID, domain.ErrConflict)
	default:
		return nil, fmt.Errorf("placement %q state %q is not terminal for successor: %w", existing.NodeID, existing.Placement.State, domain.ErrConflict)
	}
}

func (b *Broker) createProvisioningNode(ctx context.Context, req ProvisionRequest, generation int64) (*domain.Node, error) {
	deadline := b.provisioningDeadline()
	placement := &domain.NodePlacement{
		Generation:             generation,
		ReservedVCPU:           req.Resource.VCPU,
		ReservedMemGiB:         req.Resource.MemGiB,
		State:                  domain.PlacementStateProvisioning,
		ProvisioningDeadlineAt: &deadline,
		SnapshotRef:            req.SnapshotRef,
	}
	return b.store.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    req.WorkspaceKey,
		NodeID:          newPlacementID(),
		OwnerActor:      agentOwnerActor(req.AgentName),
		RuntimeProvider: domain.RuntimeProviderDaytona,
		Placement:       placement,
		Labels:          nodeLabels(req),
		Capabilities:    []string{CapLeadSession},
		ToolInventory:   []string{"loom-lead"},
		DrainState:      domain.NodeDrainDrained,
		TTL:             b.nodeTTL,
	})
}

func (b *Broker) recordSandboxID(ctx context.Context, node *domain.Node, sandboxID string) (*domain.Node, error) {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return nil, fmt.Errorf("provider returned empty sandbox id: %w", domain.ErrInvalid)
	}
	current, err := b.Get(ctx, node.WorkspaceKey, node.NodeID)
	if err != nil {
		return nil, err
	}
	if current.Placement.Generation != node.Placement.Generation {
		return nil, fmt.Errorf("placement %q generation changed from %d to %d before sandbox id write: %w", node.NodeID, node.Placement.Generation, current.Placement.Generation, domain.ErrConflict)
	}
	currentSandboxID := strings.TrimSpace(current.Placement.SandboxID)
	if currentSandboxID != "" && currentSandboxID != sandboxID {
		return nil, fmt.Errorf("placement %q sandbox id is write-once: existing %q new %q: %w", node.NodeID, currentSandboxID, sandboxID, domain.ErrConflict)
	}
	placement := clonePlacement(current.Placement)
	placement.SandboxID = sandboxID
	placement.State = domain.PlacementStateActive
	placement.LeadProcessStartedAt = nil
	placement.ProvisioningDeadlineAt = nil
	placementPtr := &placement
	updated, err := b.store.Nodes().Update(ctx, current.WorkspaceKey, current.NodeID, store.NodeUpdate{Placement: &placementPtr})
	if err != nil {
		return nil, err
	}
	if updated == nil || updated.Placement == nil {
		return nil, fmt.Errorf("record sandbox id for placement %q returned no placement: %w", node.NodeID, domain.ErrInvalid)
	}
	return updated, nil
}

func (b *Broker) resumeLivePlacement(ctx context.Context, req ProvisionRequest, node *domain.Node) (result *ProvisionResult, retry bool, err error) {
	if node == nil || node.Placement == nil {
		return nil, false, fmt.Errorf("placement record required: %w", domain.ErrInvalid)
	}
	var sandboxID string
	node, sandboxID, retry, err = b.resolveResumeSandbox(ctx, node)
	if err != nil || retry {
		return nil, retry, err
	}
	sandbox, err := b.confirmRecordedSandbox(ctx, node, sandboxID)
	if errors.Is(err, ErrSandboxNotFound) {
		return nil, false, b.markLostResumeAbsent(ctx, node, sandboxID, "is Get-confirmed absent and marked lost")
	}
	if err != nil {
		return nil, false, err
	}
	if sandbox.State == ProviderSandboxAbsent {
		return nil, false, b.markLostResumeAbsent(ctx, node, sandboxID, "is Get-confirmed absent and marked lost")
	}
	if err := b.setAutostopInterval(ctx, sandboxID, reviveAutostopInterval); err != nil {
		if errors.Is(err, ErrSandboxNotFound) {
			return nil, false, b.markLostResumeAbsent(ctx, node, sandboxID, "disappeared before autostop shielding and was marked lost")
		}
		return nil, false, err
	}
	sandboxConfirmedAbsent := false
	defer func() {
		if sandboxConfirmedAbsent {
			return
		}
		err = b.restoreParkingAfterResume(ctx, node, sandboxID, result, err)
	}()

	resumed, err := b.provider.EnsureRunning(ctx, sandboxID)
	if errors.Is(err, ErrSandboxNotFound) {
		sandboxConfirmedAbsent = true
		if markErr := b.markLost(ctx, node); markErr != nil {
			return nil, false, markErr
		}
		return nil, false, fmt.Errorf("placement %q active sandbox %q disappeared before resume and was marked lost: %w", node.NodeID, sandboxID, domain.ErrConflict)
	}
	if err != nil {
		return nil, false, fmt.Errorf("ensure placement %q sandbox %q running: %w", node.NodeID, sandboxID, err)
	}
	result, sandboxConfirmedAbsent, err = b.startOrReplaceRecordedSandbox(ctx, req, node, resumed)
	if err != nil {
		return nil, false, err
	}
	return result, false, nil
}

// restoreParkingAfterResume re-arms the parking autostop when a revive exits
// and folds a restore failure into the resume outcome without masking it:
// joined into the returned error on failure paths, recorded on LeadStartError
// when the resume otherwise succeeded.
func (b *Broker) restoreParkingAfterResume(ctx context.Context, node *domain.Node, sandboxID string, result *ProvisionResult, err error) error {
	restoreErr := b.armParkingAutostop(ctx, sandboxID)
	if restoreErr == nil {
		return err
	}
	if err != nil || result == nil {
		return errors.Join(err, restoreErr)
	}
	if result.LeadStartError == "" {
		result.LeadStartError = restoreErr.Error()
	} else {
		result.LeadStartError = errors.Join(errors.New(result.LeadStartError), restoreErr).Error()
	}
	slog.WarnContext(ctx, "restore revived lead sandbox autostop failed",
		"workspace", node.WorkspaceKey,
		"placement", node.NodeID,
		"sandbox", sandboxID,
		"error", restoreErr)
	return err
}

// resolveResumeSandbox recovers a missing recorded sandbox id from provider
// labels. retry=true means the deadline-expired placement was released and the
// caller should re-admit from scratch.
func (b *Broker) resolveResumeSandbox(ctx context.Context, node *domain.Node) (*domain.Node, string, bool, error) {
	sandboxID := strings.TrimSpace(node.Placement.SandboxID)
	if sandboxID != "" {
		return node, sandboxID, false, nil
	}
	recoveredID, err := b.providerSandboxIDForPlacement(ctx, node.NodeID)
	if err != nil {
		return nil, "", false, err
	}
	if recoveredID == "" {
		if !b.provisioningDeadlineExpired(node) {
			return nil, "", false, fmt.Errorf("placement %q has no sandbox id and provisioning deadline has not elapsed: %w", node.NodeID, domain.ErrConflict)
		}
		if _, err := b.markReleased(ctx, node.WorkspaceKey, node.NodeID, ReleaseFence{Generation: node.Placement.Generation}, domain.PlacementReleaseReasonUnspecified); err != nil {
			return nil, "", false, err
		}
		return nil, "", true, nil
	}
	recorded, err := b.recordSandboxID(ctx, node, recoveredID)
	if err != nil {
		return nil, "", false, fmt.Errorf("record recovered sandbox id %q for placement %q: %w", recoveredID, node.NodeID, err)
	}
	return recorded, recoveredID, false, nil
}

// markLostResumeAbsent marks the placement lost after a resume step confirmed
// the recorded sandbox is gone; reason carries the exact step wording callers
// previously formatted inline.
func (b *Broker) markLostResumeAbsent(ctx context.Context, node *domain.Node, sandboxID, reason string) error {
	if markErr := b.markLost(ctx, node); markErr != nil {
		return markErr
	}
	return fmt.Errorf("placement %q active sandbox %q %s: %w", node.NodeID, sandboxID, reason, domain.ErrConflict)
}

func (b *Broker) startOrReplaceRecordedSandbox(ctx context.Context, req ProvisionRequest, node *domain.Node, resumed bool) (result *ProvisionResult, sandboxAbsent bool, err error) {
	token, caps, err := b.mintToken(node, req.Caps)
	if err != nil {
		return nil, false, err
	}
	sandboxID := strings.TrimSpace(node.Placement.SandboxID)
	if sandboxID == "" {
		return nil, false, fmt.Errorf("placement %q has no recorded sandbox id: %w", node.NodeID, domain.ErrInvalid)
	}
	result = &ProvisionResult{Node: node, Token: token, Caps: caps, LeadStarted: leadProcessRecorded(node)}
	if !resumed && !req.ForceLeadProbe && !b.shouldProbeLeadProcess(node) {
		return result, false, nil
	}
	bootPlan, err := b.resolveLeadBootPlan(ctx, req, false)
	if err != nil {
		b.recordLeadBootOutcome(ctx, result, leadBootOutcome{node: node}, err)
		return result, false, nil
	}
	// Prep must run on this path too, or a resume whose checkout or prompt
	// file is missing boots a PTY that dies on the hook's cd/--prompt and every
	// retry repeats it identically -- a permanent wedge. The checkout probe is
	// idempotent (present -> one exec, no clone), and failure is recorded
	// rather than compensated: a revive must never delete a sandbox that may
	// hold the lead's work. The extra probe execs on healthy resumes disappear
	// once lead heartbeats land and shouldProbeLeadProcess stops firing.
	if bootPlan.needsPrep() {
		prepCtx, prepCancel := detachedTimeout(ctx, b.effectiveLeadBootPrepTimeout())
		prepErr := b.provider.PrepareLeadBoot(prepCtx, sandboxID, bootPlan.prep)
		prepCancel()
		if errors.Is(prepErr, ErrSandboxNotFound) {
			if lostErr := b.markLostAfterBootNotFound(ctx, node, sandboxID); lostErr != nil {
				return nil, false, lostErr
			}
			return nil, true, fmt.Errorf("placement %q active sandbox %q is Get-confirmed absent and marked lost: %w", node.NodeID, sandboxID, domain.ErrConflict)
		}
		if prepErr != nil {
			b.recordLeadBootOutcome(ctx, result, leadBootOutcome{node: node}, prepErr)
			return result, false, nil
		}
	}
	outcome, err := b.tryStartLeadProcess(ctx, req, node, token, bootPlan, false)
	if errors.Is(err, ErrSandboxNotFound) {
		if lostErr := b.markLostAfterBootNotFound(ctx, node, sandboxID); lostErr != nil {
			return nil, false, lostErr
		}
		return nil, true, fmt.Errorf("placement %q active sandbox %q is Get-confirmed absent and marked lost: %w", node.NodeID, sandboxID, domain.ErrConflict)
	}
	if err != nil {
		b.recordLeadBootOutcome(ctx, result, outcome, err)
		return result, false, nil
	}
	b.recordLeadBootOutcome(ctx, result, outcome, nil)
	return result, false, nil
}

func (b *Broker) markLeadProcessStarted(ctx context.Context, node *domain.Node, sandboxID string) (*domain.Node, error) {
	current, err := b.Get(ctx, node.WorkspaceKey, node.NodeID)
	if err != nil {
		return nil, err
	}
	if current.Placement.Generation != node.Placement.Generation || strings.TrimSpace(current.Placement.SandboxID) != sandboxID {
		return current, nil
	}
	if current.Placement.State != domain.PlacementStateProvisioning && current.Placement.State != domain.PlacementStateActive {
		return current, nil
	}
	startedAt := b.now().UTC()
	placement := clonePlacement(current.Placement)
	placement.LeadProcessStartedAt = &startedAt
	placementPtr := &placement
	writeCtx, cancel := detachedTimeout(ctx, detachedStoreWriteTimeout)
	defer cancel()
	updated, err := b.store.Nodes().Update(writeCtx, current.WorkspaceKey, current.NodeID, store.NodeUpdate{Placement: &placementPtr})
	if err != nil {
		return nil, err
	}
	if updated == nil || updated.Placement == nil {
		return nil, fmt.Errorf("mark lead process started for placement %q returned no placement: %w", node.NodeID, domain.ErrInvalid)
	}
	return updated, nil
}

func (b *Broker) markLostAfterBootNotFound(ctx context.Context, node *domain.Node, sandboxID string) error {
	confirmCtx, cancel := detachedTimeout(ctx, detachedProviderOperationTimeout)
	defer cancel()
	if _, err := b.provider.Get(confirmCtx, sandboxID); err == nil {
		return fmt.Errorf("sandbox %q was reported missing by PTY create but Get still confirms it exists: %w", sandboxID, domain.ErrConflict)
	} else if !errors.Is(err, ErrSandboxNotFound) {
		return fmt.Errorf("confirm sandbox %q after PTY not found: %w", sandboxID, err)
	}
	return b.markLost(ctx, node)
}

func (b *Broker) shouldProbeLeadProcess(node *domain.Node) bool {
	if node == nil || node.Placement == nil {
		return false
	}
	if node.Placement.LeadProcessStartedAt == nil {
		return true
	}
	if node.LastHeartbeat.IsZero() {
		return true
	}
	return b.now().UTC().Sub(node.LastHeartbeat) > b.leadHeartbeatStaleAfter
}

type leadBootOutcome struct {
	node    *domain.Node
	started bool
	errText string
}

func (b *Broker) populateLeadBootResult(ctx context.Context, req ProvisionRequest, result *ProvisionResult, token string, bootPlan leadBootPlan) {
	if result == nil || result.Node == nil || result.Node.Placement == nil {
		return
	}
	outcome, err := b.tryStartLeadProcess(ctx, req, result.Node, token, bootPlan, true)
	b.recordLeadBootOutcome(ctx, result, outcome, err)
}

func (b *Broker) recordLeadBootOutcome(ctx context.Context, result *ProvisionResult, outcome leadBootOutcome, err error) {
	if result == nil || result.Node == nil || result.Node.Placement == nil {
		return
	}
	if err != nil {
		outcome = leadBootOutcome{node: result.Node, errText: err.Error()}
		slog.WarnContext(ctx, "lead boot failed after placement provisioning",
			"workspace", result.Node.WorkspaceKey,
			"placement", result.Node.NodeID,
			"sandbox", result.Node.Placement.SandboxID,
			"error", err)
	}
	result.Node = outcome.node
	result.LeadStarted = outcome.started
	result.LeadStartError = outcome.errText
}

func (b *Broker) tryStartLeadProcess(ctx context.Context, req ProvisionRequest, node *domain.Node, token string, bootPlan leadBootPlan, armAutostop bool) (leadBootOutcome, error) {
	sandboxID := strings.TrimSpace(node.Placement.SandboxID)
	if sandboxID == "" {
		return leadBootOutcome{node: node}, fmt.Errorf("placement %q has no recorded sandbox id: %w", node.NodeID, domain.ErrInvalid)
	}
	hasLead, err := b.providerHasLeadPTY(ctx, sandboxID)
	if err != nil {
		return leadBootOutcome{node: node}, err
	}
	if hasLead {
		if node.Placement.LeadProcessStartedAt != nil {
			return leadBootOutcome{node: node, started: true}, nil
		}
		started, err := b.markLeadProcessStarted(ctx, node, sandboxID)
		if err != nil {
			return leadBootOutcome{node: node}, err
		}
		return leadBootOutcome{node: started, started: true}, nil
	}
	startCtx, cancel := detachedTimeout(ctx, detachedProviderOperationTimeout)
	defer cancel()
	if err := b.provider.CreatePty(startCtx, sandboxID, processSpec(req, node, token, b.leadAPIBaseURL, bootPlan)); err != nil && !errors.Is(err, ErrPtySessionAlreadyExists) {
		return leadBootOutcome{node: node}, err
	}
	// Confirms the PTY was not already gone at first observation. This catches
	// the fast failure class (hook cd failure, missing prompt file) but not a
	// lead that dies seconds later; durable liveness is the heartbeat's job,
	// not this probe's. A fixed delay would not close that gap either -- the
	// slow failures (e.g. an in-sandbox store bootstrap) take seconds.
	hasLead, err = b.providerHasLeadPTY(ctx, sandboxID)
	if err != nil {
		return leadBootOutcome{node: node}, err
	}
	if !hasLead {
		return leadBootOutcome{node: node}, fmt.Errorf("lead PTY exited immediately after create")
	}
	started, err := b.markLeadProcessStarted(ctx, node, sandboxID)
	if err != nil {
		return leadBootOutcome{node: node}, err
	}
	if armAutostop {
		if err := b.armParkingAutostop(ctx, sandboxID); err != nil {
			slog.WarnContext(ctx, "arm lead sandbox autostop failed",
				"workspace", node.WorkspaceKey,
				"placement", node.NodeID,
				"sandbox", sandboxID,
				"error", err)
			return leadBootOutcome{node: started, started: true, errText: err.Error()}, nil
		}
	}
	return leadBootOutcome{node: started, started: true}, nil
}

func (b *Broker) providerHasLeadPTY(ctx context.Context, sandboxID string) (bool, error) {
	listCtx, cancel := detachedTimeout(ctx, detachedProviderOperationTimeout)
	defer cancel()
	sessions, err := b.provider.ListPtySessions(listCtx, sandboxID)
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		if strings.TrimSpace(session.SessionID) == LeadPTYSessionID {
			return true, nil
		}
	}
	return false, nil
}

func (b *Broker) armParkingAutostop(ctx context.Context, sandboxID string) error {
	if b.parkingAutostopInterval <= 0 {
		return nil
	}
	return b.setAutostopInterval(ctx, sandboxID, b.parkingAutostopInterval)
}

func (b *Broker) setAutostopInterval(ctx context.Context, sandboxID string, interval time.Duration) error {
	stopCtx, cancel := detachedTimeout(ctx, detachedProviderOperationTimeout)
	defer cancel()
	if err := b.provider.SetAutostopInterval(stopCtx, sandboxID, interval); err != nil {
		return fmt.Errorf("set sandbox %q autostop interval: %w", sandboxID, err)
	}
	return nil
}
