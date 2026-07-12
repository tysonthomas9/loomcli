package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/fileaccess"
)

func TestFileAccessRemoteRequiresIdentityAndWorkspaceResolver(t *testing.T) {
	tests := []struct {
		name     string
		identity *UserIdentity
		resolver WorkspaceRoleResolver
		want     int
	}{
		{name: "unauthenticated", want: http.StatusUnauthorized},
		{name: "global identity without workspace resolver", identity: &UserIdentity{UserID: "user-1"}, want: http.StatusForbidden},
		{
			name:     "resolver failure",
			identity: &UserIdentity{UserID: "user-1"},
			resolver: func(context.Context, string, UserIdentity) (string, error) { return "", errors.New("unavailable") },
			want:     http.StatusForbidden,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := serveFileAccess(t, FileAccessConfig{RemoteAuth: true, ResolveRole: tc.resolver}, http.MethodGet, "/api/workspaces/WS/files/tree", "api.example.com", "", "WS", tc.identity)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestFileAccessRemoteMissingResolverReportsRBACNotConfigured(t *testing.T) {
	rr := serveFileAccess(t, FileAccessConfig{RemoteAuth: true}, http.MethodGet, "/api/workspaces/WS/files/tree", "api.example.com", "", "WS", &UserIdentity{UserID: "user-1"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "file browser RBAC not configured") {
		t.Fatalf("body = %s, want RBAC diagnostic", rr.Body.String())
	}
}

func TestFileAccessRemoteUsesCanonicalWorkspaceAndDeniesWrongWorkspace(t *testing.T) {
	var gotWorkspace string
	resolver := func(_ context.Context, workspaceID string, _ UserIdentity) (string, error) {
		gotWorkspace = workspaceID
		if workspaceID != "workspace-a" {
			return "", nil
		}
		return "viewer", nil
	}
	identity := &UserIdentity{UserID: "user-1"}

	allowed := serveFileAccess(t, FileAccessConfig{RemoteAuth: true, ResolveRole: resolver}, http.MethodGet, "/api/workspaces/alias/files/tree", "api.example.com", "", "workspace-a", identity)
	if allowed.Code != http.StatusNoContent || gotWorkspace != "workspace-a" {
		t.Fatalf("allowed status=%d workspace=%q body=%s", allowed.Code, gotWorkspace, allowed.Body.String())
	}

	denied := serveFileAccess(t, FileAccessConfig{RemoteAuth: true, ResolveRole: resolver}, http.MethodGet, "/api/workspaces/other/files/tree", "api.example.com", "", "workspace-b", identity)
	if denied.Code != http.StatusForbidden || gotWorkspace != "workspace-b" {
		t.Fatalf("denied status=%d workspace=%q body=%s", denied.Code, gotWorkspace, denied.Body.String())
	}
}

func TestFileAccessViewerReadsAndSearchesButCannotWrite(t *testing.T) {
	resolver := func(context.Context, string, UserIdentity) (string, error) { return "viewer", nil }
	identity := &UserIdentity{UserID: "viewer-1"}
	for _, tc := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/api/workspaces/WS/files/tree", http.StatusNoContent},
		{http.MethodPost, "/api/workspaces/WS/files/search", http.StatusNoContent},
		{http.MethodPut, "/api/workspaces/WS/files", http.StatusForbidden},
		{http.MethodPost, "/api/workspaces/WS/files/mkdir", http.StatusForbidden},
		{http.MethodPost, "/api/workspaces/WS/files/checkouts/repair", http.StatusForbidden},
	} {
		rr := serveFileAccess(t, FileAccessConfig{RemoteAuth: true, ResolveRole: resolver}, tc.method, tc.path, "api.example.com", "", "WS", identity)
		if rr.Code != tc.want {
			t.Errorf("%s %s status=%d want=%d body=%s", tc.method, tc.path, rr.Code, tc.want, rr.Body.String())
		}
	}
}

func TestFileAccessInstallsRoleCapabilities(t *testing.T) {
	for _, tc := range []struct {
		role string
		want fileaccess.Capabilities
	}{
		{role: "viewer", want: fileaccess.Capabilities{Read: true}},
		{role: "editor", want: fileaccess.Capabilities{Read: true, Write: true, Sensitive: true}},
		{role: "admin", want: fileaccess.Capabilities{Read: true, Write: true, Sensitive: true}},
	} {
		t.Run(tc.role, func(t *testing.T) {
			var got fileaccess.Capabilities
			cfg := FileAccessConfig{RemoteAuth: true, ResolveRole: func(context.Context, string, UserIdentity) (string, error) { return tc.role, nil }}
			h := FileAccess(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, _ = fileaccess.FromContext(r.Context())
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/files/capabilities", nil)
			req.Host = "api.example.com"
			req = req.WithContext(WithWorkspace(WithUserIdentity(req.Context(), UserIdentity{UserID: "user-1"}), "WS"))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusNoContent || got != tc.want {
				t.Fatalf("status=%d capabilities=%+v want=%+v", rr.Code, got, tc.want)
			}
		})
	}
}

func TestFileAccessLocalLoopbackPolicy(t *testing.T) {
	cfg := FileAccessConfig{FrontendOrigins: []string{"http://127.0.0.1:5173", "https://app.example.com"}}
	for _, tc := range []struct {
		name   string
		host   string
		origin string
		want   int
	}{
		{name: "podman bind shape", host: "127.0.0.1:8080", origin: "http://127.0.0.1:5173", want: http.StatusNoContent},
		{name: "hostile host", host: "attacker.example:8080", origin: "http://127.0.0.1:5173", want: http.StatusForbidden},
		{name: "hostile origin", host: "127.0.0.1:8080", origin: "http://attacker.example", want: http.StatusForbidden},
		{name: "loopback name mismatch", host: "localhost:8080", origin: "http://127.0.0.1:5173", want: http.StatusForbidden},
		{name: "missing origin across ports", host: "127.0.0.1:8080", want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := serveFileAccess(t, cfg, http.MethodPut, "/api/workspaces/WS/files", tc.host, tc.origin, "WS", nil)
			if rr.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestFileAccessLocalAllowsBundledSameOriginLoopback(t *testing.T) {
	cfg := FileAccessConfig{FrontendOrigins: []string{"http://localhost:8080", "http://127.0.0.1:8080"}}
	for _, host := range []string{"localhost:8080", "127.0.0.1:8080"} {
		t.Run(host, func(t *testing.T) {
			rr := serveFileAccess(t, cfg, http.MethodPut, "/api/workspaces/WS/files", host, "", "WS", nil)
			if rr.Code != http.StatusNoContent {
				t.Fatalf("status=%d want=204 body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestFileAccessLocalRequiresConfiguredLoopbackFrontend(t *testing.T) {
	for _, origins := range [][]string{nil, {"https://app.example.com"}, {"http://0.0.0.0:5173"}} {
		rr := serveFileAccess(t, FileAccessConfig{FrontendOrigins: origins}, http.MethodGet, "/api/workspaces/WS/files/tree", "127.0.0.1:8080", "http://127.0.0.1:5173", "WS", nil)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("origins=%v status=%d want=403", origins, rr.Code)
		}
	}
}

func serveFileAccess(t *testing.T, cfg FileAccessConfig, method, path, host, origin, workspace string, identity *UserIdentity) *httptest.ResponseRecorder {
	t.Helper()
	h := FileAccess(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	req.Header.Set("Origin", origin)
	req = req.WithContext(WithWorkspace(req.Context(), workspace))
	if identity != nil {
		req = req.WithContext(WithUserIdentity(req.Context(), *identity))
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
