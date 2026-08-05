package serveadapter

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
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
// serve profile, independently of the optional Catalog and Automation slices.
func RequiredFleetDBCapabilities(externalAuth, workspaceRoleResolverAvailable bool) ([]string, error) {
	catalogEnabled, err := WorkflowCatalogEnabled(externalAuth, workspaceRoleResolverAvailable)
	if err != nil {
		return nil, err
	}
	automationEnabled, err := AutomationEnabled(externalAuth, workspaceRoleResolverAvailable)
	if err != nil {
		return nil, err
	}
	required := []string{fleetdb.ExecutionAwaitAtomicResumeCapability}
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

// BuildWorkflowCatalogModule delegates capability composition to
// internal/app/serve while keeping low-level wiring out of the CLI package.
func BuildWorkflowCatalogModule(config WorkflowCatalogConfig) (*WorkflowCatalogModule, error) {
	appConfig := appserve.WorkflowCatalogConfig{
		Enabled:               config.Enabled,
		AutomationEnabled:     config.AutomationEnabled,
		RuntimeDir:            config.RuntimeDir,
		Workspace:             config.Workspace,
		ExternalAuth:          config.ExternalAuth,
		WorkspaceRoleResolver: config.WorkspaceRoleResolver,
	}
	if config.StoreHandle != nil {
		appConfig.FleetDBClient = config.StoreHandle.FleetDBClient()
		if config.StoreHandle.Store != nil {
			appConfig.WorkflowTargetCatalog = config.StoreHandle.Store
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
	return &WorkflowCatalogModule{module: module, automation: automationCapability}, nil
}
