package repositoryadmissioninfra

import (
	"context"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/app/repositoryadmission"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
)

// NewRepositoryAdmissionDurability maps the shared FleetDB wire transport onto Repository Admission's
// transport-neutral durability port.
func NewRepositoryAdmissionDurability(transport infrafleetdb.RepositoryAdmissionTransport) repositoryadmission.DurableAdmissions {
	if transport == nil {
		return nil
	}
	return &repositoryAdmissionFleetDB{transport: transport}
}

type repositoryAdmissionFleetDB struct {
	transport infrafleetdb.RepositoryAdmissionTransport
}

func (a *repositoryAdmissionFleetDB) CreateWorkspace(ctx context.Context, input repositoryadmission.WorkspaceBegin) (*repositoryadmission.WorkspaceBeginResult, error) {
	result, err := a.transport.CreateWorkspaceWithRepositoryAdmission(ctx, infrafleetdb.WorkspaceRepositoryAdmissionBeginInput{
		Workspace: infrafleetdb.RepositoryAdmissionWorkspaceInput{
			Key: input.Workspace.Key, Name: input.Workspace.Name,
			Description: input.Workspace.Description, State: input.Workspace.State,
			ErrorMessage:  input.Workspace.ErrorMessage,
			DefaultBranch: input.Workspace.DefaultBranch,
			DesignFormat:  input.Workspace.DesignFormat,
		},
		OperationID: input.OperationID, OwnerID: input.OwnerID,
		OwnerLease: input.OwnerLease, Repositories: toFleetSpecs(input.Repositories),
	})
	if err != nil {
		return nil, mapError(err)
	}
	if result == nil {
		return nil, nil
	}
	return &repositoryadmission.WorkspaceBeginResult{
		Workspace: result.Workspace, Admission: fromFleetRecord(result.Admission),
		WorkspaceEventID: result.WorkspaceEventID,
	}, nil
}

func (a *repositoryAdmissionFleetDB) Begin(ctx context.Context, workspace string, input repositoryadmission.Begin) (*repositoryadmission.Record, error) {
	record, err := a.transport.BeginRepositoryAdmission(ctx, workspace, infrafleetdb.RepositoryAdmissionBeginInput{
		OperationID: input.OperationID, OwnerID: input.OwnerID,
		OwnerLease: input.OwnerLease, Repositories: toFleetSpecs(input.Repositories),
	})
	return fromFleetRecord(record), mapError(err)
}

func (a *repositoryAdmissionFleetDB) Get(ctx context.Context, workspace, admissionID string) (*repositoryadmission.Record, error) {
	record, err := a.transport.GetRepositoryAdmission(ctx, workspace, admissionID)
	return fromFleetRecord(record), mapError(err)
}

func (a *repositoryAdmissionFleetDB) GetByOperation(ctx context.Context, workspace, operationID string) (*repositoryadmission.Record, error) {
	record, err := a.transport.GetRepositoryAdmissionByOperation(ctx, workspace, operationID)
	return fromFleetRecord(record), mapError(err)
}

func (a *repositoryAdmissionFleetDB) ListRecoverable(ctx context.Context, workspace string, limit int) ([]*repositoryadmission.Record, error) {
	records, err := a.transport.ListRecoverableRepositoryAdmissions(ctx, workspace, limit)
	if err != nil {
		return nil, mapError(err)
	}
	result := make([]*repositoryadmission.Record, 0, len(records))
	for _, record := range records {
		result = append(result, fromFleetRecord(record))
	}
	return result, nil
}

func (a *repositoryAdmissionFleetDB) Renew(ctx context.Context, input repositoryadmission.Renew) (*repositoryadmission.Record, error) {
	record, err := a.transport.RenewRepositoryAdmission(ctx, infrafleetdb.RepositoryAdmissionRenewInput{
		RepositoryAdmissionGuard: toFleetGuard(input.Guard), Lease: input.Lease,
	})
	return fromFleetRecord(record), mapError(err)
}

func (a *repositoryAdmissionFleetDB) ClaimRecovery(ctx context.Context, input repositoryadmission.RecoveryClaim) (*repositoryadmission.Record, error) {
	record, err := a.transport.ClaimRepositoryAdmissionRecovery(ctx, infrafleetdb.RepositoryAdmissionRecoveryClaimInput{
		WorkspaceKey: input.WorkspaceKey, AdmissionID: input.AdmissionID,
		ExpectedSpecFingerprint: input.ExpectedSpecFingerprint,
		ExpectedVersion:         input.ExpectedVersion, NewOwnerID: input.NewOwnerID,
		Lease: input.Lease,
	})
	return fromFleetRecord(record), mapError(err)
}

func (a *repositoryAdmissionFleetDB) Commit(ctx context.Context, input repositoryadmission.Commit) (*repositoryadmission.Record, error) {
	branches := make([]infrafleetdb.RepositoryAdmissionResolvedBranch, 0, len(input.ResolvedDefaultBranches))
	for _, branch := range input.ResolvedDefaultBranches {
		branches = append(branches, infrafleetdb.RepositoryAdmissionResolvedBranch{Name: branch.Name, DefaultBranch: branch.DefaultBranch})
	}
	var finalization *infrafleetdb.RepositoryAdmissionWorkspaceFinalization
	if input.WorkspaceFinalization != nil {
		finalization = &infrafleetdb.RepositoryAdmissionWorkspaceFinalization{
			State:         input.WorkspaceFinalization.State,
			DefaultBranch: input.WorkspaceFinalization.DefaultBranch,
		}
	}
	record, err := a.transport.CommitRepositoryAdmission(ctx, infrafleetdb.RepositoryAdmissionCommitInput{
		RepositoryAdmissionGuard: toFleetGuard(input.Guard),
		ResolvedDefaultBranches:  branches, WorkspaceFinalization: finalization,
	})
	return fromFleetRecord(record), mapError(err)
}

func (a *repositoryAdmissionFleetDB) Fail(ctx context.Context, input repositoryadmission.Fail) (*repositoryadmission.Record, error) {
	record, err := a.transport.FailRepositoryAdmission(ctx, infrafleetdb.RepositoryAdmissionFailInput{
		RepositoryAdmissionGuard: toFleetGuard(input.Guard),
		ErrorClass:               input.ErrorClass, Retryable: input.Retryable,
	})
	return fromFleetRecord(record), mapError(err)
}

func (a *repositoryAdmissionFleetDB) Abort(ctx context.Context, input repositoryadmission.Abort) (*repositoryadmission.Record, error) {
	record, err := a.transport.AbortRepositoryAdmission(ctx, infrafleetdb.RepositoryAdmissionAbortInput{
		RepositoryAdmissionGuard: toFleetGuard(input.Guard), ReasonClass: input.ReasonClass,
	})
	return fromFleetRecord(record), mapError(err)
}

func toFleetGuard(guard repositoryadmission.Guard) infrafleetdb.RepositoryAdmissionGuard {
	return infrafleetdb.RepositoryAdmissionGuard{
		WorkspaceKey: guard.WorkspaceKey, AdmissionID: guard.AdmissionID,
		OwnerID: guard.OwnerID, OwnerGenerationID: guard.OwnerGenerationID,
		SpecFingerprint: guard.SpecFingerprint, ExpectedVersion: guard.ExpectedVersion,
	}
}

func toFleetSpecs(specs []repositoryadmission.RepositorySpec) []infrafleetdb.RepositoryAdmissionRepoSpec {
	result := make([]infrafleetdb.RepositoryAdmissionRepoSpec, 0, len(specs))
	for _, spec := range specs {
		result = append(result, infrafleetdb.RepositoryAdmissionRepoSpec{
			Name: spec.Name, RemoteURL: spec.RemoteURL, Remote: spec.Remote,
			DefaultBranch: spec.DefaultBranch, Groups: append([]string(nil), spec.Groups...),
			SourceRepoID: spec.SourceRepoID,
		})
	}
	return result
}

func fromFleetRecord(record *infrafleetdb.RepositoryAdmissionRecord) *repositoryadmission.Record {
	if record == nil {
		return nil
	}
	result := &repositoryadmission.Record{
		AdmissionID: record.AdmissionID, WorkspaceKey: record.WorkspaceKey,
		OperationID: record.OperationID, OwnerID: record.OwnerID,
		OwnerGenerationID:   record.OwnerGenerationID,
		OwnerLeaseExpiresAt: record.OwnerLeaseExpiresAt,
		SpecFingerprint:     record.SpecFingerprint,
		Spec: repositoryadmission.Spec{
			WorkspaceKey: record.Spec.WorkspaceKey, OperationID: record.Spec.OperationID,
			Repositories: fromFleetSpecs(record.Spec.Repositories),
		},
		State: record.State, LastErrorClass: record.LastErrorClass,
		Version: record.Version, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		TerminalAt: record.TerminalAt,
	}
	if record.Receipt != nil {
		receipt := &repositoryadmission.Receipt{
			AdmissionID:     record.Receipt.AdmissionID,
			SpecFingerprint: record.Receipt.SpecFingerprint,
			CommittedAt:     record.Receipt.CommittedAt,
			Repositories:    make([]repositoryadmission.RepositoryReceipt, 0, len(record.Receipt.Repositories)),
		}
		for _, repository := range record.Receipt.Repositories {
			receipt.Repositories = append(receipt.Repositories, repositoryadmission.RepositoryReceipt{
				Repository: repository.Repository, EventID: repository.EventID,
			})
		}
		if record.Receipt.WorkspaceFinalization != nil {
			receipt.WorkspaceFinalization = &repositoryadmission.WorkspaceFinalization{
				State:         record.Receipt.WorkspaceFinalization.State,
				DefaultBranch: record.Receipt.WorkspaceFinalization.DefaultBranch,
			}
		}
		result.Receipt = receipt
	}
	return result
}

func fromFleetSpecs(specs []infrafleetdb.RepositoryAdmissionRepoSpec) []repositoryadmission.RepositorySpec {
	result := make([]repositoryadmission.RepositorySpec, 0, len(specs))
	for _, spec := range specs {
		result = append(result, repositoryadmission.RepositorySpec{
			Name: spec.Name, RemoteURL: spec.RemoteURL, Remote: spec.Remote,
			DefaultBranch: spec.DefaultBranch, Groups: append([]string(nil), spec.Groups...),
			SourceRepoID: spec.SourceRepoID,
		})
	}
	return result
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	var sentinel error
	switch {
	case errors.Is(err, infrafleetdb.ErrRepositoryAdmissionUnavailable):
		sentinel = repositoryadmission.ErrUnavailable
	case errors.Is(err, infrafleetdb.ErrRepositoryAdmissionNotFound):
		sentinel = repositoryadmission.ErrNotFound
	case errors.Is(err, infrafleetdb.ErrRepositoryAdmissionInvalid):
		sentinel = repositoryadmission.ErrInvalid
	case errors.Is(err, infrafleetdb.ErrRepositoryAdmissionConflict):
		sentinel = repositoryadmission.ErrConflict
	case errors.Is(err, infrafleetdb.ErrRepositoryAdmissionFenceLost):
		sentinel = repositoryadmission.ErrFenceLost
	case errors.Is(err, infrafleetdb.ErrRepositoryAdmissionState):
		sentinel = repositoryadmission.ErrState
	default:
		return err
	}
	return errors.Join(sentinel, err)
}

var _ repositoryadmission.DurableAdmissions = (*repositoryAdmissionFleetDB)(nil)
