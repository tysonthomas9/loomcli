package fleethttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuth_Apply(t *testing.T) {
	t.Run("all set", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		Auth{BearerToken: "tk", APIKey: "ak", Actor: "alice"}.Apply(req)
		if got := req.Header.Get("Authorization"); got != "Bearer tk" {
			t.Errorf("Authorization = %q, want Bearer tk", got)
		}
		if got := req.Header.Get("X-Fleet-API-Key"); got != "ak" {
			t.Errorf("X-Fleet-API-Key = %q, want ak", got)
		}
		if got := req.Header.Get("X-API-Key"); got != "ak" {
			t.Errorf("X-API-Key = %q, want ak", got)
		}
		if got := req.Header.Get("X-Actor"); got != "alice" {
			t.Errorf("X-Actor = %q, want alice", got)
		}
	})
	t.Run("empty fields skipped", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		Auth{Actor: "bob"}.Apply(req)
		if req.Header.Get("Authorization") != "" {
			t.Errorf("expected no Authorization header")
		}
		if req.Header.Get("X-Fleet-API-Key") != "" {
			t.Errorf("expected no X-Fleet-API-Key header")
		}
		if req.Header.Get("X-API-Key") != "" {
			t.Errorf("expected no X-API-Key header")
		}
		if req.Header.Get("X-Actor") != "bob" {
			t.Errorf("X-Actor = %q, want bob", req.Header.Get("X-Actor"))
		}
	})
}

func TestBuildJSONRequest(t *testing.T) {
	t.Run("nil GET body has no Content-Type", func(t *testing.T) {
		req, err := BuildJSONRequest(context.Background(), http.MethodGet, "http://x/api", Auth{}, nil)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if req.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q, want application/json", req.Header.Get("Accept"))
		}
		if req.Header.Get("Content-Type") != "" {
			t.Errorf("Content-Type set on body-less request: %q", req.Header.Get("Content-Type"))
		}
	})
	t.Run("nil POST body keeps content-type", func(t *testing.T) {
		req, err := BuildJSONRequest(context.Background(), http.MethodPost, "http://x/api", Auth{}, nil)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if req.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", req.Header.Get("Content-Type"))
		}
	})
	t.Run("body marshals + content-type set", func(t *testing.T) {
		req, err := BuildJSONRequest(context.Background(), http.MethodPost, "http://x/api", Auth{Actor: "a"}, map[string]string{"k": "v"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if req.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", req.Header.Get("Content-Type"))
		}
		if req.Header.Get("X-Actor") != "a" {
			t.Errorf("X-Actor not applied")
		}
		bodyBytes := make([]byte, 32)
		n, _ := req.Body.Read(bodyBytes)
		if got := string(bodyBytes[:n]); !strings.Contains(got, `"k":"v"`) {
			t.Errorf("body = %q, want JSON with k:v", got)
		}
	})
	t.Run("request actor overrides configured actor", func(t *testing.T) {
		ctx := WithActor(context.Background(), "connected-agent")
		req, err := BuildJSONRequest(ctx, http.MethodPatch, "http://x/api", Auth{Actor: "daemon-operator"}, map[string]string{"k": "v"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got := req.Header.Get("X-Actor"); got != "connected-agent" {
			t.Errorf("X-Actor = %q, want connected-agent", got)
		}
	})
}

func TestExtractErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"wrapper shape", `{"success":false,"error":"boom"}`, "boom"},
		{"structured shape", `{"error":{"code":"E","message":"detail"}}`, "detail"},
		{"unparsable", `not json`, ""},
		{"empty error fields", `{"error":""}`, ""},
		{"structured wins when both present", `{"error":{"message":"struct"}}`, "struct"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractErrorMessage([]byte(tt.body))
			if got != tt.want {
				t.Errorf("ExtractErrorMessage(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestDo_RetriesRateLimitUntilSuccess(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != `{"name":"test"}` {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		attempt := attempts.Add(1)
		if attempt < 3 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(server.Close)

	req, err := BuildJSONRequest(t.Context(), http.MethodPost, server.URL, Auth{}, map[string]string{"name": "test"})
	if err != nil {
		t.Fatalf("BuildJSONRequest: %v", err)
	}
	resp, err := Do(server.Client(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestDo_ExhaustedRateLimitReturnsFinalResponse(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"still limited"}`))
	}))
	t.Cleanup(server.Close)

	req, err := BuildJSONRequest(t.Context(), http.MethodGet, server.URL, Auth{}, nil)
	if err != nil {
		t.Fatalf("BuildJSONRequest: %v", err)
	}
	resp, err := Do(server.Client(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusTooManyRequests)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if got := string(body); got != `{"error":"still limited"}` {
		t.Fatalf("body = %q, want final 429 body", got)
	}
	if got := attempts.Load(); got != maxAttempts {
		t.Fatalf("attempts = %d, want %d", got, maxAttempts)
	}
}

func TestDo_DoesNotRetryOtherStatuses(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	req, err := BuildJSONRequest(t.Context(), http.MethodGet, server.URL, Auth{}, nil)
	if err != nil {
		t.Fatalf("BuildJSONRequest: %v", err)
	}
	resp, err := Do(server.Client(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

func TestRetryDelay_HonorsRetryAfterSeconds(t *testing.T) {
	if got := retryDelay("3", 2); got != 3*time.Second {
		t.Fatalf("retryDelay = %v, want 3s Retry-After", got)
	}
}

func TestRetryDelayCapsRetryAfter(t *testing.T) {
	if got := retryDelay("3600", 0); got != maxRetryDelay {
		t.Fatalf("retryDelay = %v, want cap %v", got, maxRetryDelay)
	}
}

func TestResolveFleetDBActor_Precedence(t *testing.T) {
	tests := []struct {
		name       string
		fleetActor string
		agentName  string
		configured string
		user       string
		want       string
	}{
		{name: "fleet actor env wins", fleetActor: "fleet-actor", agentName: "agent-name", configured: "configured", user: "user-name", want: "fleet-actor"},
		{name: "agent name beats configured", agentName: "agent-name", configured: "configured", user: "user-name", want: "agent-name"},
		{name: "configured beats user", configured: "configured", user: "user-name", want: "configured"},
		{name: "user is the fallback", user: "user-name", want: "user-name"},
		{name: "all empty", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvFleetDBActor, tt.fleetActor)
			t.Setenv(EnvAgentName, tt.agentName)
			t.Setenv("USER", tt.user)
			if got := ResolveFleetDBActor(tt.configured); got != tt.want {
				t.Fatalf("ResolveFleetDBActor(%q) = %q, want %q", tt.configured, got, tt.want)
			}
		})
	}
}
