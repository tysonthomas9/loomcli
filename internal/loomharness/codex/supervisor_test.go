package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestMain lets the test binary play `codex` for the supervisor tests when
// LOOM_FAKE_CODEX=1: --version prints LOOM_FAKE_CODEX_VERSION; app-server
// appends its spawn to LOOM_FAKE_CODEX_LOG and serves JSON-RPC on stdio
// until stdin closes. Methods: initialize (codexHome is its CODEX_HOME or
// ~/.codex), env (token names present, CODEX_HOME, LOOM_MARK), pid, exit
// (dies), and ask, which sends a server request for params.threadId and
// returns the answer it got back.
func TestMain(m *testing.M) {
	if os.Getenv("LOOM_FAKE_CODEX") == "1" {
		os.Exit(fakeCodex())
	}
	restartBackoff, maxBackoff = 10*time.Millisecond, 50*time.Millisecond
	os.Exit(m.Run())
}

func fakeCodex() int {
	if slices.Contains(os.Args[1:], "--version") {
		fmt.Println(os.Getenv("LOOM_FAKE_CODEX_VERSION"))
		return 0
	}
	if f, err := os.OpenFile(os.Getenv("LOOM_FAKE_CODEX_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_, _ = fmt.Fprintf(f, "%d %s\n", os.Getpid(), os.Getenv("CODEX_HOME"))
		_ = f.Close()
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		home = filepath.Join(os.Getenv("HOME"), ".codex")
	}
	in, out := bufio.NewScanner(os.Stdin), json.NewEncoder(os.Stdout)
	for in.Scan() {
		var req struct {
			ID     json.RawMessage
			Method string
			Params struct{ ThreadID string }
		}
		_ = json.Unmarshal(in.Bytes(), &req)
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]string{"codexHome": home, "platformFamily": "unix", "platformOs": "macos", "userAgent": "fake"}
		case "env":
			result = map[string]any{"tokens": presentTokens(), "codexHome": os.Getenv("CODEX_HOME"), "mark": os.Getenv("LOOM_MARK")}
		case "pid":
			result = os.Getpid()
		case "exit":
			return 1
		case "ask":
			_ = out.Encode(map[string]any{"id": "srv-1", "method": "item/tool/requestUserInput", "params": map[string]string{"threadId": req.Params.ThreadID}})
			in.Scan()
			result = json.RawMessage(in.Bytes())
		default:
			continue // a notification
		}
		_ = out.Encode(map[string]any{"id": req.ID, "result": result})
	}
	return 0
}

// presentTokens lists names only: test output never carries values.
func presentTokens() []string {
	out := []string{}
	for _, k := range githubTokens {
		if _, ok := os.LookupEnv(k); ok {
			out = append(out, k)
		}
	}
	return out
}

type fixture struct {
	dir, inherited, profile, log string
	env                          []string
}

func newFixture(t *testing.T, version string) fixture {
	t.Helper()
	dir := t.TempDir()
	f := fixture{dir: dir, inherited: filepath.Join(dir, "inherited"), profile: filepath.Join(dir, "profile"), log: filepath.Join(dir, "spawns")}
	f.env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(dir, "home"), "CODEX_HOME=" + f.inherited,
		"LOOM_FAKE_CODEX=1", "LOOM_FAKE_CODEX_VERSION=" + version, "LOOM_FAKE_CODEX_LOG=" + f.log, "LOOM_MARK=kept"}
	for _, k := range githubTokens {
		f.env = append(f.env, k+"=synthetic-"+strings.ToLower(k))
	}
	return f
}

func (f fixture) spawns(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(f.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(strings.TrimSpace(string(b)))
}

func newSupervisor(t *testing.T, f fixture, unrouted func(string, Message)) *Supervisor {
	t.Helper()
	s := New(Config{Bin: os.Args[0], Env: f.env, Unrouted: unrouted})
	t.Cleanup(s.Stop)
	return s
}

// TestCodexAppServerStripsGitHubTokens seeds every GitHub token into loom
// serve's environment and checks the spawned app-servers for the shared
// inherited root and a profile root: neither gets any token, each gets its
// own CODEX_HOME, agents on one root share one process, and the parent
// environment keeps its credentials.
func TestCodexAppServerStripsGitHubTokens(t *testing.T) {
	f := newFixture(t, "codex-cli 0.157.1")
	s := newSupervisor(t, f, nil)
	ctx := context.Background()
	pids := map[string]int{}
	for _, tc := range []struct{ name, root, codexHome string }{
		{"inherited", "", f.inherited},
		{"profile", f.profile, s.Root(f.profile)},
	} {
		c, err := s.Conn(ctx, tc.root)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var env struct {
			Tokens          []string
			CodexHome, Mark string
		}
		if err := c.Call(ctx, "env", nil, &env); err != nil {
			t.Fatal(err)
		}
		if len(env.Tokens) != 0 || env.Mark != "kept" || env.CodexHome != tc.codexHome {
			t.Errorf("%s app-server env: tokens %v, LOOM_MARK %q, CODEX_HOME %q (want %q)", tc.name, env.Tokens, env.Mark, env.CodexHome, tc.codexHome)
		}
		var pid int
		_ = c.Call(ctx, "pid", nil, &pid)
		pids[tc.name] = pid
	}
	if pids["inherited"] == pids["profile"] {
		t.Fatal("the profile root shares the inherited root's app-server")
	}
	if again, _ := s.Conn(ctx, f.inherited); again != s.servers[s.Root("")].conn {
		t.Fatal("the inherited root by path started a second app-server")
	}
	if n := len(f.spawns(t)) / 2; n != 2 {
		t.Fatalf("spawned %d app-servers, want 2", n)
	}
	for _, k := range githubTokens {
		if !slices.ContainsFunc(f.env, func(kv string) bool { return strings.HasPrefix(kv, k+"=") }) {
			t.Fatalf("the parent environment lost %s", k)
		}
	}
}

// TestCodexVersionGate refuses a too-old codex before any spawn and warns
// about a newer, untested one.
func TestCodexVersionGate(t *testing.T) {
	old := newFixture(t, "codex-cli 0.150.0")
	_, err := newSupervisor(t, old, nil).Conn(context.Background(), "")
	var tooOld *loomharness.TooOldError
	if !errors.As(err, &tooOld) || !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("want TooOldError, got %v", err)
	}
	if spawns := old.spawns(t); len(spawns) != 0 {
		t.Fatalf("a too-old codex was spawned: %v", spawns)
	}

	newer := newFixture(t, "codex-cli 0.999.0")
	s := newSupervisor(t, newer, nil)
	if _, err := s.Conn(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	h, _ := s.Health(context.Background())
	if !h.OK || !strings.Contains(h.Warning, "newer than the last tested") {
		t.Fatalf("health %+v", h)
	}
}

// TestCodexServerRequestRoundTrip answers a server request from a real
// child process on the same connection.
func TestCodexServerRequestRoundTrip(t *testing.T) {
	s := newSupervisor(t, newFixture(t, "codex-cli 0.157.1"), nil)
	c, err := s.Conn(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	c.Route("thr", func(m Message) { _ = c.Respond(m.ID, map[string]string{"answer": "yes"}) })
	var got map[string]any
	if err := c.Call(context.Background(), "ask", map[string]string{"threadId": "thr"}, &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != "srv-1" || fmt.Sprint(got["result"]) != "map[answer:yes]" {
		t.Fatalf("app-server got %v", got)
	}
}

// TestCodexRestartGapOnlyThatRoot kills one root's app-server: only that
// root's threads get a gap, the other root keeps serving, and the next Conn
// starts a new process.
func TestCodexRestartGapOnlyThatRoot(t *testing.T) {
	f := newFixture(t, "codex-cli 0.157.1")
	unrouted := make(chan string, 4)
	s := newSupervisor(t, f, func(root string, m Message) {
		if m.Gap {
			unrouted <- root
		}
	})
	ctx := context.Background()
	a, errA := s.Conn(ctx, "")
	b, errB := s.Conn(ctx, f.profile)
	if errA != nil || errB != nil {
		t.Fatal(errA, errB)
	}
	gaps := make(chan string, 4)
	a.Route("ta", func(m Message) { gaps <- "ta" })
	b.Route("tb", func(m Message) { gaps <- "tb" })
	var oldPID int
	_ = a.Call(ctx, "pid", nil, &oldPID)

	if err := a.Call(ctx, "exit", nil, nil); !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("call on a dying server: %v", err)
	}
	if g := <-gaps; g != "ta" {
		t.Fatalf("gap for %s", g)
	}
	if root := <-unrouted; root != s.Root("") {
		t.Fatalf("unrouted gap for %s", root)
	}
	if err := b.Call(ctx, "pid", nil, new(int)); err != nil {
		t.Fatalf("the other root stopped serving: %v", err)
	}
	select {
	case g := <-gaps:
		t.Fatalf("unexpected gap for %s", g)
	case <-time.After(100 * time.Millisecond):
	}

	var c *Conn
	for deadline := time.Now().Add(5 * time.Second); c == nil; time.Sleep(20 * time.Millisecond) {
		c, _ = s.Conn(ctx, "")
		if time.Now().After(deadline) {
			t.Fatal("no restart")
		}
	}
	var newPID int
	if err := c.Call(ctx, "pid", nil, &newPID); err != nil || newPID == oldPID {
		t.Fatalf("restarted pid %d (old %d): %v", newPID, oldPID, err)
	}
}
