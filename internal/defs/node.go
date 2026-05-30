package defs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func applyNodes(ctx context.Context, st store.Store, ws string, nodes []NodeModule) error {
	if len(nodes) == 0 {
		return nil
	}
	nodeStore := st.Nodes()
	if nodeStore == nil {
		return fmt.Errorf("node store not configured")
	}
	for _, node := range nodes {
		if err := applyNode(ctx, st, ws, node); err != nil {
			return err
		}
	}
	return nil
}

func applyNode(ctx context.Context, st store.Store, ws string, node NodeModule) error {
	existing, err := st.Nodes().Get(ctx, ws, node.NodeID)
	if err == nil && existing != nil {
		return syncNodeState(ctx, st, ws, node.NodeID, node)
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("get node %s: %w", node.NodeID, err)
	}
	created, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    ws,
		NodeID:          node.NodeID,
		OwnerActor:      node.OwnerActor,
		RuntimeProvider: nodeRuntimeProviderOrLocal(node.RuntimeProvider),
		Labels:          cloneStringSlice(node.Labels),
		Capabilities:    cloneStringSlice(node.Capabilities),
		ToolInventory:   cloneStringSlice(node.ToolInventory),
		Version:         node.Version,
		Capacity:        node.Capacity,
		DrainState:      nodeDrainStateOrActive(node.DrainState),
		TTL:             nodeTTL(node),
	})
	if err != nil {
		return fmt.Errorf("create node %s: %w", node.NodeID, err)
	}
	return syncNodeState(ctx, st, ws, created.NodeID, node)
}

func syncNodeState(ctx context.Context, st store.Store, ws, nodeID string, node NodeModule) error {
	ownerActor := node.OwnerActor
	runtimeProvider := nodeRuntimeProviderOrLocal(node.RuntimeProvider)
	labels := cloneStringSlice(node.Labels)
	capabilities := cloneStringSlice(node.Capabilities)
	toolInventory := cloneStringSlice(node.ToolInventory)
	version := node.Version
	capacity := node.Capacity
	drainState := nodeDrainStateOrActive(node.DrainState)
	patch := store.NodeUpdate{
		OwnerActor:      &ownerActor,
		RuntimeProvider: &runtimeProvider,
		Labels:          &labels,
		Capabilities:    &capabilities,
		ToolInventory:   &toolInventory,
		Version:         &version,
		Capacity:        &capacity,
		DrainState:      &drainState,
		LastHeartbeat:   cloneWorkflowRunTime(node.LastHeartbeat),
		ExpiresAt:       cloneWorkflowRunTime(node.ExpiresAt),
	}
	if _, err := st.Nodes().Update(ctx, ws, nodeID, patch); err != nil {
		return fmt.Errorf("update node %s: %w", nodeID, err)
	}
	return nil
}

func nodeTTL(node NodeModule) time.Duration {
	if node.ExpiresAt == nil {
		return 5 * time.Minute
	}
	base := time.Now().UTC()
	if node.LastHeartbeat != nil {
		base = node.LastHeartbeat.UTC()
	}
	if ttl := node.ExpiresAt.Sub(base); ttl > 0 {
		return ttl
	}
	return 5 * time.Minute
}

func nodeRuntimeProviderOrLocal(provider domain.RuntimeProvider) domain.RuntimeProvider {
	if provider == "" {
		return domain.RuntimeProviderLocal
	}
	return provider
}

func nodeDrainStateOrActive(state domain.NodeDrainState) domain.NodeDrainState {
	if state == "" {
		return domain.NodeDrainActive
	}
	return state
}
