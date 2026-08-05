package driver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

func TestDriverRuntimeClientUsesRunTokenWithoutLegacyIdentityHeaders(t *testing.T) {
	var request *http.Request
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		request = req.Clone(req.Context())
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"taskRunId": "task-run-1", "status": "queued"})
	}))
	t.Cleanup(server.Close)
	t.Setenv("LOOM_DRIVER_API_URL", server.URL)
	t.Setenv("LOOM_DRIVER_WORKSPACE", "WS")
	t.Setenv("LOOM_RUN_TOKEN", "run-token")
	t.Setenv("LOOM_DRIVER_API_TOKEN", "static-token")
	t.Setenv("LOOM_DRIVER_FENCING_TOKEN", "hostile-not-a-number")

	client, err := newDriverRuntimeClient(driverRuntimeClientOptions{
		DriverRunID: "run-header", NodeID: "node-header", LeaseID: "lease-header", FencingToken: 9,
	})
	if err != nil {
		t.Fatalf("newDriverRuntimeClient: %v", err)
	}
	var output map[string]any
	if err := client.call(context.Background(), "exec-task", map[string]any{"taskId": "TASK-1"}, &output); err != nil {
		t.Fatalf("call: %v", err)
	}
	if request == nil || request.URL.Path != "/api/workspaces/WS/driver/exec-task" {
		t.Fatalf("request path = %v", request)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer run-token" {
		t.Fatalf("Authorization = %q, want run token", got)
	}
	for _, header := range []string{driverRunIDHeader, driverNodeIDHeader, driverLeaseIDHeader, driverLeaseTokenHeader, driverFencingTokenHeader} {
		if got := request.Header.Get(header); got != "" {
			t.Fatalf("token-only request leaked %s=%q", header, got)
		}
	}
	if body["taskId"] != "TASK-1" || output["taskRunId"] != "task-run-1" {
		t.Fatalf("body/output = %v / %v", body, output)
	}
}

func TestDriverRuntimeClientPreservesLegacyHeaderQuadAndStaticBearer(t *testing.T) {
	var request *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		request = req.Clone(req.Context())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("LOOM_DRIVER_API_URL", server.URL)
	t.Setenv("LOOM_DRIVER_WORKSPACE", "WS")
	t.Setenv("LOOM_RUN_TOKEN", "")
	t.Setenv("LOOM_DRIVER_API_TOKEN", "static-token")

	client, err := newDriverRuntimeClient(driverRuntimeClientOptions{
		DriverRunID: "run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "driver-secret", FencingToken: 7,
	})
	if err != nil {
		t.Fatalf("newDriverRuntimeClient: %v", err)
	}
	if err := client.call(context.Background(), "complete-task", map[string]string{"taskRunId": "task-run-1"}, nil); err != nil {
		t.Fatalf("call: %v", err)
	}
	if request.Header.Get("Authorization") != "Bearer static-token" ||
		request.Header.Get(driverRunIDHeader) != "run-1" || request.Header.Get(driverNodeIDHeader) != "node-1" ||
		request.Header.Get(driverLeaseIDHeader) != "lease-1" || request.Header.Get(driverLeaseTokenHeader) != "driver-secret" ||
		request.Header.Get(driverFencingTokenHeader) != "7" {
		t.Fatalf("legacy headers = %v", request.Header)
	}
}

func TestDriverRuntimeClientFailsClosedWithoutExplicitEndpoint(t *testing.T) {
	t.Setenv("LOOM_DRIVER_API_URL", "")
	t.Setenv("LOOM_TASK_RUN_API_URL", "")
	if _, err := newDriverRuntimeClient(driverRuntimeClientOptions{WorkspaceKey: "WS", DriverRunID: "run-1"}); err == nil {
		t.Fatal("newDriverRuntimeClient succeeded without an explicit serve endpoint")
	}
}

func TestDriverRuntimeClientMapsStructuredDomainError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"not_owner","message":"wrong lease","retryable":false}}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("LOOM_DRIVER_API_URL", server.URL)
	t.Setenv("LOOM_DRIVER_WORKSPACE", "WS")
	t.Setenv("LOOM_RUN_TOKEN", "run-token")
	client, err := newDriverRuntimeClient(driverRuntimeClientOptions{})
	if err != nil {
		t.Fatalf("newDriverRuntimeClient: %v", err)
	}
	if err := client.call(context.Background(), "complete-task", map[string]string{}, nil); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("call error = %v, want ErrNotOwner", err)
	}
}

func TestDriverRuntimeClientVerifyRunUsesRunScopedEndpointAndValidatesIdentity(t *testing.T) {
	var request *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		request = req.Clone(req.Context())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workspace_key":"WS","run_id":"run-1","status":"running"}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("LOOM_DRIVER_API_URL", server.URL)
	t.Setenv("LOOM_DRIVER_WORKSPACE", "WS")
	t.Setenv("LOOM_RUN_TOKEN", "run-token")

	client, err := newDriverRuntimeClient(driverRuntimeClientOptions{DriverRunID: "run-1"})
	if err != nil {
		t.Fatalf("newDriverRuntimeClient: %v", err)
	}
	run, err := client.verifyRun(context.Background())
	if err != nil {
		t.Fatalf("verifyRun: %v", err)
	}
	if request == nil || request.URL.Path != "/api/workspaces/WS/driver/verify-run" {
		t.Fatalf("request = %v", request)
	}
	if request.Header.Get("Authorization") != "Bearer run-token" {
		t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
	}
	if run.RunID != "run-1" || run.WorkspaceKey != "WS" {
		t.Fatalf("run = %+v", run)
	}
}

func TestDriverRuntimeClientVerifyRunRejectsMismatchedResponseIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"workspace_key":"OTHER","run_id":"run-2","status":"running"}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("LOOM_DRIVER_API_URL", server.URL)
	t.Setenv("LOOM_DRIVER_WORKSPACE", "WS")
	t.Setenv("LOOM_RUN_TOKEN", "run-token")
	client, err := newDriverRuntimeClient(driverRuntimeClientOptions{DriverRunID: "run-1"})
	if err != nil {
		t.Fatalf("newDriverRuntimeClient: %v", err)
	}
	if _, err := client.verifyRun(context.Background()); !errors.Is(err, execution.ErrConflict) {
		t.Fatalf("verifyRun error = %v, want Execution conflict", err)
	}
}
