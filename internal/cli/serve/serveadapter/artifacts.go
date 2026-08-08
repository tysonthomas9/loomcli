package serveadapter

import (
	"fmt"

	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

// BuildArtifactsCapability composes Artifacts from the StoreHandle's one
// shared FleetDB client. It does not construct or authenticate another client.
func BuildArtifactsCapability(execution *appserve.ExecutionCapability, handle *bootstrap.StoreHandle) (*appserve.ArtifactsCapability, error) {
	if execution == nil {
		return nil, fmt.Errorf("compose Artifacts: Execution capability is required")
	}
	if handle == nil || handle.FleetDBClient() == nil {
		return nil, fmt.Errorf("compose Artifacts: shared FleetDB client is required")
	}
	return execution.NewArtifactsCapability(handle.FleetDBClient().ArtifactCommands())
}

// BuildExecutionAndArtifactsCapabilities composes the adjacent Phase 4
// capabilities while keeping their concrete app/serve types behind the
// existing CLI adapter boundary.
func BuildExecutionAndArtifactsCapabilities(
	module *WorkflowCatalogModule,
	handle *bootstrap.StoreHandle,
	agentQueries agents.IdentityQueries,
) (*appserve.ExecutionCapability, *appserve.ArtifactsCapability, error) {
	var execution *appserve.ExecutionCapability
	if handle != nil && handle.Store != nil {
		var err error
		execution, err = BuildExecutionCapability(module, handle, agentQueries)
		if err != nil {
			return nil, nil, err
		}
	}
	artifacts, err := BuildArtifactsCapability(execution, handle)
	if err != nil {
		return nil, nil, err
	}
	return execution, artifacts, nil
}
