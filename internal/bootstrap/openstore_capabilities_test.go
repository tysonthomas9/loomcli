package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestOpenStoreCloudWithNoRequirementsSkipsCapabilityProbe(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected capability probe", http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv(EnvFleetDBURL, server.URL)

	handle, err := OpenStore(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("capability probe calls = %d, want 0", got)
	}
}

func TestOpenStoreCloudWithRequirementsChecksCapabilities(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != fleetdb.CapabilitiesAPIPath {
			t.Errorf("request = %s %s, want GET %s", r.Method, r.URL.Path, fleetdb.CapabilitiesAPIPath)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_revision":"v1","capabilities":["workflow_catalog.version_lifecycle.v1"]}`))
	}))
	defer server.Close()
	t.Setenv(EnvFleetDBURL, server.URL)

	handle, err := OpenStoreWithOptions(context.Background(), t.TempDir(), nil, OpenStoreOptions{
		RequiredFleetDBCapabilities: []string{"workflow_catalog.version_lifecycle.v1"},
	})
	if err != nil {
		t.Fatalf("OpenStoreWithOptions: %v", err)
	}
	if handle.FleetDBClient() == nil {
		t.Fatal("shared FleetDB client is nil")
	}
	if handle.Store != handle.FleetDBClient() {
		t.Fatal("legacy Store and capability composition do not share one FleetDB client")
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("capability probe calls = %d, want 1", got)
	}
}

func TestOpenStoreCloudSurfacesTypedCapabilityIncompatibility(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	t.Setenv(EnvFleetDBURL, server.URL)

	handle, err := OpenStoreWithOptions(context.Background(), t.TempDir(), nil, OpenStoreOptions{
		RequiredFleetDBCapabilities: []string{"workflow_catalog.version_lifecycle.v1"},
	})
	if handle != nil {
		_ = handle.Close()
		t.Fatal("handle is non-nil for incompatible FleetDB deployment")
	}
	if err == nil {
		t.Fatal("OpenStoreWithOptions returned nil error")
	}
	var incompatibility *fleetdb.CapabilityIncompatibilityError
	if !errors.As(err, &incompatibility) {
		t.Fatalf("error = %v, want *fleetdb.CapabilityIncompatibilityError", err)
	}
	if incompatibility.Kind != fleetdb.CapabilityEndpointUnavailable {
		t.Fatalf("kind = %q, want %q", incompatibility.Kind, fleetdb.CapabilityEndpointUnavailable)
	}
	if !strings.Contains(err.Error(), "openstore: fleet-db compatibility: fleetdb: incompatible deployment") {
		t.Fatalf("error = %q, want startup compatibility context", err)
	}
}

func TestOpenStoreLocalRecoversOperationalCapabilityRaceOnReusedRuntime(t *testing.T) {
	t.Setenv(EnvFleetDBURL, "")
	t.Setenv(EnvFleetDBBin, filepath.Join(t.TempDir(), "missing-fleet-db"))
	dataDir := t.TempDir()
	serviceCredential, err := authority.LoadOrCreateLocalFleetDBServiceCredential(embeddedFleetDBAuthDir(dataDir))
	if err != nil {
		t.Fatalf("create service credential: %v", err)
	}

	var capabilityCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case fleetdb.CapabilitiesAPIPath:
			if got := r.Header.Get("X-API-Key"); got != serviceCredential {
				t.Errorf("capability API key = %q, want persisted local service credential", got)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if capabilityCalls.Add(1) == 1 {
				http.Error(w, "shutting down", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"api_revision":"v1","capabilities":["workflow_catalog.version_lifecycle.v1"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	fleetDir := filepath.Join(dataDir, "fleet-db")
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{PID: os.Getpid(), URL: server.URL}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}

	handle, err := OpenStoreWithOptions(context.Background(), dataDir, nil, OpenStoreOptions{
		RequiredFleetDBCapabilities: []string{fleetdb.WorkflowCatalogVersionLifecycleCapability},
	})
	if err != nil {
		t.Fatalf("OpenStoreWithOptions: %v", err)
	}
	defer handle.Close()
	if !handle.reusedLocal {
		t.Fatal("recovered handle was not identified as a reused local runtime")
	}
	if got := capabilityCalls.Load(); got != 2 {
		t.Fatalf("capability calls = %d, want initial failure plus recovery probe", got)
	}
}

func TestOpenStoreLocalDoesNotRecoverTypedCapabilityIncompatibility(t *testing.T) {
	t.Setenv(EnvFleetDBURL, "")
	t.Setenv(EnvFleetDBBin, filepath.Join(t.TempDir(), "missing-fleet-db"))
	dataDir := t.TempDir()
	if _, err := authority.LoadOrCreateLocalFleetDBServiceCredential(embeddedFleetDBAuthDir(dataDir)); err != nil {
		t.Fatalf("create service credential: %v", err)
	}

	var capabilityCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case fleetdb.CapabilitiesAPIPath:
			capabilityCalls.Add(1)
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	fleetDir := filepath.Join(dataDir, "fleet-db")
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{PID: os.Getpid(), URL: server.URL}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}

	handle, err := OpenStoreWithOptions(context.Background(), dataDir, nil, OpenStoreOptions{
		RequiredFleetDBCapabilities: []string{fleetdb.WorkflowCatalogVersionLifecycleCapability},
	})
	if handle != nil {
		_ = handle.Close()
		t.Fatal("OpenStoreWithOptions returned a handle for an incompatible reused runtime")
	}
	var incompatibility *fleetdb.CapabilityIncompatibilityError
	if !errors.As(err, &incompatibility) {
		t.Fatalf("error = %v, want *fleetdb.CapabilityIncompatibilityError", err)
	}
	if got := capabilityCalls.Load(); got != 1 {
		t.Fatalf("capability calls = %d, want no recovery retry for typed incompatibility", got)
	}
}
