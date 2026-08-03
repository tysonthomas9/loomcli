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

func phase5RequiredCapabilities(catalog, automation bool) []string {
	required := append([]string{fleetdb.ExecutionAwaitAtomicResumeCapability}, fleetdb.Phase4FoundationCapabilities()...)
	required = append(required, fleetdb.Phase5FoundationCapabilities()...)
	if catalog {
		required = append(
			required,
			fleetdb.WorkflowCatalogVersionLifecycleCapability,
			fleetdb.WorkflowCatalogVersionAuthoringCapability,
			fleetdb.AgentsProvisioningProgressCapability,
		)
	}
	if automation {
		required = append(required, fleetdb.AutomationTriggerAdmissionCapability)
	}
	return required
}

func sortedPhase5RequiredCapabilities(catalog, automation bool) []string {
	required := phase5RequiredCapabilities(catalog, automation)
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
		{name: "local default enabled", want: phase5RequiredCapabilities(true, true)},
		{name: "local explicit enabled", catalogValue: "true", want: phase5RequiredCapabilities(true, true)},
		{name: "automation explicitly disabled", catalogValue: "true", automationValue: "false", want: phase5RequiredCapabilities(true, false)},
		{name: "disabled", catalogValue: "false", want: phase5RequiredCapabilities(false, false)},
		{name: "automation cannot outlive catalog", catalogValue: "false", automationValue: "true", wantErr: true},
		{name: "external default disabled without resolver", externalAuth: true, want: phase5RequiredCapabilities(false, false)},
		{name: "external explicit disabled without resolver", catalogValue: "false", externalAuth: true, want: phase5RequiredCapabilities(false, false)},
		{name: "external default enabled with resolver", externalAuth: true, resolverAvailable: true, want: phase5RequiredCapabilities(true, true)},
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
	wantBase := sortedPhase5RequiredCapabilities(false, false)
	if incompatibility.Kind != fleetdb.CapabilityKeysMissing ||
		!reflect.DeepEqual(incompatibility.Required, wantBase) ||
		!reflect.DeepEqual(incompatibility.Missing, wantBase) {
		t.Fatalf("incompatibility=%+v", incompatibility)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("capability calls=%d, want 1", got)
	}
}

func TestOpenServeStoreRejectsFleetWithoutReviewHandoffCapability(t *testing.T) {
	var capabilityCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != fleetdb.CapabilitiesAPIPath {
			http.Error(w, "unexpected fallback request", http.StatusInternalServerError)
			return
		}
		capabilityCalls.Add(1)
		available := slices.DeleteFunc(
			phase5RequiredCapabilities(false, false),
			func(capability string) bool {
				return capability == fleetdb.ExecutionDriverRunReviewWorkItemHandoffCapability
			},
		)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"api_revision": "v1", "capabilities": available})
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
		t.Fatal("store handle is non-nil for a Fleet deployment without atomic review handoff")
	}
	var incompatibility *fleetdb.CapabilityIncompatibilityError
	if !errors.As(err, &incompatibility) {
		t.Fatalf("error=%v, want *fleetdb.CapabilityIncompatibilityError", err)
	}
	if incompatibility.Kind != fleetdb.CapabilityKeysMissing ||
		!reflect.DeepEqual(incompatibility.Missing, []string{fleetdb.ExecutionDriverRunReviewWorkItemHandoffCapability}) {
		t.Fatalf("incompatibility=%+v", incompatibility)
	}
	if got := capabilityCalls.Load(); got != 1 {
		t.Fatalf("capability calls=%d, want 1", got)
	}
}

func TestOpenServeStoreRejectsFleetWithoutRequiredAgentAndInteractionCapabilities(t *testing.T) {
	tests := []struct {
		name       string
		capability string
	}{
		{
			name:       "agent service commands",
			capability: fleetdb.AgentsServiceCommandsCapability,
		},
		{
			name:       "agent ownership lease commands",
			capability: fleetdb.AgentsOwnershipLeaseCommandsCapability,
		},
		{
			name:       "interaction session commands",
			capability: fleetdb.InteractionSessionCommandsCapability,
		},
		{
			name:       "repository admission",
			capability: fleetdb.RepositoriesAdmissionCapability,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var capabilityCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != fleetdb.CapabilitiesAPIPath {
					http.Error(w, "unexpected fallback request", http.StatusInternalServerError)
					return
				}
				capabilityCalls.Add(1)
				available := slices.DeleteFunc(
					phase5RequiredCapabilities(false, false),
					func(capability string) bool {
						return capability == test.capability
					},
				)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"api_revision": "v1", "capabilities": available})
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
				t.Fatal("store handle is non-nil for a Fleet deployment without lifecycle command fencing")
			}
			var incompatibility *fleetdb.CapabilityIncompatibilityError
			if !errors.As(err, &incompatibility) {
				t.Fatalf("error=%v, want *fleetdb.CapabilityIncompatibilityError", err)
			}
			if incompatibility.Kind != fleetdb.CapabilityKeysMissing ||
				!reflect.DeepEqual(incompatibility.Missing, []string{test.capability}) {
				t.Fatalf("incompatibility=%+v", incompatibility)
			}
			if got := capabilityCalls.Load(); got != 1 {
				t.Fatalf("capability calls=%d, want 1", got)
			}
		})
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
		available := phase5RequiredCapabilities(true, false)
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
	wantRequired := sortedPhase5RequiredCapabilities(true, true)
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

func TestOpenServeStoreRejectsMissingWorkflowCatalogAuthoringCapabilitiesWithoutFallback(t *testing.T) {
	for _, missing := range []string{
		fleetdb.WorkflowCatalogVersionAuthoringCapability,
		fleetdb.AgentsProvisioningProgressCapability,
	} {
		t.Run(missing, func(t *testing.T) {
			var capabilityCalls atomic.Int32
			var fallbackCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != fleetdb.CapabilitiesAPIPath {
					fallbackCalls.Add(1)
					http.Error(w, "unexpected fallback request", http.StatusInternalServerError)
					return
				}
				capabilityCalls.Add(1)
				available := slices.DeleteFunc(
					phase5RequiredCapabilities(true, false),
					func(capability string) bool {
						return capability == missing
					},
				)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"api_revision": "v1", "capabilities": available})
			}))
			defer server.Close()

			t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
			t.Setenv(bootstrap.EnvFleetDBURL, server.URL)
			t.Setenv(serveadapter.WorkflowCatalogEnabledEnv, "true")
			t.Setenv(serveadapter.AutomationEnabledEnv, "false")
			originalAuthURL := serveAuthURL
			serveAuthURL = ""
			t.Cleanup(func() { serveAuthURL = originalAuthURL })

			handle, err := openServeStore(context.Background(), fleetState{})
			if handle != nil {
				_ = handle.Close()
				t.Fatal("store handle is non-nil for a Fleet deployment without atomic Workflow Catalog authoring")
			}
			var incompatibility *fleetdb.CapabilityIncompatibilityError
			if !errors.As(err, &incompatibility) {
				t.Fatalf("error=%v, want *fleetdb.CapabilityIncompatibilityError", err)
			}
			if incompatibility.Kind != fleetdb.CapabilityKeysMissing ||
				!reflect.DeepEqual(incompatibility.Missing, []string{missing}) {
				t.Fatalf("incompatibility=%+v", incompatibility)
			}
			if got := capabilityCalls.Load(); got != 1 {
				t.Fatalf("capability calls=%d, want 1", got)
			}
			if got := fallbackCalls.Load(); got != 0 {
				t.Fatalf("fallback calls=%d, want 0", got)
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
		t.Setenv(serveadapter.WorkflowCatalogEnabledEnv, "")
		cfg, capabilities, err := buildServerConfig(webui.MonitorHandlers{}, fleetState{}, nil)
		if err != nil {
			t.Fatalf("buildServerConfig: %v", err)
		}
		if cfg.WorkflowCatalogModule != nil {
			t.Fatalf("WorkflowCatalogModule = %#v, want nil", cfg.WorkflowCatalogModule)
		}
		if capabilities.automation != nil || len(capabilities.runtime) != 0 {
			t.Fatalf("capabilities = %#v, want empty", capabilities)
		}
	})

	t.Run("explicit enable fails before registration", func(t *testing.T) {
		t.Setenv(serveadapter.WorkflowCatalogEnabledEnv, "true")
		cfg, capabilities, err := buildServerConfig(webui.MonitorHandlers{}, fleetState{}, nil)
		if err == nil || !strings.Contains(err.Error(), "requires a workspace role resolver") {
			t.Fatalf("buildServerConfig error = %v, want missing resolver error", err)
		}
		if cfg.WorkflowCatalogModule != nil {
			t.Fatalf("WorkflowCatalogModule = %#v, want nil", cfg.WorkflowCatalogModule)
		}
		if capabilities.automation != nil || len(capabilities.runtime) != 0 {
			t.Fatalf("capabilities = %#v, want empty", capabilities)
		}
	})
}
