package agentwire

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agentmcp"
	"github.com/tysonthomas9/loomcli/internal/agentworktree"
	"github.com/tysonthomas9/loomcli/internal/gitrunner"
	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func workspaceMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(middleware.WithWorkspace(r.Context(), r.PathValue("ws"))))
	})
}

// TestGitHubReadCapabilityGate: serve's bridge wiring advertises github_read
// (HasGitHubRead) only when serve wired its host GitHub reader at Start;
// without one the lead still works, with no github_read. HasPublish stays false,
// so the gh and git push denies, which need both, are not compiled by this
// ticket (3.1 owns the joint activation).
func TestGitHubReadCapabilityGate(t *testing.T) {
	ctx := context.Background()
	lead, err := loomagent.BuiltinPresets{}.Get(ctx, "lead")
	if err != nil {
		t.Fatal(err)
	}
	task, _ := loomagent.BuiltinPresets{}.Get(ctx, "task")
	base := func() string { return "http://127.0.0.1:1" }
	if caps, err := bridge(base, false)(ctx, lead); err != nil || caps != (loomagent.BridgeCaps{}) {
		t.Errorf("no host reader: caps %+v, err %v; want the lead without github_read", caps, err)
	}
	if caps, err := bridge(base, false)(ctx, task); err != nil || caps != (loomagent.BridgeCaps{}) {
		t.Errorf("task: caps %+v, err %v; want none", caps, err)
	}
	if caps, err := bridge(base, true)(ctx, lead); err != nil || caps != (loomagent.BridgeCaps{HasGitHubRead: true}) {
		t.Errorf("host reader wired: caps %+v, err %v; want github_read only", caps, err)
	}
}

// opens records harness Opens: a launch reaching the harness.
type opens struct {
	loomharness.Harness
	n     atomic.Int32
	tools atomic.Value // the last Open's bridge tools
}

func (o *opens) Open(ctx context.Context, spec loomharness.OpenSpec) (loomharness.NativeRef, error) {
	o.n.Add(1)
	o.tools.Store(spec.Launch.Env[agentmcp.EnvTools])
	return o.Harness.Open(ctx, spec)
}

// TestServeLeadWithoutGitHubRead: with no host GitHub reader
// (Config.GitHubRead nil), as for a local user with no GitHub configured, a
// lead is created and its session opens, with its other bridge tools and no
// github_read, so nothing falls back to gh or an agent credential. The 1.3
// fake harness stands in for OpenCode.
func TestServeLeadWithoutGitHubRead(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo},
		{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil { //nolint:norawexec // a real repo for Create
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	api, err := Start(ctx, Config{Dir: filepath.Join(dir, "loom"), OpenCodeBin: filepath.Join(dir, "no-opencode"), APIBase: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Stop)
	st, err := loomstore.Open(ctx, filepath.Join(dir, "agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	wt, err := agentworktree.New(filepath.Join(dir, "worktrees"), agentworktree.TargetLocal, gitrunner.Exec{})
	if err != nil {
		t.Fatal(err)
	}
	h := &opens{Harness: fake.New()}
	cfg := serviceConfig(st, "ws", wt, nil, map[string]loomharness.Harness{"opencode": h})
	cfg.Bridge, cfg.Launch = bridge(api.APIBase, false), launch(api.APIBase, "ws", api.tokens, false) // Start's wiring with Config.GitHubRead nil
	_, err = loomagent.New(cfg).Create(ctx, loomagent.CreateRequest{Envelope: loomagent.Envelope{RequestID: "r1"}, Preset: "lead",
		Name: "l", Repo: repo, BaseRef: "main", FirstMessage: "hi", Overrides: loomagent.Overrides{Harness: "opencode"}})
	if err != nil {
		t.Fatalf("Create lead with no host GitHub reader: %v", err)
	}
	tools, _ := h.tools.Load().(string)
	if h.n.Load() == 0 || tools == "" || strings.Contains(tools, "github_read") {
		t.Fatalf("opens %d, bridge tools %q; want the lead opened with its tools and no github_read", h.n.Load(), tools)
	}
}
