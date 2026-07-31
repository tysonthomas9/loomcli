package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestOpenStoreLocalRecoveredOwnedRuntimeOutlivesNegotiation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("helper launcher uses a POSIX shell")
	}
	t.Setenv(EnvFleetDBURL, "")
	t.Setenv("GO_WANT_FLEETDB_CAPABILITY_HELPER", "1")
	dataDir := t.TempDir()
	if _, err := authority.LoadOrCreateLocalFleetDBServiceCredential(embeddedFleetDBAuthDir(dataDir)); err != nil {
		t.Fatalf("create service credential: %v", err)
	}

	testBinary, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	helperPath := filepath.Join(t.TempDir(), "fleet-db")
	helperScript := fmt.Sprintf(`#!/bin/sh
if [ "${1:-}" = "--help" ]; then
  echo "fleet-db test server help"
  exit 0
fi
exec %q -test.run=^TestFleetDBCapabilityHelperProcess$
`, testBinary)
	if err := os.WriteFile(helperPath, []byte(helperScript), 0755); err != nil {
		t.Fatalf("write helper launcher: %v", err)
	}
	t.Setenv(EnvFleetDBBin, helperPath)

	var unavailable atomic.Bool
	oldRuntime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			if unavailable.Load() {
				http.Error(w, "shutting down", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		case fleetdb.CapabilitiesAPIPath:
			unavailable.Store(true)
			http.Error(w, "shutting down", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer oldRuntime.Close()

	fleetDir := filepath.Join(dataDir, "fleet-db")
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{PID: os.Getpid(), URL: oldRuntime.URL}); err != nil {
		t.Fatalf("write old runtime: %v", err)
	}

	handle, err := OpenStoreWithOptions(context.Background(), dataDir, nil, OpenStoreOptions{
		RequiredFleetDBCapabilities: []string{fleetdb.WorkflowCatalogVersionLifecycleCapability},
	})
	if err != nil {
		t.Fatalf("OpenStoreWithOptions: %v", err)
	}
	defer handle.Close()
	if handle.reusedLocal || handle.embedded == nil {
		t.Fatalf("recovered handle ownership = reused %t, embedded %v; want owned replacement", handle.reusedLocal, handle.embedded != nil)
	}

	// recoverLocalStore cancels its negotiation context when it returns. Give
	// that cancellation time to propagate, then prove the owned replacement is
	// still serving capabilities under the caller's service lifetime.
	time.Sleep(100 * time.Millisecond)
	if err := requireFleetDBCapabilities(context.Background(), handle, []string{fleetdb.WorkflowCatalogVersionLifecycleCapability}); err != nil {
		t.Fatalf("recovered owned runtime did not outlive negotiation: %v", err)
	}
}

func TestFleetDBCapabilityHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_FLEETDB_CAPABILITY_HELPER") != "1" {
		return
	}
	addr := os.Getenv("FLEET_SERVER_ADDR")
	if addr == "" {
		t.Fatal("FLEET_SERVER_ADDR is empty")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc(fleetdb.CapabilitiesAPIPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_revision":"v1","capabilities":["workflow_catalog.version_lifecycle.v1"]}`))
	})
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: time.Second}
	if err := server.ListenAndServe(); err != nil {
		t.Fatalf("serve FleetDB helper: %v", err)
	}
}
