package loomagent

import (
	"context"
	"encoding/json"
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

func mustPreset(t *testing.T, name string) Preset {
	t.Helper()
	p, err := BuiltinPresets{}.Get(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func wantCode(t *testing.T, err error, code Code) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
	return e
}

func TestPresetsListAndGet(t *testing.T) {
	ps, err := BuiltinPresets{}.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range ps {
		names = append(names, p.Name)
		if !slices.Equal(p.Harnesses, Harnesses) {
			t.Errorf("%s harnesses = %v", p.Name, p.Harnesses)
		}
		if p.RoleKind == "worker" && slices.ContainsFunc(p.Rules, func(r loomharness.PermissionRule) bool { return r.Effect == "ask" }) {
			t.Errorf("background preset %s uses ask", p.Name)
		}
	}
	want := []string{"lead", "task", "pr-review-webhook", "pr-review-interactive", "daemon-worker"}
	if !slices.Equal(names, want) {
		t.Fatalf("presets = %v, want %v", names, want)
	}
	if p := mustPreset(t, "task@1"); p.Mode != "single_task" {
		t.Fatalf("task@1 mode = %s", p.Mode)
	}
	_, err = BuiltinPresets{}.Get(context.Background(), "task@2")
	wantCode(t, err, CodePresetNotFound)
	_, err = BuiltinPresets{}.Get(context.Background(), "nope")
	wantCode(t, err, CodePresetNotFound)
}

func TestPresetResolveConvertsToHarnessConfig(t *testing.T) {
	budget, dur := 2.5, 600
	req := CreateRequest{Overrides: Overrides{Model: "m1", Effort: "high", MaxBudgetUSD: &budget, MaxRunDuration: &dur}}
	c, err := Resolve(mustPreset(t, "daemon-worker"), req, "opencode", []string{"m1"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Harness != "opencode" || c.Model != "m1" || c.Effort != "high" || *c.MaxBudgetUSD != 2.5 || *c.MaxRunDuration != 600 {
		t.Fatalf("config = %+v", c)
	}
	if c.Open.Name != "daemon-worker" || c.Open.Persona == "" || !slices.Equal(c.Rules, allowAll) {
		t.Fatalf("open = %+v rules = %v", c.Open, c.Rules)
	}

	lead, err := Resolve(mustPreset(t, "lead"), CreateRequest{Overrides: Overrides{Harness: "claude"}}, "opencode", nil)
	if err != nil {
		t.Fatal(err)
	}
	if lead.Harness != "claude" || !slices.Contains(lead.Open.Tools, "agent_create") {
		t.Fatalf("lead = %+v", lead)
	}
}

func TestPresetResolveRefusesInvalidValues(t *testing.T) {
	neg, dur := -1.0, 5
	cases := map[string]struct {
		preset string
		req    CreateRequest
		models []string
	}{
		"unknown harness":      {"lead", CreateRequest{Overrides: Overrides{Harness: "gemini"}}, nil},
		"malformed model":      {"lead", CreateRequest{Overrides: Overrides{Model: "openai/"}}, []string{"m1"}},
		"negative budget":      {"task", CreateRequest{Overrides: Overrides{MaxBudgetUSD: &neg}}, nil},
		"duration not allowed": {"lead", CreateRequest{Overrides: Overrides{MaxRunDuration: &dur}}, nil},
		"persona not allowed":  {"pr-review-webhook", CreateRequest{Persona: &Persona{Text: "x"}}, nil},
		"tools not allowed":    {"task", CreateRequest{Overrides: Overrides{ReadOnly: true}}, nil},
		"persona both":         {"lead", CreateRequest{Persona: &Persona{Text: "x", File: "y"}}, nil},
		"persona missing":      {"lead", CreateRequest{Persona: &Persona{File: "/nonexistent/persona.md"}}, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Resolve(mustPreset(t, tc.preset), tc.req, "opencode", tc.models)
			wantCode(t, err, CodePresetInvalid)
		})
	}
	// MCS1: a model the catalog does not list passes, flagged unverified; an
	// empty (not ready) catalog flags it too, an unwired (nil) one does not.
	for _, tc := range []struct {
		models []string
		want   bool
	}{{[]string{"m1", "m2"}, true}, {[]string{"x"}, false}, {[]string{}, true}, {nil, false}} {
		c, err := Resolve(mustPreset(t, "lead"), CreateRequest{Overrides: Overrides{Model: "x"}}, "opencode", tc.models)
		if err != nil || c.Model != "x" || c.ModelUnverified != tc.want {
			t.Fatalf("catalog %v: model %q unverified=%v, %v; want unverified=%v", tc.models, c.Model, c.ModelUnverified, err, tc.want)
		}
	}
}

func TestPolicyRestrictionsFailClosed(t *testing.T) {
	ro := CreateRequest{Overrides: Overrides{ReadOnly: true, AllowedTools: []string{"read", "edit"}, DeniedTools: []string{"webfetch"}}}
	c, err := Resolve(mustPreset(t, "daemon-worker"), ro, "opencode", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Last match wins: read-only and denied tools come after the allow list.
	want := append(slices.Clone(allowAll),
		loomharness.PermissionRule{Action: "*", Resource: "*", Effect: "deny"},
		loomharness.PermissionRule{Action: "read", Resource: "*", Effect: "allow"},
		loomharness.PermissionRule{Action: "edit", Resource: "*", Effect: "allow"},
		loomharness.PermissionRule{Action: "edit", Resource: "*", Effect: "deny"},
		loomharness.PermissionRule{Action: "bash", Resource: "*", Effect: "deny"},
		loomharness.PermissionRule{Action: "webfetch", Resource: "*", Effect: "deny"})
	if !slices.Equal(c.Rules, want) {
		t.Fatalf("rules = %v", c.Rules)
	}

	for _, h := range []string{"codex", "claude"} {
		for _, req := range []CreateRequest{
			{Overrides: Overrides{Harness: h, ReadOnly: true}},
			{Overrides: Overrides{Harness: h, AllowedTools: []string{"read"}}},
			{Overrides: Overrides{Harness: h, DeniedTools: []string{"bash"}}},
		} {
			_, err := Resolve(mustPreset(t, "daemon-worker"), req, "opencode", nil)
			if e := wantCode(t, err, CodePresetInvalid); !slices.Equal(e.Allowed, []string{"opencode"}) {
				t.Fatalf("%s allowed = %v", h, e.Allowed)
			}
		}
		_, err := Resolve(mustPreset(t, "pr-review-webhook"), CreateRequest{Overrides: Overrides{Harness: h}}, "opencode", nil)
		wantCode(t, err, CodePresetInvalid)
		if _, err := Resolve(mustPreset(t, "task"), CreateRequest{Overrides: Overrides{Harness: h}}, "opencode", nil); err != nil {
			t.Fatalf("unrestricted task on %s: %v", h, err)
		}
	}
}

func TestPresetCustomPersonaIsCreateInput(t *testing.T) {
	c, err := Resolve(mustPreset(t, "lead"), CreateRequest{Persona: &Persona{Text: "be terse"}}, "opencode", nil)
	if err != nil || c.Open.Persona != "be terse" {
		t.Fatalf("text persona = %q, %v", c.Open.Persona, err)
	}
	f := filepath.Join(t.TempDir(), "persona.md")
	if err := os.WriteFile(f, []byte("from file"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = Resolve(mustPreset(t, "daemon-worker"), CreateRequest{Persona: &Persona{File: f}}, "opencode", nil)
	if err != nil || c.Open.Persona != "from file" {
		t.Fatalf("file persona = %q, %v", c.Open.Persona, err)
	}
	if err := os.WriteFile(f, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Resolve(mustPreset(t, "task"), CreateRequest{Persona: &Persona{File: f}}, "opencode", nil)
	wantCode(t, err, CodePresetInvalid)
}

func TestPresetPackageHasNoCLIImports(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		if strings.Contains(imp, "/internal/cli") {
			t.Errorf("loomagent imports CLI package %s", imp)
		}
	}
}

// decide evaluates rules last match wins, with * as a wildcard.
func decide(rules []loomharness.PermissionRule, action, resource string) string {
	glob := func(pat, s string) bool {
		re := "^" + strings.ReplaceAll(regexp.QuoteMeta(pat), `\*`, ".*") + "$"
		return regexp.MustCompile(re).MatchString(s)
	}
	effect := "deny"
	for _, r := range rules {
		if glob(r.Action, action) && glob(r.Resource, resource) {
			effect = r.Effect
		}
	}
	return effect
}

func TestPolicyDeniesGHAndGitPush(t *testing.T) {
	ps, _ := BuiltinPresets{}.List(context.Background())
	for _, p := range ps {
		req := CreateRequest{Bridge: BridgeCaps{HasGitHubRead: true, HasPublish: true}}
		if slices.Contains(p.Overridable, "tools") {
			req.Overrides.AllowedTools = []string{"bash"} // must not re-allow them
		}
		for _, h := range []string{"opencode", "codex"} {
			req.Overrides.Harness = h
			c, err := Resolve(p, req, "opencode", nil)
			if h == "codex" && p.Name != "lead" && p.Name != "task" {
				continue // restricted presets fail closed on codex; tested above
			}
			if err != nil {
				t.Fatalf("%s on %s: %v", p.Name, h, err)
			}
			for _, cmd := range []string{"gh pr create", "gh api repos/o/r", "git push", "git push origin main", "git push --force"} {
				if got := decide(c.Rules, "bash", cmd); got != "deny" {
					t.Errorf("%s on %s: bash %q = %s, want deny", p.Name, h, cmd, got)
				}
			}
			if p.Name != "pr-review-webhook" && p.Name != "pr-review-interactive" {
				if got := decide(c.Rules, "bash", "git status"); got != "allow" {
					t.Errorf("%s: bash git status = %s, want allow", p.Name, got)
				}
			}
		}
	}
}

func TestPolicyGHAndGitPushDeniesNeedBothBridgeCaps(t *testing.T) {
	hasDeny := func(rules []loomharness.PermissionRule) bool {
		return slices.ContainsFunc(rules, func(r loomharness.PermissionRule) bool { return slices.Contains(publishDenies, r) })
	}
	ps, _ := BuiltinPresets{}.List(context.Background())
	for _, p := range ps {
		for _, caps := range []BridgeCaps{{}, {HasGitHubRead: true}, {HasPublish: true}, {HasGitHubRead: true, HasPublish: true}} {
			c, err := Resolve(p, CreateRequest{Bridge: caps}, "opencode", nil)
			if err != nil {
				t.Fatalf("%s %+v: %v", p.Name, caps, err)
			}
			if want := caps.HasGitHubRead && caps.HasPublish; hasDeny(c.Rules) != want {
				t.Errorf("%s %+v: denies present = %v, want %v", p.Name, caps, !want, want)
			}
			if !slices.Equal(c.Rules[:len(p.Rules)], p.Rules) { // the preset's own denies are kept
				t.Errorf("%s %+v: preset rules = %v", p.Name, caps, c.Rules)
			}
		}
	}
	// Read-only and denied-tool denies stay in every case.
	ro := Overrides{ReadOnly: true, DeniedTools: []string{"webfetch"}}
	for _, caps := range []BridgeCaps{{}, {HasGitHubRead: true}, {HasPublish: true}, {HasGitHubRead: true, HasPublish: true}} {
		c, err := Resolve(mustPreset(t, "daemon-worker"), CreateRequest{Overrides: ro, Bridge: caps}, "opencode", nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range []string{"edit", "bash", "webfetch"} {
			if got := decide(c.Rules, a, "x"); got != "deny" {
				t.Errorf("%+v: %s = %s, want deny", caps, a, got)
			}
		}
	}
	lead, _ := Resolve(mustPreset(t, "lead"), CreateRequest{Bridge: BridgeCaps{HasPublish: true}}, "opencode", nil)
	if got := decide(lead.Rules, "bash", "gh pr create"); got != "allow" {
		t.Errorf("lead without github_read: bash gh = %s, want allow", got)
	}
}

func TestPolicyBridgeCapsNotSetFromJSON(t *testing.T) {
	var req CreateRequest
	body := `{"Preset":"lead","Bridge":{"HasGitHubRead":true,"HasPublish":true},"bridge":{"HasGitHubRead":true,"HasPublish":true}}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	if req.Preset != "lead" || req.Bridge != (BridgeCaps{}) {
		t.Fatalf("decoded request = %+v; Bridge must stay host-only", req)
	}
	out, err := json.Marshal(CreateRequest{Bridge: BridgeCaps{HasGitHubRead: true, HasPublish: true}})
	if err != nil || strings.Contains(string(out), "Bridge") || strings.Contains(string(out), "HasPublish") {
		t.Fatalf("encoded request = %s, %v", out, err)
	}
}

// TestPolicySubagentDeny (SA1): a lead delegates through Loom child agents,
// so its compiled policy ends with the subagent deny; task and
// daemon-worker keep the harness's own subagent tool. An existing lead whose
// stored preset still allowed it gets the deny from the current preset; a
// preset no longer served keeps its stored flag.
func TestPolicySubagentDeny(t *testing.T) {
	ctx := context.Background()
	s := newCreateEnv(t).service(ServiceConfig{})
	compiled := func(cfg Config) []loomharness.PermissionRule {
		t.Helper()
		rules, err := s.policy(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return rules
	}
	for _, c := range []struct {
		preset string
		deny   bool
	}{{"lead", true}, {"task", false}, {"daemon-worker", false}} {
		p := mustPreset(t, c.preset)
		cfg, err := Resolve(p, CreateRequest{Preset: c.preset, Overrides: Overrides{Harness: "opencode"}}, "opencode", nil)
		if err != nil {
			t.Fatal(err)
		}
		rules := compiled(cfg)
		last := rules[len(rules)-1] == subagentDeny
		has := slices.ContainsFunc(rules, func(r loomharness.PermissionRule) bool { return r.Action == "subagent" })
		if last != c.deny || has != c.deny || p.Subagents == c.deny {
			t.Errorf("%s: Subagents %v, rules %+v; want the subagent deny last = %v", c.preset, p.Subagents, rules, c.deny)
		}
	}
	stored := Config{Preset: Preset{Name: "lead", Subagents: true}, Rules: allowAll}
	if rules := compiled(stored); rules[len(rules)-1] != subagentDeny {
		t.Errorf("existing lead stored with Subagents: rules %+v; want the current preset's subagent deny", rules)
	}
	gone := Config{Preset: Preset{Name: "retired-preset", Subagents: true}, Rules: allowAll}
	if rules := compiled(gone); !slices.Equal(rules, allowAll) {
		t.Errorf("unserved preset with Subagents: rules %+v; want its stored rules only", rules)
	}
}

// TestLeadPersonaSummarizesOnce (CL2): the lead's persona tells it the
// completion notice is the result (no agent_get), to stay brief while other
// children run, and to write one combined summary when none are; it names
// no harness, so OpenCode and codex Leads read the same rule.
func TestLeadPersonaSummarizesOnce(t *testing.T) {
	p, err := BuiltinPresets{}.Get(context.Background(), "lead")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"You are a lead agent", "task_completed notice", "don't call agent_get",
		"still running", "one short line or not at all", "one combined summary"} {
		if !strings.Contains(p.Persona, want) {
			t.Errorf("lead persona lacks %q", want)
		}
	}
	for _, h := range Harnesses {
		if strings.Contains(strings.ToLower(p.Persona), h) {
			t.Errorf("lead persona names harness %q", h)
		}
	}
}
