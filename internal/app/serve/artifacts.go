package serve

import (
	"fmt"

	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	artifactfleetdb "github.com/tysonthomas9/loomcli/internal/modules/artifacts/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// ArtifactsCapability is the composition-owned handle for the owner-fenced
// Artifact lifecycle. Consumers receive only the module API; the shared
// FleetDB transport and its service credentials remain inside composition.
type ArtifactsCapability struct {
	api artifacts.API
}

func (capability *ArtifactsCapability) ArtifactsAPI() artifacts.API {
	if capability == nil {
		return nil
	}
	return capability.api
}

// NewArtifactsCapability composes Artifacts with the same private authority
// issuer as Execution. The issuer remains sealed inside serve composition.
func (capability *ExecutionCapability) NewArtifactsCapability(transport infrafleetdb.ArtifactTransport) (*ArtifactsCapability, error) {
	if capability == nil || capability.issuer == nil {
		return nil, fmt.Errorf("compose Artifacts: shared Execution authority issuer is required")
	}
	return newArtifactsCapability(transport, capability.issuer)
}

func newArtifactsCapability(transport infrafleetdb.ArtifactTransport, issuer *authority.Issuer) (*ArtifactsCapability, error) {
	adapter, err := artifactfleetdb.New(newArtifactsFleetDBTransport(transport))
	if err != nil {
		return nil, fmt.Errorf("compose Artifacts: %w", err)
	}
	if issuer == nil {
		return nil, fmt.Errorf("compose Artifacts: shared Execution authority issuer is required")
	}
	admission, err := issuer.NewAdmission(artifacts.OperationRules()...)
	if err != nil {
		return nil, fmt.Errorf("compose Artifacts admission: %w", err)
	}
	service, err := artifacts.New(adapter, admission)
	if err != nil {
		return nil, fmt.Errorf("compose Artifacts service: %w", err)
	}
	return &ArtifactsCapability{api: service}, nil
}
