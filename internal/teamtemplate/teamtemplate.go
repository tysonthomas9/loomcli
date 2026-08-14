// Package teamtemplate ships the built-in Team Template bundles and the single
// apply seam shared by the CLI and the web UI.
//
// "Team Template" is the pinned glossary term; the package name avoids bare
// "template" collisions (Go prompt templates, E2B sandbox templates, issue
// IsTemplate).
//
// The package depends on internal/domain and internal/store only. Both the CLI
// and the webui import it, so an edge into either surface would be a cycle
// waiting to happen; the local worktree materializer is injected as a plain
// function value rather than imported for the same reason.
package teamtemplate

import (
	"bytes"
	"embed"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// SchemaVersion is the bundle FORMAT version. Bump on breaking changes to the
// TeamTemplate/TemplateRole/TemplateAgent shapes. Recorded in every ApplyReport,
// never persisted.
const SchemaVersion = 1

// TeamTemplate is one bundle: an ordered set of agent roles and the agents that
// take them.
type TeamTemplate struct {
	SchemaVersion int             `yaml:"schema_version"`
	ID            string          `yaml:"id"`
	Label         string          `yaml:"label"`
	Description   string          `yaml:"description"`
	Revision      int             `yaml:"revision"` // CONTENT version, bumped on roster edits
	Roles         []TemplateRole  `yaml:"roles"`
	Agents        []TemplateAgent `yaml:"agents"`
}

// TemplateRole maps 1:1 onto store.RoleCreate. The agent-role entity survives
// the worker-profile migration untouched, so this half of the schema is
// migration-safe by construction.
type TemplateRole struct {
	Name        string `yaml:"name"`
	Kind        string `yaml:"kind"` // ALWAYS explicit: "worker" | "interactive"
	Description string `yaml:"description"`
	PromptFile  string `yaml:"prompt_file"` // "builtin:<id>" only
	Model       string `yaml:"model"`
	TaskFilter  string `yaml:"task_filter"` // "has_design" | "any" | "" — never needs_plan/needs_design
	Effort      string `yaml:"effort"`

	// Skills score a candidate agent role, they never gate it: with skills set
	// and no overlap the issue still scores as a fallback match.
	Skills []string `yaml:"skills"`

	// Labels / ExcludeLabels are the HARD routing gate. Labels is AND-required;
	// ExcludeLabels is OR-rejecting and evaluated first. These are NOT the
	// presentation DisplayLabel below and NOT Skills: they are issue-label
	// semantics, which fleet-db's role model already defines.
	Labels        []string `yaml:"labels"`
	ExcludeLabels []string `yaml:"exclude_labels"`

	ReadOnly       bool     `yaml:"read_only"`     // false in every v1 bundle
	AllowedTools   []string `yaml:"allowed_tools"` // empty in every v1 bundle
	DeniedTools    []string `yaml:"denied_tools"`  // empty in every v1 bundle
	MaxConcurrency *int     `yaml:"max_concurrency"`
	MaxBudgetUSD   *float64 `yaml:"max_budget_usd"`
	MaxRunDuration *int     `yaml:"max_run_duration"`

	// DisplayLabel is presentation-only and NOT persisted: domain.Role has no
	// display-label field, and Role.Labels (above) means issue-filter labels — a
	// genuinely different thing. Consumed by the picker cards and by
	// `loom template show`.
	DisplayLabel string `yaml:"display_label"` // "Developer" | "QA" | "Architecture"
}

// TemplateAgent stays inside the store.AgentCreate ∩ worker-profile
// intersection: Name→Name, RoleName→the agent role it takes. The lifecycle half
// is not a clean intersection — a worker profile has a single Enabled bool where
// AgentCreate stores Auto and a four-state DesiredState independently — so the
// migration mapping is lossy by construction. v1 is unaffected: every bundle
// agent is auto + desired_state running.
type TemplateAgent struct {
	Name         string `yaml:"name"`
	RoleName     string `yaml:"role_name"`
	Auto         bool   `yaml:"auto"`
	DesiredState string `yaml:"desired_state"` // INTENT, not observed state

	// CrossRepo is true in every v1 bundle. Without it, empty repo affinity
	// materializes only the first repo while routing reads empty affinity as
	// "all repos" — one checkout, issues from everywhere.
	CrossRepo bool `yaml:"cross_repo"`
}

//go:embed bundles/*.yaml
var bundleFS embed.FS

// bundleFiles pins the registry order. embed.FS iterates lexically, which would
// put ai-agent first; the picker order is deliberate and belongs in code.
var bundleFiles = []string{
	"fullstack-app.yaml",
	"website.yaml",
	"ai-agent.yaml",
	"backend.yaml",
}

var registry []TeamTemplate

// init parses and validates every embedded bundle. The bundles are code-shipped,
// so a violation is a build-time defect and must not wait until a user's first
// command; the package tests walk the same path so it fails in the gate.
func init() {
	loaded, err := loadBundles()
	if err != nil {
		panic("teamtemplate: " + err.Error())
	}
	registry = loaded
}

// All returns the built-in Team Templates in picker order. Pure: no store, no
// context, so the catalog works before any workspace exists.
func All() []TeamTemplate {
	out := make([]TeamTemplate, 0, len(registry))
	for _, tpl := range registry {
		out = append(out, tpl.clone())
	}
	return out
}

// ByID returns the built-in Team Template with the given id.
func ByID(id string) (TeamTemplate, bool) {
	for _, tpl := range registry {
		if tpl.ID == id {
			return tpl.clone(), true
		}
	}
	return TeamTemplate{}, false
}

// loadBundles decodes every embedded bundle in registry order and validates it.
func loadBundles() ([]TeamTemplate, error) {
	entries, err := bundleFS.ReadDir("bundles")
	if err != nil {
		return nil, fmt.Errorf("read embedded bundles: %w", err)
	}
	embedded := make(map[string]bool, len(entries))
	for _, entry := range entries {
		embedded[entry.Name()] = true
	}
	if len(embedded) != len(bundleFiles) {
		return nil, fmt.Errorf("embedded bundles (%d) do not match the registry order list (%d)", len(embedded), len(bundleFiles))
	}
	out := make([]TeamTemplate, 0, len(bundleFiles))
	seen := make(map[string]bool, len(bundleFiles))
	for _, name := range bundleFiles {
		if !embedded[name] {
			return nil, fmt.Errorf("bundle %q is listed in registry order but not embedded", name)
		}
		tpl, err := readBundle(name)
		if err != nil {
			return nil, err
		}
		if seen[tpl.ID] {
			return nil, fmt.Errorf("bundle %q: duplicate template id %q", name, tpl.ID)
		}
		seen[tpl.ID] = true
		out = append(out, tpl)
	}
	return out, nil
}

func readBundle(name string) (TeamTemplate, error) {
	raw, err := bundleFS.ReadFile("bundles/" + name)
	if err != nil {
		return TeamTemplate{}, fmt.Errorf("read bundle %q: %w", name, err)
	}
	tpl, err := parseBundle(raw)
	if err != nil {
		return TeamTemplate{}, fmt.Errorf("bundle %q: %w", name, err)
	}
	if want := tpl.ID + ".yaml"; want != name {
		return TeamTemplate{}, fmt.Errorf("bundle %q: template id %q wants file %q", name, tpl.ID, want)
	}
	return tpl, nil
}

// parseBundle decodes one bundle and applies every validation rule. Unknown
// YAML fields are rejected outright: a typo in a bundle must not silently drop
// a routing gate.
func parseBundle(raw []byte) (TeamTemplate, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var tpl TeamTemplate
	if err := dec.Decode(&tpl); err != nil {
		return TeamTemplate{}, fmt.Errorf("decode: %w", err)
	}
	if err := validate(tpl); err != nil {
		return TeamTemplate{}, err
	}
	return tpl, nil
}

// roleCreate maps a bundle agent role onto the store's create payload. Prompt,
// Executor, Backend, PathPatterns, InputPolicy and MaxPriority are left zero:
// v1 bundles inherit them from the workspace or do not use them.
func (r TemplateRole) roleCreate(workspaceKey string) store.RoleCreate {
	return store.RoleCreate{
		WorkspaceKey:   workspaceKey,
		Name:           r.Name,
		Kind:           r.Kind,
		Description:    r.Description,
		PromptFile:     r.PromptFile,
		Model:          r.Model,
		TaskFilter:     r.TaskFilter,
		Effort:         r.Effort,
		Skills:         cloneSlice(r.Skills),
		Labels:         cloneSlice(r.Labels),
		ExcludeLabels:  cloneSlice(r.ExcludeLabels),
		ReadOnly:       r.ReadOnly,
		AllowedTools:   cloneSlice(r.AllowedTools),
		DeniedTools:    cloneSlice(r.DeniedTools),
		MaxConcurrency: clonePtr(r.MaxConcurrency),
		MaxBudgetUSD:   clonePtr(r.MaxBudgetUSD),
		MaxRunDuration: clonePtr(r.MaxRunDuration),
	}
}

// agentCreate maps a bundle agent onto the store's create payload. Backend,
// Repos and RepoGroups stay empty — a template never names, adds or picks a
// repo — and CrossRepo is what makes the worktrees match the routing scope.
func (a TemplateAgent) agentCreate(workspaceKey string) store.AgentCreate {
	return store.AgentCreate{
		WorkspaceKey: workspaceKey,
		Name:         a.Name,
		RoleName:     a.RoleName,
		Auto:         a.Auto,
		CrossRepo:    a.CrossRepo,
		DesiredState: domain.AgentDesiredState(a.DesiredState),
	}
}

// clone deep-copies a template so a caller cannot reach into the registry
// through a shared slice or pointer.
func (t TeamTemplate) clone() TeamTemplate {
	out := t
	out.Roles = make([]TemplateRole, 0, len(t.Roles))
	for _, role := range t.Roles {
		role.Skills = cloneSlice(role.Skills)
		role.Labels = cloneSlice(role.Labels)
		role.ExcludeLabels = cloneSlice(role.ExcludeLabels)
		role.AllowedTools = cloneSlice(role.AllowedTools)
		role.DeniedTools = cloneSlice(role.DeniedTools)
		role.MaxConcurrency = clonePtr(role.MaxConcurrency)
		role.MaxBudgetUSD = clonePtr(role.MaxBudgetUSD)
		role.MaxRunDuration = clonePtr(role.MaxRunDuration)
		out.Roles = append(out.Roles, role)
	}
	out.Agents = append([]TemplateAgent(nil), t.Agents...)
	return out
}

func cloneSlice(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

func clonePtr[T any](in *T) *T {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

// promptID returns the built-in prompt id an agent role names, if any.
func (r TemplateRole) promptID() (string, bool) {
	return domain.ParseBuiltinPromptRef(strings.TrimSpace(r.PromptFile))
}
