package defs

import (
	"os"
	"path/filepath"
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
