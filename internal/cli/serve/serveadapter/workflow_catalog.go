package serveadapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	authorityhttp "github.com/tysonthomas9/loomcli/internal/platform/authority/httpapi"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/webhooks"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

// WorkflowCatalogEnabledEnv controls whether serve exposes the Workflow
// Catalog slice.
const WorkflowCatalogEnabledEnv = "LOOM_WORKFLOW_CATALOG_ENABLED"

// AutomationEnabledEnv controls whether serve composes the Phase 3
// Automation slice. Automation depends on the Workflow Catalog slice and can
// never be enabled while its activated-version resolver is disabled.
const AutomationEnabledEnv = "LOOM_AUTOMATION_ENABLED"

// WorkflowCatalogEnabled resolves the slice's startup policy. External-auth
// deployments default closed until a workspace role resolver is available.
func WorkflowCatalogEnabled(externalAuth, workspaceRoleResolverAvailable bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(WorkflowCatalogEnabledEnv))
	if raw == "" {
		if externalAuth && !workspaceRoleResolverAvailable {
			return false, nil
		}
		return true, nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", WorkflowCatalogEnabledEnv, err)
	}
	if enabled && externalAuth && !workspaceRoleResolverAvailable {
		return false, fmt.Errorf("%s=%q requires a workspace role resolver when external authentication is configured", WorkflowCatalogEnabledEnv, raw)
	}
	return enabled, nil
}

func AutomationEnabled(externalAuth, workspaceRoleResolverAvailable bool) (bool, error) {
	catalogEnabled, err := WorkflowCatalogEnabled(externalAuth, workspaceRoleResolverAvailable)
	if err != nil {
		return false, err
	}
	raw := strings.TrimSpace(os.Getenv(AutomationEnabledEnv))
	if raw == "" {
		return catalogEnabled, nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", AutomationEnabledEnv, err)
	}
	if enabled && !catalogEnabled {
		return false, fmt.Errorf("%s=%q requires the Workflow Catalog slice", AutomationEnabledEnv, raw)
	}
	return enabled, nil
}

// RequiredFleetDBCapabilities derives startup compatibility requirements from
// the slices enabled in serve configuration. Atomic await/run convergence is
// an execution-platform invariant and is therefore required by every valid
// serve profile. Phase 4 also requires normalized TaskRun fencing and the
// owner-fenced Artifacts lifecycle independently of optional Catalog and
// Automation slices.
func RequiredFleetDBCapabilities(externalAuth, workspaceRoleResolverAvailable bool) ([]string, error) {
	catalogEnabled, err := WorkflowCatalogEnabled(externalAuth, workspaceRoleResolverAvailable)
	if err != nil {
		return nil, err
	}
	automationEnabled, err := AutomationEnabled(externalAuth, workspaceRoleResolverAvailable)
	if err != nil {
		return nil, err
	}
	required := append([]string{fleetdb.ExecutionAwaitAtomicResumeCapability}, fleetdb.Phase4FoundationCapabilities()...)
	if !catalogEnabled {
		return required, nil
	}
	required = append(required, fleetdb.WorkflowCatalogVersionLifecycleCapability)
	if automationEnabled {
		required = append(required, fleetdb.AutomationTriggerAdmissionCapability)
	}
	return required, nil
}

// WorkflowCatalogConfig contains the CLI-derived inputs forwarded to the
// serve composition root. The adapter never constructs a FleetDB client.
type WorkflowCatalogConfig struct {
	Enabled               bool
	AutomationEnabled     bool
	StoreHandle           *bootstrap.StoreHandle
	RuntimeDir            string
	Workspace             string
	ExternalAuth          bool
	WorkspaceRoleResolver middleware.WorkspaceRoleResolver
}

// RefreshBoundPromptAgentWorkflows keeps the legacy built-in distribution
// dependency inside the reviewed Workflow Catalog compatibility adapter. It
// must run before Automation dispatchers start so an existing binding cannot
// admit work against a stale embedded prompt-agent version after an upgrade.
func RefreshBoundPromptAgentWorkflows(ctx context.Context, handle *bootstrap.StoreHandle) error {
	if handle == nil || handle.Store == nil {
		return fmt.Errorf("refresh bound prompt-agent workflows: FleetDB Store handle is required")
	}
	return workflowdefs.EnsureBoundPromptAgentWorkflows(ctx, handle.Store)
}

// externalOperatorResolver converts only an identity already verified by the
// global JWT middleware. The application composition root supplies the issuer
// and Workflow Catalog's unauthenticated sentinel; this outer adapter retains
// ownership of middleware-specific identity and role types.
type externalOperatorResolver struct {
	issuer          *authority.Issuer
	resolveRole     middleware.WorkspaceRoleResolver
	unauthenticated error
}

func newExternalOperatorResolverFactory(resolveRole middleware.WorkspaceRoleResolver) appserve.ExternalOperatorResolverFactory {
	if resolveRole == nil {
		return nil
	}
	return func(issuer *authority.Issuer, unauthenticated error) appserve.OperatorAuthorityResolver {
		return &externalOperatorResolver{issuer: issuer, resolveRole: resolveRole, unauthenticated: unauthenticated}
	}
}

func (resolver *externalOperatorResolver) ResolveOperatorAuthority(
	request *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, error) {
	if request == nil {
		return authority.OperatorAuthority{}, resolver.unauthenticated
	}
	identity, ok := middleware.UserIdentityFromContext(request.Context())
	if !ok || strings.TrimSpace(identity.UserID) == "" {
		return authority.OperatorAuthority{}, resolver.unauthenticated
	}
	workspace = strings.TrimSpace(workspace)
	if resolver == nil || resolver.issuer == nil || workspace == "" {
		return authority.OperatorAuthority{}, authority.ErrInvalidScope
	}
	if resolver.resolveRole == nil {
		return authority.OperatorAuthority{}, authority.ErrAdmissionDenied
	}
	role, err := resolver.resolveRole(request.Context(), workspace, identity)
	if err != nil {
		return authority.OperatorAuthority{}, fmt.Errorf("resolve Workflow Catalog operator role: %w", authority.ErrAdmissionDenied)
	}
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "admin", "owner", "maintainer":
	default:
		return authority.OperatorAuthority{}, authority.ErrAdmissionDenied
	}
	principal, err := resolver.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: identity.UserID, Class: authority.ClassOperator, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		return authority.OperatorAuthority{}, err
	}
	return resolver.issuer.IssueOperator(principal, workspace, action)
}

func newLegacyWorkflowTargetPreparation(catalog workflowdefs.DriverCatalog) appserve.WorkflowTargetPreparationFactory {
	if catalog == nil {
		return nil
	}
	return func(unavailable error) appserve.WorkflowTargetPreparation {
		return func(ctx context.Context, workspace, workflow string) (appserve.WorkflowTarget, error) {
			driver, err := workflowdefs.EnsureAndResolveDriver(ctx, catalog, workspace, workflow)
			if err != nil {
				if errors.Is(err, workflowdefs.ErrBuildToolchainUnavailable) {
					return appserve.WorkflowTarget{}, fmt.Errorf("%w: %w", unavailable, err)
				}
				return appserve.WorkflowTarget{}, err
			}
			if driver == nil {
				return appserve.WorkflowTarget{}, nil
			}
			return appserve.WorkflowTarget{
				DriverID: strings.TrimSpace(driver.DriverID), DriverVersionID: strings.TrimSpace(driver.ActiveVersionID),
			}, nil
		}
	}
}

func newBrowserSessionRoutes(
	broker *authority.LocalBrowserSessionBroker,
	workspaceFromContext func(context.Context) string,
) appserve.RouteModule {
	return authorityhttp.New(broker, workspaceFromContext)
}

// AutomationCapability is the CLI composition view of Automation. The web
// surface is embedded as an interface so callers cannot recover the concrete
// app-composition handle, while runtime-only ports remain available to serve.
type AutomationCapability struct {
	webui.AutomationCapability
	issueJournal systemeventing.IssueJournalEmitter
	runOutcomes  driver.RunOutcomePublisher
	runtime      AutomationRuntimeContributor
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

func (capability *AutomationCapability) RuntimeRegistrations() []platformruntime.Registration {
	if capability == nil || capability.runtime == nil {
		return nil
	}
	return capability.runtime.RuntimeRegistrations()
}

// WorkflowCatalogModule narrows the concrete application composition to the
// route registration and optional Automation view needed by the CLI.
type WorkflowCatalogModule struct {
	module     interface{ Register(*http.ServeMux) }
	capability *appserve.WorkflowCatalogCapability
	automation *AutomationCapability
}

func (module *WorkflowCatalogModule) Register(mux *http.ServeMux) {
	if module != nil && module.module != nil {
		module.module.Register(mux)
	}
}

func (module *WorkflowCatalogModule) AutomationCapability() *AutomationCapability {
	if module == nil {
		return nil
	}
	return module.automation
}

func (module *WorkflowCatalogModule) CatalogAPI() workflowcatalog.API {
	if module == nil || module.capability == nil {
		return nil
	}
	return module.capability.CatalogAPI()
}

func (module *WorkflowCatalogModule) NewExecutionCapability(
	dependencies appserve.ExecutionDependencies,
) (*appserve.ExecutionCapability, error) {
	if module == nil || module.capability == nil {
		return nil, fmt.Errorf("compose Execution: Workflow Catalog authority is required")
	}
	return module.capability.NewExecutionCapability(dependencies)
}

// BuildExecutionCapability composes the always-on Execution slice. When
// Workflow Catalog is active it shares that control plane's issuer and
// operator resolver so Desktop's action-limited browser bearer can submit a
// run. Catalog-disabled profiles still get runtime/TaskRun execution but no
// operator submit resolver.
func BuildExecutionCapability(module *WorkflowCatalogModule, handle *bootstrap.StoreHandle) (*appserve.ExecutionCapability, error) {
	if handle == nil || handle.Store == nil || handle.FleetDBClient() == nil {
		return nil, fmt.Errorf("compose Execution: FleetDB Store handle is required")
	}
	st := handle.Store
	executionTransport := handle.FleetDBClient().Execution()
	requestPort, claimPort, workItemDesignPort, requeuePort, retryExhaustionPort, err := appserve.NewFleetTaskRunCommandPorts(executionTransport)
	if err != nil {
		return nil, err
	}
	dependencies := appserve.ExecutionDependencies{
		TaskRuns: st.TaskRuns(), DriverRuns: st.DriverRuns(), DriverSteps: st.DriverSteps(),
		TerminalStepRepairs: executionTransport, TaskRunEvents: st.TaskRunEvents(), Nodes: st.Nodes(),
		WorkerProfiles: st.WorkerProfiles(), Agents: st.Agents(), Outbox: st.Outbox(), Awaits: st.Awaits(),
		TriggerEvents:         st.TriggerEvents(),
		Workspaces:            st.Workspaces(),
		AtomicTaskRunRequests: requestPort, AtomicTaskRunClaims: claimPort,
		AtomicTaskRunWorkItemDesign: workItemDesignPort,
		AtomicTaskRunRequeues:       requeuePort, AtomicTaskRunRetryExhaustion: retryExhaustionPort,
		FleetExecution: executionTransport,
	}
	if module != nil {
		return module.NewExecutionCapability(dependencies)
	}
	return appserve.NewExecutionCapability(dependencies)
}

// BuildWorkflowCatalogModule delegates capability composition to
// internal/app/serve while keeping low-level wiring out of the CLI package.
func BuildWorkflowCatalogModule(config WorkflowCatalogConfig) (*WorkflowCatalogModule, error) {
	appConfig := appserve.WorkflowCatalogConfig{
		Enabled:                         config.Enabled,
		AutomationEnabled:               config.AutomationEnabled,
		RuntimeDir:                      config.RuntimeDir,
		Workspace:                       config.Workspace,
		ExternalAuth:                    config.ExternalAuth,
		WorkspaceFromContext:            middleware.WorkspaceFromContext,
		BuiltinWorkflow:                 workflowdefs.IsBuiltinWorkflow,
		ExternalOperatorResolverFactory: newExternalOperatorResolverFactory(config.WorkspaceRoleResolver),
		BrowserSessionRouteFactory:      newBrowserSessionRoutes,
	}
	if config.StoreHandle != nil {
		appConfig.FleetDBClient = config.StoreHandle.FleetDBClient()
		if config.StoreHandle.Store != nil {
			appConfig.PrepareWorkflowTarget = newLegacyWorkflowTargetPreparation(config.StoreHandle.Store)
			appConfig.AutomationDriverRuns = config.StoreHandle.Store.DriverRuns()
			appConfig.AutomationAwaits = config.StoreHandle.Store.Awaits()
			appConfig.AutomationWorkspaces = config.StoreHandle.Store.Workspaces()
			appConfig.AutomationWebhookVerifier = webhooks.NewCompatibilityVerifier(webhooks.CompatibilityVerifierConfig{
				Bindings: config.StoreHandle.Store.TriggerBindings(), Connectors: config.StoreHandle.Store.Connectors(),
			})
		}
	}
	module, err := appserve.NewWorkflowCatalogModule(appConfig)
	if err != nil || module == nil {
		return nil, err
	}
	var automationCapability *AutomationCapability
	if automation := module.AutomationCapability(); automation != nil {
		automationCapability = &AutomationCapability{
			AutomationCapability: automation,
			issueJournal:         automation.IssueJournalEmitter(),
			runOutcomes:          automation.RunOutcomePublisher(),
			runtime:              automation,
		}
	}
	return &WorkflowCatalogModule{module: module, capability: module, automation: automationCapability}, nil
}
