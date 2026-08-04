package connectors

import (
	"context"
	"fmt"
	"strings"
)

// ManagementService owns Connector definition validation plus grant and
// audit query scoping. Persistence adapters receive exact owner mutations and
// return credential-free projections.
type ManagementService struct{ store ManagementStore }

var _ Management = (*ManagementService)(nil)

func NewManagement(store ManagementStore) (*ManagementService, error) {
	if store == nil {
		return nil, fmt.Errorf("compose Connectors management: %w", ErrUnavailable)
	}
	return &ManagementService{store: store}, nil
}

func (service *ManagementService) CreateConnector(
	ctx context.Context,
	command CreateConnectorCommand,
) (*Connector, error) {
	command, err := normalizeCreateConnector(command)
	if err != nil {
		return nil, err
	}
	if service == nil || service.store == nil {
		return nil, ErrUnavailable
	}
	value, err := service.store.CreateConnectorRecord(ctx, CreateConnectorMutation(command))
	if err != nil {
		return nil, err
	}
	if err := validateConnectorProjection(value, command.WorkspaceKey, command.ConnectorID); err != nil {
		return nil, err
	}
	return cloneConnector(value), nil
}

func (service *ManagementService) GetConnector(
	ctx context.Context,
	query GetConnectorQuery,
) (*Connector, error) {
	workspace, err := requireCanonical("workspace", query.WorkspaceKey)
	if err != nil {
		return nil, err
	}
	connectorID, err := requireCanonical("connector id", query.ConnectorID)
	if err != nil {
		return nil, err
	}
	if service == nil || service.store == nil {
		return nil, ErrUnavailable
	}
	value, err := service.store.GetConnectorRecord(ctx, workspace, connectorID)
	if err != nil {
		return nil, err
	}
	if err := validateConnectorProjection(value, workspace, connectorID); err != nil {
		return nil, err
	}
	return cloneConnector(value), nil
}

func (service *ManagementService) ListConnectors(
	ctx context.Context,
	query ListConnectorsQuery,
) ([]*Connector, error) {
	workspace, err := requireCanonical("workspace", query.WorkspaceKey)
	if err != nil {
		return nil, err
	}
	filter, err := normalizeConnectorFilter(query.Filter)
	if err != nil {
		return nil, err
	}
	if service == nil || service.store == nil {
		return nil, ErrUnavailable
	}
	values, err := service.store.ListConnectorRecords(ctx, workspace, filter)
	if err != nil {
		return nil, err
	}
	result := make([]*Connector, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if err := validateConnectorProjection(value, workspace, ""); err != nil {
			return nil, err
		}
		if _, duplicate := seen[value.ConnectorID]; duplicate {
			return nil, fmt.Errorf("duplicate connector %q: %w", value.ConnectorID, ErrInvalidPersistedState)
		}
		seen[value.ConnectorID] = struct{}{}
		result[index] = cloneConnector(value)
	}
	return result, nil
}

func (service *ManagementService) CreateGrant(
	ctx context.Context,
	command CreateGrantCommand,
) (*ConnectorGrant, error) {
	command, err := normalizeEnsureGrantCommand(command)
	if err != nil {
		return nil, err
	}
	if service == nil || service.store == nil {
		return nil, ErrUnavailable
	}
	value, err := service.store.CreateManagementGrant(ctx, CreateGrantMutation(command))
	if err != nil {
		return nil, err
	}
	if err := validateExactGrant(value, command); err != nil {
		return nil, err
	}
	return cloneConnectorGrant(value), nil
}

func (service *ManagementService) RevokeGrant(ctx context.Context, command RevokeGrantCommand) error {
	workspace, err := requireCanonical("workspace", command.WorkspaceKey)
	if err != nil {
		return err
	}
	grantID, err := requireCanonical("grant id", command.GrantID)
	if err != nil {
		return err
	}
	if service == nil || service.store == nil {
		return ErrUnavailable
	}
	return service.store.RevokeGrantRecord(ctx, workspace, grantID)
}

func (service *ManagementService) ListGrants(
	ctx context.Context,
	query ListGrantsQuery,
) ([]*ConnectorGrant, error) {
	query, err := normalizeListGrantsQuery(query)
	if err != nil {
		return nil, err
	}
	if service == nil || service.store == nil {
		return nil, ErrUnavailable
	}
	var values []*ConnectorGrant
	if query.BindingID != "" {
		values, err = service.store.ListGrantRecordsByBinding(ctx, query.WorkspaceKey, query.BindingID)
	} else {
		values, err = service.store.ListGrantRecordsByConnector(ctx, query.WorkspaceKey, query.ConnectorID)
	}
	if err != nil {
		return nil, err
	}
	result := make([]*ConnectorGrant, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if err := validateManagementGrant(value, query); err != nil {
			return nil, err
		}
		if _, duplicate := seen[value.GrantID]; duplicate {
			return nil, fmt.Errorf("duplicate active grant %q: %w", value.GrantID, ErrInvalidPersistedState)
		}
		seen[value.GrantID] = struct{}{}
		result[index] = cloneConnectorGrant(value)
	}
	return result, nil
}

func (service *ManagementService) ListCalls(
	ctx context.Context,
	query ListCallsQuery,
) ([]*ConnectorCallRecord, error) {
	query, err := normalizeListCallsQuery(query)
	if err != nil {
		return nil, err
	}
	if service == nil || service.store == nil {
		return nil, ErrUnavailable
	}
	var values []*ConnectorCallRecord
	if query.RunID != "" {
		values, err = service.store.ListCallRecordsByRun(ctx, query.WorkspaceKey, query.RunID, query.Filter)
	} else {
		values, err = service.store.ListCallRecordsByBinding(ctx, query.WorkspaceKey, query.BindingID, query.Filter)
	}
	if err != nil {
		return nil, err
	}
	result := make([]*ConnectorCallRecord, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if err := validateCallProjection(value, query); err != nil {
			return nil, err
		}
		if _, duplicate := seen[value.CallID]; duplicate {
			return nil, fmt.Errorf("duplicate connector call %q: %w", value.CallID, ErrInvalidPersistedState)
		}
		seen[value.CallID] = struct{}{}
		result[index] = cloneConnectorCall(value)
	}
	return result, nil
}

func normalizeCreateConnector(command CreateConnectorCommand) (CreateConnectorCommand, error) {
	var err error
	if command.WorkspaceKey, err = requireCanonical("workspace", command.WorkspaceKey); err != nil {
		return CreateConnectorCommand{}, err
	}
	if command.ConnectorID, err = requireCanonical("connector id", command.ConnectorID); err != nil {
		return CreateConnectorCommand{}, err
	}
	if !command.SourceKind.Valid() {
		return CreateConnectorCommand{}, fmt.Errorf("unknown connector source %q: %w", command.SourceKind, ErrInvalid)
	}
	if command.Status == "" {
		command.Status = ConnectorStatusActive
	}
	if !command.Status.Valid() {
		return CreateConnectorCommand{}, fmt.Errorf("unknown connector status %q: %w", command.Status, ErrInvalid)
	}
	command.DisplayName = strings.TrimSpace(command.DisplayName)
	command.InboundEndpointPath = strings.TrimSpace(command.InboundEndpointPath)
	if command.InboundEndpointPath != "" && !strings.HasPrefix(command.InboundEndpointPath, "/") {
		return CreateConnectorCommand{}, fmt.Errorf("connector endpoint must start with /: %w", ErrInvalid)
	}
	command.OutboundCredentialSealed = append([]byte(nil), command.OutboundCredentialSealed...)
	return command, nil
}

func normalizeConnectorFilter(filter ConnectorFilter) (ConnectorFilter, error) {
	if filter.SourceKind != "" && !filter.SourceKind.Valid() {
		return ConnectorFilter{}, fmt.Errorf("unknown connector source %q: %w", filter.SourceKind, ErrInvalid)
	}
	if filter.Status != "" && !filter.Status.Valid() {
		return ConnectorFilter{}, fmt.Errorf("unknown connector status %q: %w", filter.Status, ErrInvalid)
	}
	if filter.Limit < 0 {
		return ConnectorFilter{}, fmt.Errorf("connector list limit cannot be negative: %w", ErrInvalid)
	}
	return filter, nil
}

func normalizeListGrantsQuery(query ListGrantsQuery) (ListGrantsQuery, error) {
	workspace, err := requireCanonical("workspace", query.WorkspaceKey)
	if err != nil {
		return ListGrantsQuery{}, err
	}
	query.WorkspaceKey = workspace
	query.BindingID = strings.TrimSpace(query.BindingID)
	query.ConnectorID = strings.TrimSpace(query.ConnectorID)
	if (query.BindingID == "") == (query.ConnectorID == "") {
		return ListGrantsQuery{}, fmt.Errorf("exactly one grant selector is required: %w", ErrInvalid)
	}
	return query, nil
}

func normalizeListCallsQuery(query ListCallsQuery) (ListCallsQuery, error) {
	workspace, err := requireCanonical("workspace", query.WorkspaceKey)
	if err != nil {
		return ListCallsQuery{}, err
	}
	query.WorkspaceKey = workspace
	query.RunID = strings.TrimSpace(query.RunID)
	query.BindingID = strings.TrimSpace(query.BindingID)
	if (query.RunID == "") == (query.BindingID == "") {
		return ListCallsQuery{}, fmt.Errorf("exactly one connector call selector is required: %w", ErrInvalid)
	}
	if query.Filter.Decision != "" && !query.Filter.Decision.Valid() {
		return ListCallsQuery{}, fmt.Errorf("unknown connector call decision %q: %w", query.Filter.Decision, ErrInvalid)
	}
	if query.Filter.Limit < 0 {
		return ListCallsQuery{}, fmt.Errorf("connector call limit cannot be negative: %w", ErrInvalid)
	}
	return query, nil
}

func validateConnectorProjection(value *Connector, workspace, connectorID string) error {
	if value == nil || value.WorkspaceKey != workspace || value.ConnectorID == "" ||
		(connectorID != "" && value.ConnectorID != connectorID) || !value.SourceKind.Valid() ||
		!value.Status.Valid() || value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() ||
		(value.InboundEndpointPath != "" && !strings.HasPrefix(value.InboundEndpointPath, "/")) {
		return ErrInvalidPersistedState
	}
	return nil
}

func validateManagementGrant(value *ConnectorGrant, query ListGrantsQuery) error {
	if value == nil || value.WorkspaceKey != query.WorkspaceKey || value.RevokedAt != nil ||
		(query.BindingID != "" && value.BindingID != query.BindingID) ||
		(query.ConnectorID != "" && value.ConnectorID != query.ConnectorID) {
		return ErrInvalidPersistedState
	}
	command := EnsureGrantCommand{
		WorkspaceKey: value.WorkspaceKey, GrantID: value.GrantID, ConnectorID: value.ConnectorID,
		BindingID: value.BindingID, Action: value.Action, ResourcePattern: value.ResourcePattern,
	}
	if _, err := normalizeEnsureGrantCommand(command); err != nil || value.CreatedAt.IsZero() {
		return ErrInvalidPersistedState
	}
	return nil
}

func validateCallProjection(value *ConnectorCallRecord, query ListCallsQuery) error {
	if value == nil || value.WorkspaceKey != query.WorkspaceKey || value.CallID == "" ||
		value.RunID == "" || value.BindingID == "" || value.ConnectorID == "" ||
		!value.SourceKind.Valid() || !value.Decision.Valid() || value.OccurredAt.IsZero() ||
		(query.RunID != "" && value.RunID != query.RunID) ||
		(query.BindingID != "" && value.BindingID != query.BindingID) {
		return ErrInvalidPersistedState
	}
	if _, err := normalizeConnectorAction(value.Action); err != nil {
		return ErrInvalidPersistedState
	}
	if value.CallID != connectorCallID(value.RunID, value.Action, value.Seq) {
		return ErrInvalidPersistedState
	}
	return nil
}

func connectorCallID(runID, action string, sequence int) string {
	return fmt.Sprintf("%s#%s#%d", runID, action, sequence)
}

func cloneConnector(value *Connector) *Connector {
	if value == nil {
		return nil
	}
	result := *value
	if value.RotatedAt != nil {
		rotatedAt := *value.RotatedAt
		result.RotatedAt = &rotatedAt
	}
	return &result
}

func cloneConnectorCall(value *ConnectorCallRecord) *ConnectorCallRecord {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
