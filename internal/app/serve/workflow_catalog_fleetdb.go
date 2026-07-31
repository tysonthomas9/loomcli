package serve

import (
	"context"
	"errors"

	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	catalogfleetdb "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/fleetdb"
)

// workflowCatalogFleetDBTransport is the composition-owned bridge between the
// shared low-level FleetDB client and the capability adapter's owned transport
// contract. It translates DTOs and error vocabularies without constructing a
// second client or adding policy.
type workflowCatalogFleetDBTransport struct {
	transport infrafleetdb.WorkflowCatalogTransport
}

var _ catalogfleetdb.Transport = (*workflowCatalogFleetDBTransport)(nil)
var _ catalogfleetdb.AuthoringTransport = (*workflowCatalogFleetDBTransport)(nil)

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
		result, err = t.transport.AuthorManagedDriverVersion(
			ctx,
			infrafleetdb.WorkflowCatalogAuthorManagedVersionInput{
				WorkflowCatalogAuthorVersionInput: input,
				Activate:                          mutation.Activate,
			},
		)
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
		ReusedVersion: result.ReusedVersion, Activated: result.Activated,
		Replayed: result.Replayed, CommittedRevision: result.CommittedRevision,
		SemanticImpact: result.SemanticImpact,
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
	case errors.Is(err, infrafleetdb.ErrWorkflowCatalogVersionNotApproved):
		translated = catalogfleetdb.ErrTransportVersionNotApproved
	case errors.Is(err, infrafleetdb.ErrWorkflowCatalogAuthoringConflict):
		translated = catalogfleetdb.ErrTransportAuthoringConflict
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
