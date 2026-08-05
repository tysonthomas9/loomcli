package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type agentOwnershipLeaseStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.AgentOwnershipLease
	next  int64
}

func newAgentOwnershipLeaseStore() *agentOwnershipLeaseStore {
	return &agentOwnershipLeaseStore{items: make(map[string]map[string]*domain.AgentOwnershipLease)}
}

func (s *agentOwnershipLeaseStore) Acquire(_ context.Context, in store.AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error) {
	if err := validateAgentOwnershipLeaseAcquire(in); err != nil {
		return nil, err
	}
	return s.acquireAgentOwnershipLease(in)
}

func validateAgentOwnershipLeaseAcquire(in store.AgentOwnershipLeaseAcquire) error {
	if in.WorkspaceKey == "" || in.AgentID == "" || in.OwnerID == "" {
		return fmt.Errorf("workspace_key + agent_id + owner_id required: %w", domain.ErrInvalid)
	}
	return nil
}

func (s *agentOwnershipLeaseStore) acquireAgentOwnershipLease(
	in store.AgentOwnershipLeaseAcquire,
) (*domain.AgentOwnershipLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.AgentOwnershipLease)
	}
	now := time.Now().UTC()
	if err := s.validateExistingAgentOwnershipLeaseLocked(in, now); err != nil {
		return nil, err
	}
	lease := s.newAgentOwnershipLeaseLocked(in, now)
	s.items[in.WorkspaceKey][in.AgentID] = lease
	return cloneAgentOwnershipLease(lease), nil
}

func (s *agentOwnershipLeaseStore) validateExistingAgentOwnershipLeaseLocked(
	in store.AgentOwnershipLeaseAcquire,
	now time.Time,
) error {
	existing := s.items[in.WorkspaceKey][in.AgentID]
	if existing == nil || existing.Status != domain.AgentLeaseActive || !existing.ExpiresAt.After(now) {
		return nil
	}
	if existing.OwnerID != in.OwnerID {
		return fmt.Errorf("agent ownership lease %q in workspace %q: %w", in.AgentID, in.WorkspaceKey, domain.ErrAlreadyClaimed)
	}
	return nil
}

func (s *agentOwnershipLeaseStore) newAgentOwnershipLeaseLocked(
	in store.AgentOwnershipLeaseAcquire,
	now time.Time,
) *domain.AgentOwnershipLease {
	s.next++
	ttl := in.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	id := in.LeaseID
	if id == "" {
		id = fmt.Sprintf("agent-owner-%d", s.next)
	}
	provider := in.RuntimeProvider
	if provider == "" {
		provider = domain.RuntimeProviderLocal
	}
	token := fmt.Sprintf("ownership-token-%d", s.next)
	return &domain.AgentOwnershipLease{
		WorkspaceKey:    in.WorkspaceKey,
		AgentID:         in.AgentID,
		LeaseID:         id,
		OwnerID:         in.OwnerID,
		RuntimeProvider: provider,
		NodeID:          in.NodeID,
		Token:           token,
		FencingToken:    s.next,
		Status:          domain.AgentLeaseActive,
		ExpiresAt:       now.Add(ttl),
		LastHeartbeat:   now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func (s *agentOwnershipLeaseStore) Get(_ context.Context, ws, agentID string) (*domain.AgentOwnershipLease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lease, ok := s.items[ws][agentID]
	if !ok {
		return nil, fmt.Errorf("agent ownership lease %q in workspace %q: %w", agentID, ws, domain.ErrNotFound)
	}
	return cloneAgentOwnershipLease(lease), nil
}

func (s *agentOwnershipLeaseStore) List(_ context.Context, ws string, filter store.AgentOwnershipLeaseFilter) ([]*domain.AgentOwnershipLease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.AgentOwnershipLease, 0, len(s.items[ws]))
	now := time.Now().UTC()
	for _, stored := range s.items[ws] {
		lease := cloneAgentOwnershipLease(stored)
		lease.Status = effectiveAgentOwnershipLeaseStatusMem(lease, now)
		if ownershipLeaseMatchesMem(lease, filter) {
			out = append(out, lease)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *agentOwnershipLeaseStore) Heartbeat(_ context.Context, ws, agentID, token string, ttl time.Duration) (*domain.AgentOwnershipLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.items[ws][agentID]
	if !ok || lease.Token != token || lease.Status != domain.AgentLeaseActive || !lease.ExpiresAt.After(time.Now().UTC()) {
		return nil, fmt.Errorf("agent ownership lease %q in workspace %q: %w", agentID, ws, domain.ErrConflict)
	}
	now := time.Now().UTC()
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	lease.LastHeartbeat = now
	lease.ExpiresAt = now.Add(ttl)
	lease.UpdatedAt = now
	return cloneAgentOwnershipLease(lease), nil
}

func (s *agentOwnershipLeaseStore) Release(_ context.Context, ws, agentID, token string) (*domain.AgentOwnershipLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.items[ws][agentID]
	if !ok || lease.Token != token {
		return nil, fmt.Errorf("agent ownership lease %q in workspace %q: %w", agentID, ws, domain.ErrConflict)
	}
	lease.Status = domain.AgentLeaseReleased
	lease.UpdatedAt = time.Now().UTC()
	return cloneAgentOwnershipLease(lease), nil
}

func cloneAgentOwnershipLease(l *domain.AgentOwnershipLease) *domain.AgentOwnershipLease {
	out := *l
	return &out
}

func ownershipLeaseMatchesMem(l *domain.AgentOwnershipLease, f store.AgentOwnershipLeaseFilter) bool {
	return (f.OwnerID == "" || l.OwnerID == f.OwnerID) && (f.NodeID == "" || l.NodeID == f.NodeID) && (f.RuntimeProvider == "" || l.RuntimeProvider == f.RuntimeProvider) && (f.Status == "" || l.Status == f.Status)
}

func effectiveAgentOwnershipLeaseStatusMem(
	lease *domain.AgentOwnershipLease,
	now time.Time,
) domain.AgentLeaseStatus {
	if lease != nil &&
		lease.Status == domain.AgentLeaseActive &&
		!lease.ExpiresAt.After(now) {
		return domain.AgentLeaseExpired
	}
	if lease == nil {
		return ""
	}
	return lease.Status
}
