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
	if patch.LastHeartbeat != nil {
		n.LastHeartbeat = *patch.LastHeartbeat
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

type agentSessionOperationStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.AgentSessionOperation
}

func newAgentSessionOperationStore() *agentSessionOperationStore {
	return &agentSessionOperationStore{items: make(map[string]map[string]*domain.AgentSessionOperation)}
}

var _ store.AgentSessionOperationStore = (*agentSessionOperationStore)(nil)

func (s *agentSessionOperationStore) Upsert(_ context.Context, in store.AgentSessionOperationUpsert) (*domain.AgentSessionOperation, error) {
	if in.WorkspaceKey == "" || in.OperationID == "" || in.SessionID == "" || in.AgentID == "" {
		return nil, fmt.Errorf("workspace_key + operation_id + session_id + agent_id required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.AgentSessionOperation)
	}
	now := time.Now().UTC()
	createdAt := now
	if existing := s.items[in.WorkspaceKey][in.OperationID]; existing != nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	}
	op := &domain.AgentSessionOperation{
		WorkspaceKey:      in.WorkspaceKey,
		OperationID:       in.OperationID,
		SessionID:         in.SessionID,
		AgentID:           in.AgentID,
		WorkflowRunID:     in.WorkflowRunID,
		TaskRunID:         in.TaskRunID,
		TaskID:            in.TaskID,
		Kind:              in.Kind,
		Status:            in.Status,
		Model:             in.Model,
		Provider:          in.Provider,
		ProviderModel:     in.ProviderModel,
		ProviderSessionID: in.ProviderSessionID,
		PromptHash:        in.PromptHash,
		Text:              in.Text,
		Input:             cloneRaw(in.Input),
		Result:            cloneRaw(in.Result),
		Usage:             cloneRaw(in.Usage),
		ToolCalls:         cloneRaw(in.ToolCalls),
		ErrorClass:        in.ErrorClass,
		ErrorMessage:      in.ErrorMessage,
		StartedAt:         in.StartedAt,
		CompletedAt:       clonePtr(in.CompletedAt),
		DurationMS:        in.DurationMS,
		Metadata:          cloneMap(in.Metadata),
		CreatedAt:         createdAt,
		UpdatedAt:         now,
	}
	if op.Status == "" {
		op.Status = domain.AgentSessionOperationAdmitted
	}
	s.items[in.WorkspaceKey][in.OperationID] = op
	return cloneAgentSessionOperation(op), nil
}

func (s *agentSessionOperationStore) Get(_ context.Context, ws, operationID string) (*domain.AgentSessionOperation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	op, ok := s.items[ws][operationID]
	if !ok {
		return nil, fmt.Errorf("agent session operation %q in workspace %q: %w", operationID, ws, domain.ErrNotFound)
	}
	return cloneAgentSessionOperation(op), nil
}

func (s *agentSessionOperationStore) List(_ context.Context, ws string, filter store.AgentSessionOperationFilter) ([]*domain.AgentSessionOperation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ops := s.items[ws]
	out := make([]*domain.AgentSessionOperation, 0, len(ops))
	for _, op := range ops {
		if !agentSessionOperationMatches(op, filter) {
			continue
		}
		out = append(out, cloneAgentSessionOperation(op))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *agentSessionOperationStore) Cancel(_ context.Context, ws, operationID string, in store.AgentSessionOperationCancel) (*domain.AgentSessionOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.items[ws][operationID]
	if !ok {
		return nil, fmt.Errorf("agent session operation %q in workspace %q: %w", operationID, ws, domain.ErrNotFound)
	}
	completedAt := in.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	op.Status = domain.AgentSessionOperationCancelled
	op.CompletedAt = &completedAt
	if !op.StartedAt.IsZero() && op.DurationMS == 0 {
		op.DurationMS = completedAt.Sub(op.StartedAt).Milliseconds()
	}
	if in.ErrorClass != "" {
		op.ErrorClass = in.ErrorClass
	} else if op.ErrorClass == "" {
		op.ErrorClass = "cancelled"
	}
	if in.ErrorMessage != "" {
		op.ErrorMessage = in.ErrorMessage
	} else if op.ErrorMessage == "" {
		op.ErrorMessage = "agent session operation cancelled"
	}
	for key, value := range in.Metadata {
		if op.Metadata == nil {
			op.Metadata = map[string]string{}
		}
		op.Metadata[key] = value
	}
	op.UpdatedAt = time.Now().UTC()
	return cloneAgentSessionOperation(op), nil
}

type agentSessionToolCallStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.AgentSessionToolCall
}

func newAgentSessionToolCallStore() *agentSessionToolCallStore {
	return &agentSessionToolCallStore{items: make(map[string]map[string]*domain.AgentSessionToolCall)}
}

var _ store.AgentSessionToolCallStore = (*agentSessionToolCallStore)(nil)

func (s *agentSessionToolCallStore) Upsert(_ context.Context, in store.AgentSessionToolCallUpsert) (*domain.AgentSessionToolCall, error) {
	if in.WorkspaceKey == "" || in.CallID == "" || in.OperationID == "" || in.SessionID == "" || in.AgentID == "" || in.Name == "" {
		return nil, fmt.Errorf("workspace_key + call_id + operation_id + session_id + agent_id + name required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.AgentSessionToolCall)
	}
	now := time.Now().UTC()
	createdAt := now
	if existing := s.items[in.WorkspaceKey][in.CallID]; existing != nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	}
	call := &domain.AgentSessionToolCall{
		WorkspaceKey:        in.WorkspaceKey,
		CallID:              in.CallID,
		ProviderCallID:      in.ProviderCallID,
		OperationID:         in.OperationID,
		SessionID:           in.SessionID,
		AgentID:             in.AgentID,
		WorkflowRunID:       in.WorkflowRunID,
		TaskRunID:           in.TaskRunID,
		TaskID:              in.TaskID,
		Name:                in.Name,
		Status:              in.Status,
		AuthorizationStatus: in.AuthorizationStatus,
		IdempotencyKey:      in.IdempotencyKey,
		ToolVersion:         in.ToolVersion,
		SourceHash:          in.SourceHash,
		Handler:             in.Handler,
		Runtime:             in.Runtime,
		Timeout:             in.Timeout,
		Cancellable:         in.Cancellable,
		ReadOnly:            in.ReadOnly,
		Redacted:            in.Redacted,
		Args:                cloneRaw(in.Args),
		Result:              cloneRaw(in.Result),
		ErrorClass:          in.ErrorClass,
		ErrorMessage:        in.ErrorMessage,
		StartedAt:           in.StartedAt,
		CompletedAt:         clonePtr(in.CompletedAt),
		DurationMS:          in.DurationMS,
		Metadata:            cloneMap(in.Metadata),
		CreatedAt:           createdAt,
		UpdatedAt:           now,
	}
	if call.Status == "" {
		call.Status = "completed"
	}
	s.items[in.WorkspaceKey][in.CallID] = call
	return cloneAgentSessionToolCall(call), nil
}

func (s *agentSessionToolCallStore) Get(_ context.Context, ws, callID string) (*domain.AgentSessionToolCall, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	call, ok := s.items[ws][callID]
	if !ok {
		return nil, fmt.Errorf("agent session tool call %q in workspace %q: %w", callID, ws, domain.ErrNotFound)
	}
	return cloneAgentSessionToolCall(call), nil
}

func (s *agentSessionToolCallStore) List(_ context.Context, ws string, filter store.AgentSessionToolCallFilter) ([]*domain.AgentSessionToolCall, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	calls := s.items[ws]
	out := make([]*domain.AgentSessionToolCall, 0, len(calls))
	for _, call := range calls {
		if !agentSessionToolCallMatches(call, filter) {
			continue
		}
		out = append(out, cloneAgentSessionToolCall(call))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
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
	out.Metadata = cloneMap(s.Metadata)
	return &out
}

func cloneAgentSessionOperation(op *domain.AgentSessionOperation) *domain.AgentSessionOperation {
	out := *op
	out.Input = cloneRaw(op.Input)
	out.Result = cloneRaw(op.Result)
	out.Usage = cloneRaw(op.Usage)
	out.ToolCalls = cloneRaw(op.ToolCalls)
	out.CompletedAt = clonePtr(op.CompletedAt)
	out.Metadata = cloneMap(op.Metadata)
	return &out
}

func cloneAgentSessionToolCall(call *domain.AgentSessionToolCall) *domain.AgentSessionToolCall {
	out := *call
	out.Args = cloneRaw(call.Args)
	out.Result = cloneRaw(call.Result)
	out.CompletedAt = clonePtr(call.CompletedAt)
	out.Metadata = cloneMap(call.Metadata)
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

func agentSessionOperationMatches(op *domain.AgentSessionOperation, filter store.AgentSessionOperationFilter) bool {
	if filter.SessionID != "" && op.SessionID != filter.SessionID {
		return false
	}
	if filter.AgentID != "" && op.AgentID != filter.AgentID {
		return false
	}
	if filter.WorkflowRunID != "" && op.WorkflowRunID != filter.WorkflowRunID {
		return false
	}
	if filter.TaskRunID != "" && op.TaskRunID != filter.TaskRunID {
		return false
	}
	if filter.TaskID != "" && op.TaskID != filter.TaskID {
		return false
	}
	if filter.Kind != "" && op.Kind != filter.Kind {
		return false
	}
	if filter.Status != "" && op.Status != filter.Status {
		return false
	}
	return true
}

func agentSessionToolCallMatches(call *domain.AgentSessionToolCall, filter store.AgentSessionToolCallFilter) bool {
	if filter.OperationID != "" && call.OperationID != filter.OperationID {
		return false
	}
	if filter.SessionID != "" && call.SessionID != filter.SessionID {
		return false
	}
	if filter.AgentID != "" && call.AgentID != filter.AgentID {
		return false
	}
	if filter.WorkflowRunID != "" && call.WorkflowRunID != filter.WorkflowRunID {
		return false
	}
	if filter.TaskRunID != "" && call.TaskRunID != filter.TaskRunID {
		return false
	}
	if filter.TaskID != "" && call.TaskID != filter.TaskID {
		return false
	}
	if filter.Name != "" && call.Name != filter.Name {
		return false
	}
	if filter.Status != "" && call.Status != filter.Status {
		return false
	}
	return true
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
