package repositoryadmission

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

const (
	workspaceRepositoryAdmissionRecoveryComponentID platformruntime.ComponentID = "workspace-repository-admission-recovery"
	workspaceRepositoryAdmissionRenewalComponentID  platformruntime.ComponentID = "workspace-repository-admission-lease-renewal"
	workspaceRepositoryAdmissionRecoveryLimit                                   = 100
	workspaceRepositoryAdmissionStartGrace                                      = 30 * time.Second
	workspaceRepositoryAdmissionExecutionTimeout                                = 6 * time.Minute
)

// Workflow is the one serve-incarnation
// coordinator for Workspace repository materialization. It implements the
// synchronous WebUI admission seam and contributes restart recovery to the
// platform runtime host while exposing neither FleetDB nor Source Control.
type Workflow struct {
	local   LocalWorkspace
	process *repositoryAdmissionProcess
}

func New(
	admissions DurableAdmissions,
	journal Journal,
	local LocalWorkspace,
) *Workflow {
	if local == nil {
		return nil
	}
	return &Workflow{
		local:   local,
		process: newRepositoryAdmissionProcess(admissions, journal),
	}
}

func (operations *Workflow) Create(
	ctx context.Context,
	req CreateCommand,
) (Result, error) {
	if operations == nil || operations.local == nil {
		return Result{}, repositoryAdmissionUnavailable()
	}
	if req.Type == "clone" {
		if operations.process == nil {
			return Result{}, repositoryAdmissionUnavailable()
		}
		return operations.executeCreate(ctx, req)
	}
	return operations.local.CreateEmpty(ctx, req)
}

func (operations *Workflow) AddRepositories(
	ctx context.Context,
	req AddRepositoriesCommand,
) (Result, error) {
	if operations == nil || operations.local == nil {
		return Result{}, repositoryAdmissionUnavailable()
	}
	if operations.process == nil {
		if len(req.CloneURLs) > 0 {
			return Result{}, repositoryAdmissionUnavailable()
		}
		return operations.local.AddWithoutAdmission(ctx, req)
	}
	return operations.executeAdd(ctx, req)
}

func (operations *Workflow) executeCreate(ctx context.Context, command CreateCommand) (Result, error) {
	plan, record, err := operations.prepareCreate(ctx, command)
	if err != nil {
		return Result{}, err
	}
	if record.State == "committed" {
		operations.process.forgetPreparedRepositoryAdmission(record.AdmissionID)
		return operations.local.Replay(ctx, record, plan.WorkspacePath, true)
	}
	materializationCtx, owned, release, err := operations.process.beginMaterialization(ctx, record, plan.WorkspacePath)
	if err != nil {
		return Result{}, err
	}
	defer release()
	verify := func(checkCtx context.Context) error {
		return operations.process.verifyMaterializationOwnership(checkCtx, owned)
	}
	materialized, err := operations.local.MaterializeCreate(materializationCtx, command, plan, owned, verify)
	if err != nil {
		return Result{}, operations.process.failMaterialization(materializationCtx, owned, err)
	}
	if len(materialized.Repositories) != len(plan.Repositories) || materialized.DefaultBranch == "" {
		return Result{}, operations.process.failMaterialization(materializationCtx, owned, ErrInvalid)
	}
	committed, err := operations.process.commit(materializationCtx, owned, materialized.Repositories, &WorkspaceFinalization{
		State: "ready", DefaultBranch: materialized.DefaultBranch,
	})
	if err != nil {
		return Result{}, err
	}
	if committed == nil || committed.State != "committed" {
		return Result{}, ErrInvalid
	}
	return Result{WorkspaceID: plan.WorkspaceKey, WorkspacePath: plan.WorkspacePath}, nil
}

func (operations *Workflow) executeAdd(ctx context.Context, command AddRepositoriesCommand) (Result, error) {
	plan, record, err := operations.prepareAdd(ctx, command)
	if err != nil {
		return Result{}, err
	}
	if record.State == "committed" {
		operations.process.forgetPreparedRepositoryAdmission(record.AdmissionID)
		return operations.local.Replay(ctx, record, plan.WorkspacePath, false)
	}
	materializationCtx, owned, release, err := operations.process.beginMaterialization(ctx, record, plan.WorkspacePath)
	if err != nil {
		return Result{}, err
	}
	defer release()
	verify := func(checkCtx context.Context) error {
		return operations.process.verifyMaterializationOwnership(checkCtx, owned)
	}
	materialized, err := operations.local.MaterializeAdd(materializationCtx, command, plan, owned, verify)
	if err != nil {
		return Result{}, operations.process.failMaterialization(materializationCtx, owned, err)
	}
	if len(materialized.Repositories) != len(plan.Repositories) {
		return Result{}, operations.process.failMaterialization(materializationCtx, owned, ErrInvalid)
	}
	if _, err := operations.process.commit(materializationCtx, owned, materialized.Repositories, nil); err != nil {
		return Result{}, err
	}
	return Result{WorkspaceID: plan.WorkspaceKey, WorkspacePath: plan.WorkspacePath}, nil
}

func (operations *Workflow) prepareCreate(ctx context.Context, command CreateCommand) (CreatePlan, *Record, error) {
	if operations == nil || operations.process == nil || operations.local == nil {
		return CreatePlan{}, nil, repositoryAdmissionUnavailable()
	}
	plan, err := operations.local.PlanCreate(ctx, command)
	if err != nil {
		return CreatePlan{}, nil, err
	}
	record, err := operations.process.prepareCreate(ctx, WorkspaceInput{
		Key: plan.WorkspaceKey, Name: command.Name, State: "creating",
		DefaultBranch: strings.TrimSpace(command.Branch),
	}, plan.WorkspacePath, plan.Repositories, plan.CloneURLs, command.Branch)
	if err != nil {
		return CreatePlan{}, nil, err
	}
	return plan, record, nil
}

func (operations *Workflow) prepareAdd(ctx context.Context, command AddRepositoriesCommand) (AddPlan, *Record, error) {
	if operations == nil || operations.process == nil || operations.local == nil {
		return AddPlan{}, nil, repositoryAdmissionUnavailable()
	}
	plan, err := operations.local.PlanAdd(ctx, command)
	if err != nil {
		return AddPlan{}, nil, err
	}
	record, err := operations.process.prepareExisting(
		ctx, plan.WorkspaceKey, plan.WorkspacePath, plan.Repositories,
		plan.CloneURLs, plan.LocalRepoPaths, command.Branch,
	)
	if err != nil {
		return AddPlan{}, nil, err
	}
	return plan, record, nil
}

// StartCreate makes the complete token-free batch durable before returning its
// opaque admission ID. Immediate execution is an optimization over the durable
// recovery loop: FleetDB remains the only status authority if this process
// exits before or during materialization.
func (operations *Workflow) StartCreate(
	ctx context.Context,
	req CreateCommand,
) (string, error) {
	admissionID, err := operations.prepareCreateCommand(ctx, req)
	if err != nil {
		return "", err
	}
	operations.launchAcceptedAdmission(
		admissionID,
		"create_workspace",
		func(runCtx context.Context) error {
			_, runErr := operations.Create(runCtx, req)
			return runErr
		},
	)
	return admissionID, nil
}

// prepareCreateCommand persists the token-free creation batch without launching it.
// It remains private to the module's recovery tests; delivery uses StartCreate.
func (operations *Workflow) prepareCreateCommand(
	ctx context.Context,
	req CreateCommand,
) (string, error) {
	if operations == nil || operations.process == nil {
		return "", repositoryAdmissionUnavailable()
	}
	if req.Type != "clone" {
		return "", fmt.Errorf(
			"durable repository admission requires clone workspace type: %w",
			ErrInvalid,
		)
	}
	_, record, err := operations.prepareCreate(ctx, req)
	if err != nil {
		return "", err
	}
	if record == nil || strings.TrimSpace(record.AdmissionID) == "" {
		return "", ErrInvalid
	}
	operations.process.notePreparedRepositoryAdmission(record)
	return record.AdmissionID, nil
}

// StartAddRepos durably reserves the complete repository set before returning
// its admission ID. Local paths remain only in the protected local journal.
func (operations *Workflow) StartAddRepositories(
	ctx context.Context,
	req AddRepositoriesCommand,
) (string, error) {
	admissionID, err := operations.prepareAddRepositoriesCommand(ctx, req)
	if err != nil {
		return "", err
	}
	operations.launchAcceptedAdmission(
		admissionID,
		"add_repositories",
		func(runCtx context.Context) error {
			_, runErr := operations.AddRepositories(runCtx, req)
			return runErr
		},
	)
	return admissionID, nil
}

// prepareAddRepositoriesCommand persists the exact repository batch without launching it.
// It remains private to the module's recovery tests; delivery uses StartAddRepos.
func (operations *Workflow) prepareAddRepositoriesCommand(
	ctx context.Context,
	req AddRepositoriesCommand,
) (string, error) {
	if operations == nil || operations.process == nil {
		return "", repositoryAdmissionUnavailable()
	}
	_, record, err := operations.prepareAdd(ctx, req)
	if err != nil {
		return "", err
	}
	if record == nil || strings.TrimSpace(record.AdmissionID) == "" {
		return "", ErrInvalid
	}
	operations.process.notePreparedRepositoryAdmission(record)
	return record.AdmissionID, nil
}

func (operations *Workflow) launchAcceptedAdmission(
	admissionID,
	kind string,
	run func(context.Context) error,
) {
	go func() {
		ctx, cancel := context.WithTimeout(
			context.Background(),
			workspaceRepositoryAdmissionExecutionTimeout,
		)
		defer cancel()
		if err := run(ctx); err != nil {
			slog.Warn(
				"accepted repository admission did not complete in its immediate runner",
				"admission_id", admissionID,
				"kind", kind,
				"err", err,
			)
		}
	}()
}

//nolint:funlen // Job lookup joins the durable admission and protected local journal without weakening either source's validation.
func (operations *Workflow) Get(
	ctx context.Context,
	jobID string,
) (*Status, bool, error) {
	if operations == nil || operations.process == nil {
		return nil, false, nil
	}
	jobID, err := NormalizeID(jobID)
	if err != nil {
		return nil, false, nil
	}
	local, err := operations.process.journal.GetByAdmission(ctx, jobID)
	if errors.Is(err, ErrLocalNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	record, err := operations.process.admissions.Get(
		ctx,
		local.Intent.WorkspaceKey,
		local.AdmissionID,
	)
	if errors.Is(err, ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := verifyPreparedRepositoryAdmission(local, record); err != nil {
		return nil, false, err
	}
	job := &Status{
		ID: jobID, WorkspaceID: local.Intent.WorkspaceKey,
	}
	switch record.State {
	case "pending":
		job.State = StateRunning
		if local.Intent.Kind == KindCreateWorkspace {
			job.Progress = "cloning workspace repositories..."
		} else {
			job.Progress = "attaching workspace repositories..."
		}
	case "retryable_failed":
		job.State = StateFailed
		job.Error = "repository materialization was interrupted; retry the request"
		job.CompletedAt = record.UpdatedAt
	case "permanent_failed", "aborted":
		job.State = StateFailed
		job.Error = "repository materialization failed"
		job.CompletedAt = record.UpdatedAt
	case "committed":
		job.State = StateDone
		job.CompletedAt = record.UpdatedAt
	default:
		return nil, false, ErrInvalid
	}
	return job, true, nil
}

func (operations *Workflow) RuntimeRegistrations() []platformruntime.Registration {
	if operations == nil || operations.process == nil {
		return nil
	}
	return []platformruntime.Registration{
		{
			Component: workspaceRepositoryAdmissionRenewal{operations: operations},
			Policy: platformruntime.Policy{
				Cadence: 15 * time.Second, Immediate: true, Timeout: 10 * time.Second,
				FailureBackoff: platformruntime.Backoff{
					Initial: 5 * time.Second, Max: 15 * time.Second, Multiplier: 2,
				},
			},
		},
		{
			Component: workspaceRepositoryAdmissionRecovery{operations: operations},
			Policy: platformruntime.Policy{
				Cadence: 15 * time.Second, Immediate: true, Timeout: 6 * time.Minute,
				FailureBackoff: platformruntime.Backoff{
					Initial: 5 * time.Second, Max: time.Minute, Multiplier: 2,
				},
			},
		},
	}
}

// Stop cancels every in-flight machine-local materialization and waits for its
// owner-scoped context to unwind before serve releases Fleet/store resources.
func (operations *Workflow) Stop(
	ctx context.Context,
) error {
	if operations == nil || operations.process == nil {
		return nil
	}
	return operations.process.stopMaterializations(ctx)
}

type workspaceRepositoryAdmissionRenewal struct {
	operations *Workflow
}

func (renewal workspaceRepositoryAdmissionRenewal) ID() platformruntime.ComponentID {
	return workspaceRepositoryAdmissionRenewalComponentID
}

func (renewal workspaceRepositoryAdmissionRenewal) RunOnce(
	ctx context.Context,
	_ time.Time,
) error {
	if renewal.operations == nil || renewal.operations.process == nil {
		return repositoryAdmissionUnavailable()
	}
	return renewal.operations.process.renewActiveRepositoryAdmissions(ctx)
}

type workspaceRepositoryAdmissionRecovery struct {
	operations *Workflow
}

func (recovery workspaceRepositoryAdmissionRecovery) ID() platformruntime.ComponentID {
	return workspaceRepositoryAdmissionRecoveryComponentID
}

func (recovery workspaceRepositoryAdmissionRecovery) RunOnce(
	ctx context.Context,
	_ time.Time,
) error {
	if recovery.operations == nil {
		return repositoryAdmissionUnavailable()
	}
	return recovery.operations.recoverRepositoryAdmissions(ctx)
}

//nolint:gocognit,cyclop,funlen // Recovery enumerates durable and local-only cases explicitly so no divergent intent is silently adopted.
func (operations *Workflow) recoverRepositoryAdmissions(
	ctx context.Context,
) error {
	if operations == nil || operations.process == nil {
		return repositoryAdmissionUnavailable()
	}
	if err := operations.process.ensureMaterializationsRunning(); err != nil {
		return err
	}
	locals, err := operations.process.journal.List(ctx)
	if err != nil {
		return err
	}
	recoverableByWorkspace := make(
		map[string]map[string]*Record,
	)
	missingWorkspaces := make(map[string]bool)
	for _, local := range locals {
		if local.AdmissionID == "" {
			continue
		}
		workspace := local.Intent.WorkspaceKey
		if _, loaded := recoverableByWorkspace[workspace]; loaded {
			continue
		}
		records, listErr := operations.process.admissions.
			ListRecoverable(
				ctx,
				workspace,
				workspaceRepositoryAdmissionRecoveryLimit,
			)
		if listErr != nil {
			if errors.Is(listErr, ErrNotFound) {
				missingWorkspaces[workspace] = true
				recoverableByWorkspace[workspace] = map[string]*Record{}
				continue
			}
			return fmt.Errorf(
				"list recoverable repository admissions for %q: %w",
				workspace,
				listErr,
			)
		}
		byID := make(
			map[string]*Record,
			len(records),
		)
		for _, record := range records {
			if record != nil {
				byID[record.AdmissionID] = record
			}
		}
		recoverableByWorkspace[workspace] = byID
	}

	now := operations.process.now()
	var recoveryErrors []error
	restoredWorkspaces := make(map[string]bool)
	for _, local := range locals {
		if err := operations.process.ensureMaterializationsRunning(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if local.AdmissionID == "" &&
			now.Before(local.UpdatedAt.Add(workspaceRepositoryAdmissionStartGrace)) {
			continue
		}
		if local.AdmissionID != "" {
			workspace := local.Intent.WorkspaceKey
			if missingWorkspaces[workspace] {
				if local.Intent.Kind != KindCreateWorkspace && !restoredWorkspaces[workspace] {
					recoveryErrors = append(recoveryErrors, fmt.Errorf(
						"workspace %q has protected repository intent but no recoverable create operation: %w",
						workspace,
						ErrNotFound,
					))
					continue
				}
			} else {
				record, ok := recoverableByWorkspace[workspace][local.AdmissionID]
				if !ok {
					continue
				}
				if err := verifyPreparedRepositoryAdmission(local, record); err != nil {
					recoveryErrors = append(recoveryErrors, err)
					continue
				}
				if operations.process.isMaterializing(local.AdmissionID) {
					continue
				}
				if operations.process.recentlyPreparedRepositoryAdmission(
					local.AdmissionID,
					now,
					workspaceRepositoryAdmissionStartGrace,
				) {
					continue
				}
			}
		}
		if err := operations.recoverLocalRepositoryAdmission(ctx, local); err != nil {
			if errors.Is(err, ErrConflict) ||
				errors.Is(err, ErrFenceLost) ||
				errors.Is(err, ErrState) {
				continue
			}
			recoveryErrors = append(
				recoveryErrors,
				fmt.Errorf(
					"recover repository admission operation %q: %w",
					local.Intent.OperationID,
					err,
				),
			)
		} else if local.Intent.Kind == KindCreateWorkspace &&
			missingWorkspaces[local.Intent.WorkspaceKey] {
			restoredWorkspaces[local.Intent.WorkspaceKey] = true
		}
	}
	return errors.Join(recoveryErrors...)
}

func (operations *Workflow) recoverLocalRepositoryAdmission(
	ctx context.Context,
	local LocalRecord,
) error {
	if err := operations.verifyRecoveryIntent(ctx, local.Intent); err != nil {
		return err
	}
	switch local.Intent.Kind {
	case KindCreateWorkspace:
		_, err := operations.Create(
			ctx,
			CreateCommand{
				Name: local.Intent.WorkspaceName, Type: "clone",
				Path: local.Intent.WorkspacePath, Branch: local.Intent.Branch,
				CloneURLs: append([]string(nil), local.Intent.CloneURLs...),
			},
		)
		return err
	case KindAddRepositories:
		_, err := operations.AddRepositories(
			ctx,
			AddRepositoriesCommand{
				WorkspaceID: local.Intent.WorkspaceKey,
				Branch:      local.Intent.Branch,
				Repos:       append([]string(nil), local.Intent.LocalRepoPaths...),
				CloneURLs:   append([]string(nil), local.Intent.CloneURLs...),
			},
		)
		return err
	default:
		return ErrInvalid
	}
}

func (operations *Workflow) verifyRecoveryIntent(
	ctx context.Context,
	intent LocalIntent,
) error {
	if operations == nil || operations.local == nil {
		return repositoryAdmissionUnavailable()
	}
	return operations.local.VerifyRecoveryIntent(ctx, intent)
}

var _ interface {
	RuntimeRegistrations() []platformruntime.Registration
} = (*Workflow)(nil)

var (
	_ Admission = (*Workflow)(nil)
	_ Executor  = (*Workflow)(nil)
)
