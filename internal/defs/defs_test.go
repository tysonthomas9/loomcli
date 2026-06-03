package defs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSlackCloneDefinitions(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".loom/agents/task.ts", `export default defineAgent({
  name: "task",
  description: "Implements Slack clone UI tasks.",
  backend: "codex",
  model: "gpt-5",
  instructions: `+"`"+`Work in the Slack clone repo and keep changes scoped to the assigned task.`+"`"+`,
  skills: ["agent-browser"],
  tools: [github.issue.read, github.issue.write],
  allowedCommands: ["npm test", "npm run build"],
  deniedCommands: ["git reset --hard"],
  repos: ["slack-src"],
  env: ["NODE_ENV=test"],
  maxConcurrency: 2,
  maxBudgetUSD: 1.25,
});`)
	writeDefFile(t, root, ".loom/runtimes/local.ts", `export default runtime.local({
  name: "local",
  image: "node:22",
  repos: ["slack-src"],
  env: ["NODE_ENV=test"],
  cpu: "2",
  memory: "2Gi",
});`)
	writeDefFile(t, root, ".loom/workflows/slack-clone-epic.ts", `export default defineWorkflow({
  name: "slack-clone-epic-runner",
  description: "Runs Slack clone child tasks from a parent epic.",
  builtin: "run-parent-work-items",
  singleton: (input) => `+"`"+`parent:${input.parentId}`+"`"+`,
  path: "/workflows/slack-clone-epic-runner/run",
  auth: "workspace",
  issueLabelAdded: { label: "slack-clone", type: "epic" },
  tools: [github.issue.read, github.issue.write],
  repos: ["slack-src"],
  env: ["NODE_ENV=test"],
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := Summary(plan); got != "agents=1 workflows=1 runtimes=1" {
		t.Fatalf("Summary() = %q, want one of each definition kind", got)
	}
	if plan.Agents[0].Name != "task" || plan.Agents[0].MaxConcurrency != 2 {
		t.Fatalf("agent = %+v, want task with max concurrency", plan.Agents[0])
	}
	if plan.Workflows[0].Name != "slack-clone-epic-runner" || plan.Workflows[0].Builtin != "run-parent-work-items" {
		t.Fatalf("workflow = %+v, want code-defined builtin delegation", plan.Workflows[0])
	}
	if plan.Workflows[0].RoutePath == "" || plan.Workflows[0].TriggerEvent != "issue.label_added" {
		t.Fatalf("workflow bindings = %+v, want route and label trigger", plan.Workflows[0])
	}
	if plan.Runtimes[0].Name != "local" || plan.Runtimes[0].Image != "node:22" {
		t.Fatalf("runtime = %+v, want local node runtime", plan.Runtimes[0])
	}
	if got := routeBindingID(plan.Workflows[0].Name, "POST", plan.Workflows[0].RoutePath); got != "workflow:slack-clone-epic-runner:POST:workflows.slack-clone-epic-runner.run" {
		t.Fatalf("routeBindingID() = %q, want path-safe route binding id", got)
	}
}

func TestLoadRejectsUnknownModelProvider(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".loom/agents/bad-model.ts", `export default defineAgent({
  name: "bad-model",
  backend: "codex",
  model: "unknown-provider/example-model",
});`)

	_, err := Load(root)
	if err == nil {
		t.Fatalf("Load() succeeded, want unknown model provider error")
	}
	if !strings.Contains(err.Error(), `unknown model provider "unknown-provider"`) ||
		!strings.Contains(err.Error(), "loom.config.ts models.allowedProviders") {
		t.Fatalf("Load() error = %v, want model provider validation guidance", err)
	}
}

func TestLoadAllowsConfiguredPrivateModelProviderAndAlias(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, "loom.config.ts", `export default defineConfig({
  sourceRoot: ".loom",
  models: {
    allowedProviders: ["acme-ai"],
    allowed: ["enterprise-default"],
  },
});`)
	writeDefFile(t, root, ".loom/agents/private-gateway.ts", `export default defineAgent({
  name: "private-gateway",
  backend: "codex",
  model: "acme-ai/team-model",
});`)
	writeDefFile(t, root, ".loom/agents/private-alias.ts", `export default defineAgent({
  name: "private-alias",
  backend: "codex",
  model: "enterprise-default",
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(plan.Agents) != 2 {
		t.Fatalf("agents = %+v, want private provider and alias agents", plan.Agents)
	}
	if plan.ModelPolicy == nil ||
		!containsString(plan.ModelPolicy.AllowedProviders, "acme-ai") ||
		!containsString(plan.ModelPolicy.AllowedModels, "enterprise-default") {
		t.Fatalf("model policy = %+v, want configured private provider and alias", plan.ModelPolicy)
	}
}

func TestWorkspaceExportPlanAllowsExistingPrivateModelProvider(t *testing.T) {
	plan := &Plan{
		Root: "workspace:PRIVATE",
		Agents: []AgentModule{{
			Name:       "private-agent",
			SourcePath: "control-plane:role/private-agent",
			Model:      "private-provider/team-model",
		}},
	}
	if err := validatePlan(plan); err != nil {
		t.Fatalf("validatePlan() error = %v, want control-plane export to preserve private provider", err)
	}
}

func TestLoadRejectsRootLayoutEntrypoints(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, "agents/root-agent.ts", `export default defineAgent({
  name: "root-agent",
  backend: "codex",
  model: "gpt-5",
});`)

	_, err := Load(root)
	if err == nil {
		t.Fatalf("Load() succeeded, want root source layout rejection")
	}
	if !strings.Contains(err.Error(), "mixed Loom TypeScript source roots") ||
		!strings.Contains(err.Error(), "project root entrypoints") ||
		!strings.Contains(err.Error(), "agents/root-agent.ts") {
		t.Fatalf("Load() error = %v, want mixed source root guidance", err)
	}
}

func TestLoadRejectsMixedLoomAndRootEntrypoints(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".loom/agents/loom-agent.ts", `export default defineAgent({
  name: "loom-agent",
  backend: "codex",
  model: "gpt-5",
});`)
	writeDefFile(t, root, "workflows/root-workflow.ts", `export default defineWorkflow({
  name: "root-workflow",
  builtin: "run-parent-work-items",
});`)

	_, err := Load(root)
	if err == nil {
		t.Fatalf("Load() succeeded, want mixed source root rejection")
	}
	if !strings.Contains(err.Error(), "mixed Loom TypeScript source roots") ||
		!strings.Contains(err.Error(), "selected .loom") ||
		!strings.Contains(err.Error(), "workflows/root-workflow.ts") {
		t.Fatalf("Load() error = %v, want mixed source root guidance", err)
	}
}

func TestLoadRejectsMixedLoomAndFlueEntrypoints(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".loom/agents/loom-agent.ts", `export default defineAgent({
  name: "loom-agent",
  backend: "codex",
  model: "gpt-5",
});`)
	writeDefFile(t, root, ".flue/workflows/flue-workflow.ts", `export default defineWorkflow({
  name: "flue-workflow",
  builtin: "run-parent-work-items",
});`)

	_, err := Load(root)
	if err == nil {
		t.Fatalf("Load() succeeded, want mixed source root rejection")
	}
	if !strings.Contains(err.Error(), "mixed Loom TypeScript source roots") ||
		!strings.Contains(err.Error(), "selected .loom") ||
		!strings.Contains(err.Error(), ".flue entrypoints") ||
		!strings.Contains(err.Error(), ".flue/workflows/flue-workflow.ts") {
		t.Fatalf("Load() error = %v, want mixed .loom/.flue source root guidance", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeDefFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
