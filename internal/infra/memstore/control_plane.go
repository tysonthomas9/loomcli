package memstore

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
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
	mu          sync.RWMutex
	items       map[string]map[string]*domain.AgentSession
	taskRuns    *taskRunStore
	lifecycleMu *sync.Mutex
}

func newAgentSessionStore(taskRuns *taskRunStore) *agentSessionStore {
	return &agentSessionStore{items: make(map[string]map[string]*domain.AgentSession), taskRuns: taskRuns, lifecycleMu: taskRuns.lifecycleMu}
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

func (s *agentSessionStore) Open(_ context.Context, run store.SessionRunContext, descriptor store.SessionDescriptor) (store.SessionRef, error) {
	if err := store.ValidateSessionDescriptor(descriptor); err != nil {
		return store.SessionRef{}, err
	}
	if run.WorkspaceKey == "" || run.TaskRunID == "" || run.Attempt <= 0 {
		return store.SessionRef{}, fmt.Errorf("session run context is incomplete: %w", domain.ErrInvalid)
	}
	descriptor = store.NormalizedSessionDescriptor(descriptor)
	s.lockLifecycle()
	defer s.unlockLifecycle()
	taskRun, err := s.taskRunForOpen(run)
	if err != nil {
		return store.SessionRef{}, err
	}
	if taskRun.Status.IsTerminal() {
		return store.SessionRef{}, lifecycleError(store.SessionLifecycleErrTaskRunTerminal, domain.ErrConflict)
	}
	if run.Attempt != store.TaskRunClaimAttempt(taskRun) {
		return store.SessionRef{}, lifecycleError(store.SessionLifecycleErrAttemptMismatch, domain.ErrConflict)
	}
	return s.openSessionLocked(run, descriptor)
}

func (s *agentSessionStore) taskRunForOpen(run store.SessionRunContext) (*domain.TaskRun, error) {
	if s.taskRuns == nil {
		return nil, fmt.Errorf("task run store unavailable: %w", domain.ErrNotFound)
	}
	s.taskRuns.mu.RLock()
	defer s.taskRuns.mu.RUnlock()
	taskRun, ok := s.taskRuns.items[run.WorkspaceKey][run.TaskRunID]
	if !ok {
		return nil, fmt.Errorf("task run %q: %w", run.TaskRunID, domain.ErrNotFound)
	}
	return cloneTaskRun(taskRun), nil
}

func (s *agentSessionStore) openSessionLocked(run store.SessionRunContext, descriptor store.SessionDescriptor) (store.SessionRef, error) {
	sessionID := store.SessionID(run.TaskRunID, run.Attempt, descriptor.InvocationKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.items[run.WorkspaceKey][sessionID]; existing != nil {
		if sessionDescriptorMatches(existing, descriptor) {
			return sessionRefMem(existing), nil
		}
		return store.SessionRef{}, lifecycleError(store.SessionLifecycleErrDescriptorConflict, domain.ErrConflict)
	}
	if s.items[run.WorkspaceKey] == nil {
		s.items[run.WorkspaceKey] = make(map[string]*domain.AgentSession)
	}
	now := time.Now().UTC()
	session := &domain.AgentSession{WorkspaceKey: run.WorkspaceKey, SessionID: sessionID, AgentID: descriptor.Backend, Kind: descriptor.Kind, TaskRunID: run.TaskRunID, InvocationKey: descriptor.InvocationKey, ParentSessionID: descriptor.ParentSessionID, Status: domain.AgentSessionRunning, Attempt: run.Attempt, Tags: descriptor.Tags, StartedAt: now, Metadata: storeOpenMetadata(run, descriptor), CreatedAt: now, UpdatedAt: now}
	s.items[run.WorkspaceKey][sessionID] = session
	return sessionRefMem(session), nil
}

func (s *agentSessionStore) Finalize(_ context.Context, ref store.SessionRef, outcome store.SessionOutcome) (*domain.AgentSession, error) {
	if err := store.ValidateSessionOutcome(outcome); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.items[ref.WorkspaceKey][ref.SessionID]
	if session == nil {
		return nil, fmt.Errorf("agent session %q: %w", ref.SessionID, domain.ErrNotFound)
	}
	if session.Status.IsTerminal() {
		if store.SessionOutcomeMatches(session, outcome) {
			return cloneAgentSession(session), nil
		}
		return nil, lifecycleError(store.SessionLifecycleErrOutcomeConflict, domain.ErrConflict)
	}
	store.ApplySessionOutcome(session, outcome, time.Now().UTC())
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
	if store.ProtectAgentSessionTerminalUpdate(session) && store.AgentSessionUpdateTouchesOutcome(patch) {
		if store.AgentSessionUpdateMatches(session, patch) {
			return cloneAgentSession(session), nil
		}
		return nil, lifecycleError(store.SessionLifecycleErrOutcomeConflict, domain.ErrConflict)
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
		session.Metadata = store.SessionMetadataUpdate(session.Metadata, *patch.Metadata)
	}
	session.UpdatedAt = time.Now().UTC()
	return cloneAgentSession(session), nil
}

func (s *agentSessionStore) lockLifecycle() {
	if s.lifecycleMu != nil {
		s.lifecycleMu.Lock()
	}
}

func (s *agentSessionStore) unlockLifecycle() {
	if s.lifecycleMu != nil {
		s.lifecycleMu.Unlock()
	}
}

func sessionDescriptorMatches(session *domain.AgentSession, descriptor store.SessionDescriptor) bool {
	if fingerprint := session.Metadata[store.SessionMetadataDescriptorFingerprint]; fingerprint != "" {
		return fingerprint == store.SessionDescriptorFingerprint(descriptor)
	}
	return session.AgentID == descriptor.Backend && session.Kind == descriptor.Kind && session.ParentSessionID == descriptor.ParentSessionID && slices.Equal(session.Tags, descriptor.Tags) && session.Metadata[store.SessionMetadataModel] == descriptor.Model && maps.Equal(leafSessionMetadata(session.Metadata), descriptor.Metadata)
}

func leafSessionMetadata(metadata map[string]string) map[string]string {
	clone := cloneMap(metadata)
	for _, key := range []string{store.SessionMetadataBackend, store.SessionMetadataModel, store.SessionMetadataFencingToken, store.SessionMetadataDriverRunID, store.SessionMetadataDriverStepID, store.SessionMetadataDriverRunnerSessionID, store.SessionMetadataTranscriptRef, store.SessionMetadataUsageTokens, store.SessionMetadataUsageCostUSD, store.SessionMetadataDescriptorFingerprint, store.SessionMetadataOutcomeFingerprint} {
		delete(clone, key)
	}
	return clone
}

func storeOpenMetadata(run store.SessionRunContext, descriptor store.SessionDescriptor) map[string]string {
	metadata := cloneMap(descriptor.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata[store.SessionMetadataBackend] = descriptor.Backend
	metadata[store.SessionMetadataModel] = descriptor.Model
	metadata[store.SessionMetadataFencingToken] = strconv.FormatInt(run.FencingToken, 10)
	metadata[store.SessionMetadataDriverRunID] = run.DriverRunID
	metadata[store.SessionMetadataDriverStepID] = run.DriverStepID
	metadata[store.SessionMetadataDescriptorFingerprint] = store.SessionDescriptorFingerprint(descriptor)
	return metadata
}

func sessionRefMem(session *domain.AgentSession) store.SessionRef {
	return store.SessionRef{WorkspaceKey: session.WorkspaceKey, SessionID: session.SessionID, Attempt: session.Attempt}
}

func lifecycleError(code string, err error) error {
	return &store.SessionLifecycleError{Code: code, Err: err}
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
