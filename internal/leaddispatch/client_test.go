package leaddispatch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/leadoccupant"
)

func TestClient_DispatchEpicRunSendsOnlyAllowedFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/workspaces/WS/lead/dispatch/epic-run" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer occupant-token" {
			t.Errorf("Authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		keys := make([]string, 0, len(body))
		for key := range body {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if want := []string{"epicId", "maxConcurrency", "runner"}; !reflect.DeepEqual(keys, want) {
			t.Errorf("body keys = %v, want %v", keys, want)
		}
		_, _ = io.WriteString(w, `{"success":true,"data":{"runId":"run-1","workflow":"epic-runner","epicId":"epic-1","status":"queued"}}`)
	}))
	t.Cleanup(server.Close)

	value := 3
	client := testClient(t, server.URL, "occupant-token")
	got, err := client.DispatchEpicRun(context.Background(), EpicRunRequest{
		EpicID: "epic-1", MaxConcurrency: &value, Runner: "daytona-task-runner",
	})
	if err != nil {
		t.Fatalf("DispatchEpicRun: %v", err)
	}
	if got.RunID != "run-1" || got.Workflow != "epic-runner" || got.Status != "queued" {
		t.Fatalf("dispatch = %+v", got)
	}
}

func TestClient_DispatchEpicRunOmitsEmptyOptionalFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if want := `{"epicId":"epic-1"}`; string(body) != want {
			t.Errorf("body = %s, want %s", body, want)
		}
		_, _ = io.WriteString(w, `{"success":true,"data":{"runId":"run-1","workflow":"epic-runner","epicId":"epic-1","status":"queued"}}`)
	}))
	t.Cleanup(server.Close)

	client := testClient(t, server.URL, "occupant-token")
	if _, err := client.DispatchEpicRun(context.Background(), EpicRunRequest{EpicID: "epic-1"}); err != nil {
		t.Fatalf("DispatchEpicRun: %v", err)
	}
}

func TestClient_BodyIsReplayableOn401(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	var calls atomic.Int64
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		if calls.Add(1) == 1 {
			if err := leadoccupant.WriteToken("refreshed-token"); err != nil {
				t.Errorf("WriteToken: %v", err)
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"success":false,"error":"expired","code":"token_expired"}`)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer refreshed-token" {
			t.Errorf("retry Authorization = %q", got)
		}
		_, _ = io.WriteString(w, `{"success":true,"data":{"runId":"run-1","workflow":"epic-runner","epicId":"epic-1","status":"queued"}}`)
	}))
	t.Cleanup(server.Close)

	client := testClient(t, server.URL, "initial-token")
	if _, err := client.DispatchEpicRun(context.Background(), EpicRunRequest{EpicID: "epic-1"}); err != nil {
		t.Fatalf("DispatchEpicRun: %v", err)
	}
	if len(bodies) != 2 || bodies[0] == "" || bodies[0] != bodies[1] {
		t.Fatalf("request bodies = %#v, want two identical full bodies", bodies)
	}
}

func TestClient_DecodesStructuredErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"success":false,"error":"cap missing","code":"cap_denied"}`)
	}))
	t.Cleanup(server.Close)
	client := testClient(t, server.URL, "token")
	_, err := client.RunStatus(context.Background(), "run-1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 || apiErr.Code != "cap_denied" || apiErr.Message != "cap missing" {
		t.Fatalf("error = %#v, want structured 403 cap_denied", err)
	}
	for status := 400; status <= 504; status++ {
		want := status == 429 || status == 502 || status == 503 || status == 504
		if got := (&APIError{Status: status}).Retryable(); got != want {
			t.Errorf("status %d Retryable = %t, want %t", status, got, want)
		}
	}
}

func TestClient_UnauthorizedMessage(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"success":false,"error":"expired","code":"token_expired"}`)
	}))
	t.Cleanup(server.Close)
	client := testClient(t, server.URL, "token")
	_, err := client.RunStatus(context.Background(), "run-1")
	if err == nil || err.Error() != leadoccupant.UnauthorizedMessage {
		t.Fatalf("error = %v, want %q", err, leadoccupant.UnauthorizedMessage)
	}
}

func TestNew_FailsClosedOnPartialEnv(t *testing.T) {
	t.Setenv(leadoccupant.EnvOccupantToken, "token")
	t.Setenv(leadoccupant.EnvLeadAPIURL, "")
	t.Setenv(leadoccupant.EnvWorkspace, "WS")
	_, err := New()
	if !errors.Is(err, leadoccupant.ErrIncompleteEnv) {
		t.Fatalf("New error = %v, want ErrIncompleteEnv", err)
	}
}

func testClient(t *testing.T, baseURL, token string) *Client {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	env := leadoccupant.Env{BaseURL: baseURL, Workspace: "WS", EnvToken: token}
	return &Client{baseURL: baseURL, workspace: "WS", doer: env.Transport()}
}
