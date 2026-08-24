package teamtemplate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const testWorkspace = "MYPROJ"

// newStore returns a memstore holding a workspace with one repo — the shape a
// local apply expects.
func newStore(t *testing.T) *memstore.Store {
	t.Helper()
	st := memstore.New()
	ctx := context.Background()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: testWorkspace, Name: "myproj"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: testWorkspace, Name: "app"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	return st
}

func fullstack(t *testing.T) TeamTemplate {
	t.Helper()
	tpl, ok := ByID("fullstack-app")
	if !ok {
		t.Fatal("fullstack-app missing")
	}
	return tpl
}

// localDeps is the shape a CLI apply against a checked-out workspace uses.
func localDeps(st store.Store) ApplyDeps {
	return ApplyDeps{Store: st, LocalPath: "/workspaces/myproj"}
}

func stepFor(report ApplyReport, entity, name string) (StepResult, bool) {
	for _, step := range report.Steps {
		if step.Entity == entity && step.Name == name {
			return step, true
		}
	}
	return StepResult{}, false
}

func mustStep(t *testing.T, report ApplyReport, entity, name string) StepResult {
	t.Helper()
	step, ok := stepFor(report, entity, name)
	if !ok {
		t.Fatalf("no %s step for %q in %+v", entity, name, report.Steps)
	}
	return step
}

func TestApplyCreatesRolesThenAgents(t *testing.T) {
	st := newStore(t)
	tpl := fullstack(t)
	var observed []string
	deps := localDeps(st)
	deps.OnStep = func(step StepResult) { observed = append(observed, step.Entity+":"+step.Name) }

	report, err := Apply(context.Background(), deps, testWorkspace, tpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if report.Created != len(tpl.Roles)+len(tpl.Agents) {
		t.Fatalf("created %d, want %d: %+v", report.Created, len(tpl.Roles)+len(tpl.Agents), report.Steps)
	}
	if report.Failed != 0 || report.Skipped != 0 || report.Diverged != 0 {
		t.Fatalf("unexpected non-created outcomes: %+v", report)
	}
	if report.TemplateID != tpl.ID || report.Revision != tpl.Revision || report.SchemaVersion != SchemaVersion {
		t.Errorf("report provenance = %q/%d/%d", report.TemplateID, report.Revision, report.SchemaVersion)
	}
	if len(observed) != len(report.Steps) {
		t.Errorf("OnStep fired %d times for %d steps", len(observed), len(report.Steps))
	}
	assertRolesBeforeAgents(t, report)

	// The interactive code-reviewer agent role is provisioned; it gets no agent.
	if _, err := st.Roles().Get(context.Background(), testWorkspace, "code-reviewer"); err != nil {
		t.Errorf("code-reviewer agent role not created: %v", err)
	}
	if _, ok := stepFor(report, entityAgent, "code-reviewer-1"); ok {
		t.Error("an agent was provisioned for the interactive agent role")
	}
}

func assertRolesBeforeAgents(t *testing.T, report ApplyReport) {
	t.Helper()
	seenAgent := false
	for _, step := range report.Steps {
		if step.Entity == entityAgent {
			seenAgent = true
			continue
		}
		if seenAgent {
			t.Fatalf("agent role step %q ran after an agent step: %+v", step.Name, report.Steps)
		}
	}
}

func TestApplyPersistsTheBundleFields(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	if _, err := Apply(ctx, localDeps(st), testWorkspace, fullstack(t)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	role, err := st.Roles().Get(ctx, testWorkspace, "app-architect")
	if err != nil {
		t.Fatalf("get agent role: %v", err)
	}
	if role.Kind != domain.RoleKindWorker || role.PromptFile != "builtin:team-architect" {
		t.Errorf("agent role kind/prompt = %q/%q", role.Kind, role.PromptFile)
	}
	if len(role.Labels) != 1 || role.Labels[0] != "architect" {
		t.Errorf("labels = %v, want [architect]", role.Labels)
	}
	if role.TaskFilter != "any" || role.Effort != "high" || role.ReadOnly {
		t.Errorf("routing/policy = %q/%q/%v", role.TaskFilter, role.Effort, role.ReadOnly)
	}
	agent, err := st.Agents().Get(ctx, testWorkspace, "app-architect-1")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if !agent.CrossRepo || !agent.Auto || agent.DesiredState != domain.AgentDesiredRunning {
		t.Errorf("agent = cross_repo %v auto %v desired %q", agent.CrossRepo, agent.Auto, agent.DesiredState)
	}
	if len(agent.Repos) != 0 || len(agent.RepoGroups) != 0 || agent.Backend != "" {
		t.Errorf("a template must not pick repos or a backend: %+v", agent)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	tpl := fullstack(t)
	if _, err := Apply(ctx, localDeps(st), testWorkspace, tpl); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	report, err := Apply(ctx, localDeps(st), testWorkspace, tpl)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if report.Created != 0 || report.Diverged != 0 || report.Failed != 0 {
		t.Fatalf("re-apply was not a clean skip: %+v", report)
	}
	if report.Skipped != len(tpl.Roles)+len(tpl.Agents) {
		t.Fatalf("skipped %d, want %d", report.Skipped, len(tpl.Roles)+len(tpl.Agents))
	}
	for _, step := range report.Steps {
		if step.Action != StepSkippedMatch {
			t.Errorf("%s %s: action %q, want skipped_match (fields %v)", step.Entity, step.Name, step.Action, step.Fields)
		}
	}
}

func TestApplyReportsDivergenceWithoutOverwriting(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	// A user's own agent role that happens to share the name.
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: testWorkspace,
		Name:         "app-architect",
		Kind:         "worker",
		Description:  "My own architect.",
		PromptFile:   "builtin:team-architect",
		TaskFilter:   "any",
		Effort:       "high",
		Skills:       []string{"architecture", "design"}, // same set, different order
		Labels:       []string{"design-review"},          // re-routed
	}); err != nil {
		t.Fatalf("seed agent role: %v", err)
	}
	report, err := Apply(ctx, localDeps(st), testWorkspace, fullstack(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	step := mustStep(t, report, entityRole, "app-architect")
	if step.Action != StepSkippedDiverged {
		t.Fatalf("action = %q, want skipped_diverged", step.Action)
	}
	if got := strings.Join(step.Fields, ","); got != "description,labels" {
		t.Fatalf("fields = %q, want \"description,labels\" (skills compare as sets)", got)
	}
	role, err := st.Roles().Get(ctx, testWorkspace, "app-architect")
	if err != nil {
		t.Fatalf("get agent role: %v", err)
	}
	if role.Description != "My own architect." || role.Labels[0] != "design-review" {
		t.Fatalf("apply overwrote a diverged agent role: %+v", role)
	}
	if report.Diverged != 1 || report.Skipped != 1 {
		t.Errorf("counts = diverged %d skipped %d", report.Diverged, report.Skipped)
	}
}

func TestApplyDivergedAgent(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: testWorkspace, Name: "app-architect", Kind: "worker"}); err != nil {
		t.Fatalf("seed agent role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: testWorkspace,
		Name:         "app-architect-1",
		RoleName:     "app-architect",
		Auto:         true,
		CrossRepo:    false,
		DesiredState: domain.AgentDesiredStopped,
	}); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	calls := 0
	deps := localDeps(st)
	deps.LocalMaterializer = func(context.Context, domain.Agent) error { calls++; return nil }
	report, err := Apply(ctx, deps, testWorkspace, fullstack(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	step := mustStep(t, report, entityAgent, "app-architect-1")
	if step.Action != StepSkippedDiverged {
		t.Fatalf("action = %q, want skipped_diverged", step.Action)
	}
	if got := strings.Join(step.Fields, ","); got != "desired_state,cross_repo" {
		t.Fatalf("fields = %q", got)
	}
	// Store divergence is preserved, while the existing worker's checkout is
	// still repaired along with the three untouched agents.
	if calls != 4 || report.Materialized != 4 {
		t.Errorf("materializer calls %d, report %d, want 4", calls, report.Materialized)
	}
}

func TestApplyDryRunWritesNothing(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	tpl := fullstack(t)
	deps := localDeps(st)
	deps.DryRun = true
	deps.LocalMaterializer = func(context.Context, domain.Agent) error {
		t.Error("dry run materialized a worktree")
		return nil
	}
	report, err := Apply(ctx, deps, testWorkspace, tpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !report.DryRun || report.Created != len(tpl.Roles)+len(tpl.Agents) {
		t.Fatalf("dry run report = %+v", report)
	}
	if report.Materialized != 0 {
		t.Errorf("dry run reported %d materializations", report.Materialized)
	}
	roles, err := st.Roles().List(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("list agent roles: %v", err)
	}
	if len(roles) != 0 {
		t.Fatalf("dry run created %d agent roles", len(roles))
	}
	agents, err := st.Agents().List(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("dry run created %d agents", len(agents))
	}
}

func TestApplyDryRunClassifiesExistingState(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	tpl := fullstack(t)
	if _, err := Apply(ctx, localDeps(st), testWorkspace, tpl); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	deps := localDeps(st)
	deps.DryRun = true
	report, err := Apply(ctx, deps, testWorkspace, tpl)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if report.Created != 0 || report.Skipped != len(tpl.Roles)+len(tpl.Agents) {
		t.Fatalf("dry run drifted from apply: %+v", report)
	}
}

// A failing agent-role create cascades to its agent, does not abort the run,
// and does not roll back what was already created.
func TestApplyKeepsGoingAfterAFailedStep(t *testing.T) {
	st := &brokenRoleStore{Store: newStore(t), failFor: "backend-dev"}
	report, err := Apply(context.Background(), localDeps(st), testWorkspace, fullstack(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	roleStep := mustStep(t, report, entityRole, "backend-dev")
	if roleStep.Action != StepFailed || !strings.Contains(roleStep.Error, "fleet-db is down") {
		t.Fatalf("agent role step = %+v", roleStep)
	}
	agentStep := mustStep(t, report, entityAgent, "backend-dev-1")
	if agentStep.Action != StepFailed {
		t.Fatalf("dependent agent step = %+v", agentStep)
	}
	if !strings.Contains(agentStep.Error, `agent role "backend-dev" was not created`) {
		t.Fatalf("dependent failure does not name the agent role: %q", agentStep.Error)
	}
	if report.Failed != 2 || report.Created != 7 {
		t.Fatalf("counts = failed %d created %d", report.Failed, report.Created)
	}
	if _, err := st.Agents().Get(context.Background(), testWorkspace, "app-architect-1"); err != nil {
		t.Errorf("a failed step rolled back an earlier create: %v", err)
	}
}

// An agent whose agent role lives outside the bundle fails with a legible
// message rather than the control plane's opaque validation error.
func TestApplyMissingOutOfBundleRole(t *testing.T) {
	st := newStore(t)
	tpl := fullstack(t)
	tpl.Agents = append(tpl.Agents, TemplateAgent{
		Name: "task-1", RoleName: "task", Auto: true, DesiredState: "running", CrossRepo: true,
	})
	report, err := Apply(context.Background(), localDeps(st), testWorkspace, tpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	step := mustStep(t, report, entityAgent, "task-1")
	if step.Action != StepFailed || step.Error != `agent role "task" not found` {
		t.Fatalf("step = %+v", step)
	}
}

// fleet-db validates the agent-role reference on agent create, so the
// roles-then-agents ordering is mandatory. This store enforces the same rule.
func TestApplyOrderingSatisfiesTheRoleReferenceCheck(t *testing.T) {
	base := newStore(t)
	st := &refCheckStore{Store: base, agents: &refCheckAgents{AgentStore: base.Agents(), roles: base.Roles()}}
	report, err := Apply(context.Background(), localDeps(st), testWorkspace, fullstack(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if report.Failed != 0 {
		t.Fatalf("agent creates were rejected for a missing agent role: %+v", report.Steps)
	}
}

func TestApplyRematerializesMatchingAgents(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	tpl := fullstack(t)
	if _, err := Apply(ctx, localDeps(st), testWorkspace, tpl); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	var seen []string
	deps := localDeps(st)
	deps.LocalMaterializer = func(_ context.Context, agent domain.Agent) error {
		seen = append(seen, agent.Name)
		return nil
	}
	report, err := Apply(ctx, deps, testWorkspace, tpl)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if len(seen) != len(tpl.Agents) || report.Materialized != len(tpl.Agents) {
		t.Fatalf("re-apply materialized %v (report %d), want all %d agents", seen, report.Materialized, len(tpl.Agents))
	}
	for _, step := range report.Steps {
		if step.Action != StepSkippedMatch {
			t.Errorf("%s %s: %q", step.Entity, step.Name, step.Action)
		}
	}
}

// A worktree failure fails the step and KEEPS the store row, so the next apply
// can retry it. The single-agent paths compensating-delete instead; that choice
// fights convergence here.
func TestApplyMaterializationFailureKeepsTheRow(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	tpl := fullstack(t)
	deps := localDeps(st)
	deps.LocalMaterializer = func(_ context.Context, agent domain.Agent) error {
		if agent.Name == "qa-engineer-1" {
			return errors.New("create worktree for repo \"app\": exit status 128")
		}
		return nil
	}
	report, err := Apply(ctx, deps, testWorkspace, tpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	step := mustStep(t, report, entityAgent, "qa-engineer-1")
	if step.Action != StepFailed || !strings.Contains(step.Error, "exit status 128") {
		t.Fatalf("step = %+v", step)
	}
	if _, err := st.Agents().Get(ctx, testWorkspace, "qa-engineer-1"); err != nil {
		t.Fatalf("the store row was rolled back: %v", err)
	}
	if report.Materialized != 3 {
		t.Errorf("materialized = %d, want 3", report.Materialized)
	}

	// Re-apply heals it: the row already matches, and materialization runs again.
	healed := 0
	deps.LocalMaterializer = func(context.Context, domain.Agent) error { healed++; return nil }
	second, err := Apply(ctx, deps, testWorkspace, tpl)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if healed != len(tpl.Agents) || second.Failed != 0 {
		t.Fatalf("re-apply did not heal the worktree: healed %d, failed %d", healed, second.Failed)
	}
}

// Interactive agent roles get no worktrees. Apply decides this itself rather
// than relying on the injected materializer's optional skip hook.
func TestApplySkipsMaterializationForInteractiveRoles(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: testWorkspace, Name: "lead", Kind: string(domain.RoleKindInteractive),
	}); err != nil {
		t.Fatalf("seed lead: %v", err)
	}
	tpl := TeamTemplate{
		SchemaVersion: SchemaVersion, ID: "custom", Label: "Custom", Description: "x", Revision: 1,
		Agents: []TemplateAgent{{Name: "lead-1", RoleName: "lead", Auto: true, DesiredState: "running", CrossRepo: true}},
	}
	deps := localDeps(st)
	deps.LocalMaterializer = func(context.Context, domain.Agent) error {
		t.Error("materialized a worktree for an interactive agent role")
		return nil
	}
	report, err := Apply(ctx, deps, testWorkspace, tpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if report.Materialized != 0 || report.Created != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestPreflightUnknownWorkspace(t *testing.T) {
	st := newStore(t)
	report, err := Apply(context.Background(), localDeps(st), "NOPE", fullstack(t))
	if err == nil {
		t.Fatal("apply into an unknown workspace succeeded")
	}
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("error %v does not wrap ErrNotFound", err)
	}
	if len(report.Steps) != 0 {
		t.Errorf("preflight failure produced %d steps", len(report.Steps))
	}
}

func TestPreflightLocalWorkspaceWithNoRepos(t *testing.T) {
	st := memstore.New()
	ctx := context.Background()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: testWorkspace, Name: "myproj"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	report, err := Apply(ctx, localDeps(st), testWorkspace, fullstack(t))
	if err == nil {
		t.Fatal("apply into a repo-less local workspace succeeded")
	}
	if !strings.Contains(err.Error(), "no repositories") || !strings.Contains(err.Error(), "loom repo add") {
		t.Errorf("error %q does not tell the user what to do", err)
	}
	if len(report.Steps) != 0 {
		t.Errorf("preflight failure produced %d steps", len(report.Steps))
	}
	roles, _ := st.Roles().List(ctx, testWorkspace)
	if len(roles) != 0 {
		t.Errorf("preflight failure left %d half-provisioned agent roles", len(roles))
	}
}

func TestPreflightPathlessWorkspaceWarnsAndProceeds(t *testing.T) {
	st := memstore.New()
	ctx := context.Background()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: testWorkspace, Name: "myproj"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	tpl := fullstack(t)
	report, err := Apply(ctx, ApplyDeps{Store: st}, testWorkspace, tpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "no local checkout") {
		t.Fatalf("warnings = %v", report.Warnings)
	}
	if strings.Contains(report.Warnings[0], "are running") {
		t.Error("a warning must not claim anything is running")
	}
	if report.Created != len(tpl.Roles)+len(tpl.Agents) {
		t.Errorf("control-plane rows were not provisioned: %+v", report)
	}
}

func TestPreflightMaxAgentsHeadroom(t *testing.T) {
	st := newStore(t)
	deps := localDeps(st)
	deps.RunnableAgentCount = 18
	deps.MaxAgents = DefaultMaxAgents
	report, err := Apply(context.Background(), deps, testWorkspace, fullstack(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.Warnings) != 1 {
		t.Fatalf("warnings = %v", report.Warnings)
	}
	for _, want := range []string{"max_agents limit of 20", "22 runnable agents", "keep running the old one"} {
		if !strings.Contains(report.Warnings[0], want) {
			t.Errorf("warning %q missing %q", report.Warnings[0], want)
		}
	}
	if report.Created == 0 {
		t.Error("the max_agents check must warn, not refuse")
	}
}

func TestPreflightMaxAgentsUnknownIsSilent(t *testing.T) {
	st := newStore(t)
	deps := localDeps(st)
	deps.RunnableAgentCount = 500
	report, err := Apply(context.Background(), deps, testWorkspace, fullstack(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("an unknown max_agents raised a false alarm: %v", report.Warnings)
	}
}

func TestPreflightMaxAgentsReapplyCountsOnlyNewAgents(t *testing.T) {
	st := newStore(t)
	tpl := fullstack(t)
	if _, err := Apply(context.Background(), localDeps(st), testWorkspace, tpl); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	deps := localDeps(st)
	deps.RunnableAgentCount = len(tpl.Agents)
	deps.MaxAgents = len(tpl.Agents)
	report, err := Apply(context.Background(), deps, testWorkspace, tpl)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("re-apply raised false max_agents warning: %v", report.Warnings)
	}
}

// The comparison ignores genuinely optional fields the bundle leaves zero,
// but policy zero values are requirements and must still report drift.
func TestCompareRoleIgnoresFieldsTheBundleLeavesZero(t *testing.T) {
	tpl := fullstack(t)
	frontend := tpl.Roles[1]
	maxPriority := 3
	existing := &domain.Role{
		WorkspaceKey:  testWorkspace,
		Name:          frontend.Name,
		Kind:          domain.RoleKindWorker,
		Description:   frontend.Description,
		PromptFile:    frontend.PromptFile,
		TaskFilter:    frontend.TaskFilter,
		Effort:        frontend.Effort,
		Skills:        []string{"ui", "frontend"},
		ExcludeLabels: frontend.ExcludeLabels,
		// Untouched by the bundle:
		Backend:      "claude",
		PathPatterns: []string{"web/**"},
		MaxPriority:  &maxPriority,
		Executor:     "conversation",
		Labels:       []string{"anything"},
		ReadOnly:     true,
		DeniedTools:  []string{"Bash"},
	}
	want := []string{"read_only", "denied_tools"}
	if fields := compareRole(frontend, existing); strings.Join(fields, ",") != strings.Join(want, ",") {
		t.Fatalf("fields = %v, want %v", fields, want)
	}
}

func TestCompareRoleReportsRequiredEmptyToolPolicy(t *testing.T) {
	role := fullstack(t).Roles[0]
	existing := &domain.Role{
		Name:         role.Name,
		Kind:         domain.RoleKindWorker,
		Description:  role.Description,
		PromptFile:   role.PromptFile,
		TaskFilter:   role.TaskFilter,
		Effort:       role.Effort,
		Skills:       role.Skills,
		Labels:       role.Labels,
		AllowedTools: []string{"Read"},
		DeniedTools:  []string{"Bash"},
	}
	want := []string{"denied_tools", "allowed_tools"}
	if fields := compareRole(role, existing); strings.Join(fields, ",") != strings.Join(want, ",") {
		t.Fatalf("fields = %v, want %v", fields, want)
	}
}

func TestCompareRoleFieldVocabulary(t *testing.T) {
	tpl, _ := ByID("ai-agent")
	eval := tpl.Roles[3] // the only agent role with budget and duration limits
	existing := &domain.Role{
		Name:          eval.Name,
		Kind:          domain.RoleKindInteractive,
		Description:   "different",
		PromptFile:    "builtin:team-qa",
		TaskFilter:    "any",
		Effort:        "max",
		Skills:        []string{"eval"},
		ExcludeLabels: []string{"architect"},
	}
	want := []string{"kind", "description", "prompt_file", "task_filter", "effort", "skills", "labels", "exclude_labels", "max_budget_usd", "max_run_duration"}
	got := compareRole(eval, existing)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("fields = %v, want %v", got, want)
	}
}

func TestCompareRoleTreatsLegacyBlankKindAsWorker(t *testing.T) {
	tpl := fullstack(t)
	frontend := tpl.Roles[1]
	existing := &domain.Role{
		Name:          frontend.Name,
		Description:   frontend.Description,
		PromptFile:    frontend.PromptFile,
		TaskFilter:    frontend.TaskFilter,
		Effort:        frontend.Effort,
		Skills:        frontend.Skills,
		ExcludeLabels: frontend.ExcludeLabels,
	}
	if fields := compareRole(frontend, existing); len(fields) != 0 {
		t.Fatalf("a blank stored kind was reported as divergence: %v", fields)
	}
}

// brokenRoleStore makes one agent-role create fail the way an unreachable
// control plane would.
type brokenRoleStore struct {
	*memstore.Store
	failFor string
}

func (s *brokenRoleStore) Roles() store.RoleStore {
	return &brokenRoles{RoleStore: s.Store.Roles(), failFor: s.failFor}
}

type brokenRoles struct {
	store.RoleStore
	failFor string
}

func (r *brokenRoles) Create(ctx context.Context, in store.RoleCreate) (*domain.Role, error) {
	if in.Name == r.failFor {
		return nil, errors.New("fleet-db is down")
	}
	return r.RoleStore.Create(ctx, in)
}

// refCheckStore mirrors the control plane's rule that an agent's agent role
// must already exist when the agent is created.
type refCheckStore struct {
	*memstore.Store
	agents store.AgentStore
}

func (s *refCheckStore) Agents() store.AgentStore { return s.agents }

type refCheckAgents struct {
	store.AgentStore
	roles store.RoleStore
}

func (a *refCheckAgents) Create(ctx context.Context, in store.AgentCreate) (*domain.Agent, error) {
	if _, err := a.roles.Get(ctx, in.WorkspaceKey, in.RoleName); err != nil {
		return nil, fmt.Errorf("agent role %q does not exist: %w", in.RoleName, domain.ErrInvalid)
	}
	return a.AgentStore.Create(ctx, in)
}
