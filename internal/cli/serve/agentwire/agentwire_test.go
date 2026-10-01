package agentwire

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// TestStartServesOneWorkspace starts the Agent API with no OpenCode build:
// the preset files are written, the routes serve its workspace only, and
// Stop returns while the feed is still retrying.
func TestStartServesOneWorkspace(t *testing.T) {
	dir := t.TempDir()
	api, err := Start(context.Background(), Config{WorkspaceID: "ws", Dir: dir,
		OpenCodeBin: filepath.Join(dir, "missing-opencode")})
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			api.Stop()
		}
	})
	if _, err := os.Stat(filepath.Join(dir, "worktrees/.opencode/agent/loom-lead.md")); err != nil {
		t.Fatalf("lead preset file: %v", err)
	}
	mux := http.NewServeMux()
	api.Register(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.WithWorkspace(r.Context(), r.PathValue("ws"))))
		})
	}, nil)
	for ws, want := range map[string]int{"ws": http.StatusOK, "other": http.StatusNotFound} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/workspaces/"+ws+"/v1/agents", nil))
		if rec.Code != want {
			t.Errorf("GET %s agents = %d %s; want %d", ws, rec.Code, rec.Body, want)
		}
	}
	api.Stop()
	stopped = true
}

func TestStartNeedsWorkspaceAndDir(t *testing.T) {
	if _, err := Start(context.Background(), Config{Dir: t.TempDir()}); err == nil {
		t.Fatal("Start without a workspace succeeded")
	}
}
