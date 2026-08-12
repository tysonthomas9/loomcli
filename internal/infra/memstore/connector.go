package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

// connectorStore is the in-memory Connectors definition store. Get/List
// return only the owner redacted projection; the privileged Resolve*
// methods are the only read paths for inbound secrets and the sealed
// outbound credential ciphertext — the store never sees plaintext outbound
// credentials (serve's vault seals before the write).
type connectorStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*connectorRecord // ws -> connectorID -> connector
}

type connectorRecord struct {
	connector                connectorsmodule.Connector
	inboundSecret            string
	previousInboundSecret    string
	outboundCredentialSealed []byte
}

func newConnectorStore() *connectorStore {
	return &connectorStore{items: make(map[string]map[string]*connectorRecord)}
}

// cloneConnector deep-copies a connector, including optional pointer fields
// and the sealed credential bytes, so callers can never mutate stored state.

func cloneConnector(record *connectorRecord) *connectorsmodule.Connector {
	out := record.connector
	out.PreviousSecretValidUntil = clonePtr(record.connector.PreviousSecretValidUntil)
	out.RotatedAt = clonePtr(record.connector.RotatedAt)
	return &out
}

// Create inserts a connector with first-writer-wins SetNX semantics and
// returns a redacted copy. Status defaults to active.
func (s *connectorStore) CreateConnectorRecord(_ context.Context, in connectorsmodule.CreateConnectorMutation) (*connectorsmodule.Connector, error) {
	now := time.Now().UTC()
	status := in.Status
	if status == "" {
		status = connectorsmodule.ConnectorStatusActive
	}
	connector := connectorsmodule.Connector{
		WorkspaceKey:        in.WorkspaceKey,
		ConnectorID:         in.ConnectorID,
		SourceKind:          in.SourceKind,
		DisplayName:         in.DisplayName,
		InboundEndpointPath: in.InboundEndpointPath,
		Status:              status,
		CreatedBy:           in.CreatedBy,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := connector.Validate(); err != nil {
		return nil, err
	}
	record := &connectorRecord{
		connector: connector, inboundSecret: in.InboundSecret,
		outboundCredentialSealed: append([]byte(nil), in.OutboundCredentialSealed...),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*connectorRecord)
	}
	if _, ok := s.items[in.WorkspaceKey][in.ConnectorID]; ok {
		return nil, fmt.Errorf("connector %q in workspace %q: %w", in.ConnectorID, in.WorkspaceKey, connectorsmodule.ErrAlreadyExists)
	}
	s.items[in.WorkspaceKey][in.ConnectorID] = record
	return cloneConnector(record), nil
}

func (s *connectorStore) GetConnectorRecord(_ context.Context, ws, connectorID string) (*connectorsmodule.Connector, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.items[ws][connectorID]
	if !ok {
		return nil, fmt.Errorf("connector %q in workspace %q: %w", connectorID, ws, connectorsmodule.ErrNotFound)
	}
	return cloneConnector(record), nil
}

func (s *connectorStore) ListConnectorRecords(_ context.Context, ws string, filter connectorsmodule.ConnectorFilter) ([]*connectorsmodule.Connector, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*connectorsmodule.Connector, 0, len(s.items[ws]))
	for _, record := range s.items[ws] {
		if filter.SourceKind != "" && record.connector.SourceKind != filter.SourceKind {
			continue
		}
		if filter.Status != "" && record.connector.Status != filter.Status {
			continue
		}
		out = append(out, cloneConnector(record))
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
func (s *connectorStore) ResolveInboundSecretsRecord(_ context.Context, ws, connectorID string) (*connectorsmodule.InboundSecrets, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.items[ws][connectorID]
	if !ok {
		return nil, fmt.Errorf("connector %q in workspace %q: %w", connectorID, ws, connectorsmodule.ErrNotFound)
	}
	out := &connectorsmodule.InboundSecrets{Current: record.inboundSecret}
	if record.previousInboundSecret != "" && record.connector.PreviousSecretValidUntil != nil &&
		time.Now().UTC().Before(*record.connector.PreviousSecretValidUntil) {
		out.Previous = record.previousInboundSecret
		out.PreviousValidUntil = *record.connector.PreviousSecretValidUntil
	}
	return out, nil
}

// ResolveOutboundCredentialSealed returns a copy of the sealed credential
// ciphertext (nil when none is configured) for serve's vault layer to
// unseal.
func (s *connectorStore) ResolveOutboundCredentialSealedRecord(_ context.Context, ws, connectorID string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.items[ws][connectorID]
	if !ok {
		return nil, fmt.Errorf("connector %q in workspace %q: %w", connectorID, ws, connectorsmodule.ErrNotFound)
	}
	if record.outboundCredentialSealed == nil {
		return nil, nil
	}
	return append([]byte(nil), record.outboundCredentialSealed...), nil
}

// RotateSecrets demotes the current inbound secret to PreviousInboundSecret
// for the dual-secret window (default now+15m, capped at now+24h) and
// installs the new one. A non-nil NewOutboundCredentialSealed replaces the
// sealed credential; nil leaves it in place. Returns a redacted copy.
func (s *connectorStore) RotateConnectorSecretsRecord(_ context.Context, ws, connectorID string, in connectorsmodule.RotateConnectorSecretsMutation) (*connectorsmodule.Connector, error) {
	if in.NewInboundSecret == "" {
		return nil, fmt.Errorf("connector rotation new_inbound_secret required: %w", connectorsmodule.ErrInvalid)
	}
	now := time.Now().UTC()
	validUntil := in.PreviousSecretValidUntil
	if validUntil.IsZero() {
		validUntil = now.Add(connectorsmodule.DefaultConnectorSecretOverlap)
	}
	if maxUntil := now.Add(connectorsmodule.MaxConnectorSecretOverlap); validUntil.After(maxUntil) {
		validUntil = maxUntil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.items[ws][connectorID]
	if !ok {
		return nil, fmt.Errorf("connector %q in workspace %q: %w", connectorID, ws, connectorsmodule.ErrNotFound)
	}
	if !in.ExpectedUpdatedAt.IsZero() && !record.connector.UpdatedAt.Equal(in.ExpectedUpdatedAt) {
		return nil, fmt.Errorf(
			"connector %q generation changed from %s to %s: %w",
			connectorID,
			in.ExpectedUpdatedAt.UTC().Format(time.RFC3339Nano),
			record.connector.UpdatedAt.UTC().Format(time.RFC3339Nano),
			connectorsmodule.ErrRotationConflict,
		)
	}
	if !now.After(record.connector.UpdatedAt) {
		now = record.connector.UpdatedAt.Add(time.Nanosecond)
	}
	if record.inboundSecret != "" {
		record.previousInboundSecret = record.inboundSecret
		until := validUntil
		record.connector.PreviousSecretValidUntil = &until
	}
	record.inboundSecret = in.NewInboundSecret
	if in.NewOutboundCredentialSealed != nil {
		record.outboundCredentialSealed = append([]byte(nil), in.NewOutboundCredentialSealed...)
	}
	rotatedAt := now
	record.connector.RotatedAt = &rotatedAt
	record.connector.UpdatedAt = now
	return cloneConnector(record), nil
}

// connectorGrantStore is the in-memory ConnectorGrantStore. Grants are
// deny-by-default join records; revoked grants are filtered from every list
// path so resolve-time callers only ever see active grants.
type connectorGrantStore struct {
	mu        sync.RWMutex
	items     map[string]map[string]*connectorsmodule.ConnectorGrant // ws -> grantID -> grant
	byBinding map[string]map[string][]string                         // ws -> bindingID -> grantIDs (insertion order)
}

func newConnectorGrantStore() *connectorGrantStore {
	return &connectorGrantStore{
		items:     make(map[string]map[string]*connectorsmodule.ConnectorGrant),
		byBinding: make(map[string]map[string][]string),
	}
}

func cloneGrant(g *connectorsmodule.ConnectorGrant) *connectorsmodule.ConnectorGrant {
	out := *g
	out.RevokedAt = clonePtr(g.RevokedAt)
	return &out
}

// Create inserts a grant with SetNX semantics on (workspaceKey, grantID).
func (s *connectorGrantStore) CreateManagementGrant(_ context.Context, in connectorsmodule.CreateGrantMutation) (*connectorsmodule.ConnectorGrant, error) {
	grant := &connectorsmodule.ConnectorGrant{
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
		s.items[in.WorkspaceKey] = make(map[string]*connectorsmodule.ConnectorGrant)
		s.byBinding[in.WorkspaceKey] = make(map[string][]string)
	}
	if _, ok := s.items[in.WorkspaceKey][in.GrantID]; ok {
		return nil, fmt.Errorf("connector grant %q in workspace %q: %w", in.GrantID, in.WorkspaceKey, connectorsmodule.ErrAlreadyExists)
	}
	s.items[in.WorkspaceKey][in.GrantID] = grant
	s.byBinding[in.WorkspaceKey][in.BindingID] = append(s.byBinding[in.WorkspaceKey][in.BindingID], in.GrantID)
	return cloneGrant(grant), nil
}

func (s *connectorGrantStore) CreateGrant(ctx context.Context, in connectorsmodule.CreateGrantMutation) (*connectorsmodule.ConnectorGrant, error) {
	return s.CreateManagementGrant(ctx, in)
}

// Revoke marks the grant revoked. Revoking an already-revoked grant fails
// wrapping connectors.ErrGrantRevoked.
func (s *connectorGrantStore) RevokeGrantRecord(_ context.Context, ws, grantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, ok := s.items[ws][grantID]
	if !ok {
		return fmt.Errorf("connector grant %q in workspace %q: %w", grantID, ws, connectorsmodule.ErrNotFound)
	}
	if grant.Revoked() {
		return fmt.Errorf("connector grant %q in workspace %q: %w", grantID, ws, connectorsmodule.ErrGrantRevoked)
	}
	now := time.Now().UTC()
	grant.RevokedAt = &now
	return nil
}

func (s *connectorGrantStore) ListGrantRecordsByBinding(_ context.Context, ws, bindingID string) ([]*connectorsmodule.ConnectorGrant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := s.byBinding[ws][bindingID]
	out := make([]*connectorsmodule.ConnectorGrant, 0, len(ids))
	for _, id := range ids {
		grant := s.items[ws][id]
		if grant == nil || grant.Revoked() {
			continue
		}
		out = append(out, cloneGrant(grant))
	}
	return out, nil
}

func (s *connectorGrantStore) ListGrantsByBinding(ctx context.Context, ws, bindingID string) ([]*connectorsmodule.ConnectorGrant, error) {
	return s.ListGrantRecordsByBinding(ctx, ws, bindingID)
}

func (s *connectorGrantStore) ListGrantRecordsByConnector(_ context.Context, ws, connectorID string) ([]*connectorsmodule.ConnectorGrant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*connectorsmodule.ConnectorGrant, 0)
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
// (the ZSET analog of fleet-db's dedicated journal, separate from
// TaskRunEvent). Append on a duplicate CallID fails wrapping
// domain.ErrAlreadyExists so retried writes stay idempotent.
type connectorAuditStore struct {
	mu       sync.RWMutex
	journals map[string][]*connectorsmodule.ConnectorCallRecord // ws -> append-only journal
	byCallID map[string]map[string]struct{}                     // ws -> seen CallIDs
}

func newConnectorAuditStore() *connectorAuditStore {
	return &connectorAuditStore{
		journals: make(map[string][]*connectorsmodule.ConnectorCallRecord),
		byCallID: make(map[string]map[string]struct{}),
	}
}

func (s *connectorAuditStore) AppendConnectorCallRecord(_ context.Context, rec *connectorsmodule.ConnectorCallRecord) error {
	if err := rec.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byCallID[rec.WorkspaceKey] == nil {
		s.byCallID[rec.WorkspaceKey] = make(map[string]struct{})
	}
	if _, ok := s.byCallID[rec.WorkspaceKey][rec.CallID]; ok {
		return fmt.Errorf("connector call %q in workspace %q: %w", rec.CallID, rec.WorkspaceKey, connectorsmodule.ErrAlreadyExists)
	}
	stored := *rec
	s.journals[rec.WorkspaceKey] = append(s.journals[rec.WorkspaceKey], &stored)
	s.byCallID[rec.WorkspaceKey][rec.CallID] = struct{}{}
	return nil
}

func (s *connectorAuditStore) ListCallRecordsByRun(_ context.Context, ws, runID string, filter connectorsmodule.ConnectorCallFilter) ([]*connectorsmodule.ConnectorCallRecord, error) {
	return s.list(ws, filter, func(rec *connectorsmodule.ConnectorCallRecord) bool { return rec.RunID == runID }), nil
}

func (s *connectorAuditStore) ListCallRecordsByBinding(_ context.Context, ws, bindingID string, filter connectorsmodule.ConnectorCallFilter) ([]*connectorsmodule.ConnectorCallRecord, error) {
	return s.list(ws, filter, func(rec *connectorsmodule.ConnectorCallRecord) bool { return rec.BindingID == bindingID }), nil
}

// list returns matching records in journal-chronological order
// (OccurredAt, then Seq, then CallID for full determinism).
func (s *connectorAuditStore) list(ws string, filter connectorsmodule.ConnectorCallFilter, match func(*connectorsmodule.ConnectorCallRecord) bool) []*connectorsmodule.ConnectorCallRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*connectorsmodule.ConnectorCallRecord, 0)
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

type connectorCatalog struct {
	*connectorStore
	*connectorGrantStore
	*connectorAuditStore
}

var _ connectorsmodule.ManagementStore = (*connectorCatalog)(nil)
var _ connectorsmodule.ConnectorGrantStore = (*connectorCatalog)(nil)

// Connectors returns the Connectors-owned persistence adapter.
func (s *Store) Connectors() connectorsmodule.ManagementStore {
	return &connectorCatalog{
		connectorStore: s.conns, connectorGrantStore: s.grants, connectorAuditStore: s.audits,
	}
}

func (s *Store) ConnectorGrants() connectorsmodule.ConnectorGrantStore {
	return &connectorCatalog{
		connectorStore: s.conns, connectorGrantStore: s.grants, connectorAuditStore: s.audits,
	}
}
