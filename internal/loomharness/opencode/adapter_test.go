package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestMain lets the test binary play `opencode` for the supervisor tests:
// LOOM_FAKE_OPENCODE=serve answers --version and serves /api/info and
// /api/model like plain `opencode serve`, checking OPENCODE_SERVER_PASSWORD;
// =exit fails every serve, and --service is refused. serve,
// exit-child and hang first start a detached grandchild (=sleep), as
// OpenCode does for shell commands, and record its pid in children.
func TestMain(m *testing.M) {
	if mode := os.Getenv("LOOM_FAKE_OPENCODE"); mode != "" {
		os.Exit(fakeOpenCode(mode))
	}
	restartBackoff, maxBackoff, trackEvery = 10*time.Millisecond, 50*time.Millisecond, 20*time.Millisecond
	os.Exit(m.Run())
}

func fakeOpenCode(mode string) int {
	args := os.Args[1:]
	if slices.Contains(args, "--version") {
		fmt.Println(os.Getenv("LOOM_FAKE_OPENCODE_VERSION"))
		return 0
	}
	if mode == "sleep" {
		time.Sleep(time.Hour)
		return 0
	}
	if mode == "exit" || slices.Contains(args, "--service") {
		return 1
	}
	state := os.Getenv("XDG_STATE_HOME")
	spawnDetached(state)
	switch mode {
	case "exit-child":
		time.Sleep(300 * time.Millisecond) // a child born just before a crash can escape tracking
		return 1
	case "hang":
		time.Sleep(time.Hour)
	}
	_ = os.WriteFile(filepath.Join(state, "config-content"), []byte(os.Getenv("OPENCODE_CONFIG_CONTENT")), 0o600)
	var tokens []string
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_TOKEN_FILE", "LOOM_PR_GIT_PASSWORD"} {
		if v, ok := os.LookupEnv(k); ok {
			tokens = append(tokens, k+"="+v)
		}
	}
	_ = os.WriteFile(filepath.Join(state, "github-tokens"), []byte(strings.Join(tokens, "\n")), 0o600)
	l, err := net.Listen("tcp", "127.0.0.1:"+args[slices.Index(args, "--port")+1])
	if err != nil {
		return 1
	}
	pw := os.Getenv("OPENCODE_SERVER_PASSWORD")
	_ = os.WriteFile(filepath.Join(state, "passwords"), []byte(pw+"\n"), 0o600)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		if _, got, _ := r.BasicAuth(); got != pw {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"pid": os.Getpid()})
	})
	mux.HandleFunc("GET /api/model", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"m","providerID":"fake","name":"M"}]}`))
	})
	go func() { _ = http.Serve(l, mux) }() //nolint:gosec // G114: test-only fake server.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)
	<-sig
	return 0
}

// spawnDetached starts a grandchild in its own session, so it leaves the
// server's process group.
func spawnDetached(state string) {
	env := append(slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "LOOM_FAKE_OPENCODE=") }), "LOOM_FAKE_OPENCODE=sleep")
	p, err := os.StartProcess(os.Args[0], os.Args[:1], &os.ProcAttr{Env: env, Sys: &syscall.SysProcAttr{Setsid: true}})
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(state, "children"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = fmt.Fprintln(f, p.Pid)
		_ = f.Close()
	}
}

// children reads the grandchild pids a fake server recorded.
func children(t *testing.T, state string) []int {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(state, "children"))
	var pids []int
	for _, f := range strings.Fields(string(b)) {
		var pid int
		_, _ = fmt.Sscan(f, &pid)
		pids = append(pids, pid)
	}
	return pids
}

func liveCount(pids []int) int {
	n := 0
	for _, p := range pids {
		if alive(p) {
			n++
		}
	}
	return n
}

func fakeAdapter(t *testing.T, mode, version string, presets ...loomharness.PresetConfig) (*Adapter, string) {
	t.Helper()
	return fakeAdapterEnv(t, mode, version, nil, presets...)
}

func fakeAdapterEnv(t *testing.T, mode, version string, extra []string, presets ...loomharness.PresetConfig) (*Adapter, string) {
	t.Helper()
	state := t.TempDir()
	a := New(Config{Bin: os.Args[0], Presets: presets, Env: append(append(os.Environ(), extra...),
		"LOOM_FAKE_OPENCODE="+mode,
		"LOOM_FAKE_OPENCODE_VERSION="+version,
		"XDG_STATE_HOME="+state,
		"OPENCODE_SERVER_PASSWORD=inherited",
		`OPENCODE_CONFIG_CONTENT={"theme":"user","agents":{"mine":{"system":"user agent"}}}`,
	)})
	t.Cleanup(a.Stop)
	return a, state
}

func serverPID(a *Adapter) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd == nil {
		return 0
	}
	return a.cmd.Process.Pid
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

func TestAdapterRefusesOldVersion(t *testing.T) {
	a, _ := fakeAdapter(t, "serve", "opencode 1.18.32")
	h, err := a.Health(context.Background())
	if err != nil || h.OK || !strings.Contains(h.Warning, "harness_too_old") {
		t.Fatalf("Health = %+v, %v; want refused", h, err)
	}
	_, err = a.Models(context.Background())
	var old *loomharness.TooOldError
	if !errors.As(err, &old) || !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("Models err = %v; want TooOldError", err)
	}
	if serverPID(a) != 0 {
		t.Fatal("a server started below the minimum version")
	}
}

func TestAdapterLifecycle(t *testing.T) {
	ctx := context.Background()
	a, state := fakeAdapter(t, "serve", "opencode v2.0.19", loomharness.PresetConfig{Name: "lead", Persona: "be the lead"})
	if serverPID(a) != 0 {
		t.Fatal("server started before first use")
	}
	models, err := a.Models(ctx)
	if err != nil || len(models) != 1 || models[0].ID != "fake/m" {
		t.Fatalf("Models = %v, %v", models, err)
	}
	pid := serverPID(a)
	base, pw := a.endpoint()
	served, _ := os.ReadFile(filepath.Join(state, "passwords"))
	if !strings.HasPrefix(base, "http://127.0.0.1:") || len(pw) < 40 || string(served) != pw+"\n" {
		t.Fatalf("endpoint = %s; per-boot password set through OPENCODE_SERVER_PASSWORD: %v", base, string(served) == pw+"\n")
	}

	b, err := os.ReadFile(filepath.Join(state, "config-content"))
	if err != nil {
		t.Fatal(err)
	}
	var content struct {
		Theme  string `json:"theme"`
		Agents map[string]struct {
			System string `json:"system"`
			Mode   string `json:"mode"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(b, &content); err != nil {
		t.Fatal(err)
	}
	if content.Theme != "user" || content.Agents["mine"].System != "user agent" ||
		content.Agents["loom-lead"].System != "be the lead" || content.Agents["loom-lead"].Mode != "primary" {
		t.Fatalf("merged config content = %s", b)
	}

	// An exit restarts the server after the backoff.
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restart after crash", func() bool { p := serverPID(a); return p != 0 && p != pid })
	if _, err := a.Models(ctx); err != nil {
		t.Fatalf("Models after restart: %v", err)
	}

	pid = serverPID(a)
	if err := a.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	if alive(pid) || serverPID(a) == pid {
		t.Fatalf("Restart left %d running", pid)
	}
	if _, pw2 := a.endpoint(); pw2 == pw {
		t.Fatal("Restart reused the password; want a new one per boot")
	}

	// Stop signals only the owned process.
	other, _ := fakeAdapter(t, "serve", "opencode v2.0.19")
	if _, err := other.Models(ctx); err != nil {
		t.Fatal(err)
	}
	pid = serverPID(a)
	a.Stop()
	if alive(pid) || !alive(serverPID(other)) {
		t.Fatalf("Stop: ours alive=%v, other alive=%v", alive(pid), alive(serverPID(other)))
	}
	if _, err := a.Models(ctx); !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("Models after Stop = %v; want ErrUnavailable", err)
	}
}

func TestAdapterUnavailableAfterRepeatedFailures(t *testing.T) {
	ctx := context.Background()
	a, _ := fakeAdapter(t, "exit", "opencode v2.0.19")
	for i := 0; i < maxFailures; i++ {
		waitFor(t, "backoff to pass", func() bool { a.mu.Lock(); defer a.mu.Unlock(); return time.Now().After(a.retryAt) })
		if _, err := a.Models(ctx); !errors.Is(err, loomharness.ErrUnavailable) {
			t.Fatalf("Models = %v; want ErrUnavailable", err)
		}
	}
	h, err := a.Health(ctx)
	if err != nil || h.OK || !strings.Contains(h.Warning, "harness_unavailable") {
		t.Fatalf("Health = %+v, %v; want harness_unavailable", h, err)
	}
}

func TestOpenCodeServeStripsGitHubTokens(t *testing.T) {
	for _, presets := range [][]loomharness.PresetConfig{nil, {{Name: "lead", Persona: "p"}}} {
		a, state := fakeAdapterEnv(t, "serve", "opencode v2.0.19",
			[]string{"GITHUB_TOKEN=ghp_secret", "GH_TOKEN=gho_secret", "GH_ENTERPRISE_TOKEN=ghe_secret",
				"GITHUB_TOKEN_FILE=/tmp/token", "LOOM_PR_GIT_PASSWORD=pr_secret", "LOOM_KEEP=1"}, presets...)
		if _, err := a.Models(context.Background()); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(state, "github-tokens"))
		if err != nil || len(b) != 0 {
			t.Fatalf("opencode serve saw GitHub tokens %q (%v)", b, err)
		}
		env, err := a.env()
		if err != nil || !slices.Contains(env, "LOOM_KEEP=1") {
			t.Fatalf("other env dropped: %v", err)
		}
	}
}

func TestAdapterOwnedProcessTree(t *testing.T) {
	ctx := context.Background()
	a, state := fakeAdapter(t, "serve", "opencode v2.0.19")
	other, otherState := fakeAdapter(t, "serve", "opencode v2.0.19")
	for _, x := range []*Adapter{a, other} {
		if _, err := x.Models(ctx); err != nil {
			t.Fatal(err)
		}
	}
	first := children(t, state)
	if len(first) != 1 || !alive(first[0]) {
		t.Fatalf("grandchild %v not running", first)
	}

	// A crashed server leaves its detached grandchild behind; the restart
	// reaps it before starting a new tree.
	pid := serverPID(a)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restart after crash", func() bool { p := serverPID(a); return p != 0 && p != pid })
	waitFor(t, "crashed tree reaped", func() bool { return !alive(first[0]) })
	if got := children(t, state); len(got) != 2 || liveCount(got) != 1 {
		t.Fatalf("after crash restart: children %v, %d alive; want exactly one", got, liveCount(got))
	}

	if err := a.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	if got := children(t, state); len(got) != 3 || liveCount(got) != 1 || !alive(got[2]) {
		t.Fatalf("after Restart: children %v, %d alive; want only the newest", got, liveCount(got))
	}

	pid = serverPID(a)
	a.Stop()
	if alive(pid) || liveCount(children(t, state)) != 0 || len(a.owned()) != 0 {
		t.Fatalf("Stop left the owned tree running: server %v, %d children, owned %v", alive(pid), liveCount(children(t, state)), a.owned())
	}
	if !alive(serverPID(other)) || liveCount(children(t, otherState)) != 1 {
		t.Fatal("Stop touched another adapter's tree")
	}
}

func TestAdapterReapsFailedAndCancelledStarts(t *testing.T) {
	a, state := fakeAdapter(t, "exit-child", "opencode v2.0.19")
	if _, err := a.Models(context.Background()); !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("Models = %v; want ErrUnavailable", err)
	}
	if got := children(t, state); len(got) != 1 || liveCount(got) != 0 {
		t.Fatalf("failed start left children %v running", got)
	}

	b, state := fakeAdapter(t, "hang", "opencode v2.0.19")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := b.Models(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Models = %v; want the cancelled start", err)
	}
	if got := children(t, state); len(got) != 1 || liveCount(got) != 0 || len(b.owned()) != 0 {
		t.Fatalf("cancelled start left children %v (owned %v)", got, b.owned())
	}
}
