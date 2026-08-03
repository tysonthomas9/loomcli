package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type RoleKind string

const (
	RoleKindInteractive RoleKind = "interactive"
	RoleKindWorker      RoleKind = "worker"
)

// Role input-policy dispositions. The empty string is "unset", which resolves
// to deny — see RoleInputPolicy for why the unset case must not be permissive.
const (
	// RoleInputDeny answers the prompt with its negative option and lets the
	// turn continue (or fail) without a human.
	RoleInputDeny = "deny"
	// RoleInputAllow answers the prompt with its affirmative option.
	RoleInputAllow = "allow"
	// RoleInputAsk surfaces the prompt and blocks until a human answers it.
	RoleInputAsk = "ask"
)

var validRoleInputDispositions = map[string]bool{
	"":             true,
	RoleInputDeny:  true,
	RoleInputAllow: true,
	RoleInputAsk:   true,
}

const (
	// MaxRoleInputPolicyKinds bounds the per-kind disposition map, mirroring
	// fleet-db's models.MaxRoleInputPolicyKinds. No harness is anywhere near 64
	// distinct prompt kinds, so the bound only ever fires on a role definition
	// someone is trying to make large.
	MaxRoleInputPolicyKinds = 64
	// MaxRoleInputPolicyKindLength bounds a single prompt-kind key, mirroring
	// fleet-db's models.MaxRoleInputPolicyKindLength.
	MaxRoleInputPolicyKindLength = 128
)

// RoleInputPolicy declares what an agent in this role may auto-answer when the
// harness raises an interactive prompt mid-turn: a folder-trust dialog, a
// permission-acceptance screen, a confirm.
//
// It is deliberately per-role and per-kind rather than one "auto-answer
// prompts" switch, because the obvious global switch is unsafe in a way that
// only shows up once you look at what the harness actually emits.
// harness-wrapper's own unattended default (pkg/oneshot.AutoAcceptAnswer)
// answers every prompt with its affirmative option, falling back to the first
// option — and claude-code renders BOTH the harmless folder-trust dialog AND
// the `--dangerously-skip-permissions` acceptance screen under the same prompt
// kind. A blanket yes therefore accepts a skip-all-permissions launch with
// nobody having decided to, which would quietly undo the role safety knobs
// (allowed_tools / denied_tools / read_only) that were just made real. Making
// the policy per-role means a role names the kinds it is willing to
// auto-accept and everything it did not name is denied.
//
// The zero value denies everything: a nil policy, an empty Default and an
// absent Kinds entry all resolve to deny. A role that says nothing must never
// end up more permissive than a role that says something, so every path that
// cannot find a disposition falls back to deny and never to allow.
//
// The yaml tags are load-bearing: this type is embedded verbatim in
// config.RoleConfig so the on-disk role definition, the fleet-db wire shape and
// the daemon's env hop are all the same struct rather than three copies that
// can drift into disagreeing about what "unset" means.
type RoleInputPolicy struct {
	// Default is the disposition for any prompt kind Kinds does not name.
	// One of "", "deny", "allow", "ask"; empty means deny.
	Default string `json:"default,omitempty" yaml:"default,omitempty"`

	// Kinds maps a harness prompt-kind string to the disposition for that
	// kind, overriding Default. Values are the same closed vocabulary as
	// Default, and an empty value is deny so a half-written policy fails
	// closed.
	//
	// The key is opaque here and is validated for shape only. Prompt kinds
	// belong to the harness's vocabulary, and a new one appears whenever a
	// harness adds a dialog; enumerating them would mean shipping a loom
	// release before a role could express a policy for a prompt that already
	// exists in the field.
	Kinds map[string]string `json:"kinds,omitempty" yaml:"kinds,omitempty"`
}

// DispositionFor resolves the effective disposition for a harness prompt kind.
// This is the single place the deny-by-default rule lives: a nil policy, an
// explicit entry with an empty disposition, and an unset Default all resolve
// to deny, so no caller can read "unset" as "allow".
func (p *RoleInputPolicy) DispositionFor(kind string) string {
	if p == nil {
		return RoleInputDeny
	}
	if disposition, ok := p.Kinds[kind]; ok {
		if disposition == "" {
			return RoleInputDeny
		}
		return disposition
	}
	if p.Default == "" {
		return RoleInputDeny
	}
	return p.Default
}

// Clone returns a deep copy so a stored policy can be handed out without the
// caller being able to mutate the Kinds map behind the store's back.
func (p *RoleInputPolicy) Clone() *RoleInputPolicy {
	if p == nil {
		return nil
	}
	out := &RoleInputPolicy{Default: p.Default}
	if p.Kinds != nil {
		out.Kinds = make(map[string]string, len(p.Kinds))
		for k, v := range p.Kinds {
			out.Kinds[k] = v
		}
	}
	return out
}

// ValidateRoleInputDisposition returns true if d is empty or a supported
// disposition.
func ValidateRoleInputDisposition(d string) bool {
	return validRoleInputDispositions[d]
}

// ValidateRoleInputPolicy checks the shape and the disposition vocabulary of an
// input policy. A nil policy is valid and means deny everything.
//
// The messages are worded to match fleet-db's ValidateRoleInputPolicy so a bad
// value fails locally with the same text the server would have returned, rather
// than round-tripping to find out. Prompt-kind keys are intentionally not
// checked against a list of known kinds; they come from the harness and change
// without a loom or fleet-db release.
func ValidateRoleInputPolicy(p *RoleInputPolicy) error {
	if p == nil {
		return nil
	}
	if !ValidateRoleInputDisposition(p.Default) {
		return fmt.Errorf("role input_policy default %q must be one of deny, allow, ask", p.Default)
	}
	if len(p.Kinds) > MaxRoleInputPolicyKinds {
		return fmt.Errorf("role input_policy exceeds maximum of %d kinds", MaxRoleInputPolicyKinds)
	}
	for kind, disposition := range p.Kinds {
		if kind == "" {
			return fmt.Errorf("role input_policy kind is required")
		}
		if len(kind) > MaxRoleInputPolicyKindLength {
			return fmt.Errorf("role input_policy kind %q exceeds maximum length of %d characters", kind, MaxRoleInputPolicyKindLength)
		}
		if !ValidateRoleInputDisposition(disposition) {
			return fmt.Errorf("role input_policy kind %q disposition %q must be one of deny, allow, ask", kind, disposition)
		}
	}
	return nil
}

// Role is the configuration shared by all Agents that take this role —
// prompt template, AI backend, tool allowlist, concurrency cap, etc.
// Workspace-scoped: every Workspace gets its own Role definitions
// (built-in "plan" and "task" are auto-seeded on workspace creation).
type Role struct {
	WorkspaceKey string   `json:"workspace_key"`
	Name         string   `json:"name"`
	Kind         RoleKind `json:"kind,omitempty"`
	Description  string   `json:"description,omitempty"`
	Prompt       string   `json:"prompt,omitempty"`
	PromptFile   string   `json:"prompt_file,omitempty"`
	Model        string   `json:"model,omitempty"`
	TaskFilter   string   `json:"task_filter,omitempty"`
	// Executor selects how the daemon runs an agent in this role: "turn"
	// (default, one-shot harness turn per run) or "conversation" (a held
	// chat conversation: surfaced input requests, bounded follow-up turns,
	// session resume). Mirrors the server's closed vocabulary.
	Executor      string   `json:"executor,omitempty"`
	Backend       string   `json:"backend,omitempty"`
	Effort        string   `json:"effort,omitempty"`
	PathPatterns  []string `json:"path_patterns,omitempty"`
	Skills        []string `json:"skills,omitempty"`
	Labels        []string `json:"labels,omitempty"`
	ExcludeLabels []string `json:"exclude_labels,omitempty"`

	// InputPolicy declares what an agent in this role may auto-answer when the
	// harness raises an interactive prompt mid-turn. Nil — the zero value —
	// denies every prompt: a role that declares no policy auto-answers
	// nothing. See RoleInputPolicy.
	InputPolicy *RoleInputPolicy `json:"input_policy,omitempty"`

	MaxPriority    *int     `json:"max_priority,omitempty"`
	MaxConcurrency *int     `json:"max_concurrency,omitempty"`
	ReadOnly       bool     `json:"read_only,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	DeniedTools    []string `json:"denied_tools,omitempty"`
	MaxBudgetUSD   *float64 `json:"max_budget_usd,omitempty"`

	// MaxRunDuration caps a single supervised run's wall-clock age, in
	// seconds. The time-domain sibling of MaxBudgetUSD: one bounds what a run
	// may spend, this bounds how long it may take. Nil inherits the daemon-wide
	// default; <= 0 disables the cap for this role. Enforced by the supervisor's
	// health checker, not by the agent — see supervisor/run_duration.go.
	MaxRunDuration *int `json:"max_run_duration,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EncodeRoleInputPolicy serializes a policy for transport as a single
// environment variable value. A nil policy encodes to "" so the supervisor can
// simply omit the variable: absent and deny-everything are the same state, and
// keeping them the same state is what stops an agent spawned by an older
// daemon from being treated as unrestricted.
func EncodeRoleInputPolicy(p *RoleInputPolicy) (string, error) {
	if p == nil {
		return "", nil
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("encode role input_policy: %w", err)
	}
	return string(raw), nil
}

// DecodeRoleInputPolicy parses a policy off the wire (an env var, today).
//
// Every failure mode returns a nil policy — the deny-everything zero value —
// alongside the error, so a caller that ignores the error still fails closed
// and a caller that logs it can say why. That ordering matters: an absent
// variable, a truncated value, a value some other tool overwrote and a value
// carrying a disposition outside the vocabulary must all end up denying, never
// allowing. The vocabulary is re-validated here rather than trusted from the
// producer because the env is a boundary loom does not solely control.
func DecodeRoleInputPolicy(raw string) (*RoleInputPolicy, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var p RoleInputPolicy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, fmt.Errorf("decode role input_policy: %w", err)
	}
	if err := ValidateRoleInputPolicy(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ResolveRoleKind returns the effective kind for a role. Explicit Kind wins;
// roles with no kind fall back to the legacy name convention where
// "lead"/"orchestrator" are interactive and everything else is a worker.
func ResolveRoleKind(role *Role, roleName string) RoleKind {
	if role != nil {
		if kind := RoleKind(strings.ToLower(strings.TrimSpace(string(role.Kind)))); kind != "" {
			return kind
		}
	}
	if IsInteractiveRoleName(roleName) {
		return RoleKindInteractive
	}
	return RoleKindWorker
}

// IsInteractiveRoleName reports whether a role name uses the legacy
// interactive-agent naming convention.
func IsInteractiveRoleName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "lead", "orchestrator":
		return true
	default:
		return false
	}
}
