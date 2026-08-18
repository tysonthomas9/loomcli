package teamtemplate

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Entity values used in StepResult.Entity. "role" is the store/API vocabulary
// the user already sees; prose about it says "agent role".
const (
	entityRole  = "role"
	entityAgent = "agent"
)

// DefaultMaxAgents mirrors the daemon project config's default max_agents. A
// caller that has not read the project config can pass it so the headroom
// warning still fires.
const DefaultMaxAgents = 20

// LocalMaterializer creates the local git worktrees an agent needs on this
// machine. It is injected because materialization is surface- and
// machine-specific; nil for cloud/path-less workspaces and for pure-store
// callers.
//
// localworkspace.AgentWorktreeMaterializer.Materialize has exactly this shape.
// The type is declared here rather than imported so this package keeps its
// two-import dependency closure.
type LocalMaterializer func(ctx context.Context, agent domain.Agent) error

// ApplyDeps carries everything Apply cannot discover for itself.
type ApplyDeps struct {
	Store             store.Store       // required
	LocalMaterializer LocalMaterializer // optional; nil = store-only apply
	OnStep            func(StepResult)  // optional; called as each step resolves
	DryRun            bool              // classify everything, write nothing

	// LocalPath, RunnableAgentCount and MaxAgents feed the preflight. They are
	// supplied by the caller because only the surface knows them: the CLI reads
	// the local project config, the webui reads the workspace record. A zero
	// value means "unknown" and downgrades that one check to a no-op rather
	// than a false alarm — except LocalPath, where "" is meaningful (a
	// path-less workspace) and the caller must pass the real value.
	LocalPath          string // "" = cloud/path-less workspace
	RunnableAgentCount int    // existing runnable agents, for the max_agents check
	MaxAgents          int    // daemon max_agents (0 = unknown; DefaultMaxAgents is 20)
}

// StepAction is what apply did about one bundle entry.
type StepAction string

const (
	StepCreated         StepAction = "created"
	StepSkippedMatch    StepAction = "skipped_match"    // exists; config matches the bundle subset
	StepSkippedDiverged StepAction = "skipped_diverged" // exists; differs — Fields names what
	StepFailed          StepAction = "failed"
)

// StepResult is one bundle entry's outcome.
type StepResult struct {
	Entity string     `json:"entity"` // "role" | "agent"
	Name   string     `json:"name"`
	Action StepAction `json:"action"`
	Fields []string   `json:"fields,omitempty"` // diverged field names, store vocabulary
	Error  string     `json:"error,omitempty"`  // user-facing message (failed only)
}

// ApplyReport is the one shape both surfaces render: the CLI prints lines and
// --json, the panel draws a checklist.
type ApplyReport struct {
	TemplateID    string       `json:"template_id"`
	Revision      int          `json:"revision"`
	SchemaVersion int          `json:"schema_version"`
	WorkspaceKey  string       `json:"workspace_key"`
	DryRun        bool         `json:"dry_run"`
	Steps         []StepResult `json:"steps"`
	Created       int          `json:"created"`
	Skipped       int          `json:"skipped"` // match + diverged
	Diverged      int          `json:"diverged"`
	Failed        int          `json:"failed"`

	// Warnings carries non-fatal preflight findings: a path-less/cloud
	// workspace where only control-plane rows are provisioned, or a max_agents
	// ceiling the new agents would push the daemon past. Both surfaces MUST
	// render these — a silent warning here is exactly the "reported success,
	// nothing actually runs" failure mode.
	Warnings []string `json:"warnings,omitempty"`

	// Materialized counts agents whose local worktrees were (re-)created this
	// run, including retries over agents that were already applied.
	Materialized int `json:"materialized"`
}

// Apply provisions tpl's agent roles and then its agents into workspaceKey.
//
// Apply never modifies anything that already exists: a name collision is
// reported as skipped_match or skipped_diverged. A failed step is recorded and
// the remaining steps continue — there is no rollback, and re-running apply
// converges.
//
// The returned error is non-nil only for preflight failures: unknown workspace,
// store unreachable before any step ran, or a local workspace with no repos.
// Per-step failures live in the report; preflight warnings are non-fatal and
// are carried in ApplyReport.Warnings.
func Apply(ctx context.Context, deps ApplyDeps, workspaceKey string, tpl TeamTemplate) (ApplyReport, error) {
	run := &applyRun{
		deps:         deps,
		workspaceKey: workspaceKey,
		tpl:          tpl,
		roleKinds:    make(map[string]string, len(tpl.Roles)),
		roleErrors:   make(map[string]string, len(tpl.Roles)),
		report: ApplyReport{
			TemplateID:    tpl.ID,
			Revision:      tpl.Revision,
			SchemaVersion: tpl.SchemaVersion,
			WorkspaceKey:  workspaceKey,
			DryRun:        deps.DryRun,
		},
	}
	warnings, err := preflight(ctx, deps, workspaceKey, tpl)
	run.report.Warnings = warnings
	if err != nil {
		return run.report, err
	}
	run.applyRoles(ctx)
	run.applyAgents(ctx)
	run.tally()
	return run.report, nil
}

// preflight runs before anything is created. It exists because two workspace
// shapes otherwise produce a green report over a team that could never run: a
// local workspace with no repos (materialization fails for every agent) and a
// path-less/cloud workspace (no worktrees, no local worker).
func preflight(ctx context.Context, deps ApplyDeps, workspaceKey string, tpl TeamTemplate) ([]string, error) {
	if deps.Store == nil {
		return nil, errors.New("apply needs a store")
	}
	if _, err := deps.Store.Workspaces().Get(ctx, workspaceKey); err != nil {
		return nil, fmt.Errorf("workspace %q: %w", workspaceKey, err)
	}
	var warnings []string
	if deps.LocalPath == "" {
		warnings = append(warnings, fmt.Sprintf(
			"workspace %q has no local checkout — agent roles and agents are provisioned in the control plane only; no worktrees are created and no local worker starts on this machine",
			workspaceKey))
	} else {
		repos, err := deps.Store.Repos().List(ctx, workspaceKey)
		if err != nil {
			return warnings, fmt.Errorf("list repositories in workspace %q: %w", workspaceKey, err)
		}
		if len(repos) == 0 {
			return warnings, fmt.Errorf(
				"workspace %q has no repositories — add a repo first (loom repo add), then apply the template",
				workspaceKey)
		}
	}
	return append(warnings, maxAgentsWarning(deps, len(tpl.Agents))...), nil
}

// maxAgentsWarning warns rather than refuses: the limit is a daemon-config
// rule, not a store rule. The rows are created successfully and the daemon then
// refuses the config reload and keeps running the previous config — so the
// visible symptom is that nothing changes, silently.
func maxAgentsWarning(deps ApplyDeps, adding int) []string {
	if deps.MaxAgents <= 0 {
		return nil
	}
	total := deps.RunnableAgentCount + adding
	if total <= deps.MaxAgents {
		return nil
	}
	return []string{fmt.Sprintf(
		"applying %d agents would put this workspace at %d runnable agents, over the daemon's max_agents limit of %d — the daemon will refuse to reload its config and will keep running the old one",
		adding, total, deps.MaxAgents)}
}

// applyRun holds the per-invocation state the two phases share.
type applyRun struct {
	deps         ApplyDeps
	workspaceKey string
	tpl          TeamTemplate
	report       ApplyReport
	roleKinds    map[string]string // bundle agent role name → kind
	roleErrors   map[string]string // bundle agent role name → this run's failure
}

func (r *applyRun) record(step StepResult) {
	r.report.Steps = append(r.report.Steps, step)
	if r.deps.OnStep != nil {
		r.deps.OnStep(step)
	}
}

// applyRoles runs the agent-role phase in bundle order. Roles come first
// because the control plane validates the agent-role reference when an agent is
// created; the ordering is mandatory, not stylistic.
func (r *applyRun) applyRoles(ctx context.Context) {
	for _, role := range r.tpl.Roles {
		r.roleKinds[role.Name] = role.Kind
		step := r.applyRole(ctx, role)
		if step.Action == StepFailed {
			r.roleErrors[role.Name] = step.Error
		}
		r.record(step)
	}
}

func (r *applyRun) applyRole(ctx context.Context, role TemplateRole) StepResult {
	roles := r.deps.Store.Roles()
	existing, err := roles.Get(ctx, r.workspaceKey, role.Name)
	switch {
	case err == nil:
		return roleStep(role, existing)
	case !errors.Is(err, domain.ErrNotFound):
		return failedStep(entityRole, role.Name, err)
	}
	if r.deps.DryRun {
		return StepResult{Entity: entityRole, Name: role.Name, Action: StepCreated}
	}
	if _, err := roles.Create(ctx, role.roleCreate(r.workspaceKey)); err != nil {
		if !errors.Is(err, domain.ErrAlreadyExists) {
			return failedStep(entityRole, role.Name, err)
		}
		// Lost a race with a concurrent apply: fall back to the found branch.
		existing, getErr := roles.Get(ctx, r.workspaceKey, role.Name)
		if getErr != nil {
			return failedStep(entityRole, role.Name, getErr)
		}
		return roleStep(role, existing)
	}
	return StepResult{Entity: entityRole, Name: role.Name, Action: StepCreated}
}

func roleStep(role TemplateRole, existing *domain.Role) StepResult {
	fields := compareRole(role, existing)
	if len(fields) == 0 {
		return StepResult{Entity: entityRole, Name: role.Name, Action: StepSkippedMatch}
	}
	return StepResult{Entity: entityRole, Name: role.Name, Action: StepSkippedDiverged, Fields: fields}
}

// applyAgents runs the agent phase in bundle order.
func (r *applyRun) applyAgents(ctx context.Context) {
	for _, spec := range r.tpl.Agents {
		kind, step, ok := r.resolveAgentRole(ctx, spec)
		if !ok {
			r.record(step)
			continue
		}
		r.record(r.createTeamMember(ctx, spec, kind))
	}
}

// resolveAgentRole answers "which agent role does this agent take, and is it
// usable this run". A dependent failure — the agent role's own step failed
// moments ago — cascades here deliberately and self-heals on re-apply.
func (r *applyRun) resolveAgentRole(ctx context.Context, spec TemplateAgent) (domain.RoleKind, StepResult, bool) {
	if kind, inBundle := r.roleKinds[spec.RoleName]; inBundle {
		if msg, failed := r.roleErrors[spec.RoleName]; failed {
			step := StepResult{
				Entity: entityAgent,
				Name:   spec.Name,
				Action: StepFailed,
				Error:  fmt.Sprintf("agent role %q was not created: %s", spec.RoleName, msg),
			}
			return "", step, false
		}
		return domain.RoleKind(kind), StepResult{}, true
	}
	// Out-of-bundle agent role (a seeded built-in): look it up so the failure
	// reads as a missing agent role rather than the control plane's opaque
	// validation error.
	existing, err := r.deps.Store.Roles().Get(ctx, r.workspaceKey, spec.RoleName)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", StepResult{
				Entity: entityAgent,
				Name:   spec.Name,
				Action: StepFailed,
				Error:  fmt.Sprintf("agent role %q not found", spec.RoleName),
			}, false
		}
		return "", failedStep(entityAgent, spec.Name, err), false
	}
	return domain.ResolveRoleKind(existing, existing.Name), StepResult{}, true
}

// createTeamMember is THE agent-instantiation seam. It is the only place in the
// Team Templates feature that knows today's persisted execution-policy entity
// is domain.Agent / store.AgentCreate. When the worker-profile migration lands,
// this body switches to profile creation; the bundle schema, Apply ordering,
// report shape and both surfaces do not change.
//
// What the signature hides — each is a thing that changes at migration: the
// entity and endpoint, the field translation (Auto and DesiredState are two
// independent knobs today and one Enabled bool afterwards — lossy by
// construction, though v1 is unaffected because every bundle agent is auto +
// running), the already-exists semantics, and the local materialization policy.
//
// roleKind is a parameter rather than a lookup because Apply has already
// resolved it: materialization is skipped for interactive agent roles, and the
// injected materializer is not required to make that decision for us.
func (r *applyRun) createTeamMember(ctx context.Context, spec TemplateAgent, roleKind domain.RoleKind) StepResult {
	agents := r.deps.Store.Agents()
	existing, err := agents.Get(ctx, r.workspaceKey, spec.Name)
	switch {
	case err == nil:
		return r.existingMemberStep(ctx, spec, existing, roleKind)
	case !errors.Is(err, domain.ErrNotFound):
		return failedStep(entityAgent, spec.Name, err)
	}
	if r.deps.DryRun {
		return StepResult{Entity: entityAgent, Name: spec.Name, Action: StepCreated}
	}
	created, err := agents.Create(ctx, spec.agentCreate(r.workspaceKey))
	if err != nil {
		if !errors.Is(err, domain.ErrAlreadyExists) {
			return failedStep(entityAgent, spec.Name, err)
		}
		raced, getErr := agents.Get(ctx, r.workspaceKey, spec.Name)
		if getErr != nil {
			return failedStep(entityAgent, spec.Name, getErr)
		}
		return r.existingMemberStep(ctx, spec, raced, roleKind)
	}
	step := StepResult{Entity: entityAgent, Name: spec.Name, Action: StepCreated}
	return r.withMaterialization(ctx, step, *created, roleKind)
}

// existingMemberStep classifies an agent that is already there. A matching
// agent still gets its worktrees re-checked: skipping the store write must not
// skip the worktree, or a worktree that failed once never heals — this feature
// keeps the row on failure instead of compensating-deleting it, so re-apply is
// the only thing that can repair it.
func (r *applyRun) existingMemberStep(ctx context.Context, spec TemplateAgent, existing *domain.Agent, roleKind domain.RoleKind) StepResult {
	fields := compareAgent(spec, existing)
	if len(fields) > 0 {
		return StepResult{Entity: entityAgent, Name: spec.Name, Action: StepSkippedDiverged, Fields: fields}
	}
	step := StepResult{Entity: entityAgent, Name: spec.Name, Action: StepSkippedMatch}
	return r.withMaterialization(ctx, step, *existing, roleKind)
}

// withMaterialization runs the injected materializer and turns a failure into a
// failed step while leaving the store row in place: keep and retry, so the next
// apply tries the worktree again.
func (r *applyRun) withMaterialization(ctx context.Context, step StepResult, agent domain.Agent, roleKind domain.RoleKind) StepResult {
	switch {
	case r.deps.DryRun, r.deps.LocalMaterializer == nil:
		return step
	case roleKind == domain.RoleKindInteractive:
		// Interactive agent roles get no worktrees, exactly as they do today.
		// Apply decides this itself: the injected materializer's own skip hook
		// is optional and historically unset on the CLI side.
		return step
	}
	if err := r.deps.LocalMaterializer(ctx, agent); err != nil {
		return failedStep(entityAgent, agent.Name, err)
	}
	r.report.Materialized++
	return step
}

func (r *applyRun) tally() {
	for _, step := range r.report.Steps {
		switch step.Action {
		case StepCreated:
			r.report.Created++
		case StepSkippedMatch:
			r.report.Skipped++
		case StepSkippedDiverged:
			r.report.Skipped++
			r.report.Diverged++
		case StepFailed:
			r.report.Failed++
		}
	}
}

func failedStep(entity, name string, err error) StepResult {
	return StepResult{Entity: entity, Name: name, Action: StepFailed, Error: err.Error()}
}
