package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestPromptAgentWorkflowBehavior executes the exact embedded prompt-agent
// source under Node. Only its two package imports are replaced: the driver SDK
// returns the recording mock installed by the harness, while the Flue runtime
// preserves the workflow definition without starting a real agent. This keeps
// the orchestration assertions behavioral instead of duplicating the TypeScript
// predicates in Go source-contract tests.
func TestPromptAgentWorkflowBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not available: %v", err)
	}

	tmp := t.TempDir()
	writePromptAgentBehaviorFixture(t, filepath.Join(tmp, "prompt-agent.mjs"), promptAgentSource(t))
	writePromptAgentBehaviorFixture(t, filepath.Join(tmp, "node_modules", "@loom", "sdk", "package.json"), `{
  "name": "@loom/sdk",
  "type": "module",
  "exports": { "./driver": "./driver.js" }
}
`)
	writePromptAgentBehaviorFixture(t, filepath.Join(tmp, "node_modules", "@loom", "sdk", "driver.js"), `
export function createLoomDriverClient() {
  if (!globalThis.__promptAgentMockLoom) {
    throw new Error("prompt-agent behavior harness did not install a Loom client");
  }
  return globalThis.__promptAgentMockLoom;
}
`)
	writePromptAgentBehaviorFixture(t, filepath.Join(tmp, "node_modules", "@flue", "runtime", "package.json"), `{
  "name": "@flue/runtime",
  "type": "module",
  "exports": { ".": "./index.js" }
}
`)
	writePromptAgentBehaviorFixture(t, filepath.Join(tmp, "node_modules", "@flue", "runtime", "index.js"), `
export function defineAgent(factory) { return factory; }
export function defineWorkflow(definition) { return definition; }
`)

	harness, err := filepath.Abs(filepath.Join("testdata", "prompt_agent_behavior.mjs"))
	if err != nil {
		t.Fatalf("resolve prompt-agent behavior harness: %v", err)
	}
	cmd := exec.Command(node, harness, filepath.Join(tmp, "prompt-agent.mjs")) //nolint:norawexec // executes the checked-in deterministic Node harness
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prompt-agent behavior harness failed: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}

func writePromptAgentBehaviorFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}
