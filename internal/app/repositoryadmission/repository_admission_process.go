package repositoryadmission

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

const (
	repositoryAdmissionLease             = time.Minute
	repositoryAdmissionLeaseSafetyMargin = 5 * time.Second
)

type repositoryAdmissionProcess struct {
	admissions        DurableAdmissions
	journal           Journal
	ownerID           string
	now               func() time.Time
	leases            *repositoryAdmissionLeaseState
	ownerLease        time.Duration
	leaseSafetyMargin time.Duration
}

func newRepositoryAdmissionProcess(
	admissions DurableAdmissions,
	journal Journal,
) *repositoryAdmissionProcess {
	if admissions == nil || journal == nil {
		return nil
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil
	}
	return &repositoryAdmissionProcess{
		admissions: admissions, journal: journal,
		ownerID: "loom-workspace-admission-" + hex.EncodeToString(nonce[:]),
		now:     time.Now, leases: newRepositoryAdmissionLeaseState(),
		ownerLease:        repositoryAdmissionLease,
		leaseSafetyMargin: repositoryAdmissionLeaseSafetyMargin,
	}
}

func (process *repositoryAdmissionProcess) repositoryAdmissionOwnerLease() time.Duration {
	if process != nil && process.ownerLease > 0 {
		return process.ownerLease
	}
	return repositoryAdmissionLease
}

func repositoryAdmissionUnavailable() error {
	return workspacemodule.NewCreateError(
		workspacemodule.SecurityViolation,
		"repository admission requires the durable FleetDB and Source Control capabilities",
		errors.Join(
			ErrUnavailable,
			sourcecontrol.ErrUnavailable,
		),
	)
}

//nolint:funlen // Create preparation binds the complete workspace and repository intent before any external materialization begins.
func (process *repositoryAdmissionProcess) prepareCreate(
	ctx context.Context,
	workspace WorkspaceInput,
	workspacePath string,
	repositories []RepositorySpec,
	cloneURLs []string,
	branch string,
) (*Record, error) {
	if process == nil {
		return nil, repositoryAdmissionUnavailable()
	}
	if err := process.ensureMaterializationsRunning(); err != nil {
		return nil, err
	}
	operationID, err := OperationID(
		"create_workspace",
		workspace.Key,
		workspacePath,
		repositories,
	)
	if err != nil {
		return nil, err
	}
	local, err := process.journal.Prepare(ctx, LocalIntent{
		OperationID: operationID, WorkspaceKey: workspace.Key,
		WorkspaceName: workspace.Name, WorkspacePath: workspacePath,
		Kind:   KindCreateWorkspace,
		Branch: strings.TrimSpace(branch), CloneURLs: cloneURLs,
	})
	if err != nil {
		return nil, err
	}
	return process.prepare(
		ctx,
		local,
		func() (*Record, error) {
			result, createErr := process.admissions.
				CreateWorkspace(
					ctx,
					WorkspaceBegin{
						Workspace: workspace, OperationID: operationID,
						OwnerID:      process.ownerID,
						OwnerLease:   process.repositoryAdmissionOwnerLease(),
						Repositories: repositories,
					},
				)
			if createErr != nil {
				return nil, createErr
			}
			if result == nil {
				return nil, ErrInvalid
			}
			return result.Admission, nil
		},
	)
}

func (process *repositoryAdmissionProcess) prepareExisting(
	ctx context.Context,
	workspaceKey,
	workspacePath string,
	repositories []RepositorySpec,
	cloneURLs,
	localRepoPaths []string,
	branch string,
) (*Record, error) {
	if process == nil {
		return nil, repositoryAdmissionUnavailable()
	}
	if err := process.ensureMaterializationsRunning(); err != nil {
		return nil, err
	}
	operationID, err := OperationID(
		"add_repositories",
		workspaceKey,
		workspacePath,
		repositories,
	)
	if err != nil {
		return nil, err
	}
	local, err := process.journal.Prepare(ctx, LocalIntent{
		OperationID: operationID, WorkspaceKey: workspaceKey,
		WorkspacePath: workspacePath,
		Kind:          KindAddRepositories,
		Branch:        strings.TrimSpace(branch),
		CloneURLs:     cloneURLs, LocalRepoPaths: localRepoPaths,
	})
	if err != nil {
		return nil, err
	}
	return process.prepare(
		ctx,
		local,
		func() (*Record, error) {
			return process.admissions.Begin(
				ctx,
				workspaceKey,
				Begin{
					OperationID: operationID, OwnerID: process.ownerID,
					OwnerLease:   process.repositoryAdmissionOwnerLease(),
					Repositories: repositories,
				},
			)
		},
	)
}

func (process *repositoryAdmissionProcess) prepare(
	ctx context.Context,
	local LocalRecord,
	begin func() (*Record, error),
) (*Record, error) {
	if process == nil || begin == nil {
		return nil, repositoryAdmissionUnavailable()
	}
	local, record, err := process.resolvePreparedAdmission(ctx, local, begin)
	if err != nil {
		return nil, err
	}
	if err := verifyPreparedRepositoryAdmission(local, record); err != nil {
		return nil, err
	}
	return process.ensureOwnership(ctx, record)
}

func (process *repositoryAdmissionProcess) resolvePreparedAdmission(
	ctx context.Context,
	local LocalRecord,
	begin func() (*Record, error),
) (LocalRecord, *Record, error) {
	if local.AdmissionID != "" {
		return process.resolveBoundAdmission(ctx, local, begin)
	}
	return process.resolveUnboundAdmission(ctx, local, begin)
}

func (process *repositoryAdmissionProcess) resolveBoundAdmission(
	ctx context.Context,
	local LocalRecord,
	begin func() (*Record, error),
) (LocalRecord, *Record, error) {
	record, err := process.admissions.Get(ctx, local.Intent.WorkspaceKey, local.AdmissionID)
	if !errors.Is(err, ErrNotFound) {
		return local, record, err
	}
	record, err = process.admissions.GetByOperation(
		ctx,
		local.Intent.WorkspaceKey,
		local.Intent.OperationID,
	)
	if errors.Is(err, ErrNotFound) {
		record, err = process.beginOrResolveConflict(ctx, local, begin)
	}
	if err != nil {
		return LocalRecord{}, nil, err
	}
	if record == nil {
		return local, nil, nil
	}
	rebound, err := process.journal.Rebind(
		ctx,
		local.Intent.OperationID,
		local.AdmissionID,
		local.SpecFingerprint,
		record.AdmissionID,
		record.SpecFingerprint,
	)
	return rebound, record, err
}

func (process *repositoryAdmissionProcess) resolveUnboundAdmission(
	ctx context.Context,
	local LocalRecord,
	begin func() (*Record, error),
) (LocalRecord, *Record, error) {
	record, err := process.admissions.GetByOperation(
		ctx,
		local.Intent.WorkspaceKey,
		local.Intent.OperationID,
	)
	if errors.Is(err, ErrNotFound) {
		record, err = process.beginOrResolveConflict(ctx, local, begin)
	}
	if errors.Is(err, ErrConflict) {
		// A definitive 409 plus an operation miss proves that this
		// machine's intent never acquired durable coordinates. Keeping
		// the unbound receipt would make recovery repeatedly replay a
		// conflict that it can never own.
		if removeErr := process.journal.Remove(ctx, local.Intent.OperationID); removeErr != nil {
			return LocalRecord{}, nil, errors.Join(err, removeErr)
		}
		return LocalRecord{}, nil, err
	}
	if err != nil || record == nil {
		return local, record, err
	}
	bound, err := process.journal.Bind(
		ctx,
		local.Intent.OperationID,
		record.AdmissionID,
		record.SpecFingerprint,
	)
	if err != nil || record.OwnerGenerationID != "" {
		return bound, record, err
	}
	record, err = process.admissions.Get(ctx, local.Intent.WorkspaceKey, record.AdmissionID)
	return bound, record, err
}

func (process *repositoryAdmissionProcess) beginOrResolveConflict(
	ctx context.Context,
	local LocalRecord,
	begin func() (*Record, error),
) (*Record, error) {
	record, err := begin()
	if !errors.Is(err, ErrConflict) {
		return record, err
	}
	beginErr := err
	record, err = process.admissions.GetByOperation(
		ctx,
		local.Intent.WorkspaceKey,
		local.Intent.OperationID,
	)
	if errors.Is(err, ErrNotFound) {
		return nil, beginErr
	}
	return record, err
}

func verifyPreparedRepositoryAdmission(
	local LocalRecord,
	record *Record,
) error {
	if record == nil ||
		local.AdmissionID != record.AdmissionID ||
		local.SpecFingerprint != record.SpecFingerprint ||
		local.Intent.WorkspaceKey != record.WorkspaceKey ||
		local.Intent.OperationID != record.OperationID {
		return fmt.Errorf(
			"repository admission returned divergent durable coordinates: %w",
			ErrInvalid,
		)
	}
	return nil
}

func (process *repositoryAdmissionProcess) ensureOwnership(
	ctx context.Context,
	record *Record,
) (*Record, error) {
	if process == nil || record == nil || process.leases == nil {
		return nil, repositoryAdmissionUnavailable()
	}
	current, err := process.getMatchingRepositoryAdmission(ctx, record)
	if err != nil {
		return nil, err
	}
	switch current.State {
	case "committed":
		return current, nil
	case "permanent_failed", "aborted":
		return nil, fmt.Errorf(
			"repository admission is terminal (%s): %w",
			current.State,
			ErrState,
		)
	case "pending", "retryable_failed":
		// Durable takeover is intentionally deferred until beginMaterialization
		// holds the cross-process admission and canonical-target locks. The
		// Fleet claim/renew then establishes the exact owner generation and
		// starts the local monotonic watchdog before any filesystem side effect.
		return current, nil
	default:
		return nil, ErrInvalid
	}
}

func exactRecoveredRepositoryAdmission(
	previous *Record,
	recovered *Record,
	ownerID string,
) bool {
	return sameRepositoryAdmissionImmutableRecord(previous, recovered) &&
		recovered.State == "pending" &&
		recovered.LastErrorClass == "" &&
		recovered.OwnerID == ownerID &&
		validRepositoryAdmissionGenerationID(recovered.OwnerGenerationID) &&
		recovered.OwnerGenerationID != previous.OwnerGenerationID &&
		recovered.Version == previous.Version+1 &&
		!recovered.UpdatedAt.Before(previous.UpdatedAt) &&
		recovered.OwnerLeaseExpiresAt.After(recovered.UpdatedAt) &&
		recovered.TerminalAt == nil &&
		recovered.Receipt == nil
}

func validRepositoryAdmissionGenerationID(value string) bool {
	if len(value) != 32 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16
}

func sameRepositoryAdmissionImmutableRecord(
	left *Record,
	right *Record,
) bool {
	if left == nil ||
		right == nil ||
		left.AdmissionID != right.AdmissionID ||
		left.WorkspaceKey != right.WorkspaceKey ||
		left.OperationID != right.OperationID ||
		left.SpecFingerprint != right.SpecFingerprint ||
		!left.CreatedAt.Equal(right.CreatedAt) ||
		left.Spec.WorkspaceKey != right.Spec.WorkspaceKey ||
		left.Spec.OperationID != right.Spec.OperationID ||
		len(left.Spec.Repositories) != len(right.Spec.Repositories) {
		return false
	}

	rightByName := make(
		map[string]RepositorySpec,
		len(right.Spec.Repositories),
	)
	for _, repository := range right.Spec.Repositories {
		if _, duplicate := rightByName[repository.Name]; duplicate {
			return false
		}
		rightByName[repository.Name] = repository
	}
	for _, repository := range left.Spec.Repositories {
		observed, exists := rightByName[repository.Name]
		if !exists ||
			repository.RemoteURL != observed.RemoteURL ||
			repository.Remote != observed.Remote ||
			repository.DefaultBranch != observed.DefaultBranch ||
			!sameRepositoryAdmissionGroups(
				repository.Groups,
				observed.Groups,
			) ||
			repository.SourceRepoID != observed.SourceRepoID {
			return false
		}
		delete(rightByName, repository.Name)
	}
	return len(rightByName) == 0
}

func sameRepositoryAdmissionGroups(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	return slices.Equal(left, right)
}

//nolint:funlen // Commit validates the exact owner generation and complete repository result before publishing terminal state.
func (process *repositoryAdmissionProcess) commit(
	ctx context.Context,
	record *Record,
	repositories []RepositoryPlacement,
	finalization *WorkspaceFinalization,
) (*Record, error) {
	if process == nil || record == nil {
		return nil, repositoryAdmissionUnavailable()
	}
	resolved := make([]ResolvedBranch, 0, len(repositories))
	for _, repository := range repositories {
		resolved = append(resolved, ResolvedBranch{
			Name: repository.Name, DefaultBranch: repository.DefaultBranch,
		})
	}
	if process.leases == nil {
		return nil, repositoryAdmissionUnavailable()
	}
	release, err := process.acquireRepositoryAdmissionMutation(
		ctx,
		record.AdmissionID,
	)
	if err != nil {
		return nil, err
	}
	defer release()

	current, err := process.getMatchingOwnedRepositoryAdmission(ctx, record)
	if err != nil {
		return nil, err
	}
	if current.State == "committed" {
		expectedVersion := record.Version + 1
		if record.State == "committed" {
			expectedVersion = record.Version
		}
		if exactCommittedRepositoryAdmission(
			record,
			current,
			resolved,
			finalization,
			expectedVersion,
		) {
			return current, nil
		}
		return nil, fmt.Errorf(
			"repository admission committed by a different owner generation or result: %w",
			ErrFenceLost,
		)
	}
	if current.OwnerLeaseExpiresAt.Sub(process.now()) <=
		repositoryAdmissionRenewalLead {
		current, err = process.renewOwnedRepositoryAdmissionLocked(ctx, current)
		if err != nil {
			return nil, err
		}
	}
	committed, err := process.admissions.Commit(
		ctx,
		Commit{
			Guard:                   repositoryAdmissionGuard(current),
			ResolvedDefaultBranches: resolved,
			WorkspaceFinalization:   finalization,
		},
	)
	if err == nil {
		if exactCommittedRepositoryAdmission(
			current,
			committed,
			resolved,
			finalization,
			current.Version+1,
		) {
			return committed, nil
		}
		return nil, fmt.Errorf(
			"repository admission commit returned a divergent owner generation or result: %w",
			ErrInvalid,
		)
	}
	observed, getErr := process.admissions.Get(
		ctx,
		current.WorkspaceKey,
		current.AdmissionID,
	)
	if getErr == nil && exactCommittedRepositoryAdmission(
		current,
		observed,
		resolved,
		finalization,
		current.Version+1,
	) {
		return observed, nil
	}
	return nil, err
}

//nolint:cyclop,funlen // Exact replay comparison intentionally checks every immutable, owner, and terminal repository-admission coordinate.
func exactCommittedRepositoryAdmission(
	attempted *Record,
	observed *Record,
	resolved []ResolvedBranch,
	finalization *WorkspaceFinalization,
	expectedVersion int64,
) bool {
	if attempted == nil ||
		observed == nil ||
		observed.State != "committed" ||
		observed.AdmissionID != attempted.AdmissionID ||
		observed.WorkspaceKey != attempted.WorkspaceKey ||
		observed.OperationID != attempted.OperationID ||
		observed.OwnerID != attempted.OwnerID ||
		observed.OwnerGenerationID != attempted.OwnerGenerationID ||
		observed.SpecFingerprint != attempted.SpecFingerprint ||
		observed.Version != expectedVersion ||
		observed.Receipt == nil ||
		observed.TerminalAt == nil ||
		observed.Receipt.AdmissionID != attempted.AdmissionID ||
		observed.Receipt.SpecFingerprint != attempted.SpecFingerprint ||
		!observed.Receipt.CommittedAt.Equal(*observed.TerminalAt) ||
		!sameRepositoryAdmissionWorkspaceFinalization(
			observed.Receipt.WorkspaceFinalization,
			finalization,
		) ||
		len(resolved) != len(attempted.Spec.Repositories) ||
		len(observed.Receipt.Repositories) != len(attempted.Spec.Repositories) {
		return false
	}

	resolvedByName := make(map[string]string, len(resolved))
	for _, branch := range resolved {
		if _, duplicate := resolvedByName[branch.Name]; duplicate {
			return false
		}
		resolvedByName[branch.Name] = branch.DefaultBranch
	}
	receiptsByName := make(
		map[string]RepositoryReceipt,
		len(observed.Receipt.Repositories),
	)
	for _, receipt := range observed.Receipt.Repositories {
		if _, duplicate := receiptsByName[receipt.Repository.Name]; duplicate {
			return false
		}
		receiptsByName[receipt.Repository.Name] = receipt
	}
	for _, spec := range attempted.Spec.Repositories {
		branch, branchExists := resolvedByName[spec.Name]
		receipt, receiptExists := receiptsByName[spec.Name]
		repository := receipt.Repository
		if !branchExists ||
			!receiptExists ||
			repository.WorkspaceKey != attempted.WorkspaceKey ||
			repository.Name != spec.Name ||
			repository.RemoteURL != spec.RemoteURL ||
			repository.Remote != spec.Remote ||
			repository.DefaultBranch != branch ||
			!slices.Equal(repository.Groups, spec.Groups) ||
			repository.SourceRepoID != spec.SourceRepoID {
			return false
		}
		delete(resolvedByName, spec.Name)
		delete(receiptsByName, spec.Name)
	}
	return len(resolvedByName) == 0 && len(receiptsByName) == 0
}

func sameRepositoryAdmissionWorkspaceFinalization(
	left *WorkspaceFinalization,
	right *WorkspaceFinalization,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.State == right.State &&
		left.DefaultBranch == right.DefaultBranch
}

//nolint:funlen // Failure convergence preserves original error class while fencing and replaying one durable terminal transition.
func (process *repositoryAdmissionProcess) fail(
	ctx context.Context,
	record *Record,
	cause error,
) error {
	if process == nil || record == nil || cause == nil || record.State != "pending" {
		return cause
	}
	if process.leases == nil {
		return errors.Join(cause, repositoryAdmissionUnavailable())
	}
	release, lockErr := process.acquireRepositoryAdmissionMutation(
		ctx,
		record.AdmissionID,
	)
	if lockErr != nil {
		return errors.Join(cause, lockErr)
	}
	defer release()

	current, refreshErr := process.getMatchingOwnedRepositoryAdmission(ctx, record)
	if refreshErr != nil {
		return errors.Join(
			cause,
			fmt.Errorf("refresh repository admission failure guard: %w", refreshErr),
		)
	}
	if current.State != "pending" {
		return cause
	}
	if current.OwnerLeaseExpiresAt.Sub(process.now()) <=
		repositoryAdmissionRenewalLead {
		current, refreshErr = process.renewOwnedRepositoryAdmissionLocked(ctx, current)
		if refreshErr != nil {
			return errors.Join(
				cause,
				fmt.Errorf(
					"renew repository admission failure guard: %w",
					refreshErr,
				),
			)
		}
	}
	errorClass, retryable := repositoryAdmissionFailureClass(cause)
	failed, failErr := process.admissions.Fail(
		ctx,
		Fail{
			Guard:      repositoryAdmissionGuard(current),
			ErrorClass: errorClass,
			Retryable:  retryable,
		},
	)
	if failErr != nil {
		return errors.Join(cause, fmt.Errorf("persist repository admission failure: %w", failErr))
	}
	if !exactFailedRepositoryAdmission(current, failed, errorClass, retryable) {
		return errors.Join(
			cause,
			fmt.Errorf(
				"persist repository admission failure returned a divergent owner generation or version: %w",
				ErrInvalid,
			),
		)
	}
	return cause
}

func (process *repositoryAdmissionProcess) failMaterialization(
	ctx context.Context,
	record *Record,
	cause error,
) error {
	if fenceCause := context.Cause(ctx); fenceCause != nil {
		return fenceCause
	}
	return process.fail(ctx, record, cause)
}

func exactFailedRepositoryAdmission(
	previous *Record,
	failed *Record,
	errorClass string,
	retryable bool,
) bool {
	expectedState := "permanent_failed"
	if retryable {
		expectedState = "retryable_failed"
	}
	if !sameRepositoryAdmissionImmutableRecord(previous, failed) ||
		failed.State != expectedState ||
		failed.LastErrorClass != errorClass ||
		failed.OwnerID != previous.OwnerID ||
		failed.OwnerGenerationID != previous.OwnerGenerationID ||
		!failed.OwnerLeaseExpiresAt.Equal(previous.OwnerLeaseExpiresAt) ||
		failed.Version != previous.Version+1 ||
		failed.UpdatedAt.Before(previous.UpdatedAt) ||
		failed.Receipt != nil {
		return false
	}
	if retryable {
		return failed.TerminalAt == nil
	}
	return failed.TerminalAt != nil &&
		failed.TerminalAt.Equal(failed.UpdatedAt)
}

func repositoryAdmissionGuard(
	record *Record,
) Guard {
	return Guard{
		WorkspaceKey: record.WorkspaceKey, AdmissionID: record.AdmissionID,
		OwnerID: record.OwnerID, OwnerGenerationID: record.OwnerGenerationID,
		SpecFingerprint: record.SpecFingerprint, ExpectedVersion: record.Version,
	}
}

func repositoryAdmissionFailureClass(err error) (string, bool) {
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, sourcecontrol.ErrUnavailable) ||
		errors.Is(err, ErrUnavailable) {
		return "materialization_interrupted", true
	}
	if errors.Is(err, sourcecontrol.ErrInvalid) ||
		errors.Is(err, sourcecontrol.ErrCheckoutConflict) ||
		errors.Is(err, sourcecontrol.ErrInvalidMaterialization) ||
		errors.Is(err, sourcecontrol.ErrIdempotencyConflict) {
		return "materialization_rejected", false
	}
	var createErr *workspacemodule.CreateError
	if errors.As(err, &createErr) {
		switch createErr.Code {
		case workspacemodule.GitFailed, workspacemodule.ConfigFailed:
			return "materialization_failed", true
		default:
			return "materialization_rejected", false
		}
	}
	return "materialization_failed", true
}

func OperationID(
	kind,
	workspaceKey,
	workspacePath string,
	repositories []RepositorySpec,
) (string, error) {
	canonical := append([]RepositorySpec(nil), repositories...)
	for index := range canonical {
		if canonical[index].Remote == "" {
			canonical[index].Remote = "origin"
		}
		canonical[index].Groups = append([]string(nil), canonical[index].Groups...)
		sort.Strings(canonical[index].Groups)
	}
	sort.Slice(canonical, func(i, j int) bool {
		return canonical[i].Name < canonical[j].Name
	})
	encoded, err := json.Marshal(struct {
		Kind          string           `json:"kind"`
		WorkspaceKey  string           `json:"workspace_key"`
		WorkspacePath string           `json:"workspace_path"`
		Repositories  []RepositorySpec `json:"repositories"`
	}{
		Kind: kind, WorkspaceKey: workspaceKey,
		WorkspacePath: filepath.Clean(workspacePath), Repositories: canonical,
	})
	if err != nil {
		return "", fmt.Errorf("encode repository admission operation: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "workspace-" + kind + ":" + hex.EncodeToString(sum[:16]), nil
}
