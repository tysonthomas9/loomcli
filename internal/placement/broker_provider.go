package placement

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
)

func (b *Broker) providerSandboxIDForPlacement(ctx context.Context, prov providerHandle, placementID string) (string, error) {
	matches, err := b.providerSandboxesForPlacement(ctx, prov, placementID)
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

func (b *Broker) providerSandboxesForPlacement(ctx context.Context, prov providerHandle, placementID string) ([]ProviderSandbox, error) {
	sandboxes, err := b.listProviderSandboxes(ctx, prov, map[string]string{
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
		confirmed, err := b.confirmListedSandbox(ctx, prov, sandbox.ID)
		if errors.Is(err, ErrSandboxNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		matches = append(matches, confirmed)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("placement %q has %d provider sandboxes with label %s: %w", placementID, len(matches), PlacementLabelKey, domain.ErrConflict)
	}
	return matches, nil
}

func (b *Broker) listProviderSandboxes(ctx context.Context, prov providerHandle, labels map[string]string) ([]ProviderSandbox, error) {
	listCtx, cancel := detachedTimeout(ctx, detachedProviderOperationTimeout)
	defer cancel()
	return prov.adapter.ListManaged(listCtx, labels)
}

func (b *Broker) confirmListedSandbox(ctx context.Context, prov providerHandle, sandboxID string) (ProviderSandbox, error) {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return ProviderSandbox{}, fmt.Errorf("provider sandbox has empty id: %w", domain.ErrInvalid)
	}
	getCtx, cancel := detachedTimeout(ctx, detachedProviderOperationTimeout)
	defer cancel()
	sandbox, err := prov.adapter.Get(getCtx, sandboxID)
	if err != nil {
		return ProviderSandbox{}, err
	}
	if sandbox.State == ProviderSandboxAbsent {
		return ProviderSandbox{}, ErrSandboxNotFound
	}
	return sandbox, nil
}

func (b *Broker) confirmRecordedSandbox(ctx context.Context, node *domain.Node, sandboxID string) (ProviderSandbox, error) {
	prov, err := b.providerForNode(node)
	if err != nil {
		return ProviderSandbox{}, err
	}
	sandbox, err := b.confirmListedSandbox(ctx, prov, sandboxID)
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

// checkQuota enforces BOTH budgets: the account-wide MaxLive and, when
// configured, the requested provider's own cap.
//
// Both are required. The global cap alone lets one provider fill the account
// budget and starve the others. A per-provider cap alone would silently raise
// total capacity every time a provider is registered -- and MaxLive is what
// bounds the shared-OAuth blast radius, which is a property of the credential
// every lead shares, not of any one provider.
func (b *Broker) checkQuota(ctx context.Context, req ProvisionRequest, excludeNodeID string) error {
	providerMax := b.maxLiveFor(req.RuntimeProvider)
	globalUncapped := b.maxLive.VCPU <= 0 && b.maxLive.MemGiB <= 0
	providerUncapped := providerMax.VCPU <= 0 && providerMax.MemGiB <= 0
	if globalUncapped && providerUncapped {
		return nil
	}
	byProvider, total, err := b.accountReserved(ctx, req.WorkspaceKey, excludeNodeID)
	if err != nil {
		return err
	}
	if err := enforceQuota(withRequest(total, req.Resource), b.maxLive); err != nil {
		return fmt.Errorf("account-wide %w", err)
	}
	if providerUncapped {
		return nil
	}
	next := withRequest(byProvider[req.RuntimeProvider], req.Resource)
	if err := enforceQuota(next, providerMax); err != nil {
		return fmt.Errorf("runtime provider %q %w", req.RuntimeProvider, err)
	}
	return nil
}

func withRequest(reserved, request ResourceSize) ResourceSize {
	reserved.VCPU += request.VCPU
	reserved.MemGiB += request.MemGiB
	return reserved
}

// accountReserved returns reservations broken down per runtime provider AND
// their account-wide total. Both come from ONE walk of the same rows so the two
// figures cannot disagree -- admission and reporting drifting apart is the
// exact defect this replaced.
func (b *Broker) accountReserved(ctx context.Context, fallbackWorkspaceKey, excludeNodeID string) (map[domain.RuntimeProvider]ResourceSize, ResourceSize, error) {
	// NodeStore has no account-wide List; enumerating WorkspaceStore.List and
	// then listing nodes per workspace is the widest scope the store exposes.
	workspaceKeys, err := b.workspaceKeys(ctx, fallbackWorkspaceKey)
	if err != nil {
		return nil, ResourceSize{}, err
	}
	byProvider := make(map[domain.RuntimeProvider]ResourceSize)
	var total ResourceSize
	for _, workspaceKey := range workspaceKeys {
		nodes, err := b.store.Nodes().List(ctx, workspaceKey)
		if err != nil {
			return nil, ResourceSize{}, err
		}
		for _, node := range nodes {
			if node == nil || node.Placement == nil {
				continue
			}
			if node.NodeID == excludeNodeID || !b.countsTowardQuota(node) {
				continue
			}
			if !isQuotaReservedPlacementState(node.Placement.State) {
				continue
			}
			addReservation(&total, node.Placement)
			perProvider := byProvider[node.RuntimeProvider]
			addReservation(&perProvider, node.Placement)
			byProvider[node.RuntimeProvider] = perProvider
		}
	}
	return byProvider, total, nil
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
	// Required, not defaulted. Per-provider quota is impossible without it, and
	// defaulting to "the only provider" is the silent-fallback failure this
	// registry exists to prevent: a placement created on one provider and
	// released against another severs the record of a live, billing sandbox.
	if req.RuntimeProvider == "" {
		return fmt.Errorf("runtime provider required on provision request: %w", domain.ErrInvalid)
	}
	return nil
}
