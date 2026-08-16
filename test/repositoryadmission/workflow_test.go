package repositoryadmission_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/repositoryadmission"
	repositoryadmissioninfra "github.com/tysonthomas9/loomcli/internal/infra/repositoryadmission"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

type durableAdmissionFake struct {
	mu               sync.Mutex
	record           *repositoryadmission.Record
	admissionID      string
	specFingerprint  string
	workspaceMissing bool
	created          chan struct{}
	createdOnce      sync.Once
	recoveryClaims   int
}

func (fake *durableAdmissionFake) CreateWorkspace(_ context.Context, input repositoryadmission.WorkspaceBegin) (*repositoryadmission.WorkspaceBeginResult, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.createRecord(input.Workspace.Key, input.OperationID, input.OwnerID, input.OwnerLease, input.Repositories)
	return &repositoryadmission.WorkspaceBeginResult{Admission: cloneRecord(fake.record)}, nil
}

func (fake *durableAdmissionFake) Begin(_ context.Context, workspace string, input repositoryadmission.Begin) (*repositoryadmission.Record, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.createRecord(workspace, input.OperationID, input.OwnerID, input.OwnerLease, input.Repositories)
	return cloneRecord(fake.record), nil
}

func (fake *durableAdmissionFake) createRecord(workspace, operationID, ownerID string, lease time.Duration, repositories []repositoryadmission.RepositorySpec) {
	if fake.record != nil {
		return
	}
	admissionID := fake.admissionID
	if admissionID == "" {
		admissionID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	}
	specFingerprint := fake.specFingerprint
	if specFingerprint == "" {
		specFingerprint = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	}
	now := time.Now().UTC()
	fake.record = &repositoryadmission.Record{
		AdmissionID:  admissionID,
		WorkspaceKey: workspace, OperationID: operationID,
		OwnerID: ownerID, OwnerGenerationID: "11111111111111111111111111111111",
		OwnerLeaseExpiresAt: now.Add(lease),
		SpecFingerprint:     specFingerprint,
		Spec: repositoryadmission.Spec{
			WorkspaceKey: workspace, OperationID: operationID,
			Repositories: append([]repositoryadmission.RepositorySpec(nil), repositories...),
		},
		State: "pending", Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	fake.workspaceMissing = false
	if fake.created != nil {
		fake.createdOnce.Do(func() { close(fake.created) })
	}
}

func (fake *durableAdmissionFake) Get(_ context.Context, workspace, admissionID string) (*repositoryadmission.Record, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.workspaceMissing || fake.record == nil || fake.record.WorkspaceKey != workspace || fake.record.AdmissionID != admissionID {
		return nil, repositoryadmission.ErrNotFound
	}
	return cloneRecord(fake.record), nil
}

func (fake *durableAdmissionFake) GetByOperation(_ context.Context, workspace, operationID string) (*repositoryadmission.Record, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.workspaceMissing || fake.record == nil || fake.record.WorkspaceKey != workspace || fake.record.OperationID != operationID {
		return nil, repositoryadmission.ErrNotFound
	}
	return cloneRecord(fake.record), nil
}

func (fake *durableAdmissionFake) ListRecoverable(context.Context, string, int) ([]*repositoryadmission.Record, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.workspaceMissing {
		return nil, repositoryadmission.ErrNotFound
	}
	if fake.record == nil || fake.record.State == "committed" {
		return nil, nil
	}
	return []*repositoryadmission.Record{cloneRecord(fake.record)}, nil
}

func (fake *durableAdmissionFake) Renew(_ context.Context, input repositoryadmission.Renew) (*repositoryadmission.Record, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !sameGuard(fake.record, input.Guard) {
		return nil, repositoryadmission.ErrFenceLost
	}
	fake.record.Version++
	fake.record.OwnerLeaseExpiresAt = time.Now().UTC().Add(input.Lease)
	fake.record.UpdatedAt = time.Now().UTC()
	return cloneRecord(fake.record), nil
}

func (fake *durableAdmissionFake) ClaimRecovery(_ context.Context, input repositoryadmission.RecoveryClaim) (*repositoryadmission.Record, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.record == nil || fake.record.WorkspaceKey != input.WorkspaceKey ||
		fake.record.AdmissionID != input.AdmissionID || fake.record.SpecFingerprint != input.ExpectedSpecFingerprint ||
		fake.record.Version != input.ExpectedVersion || fake.record.OwnerLeaseExpiresAt.After(time.Now().UTC()) {
		return nil, repositoryadmission.ErrFenceLost
	}
	fake.record.OwnerID = input.NewOwnerID
	fake.record.OwnerGenerationID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	fake.record.OwnerLeaseExpiresAt = time.Now().UTC().Add(input.Lease)
	fake.record.Version++
	fake.record.UpdatedAt = time.Now().UTC()
	fake.recoveryClaims++
	return cloneRecord(fake.record), nil
}

func (fake *durableAdmissionFake) Commit(_ context.Context, input repositoryadmission.Commit) (*repositoryadmission.Record, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !sameGuard(fake.record, input.Guard) {
		return nil, repositoryadmission.ErrFenceLost
	}
	now := time.Now().UTC()
	receipts := make([]repositoryadmission.RepositoryReceipt, 0, len(input.ResolvedDefaultBranches))
	for _, branch := range input.ResolvedDefaultBranches {
		var spec repositoryadmission.RepositorySpec
		for _, candidate := range fake.record.Spec.Repositories {
			if candidate.Name == branch.Name {
				spec = candidate
				break
			}
		}
		receipts = append(receipts, repositoryadmission.RepositoryReceipt{Repository: workspacemodule.Repository{
			WorkspaceKey: fake.record.WorkspaceKey, Name: branch.Name,
			RemoteURL: spec.RemoteURL, Remote: spec.Remote,
			DefaultBranch: branch.DefaultBranch, Groups: append([]string(nil), spec.Groups...),
			SourceRepoID: spec.SourceRepoID,
		}})
	}
	fake.record.State = "committed"
	fake.record.Version++
	fake.record.UpdatedAt = now
	fake.record.TerminalAt = &now
	fake.record.Receipt = &repositoryadmission.Receipt{
		AdmissionID: fake.record.AdmissionID, SpecFingerprint: fake.record.SpecFingerprint,
		Repositories: receipts, WorkspaceFinalization: input.WorkspaceFinalization,
		CommittedAt: now,
	}
	return cloneRecord(fake.record), nil
}

func (fake *durableAdmissionFake) Fail(_ context.Context, input repositoryadmission.Fail) (*repositoryadmission.Record, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !sameGuard(fake.record, input.Guard) {
		return nil, repositoryadmission.ErrFenceLost
	}
	now := time.Now().UTC()
	fake.record.State = "permanent_failed"
	if input.Retryable {
		fake.record.State = "retryable_failed"
	}
	fake.record.LastErrorClass = input.ErrorClass
	fake.record.Version++
	fake.record.UpdatedAt = now
	fake.record.TerminalAt = nil
	if !input.Retryable {
		fake.record.TerminalAt = &now
	}
	return cloneRecord(fake.record), nil
}

func (fake *durableAdmissionFake) Abort(context.Context, repositoryadmission.Abort) (*repositoryadmission.Record, error) {
	return nil, errors.New("unexpected Abort")
}

type localWorkspaceFake struct {
	mu                sync.Mutex
	path              string
	durable           *durableAdmissionFake
	materializeCalled chan struct{}
	release           chan struct{}
	materializeOnce   sync.Once
	materializeErr    error
	createCalls       int
	addCalls          int
	replayCalls       int
}

func (local *localWorkspaceFake) CreateEmpty(context.Context, repositoryadmission.CreateCommand) (repositoryadmission.Result, error) {
	return repositoryadmission.Result{}, errors.New("unexpected CreateEmpty")
}

func (local *localWorkspaceFake) AddWithoutAdmission(context.Context, repositoryadmission.AddRepositoriesCommand) (repositoryadmission.Result, error) {
	return repositoryadmission.Result{}, errors.New("unexpected AddWithoutAdmission")
}

func (local *localWorkspaceFake) PlanCreate(context.Context, repositoryadmission.CreateCommand) (repositoryadmission.CreatePlan, error) {
	return repositoryadmission.CreatePlan{
		WorkspaceKey: "WORK", WorkspacePath: local.path,
		CloneURLs: []string{"https://example.com/acme/repo.git"},
		Repositories: []repositoryadmission.RepositorySpec{{
			Name: "repo", RemoteURL: "https://example.com/acme/repo.git",
			Remote: "origin", SourceRepoID: "repo",
		}},
	}, nil
}

func (local *localWorkspaceFake) PlanAdd(context.Context, repositoryadmission.AddRepositoriesCommand) (repositoryadmission.AddPlan, error) {
	return repositoryadmission.AddPlan{
		WorkspaceKey: "WORK", WorkspacePath: local.path, Branch: "main",
		CloneURLs: []string{"https://example.com/acme/repo.git"},
		Repositories: []repositoryadmission.RepositorySpec{{
			Name: "repo", RemoteURL: "https://example.com/acme/repo.git",
			Remote: "origin", SourceRepoID: "repo",
		}},
	}, nil
}

func (local *localWorkspaceFake) MaterializeCreate(ctx context.Context, _ repositoryadmission.CreateCommand, _ repositoryadmission.CreatePlan, record *repositoryadmission.Record, check repositoryadmission.OwnershipCheck) (repositoryadmission.MaterializationResult, error) {
	if _, err := local.durable.Get(ctx, record.WorkspaceKey, record.AdmissionID); err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	if err := check(ctx); err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	local.mu.Lock()
	local.createCalls++
	materializeErr := local.materializeErr
	local.mu.Unlock()
	if local.materializeCalled != nil {
		local.materializeOnce.Do(func() { close(local.materializeCalled) })
	}
	if local.release != nil {
		select {
		case <-ctx.Done():
			return repositoryadmission.MaterializationResult{}, context.Cause(ctx)
		case <-local.release:
		}
	}
	if materializeErr != nil {
		return repositoryadmission.MaterializationResult{}, materializeErr
	}
	return repositoryadmission.MaterializationResult{
		Repositories:  []repositoryadmission.RepositoryPlacement{{Name: "repo", Path: filepath.Join(local.path, "repo"), DefaultBranch: "main", SourceRepoID: "repo"}},
		DefaultBranch: "main",
	}, nil
}

func (local *localWorkspaceFake) MaterializeAdd(ctx context.Context, _ repositoryadmission.AddRepositoriesCommand, _ repositoryadmission.AddPlan, record *repositoryadmission.Record, check repositoryadmission.OwnershipCheck) (repositoryadmission.MaterializationResult, error) {
	if _, err := local.durable.Get(ctx, record.WorkspaceKey, record.AdmissionID); err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	if err := check(ctx); err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	local.mu.Lock()
	local.addCalls++
	materializeErr := local.materializeErr
	local.mu.Unlock()
	if local.materializeCalled != nil {
		local.materializeOnce.Do(func() { close(local.materializeCalled) })
	}
	if local.release != nil {
		select {
		case <-ctx.Done():
			return repositoryadmission.MaterializationResult{}, context.Cause(ctx)
		case <-local.release:
		}
	}
	if materializeErr != nil {
		return repositoryadmission.MaterializationResult{}, materializeErr
	}
	return repositoryadmission.MaterializationResult{Repositories: []repositoryadmission.RepositoryPlacement{{
		Name: "repo", Path: filepath.Join(local.path, "repo"), Remote: "origin",
		DefaultBranch: "main", SourceRepoID: "repo",
	}}}, nil
}

func (local *localWorkspaceFake) Replay(_ context.Context, record *repositoryadmission.Record, workspacePath string, _ bool) (repositoryadmission.Result, error) {
	local.mu.Lock()
	local.replayCalls++
	local.mu.Unlock()
	return repositoryadmission.Result{WorkspaceID: record.WorkspaceKey, WorkspacePath: workspacePath}, nil
}

func (local *localWorkspaceFake) VerifyRecoveryIntent(context.Context, repositoryadmission.LocalIntent) error {
	return nil
}

func TestWorkflowPersistsBeforeMaterializationAndProjectsOnlyDurableStatus(t *testing.T) {
	t.Parallel()
	journal, err := repositoryadmissioninfra.NewRepositoryAdmissionJournalAt(filepath.Join(t.TempDir(), "journal"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	durable := &durableAdmissionFake{created: make(chan struct{})}
	local := &localWorkspaceFake{
		path: filepath.Join(t.TempDir(), "workspace"), durable: durable,
		materializeCalled: make(chan struct{}), release: make(chan struct{}),
	}
	workflow := repositoryadmission.New(durable, journal, local)
	if workflow == nil {
		t.Fatal("New() returned nil")
	}

	admissionID, err := workflow.StartCreate(t.Context(), repositoryadmission.CreateCommand{Name: "Work", Type: "clone"})
	if err != nil {
		t.Fatalf("StartCreate() error = %v", err)
	}
	select {
	case <-durable.created:
	default:
		t.Fatal("StartCreate returned before durable admission existed")
	}
	select {
	case <-local.materializeCalled:
	case <-time.After(3 * time.Second):
		t.Fatal("immediate runner did not start materialization")
	}
	status, found, err := workflow.Get(t.Context(), admissionID)
	if err != nil || !found || status.State != repositoryadmission.StateRunning {
		t.Fatalf("Get() = %#v, %t, %v; want durable running status", status, found, err)
	}

	close(local.release)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, found, err = workflow.Get(t.Context(), admissionID)
		if err == nil && found && status.State == repositoryadmission.StateDone {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("admission did not converge to done: %#v, found=%t, err=%v", status, found, err)
}

func TestWorkflowLaterAdmissionReplaysCommittedResultWithoutRematerializing(t *testing.T) {
	t.Parallel()
	journal, err := repositoryadmissioninfra.NewRepositoryAdmissionJournalAt(filepath.Join(t.TempDir(), "journal"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	durable := &durableAdmissionFake{}
	local := &localWorkspaceFake{path: filepath.Join(t.TempDir(), "workspace"), durable: durable}
	workflow := repositoryadmission.New(durable, journal, local)
	command := repositoryadmission.AddRepositoriesCommand{
		WorkspaceID: "WORK", Branch: "main",
		CloneURLs: []string{"https://example.com/acme/repo.git"},
	}
	admissionID, err := workflow.StartAddRepositories(t.Context(), command)
	if err != nil {
		t.Fatalf("StartAddRepositories() error = %v", err)
	}
	waitForWorkflowState(t, workflow, admissionID, repositoryadmission.StateDone)

	replayedID, err := workflow.StartAddRepositories(t.Context(), command)
	if err != nil {
		t.Fatalf("replayed StartAddRepositories() error = %v", err)
	}
	if replayedID != admissionID {
		t.Fatalf("replayed admission ID = %q, want %q", replayedID, admissionID)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		local.mu.Lock()
		addCalls, replayCalls := local.addCalls, local.replayCalls
		local.mu.Unlock()
		if addCalls == 1 && replayCalls == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	local.mu.Lock()
	defer local.mu.Unlock()
	t.Fatalf("materialize calls = %d, replay calls = %d; want 1 and 1", local.addCalls, local.replayCalls)
}

func TestWorkflowConcurrentExactAdmissionMaterializesOnce(t *testing.T) {
	t.Parallel()
	journal, err := repositoryadmissioninfra.NewRepositoryAdmissionJournalAt(filepath.Join(t.TempDir(), "journal"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	durable := &durableAdmissionFake{}
	local := &localWorkspaceFake{
		path: filepath.Join(t.TempDir(), "workspace"), durable: durable,
		materializeCalled: make(chan struct{}), release: make(chan struct{}),
	}
	workflow := repositoryadmission.New(durable, journal, local)
	command := repositoryadmission.AddRepositoriesCommand{
		WorkspaceID: "WORK", Branch: "main",
		CloneURLs: []string{"https://example.com/acme/repo.git"},
	}
	firstID, err := workflow.StartAddRepositories(t.Context(), command)
	if err != nil {
		t.Fatalf("first StartAddRepositories() error = %v", err)
	}
	select {
	case <-local.materializeCalled:
	case <-time.After(3 * time.Second):
		t.Fatal("first admission did not enter materialization")
	}
	secondID, err := workflow.StartAddRepositories(t.Context(), command)
	if err != nil {
		t.Fatalf("concurrent StartAddRepositories() error = %v", err)
	}
	if secondID != firstID {
		t.Fatalf("concurrent admission ID = %q, want exact replay %q", secondID, firstID)
	}
	close(local.release)
	waitForWorkflowState(t, workflow, firstID, repositoryadmission.StateDone)
	local.mu.Lock()
	defer local.mu.Unlock()
	if local.addCalls != 1 {
		t.Fatalf("concurrent exact admission materialized %d times, want once", local.addCalls)
	}
}

func TestWorkflowFailureIsProjectedFromDurableAdmission(t *testing.T) {
	t.Parallel()
	journal, err := repositoryadmissioninfra.NewRepositoryAdmissionJournalAt(filepath.Join(t.TempDir(), "journal"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	durable := &durableAdmissionFake{}
	local := &localWorkspaceFake{
		path: filepath.Join(t.TempDir(), "workspace"), durable: durable,
		materializeErr: sourcecontrol.ErrUnavailable,
	}
	workflow := repositoryadmission.New(durable, journal, local)
	admissionID, err := workflow.StartCreate(t.Context(), repositoryadmission.CreateCommand{Name: "Work", Type: "clone"})
	if err != nil {
		t.Fatalf("StartCreate() error = %v", err)
	}
	status := waitForWorkflowState(t, workflow, admissionID, repositoryadmission.StateFailed)
	if status.Error != "repository materialization was interrupted; retry the request" {
		t.Fatalf("failure projection = %#v", status)
	}
}

func TestWorkflowRecoveryClaimsExpiredOwnerAndCompletesProtectedIntent(t *testing.T) {
	t.Parallel()
	old := time.Now().UTC().Add(-2 * time.Minute)
	journal, err := repositoryadmissioninfra.NewRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "journal"),
		func() time.Time { return old },
	)
	if err != nil {
		t.Fatal(err)
	}
	durable := &durableAdmissionFake{}
	local := &localWorkspaceFake{path: filepath.Join(t.TempDir(), "workspace"), durable: durable}
	plan, err := local.PlanCreate(t.Context(), repositoryadmission.CreateCommand{Name: "Work", Type: "clone"})
	if err != nil {
		t.Fatal(err)
	}
	operationID, err := repositoryadmission.OperationID("create_workspace", plan.WorkspaceKey, plan.WorkspacePath, plan.Repositories)
	if err != nil {
		t.Fatal(err)
	}
	localRecord, err := journal.Prepare(t.Context(), repositoryadmission.LocalIntent{
		OperationID: operationID, WorkspaceKey: plan.WorkspaceKey,
		WorkspaceName: "Work", WorkspacePath: plan.WorkspacePath,
		Kind: repositoryadmission.KindCreateWorkspace, CloneURLs: plan.CloneURLs,
	})
	if err != nil {
		t.Fatal(err)
	}
	durable.mu.Lock()
	durable.createRecord(plan.WorkspaceKey, operationID, "crashed-owner", time.Minute, plan.Repositories)
	durable.record.OwnerLeaseExpiresAt = old
	durable.record.CreatedAt = old
	durable.record.UpdatedAt = old
	record := cloneRecord(durable.record)
	durable.mu.Unlock()
	if _, err := journal.Bind(t.Context(), localRecord.Intent.OperationID, record.AdmissionID, record.SpecFingerprint); err != nil {
		t.Fatal(err)
	}

	workflow := repositoryadmission.New(durable, journal, local)
	var recoveryRegistration interface {
		RunOnce(context.Context, time.Time) error
	}
	for _, registration := range workflow.RuntimeRegistrations() {
		if registration.Component.ID() == "workspace-repository-admission-recovery" {
			recoveryRegistration = registration.Component
			break
		}
	}
	if recoveryRegistration == nil {
		t.Fatal("recovery runtime registration not found")
	}
	if err := recoveryRegistration.RunOnce(t.Context(), time.Now()); err != nil {
		durable.mu.Lock()
		state := cloneRecord(durable.record)
		durable.mu.Unlock()
		local.mu.Lock()
		createCalls, replayCalls := local.createCalls, local.replayCalls
		local.mu.Unlock()
		t.Fatalf("recovery RunOnce() error = %v; durable=%#v create_calls=%d replay_calls=%d", err, state, createCalls, replayCalls)
	}
	durable.mu.Lock()
	defer durable.mu.Unlock()
	if durable.recoveryClaims != 1 || durable.record.State != "committed" ||
		durable.record.OwnerID == "crashed-owner" || durable.record.OwnerGenerationID != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("recovered durable admission = %#v, claims=%d", durable.record, durable.recoveryClaims)
	}
}

func TestWorkflowRecoveryReconstructsDefinitivelyMissingDurableCoordinates(t *testing.T) {
	t.Parallel()
	old := time.Now().UTC().Add(-2 * time.Minute)
	journal, err := repositoryadmissioninfra.NewRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "journal"),
		func() time.Time { return old },
	)
	if err != nil {
		t.Fatal(err)
	}
	durable := &durableAdmissionFake{
		admissionID:      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		specFingerprint:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		workspaceMissing: true,
	}
	local := &localWorkspaceFake{path: filepath.Join(t.TempDir(), "workspace"), durable: durable}
	plan, err := local.PlanCreate(t.Context(), repositoryadmission.CreateCommand{Name: "Work", Type: "clone"})
	if err != nil {
		t.Fatal(err)
	}
	operationID, err := repositoryadmission.OperationID("create_workspace", plan.WorkspaceKey, plan.WorkspacePath, plan.Repositories)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Prepare(t.Context(), repositoryadmission.LocalIntent{
		OperationID: operationID, WorkspaceKey: plan.WorkspaceKey,
		WorkspaceName: "Work", WorkspacePath: plan.WorkspacePath,
		Kind: repositoryadmission.KindCreateWorkspace, CloneURLs: plan.CloneURLs,
	}); err != nil {
		t.Fatal(err)
	}
	const (
		missingAdmissionID = "cccccccccccccccccccccccccccccccc"
		fingerprint        = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	if _, err := journal.Bind(t.Context(), operationID, missingAdmissionID, fingerprint); err != nil {
		t.Fatal(err)
	}

	workflow := repositoryadmission.New(durable, journal, local)
	var recovery interface {
		RunOnce(context.Context, time.Time) error
	}
	for _, registration := range workflow.RuntimeRegistrations() {
		if registration.Component.ID() == "workspace-repository-admission-recovery" {
			recovery = registration.Component
			break
		}
	}
	if recovery == nil {
		t.Fatal("recovery runtime registration not found")
	}
	if err := recovery.RunOnce(t.Context(), time.Now()); err != nil {
		t.Fatalf("reconstruct missing durable coordinates: %v", err)
	}
	rebound, err := journal.GetByOperation(t.Context(), operationID)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.AdmissionID != durable.admissionID || rebound.SpecFingerprint != durable.specFingerprint ||
		len(rebound.PreviousAdmissionIDs) != 1 || rebound.PreviousAdmissionIDs[0] != missingAdmissionID {
		t.Fatalf("rebound local coordinates = %#v", rebound)
	}
	status, found, err := workflow.Get(t.Context(), missingAdmissionID)
	if err != nil || !found || status == nil || status.ID != missingAdmissionID || status.State != repositoryadmission.StateDone {
		t.Fatalf("accepted pre-restart admission status = %#v, found=%t, err=%v", status, found, err)
	}
	durable.mu.Lock()
	defer durable.mu.Unlock()
	if durable.record == nil || durable.record.State != "committed" || local.createCalls != 1 {
		t.Fatalf("reconstructed durable admission = %#v, create calls=%d", durable.record, local.createCalls)
	}
}

func waitForWorkflowState(t *testing.T, workflow *repositoryadmission.Workflow, admissionID string, want repositoryadmission.State) *repositoryadmission.Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last *repositoryadmission.Status
	for time.Now().Before(deadline) {
		status, found, err := workflow.Get(t.Context(), admissionID)
		if err == nil && found {
			last = status
			if status.State == want {
				return status
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("admission %q state = %#v, want %q", admissionID, last, want)
	return nil
}

func sameGuard(record *repositoryadmission.Record, guard repositoryadmission.Guard) bool {
	return record != nil && record.WorkspaceKey == guard.WorkspaceKey &&
		record.AdmissionID == guard.AdmissionID && record.OwnerID == guard.OwnerID &&
		record.OwnerGenerationID == guard.OwnerGenerationID &&
		record.SpecFingerprint == guard.SpecFingerprint && record.Version == guard.ExpectedVersion
}

func cloneRecord(record *repositoryadmission.Record) *repositoryadmission.Record {
	if record == nil {
		return nil
	}
	copy := *record
	copy.Spec.Repositories = append([]repositoryadmission.RepositorySpec(nil), record.Spec.Repositories...)
	if record.Receipt != nil {
		receipt := *record.Receipt
		receipt.Repositories = append([]repositoryadmission.RepositoryReceipt(nil), record.Receipt.Repositories...)
		if record.Receipt.WorkspaceFinalization != nil {
			finalization := *record.Receipt.WorkspaceFinalization
			receipt.WorkspaceFinalization = &finalization
		}
		copy.Receipt = &receipt
	}
	return &copy
}

var _ repositoryadmission.DurableAdmissions = (*durableAdmissionFake)(nil)
var _ repositoryadmission.LocalWorkspace = (*localWorkspaceFake)(nil)
