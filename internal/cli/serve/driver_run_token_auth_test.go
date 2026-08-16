package serve

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	driverexecutor "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/webui"
)

func TestBuildServerConfigRejectsMalformedDriverRunTokenSigningKey(t *testing.T) {
	t.Setenv(driverexecutor.RunTokenSigningKeyEnv, "not-hex")
	handle := &bootstrap.StoreHandle{Store: &fleetdb.Client{}}

	_, _, err := buildServerConfig(webui.MonitorHandlers{}, fleetState{}, handle)
	if err == nil || !strings.Contains(err.Error(), driverexecutor.RunTokenSigningKeyEnv) {
		t.Fatalf("buildServerConfig error = %v, want malformed signing-key error", err)
	}
}
