package webui

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// setEnvCleanup sets environment variables and returns a cleanup function.
func setEnvCleanup(vars map[string]string) func() {
	original := make(map[string]string)
	for k := range vars {
		original[k] = os.Getenv(k)
	}
	for k, v := range vars {
		os.Setenv(k, v)
	}
	return func() {
		for k, v := range original {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	}
}

func TestNewLoomProxy_DefaultBehavior_LocalhostOnly(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantNil bool
	}{
		{"localhost allowed", "http://localhost:9000", false},
		{"127.0.0.1 allowed", "http://127.0.0.1:9000", false},
		{"::1 allowed", "http://[::1]:9000", false},
		{"external host rejected", "http://example.com:9000", true},
		{"internal host rejected", "http://loom:9000", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := setEnvCleanup(map[string]string{
				"LOOM_SERVER_URL":          tt.url,
				"LOOM_PROXY_ALLOWED_HOSTS": "",
			})
			defer cleanup()

			proxy := newLoomProxy("")
			if tt.wantNil && proxy != nil {
				t.Errorf("newLoomProxy() = non-nil, want nil for URL %q", tt.url)
			}
			if !tt.wantNil && proxy == nil {
				t.Errorf("newLoomProxy() = nil, want non-nil for URL %q", tt.url)
			}
		})
	}
}

func TestNewLoomProxy_SingleAllowedHost(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantNil bool
	}{
		{"allowed host works", "http://loom:9000", false},
		{"localhost still works", "http://localhost:9000", false},
		{"127.0.0.1 still works", "http://127.0.0.1:9000", false},
		{"::1 still works", "http://[::1]:9000", false},
		{"other host rejected", "http://example.com:9000", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := setEnvCleanup(map[string]string{
				"LOOM_SERVER_URL":          tt.url,
				"LOOM_PROXY_ALLOWED_HOSTS": "loom",
			})
			defer cleanup()

			proxy := newLoomProxy("")
			if tt.wantNil && proxy != nil {
				t.Errorf("newLoomProxy() = non-nil, want nil for URL %q", tt.url)
			}
			if !tt.wantNil && proxy == nil {
				t.Errorf("newLoomProxy() = nil, want non-nil for URL %q", tt.url)
			}
		})
	}
}

func TestNewLoomProxy_MultipleAllowedHosts(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantNil bool
	}{
		{"first allowed host", "http://loom:9000", false},
		{"second allowed host", "http://internal-loom:9000", false},
		{"third allowed host", "http://loom.local:9000", false},
		{"localhost still works", "http://localhost:9000", false},
		{"127.0.0.1 still works", "http://127.0.0.1:9000", false},
		{"::1 still works", "http://[::1]:9000", false},
		{"unlisted host rejected", "http://example.com:9000", true},
		{"similar but different host rejected", "http://loom2:9000", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := setEnvCleanup(map[string]string{
				"LOOM_SERVER_URL":          tt.url,
				"LOOM_PROXY_ALLOWED_HOSTS": "loom,internal-loom,loom.local",
			})
			defer cleanup()

			proxy := newLoomProxy("")
			if tt.wantNil && proxy != nil {
				t.Errorf("newLoomProxy() = non-nil, want nil for URL %q", tt.url)
			}
			if !tt.wantNil && proxy == nil {
				t.Errorf("newLoomProxy() = nil, want non-nil for URL %q", tt.url)
			}
		})
	}
}

func TestNewLoomProxy_AllowedHostsWithWhitespace(t *testing.T) {
	tests := []struct {
		name         string
		allowedHosts string
		url          string
		wantNil      bool
	}{
		{"spaces around commas", " loom , internal-loom ", "http://loom:9000", false},
		{"spaces in middle of list", "loom,  internal-loom  ,loom.local", "http://internal-loom:9000", false},
		{"empty entries ignored", "loom,,internal-loom", "http://loom:9000", false},
		{"only whitespace entry ignored", "loom,   ,internal-loom", "http://internal-loom:9000", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := setEnvCleanup(map[string]string{
				"LOOM_SERVER_URL":          tt.url,
				"LOOM_PROXY_ALLOWED_HOSTS": tt.allowedHosts,
			})
			defer cleanup()

			proxy := newLoomProxy("")
			if tt.wantNil && proxy != nil {
				t.Errorf("newLoomProxy() = non-nil, want nil")
			}
			if !tt.wantNil && proxy == nil {
				t.Errorf("newLoomProxy() = nil, want non-nil")
			}
		})
	}
}

func TestNewLoomProxy_LocalhostVariantsAlwaysAllowed(t *testing.T) {
	// Even when LOOM_PROXY_ALLOWED_HOSTS is set to something specific,
	// localhost variants should always be allowed.
	tests := []struct {
		name string
		url  string
	}{
		{"localhost", "http://localhost:9000"},
		{"localhost with path", "http://localhost:9000/api"},
		{"127.0.0.1", "http://127.0.0.1:9000"},
		{"127.0.0.1 different port", "http://127.0.0.1:8080"},
		{"::1 IPv6", "http://[::1]:9000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := setEnvCleanup(map[string]string{
				"LOOM_SERVER_URL":          tt.url,
				"LOOM_PROXY_ALLOWED_HOSTS": "some-other-host",
			})
			defer cleanup()

			proxy := newLoomProxy("")
			if proxy == nil {
				t.Errorf("newLoomProxy() = nil, want non-nil for URL %q (localhost should always be allowed)", tt.url)
			}
		})
	}
}

func TestNewLoomProxy_DisallowedHostsRejected(t *testing.T) {
	tests := []struct {
		name         string
		allowedHosts string
		url          string
	}{
		{"external domain rejected", "loom", "http://example.com:9000"},
		{"similar name rejected", "loom", "http://loom-prod:9000"},
		{"subdomain rejected", "loom", "http://sub.loom:9000"},
		{"different tld rejected", "loom.local", "http://loom.com:9000"},
		{"empty allowed list rejects non-localhost", "", "http://internal:9000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := setEnvCleanup(map[string]string{
				"LOOM_SERVER_URL":          tt.url,
				"LOOM_PROXY_ALLOWED_HOSTS": tt.allowedHosts,
			})
			defer cleanup()

			proxy := newLoomProxy("")
			if proxy != nil {
				t.Errorf("newLoomProxy() = non-nil, want nil for URL %q with allowed hosts %q", tt.url, tt.allowedHosts)
			}
		})
	}
}

func TestNewLoomProxy_InvalidURLRejected(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"empty URL uses default", ""},
		{"whitespace URL uses default", "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := setEnvCleanup(map[string]string{
				"LOOM_SERVER_URL":          tt.url,
				"LOOM_PROXY_ALLOWED_HOSTS": "",
			})
			defer cleanup()

			// Empty/whitespace URLs should fall back to default (localhost:9000)
			// which should be allowed
			proxy := newLoomProxy("")
			if proxy == nil {
				t.Errorf("newLoomProxy() = nil, want non-nil for empty URL (should use default localhost)")
			}
		})
	}
}

func TestNewLoomProxy_InvalidSchemeRejected(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"ftp scheme rejected", "ftp://localhost:9000"},
		{"file scheme rejected", "file:///etc/passwd"},
		{"no scheme rejected", "localhost:9000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := setEnvCleanup(map[string]string{
				"LOOM_SERVER_URL":          tt.url,
				"LOOM_PROXY_ALLOWED_HOSTS": "",
			})
			defer cleanup()

			proxy := newLoomProxy("")
			if proxy != nil {
				t.Errorf("newLoomProxy() = non-nil, want nil for URL %q (invalid scheme)", tt.url)
			}
		})
	}
}

func TestNewLoomProxy_HTTPSAllowed(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantNil bool
	}{
		{"https localhost", "https://localhost:9000", false},
		{"https 127.0.0.1", "https://127.0.0.1:9000", false},
		{"https allowed host", "https://loom:9000", false},
		{"https disallowed host", "https://example.com:9000", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := setEnvCleanup(map[string]string{
				"LOOM_SERVER_URL":          tt.url,
				"LOOM_PROXY_ALLOWED_HOSTS": "loom",
			})
			defer cleanup()

			proxy := newLoomProxy("")
			if tt.wantNil && proxy != nil {
				t.Errorf("newLoomProxy() = non-nil, want nil for URL %q", tt.url)
			}
			if !tt.wantNil && proxy == nil {
				t.Errorf("newLoomProxy() = nil, want non-nil for URL %q", tt.url)
			}
		})
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"10.0.0.1 private", "10.0.0.1", true},
		{"10.255.255.255 private", "10.255.255.255", true},
		{"172.16.0.1 private", "172.16.0.1", true},
		{"172.31.255.255 private", "172.31.255.255", true},
		{"172.15.0.1 not private", "172.15.0.1", false},
		{"172.32.0.1 not private", "172.32.0.1", false},
		{"192.168.1.1 private", "192.168.1.1", true},
		{"192.168.0.0 private", "192.168.0.0", true},
		{"169.254.1.1 link-local", "169.254.1.1", true},
		{"8.8.8.8 public", "8.8.8.8", false},
		{"1.1.1.1 public", "1.1.1.1", false},
		{"127.0.0.1 loopback not private", "127.0.0.1", false},
		{"::1 loopback not private", "::1", false},
		{"fc00::1 IPv6 ULA", "fc00::1", true},
		{"fd12::1 IPv6 ULA", "fd12::1", true},
		{"fe80::1 IPv6 link-local", "fe80::1", true},
		{"2001:db8::1 public IPv6", "2001:db8::1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", tt.ip)
			}
			got := isPrivateIP(ip)
			if got != tt.want {
				t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestSafeDialContext_AllowPrivate(t *testing.T) {
	dialFn := safeDialContext(true)
	if dialFn == nil {
		t.Fatal("safeDialContext(true) returned nil, want non-nil DialContext function")
	}
}

func TestSafeDialContext_BlocksPrivateIPs(t *testing.T) {
	dialFn := safeDialContext(false)
	if dialFn == nil {
		t.Fatal("safeDialContext(false) returned nil, want non-nil DialContext function")
	}

	tests := []struct {
		name        string
		addr        string
		wantBlocked bool
	}{
		{"10.0.0.1 blocked", "10.0.0.1:80", true},
		{"192.168.1.1 blocked", "192.168.1.1:80", true},
		{"172.16.0.1 blocked", "172.16.0.1:80", true},
		{"localhost allowed", "localhost:80", false},
		{"127.0.0.1 allowed", "127.0.0.1:80", false},
		{"::1 allowed", net.JoinHostPort("::1", "80"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			_, err := dialFn(ctx, "tcp", tt.addr)
			if tt.wantBlocked {
				if err == nil {
					t.Fatalf("expected error for %s, got nil", tt.addr)
				}
				errMsg := strings.ToLower(err.Error())
				if !strings.Contains(errMsg, "blocked") && !strings.Contains(errMsg, "private") {
					t.Errorf("expected error containing 'blocked' or 'private' for %s, got: %v", tt.addr, err)
				}
			} else {
				if err == nil {
					t.Logf("dial to %s succeeded (unexpected in test env, but not a test failure)", tt.addr)
					return
				}
				errMsg := strings.ToLower(err.Error())
				if strings.Contains(errMsg, "blocked") || strings.Contains(errMsg, "private") {
					t.Errorf("did not expect 'blocked'/'private' error for %s, got: %v", tt.addr, err)
				}
			}
		})
	}
}

func TestNewLoomProxy_DebugLogDoesNotLeakToken(t *testing.T) {
	// Capture slog output for the duration of this test.
	// Do NOT run sub-tests in parallel — slog.SetDefault is global.
	var buf bytes.Buffer
	origLogger := slog.Default()
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(origLogger)

	const secretToken = "supersecretvalue42"

	t.Run("Director log omits token", func(t *testing.T) {
		buf.Reset()

		// Start a dummy backend so the proxy can connect.
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer backend.Close()

		cleanup := setEnvCleanup(map[string]string{
			"LOOM_SERVER_URL":          backend.URL,
			"LOOM_PROXY_DEBUG":         "1",
			"LOOM_PROXY_ALLOWED_HOSTS": "",
		})
		defer cleanup()

		proxy := newLoomProxy("")
		if proxy == nil {
			t.Fatal("newLoomProxy returned nil")
		}

		req := httptest.NewRequest(http.MethodGet, "/api/loom/some/path?token="+secretToken, nil)
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)

		logOutput := buf.String()
		if strings.Contains(logOutput, secretToken) {
			t.Errorf("Director log leaked token: %s", logOutput)
		}
		if !strings.Contains(logOutput, "/some/path") {
			t.Errorf("Director log missing path, got: %s", logOutput)
		}
	})

	t.Run("ErrorHandler log omits token", func(t *testing.T) {
		buf.Reset()

		// Use an unreachable backend to trigger the ErrorHandler.
		cleanup := setEnvCleanup(map[string]string{
			"LOOM_SERVER_URL":          "http://127.0.0.1:19999",
			"LOOM_PROXY_DEBUG":         "1",
			"LOOM_PROXY_ALLOWED_HOSTS": "",
		})
		defer cleanup()

		proxy := newLoomProxy("")
		if proxy == nil {
			t.Fatal("newLoomProxy returned nil")
		}

		req := httptest.NewRequest(http.MethodGet, "/api/loom/other/endpoint?token="+secretToken, nil)
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadGateway {
			t.Errorf("expected 502, got %d", rec.Code)
		}

		logOutput := buf.String()
		if strings.Contains(logOutput, secretToken) {
			t.Errorf("ErrorHandler log leaked token: %s", logOutput)
		}
		// The Director runs before ErrorHandler, so the path is already stripped.
		if !strings.Contains(logOutput, "/other/endpoint") {
			t.Errorf("ErrorHandler log missing path, got: %s", logOutput)
		}
	})
}

func TestNewLoomProxy_DefaultURLParameter(t *testing.T) {
	t.Run("defaultURL used when env unset", func(t *testing.T) {
		cleanup := setEnvCleanup(map[string]string{
			"LOOM_SERVER_URL":          "",
			"LOOM_PROXY_ALLOWED_HOSTS": "",
		})
		defer cleanup()

		proxy := newLoomProxy("http://localhost:9999")
		if proxy == nil {
			t.Error("newLoomProxy(\"http://localhost:9999\") = nil, want non-nil")
		}
	})

	t.Run("env var takes precedence over defaultURL", func(t *testing.T) {
		cleanup := setEnvCleanup(map[string]string{
			"LOOM_SERVER_URL":          "http://example.com:9000",
			"LOOM_PROXY_ALLOWED_HOSTS": "",
		})
		defer cleanup()

		// example.com is not in allowed hosts, so if env var takes precedence,
		// the proxy should be nil (rejected). If defaultURL were used instead,
		// it would be non-nil.
		proxy := newLoomProxy("http://localhost:8081")
		if proxy != nil {
			t.Error("expected nil when LOOM_SERVER_URL points to disallowed host, got non-nil")
		}
	})

	t.Run("constant fallback when both empty", func(t *testing.T) {
		cleanup := setEnvCleanup(map[string]string{
			"LOOM_SERVER_URL":          "",
			"LOOM_PROXY_ALLOWED_HOSTS": "",
		})
		defer cleanup()

		// Empty defaultURL should fall back to constant (http://localhost:8081)
		proxy := newLoomProxy("")
		if proxy == nil {
			t.Error("newLoomProxy(\"\") = nil, want non-nil (should fall back to default constant)")
		}
	})
}
