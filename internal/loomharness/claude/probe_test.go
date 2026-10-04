package claude

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

const fakeInit = `{"account":{"email":"me@example.com","subscriptionType":"max","tokenSource":"claude.ai","apiProvider":"firstParty"},
 "commands":[{"name":"review","description":"Review a PR","argumentHint":"<pr>"},{"name":"  ","description":"unnamed"},
 {"name":"Compact","description":"dup","argumentHint":"<focus>"},{"name":"review","description":"later"}]}`

// TestClaudeProbeReadsInitialize: the probe launches claude on stream-json
// with no prompt, no hooks, no MCP and no saved session, and without GitHub
// tokens, sends only initialize, and reads the account kind and label (never
// the email) and the commands, with compact first and duplicates merged.
func TestClaudeProbeReadsInitialize(t *testing.T) {
	f, cfg := newFixture(t, "2.1.285", append([]string{"LOOM_FAKE_CLAUDE_INIT=" + fakeInit}, seededGitHubTokens...)...)
	caps, err := New(cfg).probeOnce(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if caps.AccountKind != "subscription" || caps.AccountLabel != "Claude Max Subscription" || caps.ProbedAt.IsZero() {
		t.Fatalf("caps = %+v", caps)
	}
	want := []loomharness.SlashCommand{
		{Name: "compact", Description: "Summarize the conversation and reduce context usage", ArgumentHint: "<focus>"},
		{Name: "review", Description: "Review a PR", ArgumentHint: "<pr>"}}
	if !slices.Equal(caps.SlashCommands, want) {
		t.Fatalf("commands = %+v", caps.SlashCommands)
	}
	ls := launches(t, f.dumpPath)
	if len(ls) != 1 {
		t.Fatalf("launches = %d", len(ls))
	}
	args := strings.Join(ls[0].Args, " ")
	for _, w := range []string{"--input-format stream-json", "--setting-sources user", `--settings {"disableAllHooks":true}`,
		"--strict-mcp-config", "--no-session-persistence"} {
		if !strings.Contains(args, w) {
			t.Errorf("args %q lack %q", args, w)
		}
	}
	if i := slices.Index(ls[0].Args, "--setting-sources"); i < 0 || ls[0].Args[i+1] != "user" {
		t.Errorf("harness-level probe must read user settings only: %v", ls[0].Args)
	}
	if slices.Contains(ls[0].Args, "--session-id") || !slices.Contains(ls[0].Env, "ENABLE_CLAUDEAI_MCP_SERVERS=false") {
		t.Errorf("launch = %+v", ls[0])
	}
	for _, kv := range ls[0].Env {
		if strings.Contains(kv, "-secret") {
			t.Errorf("GitHub token reached the probe: %s", kv)
		}
	}
}

// TestClaudeProbeFails: claude dying before it answers is an error.
func TestClaudeProbeFails(t *testing.T) {
	_, cfg := newFixture(t, "2.1.285", "LOOM_FAKE_CLAUDE_INIT_FAIL=1")
	if _, err := New(cfg).probeOnce(context.Background(), ""); err == nil {
		t.Fatal("want an error")
	}
}

// TestClaudeProbeRefusalHidesCLIText: a refused initialize is an error that
// neither returns nor logs the CLI's error text, which could carry a
// credential; the loop logs only that the probe failed.
func TestClaudeProbeRefusalHidesCLIText(t *testing.T) {
	const secret = "sk-ant-oat01-SENTINEL"
	_, cfg := newFixture(t, "2.1.285", "LOOM_FAKE_CLAUDE_INIT_ERROR="+secret)
	a := New(cfg)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	var errs []error
	ctx, cancel := context.WithCancel(context.Background())
	a.probeLoop(ctx, time.Millisecond, func(ctx context.Context) (loomharness.Capabilities, error) {
		if len(errs) == 1 { // the first probe's failure is logged by now
			cancel()
			return loomharness.Capabilities{}, ctx.Err()
		}
		caps, err := a.probeOnce(ctx, "")
		errs = append(errs, err)
		return caps, err
	})
	if errs[0] == nil || strings.Contains(errs[0].Error(), secret) {
		t.Fatalf("probe error = %v", errs[0])
	}
	if !strings.Contains(buf.String(), "claude capability probe failed") || strings.Contains(buf.String(), secret) {
		t.Fatalf("log = %s", buf.String())
	}
	if _, ok := a.Capabilities(""); ok {
		t.Fatal("a refused probe must not report capabilities")
	}
}

// TestClaudeProbeAccountKinds follows T3's claudeAuthMetadata: an API key
// wins over a subscription, then the subscription, then Bedrock; anything
// else is unknown.
func TestClaudeProbeAccountKinds(t *testing.T) {
	for raw, want := range map[string][2]string{
		`{"account":{"tokenSource":"ANTHROPIC_API_KEY","subscriptionType":"pro"}}`: {"api_key", "Claude API Key"},
		`{"account":{"subscriptionType":"claude_max_20x_subscription"}}`:           {"subscription", "Claude Max 20x Subscription"},
		`{"account":{"subscriptionType":"team"}}`:                                  {"subscription", "Claude Team Subscription"},
		`{"account":{"subscriptionType":"ultra_plan"}}`:                            {"subscription", "Claude Ultra Plan Subscription"},
		`{"account":{"subscriptionType":"claude ultra"}}`:                          {"subscription", "Claude Ultra Subscription"},
		`{"account":{"apiProvider":"bedrock"}}`:                                    {"bedrock", "Amazon Bedrock"},
		`{"account":{"tokenSource":"none","apiProvider":"firstParty"}}`:            {"unknown", ""},
		`{}`: {"unknown", ""},
	} {
		caps, err := parseInit([]byte(raw), time.Now())
		if err != nil || caps.AccountKind != want[0] || caps.AccountLabel != want[1] || len(caps.SlashCommands) != 1 {
			t.Errorf("%s: %+v, %v; want %v", raw, caps, err, want)
		}
	}
	if _, err := parseInit([]byte(`[1]`), time.Now()); err == nil {
		t.Error("a malformed response must fail")
	}
}

// TestClaudeProbeLoop: the loop probes at once and then periodically, and a
// failed probe keeps the last good result; nothing is reported before the
// first good probe.
func TestClaudeProbeLoop(t *testing.T) {
	a := New(Config{})
	if _, ok := a.Capabilities(""); ok {
		t.Fatal("reported before any probe")
	}
	results := make(chan error)
	n := 0
	probe := func(ctx context.Context) (loomharness.Capabilities, error) {
		var err error
		select {
		case err = <-results:
		case <-ctx.Done():
			return loomharness.Capabilities{}, ctx.Err()
		}
		n++
		return loomharness.Capabilities{AccountKind: "subscription", AccountLabel: string(rune('A' + n - 1))}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.probeLoop(ctx, time.Millisecond, probe); close(done) }()
	step := func(err error, wantLabel string, wantOK bool) {
		t.Helper()
		results <- err
		results <- errors.New("sync") // the next probe has started, so this one is stored
		caps, ok := a.Capabilities("")
		if ok != wantOK || caps.AccountLabel != wantLabel {
			t.Fatalf("after probe: %+v %v, want %q %v", caps, ok, wantLabel, wantOK)
		}
	}
	step(errors.New("boom"), "", false) // probes 1-2 fail
	step(nil, "C", true)                // probe 3 succeeds, 4 fails
	step(errors.New("boom"), "C", true) // 5-6 fail: C kept
	step(nil, "G", true)                // probe 7 refreshes
	cancel()
	<-done
}

// TestClaudeProbeRepoReadsProjectSettings: a repo probe runs in the repo
// clone with user, project and local settings, as T3's does in its
// workspace, so the repo's own commands are listed; hooks and MCP stay off.
func TestClaudeProbeRepoReadsProjectSettings(t *testing.T) {
	f, cfg := newFixture(t, "2.1.285", "LOOM_FAKE_CLAUDE_INIT="+fakeInit)
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".claude", "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".claude", "commands", "deploy.md"), []byte("deploy"), 0o600); err != nil {
		t.Fatal(err)
	}
	caps, err := New(cfg).probeOnce(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(caps.SlashCommands, func(c loomharness.SlashCommand) bool { return c.Name == "deploy" }) {
		t.Fatalf("project command missing: %+v", caps.SlashCommands)
	}
	ls := launches(t, f.dumpPath)
	args := strings.Join(ls[0].Args, " ")
	if ls[0].Cwd != repo || !strings.Contains(args, "--setting-sources user,project,local") ||
		!strings.Contains(args, `--settings {"disableAllHooks":true}`) || !strings.Contains(args, "--strict-mcp-config") {
		t.Fatalf("launch = %s in %s", args, ls[0].Cwd)
	}
}

// TestClaudeCapabilitiesByRepo: a repo's first request starts its probe in
// the background and answers with the harness-level result; the repo's result
// follows once probed; another probe starts only 5 minutes after the last
// attempt, and its failure keeps the last good result; the cache keeps the 32
// most recently used repos.
func TestClaudeCapabilitiesByRepo(t *testing.T) {
	a := New(Config{})
	a.caps, a.probed = loomharness.Capabilities{AccountLabel: "harness"}, true
	type call struct {
		dir string
		res chan error
	}
	calls := make(chan call)
	a.probe = func(_ context.Context, dir string) (loomharness.Capabilities, error) {
		c := call{dir, make(chan error)}
		calls <- c
		return loomharness.Capabilities{AccountLabel: dir}, <-c.res
	}
	settle := func(dir string) {
		t.Helper()
		for i := 0; i < 1000; i++ {
			a.mu.Lock()
			running := a.repos[dir].running
			a.mu.Unlock()
			if !running {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("probe never finished")
	}
	if c, _ := a.Capabilities("/r"); c.AccountLabel != "harness" {
		t.Fatalf("before the repo probe = %+v", c)
	}
	c := <-calls
	if c.dir != "/r" {
		t.Fatalf("probed %q", c.dir)
	}
	if got, _ := a.Capabilities("/r"); got.AccountLabel != "harness" { // still running: no second probe
		t.Fatalf("while probing = %+v", got)
	}
	c.res <- nil
	settle("/r")
	if got, ok := a.Capabilities("/r"); !ok || got.AccountLabel != "/r" {
		t.Fatalf("after the repo probe = %+v %v", got, ok)
	}
	select {
	case c := <-calls:
		t.Fatalf("re-probed %q within 5 minutes", c.dir)
	case <-time.After(20 * time.Millisecond):
	}
	a.mu.Lock()
	a.repos["/r"].attempted = time.Now().Add(-probeEvery)
	a.mu.Unlock()
	a.Capabilities("/r")
	c = <-calls
	c.res <- errors.New("boom")
	settle("/r")
	if got, ok := a.Capabilities("/r"); !ok || got.AccountLabel != "/r" {
		t.Fatalf("a failed refresh must keep the last result: %+v %v", got, ok)
	}

	go func() {
		for c := range calls {
			c.res <- errors.New("boom")
		}
	}()
	for i := range probeRepos {
		a.Capabilities(filepath.Join("/x", string(rune('a'+i))))
	}
	a.mu.Lock()
	_, kept := a.repos["/r"]
	n := len(a.repos)
	a.mu.Unlock()
	if n != probeRepos || kept {
		t.Fatalf("cache holds %d repos, /r kept %v; want %d and the least recently used dropped", n, kept, probeRepos)
	}
}
