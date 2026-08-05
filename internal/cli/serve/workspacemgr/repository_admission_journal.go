package workspacemgr

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr/admissionstore"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

var (
	errLocalRepositoryAdmissionNotFound = admissionstore.ErrLocalRepositoryAdmissionNotFound
	errLocalRepositoryAdmissionConflict = admissionstore.ErrLocalRepositoryAdmissionConflict
)

type (
	localRepositoryAdmissionKind   = admissionstore.LocalRepositoryAdmissionKind
	localRepositoryAdmissionIntent = admissionstore.LocalRepositoryAdmissionIntent
	localRepositoryAdmissionRecord = admissionstore.LocalRepositoryAdmissionRecord
	repositoryAdmissionCoordinate  = admissionstore.RepositoryAdmissionCoordinate
)

const (
	localRepositoryAdmissionCreateWorkspace = admissionstore.LocalRepositoryAdmissionCreateWorkspace
	localRepositoryAdmissionAddRepositories = admissionstore.LocalRepositoryAdmissionAddRepositories
)

// RepositoryAdmissionJournal preserves the Workspace Manager composition API
// while the protected journal, lock, and process-local authority implementation
// lives with the admission persistence boundary.
type RepositoryAdmissionJournal struct {
	inner *admissionstore.RepositoryAdmissionJournal
}

func NewRepositoryAdmissionJournal() (*RepositoryAdmissionJournal, error) {
	inner, err := admissionstore.NewRepositoryAdmissionJournal()
	if err != nil {
		return nil, err
	}
	return &RepositoryAdmissionJournal{inner: inner}, nil
}

func newLocalRepositoryAdmissionJournalAt(
	dir string,
	now func() time.Time,
) (*RepositoryAdmissionJournal, error) {
	inner, err := admissionstore.NewRepositoryAdmissionJournalAt(dir, now)
	if err != nil {
		return nil, err
	}
	return &RepositoryAdmissionJournal{inner: inner}, nil
}

func normalizeLocalRepositoryAdmissionID(value string) (string, error) {
	return admissionstore.NormalizeLocalRepositoryAdmissionID(value)
}

func (journal *RepositoryAdmissionJournal) admissionStore() *admissionstore.RepositoryAdmissionJournal {
	if journal == nil {
		return nil
	}
	return journal.inner
}

func (journal *RepositoryAdmissionJournal) Prepare(
	ctx context.Context,
	intent localRepositoryAdmissionIntent,
) (localRepositoryAdmissionRecord, error) {
	return journal.admissionStore().Prepare(ctx, intent)
}

func (journal *RepositoryAdmissionJournal) Bind(
	ctx context.Context,
	operationID,
	admissionID,
	specFingerprint string,
) (localRepositoryAdmissionRecord, error) {
	return journal.admissionStore().Bind(ctx, operationID, admissionID, specFingerprint)
}

func (journal *RepositoryAdmissionJournal) GetByOperation(
	ctx context.Context,
	operationID string,
) (localRepositoryAdmissionRecord, error) {
	return journal.admissionStore().GetByOperation(ctx, operationID)
}

func (journal *RepositoryAdmissionJournal) GetByAdmission(
	ctx context.Context,
	admissionID string,
) (localRepositoryAdmissionRecord, error) {
	return journal.admissionStore().GetByAdmission(ctx, admissionID)
}

func (journal *RepositoryAdmissionJournal) ResolveLocalRepositoryAdmission(
	ctx context.Context,
	admissionID string,
) (sourcecontrol.RepositoryAdmissionLocalProjection, error) {
	return journal.admissionStore().ResolveLocalRepositoryAdmission(ctx, admissionID)
}

func (journal *RepositoryAdmissionJournal) List(
	ctx context.Context,
) ([]localRepositoryAdmissionRecord, error) {
	return journal.admissionStore().List(ctx)
}

func (journal *RepositoryAdmissionJournal) Remove(
	ctx context.Context,
	operationID string,
) error {
	return journal.admissionStore().Remove(ctx, operationID)
}

func (journal *RepositoryAdmissionJournal) AcquireMaterializationLock(
	ctx context.Context,
	admissionID string,
	targets []string,
) (func(), error) {
	return journal.admissionStore().AcquireMaterializationLock(ctx, admissionID, targets)
}

func (journal *RepositoryAdmissionJournal) activateMaterializationAuthority(
	coordinate repositoryAdmissionCoordinate,
	deadline time.Time,
) error {
	return journal.admissionStore().ActivateMaterializationAuthority(coordinate, deadline)
}

func (journal *RepositoryAdmissionJournal) renewMaterializationAuthority(
	coordinate repositoryAdmissionCoordinate,
	deadline time.Time,
) bool {
	return journal.admissionStore().RenewMaterializationAuthority(coordinate, deadline)
}

func (journal *RepositoryAdmissionJournal) deactivateMaterializationAuthority(
	coordinate repositoryAdmissionCoordinate,
) {
	journal.admissionStore().DeactivateMaterializationAuthority(coordinate)
}

func (journal *RepositoryAdmissionJournal) deactivateAllMaterializationAuthorities() {
	journal.admissionStore().DeactivateAllMaterializationAuthorities()
}

func (journal *RepositoryAdmissionJournal) materializationAuthorityDeadline(
	admissionID string,
) (time.Time, bool) {
	return journal.admissionStore().MaterializationAuthorityDeadline(admissionID)
}

func (journal *RepositoryAdmissionJournal) materializationAuthorityCount() int {
	return journal.admissionStore().MaterializationAuthorityCount()
}
