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

func TestWorkflowCatalogTransportUsesExactAtomicAuthoringRoutesAndDelegatedActor(t *testing.T) {
	baseInput := WorkflowCatalogAuthorVersionInput{
		WorkspaceKey: "TEST/one", DriverID: "driver/one", DelegatedActor: "operator:alice",
		RequestID: "request-1", ExpectedRevision: 7,
		DriverName: "Driver one", VersionID: "driver-one-v-abc",
		SourceRef:    "api://workflows/driver-one/versions/source",
		SourceDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BundleRef:    ".loom/drivers/driver-one/driver-one-v-abc",
		BundleDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Runtime:      "flue-node", Manifest: map[string]string{"entrypoint": "run"},
		BuildDiagnostics: "built",
	}
	for _, test := range []struct {
		name       string
		pathSuffix string
		managed    bool
		activate   bool
	}{
		{name: "operator", pathSuffix: "author"},
		{name: "managed", pathSuffix: "author-managed", managed: true, activate: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wantPath := "/api/v1/TEST%2Fone/drivers/driver%2Fone/versions/" + test.pathSuffix
				if r.Method != http.MethodPost || r.URL.EscapedPath() != wantPath {
					t.Errorf("request = %s %s, want POST %s", r.Method, r.URL.EscapedPath(), wantPath)
				}
				if got := r.Header.Get(FleetDelegatedActorHeader); got != baseInput.DelegatedActor {
					t.Errorf("%s = %q, want %q", FleetDelegatedActorHeader, got, baseInput.DelegatedActor)
				}
				if got := r.Header.Get("X-Actor"); got != "loom-service" {
					t.Errorf("service X-Actor = %q, want loom-service", got)
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode authoring body: %v", err)
				}
				wantKeys := []string{
					"request_id", "expected_revision", "driver_name", "version_id",
					"source_ref", "source_digest", "bundle_ref", "bundle_digest",
					"runtime", "manifest", "build_diagnostics",
				}
				if test.managed {
					wantKeys = append(wantKeys, "activate")
				}
				if len(body) != len(wantKeys) {
					t.Errorf("body keys = %v, want exactly %v", body, wantKeys)
				}
				for _, key := range wantKeys {
					if _, ok := body[key]; !ok {
						t.Errorf("body missing %q: %v", key, body)
					}
				}
				for _, forbidden := range []string{
					"workspace_key", "driver_id", "delegated_actor", "managed", "created_by",
					"trust_level", "owner_type", "status", "active_version_id",
				} {
					if _, ok := body[forbidden]; ok {
						t.Errorf("body exposed server/path-owned field %q: %v", forbidden, body)
					}
				}
				wantValues := map[string]any{
					"request_id": baseInput.RequestID, "expected_revision": baseInput.ExpectedRevision,
					"driver_name": baseInput.DriverName, "version_id": baseInput.VersionID,
					"source_ref": baseInput.SourceRef, "source_digest": baseInput.SourceDigest,
					"bundle_ref": baseInput.BundleRef, "bundle_digest": baseInput.BundleDigest,
					"runtime": baseInput.Runtime, "manifest": baseInput.Manifest,
					"build_diagnostics": baseInput.BuildDiagnostics,
				}
				if test.managed {
					wantValues["activate"] = test.activate
				}
				for key, want := range wantValues {
					wantJSON, err := json.Marshal(want)
					if err != nil {
						t.Fatalf("marshal expected %s: %v", key, err)
					}
					if got := string(body[key]); got != string(wantJSON) {
						t.Errorf("body[%q] = %s, want %s", key, got, wantJSON)
					}
				}
				var activate bool
				if raw, ok := body["activate"]; ok {
					if err := json.Unmarshal(raw, &activate); err != nil {
						t.Errorf("decode activate: %v", err)
					}
				}
				if activate != test.activate {
					t.Errorf("activate = %v, want %v", activate, test.activate)
				}
				_ = json.NewEncoder(w).Encode(WorkflowCatalogAuthorVersionResult{
					Driver: &domain.Driver{
						WorkspaceKey: baseInput.WorkspaceKey, DriverID: baseInput.DriverID, Revision: 8,
					},
					Version: &domain.DriverVersion{
						WorkspaceKey: baseInput.WorkspaceKey, DriverID: baseInput.DriverID,
						VersionID: baseInput.VersionID,
					},
					CreatedDriver: true, CreatedVersion: true, Activated: test.activate,
					Replayed: true, CommittedRevision: 8,
					SemanticImpact: "workflow_catalog.version_authored.v1",
				})
			}))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL, Actor: "loom-service"})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			var result *WorkflowCatalogAuthorVersionResult
			if test.managed {
				result, err = client.WorkflowCatalog().AuthorManagedDriverVersion(
					context.Background(),
					WorkflowCatalogAuthorManagedVersionInput{
						WorkflowCatalogAuthorVersionInput: baseInput,
						Activate:                          test.activate,
					},
				)
			} else {
				result, err = client.WorkflowCatalog().AuthorDriverVersion(context.Background(), baseInput)
			}
			if err != nil {
				t.Fatalf("author version: %v", err)
			}
			if result == nil || !result.CreatedDriver || !result.CreatedVersion ||
				!result.Replayed || result.Activated != test.activate ||
				result.CommittedRevision != 8 ||
				result.SemanticImpact != "workflow_catalog.version_authored.v1" {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestWorkflowCatalogAuthoringRejectsInvalidDelegatedActorAndRevisionBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(WorkflowCatalogAuthorVersionResult{})
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	input := WorkflowCatalogAuthorVersionInput{
		WorkspaceKey: "TEST", DriverID: "driver", DelegatedActor: " operator ",
	}
	if _, err := client.WorkflowCatalog().AuthorDriverVersion(context.Background(), input); !errors.Is(err, ErrWorkflowCatalogInvalid) {
		t.Fatalf("invalid actor err = %v, want ErrWorkflowCatalogInvalid", err)
	}
	input.DelegatedActor = "operator"
	input.ExpectedRevision = uint64(math.MaxInt64)
	if _, err := client.WorkflowCatalog().AuthorDriverVersion(context.Background(), input); !errors.Is(err, ErrWorkflowCatalogInvalid) {
		t.Fatalf("invalid revision err = %v, want ErrWorkflowCatalogInvalid", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("invalid authoring requests issued %d HTTP calls", got)
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
		{code: "workflow_catalog_authoring_conflict", status: http.StatusConflict, want: ErrWorkflowCatalogAuthoringConflict},
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
			if test.code == "workflow_catalog_authoring_conflict" {
				_, err = client.WorkflowCatalog().AuthorDriverVersion(context.Background(), WorkflowCatalogAuthorVersionInput{
					WorkspaceKey: "TEST", DriverID: "driver", DelegatedActor: "operator",
				})
			} else {
				_, err = client.WorkflowCatalog().ApproveVersion(context.Background(), "TEST", "driver", "version", 1)
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want errors.Is %v", err, test.want)
			}
		})
	}
}
