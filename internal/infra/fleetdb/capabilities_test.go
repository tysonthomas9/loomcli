package fleetdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

func TestRequireCapabilitiesEmptyRequirementsSkipProbe(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected capability probe", http.StatusInternalServerError)
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, required := range [][]string{nil, {}, {"", "  "}} {
		if err := client.RequireCapabilities(context.Background(), required); err != nil {
			t.Fatalf("RequireCapabilities(%q): %v", required, err)
		}
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("capability probe calls = %d, want 0", got)
	}
}

func TestPhase4FoundationCapabilitiesIncludesWorkItemProfiles(t *testing.T) {
	t.Parallel()

	got := Phase4FoundationCapabilities()
	if len(got) != 19 {
		t.Fatalf("Phase4FoundationCapabilities length = %d, want 19", len(got))
	}
	found := map[string]bool{}
	for _, capability := range got {
		found[capability] = true
	}
	for _, capability := range []string{
		WorkItemsRepositoryRequirementCapability,
		ExecutionDriverRunWorkItemClaimCapability,
		ExecutionDriverRunReviewWorkItemHandoffCapability,
		ExecutionTaskRunWorkItemDesignCapability,
		ExecutionTaskRunTerminalConvergenceCapability,
		ExecutionTerminalDriverRunWorkRecoveryCapability,
		ExecutionTerminalDriverRunWorkRecoveryQueueCapability,
	} {
		if !found[capability] {
			t.Fatalf("Phase4FoundationCapabilities = %q, missing %q", got, capability)
		}
	}
}

func TestPhase5FoundationCapabilitiesAreExact(t *testing.T) {
	t.Parallel()

	want := []string{
		AgentsServiceCommandsCapability,
		AgentsLifecycleCommandsCapability,
		AgentsOwnershipLeaseCommandsCapability,
		InteractionSessionCommandsCapability,
		RepositoriesAdmissionCapability,
	}
	if got := Phase5FoundationCapabilities(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Phase5FoundationCapabilities = %q, want %q", got, want)
	}
}

func TestRequireCapabilitiesAcceptsNormalizedAdvertisedKeys(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != CapabilitiesAPIPath {
			t.Errorf("request = %s %s, want GET %s", r.Method, r.URL.Path, CapabilitiesAPIPath)
		}
		if got := r.Header.Get("X-Actor"); got != "capability-test" {
			t.Errorf("X-Actor = %q, want capability-test", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"api_revision":"v1",
			"capabilities":[" beta.v1 ","alpha.v1","alpha.v1",""]
		}`))
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, Actor: "capability-test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.RequireCapabilities(context.Background(), []string{"beta.v1", " alpha.v1 ", "beta.v1"}); err != nil {
		t.Fatalf("RequireCapabilities: %v", err)
	}
}

func TestRequireCapabilitiesReturnsTypedIncompatibilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		status       int
		body         string
		wantKind     CapabilityIncompatibilityKind
		wantRevision string
		wantMissing  []string
		wantError    string
		wantNotFound bool
	}{
		{
			name:         "endpoint unavailable",
			status:       http.StatusNotFound,
			body:         `{"error":{"message":"not found"}}`,
			wantKind:     CapabilityEndpointUnavailable,
			wantError:    "fleetdb: incompatible deployment: capabilities endpoint /api/v1/capabilities is unavailable (HTTP 404); required capabilities: alpha.v1, beta.v1",
			wantNotFound: true,
		},
		{
			name:         "unsupported revision",
			status:       http.StatusOK,
			body:         `{"api_revision":"v2","capabilities":["alpha.v1","beta.v1"]}`,
			wantKind:     CapabilityRevisionUnsupported,
			wantRevision: "v2",
			wantError:    "fleetdb: incompatible deployment: capabilities API revision \"v2\" is unsupported (want \"v1\"); required capabilities: alpha.v1, beta.v1",
		},
		{
			name:         "missing keys",
			status:       http.StatusOK,
			body:         `{"api_revision":"v1","capabilities":["beta.v1"]}`,
			wantKind:     CapabilityKeysMissing,
			wantRevision: "v1",
			wantMissing:  []string{"alpha.v1"},
			wantError:    "fleetdb: incompatible deployment: capabilities API revision \"v1\" is missing required capabilities: alpha.v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client, err := New(Config{BaseURL: server.URL})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			err = client.RequireCapabilities(context.Background(), []string{" beta.v1 ", "alpha.v1", "alpha.v1"})
			if err == nil {
				t.Fatal("RequireCapabilities returned nil, want incompatibility")
			}
			if err.Error() != tt.wantError {
				t.Fatalf("error = %q, want %q", err, tt.wantError)
			}
			var incompatibility *CapabilityIncompatibilityError
			if !errors.As(err, &incompatibility) {
				t.Fatalf("error type = %T, want *CapabilityIncompatibilityError", err)
			}
			if incompatibility.Kind != tt.wantKind {
				t.Errorf("kind = %q, want %q", incompatibility.Kind, tt.wantKind)
			}
			if incompatibility.APIRevision != tt.wantRevision {
				t.Errorf("APIRevision = %q, want %q", incompatibility.APIRevision, tt.wantRevision)
			}
			if want := []string{"alpha.v1", "beta.v1"}; !reflect.DeepEqual(incompatibility.Required, want) {
				t.Errorf("Required = %q, want %q", incompatibility.Required, want)
			}
			if !reflect.DeepEqual(incompatibility.Missing, tt.wantMissing) {
				t.Errorf("Missing = %q, want %q", incompatibility.Missing, tt.wantMissing)
			}
			if got := errors.Is(err, persistence.ErrNotFound); got != tt.wantNotFound {
				t.Errorf("errors.Is(ErrNotFound) = %v, want %v", got, tt.wantNotFound)
			}
		})
	}
}

func TestRequireCapabilitiesDoesNotClassifyOperationalFailureAsIncompatibility(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = client.RequireCapabilities(context.Background(), []string{"alpha.v1"})
	if err == nil {
		t.Fatal("RequireCapabilities returned nil, want operational error")
	}
	var incompatibility *CapabilityIncompatibilityError
	if errors.As(err, &incompatibility) {
		t.Fatalf("error = %v, must not be a deployment incompatibility", err)
	}
	if !strings.Contains(err.Error(), "check required capabilities alpha.v1") || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("error = %q, want capability context and HTTP 503", err)
	}
}
