package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// The test binary doubles as a fake claude when LOOM_FAKE_CLAUDE=1.
func TestMain(m *testing.M) {
	if os.Getenv("LOOM_FAKE_CLAUDE") == "1" {
		fakeClaude()
		os.Exit(0)
	}
	cleanup := ownTestHome()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// fakeClaude speaks the stream-json frames the process reads: the user
// replay, system/init with capabilities, result, and the interrupt receipt
// before the interrupted turn's result. A prompt containing "hang" runs until
// interrupted. Each launch appends its args, env and a child process's env
// to LOOM_FAKE_CLAUDE_DUMP.
func fakeClaude() {
	args := os.Args[1:]
	if slices.Contains(args, "--version") {
		fmt.Println(os.Getenv("LOOM_FAKE_CLAUDE_VERSION") + " (Claude Code)")
		return
	}
	nested, _ := exec.Command("env").Output() //nolint:norawexec // a real child stands in for a Claude tool subprocess: its inherited env is what the test checks
	cwd, _ := os.Getwd()
	dump(map[string]any{"args": args, "env": os.Environ(), "nested": strings.Split(string(nested), "\n"), "cwd": cwd})
	root := os.Getenv("CLAUDE_CONFIG_DIR")
	if root == "" {
		root = os.Getenv("LOOM_FAKE_CLAUDE_ROOT")
	}
	var id string
	resume := false
	for i, a := range args {
		if a == "--session-id" || a == "--resume" {
			id, resume = args[i+1], a == "--resume"
		}
	}
	path := filepath.Join(root, "projects", "fake", id+".jsonl")
	if _, err := os.Stat(path); err == nil && !resume {
		fmt.Fprintf(os.Stderr, "Error: Session ID %s is already in use.\n", id)
		os.Exit(1)
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, nil, 0o600)
	out := json.NewEncoder(os.Stdout)
	inited, running, n := false, false, 0
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var in struct {
			Type, UUID string
			RequestID  string `json:"request_id"`
			Message    struct{ Content string }
		}
		_ = json.Unmarshal(sc.Bytes(), &in)
		switch in.Type {
		case "user":
			_ = out.Encode(map[string]any{"type": "user", "uuid": in.UUID, "isReplay": true})
			if !inited {
				inited = true
				_ = out.Encode(map[string]any{"type": "system", "subtype": "init", "session_id": id,
					"capabilities": []string{"interrupt_receipt_v1"}})
			}
			n++
			if strings.Contains(in.Message.Content, "hang") {
				running = true
				_ = out.Encode(map[string]any{"type": "stream_event", "event": map[string]any{"type": "ping"}})
				continue
			}
			fakeTurn(out, fmt.Sprintf("msg_%d", n), in.UUID, in.Message.Content)
			_ = out.Encode(map[string]any{"type": "result", "subtype": "success", "session_id": id})
		case "control_request":
			_ = out.Encode(map[string]any{"type": "control_response",
				"response": map[string]any{"subtype": "success", "request_id": in.RequestID}})
			if running {
				running = false
				_ = out.Encode(map[string]any{"type": "result", "subtype": "error_during_execution", "session_id": id})
			}
		}
	}
}

// fakeTurn emits one assistant message in the 2.1.285 partial-message shape.
// The prompt text selects extras: "tool", "task", "resume" and "die".
func fakeTurn(out *json.Encoder, msg, key, prompt string) {
	ev := func(e map[string]any) {
		_ = out.Encode(map[string]any{"type": "stream_event", "event": e, "user_message_uuids": []string{key}})
	}
	if strings.Contains(prompt, "resume") {
		_ = out.Encode(map[string]any{"type": "system", "subtype": "status", "resume_reason": "interrupted_turn"})
	}
	ev(map[string]any{"type": "message_start", "message": map[string]any{"id": msg}})
	if strings.Contains(prompt, "die") {
		os.Exit(3)
	}
	if strings.Contains(prompt, "tool") {
		ev(map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Bash"}})
		_ = out.Encode(map[string]any{"type": "assistant", "message": map[string]any{"id": msg, "content": []any{map[string]any{"type": "tool_use", "id": "toolu_1"}}}})
		_ = out.Encode(map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_1"}}}})
	}
	if strings.Contains(prompt, "task") {
		_ = out.Encode(map[string]any{"type": "system", "subtype": "task_started", "task_type": "local_agent", "task_id": "task_1"})
	}
	ev(map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "text", "text": ""}})
	ev(map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "text_delta", "text": "hi"}})
	_ = out.Encode(map[string]any{"type": "assistant", "message": map[string]any{"id": msg, "content": []any{map[string]any{"type": "text", "text": "hi"}}}})
	ev(map[string]any{"type": "content_block_stop", "index": 1})
}

func dump(v any) {
	f, err := os.OpenFile(os.Getenv("LOOM_FAKE_CLAUDE_DUMP"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_ = json.NewEncoder(f).Encode(v)
}

type launchDump struct {
	Args, Env, Nested []string
	Cwd               string
}

func launches(t *testing.T, path string) []launchDump {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []launchDump
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var d launchDump
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

func lookup(env []string, key string) (string, bool) {
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == key {
			return v, true
		}
	}
	return "", false
}

type fixture struct {
	root     string // the fake's config root; tests pass it as Launch.Root
	dumpPath string
	frames   chan Frame
	env      []string
}

// newFixture returns a fake-claude config with a clean base env plus extra.
func newFixture(t *testing.T, version string, extra ...string) (*fixture, Config) {
	t.Helper()
	f := &fixture{root: t.TempDir(), dumpPath: filepath.Join(t.TempDir(), "dump.jsonl"), frames: make(chan Frame, 1000)}
	f.env = append([]string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
		"LOOM_FAKE_CLAUDE=1", "LOOM_FAKE_CLAUDE_VERSION=" + version,
		"LOOM_FAKE_CLAUDE_DUMP=" + f.dumpPath, "LOOM_FAKE_CLAUDE_ROOT=" + f.root,
	}, extra...)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return f, Config{Bin: exe, Env: f.env, OnFrame: func(fr Frame) { f.frames <- fr }}
}

// next waits for the next frame of type typ, returning the frames before it.
func (f *fixture) next(t *testing.T, typ string) (Frame, []Frame) {
	t.Helper()
	var before []Frame
	for {
		select {
		case fr := <-f.frames:
			if fr.Type == typ {
				return fr, before
			}
			before = append(before, fr)
		case <-time.After(10 * time.Second):
			t.Fatalf("no %s frame", typ)
		}
	}
}

func TestClaudeProcessPromptInterruptRelaunch(t *testing.T) {
	f, cfg := newFixture(t, "2.1.285")
	id := SessionID("agent-1")
	if id != SessionID("agent-1") || id == SessionID("agent-2") {
		t.Fatal("SessionID must be stable per key")
	}
	p := newTestProcess(t, cfg, ProcessSpec{SessionID: id, Launch: loomharness.Launch{Root: f.root}, Dir: t.TempDir(), Model: "haiku"})
	ctx := context.Background()
	if ok, err := p.Interrupt(ctx); ok || err != nil {
		t.Fatalf("interrupt before launch = %v, %v", ok, err)
	}
	if got := launches(t, f.dumpPath); len(got) != 0 {
		t.Fatal("Open/NewProcess must not launch")
	}
	if err := p.Prompt(ctx, "k1", "hello"); err != nil {
		t.Fatal(err)
	}
	if r, _ := f.next(t, "result"); !strings.Contains(string(r.Raw), `"success"`) {
		t.Fatalf("result = %s", r.Raw)
	}
	if err := p.Prompt(ctx, "k2", "hang please"); err != nil {
		t.Fatal(err)
	}
	if err := p.Prompt(ctx, "k3", "x"); !errors.Is(err, loomharness.ErrBusy) {
		t.Fatalf("prompt during a turn = %v, want ErrBusy", err)
	}
	f.next(t, "stream_event")
	if ok, err := p.Interrupt(ctx); !ok || err != nil {
		t.Fatalf("interrupt = %v, %v", ok, err)
	}
	r, before := f.next(t, "result")
	if !strings.Contains(string(r.Raw), "error_during_execution") || !slices.ContainsFunc(before, func(fr Frame) bool { return fr.Type == "control_response" }) {
		t.Fatalf("want the receipt before the interrupted result; got %s after %v", r.Raw, before)
	}
	if err := p.Prompt(ctx, "k4", "next"); err != nil {
		t.Fatal(err)
	}
	f.next(t, "result")
	got := launches(t, f.dumpPath)
	if len(got) != 1 || !slices.Contains(got[0].Args, "--session-id") || !slices.Contains(got[0].Args, id) ||
		!slices.Contains(got[0].Args, "haiku") {
		t.Fatalf("want one --session-id launch, got %d: %v", len(got), got[0].Args)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Prompt(ctx, "k5", "again"); err != nil {
		t.Fatal(err)
	}
	f.next(t, "result")
	got = launches(t, f.dumpPath)
	if len(got) != 2 || !slices.Contains(got[1].Args, "--resume") || !slices.Contains(got[1].Args, id) {
		t.Fatalf("relaunch must --resume the same UUID, got %d launches", len(got))
	}
	_ = p.Close(ctx)
}

func TestClaudeSessionIDInUseResumes(t *testing.T) {
	f, cfg := newFixture(t, "2.1.285")
	id := SessionID("agent-in-use")
	path := filepath.Join(f.root, "projects", "fake", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Launch.Root does not show the transcript, so the first launch is
	// --session-id, which the fake refuses as already in use.
	p := newTestProcess(t, cfg, ProcessSpec{SessionID: id, Launch: loomharness.Launch{Root: t.TempDir()}, Dir: t.TempDir()})
	defer func() { _ = p.Close(context.Background()) }()
	if err := p.Prompt(context.Background(), "k1", "hello"); err != nil {
		t.Fatal(err)
	}
	f.next(t, "result")
	got := launches(t, f.dumpPath)
	if len(got) != 2 || !slices.Contains(got[0].Args, "--session-id") || !slices.Contains(got[1].Args, "--resume") {
		t.Fatalf("want --session-id then --resume, got %d launches", len(got))
	}
}

func TestClaudeVersionGate(t *testing.T) {
	f, cfg := newFixture(t, "2.1.200")
	p := newTestProcess(t, cfg, ProcessSpec{SessionID: SessionID("old"), Launch: loomharness.Launch{Root: f.root}, Dir: t.TempDir()})
	var tooOld *loomharness.TooOldError
	if err := p.Prompt(context.Background(), "k", "hi"); !errors.As(err, &tooOld) || !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("too-old claude = %v", err)
	}
	if got := launches(t, f.dumpPath); len(got) != 0 {
		t.Fatal("a too-old claude must refuse before launch")
	}

	f, cfg = newFixture(t, "2.1.999")
	p = newTestProcess(t, cfg, ProcessSpec{SessionID: SessionID("new"), Launch: loomharness.Launch{Root: f.root}, Dir: t.TempDir()})
	defer func() { _ = p.Close(context.Background()) }()
	if err := p.Prompt(context.Background(), "k", "hi"); err != nil {
		t.Fatal(err)
	}
	f.next(t, "result")
	if !strings.Contains(p.Warning(), "newer than the last tested") {
		t.Fatalf("warning = %q", p.Warning())
	}
}

// claudeOnPath puts a `claude` that reports version on PATH for the profile
// manifest's version probe.
func claudeOnPath(t *testing.T, version string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho '" + version + " (Claude Code)'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil { //nolint:gosec // G306: test shim must be executable.
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	supervisor.ResetHarnessVersionCache()
	t.Cleanup(supervisor.ResetHarnessVersionCache)
}

// writeProfile provisions and blesses .loom/agent-profiles/<key>/claude.
func writeProfile(t *testing.T, project, key, version string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(agentprofile.Dir(project, key), "claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := slices.Sorted(maps.Keys(files))
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(files[name]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := agentprofile.Fingerprint(dir, names)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(agentprofile.Manifest{Files: names, Fingerprint: sum, HarnessVersion: version + " (Claude Code)"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, agentprofile.ManifestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// launchEnv runs one prompt with l and returns the spawned process's env.
func launchEnv(t *testing.T, l loomharness.Launch, extra ...string) []string {
	t.Helper()
	f, cfg := newFixture(t, "2.1.285", extra...)
	p := newTestProcess(t, cfg, ProcessSpec{SessionID: SessionID(t.Name()), Launch: l, Dir: t.TempDir()})
	defer func() { _ = p.Close(context.Background()) }()
	if err := p.Prompt(context.Background(), "k", "hi"); err != nil {
		t.Fatal(err)
	}
	f.next(t, "result")
	return launches(t, f.dumpPath)[0].Env
}

func TestClaudeAgentProfileEnv(t *testing.T) {
	claudeOnPath(t, "2.1.285")
	project := t.TempDir()
	// The profile is keyed by the agent's fixed profile key, not its name, so
	// a renamed agent still selects its original profile.
	dir := writeProfile(t, project, "agent-key", "2.1.285", map[string]string{"settings.json": "{}"})

	t.Run("valid profile is passed to the process", func(t *testing.T) {
		l, err := LaunchFor(project, "agent-key")
		if err != nil {
			t.Fatal(err)
		}
		if l.Root != dir || l.Root != sessions.ClaudeConfigDirFor(project, "agent-key") {
			t.Fatalf("root %q, want the profile %q that transcript discovery resolves", l.Root, dir)
		}
		if v, _ := lookup(launchEnv(t, l, "CLAUDE_CONFIG_DIR=/user/own"), "CLAUDE_CONFIG_DIR"); v != dir {
			t.Fatalf("spawned CLAUDE_CONFIG_DIR = %q, want %q", v, dir)
		}
	})
	t.Run("absent profile inherits the user's login", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", "/user/own")
		l, err := LaunchFor(project, "renamed-or-other")
		if err != nil || len(l.Env) != 0 {
			t.Fatalf("absent profile = %+v, %v", l, err)
		}
		if l.Root != "/user/own" || l.Root != sessions.ClaudeConfigDirFor(project, "renamed-or-other") {
			t.Fatalf("root = %q", l.Root)
		}
		env := launchEnv(t, l, "CLAUDE_CONFIG_DIR=/user/own")
		if v, _ := lookup(env, "CLAUDE_CONFIG_DIR"); v != "/user/own" {
			t.Fatalf("inherited CLAUDE_CONFIG_DIR changed to %q", v)
		}
		if v, _ := lookup(env, "HOME"); v != os.Getenv("HOME") {
			t.Fatal("HOME must never be replaced")
		}
	})
	t.Run("invalid profile refuses", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"tampered":1}`), 0o600); err != nil {
			t.Fatal(err)
		}
		l, err := LaunchFor(project, "agent-key")
		if !errors.Is(err, agentprofile.ErrFingerprintMismatch) || l.Root != "" || l.Env != nil {
			t.Fatalf("invalid profile = %+v, %v; want a typed refusal, no fallback", l, err)
		}
	})
}

func TestClaudeOAuthTokenExport(t *testing.T) {
	claudeOnPath(t, "2.1.285")
	project := t.TempDir()
	writeProfile(t, project, "tok", "2.1.285", map[string]string{"settings.json": "{}"})
	if err := os.WriteFile(filepath.Join(agentprofile.Dir(project, "tok"), "claude", "oauth-token"), []byte("profile-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := LaunchFor(project, "tok")
	if err != nil {
		t.Fatal(err)
	}
	env := launchEnv(t, l, "CLAUDE_CODE_OAUTH_TOKEN=inherited-token")
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "CLAUDE_CODE_OAUTH_TOKEN=") {
			n++
		}
	}
	if v, _ := lookup(env, "CLAUDE_CODE_OAUTH_TOKEN"); v != "profile-token" || n != 1 {
		t.Fatalf("token = %q (%d assignments), want only the profile token", v, n)
	}

	writeProfile(t, project, "empty", "2.1.285", map[string]string{"settings.json": "{}"})
	if err := os.WriteFile(filepath.Join(agentprofile.Dir(project, "empty"), "claude", "oauth-token"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LaunchFor(project, "empty"); !errors.Is(err, supervisor.ErrProfileTokenUnreadable) {
		t.Fatalf("empty token file = %v, want ErrProfileTokenUnreadable", err)
	}
}

// TestClaudeLeadInheritedProfileRepair: the new launch path keeps the typed
// errors `loom lead`'s enforceLeadProfile maps to its repair hints (drift ->
// `loom doctor --fix`, token -> setup-profile-token.sh), and leaves an
// inherited user config root unverified and unchanged.
func TestClaudeLeadInheritedProfileRepair(t *testing.T) {
	claudeOnPath(t, "2.1.286")
	project := t.TempDir()
	writeProfile(t, project, "drift", "2.1.285", map[string]string{"settings.json": "{}"})
	if _, err := LaunchFor(project, "drift"); !errors.Is(err, supervisor.ErrProfileVersionDrift) {
		t.Fatalf("drifted profile = %v, want ErrProfileVersionDrift", err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "/operator/own")
	if l, err := LaunchFor(project, "lead"); err != nil || len(l.Env) != 0 || l.Root != "/operator/own" {
		t.Fatalf("inherited root = %+v, %v", l, err)
	}
}

// TestClaudeProfileHelpersAfterCutover: LaunchFor exports exactly what the
// shared ProfileHarnessEnv helper resolves, so the old spawn path and the new
// launch path cannot drift.
func TestClaudeProfileHelpersAfterCutover(t *testing.T) {
	claudeOnPath(t, "2.1.285")
	project := t.TempDir()
	writeProfile(t, project, "a", "2.1.285", map[string]string{"settings.json": "{}", "oauth-token": "tok"})
	dir, assignments, err := supervisor.ProfileHarnessEnv(project, "a", "claude")
	if err != nil {
		t.Fatal(err)
	}
	l, err := LaunchFor(project, "a")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for k, v := range l.Env {
		got = append(got, k+"="+v)
	}
	slices.Sort(got)
	slices.Sort(assignments)
	if l.Root != dir || !slices.Equal(got, assignments) {
		t.Fatalf("LaunchFor = %q %v, helper = %q %v", l.Root, got, dir, assignments)
	}
}

var seededGitHubTokens = []string{"GH_TOKEN=gh-secret", "GITHUB_TOKEN=github-secret", "GITHUB_TOKEN_FILE=/secret/file"}

func TestClaudeProcessStripsGitHubTokens(t *testing.T) {
	for _, kv := range seededGitHubTokens {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v) // the host keeps its credentials
	}
	extra := append([]string{"CLAUDE_CODE_OAUTH_TOKEN=claude-auth", "ANTHROPIC_API_KEY=user-key"}, seededGitHubTokens...)
	f, cfg := newFixture(t, "2.1.285", extra...)
	p := newTestProcess(t, cfg, ProcessSpec{SessionID: SessionID("strip"), Dir: t.TempDir(),
		Launch: loomharness.Launch{Root: f.root, Env: map[string]string{"GH_TOKEN": "from-launch"}}})
	ctx := context.Background()
	for i := range 2 { // first launch and relaunch
		if err := p.Prompt(ctx, fmt.Sprint("k", i), "hi"); err != nil {
			t.Fatal(err)
		}
		f.next(t, "result")
		if err := p.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got := launches(t, f.dumpPath)
	if len(got) != 2 || !slices.Contains(got[1].Args, "--resume") {
		t.Fatalf("want a launch and a relaunch, got %d", len(got))
	}
	for i, d := range got {
		for _, env := range [][]string{d.Env, d.Nested} {
			for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_TOKEN_FILE"} {
				if _, ok := lookup(env, k); ok {
					t.Errorf("launch %d: %s reached claude or its child", i, k)
				}
			}
			for _, kv := range []string{"CLAUDE_CODE_OAUTH_TOKEN=claude-auth", "ANTHROPIC_API_KEY=user-key"} {
				if !slices.Contains(env, kv) {
					t.Errorf("launch %d: Claude auth %s was dropped", i, kv)
				}
			}
		}
	}
	if os.Getenv("GH_TOKEN") != "gh-secret" || os.Getenv("GITHUB_TOKEN_FILE") != "/secret/file" {
		t.Fatal("the host lost its GitHub credentials")
	}
}
