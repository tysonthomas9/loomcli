package workspacemgr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr/admissionstore"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/workspacecatalog"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
)

const (
	workspaceRepositoryAdmissionRecoveryComponentID platformruntime.ComponentID = "workspace-repository-admission-recovery"
	workspaceRepositoryAdmissionRenewalComponentID  platformruntime.ComponentID = "workspace-repository-admission-lease-renewal"
	workspaceRepositoryAdmissionRecoveryLimit                                   = 100
	workspaceRepositoryAdmissionStartGrace                                      = 30 * time.Second
)

// StoreBackedWorkspaceAdmissionOperations is the one serve-incarnation
// coordinator for Workspace repository materialization. It implements the
// synchronous WebUI admission seam and contributes restart recovery to the
// platform runtime host while exposing neither FleetDB nor Source Control.
type StoreBackedWorkspaceAdmissionOperations struct {
	store     admissionstore.Store
	workspace workspacemodule.API
	process   *repositoryAdmissionProcess
}

func NewStoreBackedWorkspaceAdmissionOperations(
	store admissionstore.Store,
	admissions infrafleetdb.RepositoryAdmissionTransport,
	journal *RepositoryAdmissionJournal,
	materializer repositoryCheckoutMaterializer,
) *StoreBackedWorkspaceAdmissionOperations {
	if store == nil {
		return nil
	}
	workspace, err := workspacecatalog.New(store.Workspaces(), store.Repos())
	if err != nil {
		return nil
	}
	return NewStoreBackedWorkspaceAdmissionOperationsWithWorkspace(
		store, workspace, admissions, journal, materializer,
	)
}

func NewStoreBackedWorkspaceAdmissionOperationsWithWorkspace(
	store admissionstore.Store,
	workspace workspacemodule.API,
	admissions infrafleetdb.RepositoryAdmissionTransport,
	journal *RepositoryAdmissionJournal,
	materializer repositoryCheckoutMaterializer,
) *StoreBackedWorkspaceAdmissionOperations {
	if store == nil {
		return nil
	}
	return &StoreBackedWorkspaceAdmissionOperations{
		store: store, workspace: workspace,
		process: newRepositoryAdmissionProcess(admissions, journal, materializer),
	}
}

func (operations *StoreBackedWorkspaceAdmissionOperations) CreateWorkspace(
	ctx context.Context,
	req workspacecoord.WorkspaceCreateRequest,
) (workspacecoord.WorkspaceCreateResult, error) {
	if operations == nil || operations.store == nil {
		return workspacecoord.WorkspaceCreateResult{}, repositoryAdmissionUnavailable()
	}
	if req.Type == "clone" {
		if operations.process == nil {
			return workspacecoord.WorkspaceCreateResult{}, repositoryAdmissionUnavailable()
		}
		return createStoreBackedCloneWorkspaceAdmission(
			ctx,
			operations.store,
			req,
			operations.process,
		)
	}
	return createStoreBackedEmptyWorkspace(ctx, operations.store, operations.workspace, req)
}

func (operations *StoreBackedWorkspaceAdmissionOperations) AddWorkspaceRepos(
	ctx context.Context,
	req workspacecoord.WorkspaceAddReposRequest,
) (workspacecoord.WorkspaceCreateResult, error) {
	if operations == nil || operations.store == nil {
		return workspacecoord.WorkspaceCreateResult{}, repositoryAdmissionUnavailable()
	}
	if operations.process == nil {
		if len(req.CloneURLs) > 0 {
			return workspacecoord.WorkspaceCreateResult{}, repositoryAdmissionUnavailable()
		}
		return addReposToStoreBackedWorkspace(
			ctx,
			operations.workspace,
			req,
			nil,
		)
	}
	return addReposToStoreBackedWorkspaceAdmission(
		ctx,
		operations.store,
		operations.workspace,
		req,
		operations.process,
	)
}

// PrepareCreate makes the complete token-free batch durable before the WebUI
// returns its opaque job ID. The process-local runner may then safely start
// after the request has ended or be recovered by a later serve incarnation.
func (operations *StoreBackedWorkspaceAdmissionOperations) PrepareCreate(
	ctx context.Context,
	req workspacecoord.WorkspaceCreateRequest,
) (string, error) {
	if operations == nil || operations.process == nil {
		return "", repositoryAdmissionUnavailable()
	}
	if req.Type != "clone" {
		return "", fmt.Errorf(
			"durable repository admission requires clone workspace type: %w",
			infrafleetdb.ErrRepositoryAdmissionInvalid,
		)
	}
	plan, err := prepareStoreBackedCloneWorkspaceAdmission(
		ctx,
		req,
		operations.process,
	)
	if err != nil {
		return "", err
	}
	if plan.record == nil || strings.TrimSpace(plan.record.AdmissionID) == "" {
		return "", infrafleetdb.ErrRepositoryAdmissionInvalid
	}
	operations.process.notePreparedRepositoryAdmission(plan.record)
	return plan.record.AdmissionID, nil
}

// PrepareAddRepos reserves the whole repository set before the async clone
// runner is scheduled. Local paths remain only in the protected local journal.
func (operations *StoreBackedWorkspaceAdmissionOperations) PrepareAddRepos(
	ctx context.Context,
	req workspacecoord.WorkspaceAddReposRequest,
) (string, error) {
	if operations == nil || operations.process == nil {
		return "", repositoryAdmissionUnavailable()
	}
	plan, err := prepareAddReposToStoreBackedWorkspaceAdmission(
		ctx,
		operations.workspace,
		req,
		operations.process,
	)
	if err != nil {
		return "", err
	}
	if plan.record == nil || strings.TrimSpace(plan.record.AdmissionID) == "" {
		return "", infrafleetdb.ErrRepositoryAdmissionInvalid
	}
	operations.process.notePreparedRepositoryAdmission(plan.record)
	return plan.record.AdmissionID, nil
}

//nolint:funlen // Job lookup joins the durable admission and protected local journal without weakening either source's validation.
func (operations *StoreBackedWorkspaceAdmissionOperations) LookupJob(
	ctx context.Context,
	jobID string,
) (*workspacecoord.WorkspaceJob, bool, error) {
	if operations == nil || operations.process == nil {
		return nil, false, nil
	}
	jobID, err := normalizeLocalRepositoryAdmissionID(jobID)
	if err != nil {
		return nil, false, nil
	}
	local, err := operations.process.journal.GetByAdmission(ctx, jobID)
	if errors.Is(err, errLocalRepositoryAdmissionNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	record, err := operations.process.admissions.GetRepositoryAdmission(
		ctx,
		local.Intent.WorkspaceKey,
		jobID,
	)
	if errors.Is(err, infrafleetdb.ErrRepositoryAdmissionNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := verifyPreparedRepositoryAdmission(local, record); err != nil {
		return nil, false, err
	}
	job := &workspacecoord.WorkspaceJob{
		ID: jobID, WorkspaceID: local.Intent.WorkspaceKey,
	}
	switch record.State {
	case "pending":
		job.Status = workspacecoord.JobStatusRunning
		if local.Intent.Kind == localRepositoryAdmissionCreateWorkspace {
			job.Progress = "cloning workspace repositories..."
		} else {
			job.Progress = "attaching workspace repositories..."
		}
	case "retryable_failed":
		job.Status = workspacecoord.JobStatusFailed
		job.Error = "repository materialization was interrupted; retry the request"
		job.CompletedAt = record.UpdatedAt
	case "permanent_failed", "aborted":
		job.Status = workspacecoord.JobStatusFailed
		job.Error = "repository materialization failed"
		job.CompletedAt = record.UpdatedAt
	case "committed":
		job.Status = workspacecoord.JobStatusDone
		job.CompletedAt = record.UpdatedAt
	default:
		return nil, false, infrafleetdb.ErrRepositoryAdmissionInvalid
	}
	return job, true, nil
}

func (operations *StoreBackedWorkspaceAdmissionOperations) RuntimeRegistrations() []platformruntime.Registration {
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
func (operations *StoreBackedWorkspaceAdmissionOperations) Stop(
	ctx context.Context,
) error {
	if operations == nil || operations.process == nil {
		return nil
	}
	return operations.process.stopMaterializations(ctx)
}

type workspaceRepositoryAdmissionRenewal struct {
	operations *StoreBackedWorkspaceAdmissionOperations
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
	operations *StoreBackedWorkspaceAdmissionOperations
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
func (operations *StoreBackedWorkspaceAdmissionOperations) recoverRepositoryAdmissions(
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
		map[string]map[string]*infrafleetdb.RepositoryAdmissionRecord,
	)
	for _, local := range locals {
		if local.AdmissionID == "" {
			continue
		}
		workspace := local.Intent.WorkspaceKey
		if _, loaded := recoverableByWorkspace[workspace]; loaded {
			continue
		}
		records, listErr := operations.process.admissions.
			ListRecoverableRepositoryAdmissions(
				ctx,
				workspace,
				workspaceRepositoryAdmissionRecoveryLimit,
			)
		if listErr != nil {
			return fmt.Errorf(
				"list recoverable repository admissions for %q: %w",
				workspace,
				listErr,
			)
		}
		byID := make(
			map[string]*infrafleetdb.RepositoryAdmissionRecord,
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
			record, ok := recoverableByWorkspace[local.Intent.WorkspaceKey][local.AdmissionID]
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
		if err := operations.recoverLocalRepositoryAdmission(ctx, local); err != nil {
			if errors.Is(err, infrafleetdb.ErrRepositoryAdmissionConflict) ||
				errors.Is(err, infrafleetdb.ErrRepositoryAdmissionFenceLost) ||
				errors.Is(err, infrafleetdb.ErrRepositoryAdmissionState) {
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
		}
	}
	return errors.Join(recoveryErrors...)
}

func (operations *StoreBackedWorkspaceAdmissionOperations) recoverLocalRepositoryAdmission(
	ctx context.Context,
	local localRepositoryAdmissionRecord,
) error {
	if err := operations.verifyRecoveryIntent(ctx, local.Intent); err != nil {
		return err
	}
	switch local.Intent.Kind {
	case localRepositoryAdmissionCreateWorkspace:
		_, err := operations.CreateWorkspace(
			ctx,
			workspacecoord.WorkspaceCreateRequest{
				Name: local.Intent.WorkspaceName, Type: "clone",
				Path: local.Intent.WorkspacePath, Branch: local.Intent.Branch,
				CloneURLs: append([]string(nil), local.Intent.CloneURLs...),
			},
		)
		return err
	case localRepositoryAdmissionAddRepositories:
		_, err := operations.AddWorkspaceRepos(
			ctx,
			workspacecoord.WorkspaceAddReposRequest{
				WorkspaceID: local.Intent.WorkspaceKey,
				Branch:      local.Intent.Branch,
				Repos:       append([]string(nil), local.Intent.LocalRepoPaths...),
				CloneURLs:   append([]string(nil), local.Intent.CloneURLs...),
			},
		)
		return err
	default:
		return infrafleetdb.ErrRepositoryAdmissionInvalid
	}
}

// verifyRecoveryIntent proves that mutable local Git/catalog state still
// derives the journal's exact operation ID before recovery can issue any new
// FleetDB Begin. Drift therefore fails closed instead of minting a successor
// admission for a different batch.
//
//nolint:funlen // Recovery admission compares every canonical workspace and repository field before reusing a durable generation.
func (operations *StoreBackedWorkspaceAdmissionOperations) verifyRecoveryIntent(
	ctx context.Context,
	intent localRepositoryAdmissionIntent,
) error {
	var candidate string
	switch intent.Kind {
	case localRepositoryAdmissionCreateWorkspace:
		if intent.WorkspaceName == "" || len(intent.CloneURLs) == 0 ||
			len(intent.LocalRepoPaths) != 0 {
			return infrafleetdb.ErrRepositoryAdmissionInvalid
		}
		planned, err := planCloneRepos(intent.CloneURLs, make(map[string]bool))
		if err != nil {
			return err
		}
		key := workspacecoord.WorkspaceKeyFromName(intent.WorkspaceName)
		if key != intent.WorkspaceKey {
			return infrafleetdb.ErrRepositoryAdmissionInvalid
		}
		candidate, err = repositoryAdmissionOperationID(
			"create_workspace",
			key,
			intent.WorkspacePath,
			cloneAdmissionSpecs(planned, intent.Branch),
		)
		if err != nil {
			return err
		}
	case localRepositoryAdmissionAddRepositories:
		workspaceKey, workspace, err := resolveWorkspaceForAddRepos(
			ctx,
			operations.workspace,
			intent.WorkspaceKey,
		)
		if err != nil {
			return err
		}
		if workspaceKey != intent.WorkspaceKey {
			return infrafleetdb.ErrRepositoryAdmissionInvalid
		}
		workspacePath, err := prepareWorkspaceDir(workspaceKey)
		if err != nil {
			return err
		}
		if workspacePath != intent.WorkspacePath {
			return infrafleetdb.ErrRepositoryAdmissionInvalid
		}
		localRepos, err := resolveRequestRepos(intent.LocalRepoPaths)
		if err != nil {
			return err
		}
		seen, err := dedupAddReposAgainstExisting(
			ctx,
			operations.workspace,
			workspaceKey,
			localRepos,
		)
		if err != nil {
			return err
		}
		plannedClones, err := planCloneRepos(intent.CloneURLs, seen)
		if err != nil {
			return err
		}
		branch := pickAddReposBranch(intent.Branch, workspace, workspaceKey)
		_, localSpecs, err := planLocalAdmissionRepos(
			localRepos,
			workspacePath,
			branch,
			intent.Branch,
		)
		if err != nil {
			return err
		}
		specs := append(
			append([]infrafleetdb.RepositoryAdmissionRepoSpec(nil), localSpecs...),
			cloneAdmissionSpecs(plannedClones, intent.Branch)...,
		)
		candidate, err = repositoryAdmissionOperationID(
			"add_repositories",
			workspaceKey,
			workspacePath,
			specs,
		)
		if err != nil {
			return err
		}
	default:
		return infrafleetdb.ErrRepositoryAdmissionInvalid
	}
	if candidate != intent.OperationID {
		return fmt.Errorf(
			"repository admission recovery intent drifted from %q to %q: %w",
			intent.OperationID,
			candidate,
			infrafleetdb.ErrRepositoryAdmissionConflict,
		)
	}
	return nil
}

var (
	_ workspacecoord.WorkspaceAdmissionCoordinator = (*StoreBackedWorkspaceAdmissionOperations)(nil)
	_ interface {
		RuntimeRegistrations() []platformruntime.Registration
	} = (*StoreBackedWorkspaceAdmissionOperations)(nil)
)
