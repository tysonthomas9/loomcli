package serve

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
)

const workflowCatalogEnabledEnv = "LOOM_WORKFLOW_CATALOG_ENABLED"

func workflowCatalogEnabled(externalAuth, workspaceRoleResolverAvailable bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(workflowCatalogEnabledEnv))
	if raw == "" {
		if externalAuth && !workspaceRoleResolverAvailable {
			return false, nil
		}
		return true, nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", workflowCatalogEnabledEnv, err)
	}
	if enabled && externalAuth && !workspaceRoleResolverAvailable {
		return false, fmt.Errorf("%s=%q requires a workspace role resolver when external authentication is configured", workflowCatalogEnabledEnv, raw)
	}
	return enabled, nil
}

func requiredFleetDBCapabilities(externalAuth, workspaceRoleResolverAvailable bool) ([]string, error) {
	enabled, err := workflowCatalogEnabled(externalAuth, workspaceRoleResolverAvailable)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	return []string{fleetdb.WorkflowCatalogVersionLifecycleCapability}, nil
}
