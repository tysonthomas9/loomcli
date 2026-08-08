package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Source-tree built-ins are registered and discoverable through the generalized
// registry: BuiltinWorkflowNames returns them sorted, and each resolves to a
// single-entrypoint spec at workflows/{name}.ts. BuiltinWorkflow returns a
// defensive copy of the files map (mutating it must not corrupt the registry).
func TestBuiltinWorkflowRegistryListsSourceTreeBuiltins(t *testing.T) {
	names := BuiltinWorkflowNames()
	want := []string{BuiltinEpicRunnerWorkflowName, BuiltinGitHubReviewAgentWorkflowName, BuiltinSessionEvalAgentWorkflowName}
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
		wantFiles := 1
		if name == BuiltinEpicRunnerWorkflowName {
			wantFiles = 4
		} else if name == BuiltinGitHubReviewAgentWorkflowName {
			wantFiles = 2
		} else if name == BuiltinSessionEvalAgentWorkflowName {
			wantFiles = 2
		}
		if len(spec.Files) != wantFiles {
			t.Fatalf("%s spec has %d files, want %d", name, len(spec.Files), wantFiles)
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
		`"--output-schema"`,
		`"--output-last-message"`,
		`execFileSync(CODEX`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("github-review-task-runner source missing %q", want)
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
