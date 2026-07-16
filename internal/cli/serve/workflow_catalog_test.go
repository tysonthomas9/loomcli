package serve

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/webui"
)

func TestRequiredFleetDBCapabilitiesFollowEnabledSlices(t *testing.T) {
	tests := []struct {
		name              string
		value             string
		externalAuth      bool
		resolverAvailable bool
		want              []string
		wantErr           bool
	}{
		{name: "local default enabled", want: []string{fleetdb.WorkflowCatalogVersionLifecycleCapability}},
		{name: "local explicit enabled", value: "true", want: []string{fleetdb.WorkflowCatalogVersionLifecycleCapability}},
		{name: "disabled", value: "false"},
		{name: "external default disabled without resolver", externalAuth: true},
		{name: "external explicit disabled without resolver", value: "false", externalAuth: true},
		{name: "external default enabled with resolver", externalAuth: true, resolverAvailable: true, want: []string{fleetdb.WorkflowCatalogVersionLifecycleCapability}},
		{name: "external explicit enabled without resolver fails", value: "true", externalAuth: true, wantErr: true},
		{name: "invalid", value: "sometimes", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(workflowCatalogEnabledEnv, test.value)
			got, err := requiredFleetDBCapabilities(test.externalAuth, test.resolverAvailable)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr=%t", err, test.wantErr)
			}
			if len(got) != len(test.want) {
				t.Fatalf("capabilities = %v, want %v", got, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("capabilities = %v, want %v", got, test.want)
				}
			}
		})
	}
}

func TestBuildServerConfigExternalWorkflowCatalogProfile(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", t.TempDir())
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)
	originalAuthURL := serveAuthURL
	serveAuthURL = "https://auth.example.test"
	t.Cleanup(func() { serveAuthURL = originalAuthURL })

	t.Run("default omits route module", func(t *testing.T) {
		t.Setenv(workflowCatalogEnabledEnv, "")
		cfg, err := buildServerConfig(webui.MonitorHandlers{}, fleetState{}, nil)
		if err != nil {
			t.Fatalf("buildServerConfig: %v", err)
		}
		if cfg.WorkflowCatalogModule != nil {
			t.Fatalf("WorkflowCatalogModule = %#v, want nil", cfg.WorkflowCatalogModule)
		}
	})

	t.Run("explicit enable fails before registration", func(t *testing.T) {
		t.Setenv(workflowCatalogEnabledEnv, "true")
		cfg, err := buildServerConfig(webui.MonitorHandlers{}, fleetState{}, nil)
		if err == nil || !strings.Contains(err.Error(), "requires a workspace role resolver") {
			t.Fatalf("buildServerConfig error = %v, want missing resolver error", err)
		}
		if cfg.WorkflowCatalogModule != nil {
			t.Fatalf("WorkflowCatalogModule = %#v, want nil", cfg.WorkflowCatalogModule)
		}
	})
}
