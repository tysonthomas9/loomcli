package workflows

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/scriptedroles"
)

func TestBuiltinScoutDeclaresScoutTaskRunner(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinScoutWorkflowName)
	if !ok {
		t.Fatal("scout builtin missing")
	}
	runners := workflowRunnerSpecs(BuildAndRegisterOptions{
		Entrypoint:    spec.Entrypoint,
		Files:         spec.Files,
		DeriveRunners: true,
	})
	if len(runners) != 1 {
		t.Fatalf("runner specs = %+v, want one scout runner", runners)
	}
	if runners[0].Name != BuiltinScoutTaskRunnerName || runners[0].Kind != "flue-workflow" || runners[0].Entrypoint != BuiltinScoutTaskRunnerName {
		t.Fatalf("runner spec = %+v, want scout-task-runner flue workflow", runners[0])
	}
}

// The scout is a single linear pass: analyze leaf -> issue creation via the
// issues namespace (run-token side) -> write leaf journaling agents.md +
// history.md. These string anchors lock the authoring contract (the live run
// exercises the runtime path).
func TestScoutWorkflowSourceContract(t *testing.T) {
	source := scoutWorkflowSource(t)
	tests := []struct {
		name string
		want string
	}{
		{name: "camelCase sdk import", want: "import { createLoomDriverClient } from '@loom/sdk/driver';"},
		{name: "flue pin shape: defineWorkflow default export", want: "export default defineWorkflow({"},
		{name: "flue pin shape: credential-free stub agent", want: "defineAgent(() => ({ model: false }))"},
		{name: "payload via launcher env", want: "process.env.LOOM_FLUE_INVOKE_PAYLOAD"},
		{name: "authenticated agent identity env", want: "process.env.LOOM_AGENT_SERVICE_ID"},
		{name: "agent identity rides opaque task input", want: "agent_service_id: agentServiceID"},
		{name: "default scout runner", want: `"scout-task-runner"`},
		{name: "analyze phase enqueued", want: `"scout-analyze"`},
		{name: "write phase enqueued", want: `"scout-write"`},
		{name: "deterministic leaf run ids", want: "deterministicTaskRunId(loom.driverRunId, label)"},
		{name: "leaf awaited", want: "loom.taskRuns.await({ taskRunId })"},
		{name: "hard cap of five", want: "const MAX_RECOMMENDATIONS = 5;"},
		{name: "issues namespace create", want: "issuesApi.create({"},
		{name: "issues namespace degrades when absent", want: "journaled without creating issues"},
		{name: "quarantine label re-asserted", want: `labels.push("recommended")`},
		{name: "quarantine status on create", want: `status: "review"`},
		{name: "repo routing label re-asserted", want: `labels.push("repo:" + repo)`},
		{name: "priority clamped to loom range", want: "clampInt(rec.priority, 0, 4, 2)"},
		{name: "write phase carries history entry", want: "historyEntry: historyEntry(loom, analysis.value, outcome)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(source, tt.want) {
				t.Fatalf("scout workflow source missing %q", tt.want)
			}
		})
	}
	// One request call site (invoked once per phase) keeps the deterministic
	// re-entry guarantee: a re-run re-derives the same ids and treats conflict
	// as already-enqueued.
	if got := strings.Count(source, "loom.taskRuns.request("); got != 1 {
		t.Fatalf("taskRuns.request occurrences = %d, want exactly 1 (single request site)", got)
	}
}

// The leaf is agentic in the local-task-runner lineage: it resolves the
// workspace-default backend from the host bridge env, execs the backend CLI
// with tools allowed, refuses the "." placement fallback, and owns the two
// workspace-root files with atomic writes and scout fence markers.
func TestScoutTaskRunnerSourceContract(t *testing.T) {
	source := scoutTaskRunnerSource(t)
	role, ok := scriptedroles.ForRole(scriptedroles.ScoutRoleName)
	if !ok || role.DefaultInstance == nil {
		t.Fatal("scout catalog default is missing")
	}
	for name, want := range map[string]string{
		"catalog default instance": `const DEFAULT_AGENT_SERVICE_ID = "` + role.DefaultInstance.ServiceID + `"`,
		"catalog journal filename": `const SCOUT_JOURNAL_FILENAME = "` + role.JournalFilename + `"`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("%s missing from scout leaf source: %q", name, want)
		}
	}
	tests := []struct {
		name string
		want string
	}{
		{name: "flue pin shape: defineWorkflow default export", want: "export default defineWorkflow({"},
		{name: "flue pin shape: credential-free stub agent", want: "defineAgent(() => ({ model: false }))"},
		{name: "payload via env", want: "process.env.LOOM_FLUE_INVOKE_PAYLOAD"},
		{name: "backend from host bridge env", want: "process.env.LOOM_TASK_RUNNER_BACKEND"},
		{name: "placement guard: runtime dir fallback refused", want: `LOOM_WORKSPACE_RUNTIME_DIR resolved to the "." fallback`},
		{name: "placement guard: repo checkout refused", want: "refuses to write inside a repo checkout"},
		{name: "workspace anchor fallback", want: "env.LOOM_WORKTREE_PATH"},
		{name: "loom priority semantics", want: "0 = P0 critical"},
		{name: "backlog is four", want: "4 = P4 backlog"},
		{name: "acceptance criteria folded into description", want: `"## Acceptance Criteria" section`},
		{name: "quarantine label forced", want: `labels.push("recommended")`},
		{name: "per-instance fence marker grammar", want: "<!-- loom:agent:"},
		{name: "legacy fence migration", want: "LEGACY_SCOUT_FENCE_BEGIN"},
		{name: "service id validation", want: "SERVICE_ID_PATTERN"},
		{name: "namespaced instance state", want: `path.join(root, ".loom", "agents", serviceID)`},
		{name: "atomic tmp+rename writes", want: `".loom-atomic-"`},
		{name: "zero-repo run journals", want: "nothing to analyze: the workspace has no attached repos"},
		{name: "read-only prompt discipline", want: "READ-ONLY"},
		{name: "context first, task text last", want: "--- TASK ---"},
		{name: "metadata task runner id", want: `task_runner: "scout-task-runner"`},
		{name: "analysis result metadata key", want: "scout_analysis: JSON.stringify(analysis)"},
		{name: "AI stdout log tail cap", want: "textTail(exec.stdout, 100000)"},
		{name: "AI stderr log tail cap", want: "textTail(exec.stderr, 20000)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(source, tt.want) {
				t.Fatalf("scout task runner source missing %q", tt.want)
			}
		})
	}
	// The prototype's inverted priority wording must never come back: Loom is
	// 0=P0 critical … 4=backlog, so "0 is lowest" phrasing is a bug.
	for _, forbidden := range []string{"0 lowest", "0 is lowest", "4 is highest", "4 highest"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("scout task runner source contains inverted priority wording %q", forbidden)
		}
	}
}

// The workspace context block must precede the task instructions in the
// assembled prompt source, so repeated runs share a cacheable prefix (spec:
// Known implementation notes — order shared context first, task text last).
func TestScoutTaskRunnerPromptOrdersContextBeforeTask(t *testing.T) {
	source := scoutTaskRunnerSource(t)
	contextIdx := strings.Index(source, "--- WORKSPACE CONTEXT")
	taskIdx := strings.Index(source, "--- TASK ---")
	if contextIdx < 0 || taskIdx < 0 {
		t.Fatal("scout task runner prompt is missing its context/task section markers")
	}
	if contextIdx > taskIdx {
		t.Fatal("workspace context must come before the task text (prompt-cache reuse)")
	}
}

// The source must parse as JavaScript (the flue build transpiles it).
func TestScoutWorkflowSourceParsesAsJavaScript(t *testing.T) {
	assertParsesAsJavaScript(t, BuiltinScoutWorkflowName+".mjs", scoutWorkflowSource(t))
}

func TestScoutTaskRunnerSourceParsesAsJavaScript(t *testing.T) {
	assertParsesAsJavaScript(t, BuiltinScoutTaskRunnerName+".mjs", scoutTaskRunnerSource(t))
}

func assertParsesAsJavaScript(t *testing.T, filename, source string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not available: %v", err)
	}
	path := filepath.Join(t.TempDir(), filename)
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil { //nolint:norawexec // syntax-check via the node binary located by the test itself
		t.Fatalf("node --check failed: %v\n%s", err, out)
	}
}

// TestBuildBuiltinScoutBundleWithRealFlue proves the embedded scout sources
// compile under the real pinned Flue toolchain via BuildBuiltinBundle — the
// same host-side materialization path serve's builtin ensure uses. Gated like
// TestBuildAndRegisterCustomSourceWithRealFlue: skips when no real Flue CLI is
// reachable (sibling ../flue checkout, or LOOM_REAL_FLUE_CMD_JSON /
// LOOM_REAL_FLUE_CMD plus FLUE_RUNTIME_ROOT preset in the environment).
func TestBuildBuiltinScoutBundleWithRealFlue(t *testing.T) {
	configureRealFlueForBuiltinBuild(t)
	dest := filepath.Join(t.TempDir(), "dist")
	serverPath, output, err := BuildBuiltinBundle(context.Background(), BuiltinScoutWorkflowName, dest)
	if err != nil {
		t.Fatalf("BuildBuiltinBundle(scout): %v\n%s", err, output)
	}
	info, err := os.Stat(serverPath)
	if err != nil {
		t.Fatalf("scout server.mjs not materialized: %v", err)
	}
	if info.IsDir() || info.Size() == 0 {
		t.Fatalf("scout server.mjs is empty or a directory (size=%d)", info.Size())
	}
}

func configureRealFlueForBuiltinBuild(t *testing.T) {
	t.Helper()
	repoRoot := workflowTestRepoRoot(t)
	if os.Getenv("LOOM_REAL_FLUE_CMD_JSON") == "" && os.Getenv("LOOM_REAL_FLUE_CMD") == "" {
		flueBin := filepath.Join(repoRoot, "..", "flue", "packages", "cli", "bin", "flue.mjs")
		if _, err := os.Stat(flueBin); err != nil {
			t.Skipf("real Flue CLI unavailable: %v (set LOOM_REAL_FLUE_CMD_JSON to run)", err)
		}
		node, err := exec.LookPath("node")
		if err != nil {
			t.Skipf("node unavailable: %v", err)
		}
		flueCmd, err := json.Marshal([]string{node, flueBin})
		if err != nil {
			t.Fatalf("encode flue cmd: %v", err)
		}
		t.Setenv("LOOM_REAL_FLUE_CMD_JSON", string(flueCmd))
		t.Setenv("LOOM_REAL_FLUE_CMD", "")
	}
	if os.Getenv("LOOM_SDK_ROOT") == "" {
		t.Setenv("LOOM_SDK_ROOT", filepath.Join(repoRoot, "sdk"))
	}
	if os.Getenv("LOOM_FLUE_RUNTIME_ROOT") == "" && os.Getenv("FLUE_RUNTIME_ROOT") == "" {
		t.Setenv("FLUE_RUNTIME_ROOT", filepath.Join(repoRoot, "..", "flue", "packages", "runtime"))
	}
}

func scoutWorkflowSource(t *testing.T) string {
	t.Helper()
	spec, ok := BuiltinWorkflow(BuiltinScoutWorkflowName)
	if !ok {
		t.Fatal("built-in scout workflow missing")
	}
	return spec.Files[spec.Entrypoint]
}

func scoutTaskRunnerSource(t *testing.T) string {
	t.Helper()
	spec, ok := BuiltinWorkflow(BuiltinScoutWorkflowName)
	if !ok {
		t.Fatal("built-in scout workflow missing")
	}
	source := spec.Files["workflows/"+BuiltinScoutTaskRunnerName+".ts"]
	if source == "" {
		t.Fatal("built-in scout-task-runner source missing")
	}
	return source
}
