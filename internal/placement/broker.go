package placement

import (
	"context"
	"fmt"
	"os"
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
	reviveAutostopInterval           = 30 * time.Minute
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
	LeadAPIBaseURL          string
	// LeadBootstrapEnabled turns on download-at-boot: every lead sandbox
	// downloads and installs serve's own loom binary (from LeadAPIBaseURL +
	// BootstrapLoomPath) before the PTY starts. Off leaves leads booting the
	// snapshot-baked binary. Requires LeadAPIBaseURL to be set to have effect.
	LeadBootstrapEnabled bool
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
	leadAPIBaseURL          string
	leadBootstrapEnabled    bool
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
	// SeedFiles are written into the sandbox before the lead PTY starts
	// (ticket 08's credential drop). Contents may be secrets; they are never
	// logged and never appear in errors.
	SeedFiles []SandboxFile
	Backend   string
	AgentRole string
	// ForceLeadProbe bypasses the heartbeat freshness shortcut for attach-time
	// revive, where provider state already proved the lead PTY may be missing.
	ForceLeadProbe bool
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
	Placements     []*domain.Node
	LiveReserved   ResourceSize
	NeedsAttention []string
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
		leadAPIBaseURL:          strings.TrimSpace(cfg.LeadAPIBaseURL),
		leadBootstrapEnabled:    cfg.LeadBootstrapEnabled,
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
		if PlacementNeedsAttention(node) {
			result.NeedsAttention = append(result.NeedsAttention, node.NodeID)
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
