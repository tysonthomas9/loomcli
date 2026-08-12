package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
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
