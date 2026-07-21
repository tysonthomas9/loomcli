package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinSessionEvalAgentDeclaresEvalTaskRunner(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinSessionEvalAgentWorkflowName)
	if !ok {
		t.Fatal("session-eval-agent builtin missing")
	}
	runners := workflowRunnerSpecs(BuildAndRegisterOptions{
		Entrypoint:    spec.Entrypoint,
		Files:         spec.Files,
		DeriveRunners: true,
	})
	if len(runners) != 1 {
		t.Fatalf("runner specs = %+v, want one eval runner", runners)
	}
	if runners[0].Name != BuiltinSessionEvalTaskRunnerName || runners[0].Kind != "flue-workflow" || runners[0].Entrypoint != BuiltinSessionEvalTaskRunnerName {
		t.Fatalf("runner spec = %+v, want session-eval-task-runner flue workflow", runners[0])
	}
}

func TestSessionEvalAgentSourceContract(t *testing.T) {
	source := sessionEvalAgentSource(t)
	for _, want := range []string{
		`const PROMPT_VERSION = "v1";`,
		`const DEFAULT_JUDGE_MODEL = "gpt-5.6-sol";`,
		`process.env.LOOM_EVAL_BACKEND || "codex"`,
		`process.env.LOOM_EVAL_MODEL || DEFAULT_JUDGE_MODEL`,
		`errorClass: "eval_backend_unsupported"`,
		`deterministicTaskRunId(loom.driverRunId, "preflight")`,
		`kind: "session_eval_preflight"`,
		`loom.evals.listUnevaluated({ promptVersion: PROMPT_VERSION })`,
		`loom.evals.getTranscript({ sessionId, promptVersion: PROMPT_VERSION })`,
		`renderJudgeInput(candidate, entries)`,
		`deterministicTaskRunId(loom.driverRunId, "judge-" + sessionId)`,
		`kind: "session_eval_judge"`,
		`runtimeMetadata(run)`,
		`loom.evals.putMetric({`,
		`status: "done"`,
		`status: "failed"`,
		`errorClass = judge.errorClass === "transcript_too_large" ? "transcript_too_large" : "judge_error"`,
		`=== TRANSCRIPT (`,
		`verbatim, no truncation`,
		`=== DIFF ===\n(diff stats are in the session record header; full patch content is not included in v1)`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("session-eval-agent source missing %q", want)
		}
	}
}

func TestSessionEvalTaskRunnerSourceContract(t *testing.T) {
	source := sessionEvalTaskRunnerSource(t)
	for _, want := range []string{
		`const CODEX = process.env.LOOM_CODEX_BIN || "codex";`,
		`kind === "session_eval_preflight"`,
		`execFileSync(CODEX, ["--version"]`,
		`codex_available: String(available)`,
		`kind !== "session_eval_judge"`,
		`failed("eval_backend_unsupported"`,
		`"--sandbox", "read-only"`,
		`"--model", model`,
		`"--output-schema", schemaPath`,
		`"--output-last-message", outPath`,
		`timeoutMs: 10 * 60 * 1000`,
		`contextOverflow(message) ? "transcript_too_large" : "judge_error"`,
		`eval_result: JSON.stringify(result)`,
		`judge_model: model`,
		`eval_cost: JSON.stringify(evalCost)`,
		`function outputSchema()`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("session-eval-task-runner source missing %q", want)
		}
	}
}

func TestSessionEvalPromptVersionAndRubricAdaptations(t *testing.T) {
	agent := sessionEvalAgentSource(t)
	if !strings.Contains(agent, `const PROMPT_VERSION = "v1";`) {
		t.Fatal(`session-eval-agent source missing PROMPT_VERSION "v1"`)
	}
	runner := sessionEvalTaskRunnerSource(t)
	for _, want := range []string{
		"exit_code -1 plus a transcript that ends mid-action with no terminal marker is likely a platform kill (watchdog, shutdown, or backend outage",
		"Tag \\`killed_or_truncated\\`, and score only what the visible transcript supports; do not penalize any dimension for work the truncation hides.",
		"the session's diff statistics (files changed, lines added/removed)",
		"full patch content is not included",
	} {
		if !strings.Contains(runner, want) {
			t.Fatalf("session-eval-task-runner rubric missing %q", want)
		}
	}
}

func TestSessionEvalAgentSourceParsesAsJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not available: %v", err)
	}
	source := sessionEvalAgentSource(t)
	path := filepath.Join(t.TempDir(), "session-eval-agent.mjs")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write workflow source: %v", err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil { //nolint:norawexec // syntax-check via the node binary located by the test itself
		t.Fatalf("node --check failed: %v\n%s", err, out)
	}
}

func TestSessionEvalTaskRunnerSourceParsesAsJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not available: %v", err)
	}
	source := sessionEvalTaskRunnerSource(t)
	path := filepath.Join(t.TempDir(), "session-eval-task-runner.mjs")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write task runner source: %v", err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil { //nolint:norawexec // syntax-check via the node binary located by the test itself
		t.Fatalf("node --check failed: %v\n%s", err, out)
	}
}

func sessionEvalAgentSource(t *testing.T) string {
	t.Helper()
	spec, ok := BuiltinWorkflow(BuiltinSessionEvalAgentWorkflowName)
	if !ok {
		t.Fatal("built-in session-eval-agent workflow missing")
	}
	return spec.Files[spec.Entrypoint]
}

func sessionEvalTaskRunnerSource(t *testing.T) string {
	t.Helper()
	spec, ok := BuiltinWorkflow(BuiltinSessionEvalAgentWorkflowName)
	if !ok {
		t.Fatal("built-in session-eval-agent workflow missing")
	}
	source := spec.Files["workflows/"+BuiltinSessionEvalTaskRunnerName+".ts"]
	if source == "" {
		t.Fatal("built-in session-eval-task-runner source missing")
	}
	return source
}
