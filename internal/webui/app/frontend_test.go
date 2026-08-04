package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeFrontendStaticAsset(t *testing.T) {
	root := t.TempDir()
	mustWriteFrontendFile(t, root, "index.html", "<html>app</html>")
	mustWriteFrontendFile(t, root, "assets/app.js", "console.log('ok')")

	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	rec := httptest.NewRecorder()
	serveFrontend(root)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "console.log('ok')" {
		t.Fatalf("body = %q, want asset content", got)
	}
}

func TestServeFrontendFallsBackToIndexForSPARoute(t *testing.T) {
	root := t.TempDir()
	mustWriteFrontendFile(t, root, "index.html", "<html>app</html>")

	req := httptest.NewRequest(http.MethodGet, "/ws/ACME/kanban", nil)
	rec := httptest.NewRecorder()
	serveFrontend(root)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "app") {
		t.Fatalf("body = %q, want index fallback", rec.Body.String())
	}
}

func TestServeFrontendRejectsNonReadMethods(t *testing.T) {
	root := t.TempDir()
	mustWriteFrontendFile(t, root, "index.html", "<html>app</html>")

	req := httptest.NewRequest(http.MethodPost, "/workspace", nil)
	rec := httptest.NewRecorder()
	serveFrontend(root)(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("Allow = %q, want GET, HEAD", got)
	}
}

func TestFrontendCatchAllDoesNotConflictWithWorkspaceAPIRoutes(t *testing.T) {
	root := t.TempDir()
	mustWriteFrontendFile(t, root, "index.html", "<html>app</html>")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("health"))
	})
	mux.HandleFunc("/api/workspaces/{ws}/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("workspace"))
	})
	mux.Handle("/api/", http.NotFoundHandler())
	mux.HandleFunc("/", serveFrontend(root))

	apiReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/DEFAULT/issues", nil)
	apiRec := httptest.NewRecorder()
	mux.ServeHTTP(apiRec, apiReq)
	if got := apiRec.Body.String(); got != "workspace" {
		t.Fatalf("api body = %q, want workspace route", got)
	}

	frontendReq := httptest.NewRequest(http.MethodGet, "/workspaces/DEFAULT", nil)
	frontendRec := httptest.NewRecorder()
	mux.ServeHTTP(frontendRec, frontendReq)
	if !strings.Contains(frontendRec.Body.String(), "app") {
		t.Fatalf("frontend body = %q, want index fallback", frontendRec.Body.String())
	}
}

func mustWriteFrontendFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
