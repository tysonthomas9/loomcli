// Package serve is the composition root for capability modules hosted by
// loom serve. It constructs adapters and authority mechanisms but owns no
// Workflow Catalog policy or product state.
package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/driver"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	automationfleetdb "github.com/tysonthomas9/loomcli/internal/modules/automation/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	catalogfleetdb "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const externalOperatorAuthorityTTL = time.Minute

// WorkflowTarget is the server-adapter projection of one prepared legacy
// workflow. The composition root converts it to workflowbinding's
// consumer-owned port without importing the legacy workflow package.
type WorkflowTarget struct {
	DriverID        string
	DriverVersionID string
}

// WorkflowTargetPreparation is supplied by the CLI adapter that still owns
// legacy builtin materialization. Automation receives only the resulting
// workflowbinding port.
type WorkflowTargetPreparation func(context.Context, string, string) (WorkflowTarget, error)

// WorkflowTargetPreparationFactory receives the application workflow's
// unavailable sentinel so the outer legacy adapter can preserve error
// classification without importing workflowbinding.
type WorkflowTargetPreparationFactory func(error) WorkflowTargetPreparation

type configuredWorkflowTargetPreparer struct {
	prepare WorkflowTargetPreparation
}

var _ workflowbinding.WorkflowTargetPreparer = (*configuredWorkflowTargetPreparer)(nil)

func (preparer *configuredWorkflowTargetPreparer) PrepareWorkflowTarget(
	ctx context.Context,
	workspace, workflow string,
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

// OperatorAuthorityResolver is the sole request-authority port required by
// Workflow Catalog composition.
type OperatorAuthorityResolver interface {
	ResolveOperatorAuthority(*http.Request, string, authority.Action) (authority.OperatorAuthority, error)
}

// ExternalOperatorResolverFactory keeps identity-middleware adaptation at the
// outer server boundary while the application root retains ownership of its
// authority issuer. unauthenticated is the Workflow Catalog transport's
// sentinel and must be returned for an absent verified identity.
type ExternalOperatorResolverFactory func(*authority.Issuer, error) OperatorAuthorityResolver

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
	issuer           *authority.Issuer
	operatorResolver httpapi.OperatorAuthorityResolver
	automation       *AutomationCapability
	// automationAwaitResolver is bound only after the shared Execution
	// capability has been composed. Automation is assembled with Workflow
	// Catalog earlier in startup, so this private, fail-closed indirection keeps
	// its synchronous await fast path on the typed Execution command without
	// exposing an issuer, Store, or mutable resolver to either capability.
	automationAwaitResolver *executionAwaitResolverBinding
}

// executionAwaitResolverBinding bridges the intentional startup ordering
// between Workflow Catalog/Automation and Execution. Before Bind it fails
// closed; production serve binds it before publishing the composed modules.
// The mutex also makes replacement safe in composition tests that build more
// than one Execution view from the same catalog capability.
type executionAwaitResolverBinding struct {
	mu       sync.RWMutex
	resolver store.AtomicAwaitStore
}

func (binding *executionAwaitResolverBinding) Bind(resolver store.AtomicAwaitStore) {
	if binding == nil {
		return
	}
	binding.mu.Lock()
	binding.resolver = resolver
	binding.mu.Unlock()
}

func (binding *executionAwaitResolverBinding) ResolveAwaitAndResume(
	ctx context.Context,
	workspace, instanceKey, eventID string,
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

func (c *WorkflowCatalogCapability) Register(mux *http.ServeMux) {
	if c != nil && c.routes != nil {
		c.routes.Register(mux)
	}
}

func (c *WorkflowCatalogCapability) EffectiveVersionResolver() workflowcatalog.EffectiveVersionResolver {
	if c == nil {
		return nil
	}
	return c.catalog
}

func (c *WorkflowCatalogCapability) RequestedVersionResolver() workflowcatalog.RequestedVersionResolver {
	if c == nil {
		return nil
	}
	return c.catalog
}

func (c *WorkflowCatalogCapability) CatalogAPI() workflowcatalog.API {
	if c == nil {
		return nil
	}
	return c.catalog
}

// AutomationCapability returns the fully composed Phase 3 capability handle.
// It is nil when Automation is disabled; callers receive no lower-level
// adapter, issuer, or Store through this accessor.
func (c *WorkflowCatalogCapability) AutomationCapability() *AutomationCapability {
	if c == nil {
		return nil
	}
	return c.automation
}

// NewExecutionCapability composes Execution against the same issuer and
// operator resolver as the local/external browser control plane. The
// capability receives neither the Catalog API nor its persistence adapter;
// only the authority seal and exact-purpose resolver are shared.
func (c *WorkflowCatalogCapability) NewExecutionCapability(dependencies ExecutionDependencies) (*ExecutionCapability, error) {
	if c == nil || c.issuer == nil {
		return nil, fmt.Errorf("compose Execution authority: Workflow Catalog authority is unavailable")
	}
	capability, err := newExecutionCapability(dependencies, c.issuer, c.operatorResolver)
	if err != nil {
		return nil, err
	}
	if c.automationAwaitResolver != nil {
		c.automationAwaitResolver.Bind(&driver.ExecutionAwaitResolver{
			API: capability.DriverRunAPI(), Authorities: capability.SystemAuthorityResolver(),
			ComponentID: string(AwaitEventNotificationComponentID),
		})
	}
	return capability, nil
}

// issueEffectiveVersionAuthority issues only the system action needed by a
// registered Automation runtime component. It stays private to composition so
// callers cannot turn the capability handle into a system-authority factory.
func (c *WorkflowCatalogCapability) issueEffectiveVersionAuthority(workspace string, component workflowCatalogRuntimeComponent, reason string) (authority.SystemAuthority, error) {
	if c == nil || c.issuer == nil {
		return authority.SystemAuthority{}, authority.ErrInvalidIssuer
	}
	if err := validateWorkflowCatalogRuntimeComponent(component); err != nil {
		return authority.SystemAuthority{}, err
	}
	principal, err := c.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: string(component), Class: authority.ClassSystem,
		Workspace: strings.TrimSpace(workspace),
		Actions:   []authority.Action{workflowcatalog.ActionResolveEffectiveVersion},
		ExpiresAt: time.Now().Add(externalOperatorAuthorityTTL),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return c.issuer.IssueSystem(principal, workspace, workflowcatalog.ActionResolveEffectiveVersion, reason)
}

// issueAutomationEffectiveVersionAuthority is the composition-only seam used
// by Automation's narrow EffectiveVersionAuthorityProvider. Automation is a
// request-driven capability consumer as well as a runtime contributor, so it
// must not masquerade as one particular scheduler component. Keeping this
// method private prevents callers from obtaining a general Catalog authority
// factory while still giving every Automation ingress lane the same exact
// resolve-effective-version permission.
func (c *WorkflowCatalogCapability) issueAutomationEffectiveVersionAuthority(workspace, reason string) (authority.SystemAuthority, error) {
	if c == nil || c.issuer == nil {
		return authority.SystemAuthority{}, authority.ErrInvalidIssuer
	}
	principal, err := c.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "automation", Class: authority.ClassSystem,
		Workspace: strings.TrimSpace(workspace),
		Actions:   []authority.Action{workflowcatalog.ActionResolveEffectiveVersion},
		ExpiresAt: time.Now().Add(externalOperatorAuthorityTTL),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return c.issuer.IssueSystem(principal, workspace, workflowcatalog.ActionResolveEffectiveVersion, reason)
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
	AutomationWebhookVerifier webhookingestion.Verifier
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
	adapter, err := catalogfleetdb.New(newWorkflowCatalogFleetDBTransport(config.FleetDBClient))
	if err != nil {
		return nil, fmt.Errorf("compose workflow catalog adapter: %w", err)
	}

	issuer := authority.NewIssuer()
	admission, resolver, err := composeWorkflowCatalogAuthority(config, issuer)
	if err != nil {
		return nil, err
	}

	catalog := workflowcatalog.New(adapter, adapter, admission)
	catalogHTTP := httpapi.New(catalog, resolver, config.WorkspaceFromContext, config.BuiltinWorkflow)
	capability := &WorkflowCatalogCapability{
		routes: catalogHTTP, catalog: catalog, issuer: issuer,
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
	if config.AutomationDriverRuns == nil || config.AutomationAwaits == nil || config.AutomationWorkspaces == nil || config.AutomationWebhookVerifier == nil || config.PrepareWorkflowTarget == nil {
		return fmt.Errorf("compose automation compatibility adapters: required narrow stores are unavailable")
	}
	prepareWorkflowTarget := config.PrepareWorkflowTarget(workflowbinding.ErrUnavailable)
	if prepareWorkflowTarget == nil {
		return fmt.Errorf("compose automation compatibility adapters: workflow target preparation is unavailable")
	}
	automationAdapter, err := automationfleetdb.New(newAutomationFleetDBTransport(config.FleetDBClient))
	if err != nil {
		return fmt.Errorf("compose automation FleetDB adapter: %w", err)
	}
	workspaceLister := newAutomationWorkspaceLister(config.AutomationWorkspaces)
	awaitResolver := &executionAwaitResolverBinding{}
	awaitNotifier, err := driver.NewAutomationAwaitEventNotifierWithResolver(
		config.AutomationAwaits, config.AutomationDriverRuns, awaitResolver,
	)
	if err != nil {
		return err
	}
	automationCapability, err := composeAutomationCapability(automationCapabilityConfig{
		enabled: true, workspaceKey: strings.TrimSpace(config.Workspace), catalog: capability,
	}, automationCapabilityDependencies{
		bindings: automationAdapter, unmanagedBindings: automationAdapter, managedBindings: automationAdapter,
		matcher: automationAdapter, events: automationAdapter,
		deliveries: automationAdapter, admissions: automationAdapter,
		execution: newAutomationExecutionPort(config.AutomationDriverRuns, newAutomationFleetExecutionDispatch(config.FleetDBClient)),
		cron:      automationAdapter, retries: automationAdapter, awaits: awaitNotifier,
		workspaces: workspaceLister, webhookVerifier: config.AutomationWebhookVerifier,
		workflowTargets: &configuredWorkflowTargetPreparer{prepare: prepareWorkflowTarget},
	})
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
		authority.OperatorOnly(workflowcatalog.ActionResolveRequestedVersion),
		authority.OperatorOnly(workflowcatalog.ActionApproveVersion),
		authority.OperatorOnly(workflowcatalog.ActionUnapproveVersion),
		authority.OperatorOnly(workflowcatalog.ActionActivateVersion),
	}
}

func workflowCatalogOperatorActions() []authority.Action {
	return []authority.Action{
		workflowcatalog.ActionResolveRequestedVersion,
		workflowcatalog.ActionApproveVersion,
		workflowcatalog.ActionUnapproveVersion,
		workflowcatalog.ActionActivateVersion,
	}
}

const localOpenOperatorSubject = "local-open-operator"

type localOpenOperatorResolver struct {
	issuer  *authority.Issuer
	actions map[authority.Action]struct{}
}

func newLocalOpenOperatorResolver(issuer *authority.Issuer, actions ...authority.Action) (*localOpenOperatorResolver, error) {
	if issuer == nil {
		return nil, authority.ErrInvalidIssuer
	}
	allowed := make(map[authority.Action]struct{}, len(actions))
	for _, action := range actions {
		if strings.TrimSpace(string(action)) == "" {
			return nil, authority.ErrActionNotAllowed
		}
		allowed[action] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, authority.ErrActionNotAllowed
	}
	return &localOpenOperatorResolver{issuer: issuer, actions: allowed}, nil
}

// ResolveOperatorAuthority treats reachability of the intentionally exposed
// open-mode endpoint as the deployment trust boundary. It still derives a
// sealed, short-lived authority for one canonical workspace and one
// route-selected action; request bodies and headers cannot select or widen
// either scope.
func (r *localOpenOperatorResolver) ResolveOperatorAuthority(request *http.Request, workspace string, action authority.Action) (authority.OperatorAuthority, error) {
	if request == nil {
		return authority.OperatorAuthority{}, httpapi.ErrUnauthenticated
	}
	if r == nil || r.issuer == nil {
		return authority.OperatorAuthority{}, authority.ErrInvalidIssuer
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return authority.OperatorAuthority{}, authority.ErrInvalidScope
	}
	if _, ok := r.actions[action]; !ok {
		return authority.OperatorAuthority{}, authority.ErrActionNotAllowed
	}
	principal, err := r.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: localOpenOperatorSubject, Class: authority.ClassOperator,
		Workspace: workspace, Actions: []authority.Action{action},
		ExpiresAt: time.Now().Add(externalOperatorAuthorityTTL),
	})
	if err != nil {
		return authority.OperatorAuthority{}, err
	}
	return r.issuer.IssueOperator(principal, workspace, action)
}
