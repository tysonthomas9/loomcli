package agentwire

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

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
// (HasGitHubRead) only when serve wired its host GitHub reader at Start, and
// refuses a github_read preset clearly without one. HasPublish stays false,
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
	if caps, err := bridge(base, false)(ctx, lead); err == nil || caps.HasGitHubRead || !strings.Contains(err.Error(), "no host GitHub connector") {
		t.Errorf("no host reader: caps %+v, err %v; want a clear refusal", caps, err)
	}
	if caps, err := bridge(base, false)(ctx, task); err != nil || caps != (loomagent.BridgeCaps{}) {
		t.Errorf("task: caps %+v, err %v; want none", caps, err)
	}
	if caps, err := bridge(base, true)(ctx, lead); err != nil || caps != (loomagent.BridgeCaps{HasGitHubRead: true}) {
		t.Errorf("host reader wired: caps %+v, err %v; want github_read only", caps, err)
	}
}

// opens counts harness Opens: a launch reaching the harness.
type opens struct {
	loomharness.Harness
	n atomic.Int32
}

func (o *opens) Open(ctx context.Context, spec loomharness.OpenSpec) (loomharness.NativeRef, error) {
	o.n.Add(1)
	return o.Harness.Open(ctx, spec)
}

// TestServeRefusesBridgeAgentWithoutGitHubRead (R-G, serve half): serve's
// bridge wiring with no host GitHub reader (Config.GitHubRead nil) refuses a
// lead (its preset has github_read) before the harness opens a session, so
// no turn runs, and nothing falls back to gh or an agent credential. The
// 1.3 fake harness stands in for OpenCode.
func TestServeRefusesBridgeAgentWithoutGitHubRead(t *testing.T) {
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
	cfg.Bridge, cfg.Launch = bridge(api.APIBase, false), launch(api.APIBase, "ws", api.tokens) // Start's wiring with Config.GitHubRead nil
	_, err = loomagent.New(cfg).Create(ctx, loomagent.CreateRequest{Envelope: loomagent.Envelope{RequestID: "r1"}, Preset: "lead",
		Name: "l", Repo: repo, BaseRef: "main", FirstMessage: "hi", Overrides: loomagent.Overrides{Harness: "opencode"}})
	if err == nil || !strings.Contains(err.Error(), "github_read") {
		t.Fatalf("Create lead: %v; want a refusal naming github_read", err)
	}
	if n := h.n.Load(); n != 0 {
		t.Fatalf("the harness opened %d session(s) for a lead whose github_read bridge is not registered", n)
	}
}
