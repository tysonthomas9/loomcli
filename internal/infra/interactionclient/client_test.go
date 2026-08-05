package interactionclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func completeEnvelope() map[string]string {
	return map[string]string{
		interaction.EnvSessionWorkspace:  "WS",
		interaction.EnvSessionID:         "session-1",
		interaction.EnvSessionAgentID:    "lead-1",
		interaction.EnvSessionTerminalID: "terminal-1",
		interaction.EnvSessionNodeID:     "node-1",
		interaction.EnvSessionLeaseID:    "lease-1",
		interaction.EnvSessionFence:      "7",
		interaction.EnvSessionToken:      testToken,
		interaction.EnvInteractionAPIURL: "http://127.0.0.1:8484",
	}
}

func TestClientPublishesTranscriptWithScopedProofHeaders(t *testing.T) {
	env := completeEnvelope()
	content := []byte("{\"seq\":1,\"role\":\"user\",\"text\":\"hello\"}\n")
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.Path != "/api/workspaces/WS/interaction/sessions/session-1/transcript" {
			t.Fatalf("request = %s %s", req.Method, req.URL.Path)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(body, content) || req.Header.Get("Content-Type") != "application/x-ndjson" {
			t.Fatalf("transcript request content = %q/%q", body, req.Header.Get("Content-Type"))
		}
		for name, want := range map[string]string{
			sessionTokenHeader:    testToken,
			sessionAgentHeader:    "lead-1",
			sessionTerminalHeader: "terminal-1",
			sessionNodeHeader:     "node-1",
			sessionLeaseHeader:    "lease-1",
			sessionFenceHeader:    "7",
		} {
			if got := req.Header.Get(name); got != want {
				t.Fatalf("header %s = %q, want %q", name, got, want)
			}
		}
		if got := req.Header.Get(transcriptMetadataHeader); got != `{"backend":"codex"}` {
			t.Fatalf("transcript metadata = %q", got)
		}
		if bytes.Contains(body, []byte(testToken)) || strings.Contains(req.URL.String(), testToken) {
			t.Fatal("session token leaked outside its credential header")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"session_id":"session-1"}`)),
			Header:     make(http.Header),
		}, nil
	})
	client, registered, err := newFromEnvironment(
		func(name string) (string, bool) { value, ok := env[name]; return value, ok },
		func(name string) error { delete(env, name); return nil },
		&http.Client{Transport: transport},
	)
	if err != nil || !registered {
		t.Fatalf("newFromEnvironment = registered %v err %v", registered, err)
	}
	if err := client.PublishTranscript(t.Context(), interaction.PublishTranscriptCommand{
		Content: content, Metadata: map[string]string{"backend": "codex"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNewFromEnvironmentDistinguishesStandaloneAndFailsPartialClosed(t *testing.T) {
	t.Run("standalone", func(t *testing.T) {
		client, registered, err := newFromEnvironment(
			func(string) (string, bool) { return "", false },
			func(string) error { return nil },
			http.DefaultClient,
		)
		if err != nil || registered || client != nil {
			t.Fatalf("NewFromEnv = (%v, %v, %v), want nil false nil", client, registered, err)
		}
	})

	t.Run("partial", func(t *testing.T) {
		env := map[string]string{interaction.EnvSessionID: "session-1"}
		unset := false
		client, registered, err := newFromEnvironment(
			func(name string) (string, bool) {
				value, ok := env[name]
				return value, ok
			},
			func(name string) error {
				unset = name == interaction.EnvSessionToken
				return nil
			},
			http.DefaultClient,
		)
		if err == nil || !registered || client != nil || !unset {
			t.Fatalf("partial NewFromEnv = (%v, %v, %v), unset=%v", client, registered, err, unset)
		}
	})
}

func TestClientSendsCredentialOnlyInHeaderAndExactProofInBody(t *testing.T) {
	env := completeEnvelope()
	unset := false
	var requests []*http.Request
	var bodies []map[string]any
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		content, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(content, &body); err != nil {
			t.Fatal(err)
		}
		snapshot := req.Clone(req.Context())
		snapshot.Header = req.Header.Clone()
		requests = append(requests, snapshot)
		bodies = append(bodies, body)
		responseBody := `{}`
		if strings.HasSuffix(req.URL.Path, "/claim-next") {
			responseBody = `{"message":{"workspace_key":"WS","message_id":"msg-1","target_agent_id":"lead-1","session_id":"session-1","body":"hello","status":"queued","attempt":1}}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(responseBody)),
			Header:     make(http.Header),
		}, nil
	})
	client, registered, err := newFromEnvironment(
		func(name string) (string, bool) {
			value, ok := env[name]
			return value, ok
		},
		func(name string) error {
			if name == interaction.EnvSessionToken {
				unset = true
				delete(env, name)
			}
			return nil
		},
		&http.Client{Transport: transport},
	)
	if err != nil || !registered {
		t.Fatalf("newFromEnvironment = registered %v err %v", registered, err)
	}
	if !unset {
		t.Fatal("raw token environment was not cleared")
	}

	if err := client.HeartbeatSession(t.Context(), interaction.HeartbeatSessionCommand{
		Phase: "chatting", LeaseTTL: 90 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	phase := "active"
	artifactID := "artifact-1"
	if err := client.PatchSessionRuntimeContext(t.Context(), interaction.PatchSessionCommand{
		Phase:                &phase,
		MetadataUpserts:      map[string]string{"lead_runtime_status": "active"},
		MetadataRemovals:     []string{"lead_runtime_error"},
		TranscriptArtifactID: &artifactID,
	}); err != nil {
		t.Fatal(err)
	}
	message, err := client.ClaimNextInbox(t.Context(), interaction.ClaimInboxCommand{
		LeaseTTL: 2 * time.Minute,
	})
	if err != nil || message.MessageID != "msg-1" {
		t.Fatalf("ClaimNextInbox = %+v, %v", message, err)
	}
	if err := client.CompleteInbox(t.Context(), interaction.CompleteInboxCommand{
		MessageID:         "msg-1",
		Attempt:           message.Attempt,
		Status:            interaction.InboxDelivered,
		DeliveredThreadID: "thread-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := client.FinishSession(t.Context(), interaction.FinishSessionCommand{
		Status: interaction.SessionCompleted,
	}); err != nil {
		t.Fatal(err)
	}

	for index, request := range requests {
		if got := request.Header.Get(sessionTokenHeader); got != testToken {
			t.Fatalf("request %d token header = %q", index, got)
		}
		if strings.Contains(request.URL.String(), testToken) {
			t.Fatalf("request %d leaked token in URL", index)
		}
		if _, ok := bodies[index]["token"]; ok {
			t.Fatalf("request %d leaked token in body: %#v", index, bodies[index])
		}
		for key, want := range map[string]any{
			"agent_id":      "lead-1",
			"terminal_id":   "terminal-1",
			"node_id":       "node-1",
			"lease_id":      "lease-1",
			"fencing_token": float64(7),
		} {
			if got := bodies[index][key]; got != want {
				t.Fatalf("request %d %s = %#v, want %#v", index, key, got, want)
			}
		}
	}
	wantRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/workspaces/WS/interaction/sessions/session-1/heartbeat"},
		{http.MethodPatch, "/api/workspaces/WS/interaction/sessions/session-1"},
		{http.MethodPost, "/api/workspaces/WS/interaction/sessions/session-1/inbox/claim-next"},
		{http.MethodPost, "/api/workspaces/WS/interaction/sessions/session-1/inbox/msg-1/complete"},
		{http.MethodPost, "/api/workspaces/WS/interaction/sessions/session-1/finish"},
	}
	for index, want := range wantRoutes {
		if requests[index].Method != want.method || requests[index].URL.Path != want.path {
			t.Fatalf(
				"request %d = %s %s, want %s %s",
				index,
				requests[index].Method,
				requests[index].URL.Path,
				want.method,
				want.path,
			)
		}
	}
}

func TestClientCloseZeroesCredentialAndRejectsFurtherUse(t *testing.T) {
	env := completeEnvelope()
	client, _, err := newFromEnvironment(
		func(name string) (string, bool) {
			value, ok := env[name]
			return value, ok
		},
		func(string) error { return nil },
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("request must not be sent after Close")
			return nil, nil
		})},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.HeartbeatSession(context.Background(), interaction.HeartbeatSessionCommand{
		LeaseTTL: time.Minute,
	}); err == nil {
		t.Fatal("heartbeat after Close succeeded")
	}
	for _, value := range client.token {
		if value != 0 {
			t.Fatal("credential bytes were not zeroed")
		}
	}
}

func TestClaimNextMapsNotFoundToNoQueuedMessage(t *testing.T) {
	env := completeEnvelope()
	client, _, err := newFromEnvironment(
		func(name string) (string, bool) {
			value, ok := env[name]
			return value, ok
		},
		func(string) error { return nil },
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader(`{"error":"not found"}`)),
				Header:     make(http.Header),
			}, nil
		})},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ClaimNextInbox(t.Context(), interaction.ClaimInboxCommand{LeaseTTL: time.Minute})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ClaimNextInbox error = %v, want not found", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
