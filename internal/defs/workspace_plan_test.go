package defs

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestPlanFromWorkspaceProjectsControlPlaneRecords(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "CP", Name: "Control Plane"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	maxConcurrency := 2
	budget := 1.5
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey:   "CP",
		Name:           "triage",
		Description:    "Directly managed triage role.",
		Model:          "gpt-5",
		Backend:        "codex",
		Skills:         []string{"review"},
		MaxConcurrency: &maxConcurrency,
		ReadOnly:       true,
		AllowedTools:   []string{"github.issue.read"},
		DeniedTools:    []string{"bash"},
		MaxBudgetUSD:   &budget,
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	workflowManifest := mustJSON(map[string]any{
		"builtin": "run-parent-work-items",
		"tools":   []string{"workItems.readyChildren", "taskRuns.ensure"},
		"repos":   []string{"slack-src"},
		"env":     []string{"NODE_ENV"},
	})
	if _, err := st.WorkflowDefinitions().Upsert(ctx, store.WorkflowDefinitionUpsert{
		WorkspaceKey:    "CP",
		Name:            "slack-clone-runner",
		Version:         "control-v1",
		Description:     "Run Slack clone epic tasks.",
		SingletonPolicy: "parent:${parentId}",
		Manifest:        workflowManifest,
		Status:          domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert workflow definition: %v", err)
	}
	if _, err := st.RouteBindings().Upsert(ctx, store.RouteBindingUpsert{
		WorkspaceKey:   "CP",
		DefinitionName: "slack-clone-runner",
		DefinitionType: domain.DefinitionTypeWorkflow,
		Path:           "/workflows/slack-clone-runner/run",
		Method:         "POST",
		AuthPolicy:     "workspace",
		Status:         domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert route binding: %v", err)
	}
	if _, err := st.TriggerBindings().Upsert(ctx, store.TriggerBindingUpsert{
		WorkspaceKey: "CP",
		WorkflowName: "slack-clone-runner",
		EventType:    "issue.label_added",
		Filter:       mustJSON(map[string]string{"label": "slack-clone", "type": "epic"}),
		Status:       domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert trigger binding: %v", err)
	}
	if _, err := st.RuntimeProfiles().Upsert(ctx, store.RuntimeProfileUpsert{
		WorkspaceKey: "CP",
		Name:         "local-node",
		Version:      "control-v1",
		Provider:     domain.RuntimeProviderLocal,
		Image:        "node:22",
		Repos:        []string{"slack-src"},
		Env:          []string{"NODE_ENV"},
		Status:       domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert runtime profile: %v", err)
	}
	tool := ToolModule{
		Name:        "github_issue_read",
		Description: "Read one GitHub issue visible to this workspace.",
		Version:     "tool-v1",
		SourcePath:  "control-plane:tool/github_issue_read",
		SourceHash:  "tool-hash",
		Parameters:  map[string]any{"kind": "object"},
		Handler:     "workflow",
		ReadOnly:    true,
	}
	if _, err := st.DefinitionVersions().Apply(ctx, store.DefinitionVersionApply{
		WorkspaceKey:       "CP",
		DefinitionType:     domain.DefinitionTypeTool,
		DefinitionName:     tool.Name,
		Version:            tool.Version,
		SourceHash:         tool.SourceHash,
		Manifest:           mustJSON(tool),
		CapabilityManifest: toolCapabilityManifest(tool),
		Status:             domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("apply tool definition: %v", err)
	}

	plan, err := PlanFromWorkspace(ctx, st, "CP")
	if err != nil {
		t.Fatalf("PlanFromWorkspace() error = %v", err)
	}
	if got := Summary(plan); got != "agents=1 workflows=1 runtimes=1 tools=1" {
		t.Fatalf("Summary() = %q, want direct record parity", got)
	}
	if plan.Root != "workspace:CP" {
		t.Fatalf("Root = %q, want workspace source", plan.Root)
	}
	agent := plan.Agents[0]
	if agent.Name != "triage" || agent.SourcePath != "control-plane:role/triage" {
		t.Fatalf("agent = %+v, want control-plane role projection", agent)
	}
	if agent.Model != "gpt-5" || agent.Backend != "codex" || agent.MaxConcurrency != 2 || !agent.ReadOnly {
		t.Fatalf("agent policy = %+v, want direct role policy", agent)
	}
	if !strings.Contains(fmtStringSlice(agent.Tools), "github.issue.read") || !strings.Contains(fmtStringSlice(agent.DeniedCommands), "bash") {
		t.Fatalf("agent tools = %+v denied=%+v, want direct role tool policy", agent.Tools, agent.DeniedCommands)
	}
	workflow := plan.Workflows[0]
	if workflow.Name != "slack-clone-runner" || workflow.Builtin != "run-parent-work-items" {
		t.Fatalf("workflow = %+v, want direct workflow projection", workflow)
	}
	if workflow.RoutePath != "/workflows/slack-clone-runner/run" || workflow.RouteAuth != "workspace" {
		t.Fatalf("workflow route = %q auth=%q, want active route binding", workflow.RoutePath, workflow.RouteAuth)
	}
	if workflow.TriggerEvent != "issue.label_added" || workflow.TriggerFilter["type"] != "epic" {
		t.Fatalf("workflow trigger = %q filter=%+v, want active trigger binding", workflow.TriggerEvent, workflow.TriggerFilter)
	}
	runtime := plan.Runtimes[0]
	if runtime.Name != "local-node" || runtime.Provider != domain.RuntimeProviderLocal || runtime.Image != "node:22" {
		t.Fatalf("runtime = %+v, want direct runtime profile projection", runtime)
	}
	if plan.Tools[0].Name != "github_issue_read" || plan.Tools[0].Handler != "workflow" {
		t.Fatalf("tools = %+v, want active tool DefinitionVersion projection", plan.Tools)
	}
}

func TestPlanFromWorkspacePrefersSourceBackedDefinitions(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "SRC", Name: "Source Backed"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	writeDefFile(t, root, ".loom/agents/triage.ts", `export default defineAgent({
  name: "triage",
  backend: "echo",
  model: "local/echo",
  tools: ["github.issue.read"],
  allowedCommands: ["npm test"],
});`)
	sourcePlan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := Apply(ctx, st, "SRC", "test", sourcePlan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	workspacePlan, err := PlanFromWorkspace(ctx, st, "SRC")
	if err != nil {
		t.Fatalf("PlanFromWorkspace() error = %v", err)
	}
	if len(workspacePlan.Agents) != 1 {
		t.Fatalf("agents = %+v, want one source-backed agent without duplicate role projection", workspacePlan.Agents)
	}
	agent := workspacePlan.Agents[0]
	if agent.SourcePath != sourcePlan.Agents[0].SourcePath || agent.Version != sourcePlan.Agents[0].Version {
		t.Fatalf("agent = %+v, want source-backed definition %+v", agent, sourcePlan.Agents[0])
	}
	if len(agent.AllowedCommands) != 1 || agent.AllowedCommands[0] != "npm test" {
		t.Fatalf("allowed commands = %+v, want source manifest command policy", agent.AllowedCommands)
	}
}
