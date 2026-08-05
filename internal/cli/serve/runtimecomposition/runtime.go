package runtimecomposition

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/metricscmd"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/serveadapter"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverexecutor "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type BuildDriverExecutor func(
	store.Store,
	string,
	driverexecutor.RunOutcomePublisher,
	webui.ExecutionCapability,
	int,
) (*driverexecutor.Executor, bool)

type IssueJournalConfig struct {
	WorkspaceScope string
	Interval       time.Duration
	StatePath      string
	Disabled       bool
	EmitTaskReady  bool
	EmitTaskReview bool
}

type Config struct {
	WorkspaceScope        string
	AwaitSweepInterval    time.Duration
	AwaitSweepBatch       int
	DriverExecutorEnabled bool
	TaskWorkerConcurrency int
	TaskWorkerRunnerID    string
	TaskRunMaxAttempts    int
	DriverAPIBaseURL      string
	LocalSettingsDir      string
	BuildDriverExecutor   BuildDriverExecutor
	IssueJournal          IssueJournalConfig
}

type Capabilities struct {
	WorkflowCatalog *serveadapter.WorkflowCatalogModule
	Automation      *serveadapter.AutomationCapability
	Runtime         []serveadapter.RuntimeContributor
}

type TaskReadyBridgeCallbacks struct {
	issueLookup               trigger.TaskReadyIssueLookup
	readySnapshots            trigger.TaskReadySnapshotLister
	repositoryRequiredBlocker trigger.TaskReadyRepositoryRequiredBlocker
}

type taskReadyRepositoryResolver func(context.Context, string, string, string) (bool, error)

type taskReadyRepositoryLister interface {
	List(context.Context, string) ([]*domain.Repo, error)
}

func BuildTaskReadyBridgeCallbacks(repositories taskReadyRepositoryLister, issueBackendFn metricscmd.IssueBackendFn) TaskReadyBridgeCallbacks {
	repositoryRequired := buildTaskReadyRepositoryResolver(repositories)
	return TaskReadyBridgeCallbacks{
		issueLookup:               buildTaskReadyIssueLookup(issueBackendFn, repositoryRequired),
		readySnapshots:            buildTaskReadySnapshotLister(repositories, issueBackendFn),
		repositoryRequiredBlocker: buildTaskReadyRepositoryRequiredBlocker(issueBackendFn),
	}
}

func buildTaskReadyRepositoryResolver(repositories taskReadyRepositoryLister) taskReadyRepositoryResolver {
	return func(ctx context.Context, ws, issueType, sourceRepo string) (bool, error) {
		if strings.EqualFold(strings.TrimSpace(issueType), "epic") || strings.TrimSpace(sourceRepo) != "" {
			return false, nil
		}
		repos, err := repositories.List(ctx, ws)
		if err != nil {
			return false, fmt.Errorf("list repositories for task-ready issue in workspace %q: %w", ws, err)
		}
		return TaskReadyRepositoryRequired(issueType, sourceRepo, len(repos)), nil
	}
}

func buildTaskReadyIssueLookup(
	issueBackendFn metricscmd.IssueBackendFn,
	repositoryRequired taskReadyRepositoryResolver,
) trigger.TaskReadyIssueLookup {
	return func(ctx context.Context, ws, issueID string) (trigger.TaskReadySnapshot, error) {
		detail, err := issueBackendFn(middleware.WithWorkspace(ctx, ws)).Get(ctx, issueID)
		if err != nil {
			if backend.IsKind(err, backend.KindNotFound) {
				return trigger.TaskReadySnapshot{}, domain.ErrNotFound
			}
			return trigger.TaskReadySnapshot{}, err
		}
		if detail == nil {
			return trigger.TaskReadySnapshot{}, domain.ErrNotFound
		}
		repoRequired, err := repositoryRequired(ctx, ws, detail.IssueType, detail.SourceRepo)
		if err != nil {
			return trigger.TaskReadySnapshot{}, err
		}
		return trigger.TaskReadySnapshot{
			TaskID: issueID, Status: detail.Status,
			HasDesign: detail.HasDesign || strings.TrimSpace(detail.Design) != "",
			Labels:    append([]string(nil), detail.Labels...), IssueType: detail.IssueType,
			SourceRepo: detail.SourceRepo, RepositoryRequired: repoRequired, UpdatedAt: detail.UpdatedAt,
		}, nil
	}
}

func buildTaskReadySnapshotLister(repositories taskReadyRepositoryLister, issueBackendFn metricscmd.IssueBackendFn) trigger.TaskReadySnapshotLister {
	return func(ctx context.Context, ws string) ([]trigger.TaskReadySnapshot, error) {
		issues, err := issueBackendFn(middleware.WithWorkspace(ctx, ws)).Ready(ctx, backend.ReadyOpts{
			Unassigned: true,
			// Reconciliation is exhaustive; a cap would strand later tasks.
			Limit: 0,
		})
		if err != nil {
			return nil, err
		}
		repoCount, err := taskReadyReconciliationRepoCount(ctx, repositories, ws, issues)
		if err != nil {
			return nil, err
		}
		snapshots := make([]trigger.TaskReadySnapshot, 0, len(issues))
		for _, issue := range issues {
			snapshots = append(snapshots, trigger.TaskReadySnapshot{
				TaskID: issue.ID, Status: issue.Status, HasDesign: issue.HasDesign,
				Labels: append([]string(nil), issue.Labels...), IssueType: issue.IssueType,
				SourceRepo:         issue.SourceRepo,
				RepositoryRequired: TaskReadyRepositoryRequired(issue.IssueType, issue.SourceRepo, repoCount),
				UpdatedAt:          issue.UpdatedAt,
			})
		}
		return snapshots, nil
	}
}

func taskReadyReconciliationRepoCount(
	ctx context.Context,
	repositories taskReadyRepositoryLister,
	ws string,
	issues []backend.IssueData,
) (int, error) {
	for _, issue := range issues {
		if !strings.EqualFold(strings.TrimSpace(issue.IssueType), "epic") && strings.TrimSpace(issue.SourceRepo) == "" {
			repos, err := repositories.List(ctx, ws)
			if err != nil {
				return 0, fmt.Errorf("list repositories for task-ready reconciliation in workspace %q: %w", ws, err)
			}
			return len(repos), nil
		}
	}
	return 1, nil
}

func buildTaskReadyRepositoryRequiredBlocker(
	issueBackendFn metricscmd.IssueBackendFn,
) trigger.TaskReadyRepositoryRequiredBlocker {
	return func(ctx context.Context, ws, issueID string) (trigger.TaskReadyRepositoryRequiredResult, error) {
		// The Work Items-owned conditional command revalidates any snapshot that
		// raced a repository assignment or claim.
		issueBackend := issueBackendFn(middleware.WithWorkspace(ctx, ws))
		return BlockRepositoryRequiredTask(ctx, issueBackend, issueID)
	}
}

type Stop func(context.Context) error

func Start(
	ctx context.Context,
	storeHandle *bootstrap.StoreHandle,
	cfg webui.ServerConfig,
	capabilities Capabilities,
	callbacks TaskReadyBridgeCallbacks,
	config Config,
) (Stop, error) {
	automationCapability := capabilities.Automation
	if automationCapability != nil {
		if err := serveadapter.RefreshBoundPromptAgentWorkflows(
			ctx,
			storeHandle,
			capabilities.WorkflowCatalog,
		); err != nil {
			return nil, fmt.Errorf("refresh bound prompt-agent workflows: %w", err)
		}
	}
	issueJournalSource, runOutcomes := serveAutomationRuntimePorts(storeHandle, cfg, automationCapability)
	StartIssueJournalBridge(
		ctx, storeHandle.Store, callbacks.issueLookup, callbacks.readySnapshots,
		callbacks.repositoryRequiredBlocker, issueJournalSource, config.IssueJournal,
	)
	executionPasses, err := buildExecutionRuntimePasses(
		storeHandle.Store, runOutcomes, cfg.ExecutionCapability, cfg.ArtifactsCapability, cfg.SourceControl, config,
	)
	if err != nil {
		return nil, fmt.Errorf("compose Execution compatibility passes: %w", err)
	}
	executionRuntime, err := serveadapter.BuildExecutionRuntimeContributor(
		executionPasses, cfg.ExecutionCapability, config.WorkspaceScope,
		config.AwaitSweepInterval,
	)
	if err != nil {
		return nil, fmt.Errorf("compose Execution runtime: %w", err)
	}
	runtimeHost, err := serveadapter.BuildServeRuntimeHost(
		storeHandle.Store.DriverRuns(), storeHandle.Store.Awaits(), storeHandle.Store.TriggerEvents(),
		storeHandle.Store.Workspaces(), runOutcomes, config.WorkspaceScope,
		cfg.ExecutionCapability, executionRuntime, capabilities.Runtime...,
	)
	if err != nil {
		return nil, fmt.Errorf("compose platform runtime: %w", err)
	}
	if err := runtimeHost.Start(ctx); err != nil {
		return nil, err
	}
	return runtimeHost.Stop, nil
}

func serveAutomationRuntimePorts(
	storeHandle *bootstrap.StoreHandle,
	cfg webui.ServerConfig,
	automationCapability *serveadapter.AutomationCapability,
) (trigger.InternalEventEmitter, driverexecutor.RunOutcomePublisher) {
	if automationCapability == nil {
		return nil, nil
	}
	awaitResolver := &driverexecutor.ExecutionAwaitResolver{
		API:         cfg.ExecutionCapability.DriverRunAPI(),
		Authorities: cfg.ExecutionCapability.SystemAuthorityResolver(),
		ComponentID: "serve-await-event-notifications",
	}
	issueJournalSource := serveadapter.NewAutomationIssueJournalEmitter(
		automationCapability.IssueJournalEmitter(),
		trigger.NewAwaitMatcherWithResolver(storeHandle.Store.Awaits(), storeHandle.Store.DriverRuns(), awaitResolver),
	)
	return issueJournalSource, automationCapability.RunOutcomePublisher()
}
