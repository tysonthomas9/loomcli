package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/driver"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	automationfleetdb "github.com/tysonthomas9/loomcli/internal/modules/automation/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type DriverRunStore = store.DriverRunStore
type AwaitStore = store.AwaitStore
type WorkspaceStore = store.WorkspaceStore
type WebhookVerifier = webhookingestion.Verifier

// NewAwaitEventReconcilerWithExecutionStores composes Automation's durable
// await-notification loop without exposing its infrastructure type to the
// outer serve package.
func NewAwaitEventReconcilerWithExecutionStores(
	queue execution.AwaitEventNotificationAPI,
	authorities execution.SystemAuthorityResolver,
	awaits store.AwaitStore,
	driverRuns store.DriverRunStore,
	resolver store.AtomicAwaitStore,
	workspace string,
	workspaces interface {
		ListWorkspaceKeys(context.Context) ([]string, error)
	},
	componentID string,
) (*trigger.AwaitEventReconciler, error) {
	return trigger.NewAwaitEventReconcilerWithExecutionStores(
		queue, authorities, awaits, driverRuns, resolver,
		workspace, workspaces, componentID,
	)
}

// WorkflowTarget is the composition projection of one prepared legacy
// workflow.
type WorkflowTarget struct {
	DriverID        string
	DriverVersionID string
}

type WorkflowTargetPreparation func(
	context.Context,
	string,
	string,
) (WorkflowTarget, error)

type WorkflowTargetPreparationFactory func(error) WorkflowTargetPreparation

type configuredWorkflowTargetPreparer struct {
	prepare WorkflowTargetPreparation
}

func NewWorkflowTargetPreparer(
	prepare WorkflowTargetPreparation,
) workflowbinding.WorkflowTargetPreparer {
	return &configuredWorkflowTargetPreparer{prepare: prepare}
}

var _ workflowbinding.WorkflowTargetPreparer = (*configuredWorkflowTargetPreparer)(nil)

func (preparer *configuredWorkflowTargetPreparer) PrepareWorkflowTarget(
	ctx context.Context,
	workspace,
	workflow string,
) (workflowbinding.WorkflowTarget, error) {
	if preparer == nil || preparer.prepare == nil {
		return workflowbinding.WorkflowTarget{}, workflowbinding.ErrUnavailable
	}
	target, err := preparer.prepare(ctx, workspace, workflow)
	if err != nil {
		return workflowbinding.WorkflowTarget{}, err
	}
	return workflowbinding.WorkflowTarget{
		DriverID: target.DriverID, DriverVersionID: target.DriverVersionID,
	}, nil
}

// CatalogOwner supplies only the exact Workflow Catalog authority and query
// ports Automation consumes.
type CatalogOwner struct {
	Issuer                    *authority.Issuer
	EffectiveVersions         workflowcatalog.EffectiveVersionResolver
	EffectiveVersionAuthority EffectiveVersionAuthority
	OperatorResolver          workflowcataloghttp.OperatorAuthorityResolver
}

type automationWorkflowCatalogConfig struct {
	Workspace             string
	FleetDBClient         *infrafleetdb.Client
	DriverRuns            store.DriverRunStore
	Awaits                store.AwaitStore
	Workspaces            store.WorkspaceStore
	WebhookVerifier       webhookingestion.Verifier
	PrepareWorkflowTarget WorkflowTargetPreparationFactory
	Catalog               CatalogOwner
}

// ExecutionAwaitResolverBinding bridges Workflow Catalog/Automation startup
// ordering to Execution without exposing a mutable Store or issuer.
type ExecutionAwaitResolverBinding struct {
	mu       sync.RWMutex
	resolver store.AtomicAwaitStore
}

func (binding *ExecutionAwaitResolverBinding) Bind(resolver store.AtomicAwaitStore) {
	if binding == nil {
		return
	}
	binding.mu.Lock()
	binding.resolver = resolver
	binding.mu.Unlock()
}

func (binding *ExecutionAwaitResolverBinding) ResolveAwaitAndResume(
	ctx context.Context,
	workspace,
	instanceKey,
	eventID string,
	payload json.RawMessage,
	actor string,
) error {
	if binding == nil {
		return execution.ErrUnavailable
	}
	binding.mu.RLock()
	resolver := binding.resolver
	binding.mu.RUnlock()
	if resolver == nil {
		return execution.ErrUnavailable
	}
	return resolver.ResolveAwaitAndResume(ctx, workspace, instanceKey, eventID, payload, actor)
}

//nolint:funlen // Composition intentionally wires the complete capability graph in one visible sequence.
func ComposeWorkflowCatalogAutomation(
	config automationWorkflowCatalogConfig,
) (*AutomationCapability, *ExecutionAwaitResolverBinding, error) {
	workflowTargets, err := composeWorkflowTargetPreparer(config)
	if err != nil {
		return nil, nil, err
	}
	automationAdapter, err := automationfleetdb.New(
		newAutomationFleetDBTransport(config.FleetDBClient),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("compose automation FleetDB adapter: %w", err)
	}
	approvalEvents, ok := config.FleetDBClient.TriggerEvents().(automation.ApprovalEventStore)
	if !ok {
		return nil, nil, fmt.Errorf("compose automation approval journal: %w", automation.ErrUnavailable)
	}
	workspaceLister := newAutomationWorkspaceLister(config.Workspaces)
	awaitResolver := &ExecutionAwaitResolverBinding{}
	awaitNotifier, err := trigger.NewAutomationAwaitEventNotifierWithResolver(
		config.Awaits,
		config.DriverRuns,
		awaitResolver,
	)
	if err != nil {
		return nil, nil, err
	}
	automationCapability, err := composeAutomationCapability(
		automationCapabilityConfig{
			enabled: true, workspaceKey: strings.TrimSpace(config.Workspace),
			catalog: catalogOwner{
				issuer: config.Catalog.Issuer, effectiveVersions: config.Catalog.EffectiveVersions,
				effectiveVersionAuthority: config.Catalog.EffectiveVersionAuthority,
				operatorResolver:          config.Catalog.OperatorResolver,
			},
		},
		automationCapabilityDependencies{
			bindings: automationAdapter, unmanagedBindings: automationAdapter,
			managedBindings: automationAdapter, matcher: automationAdapter,
			events: automationAdapter, deliveries: automationAdapter,
			admissions: automationAdapter, approvalEvents: approvalEvents,
			execution: newAutomationExecutionPort(
				config.DriverRuns,
				newAutomationFleetExecutionDispatch(config.FleetDBClient),
			),
			cron: automationAdapter, retries: automationAdapter, awaits: awaitNotifier,
			workspaces: workspaceLister, webhookVerifier: config.WebhookVerifier,
			workflowTargets: workflowTargets,
		},
	)
	if err != nil {
		return nil, nil, err
	}
	return automationCapability, awaitResolver, nil
}

func composeWorkflowTargetPreparer(
	config automationWorkflowCatalogConfig,
) (workflowbinding.WorkflowTargetPreparer, error) {
	if config.DriverRuns == nil || config.Awaits == nil || config.Workspaces == nil ||
		config.WebhookVerifier == nil || config.PrepareWorkflowTarget == nil {
		return nil, fmt.Errorf(
			"compose automation compatibility adapters: required narrow stores are unavailable",
		)
	}
	prepareWorkflowTarget := config.PrepareWorkflowTarget(workflowbinding.ErrUnavailable)
	if prepareWorkflowTarget == nil {
		return nil, fmt.Errorf(
			"compose automation compatibility adapters: workflow target preparation is unavailable",
		)
	}
	return &configuredWorkflowTargetPreparer{prepare: prepareWorkflowTarget}, nil
}

func OperatorActions() []authority.Action {
	return automationOperatorActions()
}

type automationWorkspaceLister struct {
	workspaces store.WorkspaceStore
}

var _ automation.WorkspaceLister = (*automationWorkspaceLister)(nil)

func newAutomationWorkspaceLister(workspaces store.WorkspaceStore) automation.WorkspaceLister {
	if workspaces == nil {
		return nil
	}
	return &automationWorkspaceLister{workspaces: workspaces}
}

func NewAutomationWorkspaceLister(workspaces store.WorkspaceStore) automation.WorkspaceLister {
	return newAutomationWorkspaceLister(workspaces)
}

func (lister *automationWorkspaceLister) ListWorkspaceKeys(ctx context.Context) ([]string, error) {
	if lister == nil || lister.workspaces == nil {
		return nil, automation.ErrUnavailable
	}
	values, err := lister.workspaces.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Automation workspaces: %w", err)
	}
	keys := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == nil {
			return nil, automation.ErrInvalidPersistedState
		}
		key := strings.TrimSpace(value.Key)
		if key == "" || key != value.Key {
			return nil, automation.ErrInvalidPersistedState
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, automation.ErrInvalidPersistedState
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

// automationDriverRunOutcomePublisher adapts Execution's consumer-owned
// outcome port at the composition boundary. The named SystemEventing workflow
// remains independent of Execution and receives only its own verified-source
// envelope and event-content request.
type automationDriverRunOutcomePublisher struct {
	emitter systemeventing.RunOutcomeEmitter
}

var _ driver.RunOutcomePublisher = (*automationDriverRunOutcomePublisher)(nil)

func newAutomationDriverRunOutcomePublisher(emitter systemeventing.RunOutcomeEmitter) (driver.RunOutcomePublisher, error) {
	if emitter == nil {
		return nil, fmt.Errorf("%w: run outcome emitter is required", systemeventing.ErrUnavailable)
	}
	return &automationDriverRunOutcomePublisher{emitter: emitter}, nil
}

func NewDriverRunOutcomePublisher(
	emitter systemeventing.RunOutcomeEmitter,
) (driver.RunOutcomePublisher, error) {
	return newAutomationDriverRunOutcomePublisher(emitter)
}

func (publisher *automationDriverRunOutcomePublisher) PublishRunOutcome(ctx context.Context, outcome driver.RunOutcome) error {
	if publisher == nil || publisher.emitter == nil {
		return systemeventing.ErrUnavailable
	}
	if outcome.EventType != driver.RunFinishedEventType ||
		outcome.EventID != driver.RunFinishedEventID(outcome.RunID, outcome.Status) ||
		!outcome.Status.IsTerminal() || outcome.ActorRef != driver.RunFinishedActor {
		return fmt.Errorf("%w: invalid driver run outcome envelope", systemeventing.ErrInvalidRequest)
	}
	_, err := publisher.emitter.EmitRunOutcome(ctx, outcome.WorkspaceKey, driver.RunFinishedActor, systemeventing.EmitRequest{
		WorkspaceKey:  outcome.WorkspaceKey,
		SourceEventID: outcome.EventID,
		EventType:     outcome.EventType,
		SourceRef:     outcome.RunID,
		SubjectRef:    outcome.RunID,
		ParentEventID: outcome.ParentEventID,
		EpicID:        outcome.EpicID,
		OccurredAt:    outcome.OccurredAt,
		Payload:       outcome.Payload,
	})
	if err != nil {
		return fmt.Errorf("publish driver run outcome %q: %w", outcome.EventID, err)
	}
	return nil
}

const (
	runOutcomeReconcileCadence = 5 * time.Second
	runOutcomeReconcileTimeout = 30 * time.Second
)

const RunOutcomeReconcileCadence = runOutcomeReconcileCadence

type runOutcomeRuntimeComponent struct {
	reconciler *driver.RunOutcomeReconciler
}

var _ platformruntime.Component = (*runOutcomeRuntimeComponent)(nil)

func (*runOutcomeRuntimeComponent) ID() platformruntime.ComponentID {
	return platformruntime.ComponentID(systemeventing.DriverRunOutcomeComponentID)
}

func (component *runOutcomeRuntimeComponent) RunOnce(ctx context.Context, now time.Time) error {
	if component == nil || component.reconciler == nil {
		return fmt.Errorf("driver run outcome reconciler is unavailable")
	}
	return component.reconciler.RunOnce(ctx, now)
}

func NewRunOutcomeRuntimeRegistrationWithExecution(
	awaits store.AwaitStore,
	triggerEvents store.TriggerEventStore,
	workspacesStore store.WorkspaceStore,
	publisher driver.RunOutcomePublisher,
	workspace string,
	api execution.DriverRunAPI,
	queue execution.DriverRunOutcomeAPI,
	terminalWorkQueue execution.TerminalDriverRunWorkRecoveryQueueAPI,
	authorities execution.SystemAuthorityResolver,
) (platformruntime.Registration, error) {
	if api == nil || queue == nil || terminalWorkQueue == nil || authorities == nil {
		return platformruntime.Registration{}, fmt.Errorf("compose driver run outcome runtime: Execution await and recovery queue APIs are unavailable")
	}
	resolver := &driver.ExecutionAwaitResolver{
		API: api, Authorities: authorities, ComponentID: driver.RunOutcomeAwaitComponentID,
	}
	if awaits == nil || triggerEvents == nil || workspacesStore == nil {
		return platformruntime.Registration{}, fmt.Errorf("compose driver run outcome runtime: required dependency is unavailable")
	}
	journal, ok := triggerEvents.(driver.RunOutcomeJournal)
	if !ok {
		return platformruntime.Registration{}, fmt.Errorf("compose driver run outcome runtime: TriggerEvent store lacks base event journal capability")
	}
	notifier, err := driver.NewRunOutcomeAwaitNotifierWithResolver(awaits, resolver)
	if err != nil {
		return platformruntime.Registration{}, fmt.Errorf("compose driver run outcome runtime: %w", err)
	}
	workspaces := driver.RunOutcomeWorkspaceLister(newAutomationWorkspaceLister(workspacesStore))
	reconciler, err := driver.NewRunOutcomeReconcilerWithExecution(
		queue, terminalWorkQueue, notifier, journal, publisher, workspace, workspaces, api, authorities,
		string(execution.DriverRunOutcomeComponentID),
	)
	if err != nil {
		return platformruntime.Registration{}, fmt.Errorf("compose driver run outcome runtime: %w", err)
	}
	return platformruntime.Registration{
		Component: &runOutcomeRuntimeComponent{reconciler: reconciler},
		Policy: platformruntime.Policy{
			Cadence: runOutcomeReconcileCadence, Immediate: true, Timeout: runOutcomeReconcileTimeout,
			FailureBackoff: platformruntime.Backoff{Initial: time.Second, Max: time.Minute, Multiplier: 2},
		},
	}, nil
}

const (
	AwaitEventNotificationComponentID platformruntime.ComponentID = "serve-await-event-notifications"
	awaitEventReconcileCadence                                    = 5 * time.Second
	awaitEventReconcileTimeout                                    = 30 * time.Second
)

type awaitEventRuntimeComponent struct {
	reconciler interface {
		RunOnce(context.Context, time.Time) error
	}
}

var _ platformruntime.Component = (*awaitEventRuntimeComponent)(nil)

func (*awaitEventRuntimeComponent) ID() platformruntime.ComponentID {
	return AwaitEventNotificationComponentID
}

func (component *awaitEventRuntimeComponent) RunOnce(ctx context.Context, now time.Time) error {
	if component == nil || component.reconciler == nil {
		return fmt.Errorf("await event reconciler is unavailable")
	}
	return component.reconciler.RunOnce(ctx, now)
}

func NewAwaitEventRuntimeRegistrationWithExecution(
	awaits store.AwaitStore,
	driverRuns store.DriverRunStore,
	workspacesStore store.WorkspaceStore,
	workspace string,
	api execution.DriverRunAPI,
	queue execution.AwaitEventNotificationAPI,
	authorities execution.SystemAuthorityResolver,
) (platformruntime.Registration, error) {
	if api == nil || queue == nil || authorities == nil {
		return platformruntime.Registration{}, fmt.Errorf("compose await event runtime: Execution await and queue APIs are unavailable")
	}
	resolver := &driver.ExecutionAwaitResolver{
		API: api, Authorities: authorities, ComponentID: string(AwaitEventNotificationComponentID),
	}
	if awaits == nil || driverRuns == nil || workspacesStore == nil {
		return platformruntime.Registration{}, fmt.Errorf("compose await event runtime: required dependency is unavailable")
	}
	reconciler, err := NewAwaitEventReconcilerWithExecutionStores(
		queue, authorities, awaits, driverRuns, resolver,
		workspace,
		NewAutomationWorkspaceLister(workspacesStore),
		string(AwaitEventNotificationComponentID),
	)
	if err != nil {
		return platformruntime.Registration{}, fmt.Errorf("compose await event runtime: %w", err)
	}
	return platformruntime.Registration{
		Component: &awaitEventRuntimeComponent{reconciler: reconciler},
		Policy: platformruntime.Policy{
			Cadence: awaitEventReconcileCadence, Immediate: true, Timeout: awaitEventReconcileTimeout,
			FailureBackoff: platformruntime.Backoff{Initial: time.Second, Max: time.Minute, Multiplier: 2},
		},
	}, nil
}
