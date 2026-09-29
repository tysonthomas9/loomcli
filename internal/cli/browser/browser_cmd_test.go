package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/browserauth"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

func withEnv(t *testing.T, env map[string]string) {
	t.Helper()
	orig := getenv
	getenv = func(k string) string { return env[k] }
	origSleep := sleep
	sleep = func(time.Duration) {}
	t.Cleanup(func() { getenv = orig; sleep = origSleep })
}

func resetFlags(t *testing.T) {
	t.Helper()
	createName, createRequestID, createJSON, stateJSON = defaultBrowserName, "", false, false
	t.Cleanup(func() { createName, createRequestID, createJSON, stateJSON = defaultBrowserName, "", false, false })
}

func TestCreateWithoutAgentSessionFails(t *testing.T) {
	resetFlags(t)
	withEnv(t, map[string]string{"LOOM_AGENT_NAME": "lead"})
	err := runCreate(context.Background(), &bytes.Buffer{})
	if !errors.Is(err, errNoAgentSession) {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateSendsSessionAndRetriesWithSameRequestID(t *testing.T) {
	resetFlags(t)
	var attempts atomic.Int32
	var ids []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/browsers" || r.Header.Get(browserauth.AgentSessionHeader) != "tok" {
			t.Errorf("request %s %v", r.URL.Path, r.Header)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["owner_agent_id"]; ok {
			t.Error("client must not send an owner")
		}
		ids = append(ids, body["request_id"].(string))
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"down","code":"browser_unavailable"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(domain.Browser{ID: "b1", Name: "Browser", Status: "starting", RequestID: body["request_id"].(string)})
	}))
	defer srv.Close()
	withEnv(t, map[string]string{browserauth.EnvAgentSessionToken: "tok", browserauth.EnvAgentBrowserURL: srv.URL})
	createJSON = true
	var out bytes.Buffer
	if err := runCreate(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != ids[1] || ids[0] == "" {
		t.Fatalf("request ids = %v (retries must reuse one id)", ids)
	}
	var got domain.Browser
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.ID != "b1" {
		t.Fatalf("output = %s", out.String())
	}
}

func TestCreateDoesNotRetryClientErrors(t *testing.T) {
	resetFlags(t)
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"the agent session is not active","code":"browser_agent_session_required"}`))
	}))
	defer srv.Close()
	withEnv(t, map[string]string{browserauth.EnvAgentSessionToken: "tok", browserauth.EnvAgentBrowserURL: srv.URL})
	createRequestID = "fixed"
	err := runCreate(context.Background(), &bytes.Buffer{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 || apiErr.Code != "browser_agent_session_required" {
		t.Fatalf("err = %v", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d", attempts.Load())
	}
}

func TestStateJSONShape(t *testing.T) {
	resetFlags(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"workspace":"ws","owner_agent_id":"lead","browsers":null}`))
	}))
	defer srv.Close()
	withEnv(t, map[string]string{browserauth.EnvAgentSessionToken: "tok", browserauth.EnvAgentBrowserURL: srv.URL + "/"})
	stateJSON = true
	var out bytes.Buffer
	if err := runState(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"browsers": []`) || !strings.Contains(out.String(), `"owner_agent_id": "lead"`) {
		t.Fatalf("state = %s", out.String())
	}
}

func TestStateTable(t *testing.T) {
	resetFlags(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"workspace":"ws","owner_agent_id":"lead","browsers":[{"id":"b1","name":"Docs","status":"failed","selected":true,"created_by":"agent:lead"}]}`))
	}))
	defer srv.Close()
	withEnv(t, map[string]string{browserauth.EnvAgentSessionToken: "tok", browserauth.EnvAgentBrowserURL: srv.URL})
	var out bytes.Buffer
	if err := runState(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Failed") || !strings.Contains(out.String(), "*") {
		t.Fatalf("table = %s", out.String())
	}
}
