package fleethttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
			t.Errorf("X-API-Key = %q, want ak (dual-send for fleet-db RBAC)", got)
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
