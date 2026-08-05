package memstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
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
	mu      sync.RWMutex
	items   map[string]map[string]*domain.Artifact
	content map[string]map[string][]byte
}

func newArtifactStore() *artifactStore {
	return &artifactStore{
		items:   make(map[string]map[string]*domain.Artifact),
		content: make(map[string]map[string][]byte),
	}
}

func (s *artifactStore) Create(_ context.Context, in store.ArtifactCreate) (*domain.Artifact, error) {
	if in.WorkspaceKey == "" || in.ArtifactID == "" || in.Type == "" {
		return nil, fmt.Errorf("workspace_key + artifact_id + type required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.Artifact)
	}
	artifact := newArtifactMem(in, time.Now().UTC())
	s.items[in.WorkspaceKey][in.ArtifactID] = artifact
	return cloneArtifact(artifact), nil
}

func newArtifactMem(in store.ArtifactCreate, now time.Time) *domain.Artifact {
	artifact := &domain.Artifact{
		WorkspaceKey:    in.WorkspaceKey,
		ArtifactID:      in.ArtifactID,
		AgentID:         in.AgentID,
		SessionID:       in.SessionID,
		TerminalID:      in.TerminalID,
		TaskID:          in.TaskID,
		OwnerType:       in.OwnerType,
		OwnerID:         in.OwnerID,
		Type:            in.Type,
		URI:             in.URI,
		Summary:         in.Summary,
		MIMEType:        in.MIMEType,
		SizeBytes:       in.SizeBytes,
		Checksum:        in.Checksum,
		ContentHash:     in.ContentHash,
		Visibility:      in.Visibility,
		RedactionStatus: in.RedactionStatus,
		DurableStatus:   defaultArtifactDurableStatusMem(in),
		Metadata:        cloneMap(in.Metadata),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	normalizeArtifactHashesMem(artifact)
	if artifact.DurableStatus == "finalized" {
		artifact.FinalizedAt = &now
	}
	return artifact
}

func defaultArtifactDurableStatusMem(in store.ArtifactCreate) string {
	if in.DurableStatus != "" {
		return in.DurableStatus
	}
	if in.URI == "" {
		return "declared"
	}
	return "finalized"
}

func normalizeArtifactHashesMem(artifact *domain.Artifact) {
	if artifact.ContentHash == "" {
		artifact.ContentHash = artifact.Checksum
	}
	if artifact.Checksum == "" {
		artifact.Checksum = artifact.ContentHash
	}
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

func (s *artifactStore) UploadContent(ctx context.Context, ws, artifactID string, upload store.ArtifactContentUpload) (*domain.Artifact, error) {
	if upload.Body == nil {
		return nil, fmt.Errorf("artifact content body required: %w", domain.ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(upload.Body)
	if err != nil {
		return nil, fmt.Errorf("read artifact content: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	contentHash := "sha256:" + hex.EncodeToString(sum[:])

	s.mu.Lock()
	defer s.mu.Unlock()
	artifact, ok := s.items[ws][artifactID]
	if !ok {
		return nil, fmt.Errorf("artifact %q in workspace %q: %w", artifactID, ws, domain.ErrNotFound)
	}
	if artifact.DurableStatus == "finalized" {
		return nil, fmt.Errorf("artifact %q in workspace %q is finalized: %w", artifactID, ws, domain.ErrInvalidTransition)
	}
	if s.content[ws] == nil {
		s.content[ws] = make(map[string][]byte)
	}
	s.content[ws][artifactID] = append([]byte(nil), body...)

	artifact.URI = fmt.Sprintf("mem://artifacts/%s/%s/%s", ws, artifactID, contentHash)
	artifact.SizeBytes = int64(len(body))
	artifact.Checksum = contentHash
	artifact.ContentHash = contentHash
	if upload.MIMEType != "" {
		artifact.MIMEType = upload.MIMEType
	}
	artifact.DurableStatus = "uploading"
	artifact.UpdatedAt = time.Now().UTC()
	return cloneArtifact(artifact), nil
}

func (s *artifactStore) ReadContent(ctx context.Context, ws, artifactID string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.items[ws][artifactID]; !ok {
		return nil, fmt.Errorf("artifact %q in workspace %q: %w", artifactID, ws, domain.ErrNotFound)
	}
	body, ok := s.content[ws][artifactID]
	if !ok {
		return nil, fmt.Errorf("artifact %q content in workspace %q: %w", artifactID, ws, domain.ErrNotFound)
	}
	return append([]byte(nil), body...), nil
}

func (s *artifactStore) Finalize(ctx context.Context, ws, artifactID string, finalize store.ArtifactFinalize) (*domain.Artifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact, ok := s.items[ws][artifactID]
	if !ok {
		return nil, fmt.Errorf("artifact %q in workspace %q: %w", artifactID, ws, domain.ErrNotFound)
	}
	now := time.Now().UTC()
	candidate := cloneArtifact(artifact)
	applyArtifactFinalizeMem(candidate, finalize, now)
	if err := s.verifyFinalizedArtifactContentMem(ws, artifactID, candidate); err != nil {
		return nil, err
	}
	*artifact = *candidate
	return cloneArtifact(artifact), nil
}

func (s *artifactStore) Update(_ context.Context, ws, artifactID string, patch store.ArtifactUpdate) (*domain.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact, ok := s.items[ws][artifactID]
	if !ok {
		return nil, fmt.Errorf("artifact %q in workspace %q: %w", artifactID, ws, domain.ErrNotFound)
	}
	applyArtifactUpdateMem(artifact, patch, time.Now().UTC())
	return cloneArtifact(artifact), nil
}

func applyArtifactUpdateMem(artifact *domain.Artifact, patch store.ArtifactUpdate, now time.Time) {
	applyArtifactOwnershipUpdateMem(artifact, patch)
	applyArtifactContentUpdateMem(artifact, patch)
	applyArtifactLifecycleUpdateMem(artifact, patch, now)
}

func applyArtifactOwnershipUpdateMem(artifact *domain.Artifact, patch store.ArtifactUpdate) {
	if patch.AgentID != nil {
		artifact.AgentID = *patch.AgentID
	}
	if patch.SessionID != nil {
		artifact.SessionID = *patch.SessionID
	}
	if patch.TerminalID != nil {
		artifact.TerminalID = *patch.TerminalID
	}
	if patch.TaskID != nil {
		artifact.TaskID = *patch.TaskID
	}
	if patch.OwnerType != nil {
		artifact.OwnerType = *patch.OwnerType
	}
	if patch.OwnerID != nil {
		artifact.OwnerID = *patch.OwnerID
	}
	if patch.Type != nil {
		artifact.Type = *patch.Type
	}
}

func applyArtifactContentUpdateMem(artifact *domain.Artifact, patch store.ArtifactUpdate) {
	if patch.Summary != nil {
		artifact.Summary = *patch.Summary
	}
	if patch.Metadata != nil {
		artifact.Metadata = cloneMap(*patch.Metadata)
	}
	if patch.URI != nil {
		artifact.URI = *patch.URI
	}
	if patch.MIMEType != nil {
		artifact.MIMEType = *patch.MIMEType
	}
	if patch.SizeBytes != nil {
		artifact.SizeBytes = *patch.SizeBytes
	}
	if patch.Checksum != nil {
		artifact.Checksum = *patch.Checksum
	}
	if patch.ContentHash != nil {
		artifact.ContentHash = *patch.ContentHash
	}
}

func applyArtifactLifecycleUpdateMem(artifact *domain.Artifact, patch store.ArtifactUpdate, now time.Time) {
	if patch.Visibility != nil {
		artifact.Visibility = *patch.Visibility
	}
	if patch.RedactionStatus != nil {
		artifact.RedactionStatus = *patch.RedactionStatus
	}
	if patch.DurableStatus != nil {
		artifact.DurableStatus = *patch.DurableStatus
	}
	if patch.FinalizedAt != nil {
		finalizedAt := *patch.FinalizedAt
		artifact.FinalizedAt = &finalizedAt
	}
	artifact.UpdatedAt = now
	normalizeArtifactHashesMem(artifact)
	if artifact.DurableStatus == "finalized" && artifact.FinalizedAt == nil {
		finalizedAt := artifact.UpdatedAt
		artifact.FinalizedAt = &finalizedAt
	}
}

func applyArtifactFinalizeMem(artifact *domain.Artifact, finalize store.ArtifactFinalize, now time.Time) {
	if finalize.URI != nil {
		artifact.URI = *finalize.URI
	}
	if finalize.Summary != nil {
		artifact.Summary = *finalize.Summary
	}
	if finalize.MIMEType != nil {
		artifact.MIMEType = *finalize.MIMEType
	}
	if finalize.SizeBytes != nil {
		artifact.SizeBytes = *finalize.SizeBytes
	}
	if finalize.Checksum != nil {
		artifact.Checksum = *finalize.Checksum
	}
	if finalize.ContentHash != nil {
		artifact.ContentHash = *finalize.ContentHash
	}
	if finalize.Visibility != nil {
		artifact.Visibility = *finalize.Visibility
	}
	if finalize.RedactionStatus != nil {
		artifact.RedactionStatus = *finalize.RedactionStatus
	}
	if finalize.Metadata != nil {
		artifact.Metadata = cloneMap(*finalize.Metadata)
	}
	if artifact.ContentHash == "" {
		artifact.ContentHash = artifact.Checksum
	}
	if artifact.Checksum == "" {
		artifact.Checksum = artifact.ContentHash
	}
	artifact.DurableStatus = "finalized"
	finalizedAt := now
	artifact.FinalizedAt = &finalizedAt
	artifact.UpdatedAt = now
}

func (s *artifactStore) verifyFinalizedArtifactContentMem(ws, artifactID string, artifact *domain.Artifact) error {
	if strings.TrimSpace(artifact.URI) == "" {
		return fmt.Errorf("artifact %q in workspace %q requires uri before finalize: %w", artifactID, ws, domain.ErrInvalidTransition)
	}
	body, hasContent := s.content[ws][artifactID]
	if !hasContent {
		return nil
	}
	if artifact.SizeBytes != int64(len(body)) {
		return fmt.Errorf("artifact %q in workspace %q size mismatch: %w", artifactID, ws, domain.ErrInvalidTransition)
	}
	sum := sha256.Sum256(body)
	actual := "sha256:" + hex.EncodeToString(sum[:])
	if expected := strings.TrimSpace(firstNonEmptyArtifactMem(artifact.ContentHash, artifact.Checksum)); expected != "" && !strings.EqualFold(actual, expected) {
		return fmt.Errorf("artifact %q in workspace %q content hash mismatch: %w", artifactID, ws, domain.ErrInvalidTransition)
	}
	return nil
}

func firstNonEmptyArtifactMem(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
	out.FinalizedAt = clonePtr(a.FinalizedAt)
	return &out
}

func terminalMatches(t *domain.TerminalSession, f store.TerminalSessionFilter) bool {
	return (f.AgentID == "" || t.AgentID == f.AgentID) && (f.SessionID == "" || t.SessionID == f.SessionID) && (f.NodeID == "" || t.NodeID == f.NodeID) && (f.TaskID == "" || t.TaskID == f.TaskID) && (f.Status == "" || t.Status == f.Status)
}

func artifactMatchesMem(a *domain.Artifact, f store.ArtifactFilter) bool {
	return (f.AgentID == "" || a.AgentID == f.AgentID) && (f.SessionID == "" || a.SessionID == f.SessionID) && (f.TerminalID == "" || a.TerminalID == f.TerminalID) && (f.TaskID == "" || a.TaskID == f.TaskID) && (f.OwnerType == "" || a.OwnerType == f.OwnerType) && (f.OwnerID == "" || a.OwnerID == f.OwnerID) && (f.Type == "" || a.Type == f.Type) && (f.Status == "" || a.DurableStatus == f.Status)
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

type agentInboxMessageStore struct {
	mu      sync.RWMutex
	items   map[string]map[string]*domain.AgentInboxMessage
	dedupe  map[string]map[string]string
	next    int64
	nowFunc func() time.Time
}

func newAgentInboxMessageStore() *agentInboxMessageStore {
	return &agentInboxMessageStore{
		items:   make(map[string]map[string]*domain.AgentInboxMessage),
		dedupe:  make(map[string]map[string]string),
		nowFunc: func() time.Time { return time.Now().UTC() },
	}
}

func (s *agentInboxMessageStore) Create(_ context.Context, in store.AgentInboxMessageCreate) (*domain.AgentInboxMessage, error) {
	if in.WorkspaceKey == "" || in.TargetAgentID == "" || in.Body == "" {
		return nil, fmt.Errorf("workspace_key + target_agent_id + body required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.AgentInboxMessage)
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
		return nil, fmt.Errorf("agent inbox message %q in workspace %q: %w", id, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	msg := &domain.AgentInboxMessage{
		WorkspaceKey:      in.WorkspaceKey,
		InboxMessageID:    id,
		Cursor:            s.next,
		TargetAgentID:     in.TargetAgentID,
		SessionID:         in.SessionID,
		Body:              in.Body,
		Status:            domain.AgentInboxMessageQueued,
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

func (s *agentInboxMessageStore) Get(_ context.Context, ws, inboxMessageID string) (*domain.AgentInboxMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msg, ok := s.items[ws][inboxMessageID]
	if !ok {
		return nil, fmt.Errorf("agent inbox message %q in workspace %q: %w", inboxMessageID, ws, domain.ErrNotFound)
	}
	return cloneAgentInboxMessage(msg), nil
}

func (s *agentInboxMessageStore) List(_ context.Context, ws string, filter store.AgentInboxMessageFilter) ([]*domain.AgentInboxMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.nowFunc()
	out := make([]*domain.AgentInboxMessage, 0, len(s.items[ws]))
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

func (s *agentInboxMessageStore) ClaimNext(_ context.Context, in store.AgentInboxMessageClaim) (*domain.AgentInboxMessage, error) {
	if in.WorkspaceKey == "" || in.TargetAgentID == "" || in.ClaimedBy == "" {
		return nil, fmt.Errorf("workspace_key + target_agent_id + claimed_by required: %w", domain.ErrInvalid)
	}
	ttl := in.LeaseTTL
	if ttl <= 0 {
		ttl = time.Minute
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowFunc()
	var selected *domain.AgentInboxMessage
	for _, msg := range s.items[in.WorkspaceKey] {
		if msg.Status != domain.AgentInboxMessageQueued || msg.TargetAgentID != in.TargetAgentID {
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
		return nil, fmt.Errorf("agent inbox message in workspace %q: %w", in.WorkspaceKey, domain.ErrNotFound)
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

func (s *agentInboxMessageStore) Complete(_ context.Context, ws, inboxMessageID string, update store.AgentInboxMessageComplete) (*domain.AgentInboxMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, ok := s.items[ws][inboxMessageID]
	if !ok {
		return nil, fmt.Errorf("agent inbox message %q in workspace %q: %w", inboxMessageID, ws, domain.ErrNotFound)
	}
	now := s.nowFunc()
	switch update.Outcome {
	case "delivered":
		msg.Status = domain.AgentInboxMessageDelivered
		msg.DeliveredThreadID = update.DeliveredThreadID
		msg.DeliveredAt = &now
		msg.LastError = ""
		msg.ErrorClass = ""
	case "retry":
		msg.Status = domain.AgentInboxMessageQueued
		msg.LastError = update.Error
		msg.ErrorClass = update.ErrorClass
	case "failed":
		msg.Status = domain.AgentInboxMessageFailed
		msg.LastError = update.Error
		msg.ErrorClass = update.ErrorClass
	default:
		return nil, fmt.Errorf("agent inbox complete outcome %q: %w", update.Outcome, domain.ErrInvalid)
	}
	msg.ClaimedBy = ""
	msg.ClaimExpiresAt = nil
	msg.UpdatedAt = now
	return cloneAgentInboxMessage(msg), nil
}

func cloneAgentInboxMessage(m *domain.AgentInboxMessage) *domain.AgentInboxMessage {
	if m == nil {
		return nil
	}
	out := *m
	out.ClaimExpiresAt = clonePtr(m.ClaimExpiresAt)
	out.DeliveredAt = clonePtr(m.DeliveredAt)
	return &out
}

func agentInboxMessageMatchesMem(m *domain.AgentInboxMessage, f store.AgentInboxMessageFilter, now time.Time) bool {
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
	if m.Status == domain.AgentInboxMessageQueued && m.ClaimedBy != "" && m.ClaimExpiresAt != nil && !m.ClaimExpiresAt.After(now) {
		return true
	}
	return true
}

func agentInboxMessageMatchesRefsMem(m *domain.AgentInboxMessage, f store.AgentInboxMessageFilter) bool {
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
