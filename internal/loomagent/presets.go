package loomagent

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// Harnesses every preset may run on (design v2 §7.1).
var Harnesses = []string{"opencode", "codex", "claude"}

// Preset is a built-in agent preset (design v2 §7.2, §4.13).
type Preset struct {
	Name           string
	Version        int
	Mode           string // persistent | single_task
	RoleKind       string // interactive | worker
	OwnerKind      string // user | parent | workspace
	ExternalKeyFmt string
	Persona        string
	Harnesses      []string
	// Rules are evaluated last match wins. Background presets never use ask.
	Rules       []loomharness.PermissionRule
	Tools       []string // Loom agent tools served by the bridge
	Subagents   bool     // the harness's own subagent tool is allowed; false denies it (subagentDeny)
	Overridable []string // persona, max_budget_usd, max_run_duration, tools
}

var allowAll = []loomharness.PermissionRule{
	{Action: "read", Resource: "*", Effect: "allow"},
	{Action: "edit", Resource: "*", Effect: "allow"},
	{Action: "bash", Resource: "*", Effect: "allow"},
}

// publishDenies end the compiled policy (last match wins) when the agent's
// bridge has both github_read and publish, so no preset or override re-allows
// them: such agents read GitHub and publish through Loom, not gh or git push.
// Defense in depth only, not a guarantee: a pattern match on the bash command
// misses `sh -c`, env or path prefixes (/usr/bin/gh), `git -C dir push`, git
// aliases, scripts and other tools. They don't trigger the fail-closed check,
// so a harness that can't enforce rules simply runs without them.
var publishDenies = []loomharness.PermissionRule{
	{Action: "bash", Resource: "gh *", Effect: "deny"},
	{Action: "bash", Resource: "git push*", Effect: "deny"},
}

// subagentDeny ends the compiled policy of a preset without Subagents, so
// its agent delegates through Loom child agents (agent_create), never the
// harness's own subagent tool, which runs inside its session with no Loom
// agent (design v2 §10). It follows allow rules, so OpenCode's Always grants
// never re-allow it (Session.install).
var subagentDeny = loomharness.PermissionRule{Action: "subagent", Resource: "*", Effect: "deny"}

var presets = []Preset{
	{Name: "lead", Version: 1, Mode: "persistent", RoleKind: "interactive", OwnerKind: "user",
		Persona: "You are a lead agent. You own one feature, work in your worktree and delegate to task agents. " +
			"Whenever you are asked to start, call or delegate to an agent, create a Loom agent with loom.agent_create.",
		Rules: allowAll, Tools: []string{"agent_create", "agent_list", "agent_get", "agent_send", "agent_archive", "github_read"},
		Overridable: []string{"persona", "max_budget_usd"}},
	{Name: "task", Version: 1, Mode: "single_task", RoleKind: "worker", OwnerKind: "parent", ExternalKeyFmt: "task:<ticket>",
		Persona: "You are a task agent. Do the brief in your worktree and commit the result.",
		Rules:   allowAll, Subagents: true, Overridable: []string{"persona", "max_budget_usd", "max_run_duration"}},
	{Name: "pr-review-webhook", Version: 1, Mode: "single_task", RoleKind: "worker", OwnerKind: "workspace", ExternalKeyFmt: "pr-review:<owner>/<repo>#<n>@<sha>",
		Persona: "You review a pull request at a pinned head and post the review with review_post.",
		Rules: []loomharness.PermissionRule{
			{Action: "*", Resource: "*", Effect: "deny"},
			{Action: "read", Resource: "*", Effect: "allow"},
		}, Tools: []string{"review_post"}},
	{Name: "pr-review-interactive", Version: 1, Mode: "persistent", RoleKind: "interactive", OwnerKind: "workspace", ExternalKeyFmt: "pr-review:<owner>/<repo>#<n>",
		Persona: "You review a pull request and discuss it with the user. You never post.",
		Rules: []loomharness.PermissionRule{
			{Action: "*", Resource: "*", Effect: "deny"},
			{Action: "read", Resource: "*", Effect: "allow"},
			{Action: "bash", Resource: "*", Effect: "ask"},
		}, Subagents: true},
	{Name: "daemon-worker", Version: 1, Mode: "single_task", RoleKind: "worker", OwnerKind: "workspace", ExternalKeyFmt: "issue:<id>",
		Persona: "You are a worker. Resolve the issue in your worktree.",
		Rules:   allowAll, Subagents: true, Overridable: []string{"persona", "max_budget_usd", "max_run_duration", "tools"}},
}

func init() {
	for i := range presets {
		presets[i].Harnesses = Harnesses
	}
}

// BuiltinPresets serves the five compiled-in presets.
type BuiltinPresets struct{}

// Presets reads presets (design v2 §4.1).
type Presets interface {
	Get(ctx context.Context, name string) (Preset, error)
	List(ctx context.Context) ([]Preset, error)
}

var _ Presets = BuiltinPresets{}

// Get returns a preset by name or name@version.
func (BuiltinPresets) Get(_ context.Context, ref string) (Preset, error) {
	name, ver, hasVer := strings.Cut(ref, "@")
	for _, p := range presets {
		if p.Name == name && (!hasVer || ver == strconv.Itoa(p.Version)) {
			return p, nil
		}
	}
	return Preset{}, &Error{Code: CodePresetNotFound, Message: fmt.Sprintf("no preset %q", ref)}
}

// List returns all five presets.
func (BuiltinPresets) List(context.Context) ([]Preset, error) { return slices.Clone(presets), nil }

// Enforces says which restrictions a harness adapter can enforce. The one
// validator below decides with it; adapters never interpret policy.
type Enforces struct{ Rules bool }

// Enforcement per harness. A harness missing here enforces nothing, so any
// restriction on it fails closed. codex and Claude join with their adapters.
var Enforcement = map[string]Enforces{"opencode": {Rules: true}}

// Config is a preset resolved for one Create, ready for the harness adapter.
type Config struct {
	Preset  Preset
	Harness string
	Model   string
	Effort  string
	Options []loomharness.Option `json:",omitempty"` // the model options Update set (UI1)
	// ModelUnverified: the harness's catalog did not list Model when it was
	// chosen (MCS1); it was passed through and the harness decides.
	ModelUnverified bool `json:",omitempty"`
	MaxBudgetUSD    *float64
	MaxRunDuration  *int
	Open            loomharness.PresetConfig
	Rules           []loomharness.PermissionRule
	Bridge          BridgeCaps `json:"-"` // host registration only; never stored in spec_json
}

// Resolve validates req against p and renders the harness config.
// defaultHarness is the workspace default; models is the harness catalog
// (nil skips the model check). A model it does not list is accepted as
// ModelUnverified (MCS1); only a malformed id is refused.
func Resolve(p Preset, req CreateRequest, defaultHarness string, models []string) (Config, error) {
	o := req.Overrides
	c := Config{Preset: p, Harness: o.Harness, Model: o.Model, Effort: o.Effort,
		MaxBudgetUSD: o.MaxBudgetUSD, MaxRunDuration: o.MaxRunDuration, Bridge: req.Bridge}
	if c.Harness == "" {
		c.Harness = defaultHarness
	}
	if !slices.Contains(p.Harnesses, c.Harness) {
		return Config{}, invalid(fmt.Sprintf("harness %q not allowed for %s", c.Harness, p.Name), p.Harnesses...)
	}
	if err := checkModelID(c.Model); err != nil {
		return Config{}, err
	}
	c.ModelUnverified = models != nil && c.Model != "" && !slices.Contains(models, c.Model)
	if err := checkOverrides(p, req); err != nil {
		return Config{}, err
	}
	persona := p.Persona
	if req.Persona != nil {
		text, err := req.Persona.load()
		if err != nil {
			return Config{}, err
		}
		persona = text
	}
	rules := policyRules(p, o)
	if restricts(rules) && !Enforcement[c.Harness].Rules {
		return Config{}, invalid(fmt.Sprintf("%s cannot enforce the permission rules of %s", c.Harness, p.Name), enforcing()...)
	}
	if req.Bridge.HasGitHubRead && req.Bridge.HasPublish {
		rules = slices.Concat(rules, publishDenies)
	}
	c.Rules = rules
	c.Open = loomharness.PresetConfig{Name: p.Name, Persona: persona, Tools: slices.Clone(p.Tools)}
	return c, nil
}

// checkModelID refuses a malformed model id, the cheap check left when an
// unlisted model passes through (MCS1): blank or with white space or control
// characters, or a provider/model with an empty part. "" is the default.
func checkModelID(model string) error {
	if model == "" {
		return nil
	}
	bad := strings.IndexFunc(model, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0
	for _, part := range strings.Split(model, "/") {
		bad = bad || part == ""
	}
	if bad {
		return invalid(fmt.Sprintf("malformed model id %q; want model or provider/model", model))
	}
	return nil
}

// checkOverrides refuses overrides the preset does not permit.
func checkOverrides(p Preset, req CreateRequest) error {
	o := req.Overrides
	set := map[string]bool{
		"max_budget_usd":   o.MaxBudgetUSD != nil,
		"max_run_duration": o.MaxRunDuration != nil,
		"persona":          req.Persona != nil,
		"tools":            o.ReadOnly || len(o.AllowedTools) > 0 || len(o.DeniedTools) > 0,
	}
	for _, what := range []string{"max_budget_usd", "max_run_duration", "persona", "tools"} {
		if set[what] && !slices.Contains(p.Overridable, what) {
			return invalid(fmt.Sprintf("%s cannot be set on %s", what, p.Name), p.Overridable...)
		}
	}
	if o.MaxBudgetUSD != nil && *o.MaxBudgetUSD <= 0 {
		return invalid("max_budget_usd must be positive")
	}
	return nil
}

// policyRules appends the role restrictions to the preset rules. Rules are
// last match wins, so read-only and denied tools override the allow list.
func policyRules(p Preset, o Overrides) []loomharness.PermissionRule {
	rules := slices.Clone(p.Rules)
	rule := func(action, effect string) {
		rules = append(rules, loomharness.PermissionRule{Action: action, Resource: "*", Effect: effect})
	}
	if len(o.AllowedTools) > 0 {
		rule("*", "deny")
		for _, t := range o.AllowedTools {
			rule(t, "allow")
		}
	}
	if o.ReadOnly {
		rule("edit", "deny")
		rule("bash", "deny")
	}
	for _, t := range o.DeniedTools {
		rule(t, "deny")
	}
	return rules
}

func (pp Persona) load() (string, error) {
	if (pp.File == "") == (pp.Text == "") {
		return "", invalid("persona needs exactly one of file or text")
	}
	if pp.Text != "" {
		return pp.Text, nil
	}
	b, err := os.ReadFile(pp.File)
	if err != nil {
		return "", invalid(fmt.Sprintf("persona file: %v", err))
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", invalid("persona file is empty")
	}
	return string(b), nil
}

func restricts(rules []loomharness.PermissionRule) bool {
	return slices.ContainsFunc(rules, func(r loomharness.PermissionRule) bool { return r.Effect != "allow" })
}

func enforcing() []string {
	var hs []string
	for _, h := range Harnesses {
		if Enforcement[h].Rules {
			hs = append(hs, h)
		}
	}
	return hs
}
