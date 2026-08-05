package fleetdb

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestWorkflowCatalogTransportUsesAtomicLifecycleRoutes(t *testing.T) {
	for _, test := range []struct {
		name string
		call func(context.Context, WorkflowCatalogTransport) (*WorkflowCatalogLifecycleResult, error)
	}{
		{name: "approve", call: func(ctx context.Context, transport WorkflowCatalogTransport) (*WorkflowCatalogLifecycleResult, error) {
			return transport.ApproveVersion(ctx, "TEST", "driver/one", "version one", 7)
		}},
		{name: "unapprove", call: func(ctx context.Context, transport WorkflowCatalogTransport) (*WorkflowCatalogLifecycleResult, error) {
			return transport.UnapproveVersion(ctx, "TEST", "driver/one", "version one", 7)
		}},
		{name: "activate", call: func(ctx context.Context, transport WorkflowCatalogTransport) (*WorkflowCatalogLifecycleResult, error) {
			return transport.ActivateVersion(ctx, "TEST", "driver/one", "version one", 7)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wantPath := "/api/v1/TEST/drivers/driver%2Fone/versions/version%20one/" + test.name
				if r.Method != http.MethodPost || r.URL.EscapedPath() != wantPath {
					t.Errorf("request = %s %s, want POST %s", r.Method, r.URL.EscapedPath(), wantPath)
				}
				var body struct {
					ExpectedRevision uint64 `json:"expected_revision"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedRevision != 7 {
					t.Errorf("body = %+v, err=%v", body, err)
				}
				_ = json.NewEncoder(w).Encode(WorkflowCatalogLifecycleResult{
					CommittedRevision: 8,
					SemanticImpact:    "workflow_catalog.version_trust_changed.v1",
					Replayed:          true,
					Driver:            &domain.Driver{WorkspaceKey: "TEST", DriverID: "driver/one", Revision: 9},
					Version:           &domain.DriverVersion{WorkspaceKey: "TEST", DriverID: "driver/one", VersionID: "version one"},
				})
			}))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			result, err := test.call(context.Background(), client.WorkflowCatalog())
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if result.CommittedRevision != 8 || !result.Replayed || result.Driver.Revision != 9 {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestWorkflowCatalogTransportReusesClientSurface(t *testing.T) {
	client, err := New(Config{BaseURL: "http://fleetdb.example.test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first := client.WorkflowCatalog()
	if first == nil || first != client.WorkflowCatalog() {
		t.Fatalf("WorkflowCatalog returned different transport surfaces: first=%p second=%p", first, client.WorkflowCatalog())
	}
}

func TestWorkflowCatalogTransportRejectsUnadvanceableRevisionBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(WorkflowCatalogLifecycleResult{})
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	transport := client.WorkflowCatalog()
	if _, err := transport.ApproveVersion(context.Background(), "TEST", "driver", "version", uint64(math.MaxInt64)-1); err != nil {
		t.Fatalf("ApproveVersion(max advanceable revision): %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("max advanceable requests = %d, want 1", got)
	}

	for _, test := range []struct {
		name string
		call func() (*WorkflowCatalogLifecycleResult, error)
	}{
		{name: "approve", call: func() (*WorkflowCatalogLifecycleResult, error) {
			return transport.ApproveVersion(context.Background(), "TEST", "driver", "version", uint64(math.MaxInt64))
		}},
		{name: "unapprove", call: func() (*WorkflowCatalogLifecycleResult, error) {
			return transport.UnapproveVersion(context.Background(), "TEST", "driver", "version", uint64(math.MaxInt64))
		}},
		{name: "activate", call: func() (*WorkflowCatalogLifecycleResult, error) {
			return transport.ActivateVersion(context.Background(), "TEST", "driver", "version", uint64(math.MaxInt64))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := requests.Load()
			if _, err := test.call(); !errors.Is(err, ErrWorkflowCatalogInvalid) {
				t.Fatalf("error = %v, want errors.Is ErrWorkflowCatalogInvalid", err)
			}
			if got := requests.Load(); got != before {
				t.Fatalf("invalid revision issued HTTP request: before=%d after=%d", before, got)
			}
		})
	}
}

func TestWorkflowCatalogTransportPreservesMachineReadableFailures(t *testing.T) {
	for _, test := range []struct {
		code   string
		status int
		want   error
	}{
		{code: "revision_conflict", status: http.StatusConflict, want: ErrWorkflowCatalogRevisionConflict},
		{code: "workflow_catalog_version_ownership", status: http.StatusBadRequest, want: ErrWorkflowCatalogVersionOwnership},
		{code: "workflow_catalog_version_not_validated", status: http.StatusUnprocessableEntity, want: ErrWorkflowCatalogVersionNotValidated},
		{code: "workflow_catalog_version_not_approved", status: http.StatusUnprocessableEntity, want: ErrWorkflowCatalogVersionNotApproved},
	} {
		t.Run(test.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": test.code, "message": test.code}})
			}))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = client.WorkflowCatalog().ApproveVersion(context.Background(), "TEST", "driver", "version", 1)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want errors.Is %v", err, test.want)
			}
		})
	}
}
