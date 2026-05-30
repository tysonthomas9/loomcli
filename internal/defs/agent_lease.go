package defs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func validateAgentLeaseModules(plan *Plan, seen map[string]string) error {
	for _, lease := range plan.AgentLeases {
		sourcePath := firstNonEmpty(lease.SourcePath, "agent_lease:"+lease.LeaseID)
		if strings.TrimSpace(lease.LeaseID) == "" {
			return fmt.Errorf("%s: agent lease id is required", sourcePath)
		}
		if strings.TrimSpace(lease.SessionID) == "" {
			return fmt.Errorf("%s: agent lease %q must declare a session_id", sourcePath, lease.LeaseID)
		}
		if prior := seen["agent-lease:"+lease.LeaseID]; prior != "" {
			return fmt.Errorf("duplicate agent lease %q in %s and %s", lease.LeaseID, prior, sourcePath)
		}
		seen["agent-lease:"+lease.LeaseID] = sourcePath
	}
	for _, lease := range plan.AgentOwnershipLeases {
		sourcePath := firstNonEmpty(lease.SourcePath, "agent_ownership_lease:"+lease.AgentID)
		if strings.TrimSpace(lease.AgentID) == "" {
			return fmt.Errorf("%s: agent ownership lease agent_id is required", sourcePath)
		}
		if strings.TrimSpace(lease.LeaseID) == "" {
			return fmt.Errorf("%s: agent ownership lease for %q must declare a lease_id", sourcePath, lease.AgentID)
		}
		if strings.TrimSpace(lease.OwnerID) == "" {
			return fmt.Errorf("%s: agent ownership lease for %q must declare an owner_id", sourcePath, lease.AgentID)
		}
		if prior := seen["agent-ownership-lease:"+lease.AgentID]; prior != "" {
			return fmt.Errorf("duplicate agent ownership lease for %q in %s and %s", lease.AgentID, prior, sourcePath)
		}
		seen["agent-ownership-lease:"+lease.AgentID] = sourcePath
	}
	return nil
}

type AgentLeaseModule struct {
	LeaseID       string                  `json:"lease_id"`
	SessionID     string                  `json:"session_id"`
	SourcePath    string                  `json:"source_path"`
	SourceHash    string                  `json:"source_hash"`
	Version       string                  `json:"version"`
	AgentID       string                  `json:"agent_id,omitempty"`
	NodeID        string                  `json:"node_id,omitempty"`
	Token         string                  `json:"token,omitempty"`
	FencingToken  int64                   `json:"fencing_token,omitempty"`
	Status        domain.AgentLeaseStatus `json:"status,omitempty"`
	ExpiresAt     *time.Time              `json:"expires_at,omitempty"`
	LastHeartbeat *time.Time              `json:"last_heartbeat,omitempty"`
	CreatedAt     *time.Time              `json:"created_at,omitempty"`
	UpdatedAt     *time.Time              `json:"updated_at,omitempty"`
}

type AgentOwnershipLeaseModule struct {
	AgentID         string                  `json:"agent_id"`
	LeaseID         string                  `json:"lease_id"`
	OwnerID         string                  `json:"owner_id"`
	SourcePath      string                  `json:"source_path"`
	SourceHash      string                  `json:"source_hash"`
	Version         string                  `json:"version"`
	RuntimeProvider domain.RuntimeProvider  `json:"runtime_provider,omitempty"`
	NodeID          string                  `json:"node_id,omitempty"`
	Token           string                  `json:"token,omitempty"`
	FencingToken    int64                   `json:"fencing_token,omitempty"`
	Status          domain.AgentLeaseStatus `json:"status,omitempty"`
	ExpiresAt       *time.Time              `json:"expires_at,omitempty"`
	LastHeartbeat   *time.Time              `json:"last_heartbeat,omitempty"`
	CreatedAt       *time.Time              `json:"created_at,omitempty"`
	UpdatedAt       *time.Time              `json:"updated_at,omitempty"`
}

func applyAgentLeases(ctx context.Context, st store.Store, ws string, leases []AgentLeaseModule) error {
	if len(leases) == 0 {
		return nil
	}
	if st.AgentLeases() == nil {
		return fmt.Errorf("agent lease store not configured")
	}
	for _, lease := range leases {
		if err := applyAgentLease(ctx, st, ws, lease); err != nil {
			return err
		}
	}
	return nil
}

func applyAgentLease(ctx context.Context, st store.Store, ws string, lease AgentLeaseModule) error {
	existing, err := st.AgentLeases().Get(ctx, ws, lease.LeaseID)
	if err == nil {
		return syncAgentLeaseState(ctx, st, ws, existing, lease)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("get agent lease %s: %w", lease.LeaseID, err)
	}
	created, err := st.AgentLeases().Create(ctx, store.AgentLeaseCreate{
		WorkspaceKey:  ws,
		SessionID:     lease.SessionID,
		LeaseID:       lease.LeaseID,
		AgentID:       lease.AgentID,
		NodeID:        lease.NodeID,
		Token:         lease.Token,
		FencingToken:  lease.FencingToken,
		Status:        agentLeaseStatusOrActive(lease.Status),
		ExpiresAt:     timeValue(lease.ExpiresAt),
		LastHeartbeat: timeValue(lease.LastHeartbeat),
		CreatedAt:     timeValue(lease.CreatedAt),
		UpdatedAt:     timeValue(lease.UpdatedAt),
		TTL:           leaseTTL(lease.ExpiresAt),
	})
	if err != nil {
		return fmt.Errorf("create agent lease %s: %w", lease.LeaseID, err)
	}
	return syncAgentLeaseState(ctx, st, ws, created, lease)
}

func syncAgentLeaseState(ctx context.Context, st store.Store, ws string, existing *domain.AgentLease, lease AgentLeaseModule) error {
	if existing == nil {
		return nil
	}
	if lease.Status == domain.AgentLeaseReleased && existing.Status != domain.AgentLeaseReleased {
		if _, err := st.AgentLeases().Release(ctx, ws, existing.LeaseID, existing.Token); err != nil {
			return fmt.Errorf("release imported agent lease %s: %w", existing.LeaseID, err)
		}
	}
	return nil
}

func applyAgentOwnershipLeases(ctx context.Context, st store.Store, ws string, leases []AgentOwnershipLeaseModule) error {
	if len(leases) == 0 {
		return nil
	}
	if st.AgentOwnershipLeases() == nil {
		return fmt.Errorf("agent ownership lease store not configured")
	}
	for _, lease := range leases {
		if err := applyAgentOwnershipLease(ctx, st, ws, lease); err != nil {
			return err
		}
	}
	return nil
}

func applyAgentOwnershipLease(ctx context.Context, st store.Store, ws string, lease AgentOwnershipLeaseModule) error {
	existing, err := st.AgentOwnershipLeases().Get(ctx, ws, lease.AgentID)
	if err == nil {
		return syncAgentOwnershipLeaseState(ctx, st, ws, existing, lease)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("get agent ownership lease %s: %w", lease.AgentID, err)
	}
	created, err := st.AgentOwnershipLeases().Acquire(ctx, store.AgentOwnershipLeaseAcquire{
		WorkspaceKey:    ws,
		AgentID:         lease.AgentID,
		LeaseID:         lease.LeaseID,
		OwnerID:         lease.OwnerID,
		RuntimeProvider: lease.RuntimeProvider,
		NodeID:          lease.NodeID,
		Token:           lease.Token,
		FencingToken:    lease.FencingToken,
		Status:          agentLeaseStatusOrActive(lease.Status),
		ExpiresAt:       timeValue(lease.ExpiresAt),
		LastHeartbeat:   timeValue(lease.LastHeartbeat),
		CreatedAt:       timeValue(lease.CreatedAt),
		UpdatedAt:       timeValue(lease.UpdatedAt),
		TTL:             leaseTTL(lease.ExpiresAt),
	})
	if err != nil {
		return fmt.Errorf("acquire agent ownership lease %s: %w", lease.AgentID, err)
	}
	return syncAgentOwnershipLeaseState(ctx, st, ws, created, lease)
}

func syncAgentOwnershipLeaseState(ctx context.Context, st store.Store, ws string, existing *domain.AgentOwnershipLease, lease AgentOwnershipLeaseModule) error {
	if existing == nil {
		return nil
	}
	if existing.LeaseID != lease.LeaseID {
		return fmt.Errorf("agent ownership lease for %s already exists as %s", lease.AgentID, existing.LeaseID)
	}
	if lease.Status == domain.AgentLeaseReleased && existing.Status != domain.AgentLeaseReleased {
		if _, err := st.AgentOwnershipLeases().Release(ctx, ws, existing.AgentID, existing.Token); err != nil {
			return fmt.Errorf("release imported agent ownership lease %s: %w", existing.AgentID, err)
		}
	}
	return nil
}

func agentLeaseStatusOrActive(status domain.AgentLeaseStatus) domain.AgentLeaseStatus {
	if status == "" {
		return domain.AgentLeaseActive
	}
	return status
}

func leaseTTL(expiresAt *time.Time) time.Duration {
	if expiresAt == nil || expiresAt.IsZero() {
		return 0
	}
	ttl := time.Until(*expiresAt)
	if ttl <= 0 {
		return time.Nanosecond
	}
	return ttl
}

func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
