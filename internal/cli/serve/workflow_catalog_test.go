package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/serveadapter"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/webui"
)

func phase4RequiredCapabilities(catalog, automation bool) []string {
	required := append([]string{fleetdb.ExecutionAwaitAtomicResumeCapability}, fleetdb.Phase4FoundationCapabilities()...)
	if catalog {
		required = append(required, fleetdb.WorkflowCatalogVersionLifecycleCapability)
	}
	if automation {
		required = append(required, fleetdb.AutomationTriggerAdmissionCapability)
	}
	return required
}

func sortedPhase4RequiredCapabilities(catalog, automation bool) []string {
	required := phase4RequiredCapabilities(catalog, automation)
	slices.Sort(required)
	return required
}

func TestAutomationWebCapabilityViewExposesNoRuntimeProducerPorts(t *testing.T) {
	view := reflect.TypeOf(automationWebCapabilityView{})
	for _, forbidden := range []string{"IssueJournalEmitter", "RunOutcomePublisher", "RuntimeRegistrations"} {
		if _, exposed := view.MethodByName(forbidden); exposed {
			t.Fatalf("web capability view exposes %s", forbidden)
		}
	}
}

func TestRequiredFleetDBCapabilitiesFollowEnabledSlices(t *testing.T) {
	tests := []struct {
		name              string
		catalogValue      string
		automationValue   string
		externalAuth      bool
		resolverAvailable bool
		want              []string
		wantErr           bool
	}{
		{name: "local default enabled", want: phase4RequiredCapabilities(true, true)},
		{name: "local explicit enabled", catalogValue: "true", want: phase4RequiredCapabilities(true, true)},
		{name: "automation explicitly disabled", catalogValue: "true", automationValue: "false", want: phase4RequiredCapabilities(true, false)},
		{name: "disabled", catalogValue: "false", want: phase4RequiredCapabilities(false, false)},
		{name: "automation cannot outlive catalog", catalogValue: "false", automationValue: "true", wantErr: true},
		{name: "external default disabled without resolver", externalAuth: true, want: phase4RequiredCapabilities(false, false)},
		{name: "external explicit disabled without resolver", catalogValue: "false", externalAuth: true, want: phase4RequiredCapabilities(false, false)},
		{name: "external default enabled with resolver", externalAuth: true, resolverAvailable: true, want: phase4RequiredCapabilities(true, true)},
		{name: "external explicit enabled without resolver fails", catalogValue: "true", externalAuth: true, wantErr: true},
		{name: "invalid catalog", catalogValue: "sometimes", wantErr: true},
		{name: "invalid automation", automationValue: "sometimes", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(serveadapter.WorkflowCatalogEnabledEnv, test.catalogValue)
			t.Setenv(serveadapter.AutomationEnabledEnv, test.automationValue)
			got, err := serveadapter.RequiredFleetDBCapabilities(test.externalAuth, test.resolverAvailable)
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

func TestOpenServeStoreRejectsMissingExecutionAwaitCapabilityBeforeRuntime(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != fleetdb.CapabilitiesAPIPath {
			t.Errorf("request=%s %s, want GET %s", r.Method, r.URL.Path, fleetdb.CapabilitiesAPIPath)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_revision":"v1","capabilities":[]}`))
	}))
	defer server.Close()

	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(bootstrap.EnvFleetDBURL, server.URL)
	t.Setenv(serveadapter.WorkflowCatalogEnabledEnv, "false")
	t.Setenv(serveadapter.AutomationEnabledEnv, "false")
	originalAuthURL := serveAuthURL
	serveAuthURL = ""
	t.Cleanup(func() { serveAuthURL = originalAuthURL })

	handle, err := openServeStore(context.Background(), fleetState{})
	if handle != nil {
		_ = handle.Close()
		t.Fatal("store handle is non-nil for an incompatible execution deployment")
	}
	if err == nil {
		t.Fatal("openServeStore returned nil error")
	}
	var incompatibility *fleetdb.CapabilityIncompatibilityError
	if !errors.As(err, &incompatibility) {
		t.Fatalf("error=%v, want *fleetdb.CapabilityIncompatibilityError", err)
	}
	wantBase := sortedPhase4RequiredCapabilities(false, false)
	if incompatibility.Kind != fleetdb.CapabilityKeysMissing ||
		!reflect.DeepEqual(incompatibility.Required, wantBase) ||
		!reflect.DeepEqual(incompatibility.Missing, wantBase) {
		t.Fatalf("incompatibility=%+v", incompatibility)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("capability calls=%d, want 1", got)
	}
}

func TestOpenServeStoreRejectsMissingAutomationCapabilityWithoutFallback(t *testing.T) {
	var capabilityCalls atomic.Int32
	var fallbackCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != fleetdb.CapabilitiesAPIPath {
			fallbackCalls.Add(1)
			http.Error(w, "unexpected fallback request", http.StatusInternalServerError)
			return
		}
		capabilityCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		available := phase4RequiredCapabilities(true, false)
		_ = json.NewEncoder(w).Encode(map[string]any{"api_revision": "v1", "capabilities": available})
	}))
	defer server.Close()

	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(bootstrap.EnvFleetDBURL, server.URL)
	t.Setenv(serveadapter.WorkflowCatalogEnabledEnv, "true")
	t.Setenv(serveadapter.AutomationEnabledEnv, "true")
	originalAuthURL := serveAuthURL
	serveAuthURL = ""
	t.Cleanup(func() { serveAuthURL = originalAuthURL })

	handle, err := openServeStore(context.Background(), fleetState{})
	if handle != nil {
		_ = handle.Close()
		t.Fatal("store handle is non-nil for an Automation-incompatible deployment")
	}
	if err == nil {
		t.Fatal("openServeStore returned nil error")
	}
	var incompatibility *fleetdb.CapabilityIncompatibilityError
	if !errors.As(err, &incompatibility) {
		t.Fatalf("error=%v, want *fleetdb.CapabilityIncompatibilityError", err)
	}
	wantRequired := sortedPhase4RequiredCapabilities(true, true)
	if incompatibility.Kind != fleetdb.CapabilityKeysMissing ||
		!reflect.DeepEqual(incompatibility.Required, wantRequired) ||
		!reflect.DeepEqual(incompatibility.Missing, []string{fleetdb.AutomationTriggerAdmissionCapability}) {
		t.Fatalf("incompatibility=%+v", incompatibility)
	}
	wantError := "open store: openstore: fleet-db compatibility: fleetdb: incompatible deployment: " +
		"capabilities API revision \"v1\" is missing required capabilities: " +
		fleetdb.AutomationTriggerAdmissionCapability
	if err.Error() != wantError {
		t.Fatalf("error=%q, want %q", err, wantError)
	}
	if got := capabilityCalls.Load(); got != 1 {
		t.Fatalf("capability calls=%d, want 1", got)
	}
	if got := fallbackCalls.Load(); got != 0 {
		t.Fatalf("fallback calls=%d, want 0", got)
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
		t.Setenv(serveadapter.WorkflowCatalogEnabledEnv, "")
		cfg, automationCapability, err := buildServerConfig(webui.MonitorHandlers{}, fleetState{}, nil)
		if err != nil {
			t.Fatalf("buildServerConfig: %v", err)
		}
		if cfg.WorkflowCatalogModule != nil {
			t.Fatalf("WorkflowCatalogModule = %#v, want nil", cfg.WorkflowCatalogModule)
		}
		if automationCapability != nil {
			t.Fatalf("AutomationCapability = %#v, want nil", automationCapability)
		}
	})

	t.Run("explicit enable fails before registration", func(t *testing.T) {
		t.Setenv(serveadapter.WorkflowCatalogEnabledEnv, "true")
		cfg, automationCapability, err := buildServerConfig(webui.MonitorHandlers{}, fleetState{}, nil)
		if err == nil || !strings.Contains(err.Error(), "requires a workspace role resolver") {
			t.Fatalf("buildServerConfig error = %v, want missing resolver error", err)
		}
		if cfg.WorkflowCatalogModule != nil {
			t.Fatalf("WorkflowCatalogModule = %#v, want nil", cfg.WorkflowCatalogModule)
		}
		if automationCapability != nil {
			t.Fatalf("AutomationCapability = %#v, want nil", automationCapability)
		}
	})
}
