package managementapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
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
		{status: http.StatusBadRequest, want: persistence.ErrInvalid},
		{status: http.StatusNotFound, want: persistence.ErrNotFound},
		{status: http.StatusConflict, want: persistence.ErrConflict},
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
	if _, err := client.CreateWorkerProfile(context.Background(), execution.CreateWorkerProfileCommand{ProfileID: "  ", Role: "task"}); !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("blank create id error = %v, want ErrInvalid", err)
	}
	if _, err := client.UpdateWorkerProfile(context.Background(), "  ", execution.WorkerProfilePatch{}); !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("blank update id error = %v, want ErrInvalid", err)
	}
	if err := client.DeleteWorkerProfile(context.Background(), "  "); !errors.Is(err, persistence.ErrInvalid) {
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

func TestAgentIdentityManagementUsesIntentOnlyRoutesAndCAS(t *testing.T) {
	const timestamp = "2026-07-30T12:00:00Z"
	requests := 0
	client := &Client{
		serverURL: "http://127.0.0.1:8484", workspace: "space/name",
		doer: managementRoundTripper(func(request *http.Request) (*http.Response, error) {
			requests++
			if request.Header.Get("Authorization") != "" {
				t.Fatalf("Authorization = %q, want none in open mode", request.Header.Get("Authorization"))
			}
			response := &http.Response{Header: make(http.Header), StatusCode: http.StatusOK}
			agentJSON := `{"workspace_key":"space/name","agent_id":"docs","generation_id":"00112233445566778899aabbccddeeff","name":"Docs","kind":"maintenance","behavior":{"role_name":"docs"},"desired_state":"running","max_instances":1,"created_at":"` + timestamp + `","updated_at":"` + timestamp + `"}`
			switch requests {
			case 1:
				if request.Method != http.MethodPost || request.URL.EscapedPath() != "/api/workspaces/space%2Fname/agent-identities" {
					t.Fatalf("create request = %s %s", request.Method, request.URL.EscapedPath())
				}
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["agent_id"] != "docs" || body["workspace_key"] != nil || body["created_by"] != nil {
					t.Fatalf("create body = %v", body)
				}
				response.StatusCode = http.StatusCreated
				response.Body = io.NopCloser(strings.NewReader(agentJSON))
			case 2:
				if request.Method != http.MethodGet || request.URL.EscapedPath() != "/api/workspaces/space%2Fname/agent-identities/docs" {
					t.Fatalf("get request = %s %s", request.Method, request.URL.EscapedPath())
				}
				response.Body = io.NopCloser(strings.NewReader(agentJSON))
			case 3:
				if request.Method != http.MethodGet || request.URL.EscapedPath() != "/api/workspaces/space%2Fname/agent-identities" {
					t.Fatalf("list request = %s %s", request.Method, request.URL.EscapedPath())
				}
				response.Body = io.NopCloser(strings.NewReader(`{"agents":[` + agentJSON + `]}`))
			case 4:
				if request.Method != http.MethodPatch || request.URL.EscapedPath() != "/api/workspaces/space%2Fname/agent-identities/docs" {
					t.Fatalf("update request = %s %s", request.Method, request.URL.EscapedPath())
				}
				var body struct {
					ExpectedUpdatedAt time.Time         `json:"expected_updated_at"`
					Patch             agents.AgentPatch `json:"patch"`
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.ExpectedUpdatedAt.Format(time.RFC3339) != timestamp ||
					body.Patch.ProfileName == nil || *body.Patch.ProfileName != "reviewer" {
					t.Fatalf("update body = %+v", body)
				}
				response.Body = io.NopCloser(strings.NewReader(agentJSON))
			case 5:
				if request.Method != http.MethodPost || request.URL.EscapedPath() != "/api/workspaces/space%2Fname/agent-identities/docs/desired-state" {
					t.Fatalf("desired-state request = %s %s", request.Method, request.URL.EscapedPath())
				}
				var body SetAgentDesiredStateRequest
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.ExpectedState != agents.DesiredRunning || body.DesiredState != agents.DesiredStopped ||
					body.ExpectedUpdatedAt.Format(time.RFC3339) != timestamp {
					t.Fatalf("desired-state body = %+v", body)
				}
				response.Body = io.NopCloser(strings.NewReader(strings.Replace(agentJSON, `"desired_state":"running"`, `"desired_state":"stopped"`, 1)))
			case 6:
				if request.Method != http.MethodPost || request.URL.EscapedPath() != "/api/workspaces/space%2Fname/agent-identities/docs/archive" {
					t.Fatalf("archive request = %s %s", request.Method, request.URL.EscapedPath())
				}
				var body ArchiveAgentRequest
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.ExpectedUpdatedAt.Format(time.RFC3339) != timestamp {
					t.Fatalf("archive body = %+v", body)
				}
				response.Body = io.NopCloser(strings.NewReader(strings.Replace(agentJSON, `"created_at":`, `"deleted_at":"`+timestamp+`","created_at":`, 1)))
			default:
				t.Fatalf("unexpected request %d", requests)
			}
			return response, nil
		}),
	}
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	if _, err := client.CreateAgent(context.Background(), agents.CreateAgentCommand{
		WorkspaceKey: "space/name", AgentID: "docs", Name: "Docs",
		Kind: agents.AgentKindMaintenance, Behavior: agents.BehaviorReference{RoleName: "docs"},
		DesiredState: agents.DesiredRunning, MaxInstances: 1,
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := client.GetAgent(context.Background(), "docs"); err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if values, err := client.ListAgents(context.Background()); err != nil || len(values) != 1 {
		t.Fatalf("ListAgents = %d, %v", len(values), err)
	}
	profile := "reviewer"
	if _, err := client.UpdateAgent(context.Background(), "docs", now, agents.AgentPatch{
		ProfileName: &profile,
	}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	if _, err := client.SetAgentDesiredState(context.Background(), "docs", SetAgentDesiredStateRequest{
		ExpectedState: agents.DesiredRunning, DesiredState: agents.DesiredStopped, ExpectedUpdatedAt: now,
	}); err != nil {
		t.Fatalf("SetAgentDesiredState: %v", err)
	}
	if _, err := client.ArchiveAgent(context.Background(), "docs", now); err != nil {
		t.Fatalf("ArchiveAgent: %v", err)
	}
}

func TestAgentIdentityManagementRejectsWorkspaceMismatchBeforeTransport(t *testing.T) {
	requests := 0
	client := &Client{
		serverURL: "http://127.0.0.1:8484", workspace: "WS",
		doer: managementRoundTripper(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, errors.New("unexpected transport")
		}),
	}
	if _, err := client.CreateAgent(context.Background(), agents.CreateAgentCommand{
		WorkspaceKey: "OTHER", AgentID: "docs",
	}); !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("CreateAgent workspace mismatch error = %v, want ErrInvalid", err)
	}
	if requests != 0 {
		t.Fatalf("workspace mismatch reached transport %d times", requests)
	}
}

func TestAgentLifecycleManagementUsesAtomicIntentRoute(t *testing.T) {
	const timestamp = "2026-07-30T12:00:00Z"
	requests := 0
	client := &Client{
		serverURL: "http://127.0.0.1:8484", workspace: "space/name",
		doer: managementRoundTripper(func(request *http.Request) (*http.Response, error) {
			requests++
			if request.Method != http.MethodPost ||
				request.URL.EscapedPath() != "/api/workspaces/space%2Fname/agent-identities/docs/lifecycle" {
				t.Fatalf("lifecycle request = %s %s", request.Method, request.URL.EscapedPath())
			}
			var body ApplyAgentLifecycleRequest
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Action != agents.LifecycleDisable ||
				body.ExpectedGenerationID != "00112233445566778899aabbccddeeff" ||
				body.IdempotencyKey != "agentdef-disable-1" {
				t.Fatalf("lifecycle body = %+v", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
				"workspace_key":"space/name",
				"agent_id":"docs",
				"idempotency_key":"agentdef-disable-1",
				"action":"disable",
				"agent":{
					"workspace_key":"space/name",
					"agent_id":"docs",
					"generation_id":"00112233445566778899aabbccddeeff",
					"name":"Docs",
					"kind":"maintenance",
					"behavior":{"role_name":"docs"},
					"desired_state":"paused",
					"max_instances":1,
					"created_at":"` + timestamp + `",
					"updated_at":"` + timestamp + `"
				},
				"committed_at":"` + timestamp + `"
			}`)),
			}, nil
		}),
	}
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	result, err := client.ApplyAgentLifecycle(context.Background(), "docs", ApplyAgentLifecycleRequest{
		Action: agents.LifecycleDisable, ExpectedUpdatedAt: now,
		ExpectedGenerationID: "00112233445566778899aabbccddeeff",
		IdempotencyKey:       "agentdef-disable-1",
	})
	if err != nil {
		t.Fatalf("ApplyAgentLifecycle: %v", err)
	}
	if requests != 1 || result.Agent == nil || result.Agent.DesiredState != agents.DesiredPaused {
		t.Fatalf("requests/result = %d/%+v", requests, result)
	}
}

func TestAgentLifecycleManagementRejectsMalformedGenerationBeforeTransport(t *testing.T) {
	requests := 0
	client := &Client{
		serverURL: "http://127.0.0.1:8484", workspace: "WS",
		doer: managementRoundTripper(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, errors.New("unexpected transport")
		}),
	}
	_, err := client.ApplyAgentLifecycle(context.Background(), "docs", ApplyAgentLifecycleRequest{
		Action:               agents.LifecycleDisable,
		ExpectedUpdatedAt:    time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
		ExpectedGenerationID: "CALLER-SELECTED",
		IdempotencyKey:       "agentdef-disable-1",
	})
	if !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("malformed generation error = %v, want ErrInvalid", err)
	}
	if requests != 0 {
		t.Fatalf("malformed generation reached transport %d times", requests)
	}
}

func TestAgentLifecycleManagementRejectsDifferentResponseGeneration(t *testing.T) {
	client := &Client{
		serverURL: "http://127.0.0.1:8484", workspace: "WS",
		doer: managementRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
					"workspace_key":"WS",
					"agent_id":"docs",
					"idempotency_key":"agentdef-disable-1",
					"action":"disable",
					"agent":{
						"workspace_key":"WS",
						"agent_id":"docs",
						"generation_id":"ffeeddccbbaa99887766554433221100",
						"name":"Docs",
						"kind":"maintenance",
						"behavior":{"role_name":"docs"},
						"desired_state":"paused",
						"max_instances":1,
						"created_at":"2026-07-30T12:00:00Z",
						"updated_at":"2026-07-30T12:00:00Z"
					},
					"committed_at":"2026-07-30T12:00:00Z"
				}`)),
			}, nil
		}),
	}
	_, err := client.ApplyAgentLifecycle(context.Background(), "docs", ApplyAgentLifecycleRequest{
		Action:               agents.LifecycleDisable,
		ExpectedUpdatedAt:    time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
		ExpectedGenerationID: "00112233445566778899aabbccddeeff",
		IdempotencyKey:       "agentdef-disable-1",
	})
	if err == nil || !strings.Contains(err.Error(), "wrong Agent generation") {
		t.Fatalf("different response generation error = %v", err)
	}
}
