package automationcomposition

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/driver"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	automationfleetdb "github.com/tysonthomas9/loomcli/internal/modules/automation/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type FleetDBClient = infrafleetdb.Client
type DriverRunStore = store.DriverRunStore
type AwaitStore = store.AwaitStore
type WorkspaceStore = store.WorkspaceStore
type WebhookVerifier = webhookingestion.Verifier

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

type WorkflowCatalogConfig struct {
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

func ComposeWorkflowCatalogAutomation(
	config WorkflowCatalogConfig,
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
	awaitNotifier, err := driver.NewAutomationAwaitEventNotifierWithResolver(
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
	config WorkflowCatalogConfig,
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
