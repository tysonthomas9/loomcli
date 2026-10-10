package agentwire

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// TestStartServesEachWorkspace starts the Agent API with no agents: the
// preset files are written, every workspace gets its own service, OpenCode
// is never run, and Stop returns.
func TestStartServesEachWorkspace(t *testing.T) {
	dir := t.TempDir()
	bin, ran := filepath.Join(dir, "opencode"), filepath.Join(dir, "ran")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ntouch "+ran+"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	api, err := Start(context.Background(), Config{Dir: dir, OpenCodeBin: bin})
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
	for _, ws := range []string{"ws", "ws2", "ws"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/workspaces/"+ws+"/v1/agents", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s agents = %d %s", ws, rec.Code, rec.Body)
		}
	}
	if len(api.services) != 2 || api.services["ws"] == api.services["ws2"] {
		t.Errorf("services = %v; want one per workspace", api.services)
	}
	drainAll(t, api) // the dispatchers' start-up is done; any feed it started has opened
	api.Stop()
	stopped = true
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("a boot with no agents ran OpenCode")
	}
}

// drainAll drains every workspace service of api: a test that needs a
// timeout to pass is wrong.
func drainAll(t *testing.T, api *API) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // a guard against a hang
	defer cancel()
	api.mu.Lock()
	services := slices.Collect(maps.Values(api.services))
	api.mu.Unlock()
	for _, s := range services {
		if err := s.Drain(ctx); err != nil {
			t.Fatalf("drain: %v", err)
		}
	}
}

func TestStartNeedsDir(t *testing.T) {
	if _, err := Start(context.Background(), Config{}); err == nil {
		t.Fatal("Start without a data dir succeeded")
	}
}

// TestStartResumesWorkspacesWithAgents: a workspace with a live OpenCode
// agent gets its service and feed at boot, before any request; one whose
// only agent is deleted gets its service (for pending purges) but OpenCode
// is never run.
func TestStartResumesWorkspacesWithAgents(t *testing.T) {
	for name, deleted := range map[string]bool{"live": false, "deleted": true} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			st, err := loomstore.Open(ctx, filepath.Join(dir, "agents.db"))
			if err != nil {
				t.Fatal(err)
			}
			if err := st.InsertAgent(ctx, loomstore.Agent{AgentID: "a1", WorkspaceID: "ws2", Name: "a1", ProfileKey: "a1",
				Preset: "lead", PresetVersion: "1", Mode: "persistent", InteractionMode: "interactive", RoleKind: "interactive",
				SpecJSON: "{}", SpecVersion: 1, OwnerKind: "user", OwnerID: "u", CreatedByKind: "user", CreatedByID: "u",
				CreateRequestID: "r1", Repo: "/repo", Harness: "opencode", State: "idle"}); err != nil {
				t.Fatal(err)
			}
			if deleted {
				if err := st.Tombstone(ctx, "a1", time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			_ = st.Close()
			bin, ran := filepath.Join(dir, "opencode"), filepath.Join(dir, "ran")
			if err := os.WriteFile(bin, []byte("#!/bin/sh\ntouch "+ran+"\nexit 1\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			api, err := Start(ctx, Config{Dir: dir, OpenCodeBin: bin})
			if err != nil {
				t.Fatal(err)
			}
			defer api.Stop()
			if len(api.services) != 1 || api.services["ws2"] == nil {
				t.Fatalf("services = %v; want ws2", api.services)
			}
			drainAll(t, api) // the start-up Reconcile is done, and the feed's first open if one started
			if deleted {
				if _, err := os.Stat(ran); err == nil {
					t.Fatal("a workspace with only a deleted agent ran OpenCode")
				}
				return
			}
			if _, err := os.Stat(ran); err != nil {
				t.Fatal("the feed of a workspace with OpenCode agents never reached OpenCode")
			}
		})
	}
}

// TestCreateRejectsUnknownRepo: a repo that is not the path of a clone, such
// as a repo's name, a missing path, a directory that is not a git clone or
// one whose .git is a dangling worktree link, is a 400 at Create, not a git
// failure (500).
func TestCreateRejectsUnknownRepo(t *testing.T) {
	dir := t.TempDir()
	api, err := Start(context.Background(), Config{Dir: dir, OpenCodeBin: filepath.Join(dir, "no-opencode")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Stop)
	mux := http.NewServeMux()
	api.Register(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.WithWorkspace(r.Context(), r.PathValue("ws"))))
		})
	}, nil)
	dangling := filepath.Join(dir, "dangling")
	if err := os.MkdirAll(dangling, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dangling, ".git"), []byte("gitdir: "+filepath.Join(dir, "gone")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{"agv1-lead-repo", filepath.Join(dir, "missing"), dir, dangling} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/workspaces/ws/v1/agents",
			strings.NewReader(`{"preset":"lead","name":"l","repo":"`+repo+`","base_ref":"main","overrides":{"harness":"opencode"}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "k-"+filepath.Base(repo))
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not the absolute path") {
			t.Errorf("Create repo %q = %d %s; want 400 naming the repo", repo, rec.Code, rec.Body)
		}
	}
}
