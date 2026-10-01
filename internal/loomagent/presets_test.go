package loomagent

import (
	"context"
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
	if c.Open.Name != "daemon-worker" || c.Open.Persona == "" || !slices.Equal(c.Rules, append(slices.Clone(allowAll), publishDenies...)) {
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
		"unknown model":        {"lead", CreateRequest{Overrides: Overrides{Model: "x"}}, []string{"m1"}},
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
	_, err := Resolve(mustPreset(t, "lead"), CreateRequest{Overrides: Overrides{Model: "x"}}, "opencode", []string{"m1", "m2"})
	if e := wantCode(t, err, CodePresetInvalid); !slices.Equal(e.Allowed, []string{"m1", "m2"}) {
		t.Fatalf("allowed = %v", e.Allowed)
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
	want = append(want, publishDenies...)
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
		req := CreateRequest{}
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
