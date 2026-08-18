package serve

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/app/workitemmove"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	catalogfleetdb "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// workItemMoveFleetDBAdapter is the one-write bridge for the named
// cross-workspace workflow. It lives in the existing process-composition
// package so atomic movement does not create another repository package.
type workItemMoveFleetDBAdapter struct {
	transport infrafleetdb.WorkItemMoveTransport
}

var _ workitemmove.AtomicMover = (*workItemMoveFleetDBAdapter)(nil)

func newWorkItemMoveFleetDBAdapter(
	transport infrafleetdb.WorkItemMoveTransport,
) (*workItemMoveFleetDBAdapter, error) {
	if transport == nil {
		return nil, fmt.Errorf("compose WorkItemMove FleetDB adapter: %w", workitemmove.ErrUnavailable)
	}
	return &workItemMoveFleetDBAdapter{transport: transport}, nil
}

func (adapter *workItemMoveFleetDBAdapter) MoveAtomic(
	ctx context.Context,
	command workitemmove.AtomicCommand,
) (*workitemmove.AtomicResult, error) {
	value, err := adapter.transport.MoveWorkItem(
		ctx,
		command.SourceWorkspace,
		command.SourceIssueID,
		infrafleetdb.WorkItemMoveInput{
			TargetWorkspace: command.TargetWorkspace, ExpectedSourceRevision: command.ExpectedSourceRevision,
			RequestID: command.RequestID,
		},
	)
	if err != nil {
		return nil, translateWorkItemMoveFleetDBError(err)
	}
	if value == nil || value.Source == nil || value.Target == nil {
		return nil, fmt.Errorf("atomic move returned incomplete owner state: %w", workitemmove.ErrInvalidPersistedState)
	}
	return &workitemmove.AtomicResult{
		Source:   workitemmove.Reference{Workspace: value.Source.Workspace, IssueID: value.Source.ID},
		Target:   workitemmove.Reference{Workspace: value.Target.Workspace, IssueID: value.Target.ID},
		Replayed: value.Replayed,
	}, nil
}

func translateWorkItemMoveFleetDBError(err error) error {
	switch {
	case errors.Is(err, persistence.ErrInvalid):
		return workitemmove.AdapterInvalid("move atomic", "FleetDB rejected the move intent")
	case errors.Is(err, persistence.ErrNotFound):
		return workitemmove.AdapterNotFound("move atomic", "source Work Item or target workspace was not found")
	case errors.Is(err, infrafleetdb.ErrWorkItemMoveRevisionConflict):
		return workitemmove.AdapterConflict("move atomic", "the source Work Item changed; refresh and try again")
	case errors.Is(err, infrafleetdb.ErrWorkItemMoveIdempotencyConflict):
		return workitemmove.AdapterConflict("move atomic", "the move request id was already used for a different intent")
	case errors.Is(err, infrafleetdb.ErrWorkItemMoveIneligible):
		return workitemmove.AdapterConflict("move atomic", "the Work Item is assigned, active, claimed, or has dependencies and cannot be moved")
	case errors.Is(err, infrafleetdb.ErrWorkItemMoveForbidden):
		return fmt.Errorf("source or target workspace access was denied: %w", workitemmove.ErrForbidden)
	case errors.Is(err, persistence.ErrConflict), errors.Is(err, persistence.ErrAlreadyExists), errors.Is(err, persistence.ErrNotOwner):
		return workitemmove.AdapterConflict("move atomic", "FleetDB rejected the move because owner state conflicts")
	case errors.Is(err, context.DeadlineExceeded):
		return workitemmove.AdapterTimeout("move atomic", "FleetDB timed out", err)
	default:
		return workitemmove.AdapterUnavailable("move atomic", "FleetDB atomic move is unavailable", err)
	}
}

// workflowCatalogFleetDBTransport is the composition-owned bridge between the
// shared low-level FleetDB client and the capability adapter's owned transport
// contract. It translates DTOs and error vocabularies without constructing a
// second client or adding policy.
type workflowCatalogFleetDBTransport struct {
	transport infrafleetdb.WorkflowCatalogTransport
}

var _ catalogfleetdb.Transport = (*workflowCatalogFleetDBTransport)(nil)
var _ catalogfleetdb.AuthoringTransport = (*workflowCatalogFleetDBTransport)(nil)
var _ catalogfleetdb.AvailabilityTransport = (*workflowCatalogFleetDBTransport)(nil)

func newWorkflowCatalogFleetDBTransport(client *infrafleetdb.Client) *workflowCatalogFleetDBTransport {
	if client == nil {
		return nil
	}
	return &workflowCatalogFleetDBTransport{transport: client.WorkflowCatalog()}
}

func (t *workflowCatalogFleetDBTransport) GetDriver(ctx context.Context, workspace, driverID string) (*workflowcatalog.Driver, error) {
	value, err := t.transport.GetDriver(ctx, workspace, driverID)
	return value, translateWorkflowCatalogFleetDBError(err)
}

func (t *workflowCatalogFleetDBTransport) FindDriverByName(ctx context.Context, workspace, name string) (*workflowcatalog.Driver, error) {
	value, err := t.transport.FindDriverByName(ctx, workspace, name)
	return value, translateWorkflowCatalogFleetDBError(err)
}

func (t *workflowCatalogFleetDBTransport) ListDrivers(ctx context.Context, workspace string) ([]*workflowcatalog.Driver, error) {
	values, err := t.transport.ListDrivers(ctx, workspace)
	return values, translateWorkflowCatalogFleetDBError(err)
}

func (t *workflowCatalogFleetDBTransport) GetVersion(ctx context.Context, workspace, versionID string) (*workflowcatalog.DriverVersion, error) {
	value, err := t.transport.GetVersion(ctx, workspace, versionID)
	return value, translateWorkflowCatalogFleetDBError(err)
}

func (t *workflowCatalogFleetDBTransport) ListVersions(ctx context.Context, workspace, driverID string) ([]*workflowcatalog.DriverVersion, error) {
	values, err := t.transport.ListVersions(ctx, workspace, driverID)
	return values, translateWorkflowCatalogFleetDBError(err)
}

func (t *workflowCatalogFleetDBTransport) ApproveVersion(ctx context.Context, workspace, driverID, versionID string, expectedRevision uint64) (*catalogfleetdb.TransportLifecycleResult, error) {
	result, err := t.transport.ApproveVersion(ctx, workspace, driverID, versionID, expectedRevision)
	return translateWorkflowCatalogFleetDBResult(result, err)
}

func (t *workflowCatalogFleetDBTransport) UnapproveVersion(ctx context.Context, workspace, driverID, versionID string, expectedRevision uint64) (*catalogfleetdb.TransportLifecycleResult, error) {
	result, err := t.transport.UnapproveVersion(ctx, workspace, driverID, versionID, expectedRevision)
	return translateWorkflowCatalogFleetDBResult(result, err)
}

func (t *workflowCatalogFleetDBTransport) ActivateVersion(ctx context.Context, workspace, driverID, versionID string, expectedRevision uint64) (*catalogfleetdb.TransportLifecycleResult, error) {
	result, err := t.transport.ActivateVersion(ctx, workspace, driverID, versionID, expectedRevision)
	return translateWorkflowCatalogFleetDBResult(result, err)
}

func (t *workflowCatalogFleetDBTransport) AuthorVersion(
	ctx context.Context,
	mutation workflowcatalog.AuthoringMutation,
) (*catalogfleetdb.TransportAuthoringResult, error) {
	input := infrafleetdb.WorkflowCatalogAuthorVersionInput{
		WorkspaceKey: mutation.WorkspaceKey, DriverID: mutation.DriverID,
		DelegatedActor: mutation.AuditActor,
		RequestID:      mutation.RequestID, ExpectedRevision: mutation.ExpectedRevision,
		DriverName: mutation.DriverName, VersionID: mutation.VersionID,
		SourceRef: mutation.SourceRef, SourceDigest: mutation.SourceDigest,
		BundleRef: mutation.BundleRef, BundleDigest: mutation.BundleDigest,
		Runtime: mutation.Runtime, Manifest: cloneWorkflowCatalogBridgeMap(mutation.Manifest),
		BuildDiagnostics: mutation.BuildDiagnostics,
	}
	var (
		result *infrafleetdb.WorkflowCatalogAuthorVersionResult
		err    error
	)
	if mutation.Managed {
		result, err = t.transport.AuthorManagedDriverVersion(ctx, input)
	} else {
		result, err = t.transport.AuthorDriverVersion(ctx, input)
	}
	return translateWorkflowCatalogFleetDBAuthoringResult(result, err)
}

func translateWorkflowCatalogFleetDBResult(result *infrafleetdb.WorkflowCatalogLifecycleResult, err error) (*catalogfleetdb.TransportLifecycleResult, error) {
	if err != nil {
		return nil, translateWorkflowCatalogFleetDBError(err)
	}
	if result == nil {
		return nil, nil
	}
	return &catalogfleetdb.TransportLifecycleResult{
		Driver:            result.Driver,
		Version:           result.Version,
		Replayed:          result.Replayed,
		CommittedRevision: result.CommittedRevision,
		SemanticImpact:    result.SemanticImpact,
	}, nil
}

func translateWorkflowCatalogFleetDBAuthoringResult(
	result *infrafleetdb.WorkflowCatalogAuthorVersionResult,
	err error,
) (*catalogfleetdb.TransportAuthoringResult, error) {
	if err != nil {
		return nil, translateWorkflowCatalogFleetDBError(err)
	}
	if result == nil {
		return nil, nil
	}
	return &catalogfleetdb.TransportAuthoringResult{
		Driver: result.Driver, Version: result.Version,
		CreatedDriver: result.CreatedDriver, CreatedVersion: result.CreatedVersion,
		ReusedVersion: result.ReusedVersion,
		Replayed:      result.Replayed, CommittedRevision: result.CommittedRevision,
		SemanticImpact: result.SemanticImpact,
	}, nil
}

func (t *workflowCatalogFleetDBTransport) RecordVersionAvailability(
	ctx context.Context,
	mutation workflowcatalog.AvailabilityMutation,
) (*catalogfleetdb.TransportAvailabilityResult, error) {
	result, err := t.transport.RecordVersionAvailability(ctx, infrafleetdb.WorkflowCatalogAvailabilityInput{
		WorkspaceKey: mutation.WorkspaceKey, DriverID: mutation.DriverID, VersionID: mutation.VersionID,
		DelegatedActor: mutation.AuditActor, RequestID: mutation.RequestID,
		ExpectedRevision: mutation.ExpectedRevision, SourceDigest: mutation.SourceDigest,
		BundleDigest: mutation.BundleDigest, Outcome: mutation.Outcome, Failure: mutation.Failure,
	})
	if err != nil {
		return nil, translateWorkflowCatalogFleetDBError(err)
	}
	if result == nil {
		return nil, nil
	}
	return &catalogfleetdb.TransportAvailabilityResult{
		Driver: result.Driver, Version: result.Version, Replayed: result.Replayed,
		CommittedRevision: result.CommittedRevision, SemanticImpact: result.SemanticImpact,
	}, nil
}

func translateWorkflowCatalogFleetDBError(err error) error {
	if err == nil {
		return nil
	}
	var translated error
	switch {
	case errors.Is(err, infrafleetdb.ErrWorkflowCatalogNotFound):
		translated = catalogfleetdb.ErrTransportNotFound
	case errors.Is(err, infrafleetdb.ErrWorkflowCatalogRevisionConflict):
		translated = catalogfleetdb.ErrTransportRevisionConflict
	case errors.Is(err, infrafleetdb.ErrWorkflowCatalogVersionOwnership):
		translated = catalogfleetdb.ErrTransportVersionOwnership
	case errors.Is(err, infrafleetdb.ErrWorkflowCatalogVersionNotValidated):
		translated = catalogfleetdb.ErrTransportVersionNotValidated
	case errors.Is(err, infrafleetdb.ErrWorkflowCatalogVersionNotAvailable):
		translated = catalogfleetdb.ErrTransportVersionNotAvailable
	case errors.Is(err, infrafleetdb.ErrWorkflowCatalogVersionNotApproved):
		translated = catalogfleetdb.ErrTransportVersionNotApproved
	case errors.Is(err, infrafleetdb.ErrWorkflowCatalogAuthoringConflict):
		translated = catalogfleetdb.ErrTransportAuthoringConflict
	case errors.Is(err, infrafleetdb.ErrWorkflowCatalogAvailabilityConflict):
		translated = catalogfleetdb.ErrTransportAvailabilityConflict
	case errors.Is(err, infrafleetdb.ErrWorkflowCatalogInvalid):
		translated = catalogfleetdb.ErrTransportInvalid
	default:
		return err
	}
	return errors.Join(translated, err)
}

func cloneWorkflowCatalogBridgeMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	out := make(map[string]string, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}
