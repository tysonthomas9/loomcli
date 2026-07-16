package serveadapter

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// WorkflowCatalogEnabledEnv controls whether serve exposes the Workflow
// Catalog slice.
const WorkflowCatalogEnabledEnv = "LOOM_WORKFLOW_CATALOG_ENABLED"

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

// RequiredFleetDBCapabilities derives startup compatibility requirements from
// the slices enabled in serve configuration.
func RequiredFleetDBCapabilities(externalAuth, workspaceRoleResolverAvailable bool) ([]string, error) {
	enabled, err := WorkflowCatalogEnabled(externalAuth, workspaceRoleResolverAvailable)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	return []string{fleetdb.WorkflowCatalogVersionLifecycleCapability}, nil
}

// WorkflowCatalogConfig contains the CLI-derived inputs forwarded to the
// serve composition root. The adapter never constructs a FleetDB client.
type WorkflowCatalogConfig struct {
	Enabled               bool
	StoreHandle           *bootstrap.StoreHandle
	RuntimeDir            string
	Workspace             string
	ExternalAuth          bool
	WorkspaceRoleResolver middleware.WorkspaceRoleResolver
}

// BuildWorkflowCatalogModule delegates capability composition to
// internal/app/serve while keeping low-level wiring out of the CLI package.
func BuildWorkflowCatalogModule(config WorkflowCatalogConfig) (interface{ Register(*http.ServeMux) }, error) {
	appConfig := appserve.WorkflowCatalogConfig{
		Enabled:               config.Enabled,
		RuntimeDir:            config.RuntimeDir,
		Workspace:             config.Workspace,
		ExternalAuth:          config.ExternalAuth,
		WorkspaceRoleResolver: config.WorkspaceRoleResolver,
	}
	if config.StoreHandle != nil {
		appConfig.FleetDBClient = config.StoreHandle.FleetDBClient()
	}
	module, err := appserve.NewWorkflowCatalogModule(appConfig)
	if err != nil || module == nil {
		return nil, err
	}
	return module, nil
}
