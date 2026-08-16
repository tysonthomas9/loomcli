package serveadapter

import (
	"strings"
	"testing"

	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
)

func TestBuildArtifactsCapabilityFailsClosedWithoutSharedFleetDBClient(t *testing.T) {
	if capability, err := BuildArtifactsCapability(nil, nil); capability != nil || err == nil || !strings.Contains(err.Error(), "Execution capability is required") {
		t.Fatalf("BuildArtifactsCapability(nil, nil) = %#v, %v; want Execution failure", capability, err)
	}
	for _, handle := range []*bootstrap.StoreHandle{nil, {Store: &fleetdb.Client{}}} {
		capability, err := BuildArtifactsCapability(&appserve.ExecutionCapability{}, handle)
		if capability != nil || err == nil || !strings.Contains(err.Error(), "shared FleetDB client is required") {
			t.Fatalf("BuildArtifactsCapability(%#v) = %#v, %v; want fail closed", handle, capability, err)
		}
	}
}
