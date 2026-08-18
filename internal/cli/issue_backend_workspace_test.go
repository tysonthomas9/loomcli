package cli

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/fleethttp"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func TestWorkspaceAwareIssueBackendForURL_UsesConcreteURLWhenEnvUnset(t *testing.T) {
	t.Setenv(bootstrap.EnvFleetDBURL, "")
	t.Setenv(fleethttp.EnvFleetDBActor, "")

	fn := WorkspaceAwareIssueBackendForURL("http://127.0.0.1:12345", "tester")
	be := fn(middleware.WithWorkspace(context.Background(), "CLEAN"))
	if be == nil {
		t.Fatal("backend was nil")
	}
	if got := be.BackendName(); got != "fleet" {
		t.Fatalf("BackendName() = %q, want fleet", got)
	}
}
