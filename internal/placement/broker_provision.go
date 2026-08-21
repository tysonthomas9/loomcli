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

func (b *Broker) createSandbox(ctx context.Context, req ProvisionRequest, node *domain.Node, bootPlan leadBootPlan) (result *ProvisionResult, adopted bool, err error) {
	token, caps, err := b.mintToken(node, req.Caps)
	if err != nil {
		return nil, false, err
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
		adopted, failErr := b.handleCreateFailure(createCtx, node, created, err)
		return nil, adopted, failErr
	}
	recorded, err := b.recordSandboxID(createCtx, node, sandboxID)
	if err != nil {
		if sandboxID != "" {
			node = b.appendAbandonedSandboxIDBestEffort(createCtx, node, sandboxID)
			if deleteErr := b.deleteSandbox(createCtx, sandboxID); deleteErr != nil {
				return nil, false, fmt.Errorf("record sandbox id %q for placement %q: %v; compensating delete failed, leaked sandbox id %q: %w", sandboxID, node.NodeID, err, sandboxID, deleteErr)
			}
		}
		return nil, false, err
	}
	if bootPlan.needsPrep() {
		prepCtx, prepCancel := detachedTimeout(ctx, b.effectiveLeadBootPrepTimeout())
		prepErr := b.provider.PrepareLeadBoot(prepCtx, sandboxID, bootPlan.prep)
		prepCancel()
		if prepErr != nil {
			if err := b.compensateLeadBootPrepFailure(createCtx, recorded, sandboxID, prepErr); err != nil {
				return nil, false, err
			}
			return nil, false, prepErr
		}
	}
	result = &ProvisionResult{Node: recorded, Token: token, Caps: caps, Created: true}
	b.populateLeadBootResult(ctx, req, result, token, bootPlan)
	return result, false, nil
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

// preparePredecessorForSuccessor gates admission of a new generation on the
// predecessor's true provider state. adopted=true means the predecessor's
// sandbox is alive and was adopted back onto its row — the caller must resume
// it instead of admitting a successor, or the account ends up paying for two
// sandboxes under one agent.
func (b *Broker) preparePredecessorForSuccessor(ctx context.Context, existing *domain.Node) (node *domain.Node, adopted bool, err error) {
	if existing == nil || existing.Placement == nil {
		return existing, false, nil
	}
	switch existing.Placement.State {
	case domain.PlacementStateReleased:
		sandbox, found, err := b.reconcileProviderIdentity(ctx, existing)
		if err != nil {
			if errors.Is(err, domain.ErrConflict) {
				b.markAttentionReasonBestEffort(ctx, existing, err.Error())
			}
			return nil, false, fmt.Errorf("reconcile released predecessor %q before successor: %w", existing.NodeID, err)
		}
		if found {
			if _, err := b.recordSandboxID(ctx, existing, sandbox.ID); err != nil {
				return nil, false, fmt.Errorf("adopt sandbox %q onto released predecessor %q: %w", sandbox.ID, existing.NodeID, err)
			}
			return nil, true, nil
		}
		return existing, false, nil
	case domain.PlacementStateLost:
		return nil, false, fmt.Errorf("placement %q is lost and blocks reprovision; resolve manually with force release before creating a successor: %w", existing.NodeID, domain.ErrConflict)
	default:
		return nil, false, fmt.Errorf("placement %q state %q is not terminal for successor: %w", existing.NodeID, existing.Placement.State, domain.ErrConflict)
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
	// Only provisioning (the create path), active (idempotent re-record), and
	// released (reconcile adoption of a sandbox that outlived its record) may
	// become active. Releasing and lost rows are mid-protocol; activating them
	// would bypass delete confirmation or the lost-release consent gate.
	switch current.Placement.State {
	case domain.PlacementStateProvisioning, domain.PlacementStateActive, domain.PlacementStateReleased:
	default:
		return nil, fmt.Errorf("placement %q state %q cannot record a sandbox id: %w", node.NodeID, current.Placement.State, domain.ErrConflict)
	}
	currentSandboxID := strings.TrimSpace(current.Placement.SandboxID)
	if currentSandboxID != "" && currentSandboxID != sandboxID {
		return nil, fmt.Errorf("placement %q sandbox id is write-once: existing %q new %q: %w", node.NodeID, currentSandboxID, sandboxID, domain.ErrConflict)
	}
	// A newer generation may have been admitted by another path (or another
	// serve instance) while this row sat ambiguous or released; activating the
	// older row then would put two live placements under one agent.
	if err := b.ensureNoNewerPlacement(ctx, current); err != nil {
		return nil, err
	}
	placement := clonePlacement(current.Placement)
	placement.SandboxID = sandboxID
	placement.State = domain.PlacementStateActive
	placement.LeadProcessStartedAt = nil
	placement.ProvisioningDeadlineAt = nil
	placement.ProvisionAmbiguousAt = nil
	placement.ProvisionAmbiguityDetail = ""
	placement.CreateAbsenceConfirmedAt = nil
	placement.AttentionReason = ""
	placement.ReleaseReason = domain.PlacementReleaseReasonUnspecified
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

// resolveResumeSandbox recovers a missing recorded sandbox id via the
// deterministic-name point read, then provider labels. retry=true means the
// placement completed the two-pass absence protocol, was released, and the
// caller should re-admit from scratch. A single zero observation never
// releases: the create that left this row id-less may have made a sandbox
// whose response was lost.
func (b *Broker) resolveResumeSandbox(ctx context.Context, node *domain.Node) (*domain.Node, string, bool, error) {
	sandboxID := strings.TrimSpace(node.Placement.SandboxID)
	if sandboxID != "" {
		return node, sandboxID, false, nil
	}
	sandbox, found, err := b.reconcileProviderIdentity(ctx, node)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			b.markAttentionReasonBestEffort(ctx, node, err.Error())
		}
		return nil, "", false, err
	}
	if !found {
		if !b.provisioningDeadlineExpired(node) {
			return nil, "", false, fmt.Errorf("placement %q has no sandbox id and provisioning deadline has not elapsed: %w", node.NodeID, domain.ErrConflict)
		}
		authorized, err := b.advanceCreateAbsence(ctx, node)
		if err != nil {
			return nil, "", false, err
		}
		if !authorized {
			return nil, "", false, errCreateAbsenceAwaitingReconfirm(node.NodeID)
		}
		if _, err := b.markReleased(ctx, node.WorkspaceKey, node.NodeID, ReleaseFence{Generation: node.Placement.Generation}, domain.PlacementReleaseReasonCreateConfirmedAbsent); err != nil {
			return nil, "", false, err
		}
		return nil, "", true, nil
	}
	recorded, err := b.recordSandboxID(ctx, node, sandbox.ID)
	if err != nil {
		return nil, "", false, fmt.Errorf("record recovered sandbox id %q for placement %q: %w", sandbox.ID, node.NodeID, err)
	}
	return recorded, sandbox.ID, false, nil
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
