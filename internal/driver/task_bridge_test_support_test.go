//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	artifactredact "github.com/tysonthomas9/loomcli/internal/infra/artifactredact"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const testTaskRunAPIURL = "http://127.0.0.1:8080"

var bridgeArtifactTestIssuer = authority.NewIssuer()

type bridgeArtifactFixtureStore interface {
	ArtifactCommands() artifactsmodule.Store
}

// testArtifactsAPI composes the real owner service over the owner-typed
// memstore adapter used by driver tests.
func testArtifactsAPI(st bridgeArtifactFixtureStore) artifactsmodule.API {
	if st == nil {
		return nil
	}
	admission, err := bridgeArtifactTestIssuer.NewAdmission(artifactsmodule.OperationRules()...)
	if err != nil {
		panic(err)
	}
	evidence, err := artifactsmodule.NewEvidencePolicy(artifactredact.Adapter{})
	if err != nil {
		panic(err)
	}
	service, err := artifactsmodule.New(st.ArtifactCommands(), admission, evidence)
	if err != nil {
		panic(err)
	}
	return service
}
