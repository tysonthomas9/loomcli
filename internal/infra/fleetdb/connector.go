// connector.go implements store.ConnectorStore, store.ConnectorGrantStore,
// and store.ConnectorAuditStore against fleet-db's connector control-plane
// routes:
//
//	POST /api/v1/{ws}/connectors
//	GET  /api/v1/{ws}/connectors
//	GET  /api/v1/{ws}/connectors/{connector_id}
//	GET  /api/v1/{ws}/connectors/{connector_id}/secrets   (privileged)
//	POST /api/v1/{ws}/connectors/{connector_id}/rotate
//	POST /api/v1/{ws}/connector-grants
//	GET  /api/v1/{ws}/connector-grants
//	POST /api/v1/{ws}/connector-grants/{grant_id}/revoke
//	POST /api/v1/{ws}/connector-audit
//	GET  /api/v1/{ws}/connector-audit
//
// Casing note: fleet-db's /api/v1 surface is snake_case, so responses are
// decoded into local wire DTOs and converted — never directly into the
// domain structs (the connector enum VALUES — "github", "active",
// "granted" — are identical on both wires and pass through untranslated).
//
// Secrets note: fleet-db only ever returns inbound secrets and the sealed
// outbound credential ciphertext from the privileged /secrets route, which
// backs ResolveInboundSecret / ResolveOutboundCredentialSealed (the
// ResolveWebhookSecret pattern). Get/List decode fleet-db's already-redacted
// responses. Plaintext outbound credentials never transit this client:
// serve's vault layer seals BEFORE Create/RotateSecrets.
package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Connectors returns the ConnectorStore.
func (c *Client) Connectors() store.ConnectorStore { return c.connectors }

// ConnectorGrants returns the ConnectorGrantStore.
func (c *Client) ConnectorGrants() store.ConnectorGrantStore { return c.connectorGrants }

// ConnectorCalls returns the ConnectorAuditStore.
func (c *Client) ConnectorCalls() store.ConnectorAuditStore { return c.connectorCalls }

// --- sentinel remapping ---

// connectorSentinel layers the connector-specific CV1 sentinels on top of
// the generic ones classifyHTTPError already attached, so callers can match
// domain.ErrConnectorNotFound / domain.ErrConnectorExists (each of which
// also wraps its generic counterpart) exactly like against memstore.
func connectorSentinel(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return fmt.Errorf("%w: %w", domain.ErrConnectorNotFound, err)
	case errors.Is(err, domain.ErrAlreadyExists):
		return fmt.Errorf("%w: %w", domain.ErrConnectorExists, err)
	}
	return err
}

// grantRevokeSentinel maps fleet-db's 409 invalid_transition on a
// double-revoke (classified as domain.ErrInvalidTransition) to the CV1
// sentinel domain.ErrGrantRevoked, matching the memstore contract.
func grantRevokeSentinel(err error) error {
	if err != nil && errors.Is(err, domain.ErrInvalidTransition) {
		return fmt.Errorf("%w: %w", domain.ErrGrantRevoked, err)
	}
	return err
}

// --- wire DTOs (fleet-db snake_case responses) ---

// connectorWire mirrors fleet-db's models.Connector JSON shape. The sealed
// outbound credential is base64 on the wire ([]byte JSON encoding); on the
// redacted Get/List responses the secret fields are absent.
type connectorWire struct {
	WorkspaceKey             string     `json:"workspace_key"`
	ConnectorID              string     `json:"connector_id"`
	SourceKind               string     `json:"source_kind"`
	DisplayName              string     `json:"display_name"`
	InboundEndpointPath      string     `json:"inbound_endpoint_path"`
	InboundSecret            string     `json:"inbound_secret"`
	PreviousInboundSecret    string     `json:"previous_inbound_secret"`
	PreviousSecretValidUntil *time.Time `json:"previous_secret_valid_until"`
	OutboundCredentialSealed []byte     `json:"outbound_credential_sealed"`
	Status                   string     `json:"status"`
	CreatedBy                string     `json:"created_by"`
	CreatedAt                time.Time  `json:"created_at"`
	UpdatedAt                time.Time  `json:"updated_at"`
	RotatedAt                *time.Time `json:"rotated_at"`
}

func (w *connectorWire) toDomain() *domain.Connector {
	return &domain.Connector{
		WorkspaceKey:             w.WorkspaceKey,
		ConnectorID:              w.ConnectorID,
		SourceKind:               domain.ConnectorSourceKind(w.SourceKind),
		DisplayName:              w.DisplayName,
		InboundEndpointPath:      w.InboundEndpointPath,
		InboundSecret:            w.InboundSecret,
		PreviousInboundSecret:    w.PreviousInboundSecret,
		PreviousSecretValidUntil: w.PreviousSecretValidUntil,
		OutboundCredentialSealed: w.OutboundCredentialSealed,
		Status:                   domain.ConnectorStatus(w.Status),
		CreatedBy:                w.CreatedBy,
		CreatedAt:                w.CreatedAt,
		UpdatedAt:                w.UpdatedAt,
		RotatedAt:                w.RotatedAt,
	}
}

// connectorGrantWire mirrors fleet-db's models.ConnectorGrant JSON shape.
type connectorGrantWire struct {
	WorkspaceKey    string     `json:"workspace_key"`
	GrantID         string     `json:"grant_id"`
	ConnectorID     string     `json:"connector_id"`
	BindingID       string     `json:"binding_id"`
	Action          string     `json:"action"`
	ResourcePattern string     `json:"resource_pattern"`
	CreatedAt       time.Time  `json:"created_at"`
	RevokedAt       *time.Time `json:"revoked_at"`
}

func (w *connectorGrantWire) toDomain() *domain.ConnectorGrant {
	return &domain.ConnectorGrant{
		WorkspaceKey:    w.WorkspaceKey,
		GrantID:         w.GrantID,
		ConnectorID:     w.ConnectorID,
		BindingID:       w.BindingID,
		Action:          w.Action,
		ResourcePattern: w.ResourcePattern,
		CreatedAt:       w.CreatedAt,
		RevokedAt:       w.RevokedAt,
	}
}

// connectorCallWire mirrors fleet-db's models.ConnectorCallRecord JSON shape.
type connectorCallWire struct {
	WorkspaceKey     string    `json:"workspace_key"`
	CallID           string    `json:"call_id"`
	Seq              int       `json:"seq"`
	RunID            string    `json:"run_id"`
	BindingID        string    `json:"binding_id"`
	ConnectorID      string    `json:"connector_id"`
	SourceKind       string    `json:"source_kind"`
	Action           string    `json:"action"`
	Resource         string    `json:"resource"`
	Decision         string    `json:"decision"`
	UpstreamStatus   int       `json:"upstream_status"`
	ErrorClass       string    `json:"error_class"`
	SanitizedSummary string    `json:"sanitized_summary"`
	OccurredAt       time.Time `json:"occurred_at"`
}

func (w *connectorCallWire) toDomain() *domain.ConnectorCallRecord {
	return &domain.ConnectorCallRecord{
		WorkspaceKey:     w.WorkspaceKey,
		CallID:           w.CallID,
		Seq:              w.Seq,
		RunID:            w.RunID,
		BindingID:        w.BindingID,
		ConnectorID:      w.ConnectorID,
		SourceKind:       domain.ConnectorSourceKind(w.SourceKind),
		Action:           w.Action,
		Resource:         w.Resource,
		Decision:         domain.ConnectorCallDecision(w.Decision),
		UpstreamStatus:   w.UpstreamStatus,
		ErrorClass:       w.ErrorClass,
		SanitizedSummary: w.SanitizedSummary,
		OccurredAt:       w.OccurredAt,
	}
}

// --- ConnectorStore ---

type connectorStore struct{ client *Client }

var _ store.ConnectorStore = (*connectorStore)(nil)

func (s *connectorStore) Create(ctx context.Context, in store.ConnectorCreate) (*domain.Connector, error) {
	body := map[string]any{
		"connector_id": in.ConnectorID,
		"source_kind":  in.SourceKind,
	}
	if in.DisplayName != "" {
		body["display_name"] = in.DisplayName
	}
	if in.InboundEndpointPath != "" {
		body["inbound_endpoint_path"] = in.InboundEndpointPath
	}
	if in.InboundSecret != "" {
		body["inbound_secret"] = in.InboundSecret
	}
	if len(in.OutboundCredentialSealed) > 0 {
		body["outbound_credential_sealed"] = in.OutboundCredentialSealed
	}
	if in.Status != "" {
		body["status"] = in.Status
	}
	if in.CreatedBy != "" {
		body["created_by"] = in.CreatedBy
	}
	var out connectorWire
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/connectors", body, &out); err != nil {
		return nil, connectorSentinel(err)
	}
	return out.toDomain(), nil
}

func (s *connectorStore) Get(ctx context.Context, ws, connectorID string) (*domain.Connector, error) {
	var out connectorWire
	path := "/api/v1/" + pathEscape(ws) + "/connectors/" + pathEscape(connectorID)
	if err := s.client.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, connectorSentinel(err)
	}
	return out.toDomain(), nil
}

func (s *connectorStore) List(ctx context.Context, ws string, filter store.ConnectorFilter) ([]*domain.Connector, error) {
	q := url.Values{}
	if filter.SourceKind != "" {
		q.Set("source_kind", string(filter.SourceKind))
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/connectors", q)
	var resp struct {
		Connectors []*connectorWire `json:"connectors"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, connectorSentinel(err)
	}
	out := make([]*domain.Connector, 0, len(resp.Connectors))
	for _, connector := range resp.Connectors {
		out = append(out, connector.toDomain())
	}
	return out, nil
}

// connectorSecretsWire mirrors the privileged GET .../secrets response.
type connectorSecretsWire struct {
	ConnectorID              string     `json:"connector_id"`
	InboundSecret            string     `json:"inbound_secret"`
	PreviousInboundSecret    string     `json:"previous_inbound_secret"`
	PreviousSecretValidUntil *time.Time `json:"previous_secret_valid_until"`
	OutboundCredentialSealed []byte     `json:"outbound_credential_sealed"`
}

// resolveSecrets fetches the privileged secrets payload backing both
// Resolve* methods (single fleet-db route serves both reads).
func (s *connectorStore) resolveSecrets(ctx context.Context, ws, connectorID string) (*connectorSecretsWire, error) {
	var out connectorSecretsWire
	path := "/api/v1/" + pathEscape(ws) + "/connectors/" + pathEscape(connectorID) + "/secrets"
	if err := s.client.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, connectorSentinel(err)
	}
	return &out, nil
}

func (s *connectorStore) ResolveInboundSecret(ctx context.Context, ws, connectorID string) (*store.ConnectorInboundSecrets, error) {
	secrets, err := s.resolveSecrets(ctx, ws, connectorID)
	if err != nil {
		return nil, err
	}
	out := &store.ConnectorInboundSecrets{
		Current: secrets.InboundSecret,
	}
	if secrets.PreviousInboundSecret != "" &&
		secrets.PreviousSecretValidUntil != nil &&
		time.Now().UTC().Before(*secrets.PreviousSecretValidUntil) {
		out.Previous = secrets.PreviousInboundSecret
		out.PreviousValidUntil = *secrets.PreviousSecretValidUntil
	}
	return out, nil
}

func (s *connectorStore) ResolveOutboundCredentialSealed(ctx context.Context, ws, connectorID string) ([]byte, error) {
	secrets, err := s.resolveSecrets(ctx, ws, connectorID)
	if err != nil {
		return nil, err
	}
	return secrets.OutboundCredentialSealed, nil
}

func (s *connectorStore) RotateSecrets(ctx context.Context, ws, connectorID string, in store.ConnectorSecretRotation) (*domain.Connector, error) {
	body := map[string]any{
		"new_inbound_secret": in.NewInboundSecret,
	}
	if !in.PreviousSecretValidUntil.IsZero() {
		// Zero stays absent so fleet-db applies the default overlap
		// window (now + domain.DefaultConnectorSecretOverlap).
		body["previous_secret_valid_until"] = in.PreviousSecretValidUntil
	}
	if !in.ExpectedUpdatedAt.IsZero() {
		body["expected_updated_at"] = in.ExpectedUpdatedAt
	}
	if in.NewOutboundCredentialSealed != nil {
		body["new_outbound_credential_sealed"] = in.NewOutboundCredentialSealed
	}
	path := "/api/v1/" + pathEscape(ws) + "/connectors/" + pathEscape(connectorID) + "/rotate"
	var out connectorWire
	if err := s.client.do(ctx, "POST", path, body, &out); err != nil {
		return nil, connectorSentinel(err)
	}
	return out.toDomain(), nil
}

// --- ConnectorGrantStore ---

type connectorGrantStore struct{ client *Client }

var _ store.ConnectorGrantStore = (*connectorGrantStore)(nil)

func (s *connectorGrantStore) Create(ctx context.Context, in store.ConnectorGrantCreate) (*domain.ConnectorGrant, error) {
	body := map[string]any{
		"grant_id":         in.GrantID,
		"connector_id":     in.ConnectorID,
		"binding_id":       in.BindingID,
		"action":           in.Action,
		"resource_pattern": in.ResourcePattern,
	}
	var out connectorGrantWire
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/connector-grants", body, &out); err != nil {
		return nil, err
	}
	return out.toDomain(), nil
}

func (s *connectorGrantStore) Revoke(ctx context.Context, ws, grantID string) error {
	path := "/api/v1/" + pathEscape(ws) + "/connector-grants/" + pathEscape(grantID) + "/revoke"
	return grantRevokeSentinel(s.client.do(ctx, "POST", path, nil, nil))
}

func (s *connectorGrantStore) ListByBinding(ctx context.Context, ws, bindingID string) ([]*domain.ConnectorGrant, error) {
	q := url.Values{}
	q.Set("binding_id", bindingID)
	return s.list(ctx, ws, q)
}

func (s *connectorGrantStore) ListByConnector(ctx context.Context, ws, connectorID string) ([]*domain.ConnectorGrant, error) {
	q := url.Values{}
	q.Set("connector_id", connectorID)
	return s.list(ctx, ws, q)
}

func (s *connectorGrantStore) list(ctx context.Context, ws string, q url.Values) ([]*domain.ConnectorGrant, error) {
	path := withQuery("/api/v1/"+pathEscape(ws)+"/connector-grants", q)
	var resp struct {
		ConnectorGrants []*connectorGrantWire `json:"connector_grants"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*domain.ConnectorGrant, 0, len(resp.ConnectorGrants))
	for _, grant := range resp.ConnectorGrants {
		out = append(out, grant.toDomain())
	}
	return out, nil
}

// --- ConnectorAuditStore ---

type connectorAuditStore struct{ client *Client }

var _ store.ConnectorAuditStore = (*connectorAuditStore)(nil)

func (s *connectorAuditStore) Append(ctx context.Context, rec *domain.ConnectorCallRecord) error {
	// Validate client-side like memstore so malformed records fail with
	// the exact domain.ErrInvalid wrap without a round-trip.
	if err := rec.Validate(); err != nil {
		return err
	}
	body := map[string]any{
		"call_id":      rec.CallID,
		"seq":          rec.Seq,
		"run_id":       rec.RunID,
		"binding_id":   rec.BindingID,
		"connector_id": rec.ConnectorID,
		"source_kind":  rec.SourceKind,
		"action":       rec.Action,
		"decision":     rec.Decision,
		"occurred_at":  rec.OccurredAt,
	}
	if rec.Resource != "" {
		body["resource"] = rec.Resource
	}
	if rec.UpstreamStatus != 0 {
		body["upstream_status"] = rec.UpstreamStatus
	}
	if rec.ErrorClass != "" {
		body["error_class"] = rec.ErrorClass
	}
	if rec.SanitizedSummary != "" {
		body["sanitized_summary"] = rec.SanitizedSummary
	}
	return s.client.do(ctx, "POST", "/api/v1/"+pathEscape(rec.WorkspaceKey)+"/connector-audit", body, nil)
}

func (s *connectorAuditStore) ListByRun(ctx context.Context, ws, runID string, filter store.ConnectorCallFilter) ([]*domain.ConnectorCallRecord, error) {
	q := url.Values{}
	q.Set("run_id", runID)
	return s.list(ctx, ws, q, filter)
}

func (s *connectorAuditStore) ListByBinding(ctx context.Context, ws, bindingID string, filter store.ConnectorCallFilter) ([]*domain.ConnectorCallRecord, error) {
	q := url.Values{}
	q.Set("binding_id", bindingID)
	return s.list(ctx, ws, q, filter)
}

func (s *connectorAuditStore) list(ctx context.Context, ws string, q url.Values, filter store.ConnectorCallFilter) ([]*domain.ConnectorCallRecord, error) {
	if filter.Decision != "" {
		q.Set("decision", string(filter.Decision))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/connector-audit", q)
	var resp struct {
		ConnectorCalls []*connectorCallWire `json:"connector_calls"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*domain.ConnectorCallRecord, 0, len(resp.ConnectorCalls))
	for _, record := range resp.ConnectorCalls {
		out = append(out, record.toDomain())
	}
	return out, nil
}
