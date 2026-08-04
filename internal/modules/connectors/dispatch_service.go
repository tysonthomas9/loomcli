package connectors

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"
)

const maxDispatchSummaryLength = 240

// DispatchService is the Connectors-owned egress choke point. It re-resolves
// connector state and grants for every call, unseals credentials only after
// authorization, dispatches through an adapter port, and journals every
// granted or refused outcome.
type DispatchService struct {
	store     DispatchStore
	vault     CredentialOpener
	providers ProviderRegistry
	now       func() time.Time
}

var _ Dispatcher = (*DispatchService)(nil)

func NewDispatch(
	store DispatchStore,
	vault CredentialOpener,
	providers ProviderRegistry,
	now func() time.Time,
) (*DispatchService, error) {
	if store == nil || vault == nil || providers == nil {
		return nil, fmt.Errorf("compose Connectors dispatch: store, vault, and providers are required: %w", ErrUnavailable)
	}
	if now == nil {
		now = time.Now
	}
	return &DispatchService{store: store, vault: vault, providers: providers, now: now}, nil
}

func (service *DispatchService) Dispatch(
	ctx context.Context,
	command DispatchCommand,
) (DispatchResult, error) {
	if err := command.Validate(); err != nil {
		return DispatchResult{}, err
	}
	result := DispatchResult{CallID: ConnectorCallID(command.RunID, command.Action, command.CallSeq)}
	if service == nil || service.store == nil || service.vault == nil || service.providers == nil {
		return result, ErrUnavailable
	}

	connector, err := service.store.GetConnectorRecord(ctx, command.WorkspaceKey, command.ConnectorID)
	if err != nil {
		return result, fmt.Errorf("resolve connector %q: %w", command.ConnectorID, err)
	}
	if connector == nil || connector.WorkspaceKey != command.WorkspaceKey || connector.ConnectorID != command.ConnectorID ||
		!connector.SourceKind.Valid() || !connector.Status.Valid() {
		return result, fmt.Errorf("connector %q returned invalid persisted state: %w", command.ConnectorID, ErrInvalidPersistedState)
	}
	if connector.Status != ConnectorStatusActive {
		return service.refuse(ctx, command, result, connector.SourceKind, ConnectorCallDenied, fmt.Errorf(
			"connector %q status %q: %w", command.ConnectorID, connector.Status, ErrConnectorDisabled,
		))
	}

	if refusal, err := service.authorize(ctx, command, result, connector.SourceKind); refusal != nil || err != nil {
		if refusal != nil {
			return *refusal, err
		}
		return result, err
	}
	provider, err := service.providers.Get(connector.SourceKind)
	if err != nil {
		return service.refuse(ctx, command, result, connector.SourceKind, ConnectorCallDenied, fmt.Errorf(
			"provider for %q: %w", connector.SourceKind, err,
		))
	}

	credential, refusal, err := service.openCredential(ctx, command, result, connector.SourceKind)
	if refusal != nil || err != nil {
		if refusal != nil {
			return *refusal, err
		}
		return result, err
	}
	defer zeroCredential(credential)

	providerResult, providerErr := provider.Call(ctx, ProviderCall{
		Action: command.Action, Resource: command.Resource, Args: maps.Clone(command.Args),
		Preconditions: command.Preconditions, IdempotencyKey: result.CallID, Credential: string(credential),
	})
	return service.journalOutcome(ctx, command, result, connector.SourceKind, providerResult, providerErr)
}

func (service *DispatchService) authorize(
	ctx context.Context,
	command DispatchCommand,
	result DispatchResult,
	source ConnectorSourceKind,
) (*DispatchResult, error) {
	grants, err := service.store.ListGrantRecordsByBinding(ctx, command.WorkspaceKey, command.BindingID)
	if err != nil {
		return nil, fmt.Errorf("list connector grants for binding %q: %w", command.BindingID, err)
	}
	scoped := make([]*ConnectorGrant, 0, len(grants))
	for _, grant := range grants {
		if grant != nil && grant.ConnectorID == command.ConnectorID {
			scoped = append(scoped, cloneConnectorGrant(grant))
		}
	}
	authorization := EvaluateGrantAuthorization(command.BindingID, scoped, command.Action, command.Resource)
	if !authorization.Allowed {
		refused, refuseErr := service.refuse(
			ctx, command, result, source, ConnectorCallDenied, authorization.Denied,
		)
		return &refused, refuseErr
	}
	if missing := MissingActionPreconditions(command.Action, command.Preconditions); len(missing) > 0 {
		refused, refuseErr := service.refuse(
			ctx,
			command,
			result,
			source,
			ConnectorCallPreconditionRequired,
			&PreconditionRequired{Action: command.Action, Fields: missing},
		)
		return &refused, refuseErr
	}
	return nil, nil
}

func (service *DispatchService) openCredential(
	ctx context.Context,
	command DispatchCommand,
	result DispatchResult,
	source ConnectorSourceKind,
) ([]byte, *DispatchResult, error) {
	sealed, err := service.store.ResolveOutboundCredentialSealedRecord(ctx, command.WorkspaceKey, command.ConnectorID)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve sealed credential for %q: %w", command.ConnectorID, err)
	}
	if len(sealed) == 0 {
		refused, refuseErr := service.refuse(ctx, command, result, source, ConnectorCallDenied, fmt.Errorf(
			"connector %q: %w", command.ConnectorID, ErrOutboundCredentialMissing,
		))
		return nil, &refused, refuseErr
	}
	credential, err := service.vault.Unseal(sealed, CredentialAAD(command.WorkspaceKey, command.ConnectorID))
	if err != nil {
		refused, refuseErr := service.refuse(ctx, command, result, source, ConnectorCallDenied, fmt.Errorf(
			"connector %q credential: %w", command.ConnectorID, err,
		))
		return nil, &refused, refuseErr
	}
	return credential, nil, nil
}

func (service *DispatchService) journalOutcome(
	ctx context.Context,
	command DispatchCommand,
	result DispatchResult,
	source ConnectorSourceKind,
	providerResult ProviderResult,
	providerErr error,
) (DispatchResult, error) {
	decision := providerResult.Decision
	if !decision.Valid() {
		if providerErr != nil {
			decision = DecisionForDispatchError(providerErr)
		} else {
			decision = ConnectorCallGranted
		}
	}
	result.Decision = decision
	result.Status = providerResult.Status
	result.Body = maps.Clone(providerResult.Body)
	summary := ""
	if providerErr != nil {
		summary = providerErr.Error()
	}
	failure, _ := ClassifyDispatchError(providerErr)
	if err := service.appendAudit(ctx, command, source, decision, providerResult.Status, failure.ErrorClass, summary); err != nil {
		if providerErr != nil {
			return result, errors.Join(providerErr, err)
		}
		return result, err
	}
	return result, providerErr
}

func (service *DispatchService) refuse(
	ctx context.Context,
	command DispatchCommand,
	result DispatchResult,
	source ConnectorSourceKind,
	decision ConnectorCallDecision,
	cause error,
) (DispatchResult, error) {
	result.Decision = decision
	if err := service.appendAudit(ctx, command, source, decision, 0, "", cause.Error()); err != nil {
		return result, errors.Join(cause, err)
	}
	return result, cause
}

func (service *DispatchService) appendAudit(
	ctx context.Context,
	command DispatchCommand,
	source ConnectorSourceKind,
	decision ConnectorCallDecision,
	status int,
	errorClass string,
	summary string,
) error {
	record := &ConnectorCallRecord{
		WorkspaceKey: command.WorkspaceKey, CallID: ConnectorCallID(command.RunID, command.Action, command.CallSeq),
		Seq: command.CallSeq, RunID: command.RunID, BindingID: command.BindingID,
		ConnectorID: command.ConnectorID, SourceKind: source, Action: command.Action, Resource: command.Resource,
		Decision: decision, UpstreamStatus: status, ErrorClass: errorClass,
		SanitizedSummary: capDispatchSummary(summary), OccurredAt: service.now().UTC(),
	}
	if err := service.store.AppendConnectorCallRecord(ctx, record); err != nil && !errors.Is(err, ErrAlreadyExists) {
		return fmt.Errorf("append connector dispatch audit %q: %w", record.CallID, err)
	}
	return nil
}

func capDispatchSummary(summary string) string {
	if len(summary) > maxDispatchSummaryLength {
		return summary[:maxDispatchSummaryLength] + "..."
	}
	return summary
}

func zeroCredential(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
