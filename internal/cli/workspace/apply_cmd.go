// Declarative workspace provisioning: `loom workspace apply -f spec.yaml`.
//
// Roles, agents and daemon settings are configuration, but the only way to
// create them was a sequence of `role add` / `role set` / `agentdef add` /
// `agentdef update` / `daemon profile set` calls whose ORDER and completeness
// nobody checks. A pipeline half-applied that way still starts: agents spawn,
// tasks move, the stack reports healthy — and the routing simply is not what
// the operator described. Every trap in validateSpec below was found that way,
// each costing a debugging session against a live daemon.
//
// The spec type is config.DaemonConfig, unchanged — the same shape the daemon
// already reads back out of fleet-db, so this introduces no second vocabulary
// and `export` can round-trip it.
package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	applySpecFile string
	applyDryRun   bool
)

var workspaceApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply a declarative workspace spec (roles, agents, daemon settings)",
	Long: `Provision a workspace from a YAML spec, idempotently.

The spec is the same shape the daemon reads: roles, agents and daemon settings.
Applying twice is a no-op; applying a changed spec updates in place.

The whole spec is VALIDATED BEFORE ANYTHING IS WRITTEN, because a half-applied
pipeline still starts and looks healthy while routing differently than you
described. Run with --dry-run to validate and print the plan without writing.

The daemon resolves role config once, at creation. Apply with the supervisor
STOPPED, then start it — a running daemon will not pick these changes up.

Example:

  loom workspace apply -f pipeline.yaml --dry-run
  loom workspace apply -f pipeline.yaml`,
	RunE: runWorkspaceApply,
}

func init() {
	workspaceApplyCmd.Flags().StringVarP(&applySpecFile, "file", "f", "", "Path to the workspace spec (YAML)")
	workspaceApplyCmd.Flags().BoolVar(&applyDryRun, "dry-run", false, "Validate and print the plan; write nothing")
	_ = workspaceApplyCmd.MarkFlagRequired("file")
	workspaceCmd.AddCommand(workspaceApplyCmd)
}

// tuiHarnessBackends are backends driven through their interactive TUI. They
// raise a folder-trust prompt on first run in a worktree, and a role with no
// input policy denies it — the harness then waits for a screen that never
// becomes ready and the run hangs until the watchdog kills it. Field-diagnosed
// 2026-08-11: six consecutive agent runs, no output, no error.
var tuiHarnessBackends = map[string]bool{"claude": true, "codex": true, "gemini": true, "cursor": true}

// trustPromptKind is the harness prompt kind that must be answerable for a TUI
// backend to reach its composer at all.
const trustPromptKind = "trust_prompt"

// builtInRoleNames mirrors supervisor.BuiltInRoles. It is duplicated rather
// than imported because internal/cli/daemon/supervisor reaches this package
// through internal/cli/agent, so importing it here is an import cycle.
var builtInRoleNames = map[string]bool{"plan": true, "task": true}

func runWorkspaceApply(_ *cobra.Command, _ []string) error {
	raw, err := os.ReadFile(applySpecFile) //nolint:gosec // operator-supplied spec path
	if err != nil {
		return fmt.Errorf("read spec: %w", err)
	}
	var spec cfgpkg.DaemonConfig
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true) // a typo'd key must fail, not silently configure nothing
	if err := dec.Decode(&spec); err != nil {
		return fmt.Errorf("parse spec %s: %w", applySpecFile, err)
	}

	specDir, err := filepath.Abs(filepath.Dir(applySpecFile))
	if err != nil {
		return fmt.Errorf("resolve spec dir: %w", err)
	}
	problems := validateSpec(&spec, specDir)
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "spec is not applicable (%d problem(s)):\n", len(problems))
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "  - %s\n", p)
		}
		return fmt.Errorf("refusing to apply: a partially applied pipeline starts and routes wrongly")
	}

	if applyDryRun {
		printPlan(&spec)
		return nil
	}
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		return applySpec(ctx, h, ws, &spec)
	})
}

// validateSpec returns every problem it can find, rather than the first: an
// operator fixing a pipeline wants the whole list, not one round-trip per typo.
func validateSpec(spec *cfgpkg.DaemonConfig, specDir string) []string {
	var problems []string
	problems = append(problems, validateRoles(spec, specDir)...)
	problems = append(problems, validateAgents(spec)...)
	problems = append(problems, validateSelfExclusion(spec)...)
	problems = append(problems, validateCapacity(spec)...)
	sort.Strings(problems)
	return problems
}

func validateRoles(spec *cfgpkg.DaemonConfig, specDir string) []string {
	var problems []string
	for name, rc := range spec.Roles {
		builtin := builtInRoleNames[name]
		switch {
		case builtin && rc.PromptFile != "":
			problems = append(problems, fmt.Sprintf("role %q: built-in roles cannot set prompt_file; use a custom role name", name))
		case !builtin && rc.PromptFile == "" && rc.Prompt == "":
			problems = append(problems, fmt.Sprintf("role %q: custom roles need prompt or prompt_file (an unresolvable prompt fails daemon creation entirely, not just this agent)", name))
		}
		if rc.PromptFile != "" && !filepath.IsAbs(rc.PromptFile) {
			if _, err := os.Stat(filepath.Join(specDir, rc.PromptFile)); err != nil {
				problems = append(problems, fmt.Sprintf("role %q: prompt_file %q not found relative to the spec", name, rc.PromptFile))
			}
		}
		// A label-routed role that keeps a content-based task_filter is filtered
		// on TWO axes at once. The built-in defaults bite hardest: `plan` is
		// needs_plan, so a planner REVISION pass (the task now has a design) is
		// silently excluded and the loop stalls with the task sitting ready.
		if len(rc.Labels) > 0 || len(rc.ExcludeLabels) > 0 {
			if rc.TaskFilter == "" {
				problems = append(problems, fmt.Sprintf("role %q: label routing needs an explicit task_filter (use \"any\"); the built-in default filters on design state and will drop stages of your pipeline", name))
			}
		}
		problems = append(problems, validateRoleInputPolicy(spec, name, rc)...)
	}
	return problems
}

// validateRoleInputPolicy enforces the rule that cost the most to learn: a TUI
// backend with no answer for the folder-trust prompt hangs forever instead of
// failing.
func validateRoleInputPolicy(spec *cfgpkg.DaemonConfig, name string, rc cfgpkg.RoleConfig) []string {
	backend := rc.Backend
	if backend == "" {
		backend = spec.Backend
	}
	if backend == "" {
		backend = backendOfAgentsUsing(spec, name)
	}
	if !tuiHarnessBackends[backend] {
		return nil
	}
	if rc.InputPolicy == nil || rc.InputPolicy.DispositionFor(trustPromptKind) != domain.RoleInputAllow {
		return []string{fmt.Sprintf(
			"role %q runs on %q and does not allow %s: the harness raises a folder-trust prompt, an unanswered prompt never becomes ready, and the run hangs until the watchdog kills it. Set input_policy: {default: deny, kinds: {%s: allow}}",
			name, backend, trustPromptKind, trustPromptKind)}
	}
	return nil
}

// backendOfAgentsUsing reports the backend the agents bound to this role run on,
// when they agree; roles carry no backend of their own in most specs.
func backendOfAgentsUsing(spec *cfgpkg.DaemonConfig, role string) string {
	seen := ""
	for _, a := range spec.Agents {
		if a.Role != role || a.Backend == "" {
			continue
		}
		if seen != "" && seen != a.Backend {
			return "" // ambiguous; do not guess
		}
		seen = a.Backend
	}
	return seen
}

func validateAgents(spec *cfgpkg.DaemonConfig) []string {
	var problems []string
	for _, a := range spec.Agents {
		if a.Worktree == "" {
			problems = append(problems, "agent with no worktree name")
			continue
		}
		if a.Role == "" {
			problems = append(problems, fmt.Sprintf("agent %q: role is required", a.Worktree))
			continue
		}
		if _, ok := spec.Roles[a.Role]; !ok && !builtInRoleNames[a.Role] {
			problems = append(problems, fmt.Sprintf("agent %q: role %q is neither built-in nor defined in this spec", a.Worktree, a.Role))
		}
		if a.Hooks != nil {
			if err := a.Hooks.Validate(); err != nil {
				problems = append(problems, fmt.Sprintf("agent %q: hooks: %v", a.Worktree, err))
			}
		}
	}
	return problems
}

// validateSelfExclusion is the pipeline invariant: a stage that stamps a label
// must be excluded by that same label, or it stays claim-eligible after its own
// run and re-claims the task it just handed on. Observed live as a cycle stage
// re-shipping one task 41 times in ~2 minutes, each re-claim a full agent run.
func validateSelfExclusion(spec *cfgpkg.DaemonConfig) []string {
	var problems []string
	for _, a := range spec.Agents {
		rc, ok := spec.Roles[a.Role]
		if !ok || a.Hooks == nil {
			continue
		}
		excluded := make(map[string]bool, len(rc.ExcludeLabels))
		for _, l := range rc.ExcludeLabels {
			excluded[l] = true
		}
		for _, label := range stampedLabels(a.Hooks) {
			if !excluded[label] {
				problems = append(problems, fmt.Sprintf(
					"agent %q stamps %q but role %q does not exclude it: the stage stays claim-eligible after its own run and will re-claim the task it just handed on",
					a.Worktree, label, a.Role))
			}
		}
	}
	return problems
}

// stampedLabels lists the labels a hook pipeline can leave on a task: added
// labels and a cycle's ship label (the cycle's re-arm label is REMOVED, which
// is the hand-off back, so it is deliberately not included).
func stampedLabels(h *domain.AgentHooks) []string {
	var out []string
	for _, action := range h.OnComplete {
		switch action.Type {
		case domain.AgentHookActionAddLabel:
			out = append(out, action.Value)
		case domain.AgentHookActionCycle:
			if action.Cycle != nil {
				out = append(out, action.Cycle.ShipLabel)
			}
		}
	}
	return out
}

// validateCapacity catches the ceiling that fails DAEMON CREATION rather than
// the extra agent: exceeding max_agents stops every agent, silently.
func validateCapacity(spec *cfgpkg.DaemonConfig) []string {
	auto := 0
	for _, a := range spec.Agents {
		if a.Auto {
			auto++
		}
	}
	max := 2 // the daemon's default
	if spec.Daemon.MaxAgents != nil {
		max = *spec.Daemon.MaxAgents
	}
	if auto > max {
		return []string{fmt.Sprintf("daemon.max_agents is %d but the spec declares %d auto agents: exceeding the ceiling fails daemon creation, so NO agent runs", max, auto)}
	}
	return nil
}

func printPlan(spec *cfgpkg.DaemonConfig) {
	fmt.Println("spec is applicable; plan:")
	names := make([]string, 0, len(spec.Roles))
	for n := range spec.Roles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		rc := spec.Roles[n]
		fmt.Printf("  role %-10s kind=%-6s model=%-8s filter=%-6s labels=%v exclude=%v\n",
			n, orDash(rc.Kind), orDash(rc.Model), orDash(rc.TaskFilter), rc.Labels, rc.ExcludeLabels)
	}
	for _, a := range spec.Agents {
		hooks := 0
		if a.Hooks != nil {
			hooks = len(a.Hooks.OnComplete)
		}
		fmt.Printf("  agent %-10s role=%-10s backend=%-10s auto=%-5t hooks=%d\n",
			a.Worktree, a.Role, orDash(a.Backend), a.Auto, hooks)
	}
	if spec.Daemon.MaxAgents != nil {
		fmt.Printf("  daemon max_agents=%d\n", *spec.Daemon.MaxAgents)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// applySpec writes roles before agents (an agent references its role) and the
// daemon profile last.
func applySpec(ctx context.Context, h *bootstrap.StoreHandle, ws string, spec *cfgpkg.DaemonConfig) error {
	names := make([]string, 0, len(spec.Roles))
	for n := range spec.Roles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := applyRole(ctx, h, ws, name, spec.Roles[name]); err != nil {
			return err
		}
	}
	for _, a := range spec.Agents {
		if err := applyAgent(ctx, h, ws, a); err != nil {
			return err
		}
	}
	return applyDaemonSettings(ctx, h, ws, spec)
}

func applyRole(ctx context.Context, h *bootstrap.StoreHandle, ws, name string, rc cfgpkg.RoleConfig) error {
	kind := rc.Kind
	if kind == "" {
		kind = "worker"
	}
	existing, err := h.Store.Roles().Get(ctx, ws, name)
	if err != nil || existing == nil {
		in := store.RoleCreate{
			WorkspaceKey: ws, Name: name, Kind: kind,
			Description: rc.Description, Prompt: rc.Prompt, PromptFile: rc.PromptFile,
			Model: rc.Model, Backend: rc.Backend, Effort: rc.Effort, Skills: rc.Skills,
			ReadOnly: rc.ReadOnly, InputPolicy: rc.InputPolicy,
			Labels: rc.Labels, ExcludeLabels: rc.ExcludeLabels,
		}
		if _, cerr := h.Store.Roles().Create(ctx, in); cerr != nil {
			return fmt.Errorf("create role %s: %w", name, cerr)
		}
	}
	patch := store.RoleUpdate{
		Kind: &kind, TaskFilter: &rc.TaskFilter,
		Labels: &rc.Labels, ExcludeLabels: &rc.ExcludeLabels,
	}
	if rc.Model != "" {
		patch.Model = &rc.Model
	}
	if rc.Executor != "" {
		patch.Executor = &rc.Executor
	}
	if rc.PromptFile != "" {
		patch.PromptFile = &rc.PromptFile
	}
	if rc.InputPolicy != nil {
		ip := rc.InputPolicy
		patch.InputPolicy = &ip
	}
	if rc.MaxBudgetUSD != nil {
		b := rc.MaxBudgetUSD
		patch.MaxBudgetUSD = &b
	}
	if rc.MaxRunDuration != nil {
		d := rc.MaxRunDuration
		patch.MaxRunDuration = &d
	}
	if _, err := h.Store.Roles().Update(ctx, ws, name, patch); err != nil {
		return fmt.Errorf("update role %s: %w", name, err)
	}
	fmt.Printf("  role %s applied\n", name)
	return nil
}

func applyAgent(ctx context.Context, h *bootstrap.StoreHandle, ws string, a cfgpkg.AgentEntry) error {
	existing, err := h.Store.Agents().Get(ctx, ws, a.Worktree)
	if err != nil || existing == nil {
		in := store.AgentCreate{
			WorkspaceKey: ws, Name: a.Worktree, RoleName: a.Role, Auto: a.Auto,
			Backend: a.Backend, Repos: a.Repos, RepoGroups: a.RepoGroups,
			CrossRepo: a.CrossRepo, Parent: a.Parent, Mode: a.Mode, Hooks: a.Hooks,
		}
		if _, cerr := h.Store.Agents().Create(ctx, in); cerr != nil {
			return fmt.Errorf("create agent %s: %w", a.Worktree, cerr)
		}
		fmt.Printf("  agent %s created\n", a.Worktree)
		return nil
	}
	patch := store.AgentUpdate{RoleName: &a.Role, Auto: &a.Auto, Hooks: a.Hooks}
	if a.Backend != "" {
		patch.Backend = &a.Backend
	}
	if len(a.Repos) > 0 {
		patch.Repos = &a.Repos
	}
	if _, err := h.Store.Agents().Update(ctx, ws, a.Worktree, patch); err != nil {
		return fmt.Errorf("update agent %s: %w", a.Worktree, err)
	}
	fmt.Printf("  agent %s applied\n", a.Worktree)
	return nil
}

func applyDaemonSettings(ctx context.Context, h *bootstrap.StoreHandle, ws string, spec *cfgpkg.DaemonConfig) error {
	p, err := h.Store.Daemon().Get(ctx, ws)
	if err != nil {
		return fmt.Errorf("read daemon profile: %w", err)
	}
	if spec.Daemon.MaxAgents != nil {
		p.MaxAgents = spec.Daemon.MaxAgents
	}
	if spec.Daemon.IssueBackend != "" {
		p.IssueBackend = spec.Daemon.IssueBackend
	}
	if spec.Daemon.LogDir != "" {
		p.LogDir = spec.Daemon.LogDir
	}
	if spec.Daemon.EventsDir != "" {
		p.EventsDir = spec.Daemon.EventsDir
	}
	if spec.Daemon.StartupTimeout != nil {
		p.StartupTimeout = spec.Daemon.StartupTimeout
	}
	if _, err := h.Store.Daemon().Upsert(ctx, p); err != nil {
		return fmt.Errorf("write daemon profile: %w", err)
	}
	fmt.Println("  daemon profile applied")
	fmt.Println("apply complete — start the supervisor now (role config is resolved at daemon creation)")
	return nil
}
