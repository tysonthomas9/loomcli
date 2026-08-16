package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type agentOwnershipLeaseStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*agents.OwnershipRecord
	next  int64
}

func newAgentOwnershipLeaseStore() *agentOwnershipLeaseStore {
	return &agentOwnershipLeaseStore{items: make(map[string]map[string]*agents.OwnershipRecord)}
}

func (s *agentOwnershipLeaseStore) Acquire(_ context.Context, in agents.AgentOwnershipLeaseAcquire) (*agents.OwnershipRecord, error) {
	if err := validateAgentOwnershipLeaseAcquire(in); err != nil {
		return nil, err
	}
	return s.acquireAgentOwnershipLease(in)
}

func validateAgentOwnershipLeaseAcquire(in agents.AgentOwnershipLeaseAcquire) error {
	if in.WorkspaceKey == "" || in.AgentID == "" || in.OwnerID == "" {
		return fmt.Errorf("workspace_key + agent_id + owner_id required: %w", persistence.ErrInvalid)
	}
	return nil
}

func (s *agentOwnershipLeaseStore) acquireAgentOwnershipLease(
	in agents.AgentOwnershipLeaseAcquire,
) (*agents.OwnershipRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*agents.OwnershipRecord)
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
	in agents.AgentOwnershipLeaseAcquire,
	now time.Time,
) error {
	existing := s.items[in.WorkspaceKey][in.AgentID]
	if existing == nil || existing.Status != agents.OwnershipActive || !existing.ExpiresAt.After(now) {
		return nil
	}
	if existing.OwnerID != in.OwnerID {
		return fmt.Errorf("agent ownership lease %q in workspace %q: %w", in.AgentID, in.WorkspaceKey, persistence.ErrAlreadyClaimed)
	}
	return nil
}

func (s *agentOwnershipLeaseStore) newAgentOwnershipLeaseLocked(
	in agents.AgentOwnershipLeaseAcquire,
	now time.Time,
) *agents.OwnershipRecord {
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
		provider = agents.RuntimeProviderLocal
	}
	token := fmt.Sprintf("ownership-token-%d", s.next)
	return &agents.OwnershipRecord{
		WorkspaceKey:    in.WorkspaceKey,
		AgentID:         in.AgentID,
		LeaseID:         id,
		OwnerID:         in.OwnerID,
		RuntimeProvider: provider,
		NodeID:          in.NodeID,
		Token:           token,
		FencingToken:    s.next,
		Status:          agents.OwnershipActive,
		ExpiresAt:       now.Add(ttl),
		LastHeartbeat:   now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func (s *agentOwnershipLeaseStore) Get(_ context.Context, ws, agentID string) (*agents.OwnershipRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lease, ok := s.items[ws][agentID]
	if !ok {
		return nil, fmt.Errorf("agent ownership lease %q in workspace %q: %w", agentID, ws, persistence.ErrNotFound)
	}
	return cloneAgentOwnershipLease(lease), nil
}

func (s *agentOwnershipLeaseStore) List(_ context.Context, ws string, filter agents.AgentOwnershipLeaseFilter) ([]*agents.OwnershipRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*agents.OwnershipRecord, 0, len(s.items[ws]))
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

func (s *agentOwnershipLeaseStore) Heartbeat(_ context.Context, ws, agentID, token string, ttl time.Duration) (*agents.OwnershipRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.items[ws][agentID]
	if !ok || lease.Token != token || lease.Status != agents.OwnershipActive || !lease.ExpiresAt.After(time.Now().UTC()) {
		return nil, fmt.Errorf("agent ownership lease %q in workspace %q: %w", agentID, ws, persistence.ErrConflict)
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

func (s *agentOwnershipLeaseStore) Release(_ context.Context, ws, agentID, token string) (*agents.OwnershipRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.items[ws][agentID]
	if !ok || lease.Token != token {
		return nil, fmt.Errorf("agent ownership lease %q in workspace %q: %w", agentID, ws, persistence.ErrConflict)
	}
	lease.Status = agents.OwnershipReleased
	lease.UpdatedAt = time.Now().UTC()
	return cloneAgentOwnershipLease(lease), nil
}

func cloneAgentOwnershipLease(l *agents.OwnershipRecord) *agents.OwnershipRecord {
	out := *l
	return &out
}

func ownershipLeaseMatchesMem(l *agents.OwnershipRecord, f agents.AgentOwnershipLeaseFilter) bool {
	return (f.OwnerID == "" || l.OwnerID == f.OwnerID) && (f.NodeID == "" || l.NodeID == f.NodeID) && (f.RuntimeProvider == "" || l.RuntimeProvider == f.RuntimeProvider) && (f.Status == "" || l.Status == f.Status)
}

func effectiveAgentOwnershipLeaseStatusMem(
	lease *agents.OwnershipRecord,
	now time.Time,
) agents.OwnershipStatus {
	if lease != nil &&
		lease.Status == agents.OwnershipActive &&
		!lease.ExpiresAt.After(now) {
		return agents.OwnershipExpired
	}
	if lease == nil {
		return ""
	}
	return lease.Status
}
