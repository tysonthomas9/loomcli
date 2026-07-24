package workflows

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

// Every built-in is registered and discoverable through the generalized
// registry: BuiltinWorkflowNames returns them sorted, and each resolves to a
// single-entrypoint spec at workflows/{name}.ts (with its bundled sibling
// runners). BuiltinWorkflow returns a defensive copy of the files map (mutating
// it must not corrupt the registry).
func TestBuiltinWorkflowRegistryListsAllBuiltins(t *testing.T) {
	names := BuiltinWorkflowNames()
	// Sorted; wantFiles is the entrypoint plus any bundled sibling task runners.
	wantFiles := map[string]int{
		BuiltinBugFixAgentWorkflowName:       3, // + local- + daytona-task-runner
		BuiltinEpicRunnerWorkflowName:        4,
		BuiltinGitHubReviewAgentWorkflowName: 2,
		BuiltinLocalReviewAgentWorkflowName:  2, // + github-review-task-runner
		BuiltinPromptAgentWorkflowName:       2, // + local-task-runner
		BuiltinReviewLoopAgentWorkflowName:   2, // + github-review-task-runner
	}
	want := []string{
		BuiltinBugFixAgentWorkflowName,
		BuiltinEpicRunnerWorkflowName,
		BuiltinGitHubReviewAgentWorkflowName,
		BuiltinLocalReviewAgentWorkflowName,
		BuiltinPromptAgentWorkflowName,
		BuiltinReviewLoopAgentWorkflowName,
	}
	if len(names) != len(want) {
		t.Fatalf("BuiltinWorkflowNames() = %v, want %v", names, want)
	}
	for i, name := range want {
		if names[i] != name {
			t.Fatalf("BuiltinWorkflowNames()[%d] = %q, want %q (sorted)", i, names[i], name)
		}
	}
	for _, name := range want {
		spec, ok := BuiltinWorkflow(name)
		if !ok {
			t.Fatalf("BuiltinWorkflow(%q) missing", name)
		}
		entrypoint := "workflows/" + name + ".ts"
		if spec.Entrypoint != entrypoint {
			t.Fatalf("%s entrypoint = %q, want %q", name, spec.Entrypoint, entrypoint)
		}
		if _, ok := spec.Files[entrypoint]; !ok {
			t.Fatalf("%s spec missing entrypoint file %q", name, entrypoint)
		}
		if len(spec.Files) != wantFiles[name] {
			t.Fatalf("%s spec has %d files, want %d", name, len(spec.Files), wantFiles[name])
		}
	}
}

func TestBuiltinEpicRunnerDeclaresSiblingTaskRunners(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("epic-runner builtin missing")
	}
	runners := workflowRunnerSpecs(BuildAndRegisterOptions{
		Entrypoint:    spec.Entrypoint,
		Files:         spec.Files,
		DeriveRunners: true,
	})
	names := make([]string, 0, len(runners))
	for _, runner := range runners {
		if runner.Kind != "flue-workflow" || runner.Entrypoint == "" {
			t.Fatalf("runner spec = %+v, want flue-workflow with entrypoint", runner)
		}
		names = append(names, runner.Name)
	}
	// openshell-task-runner is deny-listed (§4.6): its source still ships in the
	// bundle but it is never registered as a selectable runner.
	want := strings.Join([]string{"daytona-task-runner", "local-task-runner"}, ",")
	if strings.Join(names, ",") != want {
		t.Fatalf("runner names = %v, want %s", names, want)
	}
	for _, name := range names {
		if name == "openshell-task-runner" {
			t.Fatalf("openshell-task-runner must be deny-listed from derived runners, got %v", names)
		}
	}
}

func TestBuiltinGitHubReviewAgentDeclaresReviewTaskRunner(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinGitHubReviewAgentWorkflowName)
	if !ok {
		t.Fatal("github-review-agent builtin missing")
	}
	runners := workflowRunnerSpecs(BuildAndRegisterOptions{
		Entrypoint:    spec.Entrypoint,
		Files:         spec.Files,
		DeriveRunners: true,
	})
	if len(runners) != 1 {
		t.Fatalf("runner specs = %+v, want one review runner", runners)
	}
	if runners[0].Name != BuiltinGitHubReviewTaskRunnerName || runners[0].Kind != "flue-workflow" || runners[0].Entrypoint != BuiltinGitHubReviewTaskRunnerName {
		t.Fatalf("runner spec = %+v, want github-review-task-runner flue workflow", runners[0])
	}
}

// BuiltinWorkflow hands back a copy of the files map: mutating the returned
// spec must not leak back into the registry (the source-tree builtins are
// immutable across EnsureBuiltinWorkflow calls).
func TestBuiltinWorkflowReturnsDefensiveCopy(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinGitHubReviewAgentWorkflowName)
	if !ok {
		t.Fatal("github-review-agent builtin missing")
	}
	for key := range spec.Files {
		spec.Files[key] = "tampered"
	}
	fresh, _ := BuiltinWorkflow(BuiltinGitHubReviewAgentWorkflowName)
	if fresh.Files[fresh.Entrypoint] == "tampered" {
		t.Fatal("BuiltinWorkflow returned a shared files map; registry source was mutated")
	}
}

// SourceDigest is deterministic for the github-review-agent spec, so
// EnsureBuiltinWorkflow re-derives the same builtin://.../versions/{digest}
// source ref on every (idempotent) registration attempt.
func TestGitHubReviewAgentSourceDigestIsDeterministic(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinGitHubReviewAgentWorkflowName)
	if !ok {
		t.Fatal("github-review-agent builtin missing")
	}
	first := SourceDigest(spec.Files)
	second := SourceDigest(spec.Files)
	if first != second {
		t.Fatalf("SourceDigest not stable: %q != %q", first, second)
	}
	if !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("SourceDigest = %q, want sha256: prefix", first)
	}
}

// github-review-agent is wired into the registry the EnsureBuiltinWorkflow
// trusted path consumes: BuiltinWorkflow resolves it (so EnsureBuiltinWorkflow
// reaches the BuildAndRegister call that stamps domain.DriverTrustTrusted
// rather than returning ErrNotFound), and submissionTrust preserves trusted on
// the builtin path while defaulting the external path to UNTRUSTED.
func TestGitHubReviewAgentRegistersThroughTrustedBuiltinPath(t *testing.T) {
	if _, ok := BuiltinWorkflow(BuiltinGitHubReviewAgentWorkflowName); !ok {
		t.Fatal("EnsureBuiltinWorkflow would return ErrNotFound: github-review-agent not in the builtin registry")
	}
	if got := submissionTrust(domain.DriverTrustTrusted); got != domain.DriverTrustTrusted {
		t.Fatalf("submissionTrust(trusted) = %q, want trusted (builtin path)", got)
	}
	if got := submissionTrust(""); got != domain.DriverTrustUntrusted {
		t.Fatalf("submissionTrust(\"\") = %q, want untrusted (external submissions fail closed)", got)
	}
}

func TestWorkflowRunnerSpecsDeriveSiblingWorkflowFiles(t *testing.T) {
	runners := workflowRunnerSpecs(BuildAndRegisterOptions{
		Entrypoint:    "workflows/epic-runner.ts",
		DeriveRunners: true,
		Files: map[string]string{
			"workflows/epic-runner.ts":         "export async function run() {}",
			"workflows/local-task-runner.ts":   "export async function run() {}",
			"workflows/daytona-task-runner.ts": "export async function run() {}",
			"helpers/shared.ts":                "export const x = 1",
		},
	})
	if len(runners) != 2 {
		t.Fatalf("runner specs = %+v, want two sibling workflow runners", runners)
	}
	if runners[0].Name != "daytona-task-runner" || runners[0].Entrypoint != "daytona-task-runner" {
		t.Fatalf("first runner = %+v, want daytona-task-runner", runners[0])
	}
	if runners[1].Name != "local-task-runner" || runners[1].Entrypoint != "local-task-runner" {
		t.Fatalf("second runner = %+v, want local-task-runner", runners[1])
	}
}

func TestWorkflowRunnerSpecsDoNotInferCustomSiblingRunnerFiles(t *testing.T) {
	runners := workflowRunnerSpecs(BuildAndRegisterOptions{
		Entrypoint: "workflows/custom.ts",
		Files: map[string]string{
			"workflows/custom.ts":              "export async function run() {}",
			"workflows/local-task-runner.ts":   "export async function run() {}",
			"workflows/daytona-task-runner.ts": "export async function run() {}",
		},
	})
	if len(runners) != 0 {
		t.Fatalf("runner specs = %+v, want no inferred custom runners without explicit manifest", runners)
	}
}

// The github-review-agent is a single linear pass: it parses the trigger
// subject, gates on draft + liveness, fetches the diff, runs a review TaskRun,
// validates the findings, and posts a COMMENT review with the expectedHeadSha
// precondition. These string anchors lock the authoring contract (the live
// e2e exercises the runtime path).
func TestGitHubReviewAgentWorkflowSourceContract(t *testing.T) {
	source := githubReviewAgentSource(t)
	tests := []struct {
		name string
		want string
	}{
		{name: "camelCase sdk import", want: "import { createLoomDriverClient } from '@loom/sdk/driver';"},
		{name: "parses trigger subject", want: "parseReviewSubject(input)"},
		{name: "subject key is repo#pr", want: `subjectRef: repo + "#" + prNumber,`},
		{name: "draft skip encoded in output", want: `skipped(loom, "draft"`},
		{name: "preflight liveness read", want: "loom.connectors.github.readPullRequest({"},
		{name: "preflight pins expected head sha", want: "expectedHeadSha: subject.headSha,"},
		{name: "stale subject skip", want: `skipped(loom, "stale_subject"`},
		{name: "diff via compare connector", want: "loom.connectors.github.compare({"},
		{name: "review task enqueued", want: "loom.taskRuns.request(request)"},
		{name: "default review runner", want: `"github-review-task-runner"`},
		{name: "review task awaited", want: "loom.taskRuns.await({ taskRunId })"},
		{name: "deterministic review run id", want: `deterministicTaskRunId(loom.driverRunId, "review")`},
		{name: "findings validated", want: "validateFindings(review.findings)"},
		{name: "invalid findings error class", want: `errorClass: "invalid_review_findings"`},
		{name: "posts review", want: "loom.connectors.github.postReview({"},
		{name: "comment-only event", want: `event: "COMMENT",`},
		{name: "line is locator only", want: "Include line only when it is useful as a locator hint"},
		{name: "uncertified anchors cannot lose review", want: "does not risk the whole review on an uncertified inline anchor"},
		{name: "provider owns finding placement", want: `return summary + "\n\n" + count + " review finding(s) follow.";`},
		{name: "completed with review url", want: "reviewUrl:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(source, tt.want) {
				t.Fatalf("github-review-agent source missing %q", tt.want)
			}
		})
	}
}

// COMMENT-only is a locked decision: the workflow never APPROVEs or
// REQUEST_CHANGESes (no merge-gating authority in v1). The only review event
// literal is "COMMENT".
func TestGitHubReviewAgentIsCommentOnly(t *testing.T) {
	source := githubReviewAgentSource(t)
	for _, forbidden := range []string{`"APPROVE"`, `"REQUEST_CHANGES"`} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("github-review-agent source posts a non-COMMENT review event %q (v1 is COMMENT-only)", forbidden)
		}
	}
	if got := strings.Count(source, `event: "COMMENT"`); got != 1 {
		t.Fatalf(`event: "COMMENT" occurrences = %d, want exactly 1 (single review post)`, got)
	}
}

// The post is gated by a re-checked liveness read and a deterministic
// idempotency call: the connector callSeq auto-increments per action in call
// order, so the single postReview call re-derives the same idempotency key
// (runId#post) on re-entry. The expectedHeadSha precondition gates pre-egress
// in both the SDK and the provider, so it must accompany the post.
func TestGitHubReviewAgentPostIsLivenessGatedAndIdempotent(t *testing.T) {
	source := githubReviewAgentSource(t)
	postIdx := strings.Index(source, "loom.connectors.github.postReview({")
	if postIdx < 0 {
		t.Fatal("github-review-agent source missing postReview call")
	}
	recheckIdx := strings.Index(source, "const recheck = await readLivePullRequest(loom, subject);")
	if recheckIdx < 0 || recheckIdx > postIdx {
		t.Fatal("postReview must be preceded by a re-checked liveness read (recheck before post)")
	}
	postCall := source[postIdx:]
	if !strings.Contains(postCall[:strings.Index(postCall, "});")+3], "expectedHeadSha: subject.headSha,") {
		t.Fatal("postReview call must carry expectedHeadSha: subject.headSha (pre-egress precondition gate)")
	}
	// Single enqueue + single post keep the deterministic callSeq/run-id
	// re-entry guarantee intact.
	if got := strings.Count(source, "loom.taskRuns.request("); got != 1 {
		t.Fatalf("taskRuns.request occurrences = %d, want exactly 1 (one review enqueue per run)", got)
	}
	if got := strings.Count(source, "loom.connectors.github.postReview("); got != 1 {
		t.Fatalf("postReview occurrences = %d, want exactly 1 (deterministic runId#post idempotency)", got)
	}
}

// The runner command is injected on the worker (LOOM_DRIVER_TASK_RUNNER_CMD,
// task_bridge.go); the workflow must never hardcode codex or any runner
// binary, and the diff must reach the runner through connector reads rather
// than a git clone inside the sandbox.
func TestGitHubReviewAgentDoesNotHardcodeRunnerOrClone(t *testing.T) {
	source := githubReviewAgentSource(t)
	for _, forbidden := range []string{"codex", "git clone", "git_clone", "LOOM_DRIVER_TASK_RUNNER_CMD"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("github-review-agent source references %q; the runner command is injected and the diff comes from connector reads", forbidden)
		}
	}
}

// camelCase input contract: the workflow accepts the A1-1 camelCase
// convenience fields and the raw GitHub payload, but never the legacy
// snake_case driver-input field names epic-runner shed.
func TestGitHubReviewAgentUsesCamelCaseInputContract(t *testing.T) {
	source := githubReviewAgentSource(t)
	for _, want := range []string{
		"input.prNumber",
		"input.headSha",
		"input.baseRef",
		"input.repoFullName",
		"input.connectorId",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("github-review-agent source missing camelCase input field %q", want)
		}
	}
}

// The source must parse as JavaScript (the flue build transpiles it).
func TestGitHubReviewAgentWorkflowSourceParsesAsJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not available: %v", err)
	}
	source := githubReviewAgentSource(t)
	path := filepath.Join(t.TempDir(), "github-review-agent.mjs")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write workflow source: %v", err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil { //nolint:norawexec // syntax-check via the node binary located by the test itself
		t.Fatalf("node --check failed: %v\n%s", err, out)
	}
}

func TestGitHubReviewTaskRunnerSourceParsesAsJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not available: %v", err)
	}
	spec, ok := BuiltinWorkflow(BuiltinGitHubReviewAgentWorkflowName)
	if !ok {
		t.Fatal("built-in github-review-agent workflow missing")
	}
	source := spec.Files["workflows/"+BuiltinGitHubReviewTaskRunnerName+".ts"]
	if source == "" {
		t.Fatalf("built-in github review task runner source missing")
	}
	path := filepath.Join(t.TempDir(), BuiltinGitHubReviewTaskRunnerName+".mjs")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write task runner source: %v", err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil { //nolint:norawexec // syntax-check via the node binary located by the test itself
		t.Fatalf("node --check failed: %v\n%s", err, out)
	}
}

func TestGitHubReviewTaskRunnerSourceContract(t *testing.T) {
	source := githubReviewTaskRunnerSource(t)
	for _, want := range []string{
		`task_runner: "github-review-task-runner"`,
		`runtime_strategy: "codex-review"`,
		`review_findings: JSON.stringify(findings)`,
		`transcript_entries: transcriptEntries`,
		`canonicalTranscriptEntries(taskRunId`,
		`redactTranscriptText(prompt`,
		`"--output-schema"`,
		`"--output-last-message"`,
		`execFileSync(CODEX`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("github-review-task-runner source missing %q", want)
		}
	}
}

func TestGitHubReviewTaskRunnerPersistsCanonicalTranscriptAndFindings(t *testing.T) {
	const secret = "review-test-secret-value-12345"
	result := runGitHubReviewTaskRunner(t, reviewTaskRunnerFixture{
		Mode:   "success",
		Secret: secret,
		Diff:   "diff --git a/demo.go b/demo.go\n+" + secret + "\n",
	})
	if result.Status != "completed" || result.ExitCode != 0 {
		t.Fatalf("result = status %q exit %d error %q, want completed/0", result.Status, result.ExitCode, result.ErrorMessage)
	}
	var findings struct {
		Summary  string `json:"summary"`
		Comments []any  `json:"comments"`
	}
	if err := json.Unmarshal([]byte(result.RuntimeMetadata["review_findings"]), &findings); err != nil {
		t.Fatalf("review_findings is not valid JSON: %v", err)
	}
	if findings.Summary != "fixture review complete" || len(findings.Comments) != 0 {
		t.Fatalf("review_findings = %+v", findings)
	}
	assertReviewTranscriptShape(t, result.TranscriptEntries, "completed")
	if got := result.TranscriptEntries[1].Text; !strings.Contains(got, "diff --git") || strings.Contains(got, secret) || !strings.Contains(got, "REDACTED") {
		t.Fatalf("user transcript was not a redacted real prompt: %q", got)
	}
	if got := result.TranscriptEntries[2].Text; !strings.Contains(got, "fixture review complete") {
		t.Fatalf("assistant transcript = %q, want real output-last-message", got)
	}
}

func TestGitHubReviewTaskRunnerFailureTranscriptsAreHonest(t *testing.T) {
	tests := []struct {
		name          string
		fixture       reviewTaskRunnerFixture
		errorClass    string
		wantEntries   int
		wantAssistant bool
		wantResult    string
	}{
		{
			name: "codex execution failure", fixture: reviewTaskRunnerFixture{Mode: "fail", Diff: "diff --git a/a b/a\n+x\n"},
			errorClass: "codex_exec_failed", wantEntries: 3, wantResult: "failed",
		},
		{
			name: "invalid final findings", fixture: reviewTaskRunnerFixture{
				Mode: "invalid", Secret: "invalid-output-secret-value-12345", Diff: "diff --git a/a b/a\n+x\n",
			},
			errorClass: "codex_no_findings", wantEntries: 4, wantAssistant: true, wantResult: "could not",
		},
		{
			name: "empty diff", fixture: reviewTaskRunnerFixture{Mode: "success", Diff: ""},
			errorClass: "empty_diff", wantEntries: 2, wantResult: "no diff",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runGitHubReviewTaskRunner(t, tt.fixture)
			if result.Status != "failed" || result.ExitCode != 1 || result.ErrorClass != tt.errorClass {
				t.Fatalf("result = status %q exit %d class %q, want failed/1/%s", result.Status, result.ExitCode, result.ErrorClass, tt.errorClass)
			}
			if len(result.TranscriptEntries) != tt.wantEntries {
				t.Fatalf("transcript entries = %d, want %d: %+v", len(result.TranscriptEntries), tt.wantEntries, result.TranscriptEntries)
			}
			assertCanonicalTranscript(t, result.TranscriptEntries)
			last := result.TranscriptEntries[len(result.TranscriptEntries)-1]
			if last.Type != transcript.EventResult || !strings.Contains(last.Text, tt.wantResult) {
				t.Fatalf("terminal transcript = %+v, want honest failure result", last)
			}
			hasAssistant := false
			for _, entry := range result.TranscriptEntries {
				hasAssistant = hasAssistant || entry.Role == transcript.RoleAssistant
			}
			if hasAssistant != tt.wantAssistant {
				t.Fatalf("assistant transcript present = %v, want %v", hasAssistant, tt.wantAssistant)
			}
			if tt.fixture.Secret != "" {
				if strings.Contains(result.ErrorMessage, tt.fixture.Secret) {
					t.Fatalf("error message leaked secret: %q", result.ErrorMessage)
				}
				for _, entry := range result.TranscriptEntries {
					if strings.Contains(entry.Text, tt.fixture.Secret) {
						t.Fatalf("transcript leaked secret: %+v", entry)
					}
				}
			}
		})
	}
}

type reviewTaskRunnerFixture struct {
	Mode   string
	Secret string
	Diff   string
}

type reviewTaskRunnerResult struct {
	Status            string             `json:"status"`
	ExitCode          int                `json:"exitCode"`
	ErrorClass        string             `json:"errorClass"`
	ErrorMessage      string             `json:"errorMessage"`
	RuntimeMetadata   map[string]string  `json:"runtimeMetadata"`
	TranscriptEntries []transcript.Event `json:"transcript_entries"`
}

func runGitHubReviewTaskRunner(t *testing.T, fixture reviewTaskRunnerFixture) reviewTaskRunnerResult {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not available: %v", err)
	}
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "node_modules", "@flue", "runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("mkdir runtime stub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "package.json"), []byte(`{"type":"module","exports":"./index.js"}`), 0o644); err != nil {
		t.Fatalf("write runtime package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "index.js"), []byte(
		`export const defineAgent = (value) => value; export const defineWorkflow = (value) => value;`,
	), 0o644); err != nil {
		t.Fatalf("write runtime stub: %v", err)
	}
	modulePath := filepath.Join(root, "github-review-task-runner.mjs")
	if err := os.WriteFile(modulePath, []byte(githubReviewTaskRunnerSource(t)), 0o644); err != nil {
		t.Fatalf("write runner source: %v", err)
	}
	fakeCodex := filepath.Join(root, "fake-codex.mjs")
	fakeSource := `#!/usr/bin/env node
import fs from "node:fs";
const mode = process.env.FAKE_CODEX_MODE || "success";
if (mode === "fail") process.exit(7);
const args = process.argv.slice(2);
const outIndex = args.indexOf("--output-last-message");
if (outIndex < 0 || !args[outIndex + 1]) process.exit(8);
const output = mode === "invalid"
  ? "not-json findings from codex " + (process.env.REVIEW_TEST_SECRET || "")
  : JSON.stringify({summary:"fixture review complete", comments:[]});
fs.writeFileSync(args[outIndex + 1], output);
`
	if err := os.WriteFile(fakeCodex, []byte(fakeSource), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"task_run_id": "review-fixture-1",
		"input": map[string]any{
			"diff": fixture.Diff, "rubric": "Report only blocking findings.",
			"repo": "fixture/repo", "headSha": "abc1234",
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	wrapperPath := filepath.Join(root, "run.mjs")
	wrapperSource := `import { run } from "./github-review-task-runner.mjs";
const result = await run({payload: JSON.parse(process.env.RUNNER_TEST_PAYLOAD)});
process.stdout.write(JSON.stringify(result));
`
	if err := os.WriteFile(wrapperPath, []byte(wrapperSource), 0o644); err != nil {
		t.Fatalf("write runner wrapper: %v", err)
	}
	cmd := exec.Command(node, wrapperPath) //nolint:norawexec // direct behavioral test of the embedded Node task runner
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"LOOM_CODEX_BIN="+fakeCodex,
		"FAKE_CODEX_MODE="+fixture.Mode,
		"RUNNER_TEST_PAYLOAD="+string(payload),
		"REVIEW_TEST_SECRET="+fixture.Secret,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run task runner fixture: %v\n%s", err, out)
	}
	var result reviewTaskRunnerResult
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("decode task runner result: %v\n%s", err, out)
	}
	return result
}

func assertReviewTranscriptShape(t *testing.T, entries []transcript.Event, resultText string) {
	t.Helper()
	if len(entries) != 4 {
		t.Fatalf("transcript entries = %d, want 4: %+v", len(entries), entries)
	}
	assertCanonicalTranscript(t, entries)
	if entries[1].Role != transcript.RoleUser || entries[1].Type != transcript.EventText {
		t.Fatalf("prompt transcript = %+v, want canonical user text", entries[1])
	}
	if entries[2].Role != transcript.RoleAssistant || entries[2].Type != transcript.EventText {
		t.Fatalf("findings transcript = %+v, want canonical assistant text", entries[2])
	}
	if entries[3].Role != transcript.RoleSystem || entries[3].Type != transcript.EventResult || entries[3].Text != resultText {
		t.Fatalf("result transcript = %+v, want system result %q", entries[3], resultText)
	}
}

func assertCanonicalTranscript(t *testing.T, entries []transcript.Event) {
	t.Helper()
	for index, entry := range entries {
		if entry.Seq != index+1 {
			t.Fatalf("entry %d seq = %d, want %d", index, entry.Seq, index+1)
		}
		if entry.Timestamp.IsZero() {
			t.Fatalf("entry %d has zero timestamp: %+v", index, entry)
		}
		if !transcript.KnownRoles[entry.Role] || !transcript.KnownEventTypes[entry.Type] {
			t.Fatalf("entry %d is outside canonical vocabulary: %+v", index, entry)
		}
	}
	if entries[0].Role != transcript.RoleSystem || entries[0].Type != transcript.EventSessionMeta {
		t.Fatalf("first transcript entry = %+v, want system session_meta", entries[0])
	}
}

func TestLocalReviewAgentWorkflowSourceParsesAsJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not available: %v", err)
	}
	source := localReviewAgentSource(t)
	path := filepath.Join(t.TempDir(), BuiltinLocalReviewAgentWorkflowName+".mjs")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write local review source: %v", err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil { //nolint:norawexec // syntax-check via the node binary located by the test itself
		t.Fatalf("node --check failed: %v\n%s", err, out)
	}
}

func TestLocalReviewAgentWorkflowSourceContract(t *testing.T) {
	source := localReviewAgentSource(t)
	for _, want := range []string{
		`local-branch:`,
		`loom.tasks.diff({ taskId: issueId })`,
		`loom.tasks.claimReview({ taskId: issueId })`,
		`loom.tasks.releaseReview({ taskId: issueId })`,
		`runner: "github-review-task-runner"`,
		`closeTask: false`,
		`retainWorkItemClaim: true`,
		`review-cycle:`,
		`loom.tasks.handoffReview({`,
		`status: blocking ? "open" : "closed"`,
		`local_review_diff_`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("local-review-agent source missing %q", want)
		}
	}
}

func TestDaytonaTaskRunnerSourceParsesAsJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not available: %v", err)
	}
	source := daytonaTaskRunnerSource(t)
	path := filepath.Join(t.TempDir(), "daytona-task-runner.mjs")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write daytona task runner source: %v", err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil { //nolint:norawexec // syntax-check via the node binary located by the test itself
		t.Fatalf("node --check failed: %v\n%s", err, out)
	}
}

func TestDaytonaTaskRunnerSourceContract(t *testing.T) {
	source := daytonaTaskRunnerSource(t)
	for _, want := range []string{
		`TaskRunClient.fromEnv()`,
		`@loom/sdk/runtime-adapters`,
		`readRuntimeCredential(taskContext.client, "daytona")`,
		`DAYTONA_REPO_URL`,
		`client.create({`,
		// Full clone (NOT shallow): stacked-PR base SHAs + the merge-base reconcile
		// need real history, so the runner clones via cloneCommand without --depth.
		`Full clone (NOT --depth 1)`,
		`function cloneCommand(`,
		`imports.runtime.registerProvider("openai-codex"`,
		`createFlueTranscriptCollector()`,
		`transcript_entries: transcriptEntries`,
		`uploadPatchArtifact(taskContext.client`,
		`patch_artifact_id`,
		`loom_task_session_id`,
		`runtime_strategy: "flue-daytona-codex"`,
		`task_runner: "daytona-task-runner"`,
		`daytona_sandbox_env_leak`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("daytona-task-runner source missing %q", want)
		}
	}
}

func githubReviewAgentSource(t *testing.T) string {
	t.Helper()
	spec, ok := BuiltinWorkflow(BuiltinGitHubReviewAgentWorkflowName)
	if !ok {
		t.Fatal("built-in github-review-agent workflow missing")
	}
	return spec.Files[spec.Entrypoint]
}

func githubReviewTaskRunnerSource(t *testing.T) string {
	t.Helper()
	spec, ok := BuiltinWorkflow(BuiltinGitHubReviewAgentWorkflowName)
	if !ok {
		t.Fatal("built-in github-review-agent workflow missing")
	}
	source := spec.Files["workflows/"+BuiltinGitHubReviewTaskRunnerName+".ts"]
	if source == "" {
		t.Fatal("built-in github-review-task-runner source missing")
	}
	return source
}

func localReviewAgentSource(t *testing.T) string {
	t.Helper()
	spec, ok := BuiltinWorkflow(BuiltinLocalReviewAgentWorkflowName)
	if !ok {
		t.Fatal("built-in local-review-agent workflow missing")
	}
	return spec.Files[spec.Entrypoint]
}

func daytonaTaskRunnerSource(t *testing.T) string {
	t.Helper()
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("built-in epic-runner workflow missing")
	}
	source := spec.Files["workflows/daytona-task-runner.ts"]
	if source == "" {
		t.Fatal("built-in daytona-task-runner source missing")
	}
	return source
}
