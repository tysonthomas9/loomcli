package claude

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// Real-Claude contract tests: LOOM_REAL_CLAUDE=1 runs them against the
// user's installed claude with the cheapest model. HOME, the config,
// project and transcript roots and the working directories all sit in the
// test binary's owned /tmp root (isolation_test.go); the user's local login
// reaches the child only as CLAUDE_CODE_OAUTH_TOKEN (see ownedLaunch).

type realRun struct {
	p      *Process
	frames chan Frame
	dir    string
	id     string
}

// hostEnv is this process's environment (whose HOME and CLAUDE_CONFIG_DIR
// TestMain already points at an owned /tmp home) without the variables of a Claude
// Code session the test may itself run inside, which would otherwise tie the
// child to that session, and without ANTHROPIC_API_KEY and
// ANTHROPIC_AUTH_TOKEN, which Claude passes on to its tools; the child's only
// credential is CLAUDE_CODE_OAUTH_TOKEN, which Claude withholds from them.
func hostEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "CLAUDECODE" || k == "ANTHROPIC_API_KEY" || k == "ANTHROPIC_AUTH_TOKEN" || k == "CLAUDE_PID" || k == "CLAUDE_EFFORT" || k == "CLAUDE_AGENT_SDK_VERSION" ||
			(strings.HasPrefix(k, "CLAUDE_CODE_") && k != "CLAUDE_CODE_OAUTH_TOKEN") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "DISABLE_AUTOUPDATER=1")
}

// realBin skips unless LOOM_REAL_CLAUDE=1 and claude is on PATH.
func realBin(t *testing.T) string {
	t.Helper()
	if os.Getenv("LOOM_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_REAL_CLAUDE=1 to run against the real claude")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not on PATH")
	}
	return bin
}

// ownedLaunch is a launch on a fresh owned config root inside the test
// binary's owned TMPDIR, never ~/.claude, logged in with the user's existing
// local Claude login passed only as CLAUDE_CODE_OAUTH_TOKEN.
func ownedLaunch(t *testing.T) loomharness.Launch {
	t.Helper()
	token := localLoginToken(t)
	root, err := os.MkdirTemp("", "claude-config-")
	if err != nil {
		t.Fatal(err)
	}
	return loomharness.Launch{Root: root, Env: map[string]string{"CLAUDE_CONFIG_DIR": root, "CLAUDE_CODE_OAUTH_TOKEN": token}}
}

// localLoginToken reads, read-only, the access token of the user's existing
// Claude login from the macOS login keychain item Claude Code stores it in
// ("Claude Code-credentials"). It never writes the keychain or ~/.claude,
// never refreshes the token, and never prints, logs or saves its value. It
// skips with a clear message when the login cannot be read or has expired;
// there is no fallback to ~/.claude.
func localLoginToken(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Skip("real Claude: cannot resolve the account for the local Claude login")
	}
	// Name the login keychain file: the test binary's HOME is the owned /tmp
	// root, so security's default search list would not find it.
	keychain := filepath.Join(u.HomeDir, "Library", "Keychains", "login.keychain-db")
	out, err := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-a", u.Username, "-w", keychain).Output()
	if err != nil {
		t.Skip("real Claude login: absent/unreadable from the login keychain (run `claude` once to log in); no fallback to ~/.claude")
	}
	var c struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"` // Unix ms
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(out, &c) != nil || c.ClaudeAiOauth.AccessToken == "" {
		t.Skip("real Claude login: absent/unreadable (no OAuth access token)")
	}
	if time.UnixMilli(c.ClaudeAiOauth.ExpiresAt).Before(time.Now().Add(15 * time.Minute)) {
		t.Skip("real Claude login: present, expires soon (within 15 minutes); run `claude` once to refresh it (tests never refresh or write it)")
	}
	return c.ClaudeAiOauth.AccessToken
}

// TestLocalLoginTokenPreflight is the $0 first step of a real run: it only
// reads the login token as the real tests do (owned HOME, explicit login
// keychain file) and reports its state, never the token. No claude launch.
// It skips with "absent/unreadable" or "expires soon" from localLoginToken.
func TestLocalLoginTokenPreflight(t *testing.T) {
	if os.Getenv("LOOM_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_REAL_CLAUDE=1 to read the local Claude login")
	}
	if localLoginToken(t) != "" {
		t.Log("real Claude login: present, expires in >15m")
	}
}

// authAccepted runs `claude auth status`, which takes no model turn, with the
// launch's own environment and working dir, and fails unless Claude reports
// itself logged in (exit 0). Its output is never printed. It shows only that
// Claude accepts the environment token locally; the first turn is what proves
// the API accepts it.
func authAccepted(t *testing.T, p *Process) {
	t.Helper()
	cmd := exec.Command(p.cfg.Bin, "auth", "status")
	cmd.Dir, cmd.Env = p.spec.Dir, p.env()
	if err := cmd.Run(); err != nil {
		t.Fatalf("real Claude: `claude auth status` with the environment token did not report a login: %v", err)
	}
}

// noTools is passed to every real launch except the nested test's: the child
// carries the user's login token, so it gets no built-in tools and no MCP
// servers. bashOnly gives the nested test the Bash tool and nothing else.
var (
	noTools  = []string{"--tools", "", "--strict-mcp-config"}
	bashOnly = []string{"--tools", "Bash", "--allowedTools", "Bash", "--strict-mcp-config"}
)

func startReal(t *testing.T, args []string) *realRun {
	t.Helper()
	bin := realBin(t)
	dir, err := os.MkdirTemp("", "loom-claude-contract-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	l := ownedLaunch(t)
	r := &realRun{frames: make(chan Frame, 100000), dir: dir, id: SessionID(uuid.NewString())}
	cfg := Config{Bin: bin, Env: hostEnv(), Args: args, OnFrame: func(f Frame) { r.frames <- f }}
	spec := ProcessSpec{SessionID: r.id, Launch: l, Dir: dir, Model: "haiku"}
	if err := isolated(cfg, spec); err != nil {
		t.Fatal(err)
	}
	r.p = NewProcess(cfg, spec)
	authAccepted(t, r.p)
	t.Cleanup(func() { _ = r.p.Close(context.Background()) })
	return r
}

// until returns the first frame matching ok and the frames before it.
func (r *realRun) until(t *testing.T, ok func(Frame) bool) (Frame, []Frame) {
	t.Helper()
	var before []Frame
	timeout := time.After(5 * time.Minute)
	for {
		select {
		case f := <-r.frames:
			if ok(f) {
				return f, before
			}
			before = append(before, f)
		case <-timeout:
			t.Fatal("timed out waiting for a frame")
		}
	}
}

func isType(typ string) func(Frame) bool { return func(f Frame) bool { return f.Type == typ } }

type resultFrame struct {
	Subtype   string `json:"subtype"`
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
}

func (r *realRun) prompt(t *testing.T, text string) (resultFrame, []Frame) {
	t.Helper()
	if err := r.p.Prompt(context.Background(), uuid.NewString(), text); err != nil {
		t.Fatal(err)
	}
	f, before := r.until(t, isType("result"))
	var res resultFrame
	if err := json.Unmarshal(f.Raw, &res); err != nil {
		t.Fatal(err)
	}
	return res, before
}

func (r *realRun) pid() int {
	r.p.mu.Lock()
	defer r.p.mu.Unlock()
	if r.p.cmd == nil {
		return 0
	}
	return r.p.cmd.Process.Pid
}

func TestContract(t *testing.T) {
	r := startReal(t, noTools)
	var pid int

	t.Run("Prompt", func(t *testing.T) {
		res, before := r.prompt(t, "Reply with exactly the word PONG and nothing else.")
		if res.Subtype != "success" || !strings.Contains(res.Result, "PONG") || res.SessionID != r.id {
			t.Fatalf("result = %+v, want PONG on session %s", res, r.id)
		}
		if !slices.ContainsFunc(before, isType("stream_event")) {
			t.Fatal("the turn did not stream")
		}
		if w := r.p.Warning(); w != "" {
			t.Logf("version warning: %s", w)
		}
		pid = r.pid()
	})

	t.Run("Interrupt", func(t *testing.T) {
		if err := r.p.Prompt(context.Background(), uuid.NewString(),
			"Write the numbers from one to three thousand as English words, one per line. Do not stop early."); err != nil {
			t.Fatal(err)
		}
		r.until(t, func(f Frame) bool {
			return f.Type == "stream_event" && strings.Contains(string(f.Raw), "content_block_delta")
		})
		ok, err := r.p.Interrupt(context.Background())
		if !ok || err != nil {
			t.Fatalf("interrupt = %v, %v", ok, err)
		}
		f, before := r.until(t, isType("result"))
		var res resultFrame
		_ = json.Unmarshal(f.Raw, &res)
		if res.Subtype == "success" {
			t.Fatalf("interrupted turn ended %s", f.Raw)
		}
		if !slices.ContainsFunc(before, isType("control_response")) {
			t.Fatal("the interrupt receipt did not come before the turn's result")
		}
		res, _ = r.prompt(t, "Reply with exactly the word AGAIN and nothing else.")
		if res.Subtype != "success" || !strings.Contains(res.Result, "AGAIN") {
			t.Fatalf("next prompt after interrupt = %+v", res)
		}
		if got := r.pid(); got == 0 || (pid != 0 && got != pid) {
			t.Fatalf("process changed across the interrupt: %d -> %d", pid, got)
		}
	})

	t.Run("Relaunch", func(t *testing.T) {
		before := r.pid()
		if err := r.p.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		res, _ := r.prompt(t, "Reply with exactly the word BACK and nothing else.")
		if res.Subtype != "success" || !strings.Contains(res.Result, "BACK") || res.SessionID != r.id {
			t.Fatalf("relaunched result = %+v, want BACK on session %s", res, r.id)
		}
		r.p.mu.Lock()
		args := r.p.cmd.Args
		r.p.mu.Unlock()
		if r.pid() == before || !slices.Contains(args, "--resume") || !slices.Contains(args, r.id) {
			t.Fatalf("relaunch must start a new process with --resume %s; args %v", r.id, args)
		}
	})
}

// TestClaudeNestedLaunchStripsGitHubTokens proves on the real claude that
// neither the Claude process nor a tool subprocess it runs receives a GitHub
// token, on first launch and on relaunch, while Claude's own auth and the
// host's credentials stay intact.
var strippedTokens = []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_TOKEN_FILE"}

// withheldAuth is Claude's own credentials, which must not reach its tools.
var withheldAuth = []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}

func TestClaudeNestedLaunchStripsGitHubTokens(t *testing.T) {
	for _, kv := range seededGitHubTokens {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	r := startReal(t, bashOnly)
	for i, name := range []string{"first.presence", "relaunch.presence"} {
		if i == 1 {
			if err := r.p.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		// The tool records only whether each variable is set, never a value:
		// the child's environment holds the login token.
		cmd := `for k in ` + strings.Join(append(slices.Clone(strippedTokens), withheldAuth...), " ") + `; do if printenv "$k" >/dev/null; then echo "$k=present"; else echo "$k=absent"; fi; done > ` + name
		res, _ := r.prompt(t, "Use the Bash tool to run exactly this command: "+cmd+"\nThen reply DONE.")
		if res.Subtype != "success" {
			t.Fatalf("launch %d: the turn ended %s", i, res.Subtype) // never the reply, which follows tool output
		}
		r.p.mu.Lock()
		spawned, args := r.p.cmd.Env, r.p.cmd.Args
		r.p.mu.Unlock()
		if i == 1 && !slices.Contains(args, "--resume") {
			t.Fatal("second launch was not a relaunch")
		}
		raw, err := os.ReadFile(filepath.Join(r.dir, name))
		if err != nil {
			t.Fatalf("launch %d: the tool did not write its report: %v", i, err)
		}
		nested := strings.Split(string(raw), "\n")
		for _, k := range strippedTokens {
			if _, ok := lookup(spawned, k); ok {
				t.Errorf("launch %d: %s reached the claude process", i, k)
			}
			if got, ok := lookup(nested, k); !ok {
				t.Errorf("launch %d: the tool did not report %s", i, k)
			} else if got != "absent" {
				t.Errorf("launch %d: %s reached the nested tool", i, k)
			}
		}
		for _, k := range withheldAuth { // fail closed: Claude's credentials must not reach a tool
			got, ok := lookup(nested, k)
			if k == "CLAUDE_CODE_OAUTH_TOKEN" && ok && (got == "present" || got == "absent") {
				t.Logf("launch %d: %s=%s in the nested tool", i, k, got) // presence only, never a value
			}
			if !ok || got != "absent" {
				t.Errorf("launch %d: %s was not reported absent in the nested tool", i, k)
			}
		}
		if got, _ := lookup(spawned, "CLAUDE_CODE_OAUTH_TOKEN"); got == "" || got != r.p.spec.Launch.Env["CLAUDE_CODE_OAUTH_TOKEN"] {
			t.Errorf("launch %d: Claude's own auth token was dropped", i)
		}
	}
	if os.Getenv("GH_TOKEN") != "gh-secret" || os.Getenv("GITHUB_TOKEN") != "github-secret" {
		t.Fatal("the host lost its GitHub credentials")
	}
}
