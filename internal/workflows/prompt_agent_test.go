package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func promptAgentSource(t *testing.T) string {
	t.Helper()
	spec, ok := BuiltinWorkflow(BuiltinPromptAgentWorkflowName)
	if !ok {
		t.Fatal("built-in prompt-agent workflow missing")
	}
	source := spec.Files[spec.Entrypoint]
	if source == "" {
		t.Fatal("built-in prompt-agent source missing")
	}
	return source
}

// The prompt-agent orchestrator, post-GAP: it resolves the role prompt CONFIG BY
// REFERENCE — input.roleName else the binding's configured roleName read from the
// calling run's provenance (loom.binding.config) — then through the driver SDK
// roles surface (GAP C), claims the exact target task by id (GAP B) while keeping
// filterless claim-ready for untargeted pickup, and dispatches the bundled
// local-task-runner (which a custom registration reaches via workspace-global
// runner resolution, GAP A). These anchors lock that authoring contract; the
// live full-circle exercises the runtime path.
func TestPromptAgentWorkflowSourceContract(t *testing.T) {
	source := promptAgentSource(t)
	for _, want := range []string{
		"import { createLoomDriverClient } from '@loom/sdk/driver';",
		// GAP C: role prompt resolved from a Role record via the SDK.
		"loom.roles.get({ name: roleName })",
		"resolvePromptSource(loom, input)",
		// input.prompt still overrides a resolved role prompt (precedence).
		`return { prompt: stringValue(input.prompt), source: "input.prompt" };`,
		// Config by reference: roleName falls back to the binding's config when
		// the event payload names none (the internal task.ready / run-now lane).
		"loom.binding.config()",
		"bindingConfigRoleName(loom)",
		// GAP B: targeted claim-by-id, plus filterless pickup retained.
		"loom.tasks.claim({ taskId: targetId, actor })",
		"loom.tasks.claimReady({ actor, limit: 1 })",
		// A conflict from claim-by-id means the target is not claimable.
		"isConflictError(e)",
		// GAP A: dispatches the builtin runner by name.
		`runner: "local-task-runner"`,
		`taskPrompt: prompt`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("prompt-agent source missing %q", want)
		}
	}
}

// The stale spike gap notes (no role SDK surface, the claim-and-release
// emulation) must be gone now that the surfaces are first-class.
func TestPromptAgentDropsStaleGapNotes(t *testing.T) {
	source := promptAgentSource(t)
	for _, forbidden := range []string{
		"no role SDK surface",
		"no role-read surface",
		"cannot read a Role record",
		"emulates claim-by-id",
		"release every non-match",
		"releases every non-match",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("prompt-agent source still carries the stale gap note %q", forbidden)
		}
	}
}

func TestPromptAgentWorkflowSourceParsesAsJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not available: %v", err)
	}
	source := promptAgentSource(t)
	path := filepath.Join(t.TempDir(), "prompt-agent.mjs")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write workflow source: %v", err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil { //nolint:norawexec // syntax-check via the node binary located by the test itself
		t.Fatalf("node --check failed: %v\n%s", err, out)
	}
}
