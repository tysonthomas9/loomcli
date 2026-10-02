package agentwire

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentworktree"
	"github.com/tysonthomas9/loomcli/internal/gitrunner"
	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// TestFakeServeForE2E serves the real Agent API (loomagent service and
// agentsv1 routes, open mode, caller user:local) on the 1.3 fake harness under
// the names opencode, codex and claude, for the browser proof in
// internal/webui/frontend/tests/fake-agent. It is skipped unless
// LOOM_E2E_FAKE_ADDR (127.0.0.1:<port>) is set; playwright.fake-agent.config.ts
// starts it and stops it with a signal. Repo "demo" is a fresh git repo in a
// temp dir. It serves until SIGINT or SIGTERM, then shuts down cleanly.
func TestFakeServeForE2E(t *testing.T) {
	addr := os.Getenv("LOOM_E2E_FAKE_ADDR")
	if addr == "" {
		t.Skip("set LOOM_E2E_FAKE_ADDR=127.0.0.1:<port> to serve the Agent API on the fake harness")
	}
	if host, _, err := net.SplitHostPort(addr); err != nil || host != "127.0.0.1" {
		t.Fatalf("LOOM_E2E_FAKE_ADDR %q must be 127.0.0.1:<port>", addr)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dir := t.TempDir()
	repo := filepath.Join(dir, "demo")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo},
		{"-C", repo, "-c", "user.name=e2e", "-c", "user.email=e2e@example.com", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	st, err := loomstore.Open(ctx, filepath.Join(dir, "agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	wt, err := agentworktree.New(filepath.Join(dir, "worktrees"), agentworktree.TargetLocal, gitrunner.Exec{})
	if err != nil {
		t.Fatal(err)
	}
	harnesses := map[string]loomharness.Harness{"opencode": fake.New(), "codex": fake.New(), "claude": fake.New()}
	cfg := serviceConfig(st, "w1", wt, nil, harnesses)
	cfg.ResolveRepo = func(_ context.Context, _ loomagent.Target, name string) (string, error) {
		if name != "demo" {
			return "", fmt.Errorf("unknown repo %q", name)
		}
		return repo, nil
	}
	// No bridge is launched yet (2.2b), and the fake never calls tools, so a
	// stub bridge with no extra capabilities lets the lead preset start.
	cfg.Bridge = func(context.Context, loomagent.Preset) (loomagent.BridgeCaps, error) {
		return loomagent.BridgeCaps{}, nil
	}
	svc := loomagent.New(cfg)

	var wg sync.WaitGroup
	run := func(fn func(context.Context)) {
		wg.Add(1)
		go func() { defer wg.Done(); fn(ctx) }()
	}
	run(svc.RunDispatcher)
	for name := range harnesses {
		run(func(ctx context.Context) { svc.RunFeed(ctx, name) })
	}

	mux := http.NewServeMux()
	workspace := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.WithWorkspace(r.Context(), r.PathValue("ws"))))
		})
	}
	agentsv1.New(func(string) *loomagent.Service { return svc }, nil).Register(mux, workspace, nil)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve: %v", err)
		}
	}()
	t.Logf("Agent API on the fake harness at http://%s, repo demo", addr)

	<-ctx.Done()
	_ = srv.Close() // ends open event streams too
	wg.Wait()
	_ = st.Close()
}
