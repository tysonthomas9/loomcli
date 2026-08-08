// Package serve is the composition root for capability modules hosted by
// loom serve. It constructs adapters and authority mechanisms but owns no
// Workflow Catalog policy or product state.
package serve

import (
	"context"
	"errors"
	"fmt"
	"github.com/tysonthomas9/loomcli/internal/driver"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	catalogfleetdb "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"net/http"
	"strings"
	"time"
)

const externalOperatorAuthorityTTL = time.Minute

// RouteModule is the only value the web server needs from capability
// composition. Keeping this interface here prevents webui from learning about
// Workflow Catalog persistence, authority, or its low-level FleetDB client.
type RouteModule interface {
	Register(*http.ServeMux)
}

// WorkflowCatalogCapability is the composition-owned handle for the active
// capability. Web composition sees only Register, while future Automation
// composition can receive the narrow active-version resolver and a same-seal,
// exact-purpose SystemAuthority without receiving the full Issuer.
type WorkflowCatalogCapability struct {
	routes           RouteModule
	catalog          workflowcatalog.API
	authoring        workflowcatalog.VersionAuthoringAPI
	issuer           *authority.Issuer
	operatorResolver httpapi.OperatorAuthorityResolver
	automation       *AutomationCapability
	// automationAwaitResolver is bound only after the shared Execution
	// capability has been composed. Automation is assembled with Workflow
	// Catalog earlier in startup, so this private, fail-closed indirection keeps
	// its synchronous await fast path on the typed Execution command without
	// exposing an issuer, Store, or mutable resolver to either capability.
	automationAwaitResolver *ExecutionAwaitResolverBinding
}

func (catalog *WorkflowCatalogCapability) Register(mux *http.ServeMux) {
	if catalog != nil && catalog.routes != nil {
		catalog.routes.Register(mux)
	}
}

func (catalog *WorkflowCatalogCapability) EffectiveVersionResolver() workflowcatalog.EffectiveVersionResolver {
	if catalog == nil {
		return nil
	}
	return catalog.catalog
}

func (catalog *WorkflowCatalogCapability) RequestedVersionResolver() workflowcatalog.RequestedVersionResolver {
	if catalog == nil {
		return nil
	}
	return catalog.catalog
}

func (catalog *WorkflowCatalogCapability) CatalogAPI() workflowcatalog.API {
	if catalog == nil {
		return nil
	}
	return catalog.catalog
}

// VersionAuthoringAPI exposes only Workflow Catalog's authority-gated atomic
// authoring surface to outer composition. It never exposes the FleetDB
// transport or generic Driver/DriverVersion stores.
func (catalog *WorkflowCatalogCapability) VersionAuthoringAPI() workflowcatalog.VersionAuthoringAPI {
	if catalog == nil {
		return nil
	}
	return catalog.authoring
}

// OperatorAuthorityResolver exposes only the request-scoped operator resolver
// that was composed with Workflow Catalog's issuer. Inbound authoring adapters
// receive no issuer and cannot mint or widen authority themselves.
func (catalog *WorkflowCatalogCapability) OperatorAuthorityResolver() httpapi.OperatorAuthorityResolver {
	if catalog == nil {
		return nil
	}
	return catalog.operatorResolver
}

// AutomationCapability returns the fully composed Phase 3 capability handle.
// It is nil when Automation is disabled; callers receive no lower-level
// adapter, issuer, or Store through this accessor.
func (catalog *WorkflowCatalogCapability) AutomationCapability() *AutomationCapability {
	if catalog == nil {
		return nil
	}
	return catalog.automation
}

// NewExecutionCapability composes Execution against the same issuer and
// operator resolver as the local/external browser control plane. The
// capability receives neither the Catalog API nor its persistence adapter;
// only the authority seal and exact-purpose resolver are shared.
func (catalog *WorkflowCatalogCapability) NewExecutionCapability(dependencies ExecutionDependencies) (*ExecutionCapability, error) {
	if catalog == nil || catalog.issuer == nil {
		return nil, fmt.Errorf("compose Execution authority: Workflow Catalog authority is unavailable")
	}
	capability, err := newExecutionCapability(dependencies, catalog.issuer, catalog.operatorResolver)
	if err != nil {
		return nil, err
	}
	if catalog.automationAwaitResolver != nil {
		catalog.automationAwaitResolver.Bind(&driver.ExecutionAwaitResolver{
			API: capability.DriverRunAPI(), Authorities: capability.SystemAuthorityResolver(),
			ComponentID: string(AwaitEventNotificationComponentID),
		})
	}
	return capability, nil
}

// issueEffectiveVersionAuthority issues only the system action needed by a
// registered Automation runtime component. It stays private to composition so
// callers cannot turn the capability handle into a system-authority factory.
func (catalog *WorkflowCatalogCapability) issueEffectiveVersionAuthority(workspace string, component workflowCatalogRuntimeComponent, reason string) (authority.SystemAuthority, error) {
	if catalog == nil || catalog.issuer == nil {
		return authority.SystemAuthority{}, authority.ErrInvalidIssuer
	}
	if err := validateWorkflowCatalogRuntimeComponent(component); err != nil {
		return authority.SystemAuthority{}, err
	}
	principal, err := catalog.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: string(component), Class: authority.ClassSystem,
		Workspace: strings.TrimSpace(workspace),
		Actions:   []authority.Action{workflowcatalog.ActionResolveEffectiveVersion},
		ExpiresAt: time.Now().Add(externalOperatorAuthorityTTL),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return catalog.issuer.IssueSystem(principal, workspace, workflowcatalog.ActionResolveEffectiveVersion, reason)
}

// ManagedBuiltinAuthoringAuthority issues only the exact system authority used
// by Loom's registered builtin-distribution component. The component and
// action are fixed here; outer distribution code supplies only workspace and
// an audited reason.
func (catalog *WorkflowCatalogCapability) ManagedBuiltinAuthoringAuthority(
	workspace, reason string,
) (authority.SystemAuthority, error) {
	if catalog == nil || catalog.issuer == nil {
		return authority.SystemAuthority{}, authority.ErrInvalidIssuer
	}
	component := workflowCatalogBuiltinDistributionComponent
	if err := validateWorkflowCatalogManagedAuthoringComponent(component); err != nil {
		return authority.SystemAuthority{}, err
	}
	workspace = strings.TrimSpace(workspace)
	reason = strings.TrimSpace(reason)
	if workspace == "" || reason == "" {
		return authority.SystemAuthority{}, authority.ErrInvalidScope
	}
	principal, err := catalog.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: string(component), Class: authority.ClassSystem,
		Workspace: workspace,
		Actions:   []authority.Action{workflowcatalog.ActionAuthorManagedVersion},
		ExpiresAt: time.Now().Add(externalOperatorAuthorityTTL),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return catalog.issuer.IssueSystem(
		principal,
		workspace,
		workflowcatalog.ActionAuthorManagedVersion,
		reason,
	)
}

// issueAutomationEffectiveVersionAuthority is the composition-only seam used
// by Automation's narrow EffectiveVersionAuthorityProvider. Automation is a
// request-driven capability consumer as well as a runtime contributor, so it
// must not masquerade as one particular scheduler component. Keeping this
// method private prevents callers from obtaining a general Catalog authority
// factory while still giving every Automation ingress lane the same exact
// resolve-effective-version permission.
func (catalog *WorkflowCatalogCapability) issueAutomationEffectiveVersionAuthority(workspace, reason string) (authority.SystemAuthority, error) {
	if catalog == nil || catalog.issuer == nil {
		return authority.SystemAuthority{}, authority.ErrInvalidIssuer
	}
	principal, err := catalog.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "automation", Class: authority.ClassSystem,
		Workspace: strings.TrimSpace(workspace),
		Actions:   []authority.Action{workflowcatalog.ActionResolveEffectiveVersion},
		ExpiresAt: time.Now().Add(externalOperatorAuthorityTTL),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return catalog.issuer.IssueSystem(principal, workspace, workflowcatalog.ActionResolveEffectiveVersion, reason)
}

// WorkflowCatalogConfig contains only server-derived composition inputs.
type WorkflowCatalogConfig struct {
	Enabled       bool
	FleetDBClient *infrafleetdb.Client
	// Workspace is Automation's optional fixed runtime scope. Empty means the
	// runtime lists current workspaces on every pass. Request authority remains
	// derived from the canonical per-request workspace in either mode.
	Workspace                       string
	ExternalAuth                    bool
	WorkspaceFromContext            func(context.Context) string
	BuiltinWorkflow                 func(string) bool
	ExternalOperatorResolverFactory ExternalOperatorResolverFactory
	// AutomationEnabled extends the local open-mode resolver with Automation's
	// exact operator-only actions. Callers cannot supply arbitrary actions.
	AutomationEnabled bool
	// These narrow stores are used only by composition-time compatibility
	// adapters whose owner capabilities land in later phases. Automation core
	// never receives the composite Store or any of these repository types.
	AutomationDriverRuns      store.DriverRunStore
	AutomationAwaits          store.AwaitStore
	AutomationWorkspaces      store.WorkspaceStore
	AutomationWebhookVerifier WebhookVerifier
	// PrepareWorkflowTarget is held only by the temporary composition adapter
	// around legacy builtin materialization. It never enters Automation or an
	// HTTP handler and returns only the prepared target identity.
	PrepareWorkflowTarget WorkflowTargetPreparationFactory
}

// NewWorkflowCatalogModule composes one Workflow Catalog core over the shared
// FleetDB client. Disabled slices construct nothing and expose no routes.
func NewWorkflowCatalogModule(config WorkflowCatalogConfig) (*WorkflowCatalogCapability, error) {
	if !config.Enabled {
		if config.AutomationEnabled {
			return nil, fmt.Errorf("compose automation: Workflow Catalog is disabled")
		}
		return nil, nil
	}
	if err := validateWorkflowCatalogHTTPConfig(config); err != nil {
		return nil, err
	}
	transport := newWorkflowCatalogFleetDBTransport(config.FleetDBClient)
	if transport == nil {
		return nil, fmt.Errorf("compose workflow catalog adapter: shared FleetDB client is required: %w", workflowcatalog.ErrUnavailable)
	}
	adapter, err := catalogfleetdb.NewWithAuthoring(transport, transport)
	if err != nil {
		return nil, fmt.Errorf("compose workflow catalog adapter: %w", err)
	}

	issuer := authority.NewIssuer()
	admission, resolver, err := composeWorkflowCatalogAuthority(config, issuer)
	if err != nil {
		return nil, err
	}

	catalog := workflowcatalog.NewWithAuthoring(adapter, adapter, adapter, admission)
	catalogHTTP := httpapi.New(catalog, resolver, config.WorkspaceFromContext, config.BuiltinWorkflow)
	capability := &WorkflowCatalogCapability{
		routes: catalogHTTP, catalog: catalog, authoring: catalog, issuer: issuer,
		operatorResolver: resolver,
	}
	if !config.AutomationEnabled {
		return capability, nil
	}
	if err := composeWorkflowCatalogAutomation(config, capability); err != nil {
		return nil, err
	}
	return capability, nil
}

func validateWorkflowCatalogHTTPConfig(config WorkflowCatalogConfig) error {
	if config.WorkspaceFromContext == nil || config.BuiltinWorkflow == nil {
		return fmt.Errorf("compose workflow catalog HTTP: workspace and builtin resolvers are required")
	}
	if config.ExternalAuth && config.ExternalOperatorResolverFactory == nil {
		return fmt.Errorf("compose workflow catalog external authorization: operator resolver factory is required")
	}
	return nil
}

func composeWorkflowCatalogAutomation(config WorkflowCatalogConfig, capability *WorkflowCatalogCapability) error {
	automationCapability, awaitResolver, err := ComposeWorkflowCatalogAutomation(
		automationWorkflowCatalogConfig{
			Workspace: config.Workspace, FleetDBClient: config.FleetDBClient,
			DriverRuns: config.AutomationDriverRuns, Awaits: config.AutomationAwaits,
			Workspaces:            config.AutomationWorkspaces,
			WebhookVerifier:       config.AutomationWebhookVerifier,
			PrepareWorkflowTarget: config.PrepareWorkflowTarget,
			Catalog: CatalogOwner{
				Issuer: capability.issuer, EffectiveVersions: capability.EffectiveVersionResolver(),
				EffectiveVersionAuthority: func(
					_ context.Context,
					workspace,
					reason string,
				) (authority.SystemAuthority, error) {
					return capability.issueAutomationEffectiveVersionAuthority(workspace, reason)
				},
				OperatorResolver: capability.operatorResolver,
			},
		},
	)
	if err != nil {
		return err
	}
	capability.automation = automationCapability
	capability.automationAwaitResolver = awaitResolver
	return nil
}

func composeWorkflowCatalogAuthority(config WorkflowCatalogConfig, issuer *authority.Issuer) (*authority.Admission, httpapi.OperatorAuthorityResolver, error) {
	if config.ExternalAuth {
		admission, err := workflowCatalogAdmission(issuer)
		if err != nil {
			return nil, nil, err
		}
		resolver := config.ExternalOperatorResolverFactory(issuer, httpapi.ErrUnauthenticated)
		if resolver == nil {
			return nil, nil, fmt.Errorf("compose workflow catalog external authorization: operator resolver is unavailable")
		}
		return admission, resolver, nil
	}
	admission, err := workflowCatalogAdmission(issuer)
	if err != nil {
		return nil, nil, err
	}
	operatorActions := workflowCatalogOperatorActions()
	operatorActions = append(operatorActions,
		execution.ActionSubmitDriverRun,
		execution.ActionCreateWorkerProfile,
		execution.ActionUpdateWorkerProfile,
		execution.ActionDeleteWorkerProfile,
	)
	if config.AutomationEnabled {
		operatorActions = append(operatorActions, automationOperatorActions()...)
	}
	resolver, err := newLocalOpenOperatorResolver(issuer, operatorActions...)
	if err != nil {
		return nil, nil, fmt.Errorf("compose workflow catalog local open authority: %w", err)
	}
	return admission, resolver, nil
}

func workflowCatalogAdmission(issuer *authority.Issuer) (*authority.Admission, error) {
	admission, err := issuer.NewAdmission(workflowCatalogOperationRules()...)
	if err != nil {
		return nil, fmt.Errorf("compose workflow catalog admission: %w", err)
	}
	return admission, nil
}

func workflowCatalogOperationRules() []authority.OperationRule {
	return []authority.OperationRule{
		authority.Allow(workflowcatalog.ActionResolveEffectiveVersion, authority.ClassSystem),
		authority.Allow(workflowcatalog.ActionAuthorManagedVersion, authority.ClassSystem),
		authority.OperatorOnly(workflowcatalog.ActionResolveRequestedVersion),
		authority.OperatorOnly(workflowcatalog.ActionAuthorVersion),
		authority.OperatorOnly(workflowcatalog.ActionApproveVersion),
		authority.OperatorOnly(workflowcatalog.ActionUnapproveVersion),
		authority.OperatorOnly(workflowcatalog.ActionActivateVersion),
	}
}

func workflowCatalogOperatorActions() []authority.Action {
	return []authority.Action{
		workflowcatalog.ActionResolveRequestedVersion,
		workflowcatalog.ActionAuthorVersion,
		workflowcatalog.ActionApproveVersion,
		workflowcatalog.ActionUnapproveVersion,
		workflowcatalog.ActionActivateVersion,
	}
}

const localOpenOperatorSubject = LocalOpenOperatorSubject

func newLocalOpenOperatorResolver(
	issuer *authority.Issuer,
	actions ...authority.Action,
) (*LocalOpenOperatorResolver, error) {
	return NewLocalOpenOperatorResolver(issuer, actions...)
}

// NewSourceControlCapability composes Source Control with the catalog-owned
// authority seal without publishing that seal outside the serve root.
func (catalog *WorkflowCatalogCapability) NewSourceControlCapability(
	localSettingsDir string,
	repositories sourcecontrol.RepositoryResolver,
) (*SourceControlCapability, error) {
	var issuer = catalogIssuer(catalog)
	return NewSourceControlCapability(localSettingsDir, repositories, issuer)
}

// NewSourceControlCapabilityWithFleetDB composes the complete Source Control
// and Connectors boundary against the catalog-owned authority seal.
func (catalog *WorkflowCatalogCapability) NewSourceControlCapabilityWithFleetDB(
	localSettingsDir string,
	repositories sourcecontrol.RepositoryResolver,
	client *infrafleetdb.Client,
) (*SourceControlCapability, error) {
	return NewSourceControlCapabilityWithFleetDB(
		localSettingsDir,
		repositories,
		client,
		catalogIssuer(catalog),
	)
}

// NewAgentProvisioningCapability supplies only the exact owner issuers needed
// by the cross-aggregate provisioning process.
func (capability *AgentsCapability) NewAgentProvisioningCapability(
	catalog *WorkflowCatalogCapability,
	sourceControl *SourceControlCapability,
	client *infrafleetdb.Client,
	config AgentProvisioningConfig,
) (*AgentProvisioningCapability, error) {
	owners := AgentProvisioningOwners{}
	if catalog != nil && catalog.automation != nil &&
		catalog.automation.BindingOperations() != nil {
		owners.AutomationIssuer = catalog.issuer
	}
	if sourceControl != nil {
		owners.ConnectorsIssuer = sourceControl.issuer
	}
	return capability.newAgentProvisioningCapabilityWithOwners(client, config, owners)
}

// NewInteractionCapability shares the catalog-owned authority seal with
// Interaction without exposing the issuer itself.
func (catalog *WorkflowCatalogCapability) NewInteractionCapability(
	config InteractionConfig,
	dependencies InteractionDependencies,
) (*InteractionCapability, error) {
	return NewInteractionCapabilityWithIssuer(config, dependencies, catalogIssuer(catalog))
}

// NewInteractionCapabilityWithFleetDB composes the complete production
// Interaction boundary against the catalog-owned authority seal.
func (catalog *WorkflowCatalogCapability) NewInteractionCapabilityWithFleetDB(
	config InteractionConfig,
	client *infrafleetdb.Client,
) (*InteractionCapability, error) {
	return NewInteractionCapabilityWithFleetDBIssuer(config, client, catalogIssuer(catalog))
}

func (catalog *WorkflowCatalogCapability) NewInteractionSessionAuthorityResolver(
	client *infrafleetdb.Client,
) (InteractionSessionAuthorityResolver, error) {
	return NewInteractionSessionAuthorityResolver(client, catalogIssuer(catalog))
}

func catalogIssuer(catalog *WorkflowCatalogCapability) *authority.Issuer {
	if catalog == nil {
		return nil
	}
	return catalog.issuer
}

type workflowCatalogRuntimeComponent string

const (
	// Keep this ID synchronized with the canonical component registration in
	// internal/archtest/testdata/runtime-components.yaml.
	workflowCatalogCronSchedulerComponent       workflowCatalogRuntimeComponent = "serve-trigger-cron-scheduler"
	workflowCatalogBuiltinDistributionComponent workflowCatalogRuntimeComponent = "workflow-catalog-builtin-distribution"
)

var (
	errUnregisteredWorkflowCatalogRuntimeComponent = errors.New("workflow catalog: unregistered runtime component")

	workflowCatalogEffectiveVersionRuntimeComponents = map[workflowCatalogRuntimeComponent]struct{}{
		workflowCatalogCronSchedulerComponent: {},
	}
	workflowCatalogManagedAuthoringComponents = map[workflowCatalogRuntimeComponent]struct{}{
		workflowCatalogBuiltinDistributionComponent: {},
	}
)

func validateWorkflowCatalogRuntimeComponent(component workflowCatalogRuntimeComponent) error {
	if _, ok := workflowCatalogEffectiveVersionRuntimeComponents[component]; !ok {
		return fmt.Errorf("%w: %q", errUnregisteredWorkflowCatalogRuntimeComponent, component)
	}
	return nil
}

func validateWorkflowCatalogManagedAuthoringComponent(component workflowCatalogRuntimeComponent) error {
	if _, ok := workflowCatalogManagedAuthoringComponents[component]; !ok {
		return fmt.Errorf("%w: %q", errUnregisteredWorkflowCatalogRuntimeComponent, component)
	}
	return nil
}
