// Package connectorscatalog adapts the legacy composite persistence stores to
// the Connectors management owner port. It is the only place where management
// projections cross between domain/store types and the capability API.
package connectorscatalog

import (
	"context"
	"errors"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/connectorsrotation"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type Catalog struct {
	connectors store.ConnectorStore
	grants     store.ConnectorGrantStore
	audit      store.ConnectorAuditStore
	rotation   *connectorsrotation.Adapter
}

var _ connectorsmodule.ManagementStore = (*Catalog)(nil)

func New(
	connectors store.ConnectorStore,
	grants store.ConnectorGrantStore,
	audit store.ConnectorAuditStore,
) (*Catalog, error) {
	if connectors == nil || grants == nil || audit == nil {
		return nil, connectorsmodule.ErrUnavailable
	}
	rotation, err := connectorsrotation.New(connectors, audit)
	if err != nil {
		return nil, err
	}
	return &Catalog{
		connectors: connectors,
		grants:     grants,
		audit:      audit,
		rotation:   rotation,
	}, nil
}

func (catalog *Catalog) CreateConnectorRecord(
	ctx context.Context,
	mutation connectorsmodule.CreateConnectorMutation,
) (*connectorsmodule.Connector, error) {
	value, err := catalog.connectors.Create(ctx, store.ConnectorCreate{
		WorkspaceKey: mutation.WorkspaceKey, ConnectorID: mutation.ConnectorID,
		SourceKind: domain.ConnectorSourceKind(mutation.SourceKind), DisplayName: mutation.DisplayName,
		InboundEndpointPath: mutation.InboundEndpointPath, InboundSecret: mutation.InboundSecret,
		OutboundCredentialSealed: append([]byte(nil), mutation.OutboundCredentialSealed...),
		Status:                   domain.ConnectorStatus(mutation.Status), CreatedBy: mutation.CreatedBy,
	})
	return connectorProjection(value), translateError(err)
}

func (catalog *Catalog) GetConnectorRecord(
	ctx context.Context,
	workspace,
	connectorID string,
) (*connectorsmodule.Connector, error) {
	value, err := catalog.connectors.Get(ctx, workspace, connectorID)
	return connectorProjection(value), translateError(err)
}

func (catalog *Catalog) ListConnectorRecords(
	ctx context.Context,
	workspace string,
	filter connectorsmodule.ConnectorFilter,
) ([]*connectorsmodule.Connector, error) {
	values, err := catalog.connectors.List(ctx, workspace, store.ConnectorFilter{
		SourceKind: domain.ConnectorSourceKind(filter.SourceKind),
		Status:     domain.ConnectorStatus(filter.Status),
		Limit:      filter.Limit,
	})
	if err != nil {
		return nil, translateError(err)
	}
	result := make([]*connectorsmodule.Connector, len(values))
	for index, value := range values {
		result[index] = connectorProjection(value)
	}
	return result, nil
}

func (catalog *Catalog) RotateConnectorSecretsRecord(
	ctx context.Context,
	workspace,
	connectorID string,
	mutation connectorsmodule.RotateConnectorSecretsMutation,
) (*connectorsmodule.Connector, error) {
	return catalog.rotation.RotateConnectorSecretsRecord(ctx, workspace, connectorID, mutation)
}

func (catalog *Catalog) CreateManagementGrant(
	ctx context.Context,
	mutation connectorsmodule.CreateGrantMutation,
) (*connectorsmodule.ConnectorGrant, error) {
	value, err := catalog.grants.Create(ctx, store.ConnectorGrantCreate{
		WorkspaceKey: mutation.WorkspaceKey, GrantID: mutation.GrantID,
		ConnectorID: mutation.ConnectorID, BindingID: mutation.BindingID,
		Action: mutation.Action, ResourcePattern: mutation.ResourcePattern,
	})
	return grantProjection(value), translateError(err)
}

func (catalog *Catalog) RevokeGrantRecord(ctx context.Context, workspace, grantID string) error {
	return translateError(catalog.grants.Revoke(ctx, workspace, grantID))
}

func (catalog *Catalog) ListGrantRecordsByBinding(
	ctx context.Context,
	workspace,
	bindingID string,
) ([]*connectorsmodule.ConnectorGrant, error) {
	values, err := catalog.grants.ListByBinding(ctx, workspace, bindingID)
	return grantProjections(values), translateError(err)
}

func (catalog *Catalog) ListGrantRecordsByConnector(
	ctx context.Context,
	workspace,
	connectorID string,
) ([]*connectorsmodule.ConnectorGrant, error) {
	values, err := catalog.grants.ListByConnector(ctx, workspace, connectorID)
	return grantProjections(values), translateError(err)
}

func (catalog *Catalog) ListCallRecordsByRun(
	ctx context.Context,
	workspace,
	runID string,
	filter connectorsmodule.ConnectorCallFilter,
) ([]*connectorsmodule.ConnectorCallRecord, error) {
	values, err := catalog.audit.ListByRun(ctx, workspace, runID, auditFilter(filter))
	return callProjections(values), translateError(err)
}

func (catalog *Catalog) ListCallRecordsByBinding(
	ctx context.Context,
	workspace,
	bindingID string,
	filter connectorsmodule.ConnectorCallFilter,
) ([]*connectorsmodule.ConnectorCallRecord, error) {
	values, err := catalog.audit.ListByBinding(ctx, workspace, bindingID, auditFilter(filter))
	return callProjections(values), translateError(err)
}

func (catalog *Catalog) AppendConnectorCallRecord(
	ctx context.Context,
	record *connectorsmodule.ConnectorCallRecord,
) error {
	if record == nil {
		return connectorsmodule.ErrInvalid
	}
	return catalog.rotation.AppendConnectorCallRecord(ctx, record)
}

func connectorProjection(value *domain.Connector) *connectorsmodule.Connector {
	if value == nil {
		return nil
	}
	return &connectorsmodule.Connector{
		WorkspaceKey: value.WorkspaceKey, ConnectorID: value.ConnectorID,
		SourceKind: connectorsmodule.ConnectorSourceKind(value.SourceKind), DisplayName: value.DisplayName,
		InboundEndpointPath:      value.InboundEndpointPath,
		PreviousSecretValidUntil: cloneTime(value.PreviousSecretValidUntil),
		Status:                   connectorsmodule.ConnectorStatus(value.Status),
		CreatedBy:                value.CreatedBy, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		RotatedAt: cloneTime(value.RotatedAt),
	}
}

func grantProjection(value *domain.ConnectorGrant) *connectorsmodule.ConnectorGrant {
	if value == nil {
		return nil
	}
	return &connectorsmodule.ConnectorGrant{
		WorkspaceKey: value.WorkspaceKey, GrantID: value.GrantID,
		ConnectorID: value.ConnectorID, BindingID: value.BindingID,
		Action: value.Action, ResourcePattern: value.ResourcePattern,
		CreatedAt: value.CreatedAt, RevokedAt: cloneTime(value.RevokedAt),
	}
}

func grantProjections(values []*domain.ConnectorGrant) []*connectorsmodule.ConnectorGrant {
	result := make([]*connectorsmodule.ConnectorGrant, len(values))
	for index, value := range values {
		result[index] = grantProjection(value)
	}
	return result
}

func callProjections(values []*domain.ConnectorCallRecord) []*connectorsmodule.ConnectorCallRecord {
	result := make([]*connectorsmodule.ConnectorCallRecord, len(values))
	for index, value := range values {
		if value == nil {
			continue
		}
		result[index] = &connectorsmodule.ConnectorCallRecord{
			WorkspaceKey: value.WorkspaceKey, CallID: value.CallID, Seq: value.Seq,
			RunID: value.RunID, BindingID: value.BindingID, ConnectorID: value.ConnectorID,
			SourceKind: connectorsmodule.ConnectorSourceKind(value.SourceKind), Action: value.Action,
			Resource: value.Resource, Decision: connectorsmodule.ConnectorCallDecision(value.Decision),
			UpstreamStatus: value.UpstreamStatus, ErrorClass: value.ErrorClass,
			SanitizedSummary: value.SanitizedSummary, OccurredAt: value.OccurredAt,
		}
	}
	return result
}

func auditFilter(filter connectorsmodule.ConnectorCallFilter) store.ConnectorCallFilter {
	return store.ConnectorCallFilter{
		Decision: domain.ConnectorCallDecision(filter.Decision),
		Limit:    filter.Limit,
	}
}

func translateError(err error) error {
	if err == nil {
		return nil
	}
	var owner error
	switch {
	case errors.Is(err, domain.ErrConnectorNotFound), errors.Is(err, domain.ErrNotFound):
		owner = connectorsmodule.ErrNotFound
	case errors.Is(err, domain.ErrGrantRevoked):
		owner = connectorsmodule.ErrGrantRevoked
	case errors.Is(err, domain.ErrAlreadyExists):
		owner = connectorsmodule.ErrAlreadyExists
	case errors.Is(err, domain.ErrConflict):
		owner = connectorsmodule.ErrConflict
	case errors.Is(err, domain.ErrInvalid):
		owner = connectorsmodule.ErrInvalid
	default:
		return err
	}
	return errors.Join(owner, err)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
