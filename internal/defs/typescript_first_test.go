package defs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestInitTypeScriptProjectWritesStartContract(t *testing.T) {
	root := t.TempDir()
	if err := InitTypeScriptProject(root); err != nil {
		t.Fatalf("InitTypeScriptProject() error = %v", err)
	}
	path := filepath.Join(root, ".loom", "start.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read start contract: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"Create a TypeScript-First Loom Agent",
		"@loom/sdk",
		".loom/agents/<name>.ts",
		"connectors/",
		"Provider adapters, including the Daytona sandbox adapter",
		"loom connect <name> local",
		"loom run <name> --payload '{}'",
		"loom defs apply --start",
		"never invent or write secret values",
		"Loom/FleetDB durable records remain",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("start contract missing %q:\n%s", want, text)
		}
	}

	custom := []byte("# Custom onboarding\n")
	if err := os.WriteFile(path, custom, 0o644); err != nil {
		t.Fatalf("write custom start contract: %v", err)
	}
	if err := InitTypeScriptProject(root); err != nil {
		t.Fatalf("second InitTypeScriptProject() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read custom start contract: %v", err)
	}
	if string(got) != string(custom) {
		t.Fatalf("start contract overwritten = %q, want custom preserved", got)
	}
	connectorPath := filepath.Join(root, ".loom", "connectors", "daytona.ts")
	connectorData, err := os.ReadFile(connectorPath)
	if err != nil {
		t.Fatalf("read Daytona connector: %v", err)
	}
	if !strings.Contains(string(connectorData), "daytona as loomDaytona") ||
		!strings.Contains(string(connectorData), "export function daytona") {
		t.Fatalf("daytona connector = %s, want adapter export", connectorData)
	}

	customConnector := []byte("export const daytona = () => ({ provider: 'custom' });\n")
	if err := os.WriteFile(connectorPath, customConnector, 0o644); err != nil {
		t.Fatalf("write custom Daytona connector: %v", err)
	}
	if err := InitTypeScriptProject(root); err != nil {
		t.Fatalf("third InitTypeScriptProject() error = %v", err)
	}
	gotConnector, err := os.ReadFile(connectorPath)
	if err != nil {
		t.Fatalf("read custom Daytona connector: %v", err)
	}
	if string(gotConnector) != string(customConnector) {
		t.Fatalf("daytona connector overwritten = %q, want custom preserved", gotConnector)
	}

	typesPath := filepath.Join(root, ".loom", "runtime.d.ts")
	typesData, err := os.ReadFile(typesPath)
	if err != nil {
		t.Fatalf("read runtime declarations: %v", err)
	}
	if !strings.Contains(string(typesData), "declare module '@loom/sdk'") ||
		!strings.Contains(string(typesData), "declare module '@flue/runtime'") ||
		!strings.Contains(string(typesData), "export type FlueContext = WorkflowContext") ||
		!strings.Contains(string(typesData), "WorkflowRouteHandler") ||
		!strings.Contains(string(typesData), "check<T = unknown>") ||
		!strings.Contains(string(typesData), "connect<T = unknown>") ||
		!strings.Contains(string(typesData), "export const loom") {
		t.Fatalf("runtime declarations missing @loom/sdk client surface:\n%s", typesData)
	}
}

func TestTypeScriptFirstAgentApplyCreatesUIVisibleInstance(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	if err := InitTypeScriptProject(root); err != nil {
		t.Fatalf("InitTypeScriptProject() error = %v", err)
	}
	path, err := ScaffoldAgent(root, "hello-world")
	if err != nil {
		t.Fatalf("ScaffoldAgent() error = %v", err)
	}

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	agent, ok := FindAgent(plan, "hello-world")
	if !ok {
		t.Fatalf("FindAgent() did not find hello-world in %+v", plan.Agents)
	}
	if agent.SourcePath != path || agent.Backend != "echo" || agent.Model != "local/echo" {
		t.Fatalf("agent = %+v, want scaffolded echo/local agent from %s", agent, path)
	}
	if agent.MaxConcurrency != 1 {
		t.Fatalf("MaxConcurrency = %d, want policy maxConcurrency from TypeScript", agent.MaxConcurrency)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSFIRST", Name: "TypeScript First"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := Apply(ctx, st, "TSFIRST", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	version, err := st.DefinitionVersions().Get(ctx, "TSFIRST", domain.DefinitionTypeAgent, "hello-world", agent.Version)
	if err != nil {
		t.Fatalf("definition version not created: %v", err)
	}
	capability := jsonMap(t, version.CapabilityManifest)
	if capability["manifest_version"] != "loom.capabilities.v1" {
		t.Fatalf("capability manifest = %#v, want v1 scoped manifest", capability)
	}
	sandbox := capability["sandbox"].(map[string]any)
	if !strings.Contains(fmtStringSlice(sandbox["allowed_commands"]), "git status") {
		t.Fatalf("sandbox capability = %#v, want allowed git status command", sandbox)
	}
	runtime := capability["runtime"].(map[string]any)
	if !strings.Contains(fmtStringSlice(runtime["repos"]), ".") {
		t.Fatalf("runtime capability = %#v, want local repo grant preserved", runtime)
	}
	role, err := st.Roles().Get(ctx, "TSFIRST", "hello-world")
	if err != nil {
		t.Fatalf("role not created: %v", err)
	}
	if role.Backend != "echo" || role.Model != "local/echo" {
		t.Fatalf("role = %+v, want TypeScript backend/model", role)
	}

	instance, err := ApplyAgentInstance(ctx, st, "TSFIRST", agent, "local", true)
	if err != nil {
		t.Fatalf("ApplyAgentInstance() error = %v", err)
	}
	if instance.Name != "local" || instance.RoleName != "hello-world" {
		t.Fatalf("instance = %+v, want local instance for hello-world role", instance)
	}
	if instance.State != domain.AgentStateActive || instance.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("instance state = %s/%s, want active/running", instance.State, instance.DesiredState)
	}
	cmds, err := st.AgentCommands().List(ctx, "TSFIRST", store.AgentCommandFilter{TargetAgentID: "local"})
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Type != "start" || cmds[0].Payload["source"] != "typescript-first" {
		t.Fatalf("commands = %+v, want one queued typescript-first start command", cmds)
	}
}

func TestTypeScriptFirstPlanApplyStartCreatesAgentInstances(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeDefFile(t, root, ".loom/agents/lead.ts", `import { createAgent } from '@loom/runtime';

export default createAgent({
  name: 'lead',
  backend: 'echo',
  model: 'local/echo',
  repos: ['.'],
  instructions: 'Coordinate work.',
});`)
	writeDefFile(t, root, ".loom/agents/worker.ts", `import { createAgent, runtime } from '@loom/runtime';

export default createAgent({
  name: 'worker',
  backend: 'echo',
  model: 'local/echo',
  runtime: runtime.local({ repos: ['app'], env: ['NODE_ENV'] }),
  instructions: 'Implement assigned work.',
  policy: { maxConcurrency: 2 },
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSSTART", Name: "TypeScript Start"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := Apply(ctx, st, "TSSTART", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	instances, err := ApplyAgentInstancesForPlan(ctx, st, "TSSTART", plan, true)
	if err != nil {
		t.Fatalf("ApplyAgentInstancesForPlan() error = %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("instances = %+v, want one started instance per source-defined agent", instances)
	}
	lead, err := st.Agents().Get(ctx, "TSSTART", "lead")
	if err != nil {
		t.Fatalf("lead instance not created: %v", err)
	}
	if lead.RoleName != "lead" || lead.State != domain.AgentStateActive || lead.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("lead instance = %+v, want active/running lead role instance", lead)
	}
	worker, err := st.Agents().Get(ctx, "TSSTART", "worker")
	if err != nil {
		t.Fatalf("worker instance not created: %v", err)
	}
	if worker.RoleName != "worker" || worker.State != domain.AgentStateActive || worker.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("worker instance = %+v, want active/running worker role instance", worker)
	}
	if worker.MaxConcurrency != 2 {
		t.Fatalf("worker MaxConcurrency = %d, want source policy preserved", worker.MaxConcurrency)
	}
	if len(worker.Repos) != 1 || worker.Repos[0] != "app" {
		t.Fatalf("worker repos = %+v, want source runtime repo preserved", worker.Repos)
	}
	cmds, err := st.AgentCommands().List(ctx, "TSSTART", store.AgentCommandFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("commands = %+v, want one start command per source-defined agent", cmds)
	}
	seen := map[string]bool{}
	for _, cmd := range cmds {
		if cmd.Type != "start" || cmd.Payload["source"] != "typescript-first" {
			t.Fatalf("command = %+v, want TypeScript-first start command", cmd)
		}
		seen[cmd.TargetAgentID] = true
	}
	if !seen["lead"] || !seen["worker"] {
		t.Fatalf("commands = %+v, want start commands for lead and worker", cmds)
	}
}

func TestTypeScriptFirstAgentCanUseReusableProfile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeDefFile(t, root, ".loom/agents/reviewer.ts", `import { createAgent, defineAgentProfile } from '@loom/runtime';

const reviewProfile = defineAgentProfile({
  name: 'review-specialist',
  backend: 'echo',
  model: 'local/echo',
  instructions: 'Review plans and implementation evidence.',
  skills: ['review-checklist'],
  tools: ['github.issue.read'],
  allowedCommands: ['git status'],
});

export default createAgent(reviewProfile, {
  name: 'reviewer-bot',
  description: 'Workspace reviewer',
  tools: ['github.pr.open'],
  allowedCommands: ['go test ./internal/defs'],
});
`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	agent, ok := FindAgent(plan, "reviewer-bot")
	if !ok {
		t.Fatalf("FindAgent() did not find reviewer-bot in %+v", plan.Agents)
	}
	if agent.ProfileName != "review-specialist" || agent.Model != "local/echo" || agent.Backend != "echo" {
		t.Fatalf("agent = %+v, want reusable profile identity and behavior", agent)
	}
	if !strings.Contains(fmtStringSlice(agent.Tools), "github.issue.read") || !strings.Contains(fmtStringSlice(agent.Tools), "github.pr.open") {
		t.Fatalf("agent tools = %+v, want profile and agent tools merged", agent.Tools)
	}
	if !strings.Contains(fmtStringSlice(agent.AllowedCommands), "git status") ||
		!strings.Contains(fmtStringSlice(agent.AllowedCommands), "go test ./internal/defs") {
		t.Fatalf("agent allowed commands = %+v, want profile and agent commands merged", agent.AllowedCommands)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSPROFILE", Name: "TypeScript Profile"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := Apply(ctx, st, "TSPROFILE", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	version, err := st.DefinitionVersions().Get(ctx, "TSPROFILE", domain.DefinitionTypeAgent, "reviewer-bot", agent.Version)
	if err != nil {
		t.Fatalf("definition version not created: %v", err)
	}
	manifest := jsonMap(t, version.Manifest)
	if manifest["profile_name"] != "review-specialist" {
		t.Fatalf("manifest = %#v, want profile identity", manifest)
	}
	capability := jsonMap(t, version.CapabilityManifest)
	profileCapability, ok := capability["profile"].(map[string]any)
	if !ok || profileCapability["name"] != "review-specialist" {
		t.Fatalf("capability profile = %#v, want reusable profile identity", capability["profile"])
	}
}

func TestLoadSupportsSDKModuleImports(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".loom/agents/sdk-agent.ts", `import { defineAgent, runtime } from '@loom/sdk';

export default defineAgent({
  name: 'sdk-agent',
  backend: 'echo',
  model: 'local/echo',
  runtime: runtime.local({ repos: ['.'] }),
  instructions: 'Author through the SDK import path.',
});`)
	writeDefFile(t, root, ".loom/workflows/sdk-workflow.ts", `import { defineWorkflow, trigger } from '@loom/sdk';

export default defineWorkflow({
  name: 'sdk-workflow',
  triggers: [trigger.github('pull_request.closed', { action: 'closed', merged: true })],
  builtin: 'run-parent-work-items',
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, ok := FindAgent(plan, "sdk-agent"); !ok {
		t.Fatalf("agents = %+v, want sdk-agent loaded from @loom/sdk import", plan.Agents)
	}
	workflow, ok := FindWorkflow(plan, "sdk-workflow")
	if !ok {
		t.Fatalf("workflows = %+v, want sdk-workflow loaded from @loom/sdk import", plan.Workflows)
	}
	if workflow.TriggerEvent != "github.pull_request.closed" ||
		workflow.TriggerFilter["action"] != "closed" ||
		workflow.TriggerFilter["merged"] != "true" {
		t.Fatalf("workflow trigger = %q %+v, want normalized github trigger", workflow.TriggerEvent, workflow.TriggerFilter)
	}
}

func TestTypeScriptFirstWorkflowApplyCreatesDurableBindings(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	path, err := ScaffoldWorkflow(root, "epic-runner")
	if err != nil {
		t.Fatalf("ScaffoldWorkflow() error = %v", err)
	}
	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := Summary(plan); got != "agents=0 workflows=1 runtimes=0" {
		t.Fatalf("Summary() = %q, want one workflow", got)
	}
	workflow := plan.Workflows[0]
	if workflow.SourcePath != path || workflow.Name != "epic-runner" {
		t.Fatalf("workflow = %+v, want scaffolded epic-runner from %s", workflow, path)
	}
	if workflow.Runner != "workflow-context-v1" || workflow.Builtin != "" {
		t.Fatalf("workflow runner = %q builtin=%q, want constrained WorkflowContext runner", workflow.Runner, workflow.Builtin)
	}
	if workflow.RoutePath != "/workflows/epic-runner/run" || workflow.RouteAuth != "workspace" {
		t.Fatalf("route = %q auth=%q, want workspace HTTP route", workflow.RoutePath, workflow.RouteAuth)
	}
	if workflow.TriggerEvent != "issue.label_added" || workflow.TriggerFilter["type"] != "epic" {
		t.Fatalf("trigger = %q filter=%v, want issue label epic trigger", workflow.TriggerEvent, workflow.TriggerFilter)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSWF", Name: "TypeScript Workflow"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := Apply(ctx, st, "TSWF", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	def, err := st.WorkflowDefinitions().Get(ctx, "TSWF", "epic-runner")
	if err != nil {
		t.Fatalf("workflow definition not created: %v", err)
	}
	if def.Version != workflow.Version || def.SourceRef != workflow.SourcePath {
		t.Fatalf("definition = %+v, want TypeScript source/version", def)
	}
	capability := jsonMap(t, def.CapabilityManifest)
	if capability["manifest_version"] != "loom.capabilities.v1" {
		t.Fatalf("capability manifest = %#v, want v1 scoped manifest", capability)
	}
	workflowCaps := capability["workflow"].(map[string]any)
	if !strings.Contains(fmtStringSlice(workflowCaps["tools"]), "taskRuns.ensure") {
		t.Fatalf("workflow capabilities = %#v, want taskRuns.ensure grant", workflowCaps)
	}
	runner := capability["runner"].(map[string]any)
	if runner["context"] != "workflow-context-v1" {
		t.Fatalf("runner capability = %#v, want constrained WorkflowContext runner", runner)
	}
	ingress := capability["ingress"].(map[string]any)
	route := ingress["route"].(map[string]any)
	if route["path"] != "/workflows/epic-runner/run" || route["auth"] != "workspace" {
		t.Fatalf("route capability = %#v, want workspace route", route)
	}
	var manifest map[string]any
	if err := json.Unmarshal(def.Manifest, &manifest); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	if manifest["runner"] != "workflow-context-v1" {
		t.Fatalf("manifest runner = %v, want workflow-context-v1", manifest["runner"])
	}
	routes, err := st.RouteBindings().List(ctx, "TSWF", store.RouteBindingFilter{DefinitionName: "epic-runner"})
	if err != nil {
		t.Fatalf("list route bindings: %v", err)
	}
	if len(routes) != 1 || routes[0].Path != "/workflows/epic-runner/run" || routes[0].AuthPolicy != "workspace" {
		t.Fatalf("routes = %+v, want one workspace HTTP binding", routes)
	}
	triggers, err := st.TriggerBindings().List(ctx, "TSWF", store.TriggerBindingFilter{WorkflowName: "epic-runner"})
	if err != nil {
		t.Fatalf("list trigger bindings: %v", err)
	}
	if len(triggers) != 1 || triggers[0].EventType != "issue.label_added" || !strings.Contains(string(triggers[0].Filter), `"epic"`) {
		t.Fatalf("triggers = %+v, want one issue label trigger", triggers)
	}
}

func TestApplyRejectsWorkflowRouteCollision(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeDefFile(t, root, ".loom/workflows/slack-a.ts", `import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'slack-a',
  path: '/workflows/slack/run',
  auth: 'workspace',
  builtin: 'run-parent-work-items',
});`)
	writeDefFile(t, root, ".loom/workflows/slack-b.ts", `import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'slack-b',
  path: '/workflows/slack/run',
  auth: 'workspace',
  builtin: 'run-parent-work-items',
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSCOLLIDE", Name: "TypeScript Collisions"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	err = Apply(ctx, st, "TSCOLLIDE", "test", plan)
	if err == nil || !strings.Contains(err.Error(), "workflow route collision POST /workflows/slack/run") {
		t.Fatalf("Apply() error = %v, want workflow route collision", err)
	}
}

func TestApplyRejectsWorkflowTriggerCollision(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeDefFile(t, root, ".loom/workflows/slack-a.ts", `import { defineWorkflow, trigger } from '@loom/runtime';

export default defineWorkflow({
  name: 'slack-a',
  triggers: [trigger.issueLabelAdded({ label: 'ready', type: 'epic' })],
  builtin: 'run-parent-work-items',
});`)
	writeDefFile(t, root, ".loom/workflows/slack-b.ts", `import { defineWorkflow, trigger } from '@loom/runtime';

export default defineWorkflow({
  name: 'slack-b',
  triggers: [trigger.issueLabelAdded({ type: 'epic', label: 'ready' })],
  builtin: 'run-parent-work-items',
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSTRIGGER", Name: "TypeScript Trigger Collisions"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	err = Apply(ctx, st, "TSTRIGGER", "test", plan)
	if err == nil || !strings.Contains(err.Error(), `workflow trigger collision issue.label_added {"label":"ready","type":"epic"}`) {
		t.Fatalf("Apply() error = %v, want workflow trigger collision", err)
	}
}

func TestTypeScriptFirstSkillImportBundlesMetadata(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	skillPath, err := ScaffoldSkill(root, "triage")
	if err != nil {
		t.Fatalf("ScaffoldSkill() error = %v", err)
	}
	writeDefFile(t, root, ".loom/skills/triage/references/checklist.md", "Check reproduction, root cause, and validation evidence.\n")
	writeDefFile(t, root, ".loom/agents/triage-agent.ts", `import { createAgent, runtime } from '@loom/runtime';
import triageSkill from '../skills/triage/SKILL.md' with { type: 'skill' };

export default createAgent({
  name: 'triage-agent',
  backend: 'echo',
  model: 'local/echo',
  runtime: runtime.local({ repos: ['.'], env: [] }),
  instructions: 'Use imported skills when they match the requested work.',
  skills: [triageSkill],
  tools: [],
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := Summary(plan); got != "agents=1 workflows=0 runtimes=0 skills=1" {
		t.Fatalf("Summary() = %q, want agent plus skill bundle", got)
	}
	if len(plan.Skills) != 1 {
		t.Fatalf("skills = %+v, want one imported skill", plan.Skills)
	}
	skill := plan.Skills[0]
	if skill.Name != "triage" || skill.SourcePath != skillPath {
		t.Fatalf("skill = %+v, want triage from %s", skill, skillPath)
	}
	if !strings.Contains(skill.Instructions, "Before finishing") {
		t.Fatalf("skill instructions = %q, want scaffolded instructions", skill.Instructions)
	}
	if len(skill.Resources) != 1 || skill.Resources[0] != "references/checklist.md" {
		t.Fatalf("skill resources = %+v, want packaged checklist", skill.Resources)
	}
	agent, ok := FindAgent(plan, "triage-agent")
	if !ok {
		t.Fatalf("FindAgent() did not find triage-agent")
	}
	if len(agent.Skills) != 1 || agent.Skills[0] != "triage" {
		t.Fatalf("agent skills = %+v, want imported skill name", agent.Skills)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSSKILL", Name: "TypeScript Skills"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := Apply(ctx, st, "TSSKILL", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, err := st.DefinitionVersions().Get(ctx, "TSSKILL", domain.DefinitionTypeSkill, "triage", skill.Version); err != nil {
		t.Fatalf("skill definition version not created: %v", err)
	}
	role, err := st.Roles().Get(ctx, "TSSKILL", "triage-agent")
	if err != nil {
		t.Fatalf("role not created: %v", err)
	}
	if len(role.Skills) != 1 || role.Skills[0] != "triage" {
		t.Fatalf("role skills = %+v, want imported skill name", role.Skills)
	}
	version, err := st.DefinitionVersions().Get(ctx, "TSSKILL", domain.DefinitionTypeAgent, "triage-agent", agent.Version)
	if err != nil {
		t.Fatalf("agent definition version not created: %v", err)
	}
	capability := jsonMap(t, version.CapabilityManifest)
	model := capability["model"].(map[string]any)
	if model["prompt_bundle_hash"] == "" {
		t.Fatalf("model capability = %#v, want prompt bundle hash", model)
	}
	bundles := model["skill_bundles"].([]any)
	if len(bundles) != 1 {
		t.Fatalf("skill_bundles = %#v, want one bundle", bundles)
	}
	bundle := bundles[0].(map[string]any)
	if bundle["name"] != "triage" || bundle["source_hash"] != skill.SourceHash {
		t.Fatalf("skill bundle = %#v, want triage metadata", bundle)
	}
}

func TestTypeScriptFirstToolDefinitionsBecomeDurableCapabilities(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeDefFile(t, root, ".loom/tools/create-channel.ts", `import { Type, defineTool } from '@loom/runtime';

export default defineTool({
  name: 'create_channel',
  description: 'Create one channel after workspace policy checks pass.',
  parameters: Type.Object({
    name: Type.String({ description: 'Lowercase channel name' }),
    private: Type.Optional(Type.Boolean()),
  }),
  handler: 'workflow',
  timeout: '30s',
  cancellable: true,
  repos: ['slack-src'],
  env: ['SLACK_TOKEN'],
  execute: async ({ name }) => `+"`"+`created ${name}`+"`"+`,
});`)
	writeDefFile(t, root, ".loom/agents/slack-agent.ts", `import { createAgent, runtime } from '@loom/runtime';
import createChannel from '../tools/create-channel';

export default createAgent({
  name: 'slack-agent',
  backend: 'echo',
  model: 'local/echo',
  runtime: runtime.local({ repos: ['slack-src'], env: ['SLACK_TOKEN'] }),
  instructions: 'Use approved tools for Slack workspace actions.',
  tools: [createChannel],
  policy: {
    allowedCommands: ['npm test'],
  },
});`)
	writeDefFile(t, root, ".loom/workflows/provision-channel.ts", `import { defineWorkflow } from '@loom/runtime';
import createChannel from '../tools/create-channel';

export default defineWorkflow({
  name: 'provision-channel',
  description: 'Provision a Slack channel for an approved request.',
  builtin: 'run-parent-work-items',
  tools: [createChannel],
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := Summary(plan); got != "agents=1 workflows=1 runtimes=0 tools=1" {
		t.Fatalf("Summary() = %q, want tool in TypeScript plan", got)
	}
	if len(plan.Tools) != 1 {
		t.Fatalf("tools = %+v, want one typed tool definition", plan.Tools)
	}
	tool := plan.Tools[0]
	if tool.Name != "create_channel" || tool.Handler != "workflow" || tool.Timeout != "30s" || !tool.Cancellable {
		t.Fatalf("tool = %+v, want create_channel with workflow handler timeout/cancellation policy", tool)
	}
	if len(tool.Parameters) == 0 || len(tool.Env) != 1 || tool.Env[0] != "SLACK_TOKEN" {
		t.Fatalf("tool metadata = %+v, want parameters and env policy", tool)
	}
	agent, ok := FindAgent(plan, "slack-agent")
	if !ok {
		t.Fatalf("FindAgent() did not find slack-agent")
	}
	if len(agent.Tools) != 1 || agent.Tools[0] != "create_channel" {
		t.Fatalf("agent tools = %+v, want imported typed tool name", agent.Tools)
	}
	workflow, ok := FindWorkflow(plan, "provision-channel")
	if !ok {
		t.Fatalf("FindWorkflow() did not find provision-channel")
	}
	if len(workflow.Tools) != 1 || workflow.Tools[0] != "create_channel" {
		t.Fatalf("workflow tools = %+v, want imported typed tool name", workflow.Tools)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSTOOL", Name: "TypeScript Tools"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := Apply(ctx, st, "TSTOOL", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	toolVersion, err := st.DefinitionVersions().Get(ctx, "TSTOOL", domain.DefinitionTypeTool, "create_channel", tool.Version)
	if err != nil {
		t.Fatalf("tool definition version not created: %v", err)
	}
	toolCapability := jsonMap(t, toolVersion.CapabilityManifest)
	execution := toolCapability["execution"].(map[string]any)
	if execution["handler"] != "workflow" || execution["timeout"] != "30s" || execution["cancellable"] != true {
		t.Fatalf("tool execution capability = %#v, want workflow handler timeout/cancellation policy", execution)
	}
	role, err := st.Roles().Get(ctx, "TSTOOL", "slack-agent")
	if err != nil {
		t.Fatalf("role not created: %v", err)
	}
	if strings.Contains(fmtStringSlice(role.AllowedTools), "create_channel") {
		t.Fatalf("role allowed tools = %+v, want typed tool kept out of sandbox command policy", role.AllowedTools)
	}
	if !strings.Contains(fmtStringSlice(role.AllowedTools), "npm test") {
		t.Fatalf("role allowed tools = %+v, want sandbox command policy preserved", role.AllowedTools)
	}
	agentVersion, err := st.DefinitionVersions().Get(ctx, "TSTOOL", domain.DefinitionTypeAgent, "slack-agent", agent.Version)
	if err != nil {
		t.Fatalf("agent definition version not created: %v", err)
	}
	agentCapability := jsonMap(t, agentVersion.CapabilityManifest)
	model := agentCapability["model"].(map[string]any)
	toolDefs := model["tool_definitions"].([]any)
	if len(toolDefs) != 1 || toolDefs[0].(map[string]any)["name"] != "create_channel" {
		t.Fatalf("agent tool_definitions = %#v, want create_channel metadata", toolDefs)
	}
	def, err := st.WorkflowDefinitions().Get(ctx, "TSTOOL", "provision-channel")
	if err != nil {
		t.Fatalf("workflow definition not created: %v", err)
	}
	workflowCapability := jsonMap(t, def.CapabilityManifest)
	workflowCaps := workflowCapability["workflow"].(map[string]any)
	workflowToolDefs := workflowCaps["tool_definitions"].([]any)
	if len(workflowToolDefs) != 1 || workflowToolDefs[0].(map[string]any)["handler"] != "workflow" {
		t.Fatalf("workflow tool_definitions = %#v, want workflow handler metadata", workflowToolDefs)
	}
}

func TestTypeScriptFirstWorkflowBindsRuntimeProfile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeDefFile(t, root, ".loom/runtimes/local-node.ts", `import { runtime } from '@loom/runtime';

export default runtime.local({
  name: 'local-node',
  image: 'node:22',
  cwd: '.',
  workspaceSkillDirs: ['.agents/skills'],
  workspace: {
    providerWorkspaceId: 'local-dev-workspace',
    owner: 'loom',
    cleanup: { mode: 'after_ttl', ttl: '24h' },
    filesystem: { persistence: 'durable', retention: '7d' },
  },
  capabilities: {
    filesystem: { read: true, write: true, artifactURI: true },
    shell: { enabled: true, commands: ['node', 'npm'] },
    network: { enabled: false, policy: 'disabled' },
    lifecycle: { materialize: true, cleanup: true, release: true, cancellation: true, defaultTimeout: '30m' },
  },
  repos: ['slack-src'],
  env: ['NODE_ENV'],
  cpu: '2',
  memory: '4Gi',
});`)
	writeDefFile(t, root, ".loom/workflows/runtime-bound.ts", `import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'runtime-bound',
  builtin: 'run-parent-work-items',
  runtimeProfile: 'local-node',
  env: ['WORKFLOW_ONLY'],
  repos: ['workflow-src'],
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := Summary(plan); got != "agents=0 workflows=1 runtimes=1" {
		t.Fatalf("Summary() = %q, want workflow plus runtime profile", got)
	}
	workflow, ok := FindWorkflow(plan, "runtime-bound")
	if !ok {
		t.Fatalf("FindWorkflow() did not find runtime-bound")
	}
	if workflow.RuntimeProfileName != "local-node" {
		t.Fatalf("workflow runtime profile = %q, want local-node", workflow.RuntimeProfileName)
	}
	runtimeDef := plan.Runtimes[0]
	if runtimeDef.CWD != "." {
		t.Fatalf("runtime cwd = %q, want explicit local cwd", runtimeDef.CWD)
	}
	if len(runtimeDef.WorkspaceSkillDirs) != 1 || runtimeDef.WorkspaceSkillDirs[0] != ".agents/skills" {
		t.Fatalf("runtime workspace skill dirs = %+v, want .agents/skills", runtimeDef.WorkspaceSkillDirs)
	}
	if runtimeDef.Workspace == nil || runtimeDef.Workspace.ProviderWorkspaceID != "local-dev-workspace" ||
		runtimeDef.Workspace.Owner != "loom" || runtimeDef.Workspace.Cleanup == nil ||
		runtimeDef.Workspace.Cleanup.Mode != "after_ttl" || runtimeDef.Workspace.Cleanup.TTL != "24h" ||
		runtimeDef.Workspace.Filesystem == nil || runtimeDef.Workspace.Filesystem.Persistence != "durable" ||
		runtimeDef.Workspace.Filesystem.Retention != "7d" {
		t.Fatalf("runtime workspace policy = %+v, want lifecycle metadata", runtimeDef.Workspace)
	}
	if runtimeDef.Capabilities == nil || runtimeDef.Capabilities.Filesystem == nil ||
		runtimeDef.Capabilities.Filesystem.Read == nil || !*runtimeDef.Capabilities.Filesystem.Read ||
		runtimeDef.Capabilities.Filesystem.Write == nil || !*runtimeDef.Capabilities.Filesystem.Write ||
		runtimeDef.Capabilities.Shell == nil || runtimeDef.Capabilities.Shell.Enabled == nil ||
		!*runtimeDef.Capabilities.Shell.Enabled || len(runtimeDef.Capabilities.Shell.Commands) != 2 ||
		runtimeDef.Capabilities.Network == nil || runtimeDef.Capabilities.Network.Enabled == nil ||
		*runtimeDef.Capabilities.Network.Enabled || runtimeDef.Capabilities.Network.Policy != "disabled" ||
		runtimeDef.Capabilities.Env == nil || len(runtimeDef.Capabilities.Env.Forwarded) != 1 ||
		runtimeDef.Capabilities.Lifecycle == nil || runtimeDef.Capabilities.Lifecycle.DefaultTimeout != "30m" {
		t.Fatalf("runtime capabilities = %+v, want explicit capability matrix", runtimeDef.Capabilities)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSRUNTIME", Name: "TypeScript Runtime"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := Apply(ctx, st, "TSRUNTIME", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	def, err := st.WorkflowDefinitions().Get(ctx, "TSRUNTIME", "runtime-bound")
	if err != nil {
		t.Fatalf("workflow definition not created: %v", err)
	}
	if def.RuntimeProfileName != "local-node" {
		t.Fatalf("durable workflow runtime profile = %q, want local-node", def.RuntimeProfileName)
	}
	capability := jsonMap(t, def.CapabilityManifest)
	runtimeCaps := capability["runtime"].(map[string]any)
	if runtimeCaps["profile"] != "local-node" {
		t.Fatalf("runtime capability = %#v, want profile binding", runtimeCaps)
	}
	profile, err := st.RuntimeProfiles().Get(ctx, "TSRUNTIME", "local-node")
	if err != nil {
		t.Fatalf("runtime profile not created: %v", err)
	}
	manifest := jsonMap(t, profile.Manifest)
	workspaceManifest, ok := manifest["workspace"].(map[string]any)
	if !ok || workspaceManifest["provider_workspace_id"] != "local-dev-workspace" || workspaceManifest["owner"] != "loom" {
		t.Fatalf("runtime profile workspace manifest = %#v, want durable lifecycle metadata", manifest["workspace"])
	}
	capabilityManifest, ok := manifest["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("runtime profile capabilities manifest = %#v, want durable capability metadata", manifest["capabilities"])
	}
	fsCaps, ok := capabilityManifest["filesystem"].(map[string]any)
	if !ok || fsCaps["read"] != true || fsCaps["write"] != true || fsCaps["persistence"] != "durable" {
		t.Fatalf("filesystem capabilities = %#v, want durable filesystem capability metadata", capabilityManifest["filesystem"])
	}
	shellCaps, ok := capabilityManifest["shell"].(map[string]any)
	if !ok || shellCaps["enabled"] != true || len(shellCaps["commands"].([]any)) != 2 {
		t.Fatalf("shell capabilities = %#v, want durable shell capability metadata", capabilityManifest["shell"])
	}
	networkCaps, ok := capabilityManifest["network"].(map[string]any)
	if !ok || networkCaps["enabled"] != false || networkCaps["policy"] != "disabled" {
		t.Fatalf("network capabilities = %#v, want durable network capability metadata", capabilityManifest["network"])
	}
}

func TestTypeScriptFirstRuntimeProfileHelper(t *testing.T) {
	root := t.TempDir()

	writeDefFile(t, root, ".loom/runtimes/remote-dev.ts", `import { defineRuntimeProfile } from '@loom/sdk';

export default defineRuntimeProfile({
  name: 'remote-dev',
  provider: 'e2b',
  cwd: '/workspace/app',
  repos: ['app'],
  env: ['NODE_ENV'],
  workspace: {
    providerWorkspaceId: 'e2b-dev-1',
    owner: 'loom',
    cleanup: { mode: 'after_ttl', ttl: '6h' },
    filesystem: { persistence: 'session', durability: 'provider', retention: '1d' },
  },
  capabilities: {
    filesystem: { read: true, write: true, persistence: 'session' },
    shell: { enabled: true, commands: ['node', 'npm'] },
    network: { enabled: true, policy: 'provider_default' },
    lifecycle: { materialize: true, cleanup: true, cancellation: true, defaultTimeout: '20m' },
  },
});`)
	writeDefFile(t, root, ".loom/workflows/remote-run.ts", `import { defineWorkflow } from '@loom/sdk';

export default defineWorkflow({
  name: 'remote-run',
  builtin: 'run-parent-work-items',
  runtimeProfile: 'remote-dev',
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(plan.Runtimes) != 1 {
		t.Fatalf("runtimes = %+v, want one defineRuntimeProfile runtime", plan.Runtimes)
	}
	rt := plan.Runtimes[0]
	if rt.Name != "remote-dev" || rt.Provider != domain.RuntimeProviderE2B || rt.CWD != "/workspace/app" {
		t.Fatalf("runtime = %+v, want explicit remote runtime profile", rt)
	}
	if rt.Workspace == nil || rt.Workspace.ProviderWorkspaceID != "e2b-dev-1" ||
		rt.Workspace.Cleanup == nil || rt.Workspace.Cleanup.TTL != "6h" ||
		rt.Workspace.Filesystem == nil || rt.Workspace.Filesystem.Persistence != "session" {
		t.Fatalf("runtime workspace = %+v, want durable remote lifecycle policy", rt.Workspace)
	}
	if rt.Capabilities == nil || rt.Capabilities.Shell == nil || len(rt.Capabilities.Shell.Commands) != 2 ||
		rt.Capabilities.Lifecycle == nil || rt.Capabilities.Lifecycle.DefaultTimeout != "20m" {
		t.Fatalf("runtime capabilities = %+v, want explicit shell/lifecycle policy", rt.Capabilities)
	}
	workflow, ok := FindWorkflow(plan, "remote-run")
	if !ok || workflow.RuntimeProfileName != "remote-dev" {
		t.Fatalf("workflow = %+v, want runtime profile binding", workflow)
	}
}

func TestLoadDaytonaRuntimeProfileWithDeclarativeImage(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".loom/runtimes/daytona-dev.ts", `import { Image } from '@daytona/sdk';
import { runtime } from '@loom/sdk';

export default runtime.daytona({
  name: 'daytona-dev',
  image: Image.debianSlim('3.12')
    .runCommands('apt-get update && apt-get install -y --no-install-recommends git nodejs npm && rm -rf /var/lib/apt/lists/*')
    .workdir('/workspace/project'),
  language: 'typescript',
  cwd: '/workspace/project',
  repos: ['app'],
  env: ['GITHUB_TOKEN', 'NODE_ENV'],
  resources: { cpu: 2, memory: 4, disk: 8 },
  autoStopInterval: 60,
  autoArchiveInterval: 240,
  autoDeleteInterval: 1440,
  target: 'us',
  apiKeyEnv: 'DAYTONA_API_KEY',
  buildLogs: 'inherit',
  repoUrl: 'https://github.com/acme/app.git',
  branch: 'main',
  ref: 'refs/heads/main',
  gitTokenEnv: 'GITHUB_TOKEN',
  gitUsername: 'x-access-token',
  gitDeployKeyEnv: 'GIT_DEPLOY_KEY',
  openaiApiKeyEnv: 'OPENAI_API_KEY',
  codexAuthFileEnv: 'CODEX_AUTH_FILE',
  setupCommands: ['npm ci', 'npm test -- --runInBand'],
  setupTimeout: 120,
  healthTimeout: 15,
  runTimeout: 600,
  workspace: {
    providerWorkspaceId: 'daytona-sandbox-1',
    owner: 'loom',
    cleanup: { mode: 'after_ttl', ttl: '6h' },
    filesystem: { persistence: 'session', durability: 'provider', retention: '1d' },
  },
  capabilities: {
    filesystem: { read: true, write: true, policy: 'provider_default' },
    shell: { enabled: true, commands: ['git', 'npm', 'node'] },
    network: { enabled: true, policy: 'provider_default' },
    lifecycle: { materialize: true, cleanup: true, release: true, cancellation: true },
  },
});`)
	writeDefFile(t, root, ".loom/workflows/daytona-run.ts", `import { defineWorkflow } from '@loom/sdk';

export default defineWorkflow({
  name: 'daytona-run',
  builtin: 'run-parent-work-items',
  runtimeProfile: 'daytona-dev',
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(plan.Runtimes) != 1 {
		t.Fatalf("runtimes = %+v, want one Daytona runtime", plan.Runtimes)
	}
	rt := plan.Runtimes[0]
	if rt.Name != "daytona-dev" || rt.Provider != domain.RuntimeProviderDaytona || rt.CWD != "/workspace/project" {
		t.Fatalf("runtime = %+v, want Daytona runtime profile", rt)
	}
	if rt.Image != "" {
		t.Fatalf("runtime image = %q, want declarative image only in Daytona metadata", rt.Image)
	}
	if rt.Daytona["language"] != "typescript" || rt.Daytona["target"] != "us" ||
		rt.Daytona["api_key_env"] != "DAYTONA_API_KEY" || rt.Daytona["build_logs"] != "inherit" {
		t.Fatalf("daytona metadata = %+v, want SDK create options", rt.Daytona)
	}
	if rt.Daytona["repo_url"] != "https://github.com/acme/app.git" ||
		rt.Daytona["branch"] != "main" ||
		rt.Daytona["ref"] != "refs/heads/main" {
		t.Fatalf("daytona materialization metadata = %+v, want repo/branch/ref", rt.Daytona)
	}
	if rt.Daytona["git_token_env"] != "GITHUB_TOKEN" ||
		rt.Daytona["git_username"] != "x-access-token" ||
		rt.Daytona["git_deploy_key_env"] != "GIT_DEPLOY_KEY" ||
		rt.Daytona["openai_api_key_env"] != "OPENAI_API_KEY" ||
		rt.Daytona["codex_auth_file_env"] != "CODEX_AUTH_FILE" {
		t.Fatalf("daytona auth metadata = %+v, want configured auth env names", rt.Daytona)
	}
	setupCommands, ok := rt.Daytona["setup_commands"].([]any)
	if !ok || len(setupCommands) != 2 || setupCommands[0] != "npm ci" || setupCommands[1] != "npm test -- --runInBand" {
		t.Fatalf("daytona setup commands = %+v, want configured setup commands", rt.Daytona["setup_commands"])
	}
	if rt.Daytona["setup_timeout"] != float64(120) ||
		rt.Daytona["health_timeout"] != float64(15) ||
		rt.Daytona["run_timeout"] != float64(600) {
		t.Fatalf("daytona command timeouts = %+v, want setup/health/run timeouts", rt.Daytona)
	}
	if rt.Daytona["auto_stop_interval"] != float64(60) ||
		rt.Daytona["auto_archive_interval"] != float64(240) ||
		rt.Daytona["auto_delete_interval"] != float64(1440) {
		t.Fatalf("daytona intervals = %+v, want auto lifecycle intervals", rt.Daytona)
	}
	resources, ok := rt.Daytona["resources"].(map[string]any)
	if !ok || resources["cpu"] != float64(2) || resources["memory"] != float64(4) || resources["disk"] != float64(8) {
		t.Fatalf("daytona resources = %+v, want cpu/memory/disk", rt.Daytona["resources"])
	}
	image, ok := rt.Daytona["image"].(map[string]any)
	if !ok || image["base"] != "debian-slim:3.12" {
		t.Fatalf("daytona image = %+v, want declarative image metadata", rt.Daytona["image"])
	}
	steps, ok := image["steps"].([]any)
	if !ok || len(steps) != 2 {
		t.Fatalf("daytona image steps = %+v, want runCommands and workdir steps", image["steps"])
	}
	workflow, ok := FindWorkflow(plan, "daytona-run")
	if !ok || workflow.RuntimeProfileName != "daytona-dev" {
		t.Fatalf("workflow = %+v, want Daytona runtime profile binding", workflow)
	}
}

func TestTypeScriptFirstAgentDaytonaRuntimeAppliesToRole(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := InitTypeScriptProject(root); err != nil {
		t.Fatalf("InitTypeScriptProject() error = %v", err)
	}
	writeDefFile(t, root, ".loom/agents/daytona-worker.ts", `import { defineAgent, runtime } from '@loom/sdk';

export default defineAgent({
  name: 'daytona-worker',
  backend: 'codex',
  model: 'openai/gpt-5.5',
  runtime: runtime.daytona({
    name: 'daytona-agent',
    cwd: '/workspace/project',
    language: 'typescript',
    target: 'us',
    apiKeyEnv: 'DAYTONA_API_KEY',
    autoStopInterval: 15,
  }),
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	agent, ok := FindAgent(plan, "daytona-worker")
	if !ok {
		t.Fatalf("FindAgent() did not find daytona-worker in %+v", plan.Agents)
	}
	if agent.RuntimeProvider != domain.RuntimeProviderDaytona || agent.RuntimeProfileName != "daytona-agent" || agent.RuntimeCWD != "/workspace/project" {
		t.Fatalf("agent runtime = provider:%q profile:%q cwd:%q", agent.RuntimeProvider, agent.RuntimeProfileName, agent.RuntimeCWD)
	}
	if agent.RuntimeDaytona["language"] != "typescript" || agent.RuntimeDaytona["target"] != "us" ||
		agent.RuntimeDaytona["api_key_env"] != "DAYTONA_API_KEY" || agent.RuntimeDaytona["auto_stop_interval"] != float64(15) {
		t.Fatalf("agent Daytona metadata = %+v", agent.RuntimeDaytona)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSDAYTONAAGENT", Name: "TypeScript Daytona Agent"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := Apply(ctx, st, "TSDAYTONAAGENT", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	role, err := st.Roles().Get(ctx, "TSDAYTONAAGENT", "daytona-worker")
	if err != nil {
		t.Fatalf("get role: %v", err)
	}
	if role.RuntimeProvider != domain.RuntimeProviderDaytona || role.RuntimeProfileName != "daytona-agent" || role.RuntimeCWD != "/workspace/project" {
		t.Fatalf("role runtime = provider:%q profile:%q cwd:%q", role.RuntimeProvider, role.RuntimeProfileName, role.RuntimeCWD)
	}
	if role.RuntimeDaytona["language"] != "typescript" || role.RuntimeDaytona["target"] != "us" {
		t.Fatalf("role Daytona metadata = %+v", role.RuntimeDaytona)
	}
}

func TestTypeScriptFirstAgentDaytonaAdapterOptionsApplyToRole(t *testing.T) {
	root := t.TempDir()
	if err := InitTypeScriptProject(root); err != nil {
		t.Fatalf("InitTypeScriptProject() error = %v", err)
	}
	writeDefFile(t, root, ".loom/agents/daytona-adapter-worker.ts", `import { daytona, defineAgent } from '@loom/sdk';

const sandbox = {
  id: 'external-flue-style-sandbox',
  cwd: '/workspace/project',
  daytona: { target: 'us' },
};

export default defineAgent({
  name: 'daytona-adapter-worker',
  backend: 'codex',
  runtime: daytona(sandbox, {
    name: 'project',
    repos: ['app'],
    env: ['OPENAI_API_KEY', 'GH_TOKEN'],
    language: 'typescript',
    resources: { cpu: 2, memory: 4 },
    envVars: { NODE_ENV: 'test' },
    autoStopInterval: 0,
    autoDeleteInterval: 0,
    ephemeral: false,
    repoUrl: 'https://github.com/acme/app.git',
    branch: 'main',
    gitTokenEnv: 'GH_TOKEN',
    openaiApiKeyEnv: 'OPENAI_API_KEY',
    setupCommands: ['npm ci'],
    createTimeout: 90,
    buildLogs: 'inherit',
  }),
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	agent, ok := FindAgent(plan, "daytona-adapter-worker")
	if !ok {
		t.Fatalf("FindAgent() did not find daytona-adapter-worker in %+v", plan.Agents)
	}
	if agent.RuntimeProvider != domain.RuntimeProviderDaytona ||
		agent.RuntimeProfileName != "project" ||
		agent.RuntimeCWD != "/workspace/project" {
		t.Fatalf("agent runtime = provider:%q profile:%q cwd:%q", agent.RuntimeProvider, agent.RuntimeProfileName, agent.RuntimeCWD)
	}
	if len(agent.Repos) != 1 || agent.Repos[0] != "app" ||
		len(agent.Env) != 2 || agent.Env[0] != "OPENAI_API_KEY" || agent.Env[1] != "GH_TOKEN" {
		t.Fatalf("agent repos/env = %+v/%+v, want adapter options propagated", agent.Repos, agent.Env)
	}
	if agent.RuntimeDaytona["sandbox_id"] != nil || agent.RuntimeDaytona["sandboxId"] != nil {
		t.Fatalf("agent Daytona metadata = %+v, external sandbox id should not become daemon create config", agent.RuntimeDaytona)
	}
	if agent.RuntimeDaytona["language"] != "typescript" ||
		agent.RuntimeDaytona["target"] != "us" ||
		agent.RuntimeDaytona["repo_url"] != "https://github.com/acme/app.git" ||
		agent.RuntimeDaytona["branch"] != "main" ||
		agent.RuntimeDaytona["git_token_env"] != "GH_TOKEN" ||
		agent.RuntimeDaytona["openai_api_key_env"] != "OPENAI_API_KEY" ||
		agent.RuntimeDaytona["create_timeout"] != float64(90) ||
		agent.RuntimeDaytona["build_logs"] != "inherit" {
		t.Fatalf("agent Daytona metadata = %+v, want adapter runtime options", agent.RuntimeDaytona)
	}
	if agent.RuntimeDaytona["auto_stop_interval"] != float64(0) ||
		agent.RuntimeDaytona["auto_delete_interval"] != float64(0) ||
		agent.RuntimeDaytona["ephemeral"] != false {
		t.Fatalf("agent Daytona lifecycle metadata = %+v, want zero/false options preserved", agent.RuntimeDaytona)
	}
	envVars, ok := agent.RuntimeDaytona["env_vars"].(map[string]any)
	if !ok || envVars["NODE_ENV"] != "test" {
		t.Fatalf("agent Daytona env_vars = %+v, want adapter env vars", agent.RuntimeDaytona["env_vars"])
	}
	setupCommands, ok := agent.RuntimeDaytona["setup_commands"].([]any)
	if !ok || len(setupCommands) != 1 || setupCommands[0] != "npm ci" {
		t.Fatalf("agent Daytona setup_commands = %+v, want adapter setup commands", agent.RuntimeDaytona["setup_commands"])
	}
}

func TestTypeScriptFirstBuiltInTaskCanOverrideDaytonaRuntime(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := InitTypeScriptProject(root); err != nil {
		t.Fatalf("InitTypeScriptProject() error = %v", err)
	}
	writeDefFile(t, root, ".loom/agents/task.ts", `import { defineAgent, runtime } from '@loom/sdk';

export default defineAgent({
  name: 'task',
  backend: 'codex',
  repos: ['frontend'],
  runtime: runtime.daytona({
    name: 'daytona-task',
    cwd: '/workspace/project',
    repoUrl: 'https://github.com/acme/frontend.git',
    branch: 'main',
    setupCommands: ['npm install'],
    apiKeyEnv: 'DAYTONA_API_KEY',
    openaiApiKeyEnv: 'OPENAI_API_KEY',
  }),
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSDAYTONATASK", Name: "TypeScript Daytona Task"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := Apply(ctx, st, "TSDAYTONATASK", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	role, err := st.Roles().Get(ctx, "TSDAYTONATASK", "task")
	if err != nil {
		t.Fatalf("get role: %v", err)
	}
	if role.PromptFile != "" {
		t.Fatalf("built-in task prompt_file = %q, want empty so built-in command remains usable", role.PromptFile)
	}
	if role.RuntimeProvider != domain.RuntimeProviderDaytona || role.RuntimeProfileName != "daytona-task" || role.RuntimeCWD != "/workspace/project" {
		t.Fatalf("role runtime = provider:%q profile:%q cwd:%q", role.RuntimeProvider, role.RuntimeProfileName, role.RuntimeCWD)
	}
	if role.RuntimeDaytona["repo_url"] != "https://github.com/acme/frontend.git" ||
		role.RuntimeDaytona["branch"] != "main" {
		t.Fatalf("role Daytona metadata = %+v, want task worker repo materialization config", role.RuntimeDaytona)
	}
}

func TestLoadDaytonaWorkflowRouteStyleNamedRun(t *testing.T) {
	root := t.TempDir()
	if err := InitTypeScriptProject(root); err != nil {
		t.Fatalf("InitTypeScriptProject() error = %v", err)
	}
	writeDefFile(t, root, ".loom/workflows/daytona-code.ts", `import {
  Type,
  createAgent,
  defineTool,
  type FlueContext,
  type WorkflowRouteHandler,
} from '@flue/runtime';
import { Daytona } from '@daytona/sdk';
import { daytona } from '../connectors/daytona';

export const route: WorkflowRouteHandler = async (_c, next) => next();

export const cloneRepo = defineTool({
  name: 'clone_repo',
  description: 'Clone a repository into the Daytona sandbox',
  parameters: {
    repo: Type.string(),
  },
});

export async function run({ init, payload, env }: FlueContext) {
  const client = new Daytona({ apiKey: env.DAYTONA_API_KEY });
  const sandbox = await client.create({
    id: 'daytona-example',
    snapshot: 'loom-node-dev',
    cwd: '/workspace/project',
  });
  const setupAgent = createAgent(() => ({
    sandbox: daytona(sandbox),
    model: 'openai/gpt-5.5',
  }));
  const setup = await (await init(setupAgent, { name: 'setup' })).session();
  await setup.shell(`+"`git clone ${payload.repo} /workspace/project`"+`);
  const projectAgent = createAgent(() => ({
    sandbox: daytona(sandbox, { cwd: '/workspace/project', name: 'project' }),
    model: 'openai/gpt-5.5',
  }));
  const session = await (await init(projectAgent, { name: 'project' })).session();
  return await session.prompt(payload.prompt);
}
`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := Summary(plan); got != "agents=0 workflows=1 runtimes=0" {
		t.Fatalf("Summary() = %q, want implicit named-run workflow", got)
	}
	workflow, ok := FindWorkflow(plan, "daytona-code")
	if !ok {
		t.Fatalf("FindWorkflow() did not find implicit Daytona workflow")
	}
	if workflow.Runner != "workflow-context-v1" || workflow.Builtin != "" {
		t.Fatalf("workflow = %+v, want code-defined workflow context runner", workflow)
	}
}

func TestLoadFlueCompatibilitySourceRootDaytonaWorkflow(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".flue/connectors/daytona.ts", `import { daytona as loomDaytona } from '@loom/runtime';

export function daytona(sandbox, options = {}) {
  return loomDaytona(sandbox, options);
}
`)
	writeDefFile(t, root, ".flue/workflows/code.ts", `import {
  createAgent,
  type FlueContext,
  type WorkflowRouteHandler,
} from '@flue/runtime';
import { Daytona } from '@daytona/sdk';
import { daytona } from '../connectors/daytona';

export const route: WorkflowRouteHandler = async (_c, next) => next();

export async function run({ init, payload, env }: FlueContext) {
  const client = new Daytona({ apiKey: env.DAYTONA_API_KEY });
  const sandbox = await client.create();
  const setupAgent = createAgent(() => ({
    sandbox: daytona(sandbox),
    model: 'openai/gpt-5.5',
  }));
  const setup = await (await init(setupAgent, { name: 'setup' })).session();
  await setup.shell(`+"`git clone ${payload.repo} /workspace/project`"+`);
  const projectAgent = createAgent(() => ({
    sandbox: daytona(sandbox, { cwd: '/workspace/project' }),
    model: 'openai/gpt-5.5',
  }));
  const session = await (await init(projectAgent, { name: 'project' })).session();
  return await session.prompt(payload.prompt);
}
`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := Summary(plan); got != "agents=0 workflows=1 runtimes=0" {
		t.Fatalf("Summary() = %q, want .flue implicit named-run workflow", got)
	}
	workflow, ok := FindWorkflow(plan, "code")
	if !ok {
		t.Fatalf("FindWorkflow() did not find .flue compatibility workflow")
	}
	if workflow.Runner != "workflow-context-v1" ||
		!strings.Contains(filepath.ToSlash(workflow.SourcePath), "/.flue/workflows/code.ts") {
		t.Fatalf("workflow = %+v, want .flue code-defined workflow context runner", workflow)
	}
	if !containsString(workflow.Env, "DAYTONA_API_KEY") {
		t.Fatalf("workflow env = %+v, want implicit Daytona API key grant for copied @daytona/sdk workflow", workflow.Env)
	}
}

func TestLoadRejectsUnauthenticatedWorkflowRoute(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".loom/workflows/unsafe.ts", `export default defineWorkflow({
  name: "unsafe-route",
  builtin: "run-parent-work-items",
  path: "/workflows/unsafe/run",
});`)

	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "must declare an auth policy") {
		t.Fatalf("Load() error = %v, want route auth policy validation", err)
	}
}

func TestLoadRejectsMissingTypedAgentToolDefinition(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".loom/agents/slack-agent.ts", `import { createAgent } from '@loom/runtime';

export default createAgent({
  name: 'slack-agent',
  backend: 'echo',
  model: 'local/echo',
  tools: ['create_channel'],
});`)

	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), `agent model tool "create_channel" must reference a declared typed tool definition`) {
		t.Fatalf("Load() error = %v, want missing typed agent tool validation", err)
	}
}

func TestLoadRejectsMissingTypedWorkflowToolDefinition(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".loom/workflows/provision-channel.ts", `import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'provision-channel',
  builtin: 'run-parent-work-items',
  tools: ['create_channel'],
});`)

	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), `workflow tool "create_channel" must reference a declared typed tool definition`) {
		t.Fatalf("Load() error = %v, want missing typed workflow tool validation", err)
	}
}

func TestLoadSupportsTypeScriptSyntax(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".loom/workflows/typed-epic.ts", `import { defineWorkflow, trigger } from '@loom/runtime';

type Input = { parentId: string };
type ToolName = string;

const workflowTools = ['workItems.readyChildren', 'taskRuns.ensure', 'shell.run'] satisfies ToolName[];
const route = {
  path: '/workflows/typed-epic/run',
  auth: 'workspace',
} as const;

export default defineWorkflow({
  name: 'typed-epic',
  builtin: 'run-parent-work-items',
  singleton: (input: Input): string => `+"`"+`parent:${input.parentId}`+"`"+`,
  expose: { http: route },
  triggers: [
    trigger.issueLabelAdded({ label: 'typed-epic', type: 'epic' } as const),
  ],
  tools: workflowTools,
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(plan.Workflows) != 1 {
		t.Fatalf("workflows = %+v, want one typed workflow", plan.Workflows)
	}
	workflow := plan.Workflows[0]
	if workflow.Name != "typed-epic" || workflow.RoutePath != "/workflows/typed-epic/run" {
		t.Fatalf("workflow = %+v, want typed workflow route", workflow)
	}
	if len(workflow.Tools) != 3 || workflow.Tools[1] != "taskRuns.ensure" || workflow.Tools[2] != "shell.run" {
		t.Fatalf("tools = %+v, want typed workflow tools", workflow.Tools)
	}
}

func jsonMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode JSON map: %v\n%s", err, raw)
	}
	return out
}

func fmtStringSlice(value any) string {
	return strings.Trim(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(fmtAny(value)), "\n", " "), "  ", " "), "[]")
}

func fmtAny(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}
