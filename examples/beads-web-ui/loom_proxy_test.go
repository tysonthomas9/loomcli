package main

import (
	"os"
	"testing"
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

			proxy := newLoomProxy()
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

			proxy := newLoomProxy()
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

			proxy := newLoomProxy()
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

			proxy := newLoomProxy()
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

			proxy := newLoomProxy()
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

			proxy := newLoomProxy()
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
			proxy := newLoomProxy()
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

			proxy := newLoomProxy()
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

			proxy := newLoomProxy()
			if tt.wantNil && proxy != nil {
				t.Errorf("newLoomProxy() = non-nil, want nil for URL %q", tt.url)
			}
			if !tt.wantNil && proxy == nil {
				t.Errorf("newLoomProxy() = nil, want non-nil for URL %q", tt.url)
			}
		})
	}
}
