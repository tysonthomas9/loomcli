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
// /api/model like a service-mode server; =exit fails every serve.
func TestMain(m *testing.M) {
	if mode := os.Getenv("LOOM_FAKE_OPENCODE"); mode != "" {
		os.Exit(fakeOpenCode(mode))
	}
	restartBackoff, maxBackoff = 10*time.Millisecond, 50*time.Millisecond
	os.Exit(m.Run())
}

func fakeOpenCode(mode string) int {
	args := os.Args[1:]
	if slices.Contains(args, "--version") {
		fmt.Println(os.Getenv("LOOM_FAKE_OPENCODE_VERSION"))
		return 0
	}
	if mode == "exit" || !slices.Contains(args, "--service") {
		return 1
	}
	state := os.Getenv("XDG_STATE_HOME")
	_ = os.WriteFile(filepath.Join(state, "config-content"), []byte(os.Getenv("OPENCODE_CONFIG_CONTENT")), 0o600)
	var tokens []string
	for _, k := range githubTokens {
		if v, ok := os.LookupEnv(k); ok {
			tokens = append(tokens, k+"="+v)
		}
	}
	_ = os.WriteFile(filepath.Join(state, "github-tokens"), []byte(strings.Join(tokens, "\n")), 0o600)
	l, err := net.Listen("tcp", "127.0.0.1:"+args[slices.Index(args, "--port")+1])
	if err != nil {
		return 1
	}
	pw := fmt.Sprintf("pw-%d", os.Getpid())
	reg, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "url": "http://" + l.Addr().String(), "password": pw})
	_ = os.MkdirAll(filepath.Join(state, "opencode"), 0o700)
	_ = os.WriteFile(filepath.Join(state, "opencode", "service.json"), reg, 0o600)
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
	if base, pw := a.endpoint(); !strings.HasPrefix(base, "http://127.0.0.1:") || pw != fmt.Sprintf("pw-%d", pid) {
		t.Fatalf("endpoint = %s (password from service.json: %v)", base, pw != "")
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
			[]string{"GITHUB_TOKEN=ghp_secret", "GH_TOKEN=gho_secret", "GH_ENTERPRISE_TOKEN=ghe_secret", "LOOM_KEEP=1"}, presets...)
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
