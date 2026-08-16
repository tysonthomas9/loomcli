package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type nodeStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*execution.WorkerNode
}

func newNodeStore() *nodeStore {
	return &nodeStore{items: make(map[string]map[string]*execution.WorkerNode)}
}

var _ execution.NodeStore = (*nodeStore)(nil)

func (s *nodeStore) Create(_ context.Context, in execution.NodeCreate) (*execution.WorkerNode, error) {
	if in.WorkspaceKey == "" || in.NodeID == "" {
		return nil, fmt.Errorf("workspace_key + node_id required: %w", persistence.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*execution.WorkerNode)
	}
	now := time.Now().UTC()
	ttl := in.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	n := &execution.WorkerNode{
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

func (s *nodeStore) Get(_ context.Context, ws, nodeID string) (*execution.WorkerNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.items[ws][nodeID]
	if !ok {
		return nil, fmt.Errorf("node %q in workspace %q: %w", nodeID, ws, persistence.ErrNotFound)
	}
	return cloneNode(n), nil
}

func (s *nodeStore) List(_ context.Context, ws string) ([]*execution.WorkerNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	nodes := s.items[ws]
	out := make([]*execution.WorkerNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, cloneNode(n))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, nil
}

func (s *nodeStore) Heartbeat(_ context.Context, ws, nodeID string, ttl time.Duration) (*execution.WorkerNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.items[ws][nodeID]
	if !ok {
		return nil, fmt.Errorf("node %q in workspace %q: %w", nodeID, ws, persistence.ErrNotFound)
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

func (s *nodeStore) Update(_ context.Context, ws, nodeID string, patch execution.NodeUpdate) (*execution.WorkerNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.items[ws][nodeID]
	if !ok {
		return nil, fmt.Errorf("node %q in workspace %q: %w", nodeID, ws, persistence.ErrNotFound)
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
	items map[string]map[string]*interaction.SessionRecord
}

func newAgentSessionStore() *agentSessionStore {
	return &agentSessionStore{items: make(map[string]map[string]*interaction.SessionRecord)}
}

var _ interaction.AgentSessionStore = (*agentSessionStore)(nil)

func (s *agentSessionStore) Create(_ context.Context, in interaction.AgentSessionCreate) (*interaction.SessionRecord, error) {
	if in.WorkspaceKey == "" || in.SessionID == "" || in.AgentID == "" {
		return nil, fmt.Errorf("workspace_key + session_id + agent_id required: %w", persistence.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*interaction.SessionRecord)
	}
	if _, ok := s.items[in.WorkspaceKey][in.SessionID]; ok {
		return nil, fmt.Errorf("agent session %q in workspace %q: %w", in.SessionID, in.WorkspaceKey, persistence.ErrAlreadyExists)
	}
	now := time.Now().UTC()
	session := &interaction.SessionRecord{
		WorkspaceKey:    in.WorkspaceKey,
		SessionID:       in.SessionID,
		AgentID:         in.AgentID,
		NodeID:          in.NodeID,
		Kind:            in.Kind,
		TaskID:          in.TaskID,
		TerminalID:      in.TerminalID,
		ParentSessionID: in.ParentSessionID,
		Status:          in.Status,
		Phase:           in.Phase,
		Attempt:         in.Attempt,
		Metadata:        cloneMap(in.Metadata),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.items[in.WorkspaceKey][in.SessionID] = session
	return cloneAgentSession(session), nil
}

func (s *agentSessionStore) Get(_ context.Context, ws, sessionID string) (*interaction.SessionRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.items[ws][sessionID]
	if !ok {
		return nil, fmt.Errorf("agent session %q in workspace %q: %w", sessionID, ws, persistence.ErrNotFound)
	}
	return cloneAgentSession(session), nil
}

func (s *agentSessionStore) List(_ context.Context, ws string, filter interaction.AgentSessionFilter) ([]*interaction.SessionRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sessions := s.items[ws]
	out := make([]*interaction.SessionRecord, 0, len(sessions))
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

func (s *agentSessionStore) Heartbeat(ctx context.Context, ws, sessionID string) (*interaction.SessionRecord, error) {
	now := time.Now().UTC()
	return s.Update(ctx, ws, sessionID, interaction.AgentSessionUpdate{LastHeartbeat: &now})
}

func (s *agentSessionStore) Update(_ context.Context, ws, sessionID string, patch interaction.AgentSessionUpdate) (*interaction.SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.items[ws][sessionID]
	if !ok {
		return nil, fmt.Errorf("agent session %q in workspace %q: %w", sessionID, ws, persistence.ErrNotFound)
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

func cloneNode(n *execution.WorkerNode) *execution.WorkerNode {
	out := *n
	out.Labels = append([]string(nil), n.Labels...)
	out.Capabilities = append([]string(nil), n.Capabilities...)
	out.ToolInventory = append([]string(nil), n.ToolInventory...)
	return &out
}

func cloneAgentSession(s *interaction.SessionRecord) *interaction.SessionRecord {
	out := *s
	out.FinishedAt = clonePtr(s.FinishedAt)
	out.ExitCode = clonePtr(s.ExitCode)
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

func sessionMatches(s *interaction.SessionRecord, filter interaction.AgentSessionFilter) bool {
	if filter.AgentID != "" && s.AgentID != filter.AgentID {
		return false
	}
	if filter.NodeID != "" && s.NodeID != filter.NodeID {
		return false
	}
	if filter.TaskID != "" && s.TaskID != filter.TaskID {
		return false
	}
	if filter.Status != "" && s.Status != filter.Status {
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
