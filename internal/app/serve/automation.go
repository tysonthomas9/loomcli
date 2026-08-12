// Package serve composes Automation's capability core, named application
// workflows, typed authority providers, and runtime registrations. It exposes
// only narrow ports to HTTP composition; no adapter receives the core's issuer
// or the process-wide FleetDB client.
package serve

import (
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

// AutomationBindingOperations is the complete binding-management surface
// needed by the trigger-binding HTTP adapter. It deliberately omits event
// admission, runtime commands, and unrelated query families.
type AutomationBindingOperations = automation.BindingOperations

// AutomationAuditQueries is the read-only surface needed by webhook audit
// routes. Mutation transports never receive these interfaces as write stores.
type AutomationAuditQueries = automation.AuditQueries

// AutomationCapability is a composition-owned handle. Its fields stay
// private so each consumer can receive only the narrow accessor it needs.
type AutomationCapability struct {
	bindings          AutomationBindingOperations
	audit             AutomationAuditQueries
	webhookWorkflow   *webhookingestion.Workflow
	workflowBinding   *workflowbinding.Workflow
	workflowEventing  *workfloweventing.Workflow
	issueJournal      systemeventing.IssueJournalEmitter
	runOutcomes       driver.RunOutcomePublisher
	operatorResolver  workflowcataloghttp.OperatorAuthorityResolver
	runtimeComponents []platformruntime.Registration
}

func (capability *AutomationCapability) BindingOperations() AutomationBindingOperations {
	if capability == nil {
		return nil
	}
	return capability.bindings
}

func (capability *AutomationCapability) AuditQueries() AutomationAuditQueries {
	if capability == nil {
		return nil
	}
	return capability.audit
}

func (capability *AutomationCapability) WebhookWorkflow() *webhookingestion.Workflow {
	if capability == nil {
		return nil
	}
	return capability.webhookWorkflow
}

// WorkflowBinding returns the named application workflow used by trigger-
// binding creation. The HTTP adapter never receives its target-preparation
// dependency or the legacy Store hidden behind composition.
func (capability *AutomationCapability) WorkflowBinding() *workflowbinding.Workflow {
	if capability == nil {
		return nil
	}
	return capability.workflowBinding
}

func (capability *AutomationCapability) WorkflowEventing() *workfloweventing.Workflow {
	if capability == nil {
		return nil
	}
	return capability.workflowEventing
}

func (capability *AutomationCapability) IssueJournalEmitter() systemeventing.IssueJournalEmitter {
	if capability == nil {
		return nil
	}
	return capability.issueJournal
}

func (capability *AutomationCapability) RunOutcomePublisher() driver.RunOutcomePublisher {
	if capability == nil {
		return nil
	}
	return capability.runOutcomes
}

func (capability *AutomationCapability) OperatorAuthorityResolver() workflowcataloghttp.OperatorAuthorityResolver {
	if capability == nil {
		return nil
	}
	return capability.operatorResolver
}

func (capability *AutomationCapability) RuntimeRegistrations() []platformruntime.Registration {
	if capability == nil {
		return nil
	}
	return append([]platformruntime.Registration(nil), capability.runtimeComponents...)
}

type automationCapabilityConfig struct {
	enabled      bool
	workspaceKey string
	catalog      *WorkflowCatalogCapability
}

// automationCapabilityDependencies are adapter-owned ports assembled by the
// production composition wrapper. Keeping this type private prevents CLI or
// web packages from injecting a composite Store directly into the core.
type automationCapabilityDependencies struct {
	bindings          automation.BindingStore
	unmanagedBindings automation.UnmanagedBindingStore
	managedBindings   automation.ManagedBindingStore
	matcher           automation.BindingMatcher
	events            automation.EventReader
	deliveries        automation.DeliveryReader
	admissions        automation.AdmissionStore
	execution         automation.ExecutionPort
	cron              automation.CronSweepPort
	retries           automation.DeliveryRetryPort
	awaits            automation.AwaitEventNotifier
	workspaces        automation.WorkspaceLister
	webhookVerifier   webhookingestion.Verifier
	workflowTargets   workflowbinding.WorkflowTargetPreparer
}

//nolint:funlen // Keep capability construction and its named application-workflow bindings in one auditable composition sequence.
func composeAutomationCapability(config automationCapabilityConfig, dependencies automationCapabilityDependencies) (*AutomationCapability, error) {
	if !config.enabled {
		return nil, nil
	}
	if err := validateAutomationCapabilityConfig(config); err != nil {
		return nil, err
	}
	if err := validateAutomationCapabilityDependencies(config, dependencies); err != nil {
		return nil, err
	}

	authorityAdmission, err := config.catalog.issuer.NewAdmission(automationOperationRules()...)
	if err != nil {
		return nil, fmt.Errorf("compose automation admission authority: %w", err)
	}
	service := automation.New(
		dependencies.bindings, dependencies.unmanagedBindings, dependencies.managedBindings,
		dependencies.matcher, dependencies.events,
		dependencies.deliveries, dependencies.admissions, dependencies.execution,
		config.catalog.EffectiveVersionResolver(), newAutomationCatalogAuthorityProvider(config.catalog),
		authorityAdmission, automation.WithRuntimePorts(dependencies.cron, dependencies.retries),
		automation.WithAwaitEventNotifier(dependencies.awaits),
		automation.WithEventTrustPolicy(driver.AutomationEventTrustPolicy()),
	)
	webhookWorkflow, err := webhookingestion.New(
		dependencies.webhookVerifier, newAutomationWebhookAuthorityProvider(config.catalog.issuer), service,
	)
	if err != nil {
		return nil, fmt.Errorf("compose automation webhook workflow: %w", err)
	}
	workflowBinding, err := workflowbinding.New(dependencies.workflowTargets, service)
	if err != nil {
		return nil, fmt.Errorf("compose workflow binding workflow: %w", err)
	}
	workflowEvents, err := workfloweventing.New(newAutomationExecutionAuthorityProvider(config.catalog.issuer), service)
	if err != nil {
		return nil, fmt.Errorf("compose automation workflow eventing: %w", err)
	}
	systemEvents, err := systemeventing.New(newAutomationSystemAuthorityProvider(config.catalog.issuer), service)
	if err != nil {
		return nil, fmt.Errorf("compose automation system eventing: %w", err)
	}
	issueJournal, err := systemeventing.BindIssueJournalEmitter(systemEvents)
	if err != nil {
		return nil, fmt.Errorf("compose automation issue journal emitter: %w", err)
	}
	runOutcomeEvents, err := systemeventing.BindRunOutcomeEmitter(systemEvents)
	if err != nil {
		return nil, fmt.Errorf("compose automation run outcome emitter: %w", err)
	}
	runOutcomes, err := newAutomationDriverRunOutcomePublisher(runOutcomeEvents)
	if err != nil {
		return nil, fmt.Errorf("compose automation driver outcome publisher: %w", err)
	}
	registrations, err := automation.RuntimeRegistrations(
		service, newAutomationRuntimeAuthorityProvider(config.catalog.issuer),
		automation.RuntimeConfig{WorkspaceKey: config.workspaceKey, WorkspaceLister: dependencies.workspaces},
	)
	if err != nil {
		return nil, fmt.Errorf("compose automation runtime components: %w", err)
	}
	return &AutomationCapability{
		bindings: service, audit: service, webhookWorkflow: webhookWorkflow, workflowBinding: workflowBinding,
		workflowEventing: workflowEvents, issueJournal: issueJournal, runOutcomes: runOutcomes,
		operatorResolver:  config.catalog.operatorResolver,
		runtimeComponents: registrations,
	}, nil
}

func validateAutomationCapabilityConfig(config automationCapabilityConfig) error {
	if config.catalog == nil || config.catalog.issuer == nil ||
		config.catalog.EffectiveVersionResolver() == nil || config.catalog.operatorResolver == nil {
		return fmt.Errorf("compose automation: active Workflow Catalog composition is required: %w", automation.ErrUnavailable)
	}
	return nil
}

func validateAutomationCapabilityDependencies(
	config automationCapabilityConfig,
	dependencies automationCapabilityDependencies,
) error {
	switch {
	case dependencies.bindings == nil:
		return fmt.Errorf("compose automation binding store: %w", automation.ErrUnavailable)
	case dependencies.unmanagedBindings == nil:
		return fmt.Errorf("compose automation unmanaged binding store: %w", automation.ErrUnavailable)
	case dependencies.managedBindings == nil:
		return fmt.Errorf("compose automation managed binding store: %w", automation.ErrUnavailable)
	case dependencies.matcher == nil:
		return fmt.Errorf("compose automation binding matcher: %w", automation.ErrUnavailable)
	case dependencies.events == nil:
		return fmt.Errorf("compose automation event reader: %w", automation.ErrUnavailable)
	case dependencies.deliveries == nil:
		return fmt.Errorf("compose automation delivery reader: %w", automation.ErrUnavailable)
	case dependencies.admissions == nil:
		return fmt.Errorf("compose automation admission store: %w", automation.ErrUnavailable)
	case dependencies.execution == nil:
		return fmt.Errorf("compose automation execution port: %w", automation.ErrUnavailable)
	case dependencies.cron == nil:
		return fmt.Errorf("compose automation cron port: %w", automation.ErrUnavailable)
	case dependencies.retries == nil:
		return fmt.Errorf("compose automation retry port: %w", automation.ErrUnavailable)
	case dependencies.awaits == nil:
		return fmt.Errorf("compose automation await notifier: %w", automation.ErrUnavailable)
	case dependencies.workspaces == nil && config.workspaceKey == "":
		return fmt.Errorf("compose automation workspace lister: %w", automation.ErrUnavailable)
	case dependencies.webhookVerifier == nil:
		return fmt.Errorf("compose automation webhook verifier: %w", automation.ErrUnavailable)
	case dependencies.workflowTargets == nil:
		return fmt.Errorf("compose workflow binding target preparer: %w", automation.ErrUnavailable)
	default:
		return nil
	}
}
