package managementapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

type managementRoundTripper func(*http.Request) (*http.Response, error)

func (do managementRoundTripper) Do(request *http.Request) (*http.Response, error) {
	return do(request)
}

func TestSubmitDriverRunUsesWorkspaceManagementRouteWithoutOpenModeCredential(t *testing.T) {
	var captured SubmitDriverRunRequest
	client := &Client{
		serverURL: "http://127.0.0.1:8484",
		workspace: "space/name",
		doer: managementRoundTripper(func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodPost || request.URL.EscapedPath() != "/api/workspaces/space%2Fname/execution/driver-runs" {
				t.Fatalf("request = %s %s", request.Method, request.URL.EscapedPath())
			}
			if got := request.Header.Get("Authorization"); got != "" {
				t.Fatalf("Authorization = %q, want none in open mode", got)
			}
			if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusAccepted,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"workspace_key":"space/name","run_id":"run-1","driver_id":"driver-1","driver_version_id":"version-1","status":"queued"}`,
				)),
			}, nil
		}),
	}
	run, err := client.SubmitDriverRun(context.Background(), SubmitDriverRunRequest{
		CLICommand: "driver-run", DriverRef: "driver-1", DriverVersionID: "version-1", RunID: "run-1",
		IdempotencyKey: "request-1", Entrypoint: "run",
		EpicID: "epic-1", Payload: json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("SubmitDriverRun: %v", err)
	}
	if run.RunID != "run-1" || captured.DriverRef != "driver-1" || captured.IdempotencyKey != "request-1" ||
		captured.CLICommand != "driver-run" || string(captured.Payload) != `{"ok":true}` {
		t.Fatalf("response=%#v request=%#v", run, captured)
	}
}

func TestManagementStatusErrorPreservesDomainClass(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{status: http.StatusBadRequest, want: domain.ErrInvalid},
		{status: http.StatusNotFound, want: domain.ErrNotFound},
		{status: http.StatusConflict, want: domain.ErrConflict},
	}
	for _, test := range tests {
		err := statusError(test.status, []byte(`{"error":"denied"}`))
		if !errors.Is(err, test.want) {
			t.Errorf("statusError(%d) = %v, want %v", test.status, err, test.want)
		}
	}
}

func TestWorkerProfileMutationsUseOpenManagementRoutesWithoutCredentialOrWorkspaceBody(t *testing.T) {
	requests := 0
	client := &Client{
		serverURL: "http://127.0.0.1:8484", workspace: "space/name",
		doer: managementRoundTripper(func(request *http.Request) (*http.Response, error) {
			requests++
			if request.Header.Get("Authorization") != "" {
				t.Fatalf("Authorization = %q, want none in open mode", request.Header.Get("Authorization"))
			}
			response := &http.Response{Header: make(http.Header)}
			switch requests {
			case 1:
				if request.Method != http.MethodPost || request.URL.EscapedPath() != "/api/workspaces/space%2Fname/execution/worker-profiles" {
					t.Fatalf("create request = %s %s", request.Method, request.URL.EscapedPath())
				}
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["profile_id"] != "falcon" || body["role"] != "task" || body["workspace_key"] != nil || body["request_id"] != nil {
					t.Fatalf("create body = %v", body)
				}
				response.StatusCode = http.StatusCreated
				response.Body = io.NopCloser(strings.NewReader(`{"workspace_key":"space/name","profile_id":"falcon","role":"task","enabled":true}`))
			case 2:
				if request.Method != http.MethodPatch || request.URL.EscapedPath() != "/api/workspaces/space%2Fname/execution/worker-profiles/falcon" {
					t.Fatalf("update request = %s %s", request.Method, request.URL.EscapedPath())
				}
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["backend"] != "codex" || body["workspace_key"] != nil {
					t.Fatalf("update body = %v", body)
				}
				response.StatusCode = http.StatusOK
				response.Body = io.NopCloser(strings.NewReader(`{"workspace_key":"space/name","profile_id":"falcon","role":"task","backend":"codex","enabled":true}`))
			case 3:
				if request.Method != http.MethodDelete || request.URL.EscapedPath() != "/api/workspaces/space%2Fname/execution/worker-profiles/falcon" || request.Body != nil {
					t.Fatalf("delete request = %s %s body=%v", request.Method, request.URL.EscapedPath(), request.Body)
				}
				response.StatusCode = http.StatusNoContent
				response.Body = io.NopCloser(strings.NewReader(""))
			default:
				t.Fatalf("unexpected request %d", requests)
			}
			return response, nil
		}),
	}
	if _, err := client.CreateWorkerProfile(context.Background(), execution.CreateWorkerProfileCommand{
		WorkspaceKey: "hostile", RequestID: "hostile", ProfileID: "falcon", Role: "task",
	}); err != nil {
		t.Fatalf("CreateWorkerProfile: %v", err)
	}
	backend := "codex"
	if _, err := client.UpdateWorkerProfile(context.Background(), "falcon", execution.WorkerProfilePatch{Backend: &backend}); err != nil {
		t.Fatalf("UpdateWorkerProfile: %v", err)
	}
	if err := client.DeleteWorkerProfile(context.Background(), "falcon"); err != nil {
		t.Fatalf("DeleteWorkerProfile: %v", err)
	}
}

func TestWorkerProfileMutationsFailClosedOnBlankOrMismatchedIdentity(t *testing.T) {
	requests := 0
	client := &Client{
		serverURL: "http://127.0.0.1:8484", workspace: "WS",
		doer: managementRoundTripper(func(_ *http.Request) (*http.Response, error) {
			requests++
			return &http.Response{
				StatusCode: http.StatusCreated, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"workspace_key":"WS","profile_id":"other","role":"task","enabled":true}`)),
			}, nil
		}),
	}
	if _, err := client.CreateWorkerProfile(context.Background(), execution.CreateWorkerProfileCommand{ProfileID: "falcon", Role: "task"}); err == nil {
		t.Fatal("mismatched create response identity was accepted")
	}
	if requests != 1 {
		t.Fatalf("mismatched create requests = %d, want 1", requests)
	}
	if _, err := client.CreateWorkerProfile(context.Background(), execution.CreateWorkerProfileCommand{ProfileID: "  ", Role: "task"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("blank create id error = %v, want ErrInvalid", err)
	}
	if _, err := client.UpdateWorkerProfile(context.Background(), "  ", execution.WorkerProfilePatch{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("blank update id error = %v, want ErrInvalid", err)
	}
	if err := client.DeleteWorkerProfile(context.Background(), "  "); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("blank delete id error = %v, want ErrInvalid", err)
	}
	if requests != 1 {
		t.Fatalf("blank identity reached management transport; requests = %d", requests)
	}
}

func TestWorkerProfileManagementAuthorizationFailureHasNoFallback(t *testing.T) {
	requests := 0
	client := &Client{
		serverURL: "http://127.0.0.1:8484", workspace: "WS",
		doer: managementRoundTripper(func(_ *http.Request) (*http.Response, error) {
			requests++
			return &http.Response{
				StatusCode: http.StatusForbidden, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"code":"forbidden","error":"operator denied"}`)),
			}, nil
		}),
	}
	if _, err := client.CreateWorkerProfile(context.Background(), execution.CreateWorkerProfileCommand{ProfileID: "falcon", Role: "task"}); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("forbidden create error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("forbidden create transport calls = %d, want exactly 1", requests)
	}
}
