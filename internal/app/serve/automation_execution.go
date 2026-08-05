package serve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type automationExecutionDispatchFunc func(context.Context, automation.ExecutionDispatchRequest) (*automation.ExecutionDispatchResult, error)

// automationExecutionPort is the Phase 3 compatibility adapter for
// Automation's consumer-owned Execution intent. Emission context is reloaded
// from Execution-owned durable run state using the opaque authority subject;
// delivery and manual-binding dispatch are supplied by atomic Fleet intents
// and never fall back to DriverRunStore.Create.
type automationExecutionPort struct {
	runs     store.DriverRunStore
	dispatch automationExecutionDispatchFunc
}

var _ automation.ExecutionPort = (*automationExecutionPort)(nil)

func newAutomationExecutionPort(runs store.DriverRunStore, dispatch automationExecutionDispatchFunc) automation.ExecutionPort {
	if runs == nil {
		return nil
	}
	return &automationExecutionPort{runs: runs, dispatch: dispatch}
}

// newAutomationFleetExecutionDispatch maps Automation's Execution-owned
// intent onto FleetDB's atomic reserved-delivery or manual-binding command.
// One ambiguous transport failure is retried with the identical idempotency
// key so a lost success response replays the committed DriverRun envelope.
// Typed client and domain errors are deterministic responses and are never
// retried here.
func newAutomationFleetExecutionDispatch(client *infrafleetdb.Client) automationExecutionDispatchFunc {
	if client == nil {
		return nil
	}
	return func(ctx context.Context, request automation.ExecutionDispatchRequest) (*automation.ExecutionDispatchResult, error) {
		if strings.TrimSpace(request.DeliveryID) == "" {
			return dispatchAutomationBindingWithFleet(ctx, client, request)
		}
		command := infrafleetdb.AutomationDeliveryDispatch{
			WorkspaceKey: request.WorkspaceKey, DeliveryID: request.DeliveryID,
			IdempotencyKey: request.IdempotencyKey, ExpectedStatus: request.ExpectedDeliveryStatus,
			ExpectedAttempt: request.ExpectedDeliveryAttempt,
		}
		result, err := client.Automation().DispatchAutomationDelivery(ctx, command)
		if err != nil && automationDispatchRetryable(ctx, err) {
			result, err = client.Automation().DispatchAutomationDelivery(ctx, command)
		}
		if err != nil {
			return nil, translateAutomationExecutionFleetDBError(err)
		}
		if result == nil {
			return nil, automation.ErrInvalidPersistedState
		}
		mapped := &automation.ExecutionDispatchResult{
			Delivery: result.Delivery,
			Replayed: result.Replayed,
		}
		switch result.Outcome {
		case infrafleetdb.AutomationDeliveryDispatchBusy:
			mapped.Busy = true
			mapped.BusyRunID = result.BusyRunID
		case infrafleetdb.AutomationDeliveryDispatchRun,
			infrafleetdb.AutomationDeliveryDispatchReused,
			infrafleetdb.AutomationDeliveryDispatchSuperseded:
			if result.DriverRun != nil {
				mapped.RunID = result.DriverRun.RunID
			}
		default:
			return nil, automation.ErrInvalidPersistedState
		}
		return mapped, nil
	}
}

func dispatchAutomationBindingWithFleet(
	ctx context.Context,
	client *infrafleetdb.Client,
	request automation.ExecutionDispatchRequest,
) (*automation.ExecutionDispatchResult, error) {
	command := infrafleetdb.AutomationBindingDispatch{
		WorkspaceKey: request.WorkspaceKey, BindingID: request.TriggerBindingID, IdempotencyKey: request.IdempotencyKey,
		ReplayOnly: request.ReplayOnly,
		SubjectRef: request.SubjectRef, EpicID: request.EpicID, ActorRef: request.ActorRef,
		RawPayloadRef: request.RawPayloadRef, Payload: append([]byte(nil), request.Payload...),
		SubjectAttrs: cloneAutomationStringMap(request.SubjectAttrs),
	}
	if !request.ReplayOnly {
		command.EffectiveVersion = infrafleetdb.AutomationCatalogGuard{
			BindingID: request.TriggerBindingID, DriverID: request.DriverID, VersionID: request.DriverVersionID,
			DriverRevision: request.DriverRevision, SourceDigest: request.SourceDigest, BundleDigest: request.BundleDigest,
		}
	}
	result, err := client.Automation().DispatchAutomationBinding(ctx, command)
	if err != nil && automationDispatchRetryable(ctx, err) {
		result, err = client.Automation().DispatchAutomationBinding(ctx, command)
	}
	if err != nil {
		return nil, translateAutomationExecutionFleetDBError(err)
	}
	if result == nil {
		return nil, automation.ErrInvalidPersistedState
	}
	mapped := &automation.ExecutionDispatchResult{Replayed: result.Replayed}
	switch result.Outcome {
	case infrafleetdb.AutomationDeliveryDispatchBusy:
		mapped.Busy = true
		mapped.BusyRunID = result.BusyRunID
	case infrafleetdb.AutomationDeliveryDispatchRun,
		infrafleetdb.AutomationDeliveryDispatchReused,
		infrafleetdb.AutomationDeliveryDispatchSuperseded:
		if result.DriverRun == nil {
			return nil, automation.ErrInvalidPersistedState
		}
		mapped.RunID = result.DriverRun.RunID
		mapped.RunSnapshot = append([]byte(nil), result.DriverRunSnapshot...)
	default:
		return nil, automation.ErrInvalidPersistedState
	}
	return mapped, nil
}

func automationDispatchRetryable(ctx context.Context, err error) bool {
	if err == nil || ctx == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return !isStableAutomationFleetDBError(err)
}

func isStableAutomationFleetDBError(err error) bool {
	stable := []error{
		infrafleetdb.ErrAutomationInvalid,
		infrafleetdb.ErrAutomationRouteNotFound,
		infrafleetdb.ErrAutomationParentRunNotFound,
		infrafleetdb.ErrAutomationIdempotencyConflict,
		infrafleetdb.ErrAutomationBindingSnapshotConflict,
		infrafleetdb.ErrAutomationCatalogSnapshotConflict,
		infrafleetdb.ErrAutomationHopDepthExceeded,
		infrafleetdb.ErrAutomationCatalogUnavailable,
		infrafleetdb.ErrAutomationFanoutLimitExceeded,
		infrafleetdb.ErrAutomationAdmissionUnavailable,
		infrafleetdb.ErrAutomationDeliveryNotFound,
		infrafleetdb.ErrAutomationDeliveryNotDispatchable,
		infrafleetdb.ErrAutomationDeliveryTransitionConflict,
		infrafleetdb.ErrAutomationPayloadDigestMismatch,
		infrafleetdb.ErrAutomationBindingNotFound,
		infrafleetdb.ErrAutomationBindingDispatchReplayNotFound,
		domain.ErrNotFound,
		domain.ErrAlreadyExists,
		domain.ErrAlreadyClaimed,
		domain.ErrInvalidTransition,
		domain.ErrInvalid,
		domain.ErrConflict,
		domain.ErrGone,
		domain.ErrNotOwner,
	}
	for _, sentinel := range stable {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}

func translateAutomationExecutionFleetDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var mapped error
	switch {
	case errors.Is(err, infrafleetdb.ErrAutomationBindingDispatchReplayNotFound):
		mapped = automation.ErrDispatchReplayNotFound
	case errors.Is(err, infrafleetdb.ErrAutomationDeliveryNotFound),
		errors.Is(err, infrafleetdb.ErrAutomationBindingNotFound), errors.Is(err, domain.ErrNotFound):
		mapped = automation.ErrNotFound
	case errors.Is(err, infrafleetdb.ErrAutomationInvalid), errors.Is(err, domain.ErrInvalid):
		mapped = automation.ErrInvalid
	case errors.Is(err, infrafleetdb.ErrAutomationIdempotencyConflict),
		errors.Is(err, infrafleetdb.ErrAutomationBindingSnapshotConflict),
		errors.Is(err, infrafleetdb.ErrAutomationCatalogSnapshotConflict),
		errors.Is(err, infrafleetdb.ErrAutomationDeliveryNotDispatchable),
		errors.Is(err, infrafleetdb.ErrAutomationDeliveryTransitionConflict),
		errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrAlreadyClaimed),
		errors.Is(err, domain.ErrInvalidTransition), errors.Is(err, domain.ErrConflict):
		mapped = automation.ErrConflict
	default:
		mapped = automation.ErrUnavailable
	}
	return errors.Join(mapped, err)
}

func (port *automationExecutionPort) EmissionContext(ctx context.Context, auth authority.ExecutionAuthority) (*automation.ExecutionEmissionContext, error) {
	if port == nil || port.runs == nil {
		return nil, automation.ErrUnavailable
	}
	if ctx == nil {
		return nil, fmt.Errorf("execution emission context is required: %w", automation.ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workspace, runID, err := validateAutomationEmissionAuthority(auth)
	if err != nil {
		return nil, err
	}
	run, err := port.runs.Get(ctx, workspace, runID)
	if err != nil {
		return nil, fmt.Errorf("load execution emission run: %w", err)
	}
	if err := validateAutomationEmissionRun(run, workspace, runID, auth); err != nil {
		return nil, err
	}
	epicID := strings.TrimSpace(run.EpicID)
	if epicID == "" {
		epicID = strings.TrimSpace(driverpkg.DriverRunPayloadEpicID(run.Payload))
	}
	return &automation.ExecutionEmissionContext{
		WorkspaceKey:  workspace,
		RunID:         runID,
		NodeID:        run.NodeID,
		LeaseID:       run.LeaseID,
		ParentEventID: strings.TrimSpace(run.SourceRef),
		ActorRef:      driverpkg.DriverRunActor(runID),
		EpicID:        epicID,
		FencingToken:  run.FencingToken,
	}, nil
}

func validateAutomationEmissionAuthority(auth authority.ExecutionAuthority) (string, string, error) {
	workspace := strings.TrimSpace(auth.Workspace())
	runID := strings.TrimSpace(auth.Subject())
	if auth.Action() != automation.ActionAdmitEvent || workspace == "" || workspace != auth.Workspace() ||
		runID == "" || runID != auth.Subject() || strings.TrimSpace(auth.NodeID()) == "" ||
		strings.TrimSpace(auth.LeaseID()) == "" || auth.FencingToken() <= 0 {
		return "", "", authority.ErrAdmissionDenied
	}
	return workspace, runID, nil
}

func validateAutomationEmissionRun(
	run *domain.DriverRun,
	workspace, runID string,
	auth authority.ExecutionAuthority,
) error {
	if run == nil || run.WorkspaceKey != workspace || run.RunID != runID ||
		run.Status != domain.DriverRunRunning || strings.TrimSpace(run.NodeID) == "" ||
		strings.TrimSpace(run.LeaseID) == "" || run.FencingToken <= 0 ||
		run.NodeID != auth.NodeID() || run.LeaseID != auth.LeaseID() || run.FencingToken != auth.FencingToken() {
		return fmt.Errorf("execution emission run is not a fenced running owner: %w", automation.ErrInvalidPersistedState)
	}
	return nil
}

func (port *automationExecutionPort) Dispatch(ctx context.Context, request automation.ExecutionDispatchRequest) (*automation.ExecutionDispatchResult, error) {
	if port == nil || port.dispatch == nil {
		return nil, automation.ErrUnavailable
	}
	if ctx == nil {
		return nil, fmt.Errorf("execution dispatch context is required: %w", automation.ErrInvalid)
	}
	return port.dispatch(ctx, request)
}
