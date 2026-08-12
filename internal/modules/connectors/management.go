package connectors

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ManagementService owns Connector definition validation plus grant and
// audit query scoping. Persistence adapters receive exact owner mutations and
// return credential-free projections.
type ManagementService struct {
	store       ManagementStore
	secretStore SecretLifecycleStore
	sealer      CredentialSealer
	vault       CredentialVault
	now         func() time.Time
}

var _ Management = (*ManagementService)(nil)

func NewManagement(store ManagementStore) (*ManagementService, error) {
	if store == nil {
		return nil, fmt.Errorf("compose Connectors management: %w", ErrUnavailable)
	}
	return &ManagementService{store: store, secretStore: store, now: time.Now}, nil
}

func NewSecretLifecycle(
	store SecretLifecycleStore,
	sealer CredentialSealer,
	now func() time.Time,
) (*ManagementService, error) {
	if store == nil || now == nil {
		return nil, fmt.Errorf("compose Connectors secret lifecycle: %w", ErrUnavailable)
	}
	return &ManagementService{secretStore: store, sealer: sealer, now: now}, nil
}

func NewManagementWithSecrets(
	store ManagementStore,
	sealer CredentialSealer,
	now func() time.Time,
) (*ManagementService, error) {
	if sealer == nil || now == nil {
		return nil, fmt.Errorf("compose Connectors secret lifecycle: %w", ErrUnavailable)
	}
	service, err := NewSecretLifecycle(store, sealer, now)
	if err != nil {
		return nil, err
	}
	service.store = store
	return service, nil
}

func NewManagementWithCredentialVault(
	store ManagementStore,
	vault CredentialVault,
	now func() time.Time,
) (*ManagementService, error) {
	if vault == nil || now == nil {
		return nil, fmt.Errorf("compose Connectors credential vault: %w", ErrUnavailable)
	}
	service, err := NewManagementWithSecrets(store, vault, now)
	if err != nil {
		return nil, err
	}
	service.vault = vault
	return service, nil
}

// SynchronizeConnectorCredential owns credential comparison, inbound-secret
// preservation, conflict retries, and atomic resealing. Plaintext is wiped and
// neither the stored ciphertext nor unsealed current value leaves Connectors.
//
//nolint:gocognit,funlen // Credential convergence enumerates each fail-closed vault and rollback branch explicitly.
func (service *ManagementService) SynchronizeConnectorCredential(
	ctx context.Context,
	command SynchronizeConnectorCredentialCommand,
) (*Connector, error) {
	defer zeroBytes(command.DesiredCredential)
	workspace, err := requireCanonical("workspace", command.WorkspaceKey)
	if err != nil {
		return nil, err
	}
	connectorID, err := requireCanonical("connector id", command.ConnectorID)
	if err != nil {
		return nil, err
	}
	if len(command.DesiredCredential) == 0 {
		return nil, fmt.Errorf("desired credential is required: %w", ErrInvalid)
	}
	if service == nil || service.secretStore == nil || service.vault == nil {
		return nil, ErrCredentialVaultMissing
	}

	for attempt := 0; attempt < 3; attempt++ {
		current, getErr := service.secretStore.GetConnectorRecord(ctx, workspace, connectorID)
		if getErr != nil {
			return nil, getErr
		}
		if err := validateConnectorProjection(current, workspace, connectorID); err != nil {
			return nil, err
		}
		sealed, resolveErr := service.secretStore.ResolveOutboundCredentialSealedRecord(ctx, workspace, connectorID)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolve connector credential: %w", resolveErr)
		}
		if len(sealed) > 0 {
			same, matchErr := service.vault.Matches(
				sealed, command.DesiredCredential, credentialAAD(workspace, connectorID),
			)
			zeroBytes(sealed)
			if matchErr != nil {
				return nil, fmt.Errorf("compare connector credential: %w", matchErr)
			}
			if same {
				return cloneConnector(current), nil
			}
		}
		inbound, resolveErr := service.secretStore.ResolveInboundSecretsRecord(ctx, workspace, connectorID)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolve connector inbound secret: %w", resolveErr)
		}
		if inbound == nil {
			return nil, ErrInvalidPersistedState
		}
		currentInbound := inbound.Current
		if currentInbound == "" {
			currentInbound, err = randomInboundSecret()
			if err != nil {
				return nil, err
			}
		}
		rotated, rotateErr := service.RotateConnector(ctx, RotateConnectorCommand{
			WorkspaceKey: workspace, ConnectorID: connectorID, NewInboundSecret: currentInbound,
			NewCredential:     append([]byte(nil), command.DesiredCredential...),
			ExpectedUpdatedAt: current.UpdatedAt,
		})
		if rotateErr == nil {
			return rotated, nil
		}
		if !errors.Is(rotateErr, ErrRotationConflict) {
			return rotated, rotateErr
		}
	}
	return nil, fmt.Errorf("connector credential changed during synchronization: %w", ErrRotationConflict)
}

// RotateConnector performs one atomic dual-secret rotation and appends its
// redaction-safe audit record. On an audit failure the rotated Connector is
// returned together with the error because the atomic secret write has landed.
//
//nolint:funlen // Rotation keeps secret generation, durable update, and audit compensation in one ceremony.
func (service *ManagementService) RotateConnector(
	ctx context.Context,
	command RotateConnectorCommand,
) (*Connector, error) {
	defer zeroBytes(command.NewCredential)
	command, err := normalizeRotateConnector(command)
	if err != nil {
		return nil, err
	}
	if service == nil || service.secretStore == nil || service.now == nil {
		return nil, ErrUnavailable
	}
	if len(command.NewCredential) > 0 && service.sealer == nil {
		return nil, ErrRotationSealerMissing
	}
	current, err := service.secretStore.GetConnectorRecord(ctx, command.WorkspaceKey, command.ConnectorID)
	if err != nil {
		return nil, fmt.Errorf("resolve connector for rotation: %w", err)
	}
	if err := validateConnectorProjection(current, command.WorkspaceKey, command.ConnectorID); err != nil {
		return nil, err
	}
	if !command.ExpectedUpdatedAt.IsZero() && !current.UpdatedAt.Equal(command.ExpectedUpdatedAt) {
		return nil, fmt.Errorf("connector generation changed before rotation: %w", ErrRotationConflict)
	}
	validUntil := service.now().UTC().Add(rotationOverlap(command.InboundWindow))
	mutation := RotateConnectorSecretsMutation{
		NewInboundSecret: command.NewInboundSecret, PreviousSecretValidUntil: validUntil,
		ExpectedUpdatedAt: current.UpdatedAt,
	}
	if len(command.NewCredential) > 0 {
		sealed, sealErr := service.sealer.Seal(
			command.NewCredential,
			credentialAAD(command.WorkspaceKey, command.ConnectorID),
		)
		zeroBytes(command.NewCredential)
		if sealErr != nil {
			return nil, fmt.Errorf("seal replacement connector credential: %w", sealErr)
		}
		mutation.NewOutboundCredentialSealed = sealed
	}
	rotated, err := service.secretStore.RotateConnectorSecretsRecord(
		ctx, command.WorkspaceKey, command.ConnectorID, mutation,
	)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			err = errors.Join(ErrRotationConflict, err)
		}
		return nil, fmt.Errorf("rotate connector %q: %w", command.ConnectorID, err)
	}
	if err := validateConnectorProjection(rotated, command.WorkspaceKey, command.ConnectorID); err != nil {
		return nil, err
	}
	rotated = cloneConnector(rotated)
	if err := service.appendRotationAudit(ctx, current.SourceKind, rotated, validUntil, mutation.NewOutboundCredentialSealed != nil); err != nil {
		return rotated, err
	}
	return rotated, nil
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

// RevokeBindingGrants converges a binding to no active Connector grants. A
// repeated revoke is success and does not increment the changed-row count.
func (service *ManagementService) RevokeBindingGrants(
	ctx context.Context,
	command BindingGrantCleanupCommand,
) (int, error) {
	workspace, err := requireCanonical("workspace", command.WorkspaceKey)
	if err != nil {
		return 0, err
	}
	bindingID, err := requireCanonical("binding id", command.BindingID)
	if err != nil {
		return 0, err
	}
	if service == nil || service.store == nil {
		return 0, ErrUnavailable
	}
	grants, err := service.store.ListGrantRecordsByBinding(ctx, workspace, bindingID)
	if err != nil {
		return 0, fmt.Errorf("list binding grants: %w", err)
	}
	revoked := 0
	for _, grant := range grants {
		if grant == nil {
			return revoked, ErrInvalidPersistedState
		}
		if err := validateManagementGrant(grant, ListGrantsQuery{
			WorkspaceKey: workspace,
			BindingID:    bindingID,
		}); err != nil {
			return revoked, err
		}
		if err := service.store.RevokeGrantRecord(ctx, workspace, grant.GrantID); err != nil {
			if errors.Is(err, ErrGrantRevoked) {
				continue
			}
			return revoked, fmt.Errorf("revoke binding grant %q: %w", grant.GrantID, err)
		}
		revoked++
	}
	return revoked, nil
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

func normalizeRotateConnector(command RotateConnectorCommand) (RotateConnectorCommand, error) {
	var err error
	if command.WorkspaceKey, err = requireCanonical("workspace", command.WorkspaceKey); err != nil {
		return RotateConnectorCommand{}, err
	}
	if command.ConnectorID, err = requireCanonical("connector id", command.ConnectorID); err != nil {
		return RotateConnectorCommand{}, err
	}
	if strings.TrimSpace(command.NewInboundSecret) == "" {
		return RotateConnectorCommand{}, fmt.Errorf("new inbound secret is required: %w", ErrInvalid)
	}
	if command.InboundWindow < 0 {
		return RotateConnectorCommand{}, fmt.Errorf("rotation overlap cannot be negative: %w", ErrInvalid)
	}
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

func rotationOverlap(window time.Duration) time.Duration {
	switch {
	case window == 0:
		return DefaultConnectorSecretOverlap
	case window > MaxConnectorSecretOverlap:
		return MaxConnectorSecretOverlap
	default:
		return window
	}
}

func credentialAAD(workspaceKey, connectorID string) []byte {
	result := make([]byte, 0, len("loom-connector-credential")+len(workspaceKey)+len(connectorID)+2)
	result = append(result, "loom-connector-credential"...)
	result = append(result, 0)
	result = append(result, workspaceKey...)
	result = append(result, 0)
	result = append(result, connectorID...)
	return result
}

func randomInboundSecret() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate connector inbound secret: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func (service *ManagementService) appendRotationAudit(
	ctx context.Context,
	source ConnectorSourceKind,
	rotated *Connector,
	validUntil time.Time,
	resealed bool,
) error {
	occurredAt := service.now().UTC()
	if rotated.RotatedAt != nil && !rotated.RotatedAt.IsZero() {
		occurredAt = rotated.RotatedAt.UTC()
	}
	generation := rotated.UpdatedAt.UTC()
	if generation.IsZero() {
		generation = occurredAt
	}
	runID := fmt.Sprintf("rotation-%s-%d", rotated.ConnectorID, generation.UnixNano())
	record := &ConnectorCallRecord{
		WorkspaceKey: rotated.WorkspaceKey,
		CallID:       connectorCallID(runID, RotationAuditAction, 0),
		RunID:        runID,
		BindingID:    RotationAuditBindingID,
		ConnectorID:  rotated.ConnectorID,
		SourceKind:   source,
		Action:       RotationAuditAction,
		Resource:     "connector:" + rotated.ConnectorID,
		Decision:     ConnectorCallGranted,
		SanitizedSummary: fmt.Sprintf(
			"inbound secret rotated (previous secret valid until %s); outbound credential resealed=%t",
			validUntil.UTC().Format(time.RFC3339),
			resealed,
		),
		OccurredAt: occurredAt,
	}
	if err := service.secretStore.AppendConnectorCallRecord(ctx, record); err != nil && !errors.Is(err, ErrAlreadyExists) {
		return fmt.Errorf("append connector rotation audit %q: %w", record.CallID, err)
	}
	return nil
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func cloneConnector(value *Connector) *Connector {
	if value == nil {
		return nil
	}
	result := *value
	if value.PreviousSecretValidUntil != nil {
		validUntil := *value.PreviousSecretValidUntil
		result.PreviousSecretValidUntil = &validUntil
	}
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
