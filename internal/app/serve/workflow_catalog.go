// Package serve is the composition root for capability modules hosted by
// loom serve. It constructs adapters and authority mechanisms but owns no
// Workflow Catalog policy or product state.
package serve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/driver"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	automationfleetdb "github.com/tysonthomas9/loomcli/internal/modules/automation/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	catalogfleetdb "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	authorityhttp "github.com/tysonthomas9/loomcli/internal/platform/authority/httpapi"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

const externalOperatorAuthorityTTL = time.Minute

// legacyWorkflowTargetPreparer is the temporary composition adapter around
// the mixed legacy workflow registration package. It stays in this already-
// reviewed legacy importer so the migration does not widen that exception.
// The application workflow sees only WorkflowTargetPreparer; neither the HTTP
// adapter nor Automation inherits this Store dependency or builtin self-heal.
type legacyWorkflowTargetPreparer struct {
	catalog workflowdefs.DriverCatalog
}

var _ workflowbinding.WorkflowTargetPreparer = (*legacyWorkflowTargetPreparer)(nil)

func newLegacyWorkflowTargetPreparer(catalog workflowdefs.DriverCatalog) workflowbinding.WorkflowTargetPreparer {
	if catalog == nil {
		return nil
	}
	return &legacyWorkflowTargetPreparer{catalog: catalog}
}

func (preparer *legacyWorkflowTargetPreparer) PrepareWorkflowTarget(
	ctx context.Context,
	workspace, workflow string,
) (workflowbinding.WorkflowTarget, error) {
	driver, err := workflowdefs.EnsureAndResolveDriver(ctx, preparer.catalog, workspace, workflow)
	if err != nil {
		if errors.Is(err, workflowdefs.ErrBuildToolchainUnavailable) {
			return workflowbinding.WorkflowTarget{}, fmt.Errorf("%w: %w", workflowbinding.ErrUnavailable, err)
		}
		return workflowbinding.WorkflowTarget{}, err
	}
	if driver == nil {
		return workflowbinding.WorkflowTarget{}, nil
	}
	return workflowbinding.WorkflowTarget{
		DriverID: strings.TrimSpace(driver.DriverID), DriverVersionID: strings.TrimSpace(driver.ActiveVersionID),
	}, nil
}

// RouteModule is the only value the web server needs from capability
// composition. Keeping this interface here prevents webui from learning about
// Workflow Catalog persistence, authority, or its low-level FleetDB client.
type RouteModule interface {
	Register(*http.ServeMux)
}

type routeModules []RouteModule

func (modules routeModules) Register(mux *http.ServeMux) {
	for _, module := range modules {
		if module != nil {
			module.Register(mux)
		}
	}
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
	browserSession   *authority.LocalBrowserSessionBroker
	automation       *AutomationCapability
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

// AutomationCapability returns the fully composed Phase 3 capability handle.
// It is nil when Automation is disabled; callers receive no lower-level
// adapter, issuer, or Store through this accessor.
func (c *WorkflowCatalogCapability) AutomationCapability() *AutomationCapability {
	if c == nil {
		return nil
	}
	return c.automation
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
	RuntimeDir    string
	// Workspace is Automation's optional fixed runtime scope. Empty means the
	// runtime lists current workspaces on every pass. Request authority remains
	// derived from the canonical per-request workspace in either mode.
	Workspace             string
	ExternalAuth          bool
	WorkspaceRoleResolver middleware.WorkspaceRoleResolver
	// AutomationEnabled extends the one platform browser-session broker with
	// Automation's exact operator-only actions. Callers cannot supply arbitrary
	// actions or create a second broker/credential namespace.
	AutomationEnabled bool
	// These narrow stores are used only by composition-time compatibility
	// adapters whose owner capabilities land in later phases. Automation core
	// never receives the composite Store or any of these repository types.
	AutomationDriverRuns      store.DriverRunStore
	AutomationAwaits          store.AwaitStore
	AutomationWorkspaces      store.WorkspaceStore
	AutomationWebhookVerifier webhookingestion.Verifier
	// WorkflowTargetCatalog is held only by the temporary composition adapter
	// around workflows.EnsureAndResolveDriver. It never enters Automation or an
	// HTTP handler and exposes only Driver/DriverVersion repositories.
	WorkflowTargetCatalog workflowdefs.DriverCatalog
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
	if config.ExternalAuth && config.WorkspaceRoleResolver == nil {
		return nil, fmt.Errorf("compose workflow catalog external authorization: workspace role resolver is required")
	}
	adapter, err := catalogfleetdb.New(newWorkflowCatalogFleetDBTransport(config.FleetDBClient))
	if err != nil {
		return nil, fmt.Errorf("compose workflow catalog adapter: %w", err)
	}

	issuer := authority.NewIssuer()
	admission, resolver, browserSession, err := composeWorkflowCatalogAuthority(config, issuer)
	if err != nil {
		return nil, err
	}

	catalog := workflowcatalog.New(adapter, adapter, admission)
	catalogHTTP := httpapi.New(catalog, resolver, middleware.WorkspaceFromContext, workflowdefs.IsBuiltinWorkflow)
	var capability *WorkflowCatalogCapability
	if browserSession == nil {
		capability = &WorkflowCatalogCapability{
			routes: catalogHTTP, catalog: catalog, issuer: issuer,
			operatorResolver: resolver,
		}
	} else {
		routes := routeModules{
			catalogHTTP,
			authorityhttp.New(browserSession, middleware.WorkspaceFromContext),
		}
		capability = &WorkflowCatalogCapability{
			routes: routes, catalog: catalog, issuer: issuer,
			operatorResolver: resolver, browserSession: browserSession,
		}
	}
	if !config.AutomationEnabled {
		return capability, nil
	}
	if err := composeWorkflowCatalogAutomation(config, capability); err != nil {
		return nil, err
	}
	return capability, nil
}

func composeWorkflowCatalogAutomation(config WorkflowCatalogConfig, capability *WorkflowCatalogCapability) error {
	if config.AutomationDriverRuns == nil || config.AutomationAwaits == nil || config.AutomationWorkspaces == nil || config.AutomationWebhookVerifier == nil || config.WorkflowTargetCatalog == nil {
		return fmt.Errorf("compose automation compatibility adapters: required narrow stores are unavailable")
	}
	automationAdapter, err := automationfleetdb.New(newAutomationFleetDBTransport(config.FleetDBClient))
	if err != nil {
		return fmt.Errorf("compose automation FleetDB adapter: %w", err)
	}
	workspaceLister := newAutomationWorkspaceLister(config.AutomationWorkspaces)
	awaitNotifier, err := driver.NewAutomationAwaitEventNotifier(config.AutomationAwaits, config.AutomationDriverRuns)
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
		workflowTargets: newLegacyWorkflowTargetPreparer(config.WorkflowTargetCatalog),
	})
	if err != nil {
		return err
	}
	capability.automation = automationCapability
	return nil
}

func composeWorkflowCatalogAuthority(config WorkflowCatalogConfig, issuer *authority.Issuer) (*authority.Admission, httpapi.OperatorAuthorityResolver, *authority.LocalBrowserSessionBroker, error) {
	if config.ExternalAuth {
		admission, err := workflowCatalogAdmission(issuer)
		if err != nil {
			return nil, nil, nil, err
		}
		resolver := &externalOperatorResolver{issuer: issuer, resolveRole: config.WorkspaceRoleResolver}
		return admission, resolver, nil, nil
	}
	credentialDir := filepath.Join(config.RuntimeDir, ".loom", "operator")
	localIssuer, err := authority.LoadOrCreateLocalRuntimeOperatorCredentialWithIssuer(credentialDir, issuer)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("compose workflow catalog local operator credential: %w", err)
	}
	admission, err := localIssuer.NewAdmission(workflowCatalogOperationRules()...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("compose workflow catalog admission: %w", err)
	}
	browserActions := workflowCatalogOperatorActions()
	if config.AutomationEnabled {
		browserActions = append(browserActions, automationOperatorActions()...)
	}
	browserSession, err := authority.NewLocalBrowserSessionBroker(localIssuer, browserActions...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("compose workflow catalog browser authority: %w", err)
	}
	resolver := &localOperatorResolver{issuer: localIssuer, browserSession: browserSession}
	return admission, resolver, browserSession, nil
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
		workflowcatalog.ActionApproveVersion,
		workflowcatalog.ActionUnapproveVersion,
		workflowcatalog.ActionActivateVersion,
	}
}

type localOperatorResolver struct {
	issuer         *authority.LocalOperatorIssuer
	browserSession *authority.LocalBrowserSessionBroker
}

func (r *localOperatorResolver) ResolveOperatorAuthority(request *http.Request, workspace string, action authority.Action) (authority.OperatorAuthority, error) {
	if request == nil || strings.TrimSpace(request.Header.Get("Authorization")) == "" {
		return authority.OperatorAuthority{}, httpapi.ErrUnauthenticated
	}
	bearer := request.Header.Get("Authorization")
	durable, durableErr := authority.IssueOperator(r.issuer, bearer, workspace, action)
	if durableErr == nil {
		return durable, nil
	}
	if r.browserSession == nil {
		return authority.OperatorAuthority{}, durableErr
	}
	delegated, delegatedErr := r.browserSession.IssueOperator(bearer, workspace, action)
	if delegatedErr == nil {
		return delegated, nil
	}
	// Preserve the more precise scope denial for a valid durable credential;
	// otherwise report the delegated credential result without revealing which
	// credential class was presented.
	if errors.Is(durableErr, authority.ErrWorkspaceMismatch) {
		return authority.OperatorAuthority{}, durableErr
	}
	return authority.OperatorAuthority{}, delegatedErr
}

// externalOperatorResolver converts only an identity already verified by the
// global JWT middleware. A dedicated loom serve is an operator control plane;
// the server-derived workspace binding prevents a credential authenticated for
// that host from being widened by a request path or request body.
type externalOperatorResolver struct {
	issuer      *authority.Issuer
	resolveRole middleware.WorkspaceRoleResolver
}

func (r *externalOperatorResolver) ResolveOperatorAuthority(request *http.Request, workspace string, action authority.Action) (authority.OperatorAuthority, error) {
	if request == nil {
		return authority.OperatorAuthority{}, httpapi.ErrUnauthenticated
	}
	identity, ok := middleware.UserIdentityFromContext(request.Context())
	if !ok || strings.TrimSpace(identity.UserID) == "" {
		return authority.OperatorAuthority{}, httpapi.ErrUnauthenticated
	}
	workspace = strings.TrimSpace(workspace)
	if r == nil || r.issuer == nil || workspace == "" {
		return authority.OperatorAuthority{}, authority.ErrInvalidScope
	}
	if r.resolveRole == nil {
		return authority.OperatorAuthority{}, authority.ErrAdmissionDenied
	}
	role, err := r.resolveRole(request.Context(), workspace, identity)
	if err != nil {
		return authority.OperatorAuthority{}, fmt.Errorf("resolve Workflow Catalog operator role: %w", authority.ErrAdmissionDenied)
	}
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "admin", "owner", "maintainer":
	default:
		return authority.OperatorAuthority{}, authority.ErrAdmissionDenied
	}
	principal, err := r.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   identity.UserID,
		Class:     authority.ClassOperator,
		Workspace: workspace,
		Actions:   []authority.Action{action},
		ExpiresAt: time.Now().Add(externalOperatorAuthorityTTL),
	})
	if err != nil {
		return authority.OperatorAuthority{}, err
	}
	return r.issuer.IssueOperator(principal, workspace, action)
}
