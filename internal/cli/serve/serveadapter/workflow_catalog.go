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
	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/infra/workflowdistribution/authoring"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/webhooks"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
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
	required = append(required, fleetdb.Phase5FoundationCapabilities()...)
	if !catalogEnabled {
		return required, nil
	}
	required = append(
		required,
		fleetdb.WorkflowCatalogVersionLifecycleCapability,
		fleetdb.WorkflowCatalogVersionAuthoringCapability,
		fleetdb.AgentsProvisioningProgressCapability,
	)
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
	Workspace             string
	ExternalAuth          bool
	WorkspaceRoleResolver middleware.WorkspaceRoleResolver
}

// RefreshBoundPromptAgentWorkflows keeps the legacy built-in distribution
// dependency inside the reviewed Workflow Catalog compatibility adapter. It
// must run before Automation dispatchers start so an existing binding cannot
// admit work against a stale embedded prompt-agent version after an upgrade.
func RefreshBoundPromptAgentWorkflows(
	ctx context.Context,
	handle *bootstrap.StoreHandle,
	module *WorkflowCatalogModule,
) error {
	if handle == nil || handle.Store == nil {
		return fmt.Errorf("refresh bound prompt-agent workflows: FleetDB Store handle is required")
	}
	if module == nil {
		return fmt.Errorf("refresh bound prompt-agent workflows: Workflow Catalog capability is required")
	}
	coordinator, err := appworkflowauthoring.New(workflowdefs.NewBundleStager())
	if err != nil {
		return err
	}
	return coordinator.RefreshBoundPromptAgentWorkflows(
		ctx,
		workflowdefs.NewBoundPromptAgentIndex(handle.Store),
		module.CatalogAPI(),
		module.VersionAuthoringAPI(),
		module,
		workflowdefs.NewBuiltinSupport(),
	)
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

type workflowTargetAuthoringPorts struct {
	catalog     workflowcatalog.API
	authoring   workflowcatalog.VersionAuthoringAPI
	authorities appworkflowauthoring.ManagedBuiltinAuthorityProvider
}

type workflowTargetAuthoringPortsResolver func() workflowTargetAuthoringPorts

func newWorkflowCatalogTargetPreparation(resolve workflowTargetAuthoringPortsResolver) appserve.WorkflowTargetPreparationFactory {
	if resolve == nil {
		return nil
	}
	return func(unavailable error) appserve.WorkflowTargetPreparation {
		return func(ctx context.Context, workspace, workflow string) (appserve.WorkflowTarget, error) {
			ports := resolve()
			driver, err := prepareWorkflowCatalogTarget(ctx, ports, workspace, workflow)
			if err != nil {
				if errors.Is(err, appworkflowauthoring.ErrBuildToolchainUnavailable) {
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

func prepareWorkflowCatalogTarget(
	ctx context.Context,
	ports workflowTargetAuthoringPorts,
	workspace, workflow string,
) (*workflowcatalog.Driver, error) {
	if ports.catalog == nil || ports.authoring == nil || ports.authorities == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	if workflowcatalog.IsBuiltinWorkflowName(strings.TrimSpace(workflow)) {
		coordinator, err := appworkflowauthoring.New(workflowdefs.NewBundleStager())
		if err != nil {
			return nil, err
		}
		if err := coordinator.EnsureBuiltin(
			ctx,
			ports.catalog,
			ports.authoring,
			ports.authorities,
			workflowdefs.NewBuiltinSupport(),
			workspace,
			workflow,
		); err != nil {
			return nil, err
		}
	}
	return ports.catalog.GetDriver(ctx, workspace, workflow)
}

// AutomationCapability is the CLI composition view of Automation. The web
// surface is embedded as an interface so callers cannot recover the concrete
// app-composition handle, while runtime-only ports remain available to serve.
type AutomationCapability struct {
	webui.AutomationCapability
	issueJournal systemeventing.IssueJournalEmitter
	runOutcomes  driver.RunOutcomePublisher
	runtime      interface {
		RuntimeRegistrations() []platformruntime.Registration
	}
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

func (module *WorkflowCatalogModule) VersionAuthoringAPI() workflowcatalog.VersionAuthoringAPI {
	if module == nil || module.capability == nil {
		return nil
	}
	return module.capability.VersionAuthoringAPI()
}

func (module *WorkflowCatalogModule) OperatorAuthorityResolver() appserve.OperatorAuthorityResolver {
	if module == nil || module.capability == nil {
		return nil
	}
	return module.capability.OperatorAuthorityResolver()
}

func (module *WorkflowCatalogModule) AuthorityForManagedBuiltin(
	_ context.Context,
	workspace, reason string,
) (authority.SystemAuthority, error) {
	if module == nil || module.capability == nil {
		return authority.SystemAuthority{}, workflowcatalog.ErrUnavailable
	}
	return module.capability.ManagedBuiltinAuthoringAuthority(workspace, reason)
}

func (module *WorkflowCatalogModule) PrepareWorkflowTarget(
	ctx context.Context,
	workspace, workflow string,
) (*workflowcatalog.Driver, error) {
	if module == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	return prepareWorkflowCatalogTarget(ctx, workflowTargetAuthoringPorts{
		catalog: module.CatalogAPI(), authoring: module.VersionAuthoringAPI(), authorities: module,
	}, workspace, workflow)
}

func (module *WorkflowCatalogModule) NewExecutionCapability(
	dependencies appserve.ExecutionDependencies,
) (*appserve.ExecutionCapability, error) {
	if module == nil || module.capability == nil {
		return nil, fmt.Errorf("compose Execution: Workflow Catalog authority is required")
	}
	return module.capability.NewExecutionCapability(dependencies)
}

// CapabilityAvailable reports whether the application composition handle is
// present without exposing that concrete handle across the CLI adapter
// boundary.
func (module *WorkflowCatalogModule) CapabilityAvailable() bool {
	return module != nil && module.capability != nil
}

// NewSourceControlCapabilityWithFleetDB shares the catalog-owned authority
// seal with Source Control while keeping the concrete catalog capability
// private to this composition package.
func (module *WorkflowCatalogModule) NewSourceControlCapabilityWithFleetDB(
	localSettingsDir string,
	repositories sourcecontrol.RepositoryResolver,
	client *fleetdb.Client,
) (*appserve.SourceControlCapability, error) {
	if !module.CapabilityAvailable() {
		return nil, workflowcatalog.ErrUnavailable
	}
	return module.capability.NewSourceControlCapabilityWithFleetDB(
		localSettingsDir,
		repositories,
		client,
	)
}

// NewAgentProvisioningCapability composes the cross-owner provisioning
// process with the same catalog authority without publishing that authority.
func (module *WorkflowCatalogModule) NewAgentProvisioningCapability(
	agents *appserve.AgentsCapability,
	sourceControl *appserve.SourceControlCapability,
	client *fleetdb.Client,
	config appserve.AgentProvisioningConfig,
) (*appserve.AgentProvisioningCapability, error) {
	if !module.CapabilityAvailable() {
		return nil, workflowcatalog.ErrUnavailable
	}
	return agents.NewAgentProvisioningCapability(
		module.capability,
		sourceControl,
		client,
		config,
	)
}

// NewInteractionCapabilityWithFleetDB shares the catalog-owned issuer with
// Interaction without leaking the concrete catalog capability.
func (module *WorkflowCatalogModule) NewInteractionCapabilityWithFleetDB(
	config appserve.InteractionConfig,
	client *fleetdb.Client,
) (*appserve.InteractionCapability, error) {
	if !module.CapabilityAvailable() {
		return nil, workflowcatalog.ErrUnavailable
	}
	return module.capability.NewInteractionCapabilityWithFleetDB(config, client)
}

// BuildExecutionCapability composes the always-on Execution slice. When
// Workflow Catalog is active it shares that control plane's issuer and
// operator resolver so Desktop's action-limited browser bearer can submit a
// run. Catalog-disabled profiles still get runtime/TaskRun execution but no
// operator submit resolver.
func BuildExecutionCapability(
	module *WorkflowCatalogModule,
	handle *bootstrap.StoreHandle,
	agentQueries agents.IdentityQueries,
) (*appserve.ExecutionCapability, error) {
	if handle == nil || handle.Store == nil || handle.FleetDBClient() == nil {
		return nil, fmt.Errorf("compose Execution: FleetDB Store handle is required")
	}
	if agentQueries == nil {
		return nil, fmt.Errorf("compose Execution: canonical Agents queries are required")
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
		WorkerProfiles: st.WorkerProfiles(), AgentQueries: agentQueries, Outbox: st.Outbox(), Awaits: st.Awaits(),
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
func BuildWorkflowCatalogModule(config WorkflowCatalogConfig) (*WorkflowCatalogModule, error) { //nolint:funlen // Composition validates the complete catalog transport, authority, and authoring dependency set.
	var composed *WorkflowCatalogModule
	appConfig := appserve.WorkflowCatalogConfig{
		Enabled:              config.Enabled,
		AutomationEnabled:    config.AutomationEnabled,
		Workspace:            config.Workspace,
		ExternalAuth:         config.ExternalAuth,
		WorkspaceFromContext: middleware.WorkspaceFromContext,
		BuiltinWorkflow: func(name string) bool {
			return workflowcatalog.IsBuiltinWorkflowName(strings.TrimSpace(name))
		},
		ExternalOperatorResolverFactory: newExternalOperatorResolverFactory(config.WorkspaceRoleResolver),
		PrepareWorkflowTarget: newWorkflowCatalogTargetPreparation(func() workflowTargetAuthoringPorts {
			if composed == nil {
				return workflowTargetAuthoringPorts{}
			}
			return workflowTargetAuthoringPorts{
				catalog: composed.CatalogAPI(), authoring: composed.VersionAuthoringAPI(), authorities: composed,
			}
		}),
	}
	if config.StoreHandle != nil {
		appConfig.FleetDBClient = config.StoreHandle.FleetDBClient()
		if config.StoreHandle.Store != nil {
			appConfig.AutomationDriverRuns = config.StoreHandle.Store.DriverRuns()
			appConfig.AutomationAwaits = config.StoreHandle.Store.Awaits()
			appConfig.AutomationWorkspaces = config.StoreHandle.Store.Workspaces()
			appConfig.AutomationWebhookVerifierFactory = func(bindings automation.BindingQueries) appserve.WebhookVerifier {
				return webhooks.NewVerifier(webhooks.VerifierConfig{
					Bindings: bindings, Connectors: config.StoreHandle.Store.Connectors(),
				})
			}
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
	composed = &WorkflowCatalogModule{module: module, capability: module, automation: automationCapability}
	driver.SetGlobalRunnerResolver(newGlobalBuiltinRunnerResolver(
		composed.CatalogAPI(),
		composed.VersionAuthoringAPI(),
		composed,
	))
	return composed, nil
}

func newGlobalBuiltinRunnerResolver(
	catalog workflowcatalog.API,
	authoring workflowcatalog.VersionAuthoringAPI,
	authorities appworkflowauthoring.ManagedBuiltinAuthorityProvider,
) driver.GlobalRunnerResolver {
	coordinator, err := appworkflowauthoring.New(workflowdefs.NewBundleStager())
	if err != nil {
		return nil
	}
	support := workflowdefs.NewBuiltinSupport()
	return func(
		ctx context.Context,
		workspace,
		runnerName string,
	) (*driver.GlobalRunnerResolution, error) {
		result, err := coordinator.ResolveGlobalBuiltinRunner(
			ctx,
			catalog,
			authoring,
			authorities,
			support,
			workspace,
			runnerName,
		)
		if errors.Is(err, workflowcatalog.ErrNotFound) {
			return nil, domain.ErrNotFound
		}
		if errors.Is(err, workflowcatalog.ErrInvalid) {
			return nil, domain.ErrInvalid
		}
		if err != nil {
			return nil, err
		}
		if result == nil || result.Driver == nil || result.Version == nil {
			return nil, workflowcatalog.ErrInvalidPersistedState
		}
		return &driver.GlobalRunnerResolution{
			Driver:  result.Driver,
			Version: result.Version,
			Spec: driver.DriverRunnerSpec{
				Name:       result.Spec.Name,
				Kind:       result.Spec.Kind,
				Entrypoint: result.Spec.Entrypoint,
			},
		}, nil
	}
}
