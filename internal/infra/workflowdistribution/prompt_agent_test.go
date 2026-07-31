package workflowdistribution

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
// filterless claim-ready for unconstrained pickup, and dispatches the bundled
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
		// Every named role is assigned one lifecycle owner before it can claim:
		// old UI empty/any records become coder roles; unknown filters fail closed.
		"resolveRolePhase(resolved)",
		`errorClass: "prompt_agent_unsupported_task_filter"`,
		`return { supported: true, taskFilter: "has_design", rawTaskFilter };`,
		`rawTaskFilter === "review"`,
		`if (rawTaskFilter === "bug") {`,
		`errorClass: "prompt_agent_bug_filter_requires_read_only"`,
		`errorClass: "prompt_agent_review_filter_requires_mutating_role"`,
		`errorClass: "prompt_agent_bug_filter_card_read_failed"`,
		"issueTypeAllowsBug",
		// GAP B: targeted claim-by-id, plus filterless pickup retained.
		"loom.tasks.claim({ taskId: targetId, actor })",
		"loom.tasks.claimReady({ actor, limit: 1 })",
		"loom.tasks.claimReview({ taskId: targetId })",
		"loom.tasks.releaseReview({ taskId: issueId })",
		`errorClass: "prompt_agent_repository_block_not_applied"`,
		`errorClass: "prompt_agent_review_external_ref_unsupported"`,
		"parseLocalBranchExternalRef",
		// A conflict from claim-by-id means the target is not claimable.
		"isConflictError(e)",
		// GAP A: dispatches the builtin runner by name.
		`runner: "local-task-runner"`,
		`taskPrompt: prompt`,
		"requestInput.requireLocalBranchDelivery = true",
		"requestInput.localBranchName = reviewBranchResume.branch",
		"requestInput.localBranchBaseRef = reviewBranchResume.headSha",
		// A lost TaskRun response is not proof that enqueue failed; only a
		// certified pre-commit rejection may unclaim the Work Item. A 409 is a
		// real conflict because exact durable replay resolves before this layer.
		"if (!isAmbiguousTaskRunRequestError(e)) {",
		`await releaseClaimAfterError(loom, issueId, "request the TaskRun", e, isReview)`,
		// Event payloads are an early spend gate, not authoritative after claim;
		// every role rechecks the current card before dispatch.
		"if (isGatingFilter(taskFilter)) {",
		// Typed release is the sole phase-mismatch/pre-commit cleanup command.
		"await loom.tasks.release({ taskId: issueId })",
		// A planner terminal receipt retires the typed generation and lock, but
		// the host must also clear the former DriverRun assignee during handoff.
		`loom.issues.update({ issueId, status: "review", assignee: "" })`,
		`errorClass: "prompt_agent_planner_handoff_failed"`,
		// Failed/cancelled TaskRuns must not leave the terminal card looking
		// claimed by a DriverRun whose typed generation was retired.
		`loom.issues.update({ issueId, status: "open", assignee: "" })`,
		`errorClass: "prompt_agent_terminal_handoff_failed"`,
		`errorClass: "prompt_agent_review_handoff_failed"`,
		`errorClass: "prompt_agent_review_delivery_invalid"`,
		"handoff.externalRef = externalRef",
		`outcome: "review-role-review"`,
		// A needs-revision card belongs exclusively to the planner even when it
		// still carries an older design.
		`if (taskFilter === "has_design") return hasDesign === true && !hasRevision;`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("prompt-agent source missing %q", want)
		}
	}
	bugRead := strings.Index(source, "currentCard = (await loom.issues.get({ issueId: targetId }))")
	targetClaim := strings.Index(source, ": await claimTargetTask(loom, actor, targetId)")
	if bugRead < 0 || targetClaim < 0 || bugRead >= targetClaim {
		t.Fatal("bug-filtered prompt-agent must read and verify the current card before claiming")
	}
	unclaimStart := strings.Index(source, "async function unclaimTask")
	if unclaimStart < 0 {
		t.Fatal("prompt-agent unclaimTask helper missing")
	}
	unclaimEnd := strings.Index(source[unclaimStart:], "\n}\n")
	if unclaimEnd < 0 {
		t.Fatal("prompt-agent unclaimTask helper is unterminated")
	}
	unclaimSource := source[unclaimStart : unclaimStart+unclaimEnd]
	if strings.Contains(unclaimSource, `loom.issues.update({ issueId, status: "open" })`) {
		t.Fatal("prompt-agent must use the typed Work Item release instead of a generic lifecycle update")
	}
	if strings.Contains(unclaimSource, "catch (_releaseErr)") {
		t.Fatal("prompt-agent must not report a successful handback after typed release fails")
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
