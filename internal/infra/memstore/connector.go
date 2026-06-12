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

// connectorStore is the in-memory ConnectorStore. Get/List always return
// redacted copies (see domain.Connector.Redacted); the privileged Resolve*
// methods are the only read paths for inbound secrets and the sealed
// outbound credential ciphertext — the store never sees plaintext outbound
// credentials (serve's vault seals before the write).
type connectorStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.Connector // ws -> connectorID -> connector
}

func newConnectorStore() *connectorStore {
	return &connectorStore{items: make(map[string]map[string]*domain.Connector)}
}

var _ store.ConnectorStore = (*connectorStore)(nil)

// cloneConnector deep-copies a connector, including optional pointer fields
// and the sealed credential bytes, so callers can never mutate stored state.
func cloneConnector(c *domain.Connector) *domain.Connector {
	out := *c
	out.PreviousSecretValidUntil = clonePtr(c.PreviousSecretValidUntil)
	out.RotatedAt = clonePtr(c.RotatedAt)
	if c.OutboundCredentialSealed != nil {
		out.OutboundCredentialSealed = append([]byte(nil), c.OutboundCredentialSealed...)
	}
	return &out
}

// Create inserts a connector with first-writer-wins SetNX semantics and
// returns a redacted copy. Status defaults to active.
func (s *connectorStore) Create(_ context.Context, in store.ConnectorCreate) (*domain.Connector, error) {
	now := time.Now().UTC()
	status := in.Status
	if status == "" {
		status = domain.ConnectorStatusActive
	}
	conn := &domain.Connector{
		WorkspaceKey:        in.WorkspaceKey,
		ConnectorID:         in.ConnectorID,
		SourceKind:          in.SourceKind,
		DisplayName:         in.DisplayName,
		InboundEndpointPath: in.InboundEndpointPath,
		InboundSecret:       in.InboundSecret,
		Status:              status,
		CreatedBy:           in.CreatedBy,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if len(in.OutboundCredentialSealed) > 0 {
		conn.OutboundCredentialSealed = append([]byte(nil), in.OutboundCredentialSealed...)
	}
	if err := conn.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.Connector)
	}
	if _, ok := s.items[in.WorkspaceKey][in.ConnectorID]; ok {
		return nil, fmt.Errorf("connector %q in workspace %q: %w", in.ConnectorID, in.WorkspaceKey, domain.ErrConnectorExists)
	}
	s.items[in.WorkspaceKey][in.ConnectorID] = conn
	red := cloneConnector(conn).Redacted()
	return &red, nil
}

func (s *connectorStore) Get(_ context.Context, ws, connectorID string) (*domain.Connector, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	conn, ok := s.items[ws][connectorID]
	if !ok {
		return nil, fmt.Errorf("connector %q in workspace %q: %w", connectorID, ws, domain.ErrConnectorNotFound)
	}
	red := cloneConnector(conn).Redacted()
	return &red, nil
}

func (s *connectorStore) List(_ context.Context, ws string, filter store.ConnectorFilter) ([]*domain.Connector, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.Connector, 0, len(s.items[ws]))
	for _, conn := range s.items[ws] {
		if filter.SourceKind != "" && conn.SourceKind != filter.SourceKind {
			continue
		}
		if filter.Status != "" && conn.Status != filter.Status {
			continue
		}
		red := cloneConnector(conn).Redacted()
		out = append(out, &red)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ConnectorID < out[j].ConnectorID })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// ResolveInboundSecret is the privileged read path for webhook signature
// verification. Previous is only populated inside an unexpired rotation
// window; verifiers matching against it must emit a stale-secret audit
// signal.
func (s *connectorStore) ResolveInboundSecret(_ context.Context, ws, connectorID string) (*store.ConnectorInboundSecrets, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	conn, ok := s.items[ws][connectorID]
	if !ok {
		return nil, fmt.Errorf("connector %q in workspace %q: %w", connectorID, ws, domain.ErrConnectorNotFound)
	}
	out := &store.ConnectorInboundSecrets{Current: conn.InboundSecret}
	if conn.PreviousInboundSecret != "" && conn.PreviousSecretValidUntil != nil &&
		time.Now().UTC().Before(*conn.PreviousSecretValidUntil) {
		out.Previous = conn.PreviousInboundSecret
		out.PreviousValidUntil = *conn.PreviousSecretValidUntil
	}
	return out, nil
}

// ResolveOutboundCredentialSealed returns a copy of the sealed credential
// ciphertext (nil when none is configured) for serve's vault layer to
// unseal.
func (s *connectorStore) ResolveOutboundCredentialSealed(_ context.Context, ws, connectorID string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	conn, ok := s.items[ws][connectorID]
	if !ok {
		return nil, fmt.Errorf("connector %q in workspace %q: %w", connectorID, ws, domain.ErrConnectorNotFound)
	}
	if conn.OutboundCredentialSealed == nil {
		return nil, nil
	}
	return append([]byte(nil), conn.OutboundCredentialSealed...), nil
}

// RotateSecrets demotes the current inbound secret to PreviousInboundSecret
// for the dual-secret window (default now+15m, capped at now+24h) and
// installs the new one. A non-nil NewOutboundCredentialSealed replaces the
// sealed credential; nil leaves it in place. Returns a redacted copy.
func (s *connectorStore) RotateSecrets(_ context.Context, ws, connectorID string, in store.ConnectorSecretRotation) (*domain.Connector, error) {
	if in.NewInboundSecret == "" {
		return nil, fmt.Errorf("connector rotation new_inbound_secret required: %w", domain.ErrInvalid)
	}
	now := time.Now().UTC()
	validUntil := in.PreviousSecretValidUntil
	if validUntil.IsZero() {
		validUntil = now.Add(domain.DefaultConnectorSecretOverlap)
	}
	if maxUntil := now.Add(domain.MaxConnectorSecretOverlap); validUntil.After(maxUntil) {
		validUntil = maxUntil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	conn, ok := s.items[ws][connectorID]
	if !ok {
		return nil, fmt.Errorf("connector %q in workspace %q: %w", connectorID, ws, domain.ErrConnectorNotFound)
	}
	if conn.InboundSecret != "" {
		conn.PreviousInboundSecret = conn.InboundSecret
		until := validUntil
		conn.PreviousSecretValidUntil = &until
	}
	conn.InboundSecret = in.NewInboundSecret
	if in.NewOutboundCredentialSealed != nil {
		conn.OutboundCredentialSealed = append([]byte(nil), in.NewOutboundCredentialSealed...)
	}
	rotatedAt := now
	conn.RotatedAt = &rotatedAt
	conn.UpdatedAt = now
	red := cloneConnector(conn).Redacted()
	return &red, nil
}

// connectorGrantStore is the in-memory ConnectorGrantStore. Grants are
// deny-by-default join records; revoked grants are filtered from every list
// path so resolve-time callers only ever see active grants.
type connectorGrantStore struct {
	mu        sync.RWMutex
	items     map[string]map[string]*domain.ConnectorGrant // ws -> grantID -> grant
	byBinding map[string]map[string][]string               // ws -> bindingID -> grantIDs (insertion order)
}

func newConnectorGrantStore() *connectorGrantStore {
	return &connectorGrantStore{
		items:     make(map[string]map[string]*domain.ConnectorGrant),
		byBinding: make(map[string]map[string][]string),
	}
}

var _ store.ConnectorGrantStore = (*connectorGrantStore)(nil)

func cloneGrant(g *domain.ConnectorGrant) *domain.ConnectorGrant {
	out := *g
	out.RevokedAt = clonePtr(g.RevokedAt)
	return &out
}

// Create inserts a grant with SetNX semantics on (workspaceKey, grantID).
func (s *connectorGrantStore) Create(_ context.Context, in store.ConnectorGrantCreate) (*domain.ConnectorGrant, error) {
	grant := &domain.ConnectorGrant{
		WorkspaceKey:    in.WorkspaceKey,
		GrantID:         in.GrantID,
		ConnectorID:     in.ConnectorID,
		BindingID:       in.BindingID,
		Action:          in.Action,
		ResourcePattern: in.ResourcePattern,
		CreatedAt:       time.Now().UTC(),
	}
	if err := grant.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.ConnectorGrant)
		s.byBinding[in.WorkspaceKey] = make(map[string][]string)
	}
	if _, ok := s.items[in.WorkspaceKey][in.GrantID]; ok {
		return nil, fmt.Errorf("connector grant %q in workspace %q: %w", in.GrantID, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	s.items[in.WorkspaceKey][in.GrantID] = grant
	s.byBinding[in.WorkspaceKey][in.BindingID] = append(s.byBinding[in.WorkspaceKey][in.BindingID], in.GrantID)
	return cloneGrant(grant), nil
}

// Revoke marks the grant revoked. Revoking an already-revoked grant fails
// wrapping domain.ErrGrantRevoked.
func (s *connectorGrantStore) Revoke(_ context.Context, ws, grantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, ok := s.items[ws][grantID]
	if !ok {
		return fmt.Errorf("connector grant %q in workspace %q: %w", grantID, ws, domain.ErrNotFound)
	}
	if grant.Revoked() {
		return fmt.Errorf("connector grant %q in workspace %q: %w", grantID, ws, domain.ErrGrantRevoked)
	}
	now := time.Now().UTC()
	grant.RevokedAt = &now
	return nil
}

func (s *connectorGrantStore) ListByBinding(_ context.Context, ws, bindingID string) ([]*domain.ConnectorGrant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := s.byBinding[ws][bindingID]
	out := make([]*domain.ConnectorGrant, 0, len(ids))
	for _, id := range ids {
		grant := s.items[ws][id]
		if grant == nil || grant.Revoked() {
			continue
		}
		out = append(out, cloneGrant(grant))
	}
	return out, nil
}

func (s *connectorGrantStore) ListByConnector(_ context.Context, ws, connectorID string) ([]*domain.ConnectorGrant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.ConnectorGrant, 0)
	for _, grant := range s.items[ws] {
		if grant.ConnectorID != connectorID || grant.Revoked() {
			continue
		}
		out = append(out, cloneGrant(grant))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].GrantID < out[j].GrantID
	})
	return out, nil
}

// connectorAuditStore is the in-memory append-only connector-call journal
// (the ZSET analogue of fleet-db's dedicated journal, separate from
// TaskRunEvent). Append on a duplicate CallID fails wrapping
// domain.ErrAlreadyExists so retried writes stay idempotent.
type connectorAuditStore struct {
	mu       sync.RWMutex
	journals map[string][]*domain.ConnectorCallRecord // ws -> append-only journal
	byCallID map[string]map[string]struct{}           // ws -> seen CallIDs
}

func newConnectorAuditStore() *connectorAuditStore {
	return &connectorAuditStore{
		journals: make(map[string][]*domain.ConnectorCallRecord),
		byCallID: make(map[string]map[string]struct{}),
	}
}

var _ store.ConnectorAuditStore = (*connectorAuditStore)(nil)

func (s *connectorAuditStore) Append(_ context.Context, rec *domain.ConnectorCallRecord) error {
	if err := rec.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byCallID[rec.WorkspaceKey] == nil {
		s.byCallID[rec.WorkspaceKey] = make(map[string]struct{})
	}
	if _, ok := s.byCallID[rec.WorkspaceKey][rec.CallID]; ok {
		return fmt.Errorf("connector call %q in workspace %q: %w", rec.CallID, rec.WorkspaceKey, domain.ErrAlreadyExists)
	}
	stored := *rec
	s.journals[rec.WorkspaceKey] = append(s.journals[rec.WorkspaceKey], &stored)
	s.byCallID[rec.WorkspaceKey][rec.CallID] = struct{}{}
	return nil
}

func (s *connectorAuditStore) ListByRun(_ context.Context, ws, runID string, filter store.ConnectorCallFilter) ([]*domain.ConnectorCallRecord, error) {
	return s.list(ws, filter, func(rec *domain.ConnectorCallRecord) bool { return rec.RunID == runID }), nil
}

func (s *connectorAuditStore) ListByBinding(_ context.Context, ws, bindingID string, filter store.ConnectorCallFilter) ([]*domain.ConnectorCallRecord, error) {
	return s.list(ws, filter, func(rec *domain.ConnectorCallRecord) bool { return rec.BindingID == bindingID }), nil
}

// list returns matching records in journal-chronological order
// (OccurredAt, then Seq, then CallID for full determinism).
func (s *connectorAuditStore) list(ws string, filter store.ConnectorCallFilter, match func(*domain.ConnectorCallRecord) bool) []*domain.ConnectorCallRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.ConnectorCallRecord, 0)
	for _, rec := range s.journals[ws] {
		if !match(rec) {
			continue
		}
		if filter.Decision != "" && rec.Decision != filter.Decision {
			continue
		}
		clone := *rec
		out = append(out, &clone)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].OccurredAt.Equal(out[j].OccurredAt) {
			return out[i].OccurredAt.Before(out[j].OccurredAt)
		}
		if out[i].Seq != out[j].Seq {
			return out[i].Seq < out[j].Seq
		}
		return out[i].CallID < out[j].CallID
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out
}

// Connectors returns the ConnectorStore.
func (s *Store) Connectors() store.ConnectorStore { return s.conns }

// ConnectorGrants returns the ConnectorGrantStore.
func (s *Store) ConnectorGrants() store.ConnectorGrantStore { return s.grants }

// ConnectorCalls returns the ConnectorAuditStore.
func (s *Store) ConnectorCalls() store.ConnectorAuditStore { return s.audits }
