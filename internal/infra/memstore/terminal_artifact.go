package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type terminalSessionStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*interaction.TerminalRecord
}

func newTerminalSessionStore() *terminalSessionStore {
	return &terminalSessionStore{items: make(map[string]map[string]*interaction.TerminalRecord)}
}

func (s *terminalSessionStore) Create(_ context.Context, in interaction.TerminalSessionCreate) (*interaction.TerminalRecord, error) {
	if in.WorkspaceKey == "" || in.TerminalID == "" {
		return nil, fmt.Errorf("workspace_key + terminal_id required: %w", persistence.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*interaction.TerminalRecord)
	}
	now := time.Now().UTC()
	term := &interaction.TerminalRecord{WorkspaceKey: in.WorkspaceKey, TerminalID: in.TerminalID, AgentID: in.AgentID, SessionID: in.SessionID, NodeID: in.NodeID, TaskID: in.TaskID, Title: in.Title, Kind: in.Kind, Status: in.Status, PTYProvider: in.PTYProvider, StreamRef: in.StreamRef, TranscriptRef: in.TranscriptRef, AttachedClients: in.AttachedClients, Metadata: cloneMap(in.Metadata), StartedAt: now, CreatedAt: now, UpdatedAt: now}
	s.items[in.WorkspaceKey][in.TerminalID] = term
	return cloneTerminalSession(term), nil
}

func (s *terminalSessionStore) Get(_ context.Context, ws, terminalID string) (*interaction.TerminalRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	term, ok := s.items[ws][terminalID]
	if !ok {
		return nil, fmt.Errorf("terminal session %q in workspace %q: %w", terminalID, ws, persistence.ErrNotFound)
	}
	return cloneTerminalSession(term), nil
}

func (s *terminalSessionStore) List(_ context.Context, ws string, filter interaction.TerminalSessionFilter) ([]*interaction.TerminalRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*interaction.TerminalRecord, 0, len(s.items[ws]))
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

func (s *terminalSessionStore) Update(_ context.Context, ws, terminalID string, patch interaction.TerminalSessionUpdate) (*interaction.TerminalRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	term, ok := s.items[ws][terminalID]
	if !ok {
		return nil, fmt.Errorf("terminal session %q in workspace %q: %w", terminalID, ws, persistence.ErrNotFound)
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
	mu      sync.RWMutex
	items   map[string]map[string]*artifacts.Artifact
	content map[string]map[string][]byte
}

func newArtifactStore() *artifactStore {
	return &artifactStore{
		items:   make(map[string]map[string]*artifacts.Artifact),
		content: make(map[string]map[string][]byte),
	}
}

func cloneTerminalSession(t *interaction.TerminalRecord) *interaction.TerminalRecord {
	out := *t
	out.EndedAt = clonePtr(t.EndedAt)
	out.Metadata = cloneMap(t.Metadata)
	return &out
}

func cloneArtifact(a *artifacts.Artifact) *artifacts.Artifact {
	out := *a
	out.Metadata = cloneMap(a.Metadata)
	out.FinalizedAt = clonePtr(a.FinalizedAt)
	return &out
}

func terminalMatches(t *interaction.TerminalRecord, f interaction.TerminalSessionFilter) bool {
	return (f.AgentID == "" || t.AgentID == f.AgentID) && (f.SessionID == "" || t.SessionID == f.SessionID) && (f.NodeID == "" || t.NodeID == f.NodeID) && (f.TaskID == "" || t.TaskID == f.TaskID) && (f.Status == "" || t.Status == f.Status)
}

type agentLeaseStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*interaction.LeaseRecord
	next  int64
}

func newAgentLeaseStore() *agentLeaseStore {
	return &agentLeaseStore{items: make(map[string]map[string]*interaction.LeaseRecord)}
}

func (s *agentLeaseStore) Create(_ context.Context, in interaction.AgentLeaseCreate) (*interaction.LeaseRecord, error) {
	if in.WorkspaceKey == "" || in.SessionID == "" {
		return nil, fmt.Errorf("workspace_key + session_id required: %w", persistence.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*interaction.LeaseRecord)
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
	lease := &interaction.LeaseRecord{WorkspaceKey: in.WorkspaceKey, LeaseID: id, SessionID: in.SessionID, AgentID: in.AgentID, NodeID: in.NodeID, Token: fmt.Sprintf("token-%d", s.next), FencingToken: s.next, Status: interaction.LeaseRecordActive, ExpiresAt: now.Add(ttl), LastHeartbeat: now, CreatedAt: now, UpdatedAt: now}
	s.items[in.WorkspaceKey][id] = lease
	return cloneAgentLease(lease), nil
}

func (s *agentLeaseStore) Get(_ context.Context, ws, leaseID string) (*interaction.LeaseRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lease, ok := s.items[ws][leaseID]
	if !ok {
		return nil, fmt.Errorf("agent lease %q in workspace %q: %w", leaseID, ws, persistence.ErrNotFound)
	}
	return cloneAgentLease(lease), nil
}

func (s *agentLeaseStore) List(_ context.Context, ws string, filter interaction.AgentLeaseFilter) ([]*interaction.LeaseRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*interaction.LeaseRecord, 0, len(s.items[ws]))
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

func (s *agentLeaseStore) Heartbeat(_ context.Context, ws, leaseID, token string, ttl time.Duration) (*interaction.LeaseRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.items[ws][leaseID]
	if !ok || lease.Token != token {
		return nil, fmt.Errorf("agent lease %q in workspace %q: %w", leaseID, ws, persistence.ErrConflict)
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

func (s *agentLeaseStore) Release(_ context.Context, ws, leaseID, token string) (*interaction.LeaseRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.items[ws][leaseID]
	if !ok || lease.Token != token {
		return nil, fmt.Errorf("agent lease %q in workspace %q: %w", leaseID, ws, persistence.ErrConflict)
	}
	lease.Status = interaction.LeaseRecordReleased
	lease.UpdatedAt = time.Now().UTC()
	return cloneAgentLease(lease), nil
}

func cloneAgentLease(l *interaction.LeaseRecord) *interaction.LeaseRecord {
	out := *l
	return &out
}

func leaseMatchesMem(l *interaction.LeaseRecord, f interaction.AgentLeaseFilter) bool {
	return (f.SessionID == "" || l.SessionID == f.SessionID) && (f.AgentID == "" || l.AgentID == f.AgentID) && (f.NodeID == "" || l.NodeID == f.NodeID) && (f.Status == "" || l.Status == f.Status)
}

type agentInboxMessageStore struct {
	mu      sync.RWMutex
	items   map[string]map[string]*interaction.InboxRecord
	dedupe  map[string]map[string]string
	next    int64
	nowFunc func() time.Time
}

func newAgentInboxMessageStore() *agentInboxMessageStore {
	return &agentInboxMessageStore{
		items:   make(map[string]map[string]*interaction.InboxRecord),
		dedupe:  make(map[string]map[string]string),
		nowFunc: func() time.Time { return time.Now().UTC() },
	}
}

func (s *agentInboxMessageStore) Create(_ context.Context, in interaction.AgentInboxMessageCreate) (*interaction.InboxRecord, error) {
	if in.WorkspaceKey == "" || in.TargetAgentID == "" || in.Body == "" {
		return nil, fmt.Errorf("workspace_key + target_agent_id + body required: %w", persistence.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*interaction.InboxRecord)
	}
	if s.dedupe[in.WorkspaceKey] == nil {
		s.dedupe[in.WorkspaceKey] = make(map[string]string)
	}
	if in.DedupeKey != "" {
		if id := s.dedupe[in.WorkspaceKey][in.DedupeKey]; id != "" {
			return cloneAgentInboxMessage(s.items[in.WorkspaceKey][id]), nil
		}
	}
	s.next++
	now := s.nowFunc()
	id := in.InboxMessageID
	if id == "" {
		id = fmt.Sprintf("inbox-%d", s.next)
	}
	if _, ok := s.items[in.WorkspaceKey][id]; ok {
		return nil, fmt.Errorf("agent inbox message %q in workspace %q: %w", id, in.WorkspaceKey, persistence.ErrAlreadyExists)
	}
	msg := &interaction.InboxRecord{
		WorkspaceKey:      in.WorkspaceKey,
		InboxMessageID:    id,
		Cursor:            s.next,
		TargetAgentID:     in.TargetAgentID,
		SessionID:         in.SessionID,
		Body:              in.Body,
		Status:            interaction.InboxRecordQueued,
		SourceKind:        in.SourceKind,
		SourceRef:         in.SourceRef,
		DriverRunID:       in.DriverRunID,
		TaskRunID:         in.TaskRunID,
		TriggerEventID:    in.TriggerEventID,
		TriggerDeliveryID: in.TriggerDeliveryID,
		DedupeKey:         in.DedupeKey,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	s.items[in.WorkspaceKey][id] = msg
	if in.DedupeKey != "" {
		s.dedupe[in.WorkspaceKey][in.DedupeKey] = id
	}
	return cloneAgentInboxMessage(msg), nil
}

func (s *agentInboxMessageStore) Get(_ context.Context, ws, inboxMessageID string) (*interaction.InboxRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msg, ok := s.items[ws][inboxMessageID]
	if !ok {
		return nil, fmt.Errorf("agent inbox message %q in workspace %q: %w", inboxMessageID, ws, persistence.ErrNotFound)
	}
	return cloneAgentInboxMessage(msg), nil
}

func (s *agentInboxMessageStore) List(_ context.Context, ws string, filter interaction.AgentInboxMessageFilter) ([]*interaction.InboxRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.nowFunc()
	out := make([]*interaction.InboxRecord, 0, len(s.items[ws]))
	for _, msg := range s.items[ws] {
		if agentInboxMessageMatchesMem(msg, filter, now) {
			out = append(out, cloneAgentInboxMessage(msg))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cursor < out[j].Cursor })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *agentInboxMessageStore) ClaimNext(_ context.Context, in interaction.AgentInboxMessageClaim) (*interaction.InboxRecord, error) {
	if in.WorkspaceKey == "" || in.TargetAgentID == "" || in.ClaimedBy == "" {
		return nil, fmt.Errorf("workspace_key + target_agent_id + claimed_by required: %w", persistence.ErrInvalid)
	}
	ttl := in.LeaseTTL
	if ttl <= 0 {
		ttl = time.Minute
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowFunc()
	var selected *interaction.InboxRecord
	for _, msg := range s.items[in.WorkspaceKey] {
		if msg.Status != interaction.InboxRecordQueued || msg.TargetAgentID != in.TargetAgentID {
			continue
		}
		if in.SessionID != "" && msg.SessionID != "" && msg.SessionID != in.SessionID {
			continue
		}
		if msg.ClaimedBy != "" && msg.ClaimExpiresAt != nil && msg.ClaimExpiresAt.After(now) {
			continue
		}
		if selected == nil || msg.Cursor < selected.Cursor {
			selected = msg
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("agent inbox message in workspace %q: %w", in.WorkspaceKey, persistence.ErrNotFound)
	}
	expires := now.Add(ttl)
	selected.ClaimedBy = in.ClaimedBy
	selected.ClaimExpiresAt = &expires
	selected.Attempt++
	if selected.SessionID == "" {
		selected.SessionID = in.SessionID
	}
	selected.UpdatedAt = now
	return cloneAgentInboxMessage(selected), nil
}

func (s *agentInboxMessageStore) Complete(_ context.Context, ws, inboxMessageID string, update interaction.AgentInboxMessageComplete) (*interaction.InboxRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, ok := s.items[ws][inboxMessageID]
	if !ok {
		return nil, fmt.Errorf("agent inbox message %q in workspace %q: %w", inboxMessageID, ws, persistence.ErrNotFound)
	}
	now := s.nowFunc()
	switch update.Outcome {
	case "delivered":
		msg.Status = interaction.InboxRecordDelivered
		msg.DeliveredThreadID = update.DeliveredThreadID
		msg.DeliveredAt = &now
		msg.LastError = ""
		msg.ErrorClass = ""
	case "retry":
		msg.Status = interaction.InboxRecordQueued
		msg.LastError = update.Error
		msg.ErrorClass = update.ErrorClass
	case "failed":
		msg.Status = interaction.InboxRecordFailed
		msg.LastError = update.Error
		msg.ErrorClass = update.ErrorClass
	default:
		return nil, fmt.Errorf("agent inbox complete outcome %q: %w", update.Outcome, persistence.ErrInvalid)
	}
	msg.ClaimedBy = ""
	msg.ClaimExpiresAt = nil
	msg.UpdatedAt = now
	return cloneAgentInboxMessage(msg), nil
}

func cloneAgentInboxMessage(m *interaction.InboxRecord) *interaction.InboxRecord {
	if m == nil {
		return nil
	}
	out := *m
	out.ClaimExpiresAt = clonePtr(m.ClaimExpiresAt)
	out.DeliveredAt = clonePtr(m.DeliveredAt)
	return &out
}

func agentInboxMessageMatchesMem(m *interaction.InboxRecord, f interaction.AgentInboxMessageFilter, now time.Time) bool {
	if f.TargetAgentID != "" && m.TargetAgentID != f.TargetAgentID {
		return false
	}
	if f.SessionID != "" && m.SessionID != f.SessionID {
		return false
	}
	if f.Status != "" && m.Status != f.Status {
		return false
	}
	if !agentInboxMessageMatchesRefsMem(m, f) {
		return false
	}
	if f.AfterCursor > 0 && m.Cursor <= f.AfterCursor {
		return false
	}
	if m.Status == interaction.InboxRecordQueued && m.ClaimedBy != "" && m.ClaimExpiresAt != nil && !m.ClaimExpiresAt.After(now) {
		return true
	}
	return true
}

func agentInboxMessageMatchesRefsMem(m *interaction.InboxRecord, f interaction.AgentInboxMessageFilter) bool {
	if f.SourceKind != "" && m.SourceKind != f.SourceKind {
		return false
	}
	if f.SourceRef != "" && m.SourceRef != f.SourceRef {
		return false
	}
	if f.DriverRunID != "" && m.DriverRunID != f.DriverRunID {
		return false
	}
	if f.TaskRunID != "" && m.TaskRunID != f.TaskRunID {
		return false
	}
	return true
}
