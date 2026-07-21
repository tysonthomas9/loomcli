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

type nodeStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.Node
}

func newNodeStore() *nodeStore {
	return &nodeStore{items: make(map[string]map[string]*domain.Node)}
}

var _ store.NodeStore = (*nodeStore)(nil)

func (s *nodeStore) Create(_ context.Context, in store.NodeCreate) (*domain.Node, error) {
	if in.WorkspaceKey == "" || in.NodeID == "" {
		return nil, fmt.Errorf("workspace_key + node_id required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.Node)
	}
	now := time.Now().UTC()
	ttl := in.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	n := &domain.Node{
		WorkspaceKey:    in.WorkspaceKey,
		NodeID:          in.NodeID,
		OwnerActor:      in.OwnerActor,
		RuntimeProvider: in.RuntimeProvider,
		Labels:          append([]string(nil), in.Labels...),
		Capabilities:    append([]string(nil), in.Capabilities...),
		ToolInventory:   append([]string(nil), in.ToolInventory...),
		Version:         in.Version,
		Capacity:        in.Capacity,
		DrainState:      in.DrainState,
		LastHeartbeat:   now,
		ExpiresAt:       now.Add(ttl),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.items[in.WorkspaceKey][in.NodeID] = n
	return cloneNode(n), nil
}

func (s *nodeStore) Get(_ context.Context, ws, nodeID string) (*domain.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.items[ws][nodeID]
	if !ok {
		return nil, fmt.Errorf("node %q in workspace %q: %w", nodeID, ws, domain.ErrNotFound)
	}
	return cloneNode(n), nil
}

func (s *nodeStore) List(_ context.Context, ws string) ([]*domain.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	nodes := s.items[ws]
	out := make([]*domain.Node, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, cloneNode(n))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, nil
}

func (s *nodeStore) Heartbeat(_ context.Context, ws, nodeID string, ttl time.Duration) (*domain.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.items[ws][nodeID]
	if !ok {
		return nil, fmt.Errorf("node %q in workspace %q: %w", nodeID, ws, domain.ErrNotFound)
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	now := time.Now().UTC()
	n.LastHeartbeat = now
	n.ExpiresAt = now.Add(ttl)
	n.UpdatedAt = now
	return cloneNode(n), nil
}

func (s *nodeStore) Update(_ context.Context, ws, nodeID string, patch store.NodeUpdate) (*domain.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.items[ws][nodeID]
	if !ok {
		return nil, fmt.Errorf("node %q in workspace %q: %w", nodeID, ws, domain.ErrNotFound)
	}
	if patch.OwnerActor != nil {
		n.OwnerActor = *patch.OwnerActor
	}
	if patch.RuntimeProvider != nil {
		n.RuntimeProvider = *patch.RuntimeProvider
	}
	if patch.Labels != nil {
		n.Labels = append([]string(nil), (*patch.Labels)...)
	}
	if patch.Capabilities != nil {
		n.Capabilities = append([]string(nil), (*patch.Capabilities)...)
	}
	if patch.ToolInventory != nil {
		n.ToolInventory = append([]string(nil), (*patch.ToolInventory)...)
	}
	if patch.Version != nil {
		n.Version = *patch.Version
	}
	if patch.Capacity != nil {
		n.Capacity = *patch.Capacity
	}
	if patch.DrainState != nil {
		n.DrainState = *patch.DrainState
	}
	if patch.ExpiresAt != nil {
		n.ExpiresAt = *patch.ExpiresAt
	}
	n.UpdatedAt = time.Now().UTC()
	return cloneNode(n), nil
}

type agentSessionStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.AgentSession
}

func newAgentSessionStore() *agentSessionStore {
	return &agentSessionStore{items: make(map[string]map[string]*domain.AgentSession)}
}

var _ store.AgentSessionStore = (*agentSessionStore)(nil)

func (s *agentSessionStore) Create(_ context.Context, in store.AgentSessionCreate) (*domain.AgentSession, error) {
	if in.WorkspaceKey == "" || in.SessionID == "" || in.AgentID == "" {
		return nil, fmt.Errorf("workspace_key + session_id + agent_id required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.AgentSession)
	}
	if _, ok := s.items[in.WorkspaceKey][in.SessionID]; ok {
		return nil, fmt.Errorf("agent session %q in workspace %q: %w", in.SessionID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	now := time.Now().UTC()
	session := &domain.AgentSession{
		WorkspaceKey:    in.WorkspaceKey,
		SessionID:       in.SessionID,
		AgentID:         in.AgentID,
		NodeID:          in.NodeID,
		Kind:            in.Kind,
		TaskID:          in.TaskID,
		TaskRunID:       in.TaskRunID,
		InvocationKey:   in.InvocationKey,
		TerminalID:      in.TerminalID,
		ParentSessionID: in.ParentSessionID,
		Status:          in.Status,
		Phase:           in.Phase,
		Attempt:         in.Attempt,
		Tags:            append([]string(nil), in.Tags...),
		StartedAt:       in.StartedAt,
		Metadata:        cloneMap(in.Metadata),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if session.StartedAt.IsZero() {
		session.StartedAt = now
	}
	s.items[in.WorkspaceKey][in.SessionID] = session
	return cloneAgentSession(session), nil
}

func (s *agentSessionStore) Get(_ context.Context, ws, sessionID string) (*domain.AgentSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.items[ws][sessionID]
	if !ok {
		return nil, fmt.Errorf("agent session %q in workspace %q: %w", sessionID, ws, domain.ErrNotFound)
	}
	return cloneAgentSession(session), nil
}

func (s *agentSessionStore) List(_ context.Context, ws string, filter store.AgentSessionFilter) ([]*domain.AgentSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sessions := s.items[ws]
	out := make([]*domain.AgentSession, 0, len(sessions))
	for _, session := range sessions {
		if !sessionMatches(session, filter) {
			continue
		}
		out = append(out, cloneAgentSession(session))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *agentSessionStore) ListPage(_ context.Context, ws string, filter store.AgentSessionFilter) ([]*domain.AgentSession, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sessions := s.items[ws]
	out := make([]*domain.AgentSession, 0, len(sessions))
	for _, session := range sessions {
		if !sessionMatchesPage(session, filter) {
			continue
		}
		out = append(out, cloneAgentSession(session))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	total := len(out)
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, total, nil
}

func (s *agentSessionStore) Heartbeat(ctx context.Context, ws, sessionID string) (*domain.AgentSession, error) {
	now := time.Now().UTC()
	return s.Update(ctx, ws, sessionID, store.AgentSessionUpdate{LastHeartbeat: &now})
}

func (s *agentSessionStore) Update(_ context.Context, ws, sessionID string, patch store.AgentSessionUpdate) (*domain.AgentSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.items[ws][sessionID]
	if !ok {
		return nil, fmt.Errorf("agent session %q in workspace %q: %w", sessionID, ws, domain.ErrNotFound)
	}
	if patch.NodeID != nil {
		session.NodeID = *patch.NodeID
	}
	if patch.TaskID != nil {
		session.TaskID = *patch.TaskID
	}
	if patch.Status != nil {
		session.Status = *patch.Status
	}
	if patch.Phase != nil {
		session.Phase = *patch.Phase
	}
	if patch.LastHeartbeat != nil {
		session.LastHeartbeat = *patch.LastHeartbeat
	}
	if patch.FinishedAt != nil {
		session.FinishedAt = *patch.FinishedAt
	}
	if patch.Summary != nil {
		session.Summary = *patch.Summary
	}
	if patch.ErrorClass != nil {
		session.ErrorClass = *patch.ErrorClass
	}
	if patch.ExitCode != nil {
		session.ExitCode = *patch.ExitCode
	}
	if patch.Metadata != nil {
		session.Metadata = cloneMap(*patch.Metadata)
	}
	session.UpdatedAt = time.Now().UTC()
	return cloneAgentSession(session), nil
}

func cloneNode(n *domain.Node) *domain.Node {
	out := *n
	out.Labels = append([]string(nil), n.Labels...)
	out.Capabilities = append([]string(nil), n.Capabilities...)
	out.ToolInventory = append([]string(nil), n.ToolInventory...)
	return &out
}

func cloneAgentSession(s *domain.AgentSession) *domain.AgentSession {
	out := *s
	out.FinishedAt = clonePtr(s.FinishedAt)
	out.ExitCode = clonePtr(s.ExitCode)
	out.Tags = append([]string(nil), s.Tags...)
	out.Metadata = cloneMap(s.Metadata)
	return &out
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sessionMatches(s *domain.AgentSession, filter store.AgentSessionFilter) bool {
	if filter.AgentID != "" && s.AgentID != filter.AgentID {
		return false
	}
	if filter.NodeID != "" && s.NodeID != filter.NodeID {
		return false
	}
	if filter.TaskID != "" && s.TaskID != filter.TaskID {
		return false
	}
	if filter.TaskRunID != "" && s.TaskRunID != filter.TaskRunID {
		return false
	}
	if filter.Status != "" && s.Status != filter.Status {
		return false
	}
	if filter.Attempt != nil && s.Attempt != *filter.Attempt {
		return false
	}
	if filter.NonTerminal && s.Status.IsTerminal() {
		return false
	}
	if filter.Kind != "" && s.Kind != filter.Kind {
		return false
	}
	if filter.ParentSessionID != "" && s.ParentSessionID != filter.ParentSessionID {
		return false
	}
	return true
}

func sessionMatchesPage(s *domain.AgentSession, filter store.AgentSessionFilter) bool {
	if !sessionMatches(s, filter) {
		return false
	}
	if filter.Since != nil && s.StartedAt.Before(*filter.Since) {
		return false
	}
	if filter.Until != nil && s.StartedAt.After(*filter.Until) {
		return false
	}
	return true
}
