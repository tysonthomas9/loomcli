package claude

import (
	"context"
	"errors"
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
	caps, err := New(cfg).probeOnce(context.Background())
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
	if _, err := New(cfg).probeOnce(context.Background()); err == nil {
		t.Fatal("want an error")
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
	if _, ok := a.Capabilities(); ok {
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
		caps, ok := a.Capabilities()
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
