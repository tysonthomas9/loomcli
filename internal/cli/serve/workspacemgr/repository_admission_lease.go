package workspacemgr

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
)

const repositoryAdmissionRenewalLead = 45 * time.Second

var errRepositoryAdmissionProcessStopped = errors.New(
	"repository admission materialization stopped",
)

const (
	repositoryAdmissionRenewalConcurrency  = 8
	repositoryAdmissionRenewalAttemptLimit = 5 * time.Second
)

type repositoryAdmissionMutationLock struct {
	token chan struct{}
	refs  int
}

type activeRepositoryAdmission struct {
	coordinate      repositoryAdmissionCoordinate
	owned           bool
	cancel          context.CancelCauseFunc
	deadlineUpdates chan time.Time
	watchdogDone    chan struct{}
	operationDone   chan struct{}
}

type repositoryAdmissionLeaseState struct {
	// Mutation locks are per admission and context-aware. A slow Fleet call for
	// one admission must not starve renewals for unrelated active admissions.
	mutationMu sync.Mutex
	mutations  map[string]*repositoryAdmissionMutationLock

	activeMu sync.RWMutex
	active   map[string]*activeRepositoryAdmission
	prepared map[string]time.Time
	stopped  bool
	// renewalOffset rotates the stable active-admission order after a pass
	// that exhausted its host deadline. Without this cursor, hung low IDs can
	// consume every worker on every pass and starve healthy later IDs.
	renewalOffset int
}

func newRepositoryAdmissionLeaseState() *repositoryAdmissionLeaseState {
	return &repositoryAdmissionLeaseState{
		mutations: make(map[string]*repositoryAdmissionMutationLock),
		active:    make(map[string]*activeRepositoryAdmission),
		prepared:  make(map[string]time.Time),
	}
}

func repositoryAdmissionCoordinateFromRecord(
	record *infrafleetdb.RepositoryAdmissionRecord,
) (repositoryAdmissionCoordinate, error) {
	if record == nil ||
		record.WorkspaceKey == "" ||
		record.AdmissionID == "" ||
		record.OperationID == "" ||
		record.OwnerID == "" ||
		record.OwnerGenerationID == "" ||
		record.SpecFingerprint == "" {
		return repositoryAdmissionCoordinate{}, infrafleetdb.ErrRepositoryAdmissionInvalid
	}
	return repositoryAdmissionCoordinate{
		WorkspaceKey:      record.WorkspaceKey,
		AdmissionID:       record.AdmissionID,
		OperationID:       record.OperationID,
		OwnerID:           record.OwnerID,
		OwnerGenerationID: record.OwnerGenerationID,
		SpecFingerprint:   record.SpecFingerprint,
	}, nil
}

//nolint:cyclop,funlen // Ownership acquisition, local locking, lease renewal, and cleanup form one fail-closed materialization boundary.
func (process *repositoryAdmissionProcess) beginMaterialization(
	ctx context.Context,
	record *infrafleetdb.RepositoryAdmissionRecord,
	workspacePath string,
) (context.Context, *infrafleetdb.RepositoryAdmissionRecord, func(), error) {
	if process == nil || process.leases == nil || process.journal == nil ||
		ctx == nil || record == nil {
		return nil, nil, nil, repositoryAdmissionUnavailable()
	}
	if record.State != "pending" && record.State != "retryable_failed" {
		return nil, nil, nil, infrafleetdb.ErrRepositoryAdmissionFenceLost
	}
	coordinate, err := repositoryAdmissionCoordinateFromRecord(record)
	if err != nil {
		return nil, nil, nil, err
	}
	materializationCtx, cancel := context.WithCancelCause(ctx)
	active := &activeRepositoryAdmission{
		coordinate:      coordinate,
		cancel:          cancel,
		deadlineUpdates: make(chan time.Time, 1),
		watchdogDone:    make(chan struct{}),
		operationDone:   make(chan struct{}),
	}

	process.leases.activeMu.Lock()
	if process.leases.stopped {
		process.leases.activeMu.Unlock()
		cancel(errRepositoryAdmissionProcessStopped)
		close(active.watchdogDone)
		close(active.operationDone)
		return nil, nil, nil, errRepositoryAdmissionProcessStopped
	}
	if _, exists := process.leases.active[coordinate.AdmissionID]; exists {
		process.leases.activeMu.Unlock()
		cancel(infrafleetdb.ErrRepositoryAdmissionConflict)
		close(active.operationDone)
		return nil, nil, nil, fmt.Errorf(
			"repository admission %q is already materializing in this serve incarnation: %w",
			coordinate.AdmissionID,
			infrafleetdb.ErrRepositoryAdmissionConflict,
		)
	}
	delete(process.leases.prepared, coordinate.AdmissionID)
	process.leases.active[coordinate.AdmissionID] = active
	process.leases.activeMu.Unlock()

	ownedCoordinate := coordinate
	cleanup := func(cause error) {
		cancel(cause)
		process.leases.activeMu.Lock()
		process.journal.deactivateMaterializationAuthority(ownedCoordinate)
		if process.leases.active[coordinate.AdmissionID] == active {
			delete(process.leases.active, coordinate.AdmissionID)
		}
		process.leases.activeMu.Unlock()
		select {
		case <-active.operationDone:
		default:
			close(active.operationDone)
		}
	}

	targets := make([]string, 0, len(record.Spec.Repositories))
	for _, repository := range record.Spec.Repositories {
		targets = append(targets, filepath.Join(workspacePath, repository.Name))
	}
	unlock, err := process.journal.AcquireMaterializationLock(
		materializationCtx,
		record.AdmissionID,
		targets,
	)
	if err != nil {
		cleanup(err)
		return nil, nil, nil, err
	}
	renewed, renewalStart, err := process.acquireMaterializationOwnership(
		materializationCtx,
		record,
	)
	if err != nil {
		unlock()
		cleanup(err)
		return nil, nil, nil, err
	}
	ownedCoordinate, err = repositoryAdmissionCoordinateFromRecord(renewed)
	if err != nil {
		unlock()
		cleanup(err)
		return nil, nil, nil, err
	}
	leaseDeadline, err := process.materializationLeaseDeadline(
		renewed,
		renewalStart,
	)
	if err != nil {
		unlock()
		cleanup(err)
		return nil, nil, nil, err
	}
	process.leases.activeMu.Lock()
	if process.leases.stopped ||
		process.leases.active[coordinate.AdmissionID] != active ||
		context.Cause(materializationCtx) != nil {
		process.leases.activeMu.Unlock()
		unlock()
		cause := context.Cause(materializationCtx)
		if cause == nil {
			cause = infrafleetdb.ErrRepositoryAdmissionFenceLost
		}
		cleanup(cause)
		return nil, nil, nil, cause
	}
	if err := process.journal.activateMaterializationAuthority(
		ownedCoordinate,
		leaseDeadline,
	); err != nil {
		process.leases.activeMu.Unlock()
		unlock()
		cleanup(err)
		return nil, nil, nil, err
	}
	active.coordinate = ownedCoordinate
	active.owned = true
	process.leases.activeMu.Unlock()
	go process.watchRepositoryAdmissionLease(materializationCtx, active, leaseDeadline)
	if err := process.verifyMaterializationOwnership(materializationCtx, renewed); err != nil {
		unlock()
		cleanup(err)
		<-active.watchdogDone
		return nil, nil, nil, err
	}

	var once sync.Once
	return materializationCtx, renewed, func() {
		once.Do(func() {
			cancel(context.Canceled)
			process.leases.activeMu.Lock()
			process.journal.deactivateMaterializationAuthority(ownedCoordinate)
			if process.leases.active[coordinate.AdmissionID] == active {
				delete(process.leases.active, coordinate.AdmissionID)
			}
			process.leases.activeMu.Unlock()
			unlock()
			close(active.operationDone)
			<-active.watchdogDone
		})
	}, nil
}

func (process *repositoryAdmissionProcess) acquireMaterializationOwnership(
	ctx context.Context,
	record *infrafleetdb.RepositoryAdmissionRecord,
) (*infrafleetdb.RepositoryAdmissionRecord, time.Time, error) {
	release, err := process.acquireRepositoryAdmissionMutation(ctx, record.AdmissionID)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer release()
	current, err := process.getMatchingRepositoryAdmission(ctx, record)
	if err != nil {
		return nil, time.Time{}, err
	}
	if current.State == "pending" &&
		current.OwnerID == process.ownerID &&
		current.OwnerGenerationID == record.OwnerGenerationID {
		renewed, renewalStart, renewErr :=
			process.renewOwnedRepositoryAdmissionWithStartLocked(ctx, current)
		if renewErr == nil {
			return renewed, renewalStart, nil
		}
		if !errors.Is(renewErr, infrafleetdb.ErrRepositoryAdmissionFenceLost) {
			return nil, time.Time{}, renewErr
		}
	}
	if current.State != "pending" && current.State != "retryable_failed" {
		return nil, time.Time{}, infrafleetdb.ErrRepositoryAdmissionState
	}
	recovered, err := process.admissions.ClaimRepositoryAdmissionRecovery(
		ctx,
		infrafleetdb.RepositoryAdmissionRecoveryClaimInput{
			WorkspaceKey: current.WorkspaceKey, AdmissionID: current.AdmissionID,
			ExpectedSpecFingerprint: current.SpecFingerprint,
			ExpectedVersion:         current.Version,
			NewOwnerID:              process.ownerID,
			Lease:                   process.repositoryAdmissionOwnerLease(),
		},
	)
	if err != nil {
		return nil, time.Time{}, err
	}
	if !exactRecoveredRepositoryAdmission(
		current,
		recovered,
		process.ownerID,
	) {
		return nil, time.Time{}, infrafleetdb.ErrRepositoryAdmissionInvalid
	}
	// Renewal immediately after the exact recovery claim makes begin use the
	// same response validator and monotonic watchdog rule as a fresh owner.
	return process.renewOwnedRepositoryAdmissionWithStartLocked(ctx, recovered)
}

func (process *repositoryAdmissionProcess) watchRepositoryAdmissionLease(
	ctx context.Context,
	active *activeRepositoryAdmission,
	initialDeadline time.Time,
) {
	defer close(active.watchdogDone)
	if process == nil || active == nil {
		return
	}
	deadline := initialDeadline
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case next := <-active.deadlineUpdates:
			deadline = next
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(time.Until(deadline))
		case <-timer.C:
			process.journal.deactivateMaterializationAuthority(active.coordinate)
			active.cancel(infrafleetdb.ErrRepositoryAdmissionFenceLost)
			return
		}
	}
}

func (process *repositoryAdmissionProcess) acquireRepositoryAdmissionMutation(
	ctx context.Context,
	admissionID string,
) (func(), error) {
	if process == nil || process.leases == nil || ctx == nil ||
		admissionID == "" {
		return nil, repositoryAdmissionUnavailable()
	}
	process.leases.mutationMu.Lock()
	lock := process.leases.mutations[admissionID]
	if lock == nil {
		lock = &repositoryAdmissionMutationLock{token: make(chan struct{}, 1)}
		lock.token <- struct{}{}
		process.leases.mutations[admissionID] = lock
	}
	lock.refs++
	process.leases.mutationMu.Unlock()

	select {
	case <-ctx.Done():
		process.releaseRepositoryAdmissionMutationReference(admissionID, lock)
		return nil, context.Cause(ctx)
	case <-lock.token:
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			lock.token <- struct{}{}
			process.releaseRepositoryAdmissionMutationReference(admissionID, lock)
		})
	}, nil
}

func (process *repositoryAdmissionProcess) releaseRepositoryAdmissionMutationReference(
	admissionID string,
	lock *repositoryAdmissionMutationLock,
) {
	process.leases.mutationMu.Lock()
	lock.refs--
	if lock.refs == 0 && process.leases.mutations[admissionID] == lock {
		delete(process.leases.mutations, admissionID)
	}
	process.leases.mutationMu.Unlock()
}

func (process *repositoryAdmissionProcess) materializationLeaseDeadline(
	record *infrafleetdb.RepositoryAdmissionRecord,
	renewalStart time.Time,
) (time.Time, error) {
	if process == nil || record == nil || renewalStart.IsZero() ||
		record.UpdatedAt.IsZero() ||
		!record.OwnerLeaseExpiresAt.After(record.UpdatedAt) {
		return time.Time{}, infrafleetdb.ErrRepositoryAdmissionInvalid
	}
	lease := process.repositoryAdmissionOwnerLease()
	grantedLease := record.OwnerLeaseExpiresAt.Sub(record.UpdatedAt)
	if grantedLease < lease {
		lease = grantedLease
	}
	margin := process.leaseSafetyMargin
	if margin <= 0 {
		margin = repositoryAdmissionLeaseSafetyMargin
	}
	lifetime := lease - margin
	if lifetime <= 0 {
		return time.Time{}, infrafleetdb.ErrRepositoryAdmissionInvalid
	}
	return renewalStart.Add(lifetime), nil
}

func (process *repositoryAdmissionProcess) noteActiveRepositoryAdmissionRenewal(
	coordinate repositoryAdmissionCoordinate,
	deadline time.Time,
) {
	if process == nil || process.leases == nil || deadline.IsZero() {
		return
	}
	process.leases.activeMu.RLock()
	active := process.leases.active[coordinate.AdmissionID]
	if active == nil || !active.owned || active.coordinate != coordinate {
		process.leases.activeMu.RUnlock()
		return
	}
	if process.journal == nil ||
		!process.journal.renewMaterializationAuthority(coordinate, deadline) {
		active.cancel(infrafleetdb.ErrRepositoryAdmissionFenceLost)
		process.leases.activeMu.RUnlock()
		return
	}
	select {
	case active.deadlineUpdates <- deadline:
	default:
		select {
		case <-active.deadlineUpdates:
		default:
		}
		active.deadlineUpdates <- deadline
	}
	process.leases.activeMu.RUnlock()
}

func (process *repositoryAdmissionProcess) cancelActiveRepositoryAdmission(
	coordinate repositoryAdmissionCoordinate,
	cause error,
) {
	if process == nil || process.leases == nil || cause == nil {
		return
	}
	process.leases.activeMu.RLock()
	active := process.leases.active[coordinate.AdmissionID]
	if active != nil && active.coordinate == coordinate {
		if process.journal != nil {
			process.journal.deactivateMaterializationAuthority(coordinate)
		}
		active.cancel(cause)
	}
	process.leases.activeMu.RUnlock()
}

func (process *repositoryAdmissionProcess) stopMaterializations(ctx context.Context) error {
	if process == nil || process.leases == nil || ctx == nil {
		return nil
	}
	process.leases.activeMu.Lock()
	process.leases.stopped = true
	active := make([]*activeRepositoryAdmission, 0, len(process.leases.active))
	for _, job := range process.leases.active {
		active = append(active, job)
		job.cancel(errRepositoryAdmissionProcessStopped)
	}
	if process.journal != nil {
		process.journal.deactivateAllMaterializationAuthorities()
	}
	process.leases.activeMu.Unlock()
	for _, job := range active {
		select {
		case <-job.operationDone:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	return nil
}

func (process *repositoryAdmissionProcess) ensureMaterializationsRunning() error {
	if process == nil || process.leases == nil {
		return repositoryAdmissionUnavailable()
	}
	process.leases.activeMu.RLock()
	stopped := process.leases.stopped
	process.leases.activeMu.RUnlock()
	if stopped {
		return errRepositoryAdmissionProcessStopped
	}
	return nil
}

func (process *repositoryAdmissionProcess) notePreparedRepositoryAdmission(
	record *infrafleetdb.RepositoryAdmissionRecord,
) {
	if process == nil || process.leases == nil || record == nil ||
		record.State != "pending" || record.OwnerID != process.ownerID {
		return
	}
	process.leases.activeMu.Lock()
	if process.leases.stopped {
		process.leases.activeMu.Unlock()
		return
	}
	process.leases.prepared[record.AdmissionID] = process.now()
	process.leases.activeMu.Unlock()
}

func (process *repositoryAdmissionProcess) forgetPreparedRepositoryAdmission(
	admissionID string,
) {
	if process == nil || process.leases == nil {
		return
	}
	process.leases.activeMu.Lock()
	delete(process.leases.prepared, admissionID)
	process.leases.activeMu.Unlock()
}

func (process *repositoryAdmissionProcess) recentlyPreparedRepositoryAdmission(
	admissionID string,
	now time.Time,
	grace time.Duration,
) bool {
	if process == nil || process.leases == nil {
		return false
	}
	process.leases.activeMu.Lock()
	preparedAt, prepared := process.leases.prepared[admissionID]
	if prepared && !now.Before(preparedAt.Add(grace)) {
		delete(process.leases.prepared, admissionID)
		prepared = false
	}
	process.leases.activeMu.Unlock()
	return prepared
}

func (process *repositoryAdmissionProcess) isMaterializing(admissionID string) bool {
	if process == nil || process.leases == nil {
		return false
	}
	process.leases.activeMu.RLock()
	_, active := process.leases.active[admissionID]
	process.leases.activeMu.RUnlock()
	return active
}

func (process *repositoryAdmissionProcess) activeRepositoryAdmissions() []repositoryAdmissionCoordinate {
	if process == nil || process.leases == nil {
		return nil
	}
	process.leases.activeMu.RLock()
	result := make([]repositoryAdmissionCoordinate, 0, len(process.leases.active))
	for _, active := range process.leases.active {
		if active.owned {
			result = append(result, active.coordinate)
		}
	}
	offset := process.leases.renewalOffset
	process.leases.activeMu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].WorkspaceKey == result[j].WorkspaceKey {
			return result[i].AdmissionID < result[j].AdmissionID
		}
		return result[i].WorkspaceKey < result[j].WorkspaceKey
	})
	if len(result) > 0 {
		offset %= len(result)
		result = append(
			append(
				make([]repositoryAdmissionCoordinate, 0, len(result)),
				result[offset:]...,
			),
			result[:offset]...,
		)
	}
	return result
}

func (process *repositoryAdmissionProcess) advanceRepositoryAdmissionRenewalCursor(
	attempted,
	total int,
) {
	if process == nil || process.leases == nil || attempted <= 0 || total <= 0 {
		return
	}
	process.leases.activeMu.Lock()
	if attempted >= total {
		process.leases.renewalOffset = 0
	} else {
		process.leases.renewalOffset =
			(process.leases.renewalOffset + attempted) % total
	}
	process.leases.activeMu.Unlock()
}

//nolint:funlen // One renewal pass must preserve cursor fairness and exact owner/fence handling across every active admission.
func (process *repositoryAdmissionProcess) renewActiveRepositoryAdmissions(
	ctx context.Context,
) error {
	if ctx == nil {
		return repositoryAdmissionUnavailable()
	}
	if err := process.ensureMaterializationsRunning(); err != nil {
		return err
	}
	coordinates := process.activeRepositoryAdmissions()
	if len(coordinates) == 0 {
		return nil
	}
	workerCount := min(repositoryAdmissionRenewalConcurrency, len(coordinates))
	jobs := make(chan repositoryAdmissionCoordinate, len(coordinates))
	results := make(chan error, len(coordinates))
	attempted := make(chan struct{}, len(coordinates))
	for _, coordinate := range coordinates {
		jobs <- coordinate
	}
	close(jobs)

	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for coordinate := range jobs {
				if context.Cause(ctx) != nil {
					return
				}
				attempted <- struct{}{}
				attemptCtx, cancel := context.WithTimeout(
					ctx,
					repositoryAdmissionRenewalAttemptLimit,
				)
				err := process.renewActiveRepositoryAdmission(
					attemptCtx,
					coordinate,
				)
				cancel()
				if err != nil {
					if definiteRepositoryAdmissionFenceLoss(err) {
						process.cancelActiveRepositoryAdmission(coordinate, err)
					}
					results <- fmt.Errorf(
						"renew repository admission %q: %w",
						coordinate.AdmissionID,
						err,
					)
				}
			}
		}()
	}
	workers.Wait()
	process.advanceRepositoryAdmissionRenewalCursor(
		len(attempted),
		len(coordinates),
	)
	close(results)
	renewalErrors := make([]error, 0, len(results))
	for err := range results {
		renewalErrors = append(renewalErrors, err)
	}
	return errors.Join(renewalErrors...)
}

func (process *repositoryAdmissionProcess) renewActiveRepositoryAdmission(
	ctx context.Context,
	coordinate repositoryAdmissionCoordinate,
) error {
	if process == nil || process.leases == nil {
		return repositoryAdmissionUnavailable()
	}
	release, err := process.acquireRepositoryAdmissionMutation(
		ctx,
		coordinate.AdmissionID,
	)
	if err != nil {
		return err
	}
	defer release()

	record, err := process.admissions.GetRepositoryAdmission(
		ctx,
		coordinate.WorkspaceKey,
		coordinate.AdmissionID,
	)
	if err != nil {
		return err
	}
	if err := verifyRepositoryAdmissionOwnerCoordinate(coordinate, record); err != nil {
		return err
	}
	if record.State != "pending" {
		return infrafleetdb.ErrRepositoryAdmissionFenceLost
	}
	if record.OwnerID != process.ownerID ||
		record.OwnerGenerationID != coordinate.OwnerGenerationID {
		return infrafleetdb.ErrRepositoryAdmissionFenceLost
	}
	_, err = process.renewOwnedRepositoryAdmissionLocked(ctx, record)
	return err
}

func (process *repositoryAdmissionProcess) renewOwnedRepositoryAdmissionLocked(
	ctx context.Context,
	record *infrafleetdb.RepositoryAdmissionRecord,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	renewed, _, err := process.renewOwnedRepositoryAdmissionWithStartLocked(
		ctx,
		record,
	)
	return renewed, err
}

func (process *repositoryAdmissionProcess) renewOwnedRepositoryAdmissionWithStartLocked(
	ctx context.Context,
	record *infrafleetdb.RepositoryAdmissionRecord,
) (*infrafleetdb.RepositoryAdmissionRecord, time.Time, error) {
	if process == nil || record == nil {
		return nil, time.Time{}, repositoryAdmissionUnavailable()
	}
	if record.State != "pending" ||
		record.OwnerID != process.ownerID {
		return nil, time.Time{}, infrafleetdb.ErrRepositoryAdmissionFenceLost
	}
	renewalStart := time.Now()
	renewed, err := process.admissions.RenewRepositoryAdmission(
		ctx,
		infrafleetdb.RepositoryAdmissionRenewInput{
			RepositoryAdmissionGuard: repositoryAdmissionGuard(record),
			Lease:                    process.repositoryAdmissionOwnerLease(),
		},
	)
	if err != nil {
		return nil, time.Time{}, err
	}
	coordinate, err := repositoryAdmissionCoordinateFromRecord(record)
	if err != nil {
		return nil, time.Time{}, err
	}
	if err := verifyRepositoryAdmissionCoordinate(coordinate, renewed); err != nil {
		return nil, time.Time{}, err
	}
	if !exactRenewedRepositoryAdmission(
		record,
		renewed,
		process.ownerID,
	) {
		return nil, time.Time{}, infrafleetdb.ErrRepositoryAdmissionInvalid
	}
	deadline, err := process.materializationLeaseDeadline(renewed, renewalStart)
	if err != nil {
		return nil, time.Time{}, err
	}
	process.noteActiveRepositoryAdmissionRenewal(coordinate, deadline)
	return renewed, renewalStart, nil
}

func definiteRepositoryAdmissionFenceLoss(err error) bool {
	return errors.Is(err, infrafleetdb.ErrRepositoryAdmissionFenceLost) ||
		errors.Is(err, infrafleetdb.ErrRepositoryAdmissionInvalid) ||
		errors.Is(err, infrafleetdb.ErrRepositoryAdmissionState) ||
		errors.Is(err, infrafleetdb.ErrRepositoryAdmissionNotFound) ||
		errors.Is(err, infrafleetdb.ErrRepositoryAdmissionConflict)
}

func exactRenewedRepositoryAdmission(
	previous *infrafleetdb.RepositoryAdmissionRecord,
	renewed *infrafleetdb.RepositoryAdmissionRecord,
	ownerID string,
) bool {
	return sameRepositoryAdmissionImmutableRecord(previous, renewed) &&
		renewed.State == "pending" &&
		renewed.LastErrorClass == previous.LastErrorClass &&
		renewed.OwnerID == ownerID &&
		renewed.OwnerGenerationID == previous.OwnerGenerationID &&
		renewed.Version == previous.Version+1 &&
		!renewed.UpdatedAt.Before(previous.UpdatedAt) &&
		renewed.OwnerLeaseExpiresAt.After(previous.OwnerLeaseExpiresAt) &&
		renewed.TerminalAt == nil &&
		renewed.Receipt == nil
}

func (process *repositoryAdmissionProcess) getMatchingRepositoryAdmission(
	ctx context.Context,
	expected *infrafleetdb.RepositoryAdmissionRecord,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	if process == nil || expected == nil {
		return nil, repositoryAdmissionUnavailable()
	}
	coordinate, err := repositoryAdmissionCoordinateFromRecord(expected)
	if err != nil {
		return nil, err
	}
	current, err := process.admissions.GetRepositoryAdmission(
		ctx,
		coordinate.WorkspaceKey,
		coordinate.AdmissionID,
	)
	if err != nil {
		return nil, err
	}
	if err := verifyRepositoryAdmissionCoordinate(coordinate, current); err != nil {
		return nil, err
	}
	return current, nil
}

func (process *repositoryAdmissionProcess) getMatchingOwnedRepositoryAdmission(
	ctx context.Context,
	expected *infrafleetdb.RepositoryAdmissionRecord,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	current, err := process.getMatchingRepositoryAdmission(ctx, expected)
	if err != nil {
		return nil, err
	}
	if current.State == "committed" {
		return current, nil
	}
	if current.OwnerID != process.ownerID ||
		current.OwnerGenerationID != expected.OwnerGenerationID {
		return nil, infrafleetdb.ErrRepositoryAdmissionFenceLost
	}
	switch current.State {
	case "pending":
		return current, nil
	case "retryable_failed", "permanent_failed", "aborted":
		return nil, infrafleetdb.ErrRepositoryAdmissionState
	default:
		return nil, infrafleetdb.ErrRepositoryAdmissionInvalid
	}
}

func verifyRepositoryAdmissionCoordinate(
	expected repositoryAdmissionCoordinate,
	record *infrafleetdb.RepositoryAdmissionRecord,
) error {
	if record == nil ||
		record.WorkspaceKey != expected.WorkspaceKey ||
		record.AdmissionID != expected.AdmissionID ||
		record.OperationID != expected.OperationID ||
		record.SpecFingerprint != expected.SpecFingerprint {
		return fmt.Errorf(
			"repository admission durable coordinates diverged: %w",
			infrafleetdb.ErrRepositoryAdmissionInvalid,
		)
	}
	return nil
}

func verifyRepositoryAdmissionOwnerCoordinate(
	expected repositoryAdmissionCoordinate,
	record *infrafleetdb.RepositoryAdmissionRecord,
) error {
	if err := verifyRepositoryAdmissionCoordinate(expected, record); err != nil {
		return err
	}
	if record.OwnerID != expected.OwnerID ||
		record.OwnerGenerationID != expected.OwnerGenerationID {
		return infrafleetdb.ErrRepositoryAdmissionFenceLost
	}
	return nil
}

func (process *repositoryAdmissionProcess) verifyMaterializationOwnership(
	ctx context.Context,
	expected *infrafleetdb.RepositoryAdmissionRecord,
) error {
	if ctx == nil {
		return repositoryAdmissionUnavailable()
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	current, err := process.getMatchingRepositoryAdmission(ctx, expected)
	if err != nil {
		return err
	}
	if current.State != "pending" ||
		current.OwnerID != process.ownerID ||
		current.OwnerGenerationID != expected.OwnerGenerationID {
		return infrafleetdb.ErrRepositoryAdmissionFenceLost
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return nil
}
