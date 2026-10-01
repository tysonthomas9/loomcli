package agentwire

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// TestStartServesOneWorkspace starts the Agent API with no agents: the
// preset files are written, the routes serve its workspace only, OpenCode is
// never run, and Stop returns.
func TestStartServesOneWorkspace(t *testing.T) {
	dir := t.TempDir()
	bin, ran := filepath.Join(dir, "opencode"), filepath.Join(dir, "ran")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ntouch "+ran+"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	api, err := Start(context.Background(), Config{WorkspaceID: "ws", Dir: dir, OpenCodeBin: bin})
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
	time.Sleep(500 * time.Millisecond) // longer than the first feed retry
	api.Stop()
	stopped = true
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("a boot with no agents ran OpenCode")
	}
}

func TestStartNeedsWorkspaceAndDir(t *testing.T) {
	if _, err := Start(context.Background(), Config{Dir: t.TempDir()}); err == nil {
		t.Fatal("Start without a workspace succeeded")
	}
}
