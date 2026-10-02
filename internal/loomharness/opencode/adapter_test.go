package opencode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestMain lets the test binary play `opencode` for the supervisor tests.
// LOOM_FAKE_OPENCODE=serve answers --version and, for `serve --service`,
// behaves like OpenCode's service mode: it exits at once when a live service
// is registered (the incumbent), else listens on a free loopback port, keeps
// the password from $XDG_CONFIG_HOME/opencode/service.json or initializes an
// absent one there, registers {id, version, url, pid, password} in
// $XDG_STATE_HOME/opencode/service.json, serves /api/info and /api/model,
// and on SIGTERM removes the registration if it still owns it. =exit fails
// every serve; =hang never registers. Every fake process records its pid in
// $XDG_STATE_HOME/fake-pids so cleanup stops exactly those.
func TestMain(m *testing.M) {
	if mode := os.Getenv("LOOM_FAKE_OPENCODE"); mode != "" {
		os.Exit(fakeOpenCode(mode))
	}
	restartBackoff, maxBackoff, exitGrace = 10*time.Millisecond, 50*time.Millisecond, 300*time.Millisecond
	os.Exit(m.Run())
}

func fakeOpenCode(mode string) int {
	args := os.Args[1:]
	if slices.Contains(args, "--version") {
		fmt.Println(os.Getenv("LOOM_FAKE_OPENCODE_VERSION"))
		return 0
	}
	if mode == "exit" || !slices.Equal(args, []string{"serve", "--service"}) {
		return 1
	}
	state, config := os.Getenv("XDG_STATE_HOME"), os.Getenv("XDG_CONFIG_HOME")
	appendLine(filepath.Join(state, "fake-pids"), fmt.Sprint(os.Getpid()))
	if mode == "hang" {
		time.Sleep(time.Hour)
	}
	regFile := filepath.Join(state, "opencode", "service.json")
	var reg registration
	live := func() bool {
		b, err := os.ReadFile(regFile)
		return err == nil && json.Unmarshal(b, &reg) == nil && alive(reg.PID)
	}
	if live() {
		return 0 // the incumbent keeps the registration
	}
	// The lock plays the service's fixed port: one process wins it, and the
	// others exit once the winner registers (server-process.ts:143-156).
	_ = os.MkdirAll(filepath.Dir(regFile), 0o700)
	port, err := os.OpenFile(filepath.Join(state, "port.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return 1
	}
	if syscall.Flock(int(port.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		for range 50 {
			if live() {
				return 0
			}
			time.Sleep(100 * time.Millisecond)
		}
		return 1
	}
	_ = os.WriteFile(filepath.Join(state, "config-content"), []byte(os.Getenv("OPENCODE_CONFIG_CONTENT")), 0o600)
	var tokens []string
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_TOKEN_FILE", "LOOM_PR_GIT_PASSWORD",
		"OPENCODE_SERVER_PASSWORD", "OPENCODE_PASSWORD", "OPENCODE_DISABLE_PROJECT_CONFIG", "OPENCODE_CONFIG_PROJECT_DISABLE"} {
		if _, ok := os.LookupEnv(k); ok {
			tokens = append(tokens, k) // names only: test output never carries values
		}
	}
	_ = os.WriteFile(filepath.Join(state, "secret-names"), []byte(strings.Join(tokens, "\n")), 0o600)

	cfgFile := filepath.Join(config, "opencode", "service.json")
	cfg := map[string]any{}
	if b, err := os.ReadFile(cfgFile); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	pw, _ := cfg["password"].(string)
	if pw == "" {
		pw = fmt.Sprintf("fake-%d", os.Getpid())
		cfg["password"] = pw
		b, _ := json.Marshal(cfg)
		_ = os.MkdirAll(filepath.Dir(cfgFile), 0o700)
		_ = os.WriteFile(cfgFile, b, 0o600)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 1
	}
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
	version := os.Getenv("LOOM_FAKE_SERVICE_VERSION")
	if version == "" {
		version = "2.0.19"
	}
	reg = registration{ID: fmt.Sprintf("id-%d", os.Getpid()), Version: version, URL: "http://" + l.Addr().String(), PID: os.Getpid(), Password: pw}
	b, _ := json.Marshal(reg) //nolint:gosec // G117: the fake writes OpenCode's registration format; synthetic password.
	_ = os.WriteFile(regFile, b, 0o600)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)
	<-sig
	if cur, err := os.ReadFile(regFile); err == nil && string(cur) == string(b) {
		_ = os.Remove(regFile)
	}
	return 0
}

func appendLine(file, line string) {
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = fmt.Fprintln(f, line)
		_ = f.Close()
	}
}

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// fakeRoots makes the state and config roots of one fake user and stops
// every fake process started for them at cleanup.
func fakeRoots(t *testing.T) (state, config string) {
	t.Helper()
	state, config = t.TempDir(), t.TempDir()
	t.Cleanup(func() {
		b, _ := os.ReadFile(filepath.Join(state, "fake-pids"))
		for _, f := range strings.Fields(string(b)) {
			var pid int
			_, _ = fmt.Sscan(f, &pid)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return state, config
}

func fakeEnv(mode, version, state, config string, extra ...string) []string {
	return append(append(hostEnv(), extra...),
		"LOOM_FAKE_OPENCODE="+mode,
		"LOOM_FAKE_OPENCODE_VERSION="+version,
		"XDG_STATE_HOME="+state,
		"XDG_CONFIG_HOME="+config,
		"OPENCODE_SERVER_PASSWORD=inherited",
		`OPENCODE_CONFIG_CONTENT={"theme":"user","agents":{"mine":{"system":"user agent"}}}`,
	)
}

func fakeAdapter(t *testing.T, mode, version string, presets ...loomharness.PresetConfig) (*Adapter, string) {
	t.Helper()
	state, config := fakeRoots(t)
	a := New(Config{Bin: os.Args[0], Presets: presets, Worktrees: t.TempDir(), Env: fakeEnv(mode, version, state, config)})
	t.Cleanup(a.Stop)
	return a, state
}

// userService starts a fake service the way the user's own client would,
// outside any adapter, and waits for its registration.
func userService(t *testing.T, state, config string, extra ...string) registration {
	t.Helper()
	cmd := exec.Command(os.Args[0], "serve", "--service") //nolint:norawexec // the test binary plays the user's own service, outside the adapter.
	cmd.Env = fakeEnv("serve", "opencode v2.0.19", state, config, extra...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait() }()
	var reg registration
	waitFor(t, "user service registration", func() bool {
		b, err := os.ReadFile(filepath.Join(state, "opencode", "service.json"))
		return err == nil && json.Unmarshal(b, &reg) == nil && reg.PID == cmd.Process.Pid
	})
	return reg
}

func serverPID(a *Adapter) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reg.PID
}

// startedPID is the service this adapter started, or 0.
func startedPID(a *Adapter) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd == nil {
		return 0
	}
	return a.cmd.Process.Pid
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	waitWithin(t, 10*time.Second, what, ok)
}

func waitWithin(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(d); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestAdapterRefusesOldVersion(t *testing.T) {
	a, state := fakeAdapter(t, "serve", "opencode 1.18.32")
	h, err := a.Health(context.Background())
	if err != nil || h.OK || !strings.Contains(h.Warning, "harness_too_old") {
		t.Fatalf("Health = %+v, %v; want refused", h, err)
	}
	_, err = a.Models(context.Background())
	var old *loomharness.TooOldError
	if !errors.As(err, &old) || !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("Models err = %v; want TooOldError", err)
	}
	if _, err := os.Stat(filepath.Join(state, "fake-pids")); err == nil {
		t.Fatal("a service started below the minimum version")
	}
}

// TestAdapterStartsServiceWhenNoneRuns: with nothing registered, Loom starts
// `opencode serve --service` with its filtered environment, leaving the
// user's OPENCODE_CONFIG_CONTENT as it is (presets are files, not config); the
// service initializes the absent config password, and Loom uses the
// registered endpoint.
func TestAdapterStartsServiceWhenNoneRuns(t *testing.T) {
	ctx := context.Background()
	a, state := fakeAdapter(t, "serve", "opencode v2.0.19", loomharness.PresetConfig{Name: "lead", Persona: "be the lead"})
	if serverPID(a) != 0 {
		t.Fatal("service started before first use")
	}
	models, err := a.Models(ctx)
	if err != nil || len(models) != 1 || models[0].ID != "fake/m" {
		t.Fatalf("Models = %v, %v", models, err)
	}
	reg, ok := a.registered()
	if !ok || reg.PID != startedPID(a) || serverPID(a) != reg.PID {
		t.Fatalf("Loom does not use the service it started: registered %d, started %d, in use %d", reg.PID, startedPID(a), serverPID(a))
	}
	if base, pw := a.endpoint(); base != reg.URL || sha(pw) != sha(reg.Password) {
		t.Fatal("endpoint is not the registered url and password")
	}

	b, err := os.ReadFile(filepath.Join(state, "config-content"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"theme":"user","agents":{"mine":{"system":"user agent"}}}` {
		t.Fatalf("Loom changed OPENCODE_CONFIG_CONTENT: %s", b)
	}
}

// TestAdapterKeepsExistingServicePassword: the only write Loom's start
// causes is initializing an absent password; an existing one is kept.
func TestAdapterKeepsExistingServicePassword(t *testing.T) {
	state, config := fakeRoots(t)
	cfgFile := filepath.Join(config, "opencode", "service.json")
	_ = os.MkdirAll(filepath.Dir(cfgFile), 0o700)
	before := []byte(`{"password":"fixture-existing","port":4321}`)
	if err := os.WriteFile(cfgFile, before, 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(Config{Bin: os.Args[0], Env: fakeEnv("serve", "opencode v2.0.19", state, config)})
	t.Cleanup(a.Stop)
	if _, err := a.Models(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(cfgFile)
	if string(after) != string(before) {
		t.Fatal("the service config changed although it had a password")
	}
	if _, pw := a.endpoint(); sha(pw) != sha("fixture-existing") {
		t.Fatal("Loom does not use the existing service password")
	}
}

// TestAdapterReusesRegisteredService: a service the user started is used as
// is; Restart only reconnects and Stop leaves it running.
func TestAdapterReusesRegisteredService(t *testing.T) {
	ctx := context.Background()
	state, config := fakeRoots(t)
	user := userService(t, state, config)
	a := New(Config{Bin: os.Args[0], Env: fakeEnv("serve", "opencode v2.0.19", state, config)})
	t.Cleanup(a.Stop)
	if _, err := a.Models(ctx); err != nil {
		t.Fatal(err)
	}
	if serverPID(a) != user.PID || startedPID(a) != 0 {
		t.Fatalf("in use %d, started %d; want the user's %d and none started", serverPID(a), startedPID(a), user.PID)
	}
	if err := a.Restart(ctx); err != nil || !alive(user.PID) || serverPID(a) != user.PID {
		t.Fatalf("Restart = %v: user service alive %v, in use %d", err, alive(user.PID), serverPID(a))
	}
	a.Stop()
	if !alive(user.PID) {
		t.Fatal("Stop stopped the user's service")
	}
	if _, err := a.Models(ctx); !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("Models after Stop = %v; want ErrUnavailable", err)
	}
}

// TestAdapterRefusesWrongServiceVersion: a registered service of another
// version is refused with a clear error and left running and registered.
func TestAdapterRefusesWrongServiceVersion(t *testing.T) {
	state, config := fakeRoots(t)
	user := userService(t, state, config, "LOOM_FAKE_SERVICE_VERSION=2.0.18")
	regFile := filepath.Join(state, "opencode", "service.json")
	before, _ := os.ReadFile(regFile)
	a := New(Config{Bin: os.Args[0], Env: fakeEnv("serve", "opencode v2.0.19", state, config)})
	t.Cleanup(a.Stop)
	_, err := a.Models(context.Background())
	if !errors.Is(err, loomharness.ErrUnavailable) || !strings.Contains(err.Error(), "2.0.18") || !strings.Contains(err.Error(), "never stops or replaces") {
		t.Fatalf("Models = %v; want a clear wrong-version refusal", err)
	}
	after, _ := os.ReadFile(regFile)
	if !alive(user.PID) || string(after) != string(before) || startedPID(a) != 0 {
		t.Fatal("the wrong-version service was stopped, replaced or competed with")
	}
}

// TestAdapterIncumbentRace: adapters starting at once end up on one service.
func TestAdapterIncumbentRace(t *testing.T) {
	state, config := fakeRoots(t)
	var adapters []*Adapter
	for range 3 {
		a := New(Config{Bin: os.Args[0], Env: fakeEnv("serve", "opencode v2.0.19", state, config)})
		t.Cleanup(a.Stop)
		adapters = append(adapters, a)
	}
	var wg sync.WaitGroup
	errs := make([]error, len(adapters))
	for i, a := range adapters {
		wg.Add(1)
		go func() { defer wg.Done(); _, errs[i] = a.Models(context.Background()) }()
	}
	wg.Wait()
	reg, _ := adapters[0].registered()
	for i, a := range adapters {
		if errs[i] != nil || serverPID(a) != reg.PID {
			t.Fatalf("adapter %d: %v, in use %d; want the registered %d", i, errs[i], serverPID(a), reg.PID)
		}
	}
	// The losers exit once the winner registers.
	waitFor(t, "one service after the race", func() bool {
		live := 0
		b, _ := os.ReadFile(filepath.Join(state, "fake-pids"))
		for _, f := range strings.Fields(string(b)) {
			var pid int
			_, _ = fmt.Sscan(f, &pid)
			if alive(pid) {
				live++
			}
		}
		return live == 1 && alive(reg.PID)
	})
}

// TestAdapterOwnedServiceLifecycle: a crash of the service Loom started is
// recovered by starting another, Restart replaces only that owned service,
// and Loom's shutdown leaves it running.
func TestAdapterOwnedServiceLifecycle(t *testing.T) {
	ctx := context.Background()
	a, _ := fakeAdapter(t, "serve", "opencode v2.0.19")
	if _, err := a.Models(ctx); err != nil {
		t.Fatal(err)
	}
	pid := serverPID(a)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a new service after the crash", func() bool { p := serverPID(a); return p != 0 && p != pid })
	if _, err := a.Models(ctx); err != nil {
		t.Fatalf("Models after crash: %v", err)
	}

	pid = serverPID(a)
	if err := a.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	if alive(pid) || serverPID(a) == pid || serverPID(a) == 0 {
		t.Fatalf("Restart of the owned service: old alive %v, in use %d", alive(pid), serverPID(a))
	}

	pid = serverPID(a)
	a.Stop()
	if !alive(pid) {
		t.Fatal("Stop stopped the service Loom started; it must keep running for the user")
	}
	if reg, ok := a.registered(); !ok || reg.PID != pid {
		t.Fatal("Stop changed the registration")
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

func TestAdapterCancelledStart(t *testing.T) {
	a, state := fakeAdapter(t, "hang", "opencode v2.0.19")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { // cancel once the service process runs, mid-start
		waitFor(t, "the started service", func() bool { _, err := os.Stat(filepath.Join(state, "fake-pids")); return err == nil })
		cancel()
	}()
	if _, err := a.Models(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Models = %v; want the cancelled start", err)
	}
	if serverPID(a) != 0 {
		t.Fatal("a service that never registered is in use")
	}
	// The next call waits for the service the cancelled call started; it
	// never starts a second one beside it.
	short, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if _, err := a.Models(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Models after a cancelled start = %v; want to keep waiting", err)
	}
	if b, _ := os.ReadFile(filepath.Join(state, "fake-pids")); len(strings.Fields(string(b))) != 1 {
		t.Fatalf("services started: %q; want exactly one", b)
	}
}

func TestOpenCodeServeStripsGitHubTokens(t *testing.T) {
	for _, presets := range [][]loomharness.PresetConfig{nil, {{Name: "lead", Persona: "p"}}} {
		state, config := fakeRoots(t)
		a := New(Config{Bin: os.Args[0], Presets: presets, Worktrees: t.TempDir(), Env: fakeEnv("serve", "opencode v2.0.19", state, config,
			"GITHUB_TOKEN=fixture-ghp", "GH_TOKEN=fixture-gho", "GH_ENTERPRISE_TOKEN=fixture-ghe",
			"GITHUB_TOKEN_FILE=/tmp/fixture-token", "LOOM_PR_GIT_PASSWORD=fixture-pr", "OPENCODE_PASSWORD=fixture-pw",
			"OPENCODE_DISABLE_PROJECT_CONFIG=1", "OPENCODE_CONFIG_PROJECT_DISABLE=1", "LOOM_KEEP=1")})
		t.Cleanup(a.Stop)
		if _, err := a.Models(context.Background()); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(state, "secret-names"))
		if err != nil || len(b) != 0 {
			t.Fatalf("opencode serve --service saw secret names %q (%v)", b, err)
		}
		env, err := a.env()
		if err != nil || !slices.Contains(env, "LOOM_KEEP=1") {
			t.Fatalf("other env dropped: %v", err)
		}
	}
}

// TestAdapterPresetFiles: Loom keeps exactly its presets as
// <worktrees>/.opencode/agent/loom-<name>.md, rewrites one only when it
// changes, removes loom-* files it no longer defines, and never touches
// other files there.
func TestAdapterPresetFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".opencode", "agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"mine.md": "user agent", "loom-old.md": "stale", "notes.txt": "x"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	state, config := fakeRoots(t)
	a := New(Config{Bin: os.Args[0], Worktrees: root, Env: fakeEnv("serve", "opencode v2.0.19", state, config),
		Presets: []loomharness.PresetConfig{{Name: "lead", Persona: "be the lead"}, {Name: "reviewer", Persona: "review"}}})
	t.Cleanup(a.Stop)
	if _, err := a.Models(context.Background()); err != nil {
		t.Fatal(err)
	}
	files := func() map[string]string {
		out := map[string]string{}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			out[e.Name()] = string(b)
		}
		return out
	}
	got := files()
	if got["loom-lead.md"] != "---\ndescription: Loom preset lead\nmode: primary\n---\nbe the lead\n" || got["loom-reviewer.md"] == "" ||
		got["mine.md"] != "user agent" || got["notes.txt"] != "x" || got["loom-old.md"] != "" || len(got) != 4 {
		t.Fatalf("preset files after start = %q", got)
	}
	lead, _ := os.Stat(filepath.Join(dir, "loom-lead.md"))

	if err := a.SetPresets([]loomharness.PresetConfig{{Name: "lead", Persona: "be the lead"}, {Name: "added", Persona: "new"}}); err != nil {
		t.Fatal(err)
	}
	got = files()
	if got["loom-added.md"] == "" || got["loom-reviewer.md"] != "" || got["mine.md"] != "user agent" || len(got) != 4 {
		t.Fatalf("preset files after change = %q", got)
	}
	if !a.definesPreset("loom-added") || a.definesPreset("loom-reviewer") {
		t.Fatal("definesPreset does not follow SetPresets")
	}
	if same, _ := os.Stat(filepath.Join(dir, "loom-lead.md")); !os.SameFile(lead, same) {
		t.Fatal("an unchanged preset file was rewritten")
	}

	if err := a.SetPresets([]loomharness.PresetConfig{{Name: "../escape", Persona: "x"}}); err == nil {
		t.Fatal("SetPresets accepted a name that is not a file name")
	}
	b := New(Config{Bin: os.Args[0], Env: fakeEnv("serve", "opencode v2.0.19", state, config), Presets: []loomharness.PresetConfig{{Name: "lead"}}})
	t.Cleanup(b.Stop)
	if _, err := b.Models(context.Background()); !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("Models with presets and no worktrees root = %v; want ErrUnavailable", err)
	}
}

// TestAdapterAcceptsNewerVersion: 2.0.19 or newer is fine (R22; Tyson 18:41
// UTC). A 2.0.20 binary starts a service, and a 2.0.20 registered service is
// reused by a 2.0.19 binary. Older is refused (TestAdapterRefusesOldVersion,
// TestAdapterRefusesWrongServiceVersion).
func TestAdapterAcceptsNewerVersion(t *testing.T) {
	ctx := context.Background()
	a, _ := fakeAdapter(t, "serve", "opencode v2.0.20")
	if h, err := a.Health(ctx); err != nil || !h.OK {
		t.Fatalf("Health = %+v, %v; want OK", h, err)
	}
	if _, err := a.Models(ctx); err != nil || startedPID(a) == 0 {
		t.Fatalf("Models with a 2.0.20 binary = %v; want a started service", err)
	}

	state, config := fakeRoots(t)
	user := userService(t, state, config, "LOOM_FAKE_SERVICE_VERSION=2.0.20")
	b := New(Config{Bin: os.Args[0], Env: fakeEnv("serve", "opencode v2.0.19", state, config)})
	t.Cleanup(b.Stop)
	if _, err := b.Models(ctx); err != nil || serverPID(b) != user.PID {
		t.Fatalf("Models with a 2.0.20 service = %v (in use %d); want it reused", err, serverPID(b))
	}
}
