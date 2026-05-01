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

type terminalSessionStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.TerminalSession
}

func newTerminalSessionStore() *terminalSessionStore {
	return &terminalSessionStore{items: make(map[string]map[string]*domain.TerminalSession)}
}

func (s *terminalSessionStore) Create(_ context.Context, in store.TerminalSessionCreate) (*domain.TerminalSession, error) {
	if in.WorkspaceKey == "" || in.TerminalID == "" {
		return nil, fmt.Errorf("workspace_key + terminal_id required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.TerminalSession)
	}
	now := time.Now().UTC()
	term := &domain.TerminalSession{WorkspaceKey: in.WorkspaceKey, TerminalID: in.TerminalID, AgentID: in.AgentID, SessionID: in.SessionID, NodeID: in.NodeID, TaskID: in.TaskID, Title: in.Title, Kind: in.Kind, Status: in.Status, PTYProvider: in.PTYProvider, StreamRef: in.StreamRef, TranscriptRef: in.TranscriptRef, AttachedClients: in.AttachedClients, Metadata: cloneMap(in.Metadata), StartedAt: now, CreatedAt: now, UpdatedAt: now}
	s.items[in.WorkspaceKey][in.TerminalID] = term
	return cloneTerminalSession(term), nil
}

func (s *terminalSessionStore) Get(_ context.Context, ws, terminalID string) (*domain.TerminalSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	term, ok := s.items[ws][terminalID]
	if !ok {
		return nil, fmt.Errorf("terminal session %q in workspace %q: %w", terminalID, ws, domain.ErrNotFound)
	}
	return cloneTerminalSession(term), nil
}

func (s *terminalSessionStore) List(_ context.Context, ws string, filter store.TerminalSessionFilter) ([]*domain.TerminalSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.TerminalSession, 0, len(s.items[ws]))
	for _, term := range s.items[ws] {
		if terminalMatches(term, filter) {
			out = append(out, cloneTerminalSession(term))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *terminalSessionStore) Update(_ context.Context, ws, terminalID string, patch store.TerminalSessionUpdate) (*domain.TerminalSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	term, ok := s.items[ws][terminalID]
	if !ok {
		return nil, fmt.Errorf("terminal session %q in workspace %q: %w", terminalID, ws, domain.ErrNotFound)
	}
	if patch.Status != nil {
		term.Status = *patch.Status
	}
	if patch.LastSeenAt != nil {
		term.LastSeenAt = *patch.LastSeenAt
	}
	if patch.EndedAt != nil {
		term.EndedAt = *patch.EndedAt
	}
	if patch.Metadata != nil {
		term.Metadata = cloneMap(*patch.Metadata)
	}
	if patch.AttachedClients != nil {
		term.AttachedClients = *patch.AttachedClients
	}
	term.UpdatedAt = time.Now().UTC()
	return cloneTerminalSession(term), nil
}

type artifactStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.Artifact
}

func newArtifactStore() *artifactStore {
	return &artifactStore{items: make(map[string]map[string]*domain.Artifact)}
}

func (s *artifactStore) Create(_ context.Context, in store.ArtifactCreate) (*domain.Artifact, error) {
	if in.WorkspaceKey == "" || in.ArtifactID == "" || in.Type == "" || in.URI == "" {
		return nil, fmt.Errorf("workspace_key + artifact_id + type + uri required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.Artifact)
	}
	now := time.Now().UTC()
	artifact := &domain.Artifact{WorkspaceKey: in.WorkspaceKey, ArtifactID: in.ArtifactID, AgentID: in.AgentID, SessionID: in.SessionID, TerminalID: in.TerminalID, TaskID: in.TaskID, Type: in.Type, URI: in.URI, Summary: in.Summary, MIMEType: in.MIMEType, SizeBytes: in.SizeBytes, Checksum: in.Checksum, Metadata: cloneMap(in.Metadata), CreatedAt: now, UpdatedAt: now}
	s.items[in.WorkspaceKey][in.ArtifactID] = artifact
	return cloneArtifact(artifact), nil
}

func (s *artifactStore) Get(_ context.Context, ws, artifactID string) (*domain.Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	artifact, ok := s.items[ws][artifactID]
	if !ok {
		return nil, fmt.Errorf("artifact %q in workspace %q: %w", artifactID, ws, domain.ErrNotFound)
	}
	return cloneArtifact(artifact), nil
}

func (s *artifactStore) List(_ context.Context, ws string, filter store.ArtifactFilter) ([]*domain.Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.Artifact, 0, len(s.items[ws]))
	for _, artifact := range s.items[ws] {
		if artifactMatchesMem(artifact, filter) {
			out = append(out, cloneArtifact(artifact))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *artifactStore) Update(_ context.Context, ws, artifactID string, patch store.ArtifactUpdate) (*domain.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact, ok := s.items[ws][artifactID]
	if !ok {
		return nil, fmt.Errorf("artifact %q in workspace %q: %w", artifactID, ws, domain.ErrNotFound)
	}
	if patch.Summary != nil {
		artifact.Summary = *patch.Summary
	}
	if patch.Metadata != nil {
		artifact.Metadata = cloneMap(*patch.Metadata)
	}
	if patch.URI != nil {
		artifact.URI = *patch.URI
	}
	artifact.UpdatedAt = time.Now().UTC()
	return cloneArtifact(artifact), nil
}

func cloneTerminalSession(t *domain.TerminalSession) *domain.TerminalSession {
	out := *t
	out.EndedAt = clonePtr(t.EndedAt)
	out.Metadata = cloneMap(t.Metadata)
	return &out
}

func cloneArtifact(a *domain.Artifact) *domain.Artifact {
	out := *a
	out.Metadata = cloneMap(a.Metadata)
	return &out
}

func terminalMatches(t *domain.TerminalSession, f store.TerminalSessionFilter) bool {
	return (f.AgentID == "" || t.AgentID == f.AgentID) && (f.SessionID == "" || t.SessionID == f.SessionID) && (f.NodeID == "" || t.NodeID == f.NodeID) && (f.TaskID == "" || t.TaskID == f.TaskID) && (f.Status == "" || t.Status == f.Status)
}

func artifactMatchesMem(a *domain.Artifact, f store.ArtifactFilter) bool {
	return (f.AgentID == "" || a.AgentID == f.AgentID) && (f.SessionID == "" || a.SessionID == f.SessionID) && (f.TerminalID == "" || a.TerminalID == f.TerminalID) && (f.TaskID == "" || a.TaskID == f.TaskID) && (f.Type == "" || a.Type == f.Type)
}

type agentLeaseStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.AgentLease
	next  int64
}

func newAgentLeaseStore() *agentLeaseStore {
	return &agentLeaseStore{items: make(map[string]map[string]*domain.AgentLease)}
}

func (s *agentLeaseStore) Create(_ context.Context, in store.AgentLeaseCreate) (*domain.AgentLease, error) {
	if in.WorkspaceKey == "" || in.SessionID == "" {
		return nil, fmt.Errorf("workspace_key + session_id required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.AgentLease)
	}
	s.next++
	now := time.Now().UTC()
	ttl := in.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	id := in.LeaseID
	if id == "" {
		id = fmt.Sprintf("lease-%d", s.next)
	}
	lease := &domain.AgentLease{WorkspaceKey: in.WorkspaceKey, LeaseID: id, SessionID: in.SessionID, AgentID: in.AgentID, NodeID: in.NodeID, Token: fmt.Sprintf("token-%d", s.next), FencingToken: s.next, Status: domain.AgentLeaseActive, ExpiresAt: now.Add(ttl), LastHeartbeat: now, CreatedAt: now, UpdatedAt: now}
	s.items[in.WorkspaceKey][id] = lease
	return cloneAgentLease(lease), nil
}

func (s *agentLeaseStore) Get(_ context.Context, ws, leaseID string) (*domain.AgentLease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lease, ok := s.items[ws][leaseID]
	if !ok {
		return nil, fmt.Errorf("agent lease %q in workspace %q: %w", leaseID, ws, domain.ErrNotFound)
	}
	return cloneAgentLease(lease), nil
}

func (s *agentLeaseStore) List(_ context.Context, ws string, filter store.AgentLeaseFilter) ([]*domain.AgentLease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.AgentLease, 0, len(s.items[ws]))
	for _, lease := range s.items[ws] {
		if leaseMatchesMem(lease, filter) {
			out = append(out, cloneAgentLease(lease))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *agentLeaseStore) Heartbeat(_ context.Context, ws, leaseID, token string, ttl time.Duration) (*domain.AgentLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.items[ws][leaseID]
	if !ok || lease.Token != token {
		return nil, fmt.Errorf("agent lease %q in workspace %q: %w", leaseID, ws, domain.ErrConflict)
	}
	now := time.Now().UTC()
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	lease.LastHeartbeat = now
	lease.ExpiresAt = now.Add(ttl)
	lease.UpdatedAt = now
	return cloneAgentLease(lease), nil
}

func (s *agentLeaseStore) Release(_ context.Context, ws, leaseID, token string) (*domain.AgentLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.items[ws][leaseID]
	if !ok || lease.Token != token {
		return nil, fmt.Errorf("agent lease %q in workspace %q: %w", leaseID, ws, domain.ErrConflict)
	}
	lease.Status = domain.AgentLeaseReleased
	lease.UpdatedAt = time.Now().UTC()
	return cloneAgentLease(lease), nil
}

func cloneAgentLease(l *domain.AgentLease) *domain.AgentLease {
	out := *l
	return &out
}

func leaseMatchesMem(l *domain.AgentLease, f store.AgentLeaseFilter) bool {
	return (f.SessionID == "" || l.SessionID == f.SessionID) && (f.AgentID == "" || l.AgentID == f.AgentID) && (f.NodeID == "" || l.NodeID == f.NodeID) && (f.Status == "" || l.Status == f.Status)
}

type agentCommandStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.AgentCommand
	next  int64
}

func newAgentCommandStore() *agentCommandStore {
	return &agentCommandStore{items: make(map[string]map[string]*domain.AgentCommand)}
}

func (s *agentCommandStore) Create(_ context.Context, in store.AgentCommandCreate) (*domain.AgentCommand, error) {
	if in.WorkspaceKey == "" || in.Type == "" {
		return nil, fmt.Errorf("workspace_key + type required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.AgentCommand)
	}
	s.next++
	now := time.Now().UTC()
	id := in.CommandID
	if id == "" {
		id = fmt.Sprintf("cmd-%d", s.next)
	}
	cmd := &domain.AgentCommand{WorkspaceKey: in.WorkspaceKey, CommandID: id, Cursor: s.next, TargetAgentID: in.TargetAgentID, TargetNodeID: in.TargetNodeID, SessionID: in.SessionID, Type: in.Type, Payload: cloneMap(in.Payload), Status: domain.AgentCommandQueued, CreatedAt: now, UpdatedAt: now}
	s.items[in.WorkspaceKey][id] = cmd
	return cloneAgentCommand(cmd), nil
}

func (s *agentCommandStore) Get(_ context.Context, ws, commandID string) (*domain.AgentCommand, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cmd, ok := s.items[ws][commandID]
	if !ok {
		return nil, fmt.Errorf("agent command %q in workspace %q: %w", commandID, ws, domain.ErrNotFound)
	}
	return cloneAgentCommand(cmd), nil
}

func (s *agentCommandStore) List(_ context.Context, ws string, filter store.AgentCommandFilter) ([]*domain.AgentCommand, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.AgentCommand, 0, len(s.items[ws]))
	for _, cmd := range s.items[ws] {
		if commandMatchesMem(cmd, filter) {
			out = append(out, cloneAgentCommand(cmd))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cursor < out[j].Cursor })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *agentCommandStore) Ack(_ context.Context, ws, commandID string) (*domain.AgentCommand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd, ok := s.items[ws][commandID]
	if !ok {
		return nil, fmt.Errorf("agent command %q in workspace %q: %w", commandID, ws, domain.ErrNotFound)
	}
	now := time.Now().UTC()
	cmd.Status = domain.AgentCommandAcked
	cmd.AckedAt = &now
	cmd.UpdatedAt = now
	return cloneAgentCommand(cmd), nil
}

func (s *agentCommandStore) Complete(_ context.Context, ws, commandID string, update store.AgentCommandComplete) (*domain.AgentCommand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd, ok := s.items[ws][commandID]
	if !ok {
		return nil, fmt.Errorf("agent command %q in workspace %q: %w", commandID, ws, domain.ErrNotFound)
	}
	if update.Status == "" {
		update.Status = domain.AgentCommandSucceeded
	}
	cmd.Status = update.Status
	cmd.Result = update.Result
	cmd.ErrorClass = update.ErrorClass
	cmd.UpdatedAt = time.Now().UTC()
	return cloneAgentCommand(cmd), nil
}

func cloneAgentCommand(c *domain.AgentCommand) *domain.AgentCommand {
	out := *c
	out.AckedAt = clonePtr(c.AckedAt)
	out.Payload = cloneMap(c.Payload)
	return &out
}

func commandMatchesMem(c *domain.AgentCommand, f store.AgentCommandFilter) bool {
	return (f.TargetAgentID == "" || c.TargetAgentID == f.TargetAgentID) && (f.TargetNodeID == "" || c.TargetNodeID == f.TargetNodeID) && (f.Status == "" || c.Status == f.Status) && (f.AfterCursor <= 0 || c.Cursor > f.AfterCursor)
}
