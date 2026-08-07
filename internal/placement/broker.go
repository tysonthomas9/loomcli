package placement

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	defaultNodeTTL             = 10 * time.Minute
	defaultProvisioningTimeout = 10 * time.Minute
	// deleteConfirmAttempts bounds the poll proving a delete completed.
	// Provider deletion is asynchronous, so one read never confirms.
	deleteConfirmAttempts            = 5
	deleteConfirmBackoff             = 400 * time.Millisecond
	defaultLeadHeartbeatStaleAfter   = 5 * time.Minute
	defaultParkingAutostopInterval   = 2 * time.Minute
	defaultLeadBootPrepTimeout       = 5 * time.Minute
	defaultLeadPromptPath            = "/tmp/loom-lead-prompt.md"
	leadCheckoutRoot                 = "/root/workspace"
	deploymentIDEnv                  = "LOOM_DEPLOYMENT_ID"
	unknownSandboxIDMarker           = "<unknown>"
	detachedCreateTimeout            = 5 * time.Minute
	detachedStoreWriteTimeout        = 10 * time.Second
	detachedProviderOperationTimeout = 30 * time.Second
)

// Config wires Broker dependencies.
type Config struct {
	Store                   store.Store
	Provider                Provider
	TokenKey                []byte
	TokenTTL                time.Duration
	MaxLive                 ResourceSize
	NodeTTL                 time.Duration
	ProvisioningTimeout     time.Duration
	LeadHeartbeatStaleAfter time.Duration
	ParkingAutostopInterval time.Duration
	LeadBootPrepTimeout     time.Duration
	DeploymentID            string
	// DeleteConfirmBackoff is the first delay in the poll that proves a
	// delete completed; it doubles per attempt. Exposed so tests do not sleep
	// for seconds. Zero uses the default.
	DeleteConfirmBackoff time.Duration
	Now                  func() time.Time
}

// Broker creates, reads, lists, and releases lead placements.
type Broker struct {
	store                   store.Store
	provider                Provider
	tokenKey                []byte
	tokenTTL                time.Duration
	maxLive                 ResourceSize
	nodeTTL                 time.Duration
	provisioningTimeout     time.Duration
	leadHeartbeatStaleAfter time.Duration
	parkingAutostopInterval time.Duration
	leadBootPrepTimeout     time.Duration
	deploymentID            string
	deleteConfirmBackoff    time.Duration
	now                     func() time.Time

	// Deployed loom serve is a single process today. These per-key locks are
	// the only uniqueness guard for live (workspace, agent) placements;
	// multi-instance serve breaks that uniqueness silently without a
	// cross-process lock.
	locksMu     sync.Mutex
	locks       map[placementLockKey]*sync.Mutex
	admissionMu sync.Mutex
}

type placementLockKey struct {
	workspaceKey string
	agentName    string
}

// ProvisionRequest asks the broker to get or create a lead placement.
type ProvisionRequest struct {
	WorkspaceKey           string
	AgentName              string
	SnapshotRef            string
	Labels                 map[string]string
	Env                    map[string]string
	Caps                   []string
	Resource               ResourceSize
	NetworkDomainAllowlist []string
	Process                ProcessSpec
	RepoName               string
	GitToken               func() (string, error)
	PromptText             string
	Backend                string
	AgentRole              string
}

// ProvisionResult is returned by Provision for both created and existing rows.
type ProvisionResult struct {
	Node           *domain.Node
	Token          string
	Caps           []string
	Created        bool
	LeadStarted    bool
	LeadStartError string
}

// ReleaseFence identifies the placement generation and, optionally, the
// concrete sandbox a release is allowed to affect.
type ReleaseFence struct {
	Generation int64
	SandboxID  string
	Force      bool
}

// ListResult contains placement records plus summed live reservations.
type ListResult struct {
	Placements   []*domain.Node
	LiveReserved ResourceSize
}

// NewBroker validates dependencies and resolves the signing key.
func NewBroker(cfg Config) (*Broker, error) {
	if cfg.Store == nil || cfg.Provider == nil {
		return nil, fmt.Errorf("store and provider required: %w", domain.ErrInvalid)
	}
	key, err := brokerTokenKey(cfg.TokenKey)
	if err != nil {
		return nil, err
	}
	ttl := orDefaultDuration(cfg.TokenTTL, leadtoken.DefaultOccupantTokenTTL)
	nodeTTL := orDefaultDuration(cfg.NodeTTL, defaultNodeTTL)
	provisioningTimeout := orDefaultDuration(cfg.ProvisioningTimeout, defaultProvisioningTimeout)
	staleAfter := orDefaultDuration(cfg.LeadHeartbeatStaleAfter, defaultLeadHeartbeatStaleAfter)
	parkingAutostopInterval := orDefaultDuration(cfg.ParkingAutostopInterval, defaultParkingAutostopInterval)
	leadBootPrepTimeout := orDefaultDuration(cfg.LeadBootPrepTimeout, defaultLeadBootPrepTimeout)
	confirmBackoff := orDefaultDuration(cfg.DeleteConfirmBackoff, deleteConfirmBackoff)
	deploymentID, err := resolveDeploymentID(cfg.DeploymentID)
	if err != nil {
		return nil, err
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Broker{
		store:                   cfg.Store,
		provider:                cfg.Provider,
		tokenKey:                key,
		tokenTTL:                ttl,
		maxLive:                 cfg.MaxLive,
		nodeTTL:                 nodeTTL,
		provisioningTimeout:     provisioningTimeout,
		leadHeartbeatStaleAfter: staleAfter,
		parkingAutostopInterval: parkingAutostopInterval,
		leadBootPrepTimeout:     leadBootPrepTimeout,
		deploymentID:            deploymentID,
		deleteConfirmBackoff:    confirmBackoff,
		now:                     now,
		locks:                   make(map[placementLockKey]*sync.Mutex),
	}, nil
}

// orDefaultDuration treats a non-positive configured value as unset.
func orDefaultDuration(configured, fallback time.Duration) time.Duration {
	if configured <= 0 {
		return fallback
	}
	return configured
}

func resolveDeploymentID(configured string) (string, error) {
	if id := strings.TrimSpace(configured); id != "" {
		return id, nil
	}
	if id := strings.TrimSpace(os.Getenv(deploymentIDEnv)); id != "" {
		return id, nil
	}
	return "", fmt.Errorf("placement deployment id required: set Config.DeploymentID or %s so provider sandboxes are labeled with %s: %w", deploymentIDEnv, EnvironmentLabelKey, domain.ErrInvalid)
}

func brokerTokenKey(configured []byte) ([]byte, error) {
	if len(configured) > 0 {
		return append([]byte(nil), configured...), nil
	}
	key, err := leadtoken.ResolveSigningKey()
	if err != nil {
		return nil, fmt.Errorf("resolve occupant token signing key: %w", err)
	}
	return key, nil
}

func (b *Broker) lockPlacement(workspaceKey, agentName string) func() {
	key := placementLockKey{workspaceKey: workspaceKey, agentName: agentName}
	b.locksMu.Lock()
	lock := b.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		b.locks[key] = lock
	}
	b.locksMu.Unlock()

	// Provider I/O runs while this per-key lock is held because one
	// workspace/agent shares a placement identity, label, and generation
	// fence. Unrelated agents and workspaces use different locks.
	lock.Lock()
	return lock.Unlock
}

// Provision is get-or-create over live placements for one workspace/agent.
func (b *Broker) Provision(ctx context.Context, req ProvisionRequest) (*ProvisionResult, error) {
	req = normalizeProvisionRequest(req)
	if err := validateProvisionRequest(req); err != nil {
		return nil, err
	}
	unlock := b.lockPlacement(req.WorkspaceKey, req.AgentName)
	defer unlock()

	for {
		nodes, err := b.placementsForAgent(ctx, req.WorkspaceKey, req.AgentName)
		if err != nil {
			return nil, err
		}
		if live := latestLivePlacement(nodes); live != nil {
			if live.Placement.State == domain.PlacementStateReleasing {
				if _, err := b.releaseLocked(ctx, live.WorkspaceKey, live.NodeID, ReleaseFence{
					Generation: live.Placement.Generation,
					SandboxID:  live.Placement.SandboxID,
				}); err != nil {
					return nil, err
				}
				continue
			}
			result, retry, err := b.resumeLivePlacement(ctx, req, live)
			if err != nil {
				return nil, err
			}
			if retry {
				continue
			}
			return result, nil
		}
		existing := latestPlacement(nodes)
		predecessor, err := b.preparePredecessorForSuccessor(existing)
		if err != nil {
			return nil, err
		}
		bootPlan, err := b.resolveLeadBootPlan(ctx, req, true)
		if err != nil {
			return nil, err
		}
		node, err := b.admitProvisioningNode(ctx, req, predecessor)
		if err != nil {
			return nil, err
		}
		return b.createSandbox(ctx, req, node, bootPlan)
	}
}

// Get returns one placement node by placement id.
func (b *Broker) Get(ctx context.Context, workspaceKey, placementID string) (*domain.Node, error) {
	node, err := b.store.Nodes().Get(ctx, strings.TrimSpace(workspaceKey), strings.TrimSpace(placementID))
	if err != nil {
		return nil, err
	}
	if node == nil || node.Placement == nil {
		return nil, fmt.Errorf("placement %q: %w", placementID, domain.ErrNotFound)
	}
	return node, nil
}

// List returns placement nodes for a workspace and the live reserved pool.
func (b *Broker) List(ctx context.Context, workspaceKey string) (*ListResult, error) {
	nodes, err := b.store.Nodes().List(ctx, strings.TrimSpace(workspaceKey))
	if err != nil {
		return nil, err
	}
	result := &ListResult{}
	for _, node := range nodes {
		if node == nil || node.Placement == nil {
			continue
		}
		result.Placements = append(result.Placements, node)
		if node.RuntimeProvider == domain.RuntimeProviderDaytona && isQuotaReservedPlacementState(node.Placement.State) {
			addReservation(&result.LiveReserved, node.Placement)
		}
	}
	return result, nil
}

// Release deletes a provider sandbox and marks the placement released only
// after provider confirmation.
func (b *Broker) Release(ctx context.Context, workspaceKey, placementID string, fence ReleaseFence) (*domain.Node, error) {
	workspaceKey = strings.TrimSpace(workspaceKey)
	placementID = strings.TrimSpace(placementID)
	fence.SandboxID = strings.TrimSpace(fence.SandboxID)
	if !fence.Force && fence.Generation <= 0 && fence.SandboxID == "" {
		return nil, fmt.Errorf("release fence required: %w", domain.ErrInvalid)
	}
	current, err := b.Get(ctx, workspaceKey, placementID)
	if err != nil {
		return nil, err
	}
	agentName := placementAgentName(current)
	if agentName == "" {
		return nil, fmt.Errorf("placement %q agent missing: %w", placementID, domain.ErrInvalid)
	}
	unlock := b.lockPlacement(workspaceKey, agentName)
	defer unlock()

	return b.releaseLocked(ctx, workspaceKey, placementID, fence)
}

func (b *Broker) existingProvisionResult(node *domain.Node, caps []string) (*ProvisionResult, error) {
	token, outCaps, err := b.mintToken(node, caps)
	if err != nil {
		return nil, err
	}
	return &ProvisionResult{Node: node, Token: token, Caps: outCaps, LeadStarted: leadProcessRecorded(node)}, nil
}

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
	created, err := b.provider.Create(createCtx, providerCreateRequest(req, node.NodeID, token, b.deploymentID, bootPlan))
	sandboxID := strings.TrimSpace(created.SandboxID)
	if err != nil {
		if sandboxID != "" {
			node = b.appendAbandonedSandboxIDBestEffort(createCtx, node, sandboxID)
			if deleteErr := b.deleteSandbox(createCtx, sandboxID); deleteErr != nil {
				return nil, fmt.Errorf("create sandbox for placement %q returned sandbox %q but failed: %v; compensating delete failed, leaked sandbox id %q: %w", node.NodeID, sandboxID, err, sandboxID, deleteErr)
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
		DrainState:      domain.NodeDrainActive,
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

func (b *Broker) resumeLivePlacement(ctx context.Context, req ProvisionRequest, node *domain.Node) (*ProvisionResult, bool, error) {
	if node == nil || node.Placement == nil {
		return nil, false, fmt.Errorf("placement record required: %w", domain.ErrInvalid)
	}
	sandboxID := strings.TrimSpace(node.Placement.SandboxID)
	if sandboxID == "" {
		recoveredID, err := b.providerSandboxIDForPlacement(ctx, node.NodeID)
		if err != nil {
			return nil, false, err
		}
		if recoveredID == "" {
			if !b.provisioningDeadlineExpired(node) {
				return nil, false, fmt.Errorf("placement %q has no sandbox id and provisioning deadline has not elapsed: %w", node.NodeID, domain.ErrConflict)
			}
			if _, err := b.markReleased(ctx, node.WorkspaceKey, node.NodeID, ReleaseFence{Generation: node.Placement.Generation}); err != nil {
				return nil, false, err
			}
			return nil, true, nil
		}
		recorded, err := b.recordSandboxID(ctx, node, recoveredID)
		if err != nil {
			return nil, false, fmt.Errorf("record recovered sandbox id %q for placement %q: %w", recoveredID, node.NodeID, err)
		}
		node = recorded
		sandboxID = recoveredID
	}
	sandbox, err := b.confirmRecordedSandbox(ctx, node, sandboxID)
	if errors.Is(err, ErrSandboxNotFound) {
		if markErr := b.markLost(ctx, node); markErr != nil {
			return nil, false, markErr
		}
		return nil, false, fmt.Errorf("placement %q active sandbox %q is Get-confirmed absent and marked lost: %w", node.NodeID, sandboxID, domain.ErrConflict)
	}
	if err != nil {
		return nil, false, err
	}
	if sandbox.State == ProviderSandboxAbsent {
		if markErr := b.markLost(ctx, node); markErr != nil {
			return nil, false, markErr
		}
		return nil, false, fmt.Errorf("placement %q active sandbox %q is Get-confirmed absent and marked lost: %w", node.NodeID, sandboxID, domain.ErrConflict)
	}
	result, err := b.startOrReplaceRecordedSandbox(ctx, req, node)
	if err != nil {
		return nil, false, err
	}
	return result, false, nil
}

func (b *Broker) startOrReplaceRecordedSandbox(ctx context.Context, req ProvisionRequest, node *domain.Node) (*ProvisionResult, error) {
	token, caps, err := b.mintToken(node, req.Caps)
	if err != nil {
		return nil, err
	}
	sandboxID := strings.TrimSpace(node.Placement.SandboxID)
	if sandboxID == "" {
		return nil, fmt.Errorf("placement %q has no recorded sandbox id: %w", node.NodeID, domain.ErrInvalid)
	}
	result := &ProvisionResult{Node: node, Token: token, Caps: caps, LeadStarted: leadProcessRecorded(node)}
	if !b.shouldProbeLeadProcess(node) {
		return result, nil
	}
	bootPlan, err := b.resolveLeadBootPlan(ctx, req, false)
	if err != nil {
		b.recordLeadBootOutcome(ctx, result, leadBootOutcome{node: node}, err)
		return result, nil
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
				return nil, lostErr
			}
			return nil, fmt.Errorf("placement %q active sandbox %q is Get-confirmed absent and marked lost: %w", node.NodeID, sandboxID, domain.ErrConflict)
		}
		if prepErr != nil {
			b.recordLeadBootOutcome(ctx, result, leadBootOutcome{node: node}, prepErr)
			return result, nil
		}
	}
	outcome, err := b.tryStartLeadProcess(ctx, req, node, token, bootPlan)
	if errors.Is(err, ErrSandboxNotFound) {
		if lostErr := b.markLostAfterBootNotFound(ctx, node, sandboxID); lostErr != nil {
			return nil, lostErr
		}
		return nil, fmt.Errorf("placement %q active sandbox %q is Get-confirmed absent and marked lost: %w", node.NodeID, sandboxID, domain.ErrConflict)
	}
	if err != nil {
		b.recordLeadBootOutcome(ctx, result, outcome, err)
		return result, nil
	}
	b.recordLeadBootOutcome(ctx, result, outcome, nil)
	return result, nil
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
	outcome, err := b.tryStartLeadProcess(ctx, req, result.Node, token, bootPlan)
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

func (b *Broker) tryStartLeadProcess(ctx context.Context, req ProvisionRequest, node *domain.Node, token string, bootPlan leadBootPlan) (leadBootOutcome, error) {
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
	if err := b.provider.CreatePty(startCtx, sandboxID, processSpec(req, node, token, bootPlan)); err != nil && !errors.Is(err, ErrPtySessionAlreadyExists) {
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
	if err := b.armParkingAutostop(ctx, sandboxID); err != nil {
		slog.WarnContext(ctx, "arm lead sandbox autostop failed",
			"workspace", node.WorkspaceKey,
			"placement", node.NodeID,
			"sandbox", sandboxID,
			"error", err)
		return leadBootOutcome{node: started, started: true, errText: err.Error()}, nil
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
	stopCtx, cancel := detachedTimeout(ctx, detachedProviderOperationTimeout)
	defer cancel()
	if err := b.provider.SetAutostopInterval(stopCtx, sandboxID, b.parkingAutostopInterval); err != nil {
		return fmt.Errorf("set sandbox %q autostop interval: %w", sandboxID, err)
	}
	return nil
}

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
	return b.markReleased(releaseCtx, workspaceKey, placementID, fence)
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

func (b *Broker) markReleased(ctx context.Context, workspaceKey, placementID string, fence ReleaseFence) (*domain.Node, error) {
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
	})
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
		return b.markReleased(ctx, node.WorkspaceKey, node.NodeID, fence)
	}
	if !forceClean && !b.provisioningDeadlineExpired(node) {
		return node, fmt.Errorf("placement %q has no sandbox id and provider list has no positive match before provisioning deadline: %w", node.NodeID, domain.ErrConflict)
	}
	return b.markReleased(ctx, node.WorkspaceKey, node.NodeID, fence)
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

func (b *Broker) providerSandboxIDForPlacement(ctx context.Context, placementID string) (string, error) {
	matches, err := b.providerSandboxesForPlacement(ctx, placementID)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("placement %q has %d provider sandboxes with label %s: %w", placementID, len(matches), PlacementLabelKey, domain.ErrConflict)
	}
	sandboxID := strings.TrimSpace(matches[0].ID)
	if sandboxID == "" {
		return "", fmt.Errorf("provider sandbox labeled for placement %q has empty id: %w", placementID, domain.ErrInvalid)
	}
	return sandboxID, nil
}

func (b *Broker) providerSandboxesForPlacement(ctx context.Context, placementID string) ([]ProviderSandbox, error) {
	sandboxes, err := b.listProviderSandboxes(ctx, map[string]string{
		PlacementLabelKey: strings.TrimSpace(placementID),
	})
	if err != nil {
		return nil, fmt.Errorf("list provider sandboxes for placement %q: %w", placementID, err)
	}
	var matches []ProviderSandbox
	for _, sandbox := range sandboxes {
		if sandbox.State == ProviderSandboxAbsent {
			continue
		}
		if !providerSandboxMatchesPlacement(sandbox, placementID) {
			continue
		}
		confirmed, err := b.confirmListedSandbox(ctx, sandbox.ID)
		if errors.Is(err, ErrSandboxNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		matches = append(matches, confirmed)
	}
	return matches, nil
}

func (b *Broker) listProviderSandboxes(ctx context.Context, labels map[string]string) ([]ProviderSandbox, error) {
	listCtx, cancel := detachedTimeout(ctx, detachedProviderOperationTimeout)
	defer cancel()
	return b.provider.ListManaged(listCtx, labels)
}

func (b *Broker) confirmListedSandbox(ctx context.Context, sandboxID string) (ProviderSandbox, error) {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return ProviderSandbox{}, fmt.Errorf("provider sandbox has empty id: %w", domain.ErrInvalid)
	}
	getCtx, cancel := detachedTimeout(ctx, detachedProviderOperationTimeout)
	defer cancel()
	sandbox, err := b.provider.Get(getCtx, sandboxID)
	if err != nil {
		return ProviderSandbox{}, err
	}
	if sandbox.State == ProviderSandboxAbsent {
		return ProviderSandbox{}, ErrSandboxNotFound
	}
	return sandbox, nil
}

func (b *Broker) confirmRecordedSandbox(ctx context.Context, node *domain.Node, sandboxID string) (ProviderSandbox, error) {
	sandbox, err := b.confirmListedSandbox(ctx, sandboxID)
	if err != nil {
		return ProviderSandbox{}, err
	}
	if !providerSandboxMatchesPlacement(sandbox, node.NodeID) {
		return ProviderSandbox{}, fmt.Errorf("sandbox %q labels do not match placement %q: %w", sandboxID, node.NodeID, domain.ErrConflict)
	}
	return sandbox, nil
}

func (b *Broker) mintToken(node *domain.Node, caps []string) (string, []string, error) {
	if node == nil || node.Placement == nil {
		return "", nil, fmt.Errorf("placement record required: %w", domain.ErrInvalid)
	}
	outCaps := normalizeCaps(caps)
	token, err := leadtoken.MintOccupantToken(leadtoken.OccupantClaims{
		WorkspaceKey: node.WorkspaceKey,
		PlacementID:  node.NodeID,
		Generation:   node.Placement.Generation,
		Caps:         outCaps,
	}, b.tokenKey, b.tokenTTL)
	if err != nil {
		return "", nil, err
	}
	return token, outCaps, nil
}

func (b *Broker) placementsForAgent(ctx context.Context, workspaceKey, agentName string) ([]*domain.Node, error) {
	nodes, err := b.store.Nodes().List(ctx, workspaceKey)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.Node, 0, len(nodes))
	liveCount := 0
	for _, node := range nodes {
		if nodeMatchesAgent(node, agentName) {
			if node.Placement != nil && isLivePlacementState(node.Placement.State) {
				liveCount++
			}
			out = append(out, node)
		}
	}
	if liveCount > 1 {
		return nil, fmt.Errorf("agent %q in workspace %q has %d live placement rows: %w", agentName, workspaceKey, liveCount, domain.ErrConflict)
	}
	return out, nil
}

func (b *Broker) admitProvisioningNode(ctx context.Context, req ProvisionRequest, existing *domain.Node) (*domain.Node, error) {
	b.admissionMu.Lock()
	defer b.admissionMu.Unlock()
	excludeNodeID := ""
	generation := int64(1)
	if existing != nil {
		excludeNodeID = existing.NodeID
		if existing.Placement != nil {
			generation = existing.Placement.Generation + 1
		}
	}
	if err := b.checkQuota(ctx, req, excludeNodeID); err != nil {
		return nil, err
	}
	return b.createProvisioningNode(ctx, req, generation)
}

func (b *Broker) checkQuota(ctx context.Context, req ProvisionRequest, excludeNodeID string) error {
	if b.maxLive.VCPU <= 0 && b.maxLive.MemGiB <= 0 {
		return nil
	}
	reserved, err := b.accountReserved(ctx, req.WorkspaceKey, excludeNodeID)
	if err != nil {
		return err
	}
	next := reserved
	next.VCPU += req.Resource.VCPU
	next.MemGiB += req.Resource.MemGiB
	return enforceQuota(next, b.maxLive)
}

func (b *Broker) accountReserved(ctx context.Context, fallbackWorkspaceKey, excludeNodeID string) (ResourceSize, error) {
	// NodeStore has no account-wide List; enumerating WorkspaceStore.List and
	// then listing nodes per workspace is the widest scope the store exposes.
	workspaceKeys, err := b.workspaceKeys(ctx, fallbackWorkspaceKey)
	if err != nil {
		return ResourceSize{}, err
	}
	var total ResourceSize
	for _, workspaceKey := range workspaceKeys {
		nodes, err := b.store.Nodes().List(ctx, workspaceKey)
		if err != nil {
			return ResourceSize{}, err
		}
		for _, node := range nodes {
			if node == nil || node.Placement == nil {
				continue
			}
			if node.NodeID == excludeNodeID || node.RuntimeProvider != domain.RuntimeProviderDaytona {
				continue
			}
			if isQuotaReservedPlacementState(node.Placement.State) {
				addReservation(&total, node.Placement)
			}
		}
	}
	return total, nil
}

func (b *Broker) workspaceKeys(ctx context.Context, fallbackWorkspaceKey string) ([]string, error) {
	workspaces, err := b.store.Workspaces().List(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(workspaces)+1)
	keys := make([]string, 0, len(workspaces)+1)
	add := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		keys = append(keys, key)
	}
	for _, workspace := range workspaces {
		if workspace != nil {
			add(workspace.Key)
		}
	}
	add(fallbackWorkspaceKey)
	return keys, nil
}

func enforceQuota(next, max ResourceSize) error {
	if max.VCPU > 0 && next.VCPU > max.VCPU {
		return fmt.Errorf("live vcpu reservation %d exceeds cap %d: %w", next.VCPU, max.VCPU, domain.ErrUnschedulable)
	}
	if max.MemGiB > 0 && next.MemGiB > max.MemGiB {
		return fmt.Errorf("live memory reservation %dGiB exceeds cap %dGiB: %w", next.MemGiB, max.MemGiB, domain.ErrUnschedulable)
	}
	return nil
}

func normalizeProvisionRequest(req ProvisionRequest) ProvisionRequest {
	req.WorkspaceKey = strings.TrimSpace(req.WorkspaceKey)
	req.AgentName = strings.TrimSpace(req.AgentName)
	req.SnapshotRef = strings.TrimSpace(req.SnapshotRef)
	req.RepoName = strings.TrimSpace(req.RepoName)
	req.Backend = strings.TrimSpace(req.Backend)
	req.AgentRole = strings.TrimSpace(req.AgentRole)
	req.Caps = normalizeCaps(req.Caps)
	req.Labels = copyMap(req.Labels)
	req.Env = copyMap(req.Env)
	req.NetworkDomainAllowlist = append([]string(nil), req.NetworkDomainAllowlist...)
	return req
}

func validateProvisionRequest(req ProvisionRequest) error {
	if req.WorkspaceKey == "" || req.AgentName == "" || req.SnapshotRef == "" {
		return fmt.Errorf("workspace, agent, and snapshot ref required: %w", domain.ErrInvalid)
	}
	if req.Resource.VCPU <= 0 || req.Resource.MemGiB <= 0 {
		return fmt.Errorf("positive vcpu and memory reservations required: %w", domain.ErrInvalid)
	}
	return nil
}

type leadBootPlan struct {
	prep      LeadBootPrep
	checkout  string
	backend   string
	agentRole string
}

func (p leadBootPlan) needsPrep() bool {
	return p.prep.Repo != nil || p.prep.PromptText != ""
}

func (b *Broker) resolveLeadBootPlan(ctx context.Context, req ProvisionRequest, logEmptyRepo bool) (leadBootPlan, error) {
	backend, agentRole := b.resolveLeadEnvValues(ctx, req)
	plan := leadBootPlan{
		backend:   backend,
		agentRole: agentRole,
	}
	plan.prep.Timeout = b.effectiveLeadBootPrepTimeout()
	if req.PromptText != "" {
		promptPath := promptPathFromCommand(effectiveLeadCommand(req))
		if promptPath == "" {
			promptPath = defaultLeadPromptPath
		}
		if !strings.HasPrefix(strings.TrimSpace(promptPath), "/") {
			return leadBootPlan{}, fmt.Errorf("lead prompt path must be absolute: %w", domain.ErrInvalid)
		}
		plan.prep.PromptPath = promptPath
		plan.prep.PromptText = req.PromptText
	}

	repo, err := b.resolveLeadRepo(ctx, req, logEmptyRepo)
	if err != nil || repo == nil {
		return plan, err
	}
	checkout, err := leadRepoCheckoutPath(repo.Name)
	if err != nil {
		return leadBootPlan{}, err
	}
	_, host, err := NormalizeRepoCloneRemote(repo.RemoteURL)
	if err != nil {
		return leadBootPlan{}, fmt.Errorf("resolve lead repo clone remote for %q: %w", repo.Name, err)
	}
	if err := enforceCloneHostAllowlist(req.NetworkDomainAllowlist, host); err != nil {
		return leadBootPlan{}, err
	}
	plan.checkout = checkout
	plan.prep.Repo = &RepoClone{
		Name:      repo.Name,
		RemoteURL: repo.RemoteURL,
		Ref:       strings.TrimSpace(repo.DefaultBranch),
		Checkout:  checkout,
	}
	plan.prep.GitToken = req.GitToken
	return plan, nil
}

func (b *Broker) resolveLeadEnvValues(ctx context.Context, req ProvisionRequest) (backend string, agentRole string) {
	backend = strings.TrimSpace(req.Backend)
	agentRole = strings.TrimSpace(req.AgentRole)
	if backend != "" && agentRole != "" {
		return backend, agentRole
	}
	agent, err := b.store.Agents().Get(ctx, req.WorkspaceKey, req.AgentName)
	if err != nil || agent == nil {
		return backend, agentRole
	}
	if backend == "" {
		backend = strings.TrimSpace(agent.Backend)
	}
	if agentRole == "" {
		agentRole = strings.TrimSpace(agent.RoleName)
	}
	return backend, agentRole
}

func (b *Broker) resolveLeadRepo(ctx context.Context, req ProvisionRequest, logEmpty bool) (*domain.Repo, error) {
	repos, err := b.store.Repos().List(ctx, req.WorkspaceKey)
	if err != nil {
		return nil, err
	}
	repos = nonNilRepos(repos)
	switch len(repos) {
	case 0:
		if logEmpty {
			slog.InfoContext(ctx, "lead placement has no repos; booting without checkout",
				"workspace", req.WorkspaceKey,
				"agent", req.AgentName)
		}
		return nil, nil
	case 1:
		return repos[0], nil
	default:
		return selectNamedLeadRepo(repos, req.RepoName)
	}
}

func nonNilRepos(in []*domain.Repo) []*domain.Repo {
	// Allocates rather than filtering in place: the input is the store's
	// return value, and mutating its backing array would corrupt any store
	// that hands out an internal slice.
	out := make([]*domain.Repo, 0, len(in))
	for _, repo := range in {
		if repo != nil {
			out = append(out, repo)
		}
	}
	return out
}

func selectNamedLeadRepo(repos []*domain.Repo, name string) (*domain.Repo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("repo name required when workspace has %d repos: %w", len(repos), domain.ErrInvalid)
	}
	for _, repo := range repos {
		if strings.TrimSpace(repo.Name) == name {
			return repo, nil
		}
	}
	return nil, fmt.Errorf("repo %q not found among %d workspace repos: %w", name, len(repos), domain.ErrNotFound)
}

func leadRepoCheckoutPath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("repo name %q cannot form a lead checkout path: %w", name, domain.ErrInvalid)
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("repo name %q cannot form a lead checkout path: %w", name, domain.ErrInvalid)
		}
	}
	return path.Join(leadCheckoutRoot, name), nil
}

func enforceCloneHostAllowlist(allowlist []string, host string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || len(allowlist) == 0 {
		return nil
	}
	for _, entry := range allowlist {
		if strings.EqualFold(strings.TrimSpace(entry), host) {
			return nil
		}
	}
	return fmt.Errorf("lead repo clone host %q is not in network domain allowlist: %w", host, domain.ErrInvalid)
}

func providerCreateRequest(req ProvisionRequest, nodeID, token, deploymentID string, bootPlan leadBootPlan) CreateRequest {
	labels := copyMap(req.Labels)
	labels[PlacementLabelKey] = nodeID
	labels[EnvironmentLabelKey] = deploymentID
	labels["loom-workspace"] = req.WorkspaceKey
	labels["loom-agent"] = req.AgentName
	env := leadEnv(req.Env, req.WorkspaceKey, req.AgentName, nodeID, token, bootPlan)
	return CreateRequest{
		WorkspaceKey:           req.WorkspaceKey,
		AgentName:              req.AgentName,
		SnapshotRef:            req.SnapshotRef,
		Labels:                 labels,
		Env:                    env,
		Resource:               req.Resource,
		NetworkDomainAllowlist: append([]string(nil), req.NetworkDomainAllowlist...),
	}
}

func processSpec(req ProvisionRequest, node *domain.Node, token string, bootPlan leadBootPlan) ProcessSpec {
	spec := req.Process
	spec.SessionID = LeadPTYSessionID
	spec.Command = effectiveLeadCommand(req)
	if bootPlan.prep.PromptPath != "" && !commandHasPrompt(spec.Command) {
		spec.Command = append(spec.Command, "--prompt", bootPlan.prep.PromptPath)
	}
	if bootPlan.checkout != "" {
		spec.WorkingDir = bootPlan.checkout
	}
	spec.Env = leadEnv(spec.Env, req.WorkspaceKey, req.AgentName, node.NodeID, token, bootPlan)
	spec.TTY = true
	return spec
}

func effectiveLeadCommand(req ProvisionRequest) []string {
	command := append([]string(nil), req.Process.Command...)
	if len(command) == 0 {
		return []string{"loom", "--workspace", req.WorkspaceKey, "lead"}
	}
	return command
}

func commandHasPrompt(command []string) bool {
	return promptPathFromCommand(command) != ""
}

func promptPathFromCommand(command []string) string {
	for i, arg := range command {
		arg = strings.TrimSpace(arg)
		if arg == "--prompt" && i+1 < len(command) {
			return strings.TrimSpace(command[i+1])
		}
		if value, ok := strings.CutPrefix(arg, "--prompt="); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func leadEnv(base map[string]string, workspace, agent, nodeID, token string, bootPlan leadBootPlan) map[string]string {
	env := copyMap(base)
	env["LOOM_WORKSPACE"] = workspace
	env["LOOM_AGENT_NAME"] = agent
	env["LOOM_LEAD_PLACEMENT_ID"] = nodeID
	env[OccupantTokenEnv] = token
	env["TERM"] = "xterm-256color"
	if bootPlan.backend != "" {
		env["LOOM_BACKEND"] = bootPlan.backend
	}
	if bootPlan.agentRole != "" {
		env["LOOM_AGENT_ROLE"] = bootPlan.agentRole
	}
	return env
}

func nodeLabels(req ProvisionRequest) []string {
	return []string{
		"loom-lead-placement",
		"loom-workspace=" + req.WorkspaceKey,
		"loom-agent=" + req.AgentName,
	}
}

func latestLivePlacement(nodes []*domain.Node) *domain.Node {
	var out *domain.Node
	for _, node := range nodes {
		if node != nil && node.Placement != nil && isLivePlacementState(node.Placement.State) {
			out = laterPlacement(out, node)
		}
	}
	return out
}

func latestPlacement(nodes []*domain.Node) *domain.Node {
	var out *domain.Node
	for _, node := range nodes {
		if node != nil && node.Placement != nil {
			out = laterPlacement(out, node)
		}
	}
	return out
}

func laterPlacement(current, candidate *domain.Node) *domain.Node {
	if current == nil {
		return candidate
	}
	if candidate.Placement.Generation > current.Placement.Generation {
		return candidate
	}
	if candidate.Placement.Generation == current.Placement.Generation && candidate.UpdatedAt.After(current.UpdatedAt) {
		return candidate
	}
	return current
}

func isLivePlacementState(state domain.PlacementState) bool {
	switch state {
	case domain.PlacementStateProvisioning, domain.PlacementStateActive, domain.PlacementStateReleasing:
		return true
	default:
		return false
	}
}

func isQuotaReservedPlacementState(state domain.PlacementState) bool {
	switch state {
	case domain.PlacementStateProvisioning, domain.PlacementStateActive, domain.PlacementStateReleasing, domain.PlacementStateLost:
		return true
	default:
		return false
	}
}

func nodeMatchesAgent(node *domain.Node, agentName string) bool {
	if node == nil || node.Placement == nil {
		return false
	}
	if node.OwnerActor == agentOwnerActor(agentName) {
		return true
	}
	return hasLabel(node.Labels, "loom-agent="+agentName)
}

func placementAgentName(node *domain.Node) string {
	if node == nil {
		return ""
	}
	if name, ok := strings.CutPrefix(node.OwnerActor, "agent:"); ok {
		return strings.TrimSpace(name)
	}
	for _, label := range node.Labels {
		if name, ok := strings.CutPrefix(label, "loom-agent="); ok {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

func providerSandboxMatchesPlacement(sandbox ProviderSandbox, placementID string) bool {
	return strings.TrimSpace(sandbox.Labels[PlacementLabelKey]) == strings.TrimSpace(placementID)
}

func leadProcessRecorded(node *domain.Node) bool {
	return node != nil && node.Placement != nil && node.Placement.LeadProcessStartedAt != nil
}

func hasLabel(labels []string, want string) bool {
	for _, label := range labels {
		if label == want {
			return true
		}
	}
	return false
}

func addReservation(total *ResourceSize, placement *domain.NodePlacement) {
	total.VCPU += placement.ReservedVCPU
	total.MemGiB += placement.ReservedMemGiB
}

func normalizeCaps(caps []string) []string {
	out := make([]string, 0, len(caps)+1)
	for _, cap := range caps {
		if cap = strings.TrimSpace(cap); cap != "" {
			out = append(out, cap)
		}
	}
	if len(out) == 0 {
		return []string{CapLeadSession}
	}
	return out
}

func agentOwnerActor(agentName string) string {
	return "agent:" + agentName
}

func clonePlacement(in *domain.NodePlacement) domain.NodePlacement {
	if in == nil {
		return domain.NodePlacement{}
	}
	out := *in
	out.AbandonedSandboxIDs = append([]string(nil), in.AbandonedSandboxIDs...)
	return out
}

func (b *Broker) provisioningDeadline() time.Time {
	return b.now().UTC().Add(b.provisioningTimeout)
}

func (b *Broker) provisioningDeadlineExpired(node *domain.Node) bool {
	if node == nil || node.Placement == nil {
		return false
	}
	deadline := node.Placement.ProvisioningDeadlineAt
	if deadline == nil {
		fallback := node.CreatedAt
		if fallback.IsZero() {
			fallback = node.UpdatedAt
		}
		if fallback.IsZero() {
			fallback = b.now().UTC()
		}
		d := fallback.Add(b.provisioningTimeout)
		deadline = &d
	}
	return !b.now().UTC().Before(deadline.UTC())
}

func copyMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func detachedTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), timeout)
}

func newPlacementID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "lead-placement-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
	}
	return "lead-placement-" + hex.EncodeToString(buf)
}
