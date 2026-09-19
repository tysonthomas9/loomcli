package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests drive the local-mode entrypoint's seeding and pipeline-wiring
// functions directly, with a recording stub on PATH in place of `loom`. The
// entrypoint sources as a library under LOOM_LOCAL_MODE_LIB_ONLY, so none of
// this needs a container or a fleet-db.
//
// What they guard is the failure mode the rig exists to rule out: wiring that
// silently does not happen. A pipeline that stamps labels but gates no claims,
// or an extra agent that was never created, leaves a stack that is up, healthy
// and validating nothing.

// loomStub writes a fake `loom` that appends its argv to a log and answers the
// two commands the wiring reads back. roleSetKeys is what `loom role set
// --help` will advertise; failOn makes any invocation whose argv contains that
// substring exit non-zero.
func loomStub(t *testing.T, roleSetKeys []string, failOn string) (dir, logPath string) {
	t.Helper()
	dir = t.TempDir()
	logPath = filepath.Join(dir, "calls.log")

	var help strings.Builder
	help.WriteString("Set a role field by key. Supported keys:\\n")
	for _, k := range roleSetKeys {
		help.WriteString("  " + k + "     string\\n")
	}

	script := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "` + logPath + `"
if [ "$1 $2" = "role set" ] && [ "$3" = "--help" ]; then
  printf '` + help.String() + `'
  exit 0
fi
`
	if failOn != "" {
		script += `case "$*" in
  *` + failOn + `*) echo "stub: refusing $*" >&2; exit 1 ;;
esac
`
	}
	script += "exit 0\n"

	path := filepath.Join(dir, "loom")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatalf("write loom stub: %v", err)
	}
	// git is called by seed_extra_repos; a no-op stub keeps the test off the
	// real filesystem layout the container provides.
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatalf("write git stub: %v", err)
	}
	return dir, logPath
}

// runEntrypointFunc sources the entrypoint as a library and runs one snippet
// against it. It returns the combined output, the recorded `loom` calls and
// whether the snippet succeeded.
func runEntrypointFunc(t *testing.T, env []string, roleSetKeys []string, failOn, snippet string) (out, calls string, ok bool) {
	t.Helper()
	stubDir, logPath := loomStub(t, roleSetKeys, failOn)
	root := repoRoot(t)

	script := `set -uo pipefail
export LOOM_LOCAL_MODE_LIB_ONLY=1
. "` + root + `/test/local-mode/local-mode-entrypoint"
` + snippet

	cmd := exec.Command("bash", "-c", script) //nolint:norawexec
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"LOOM_LOCAL_MODE_WORKSPACE_ROOT="+t.TempDir(),
	)
	cmd.Env = append(cmd.Env, env...)

	raw, err := cmd.CombinedOutput()
	recorded, readErr := os.ReadFile(logPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("read stub log: %v", readErr)
	}
	return string(raw), string(recorded), err == nil
}

const routingKeys = "task_filter labels exclude_labels"

func splitKeys(s string) []string { return strings.Fields(s) }

func TestEntrypointSeedsExtraAgentsWithGeneratedRole(t *testing.T) {
	t.Parallel()
	out, calls, ok := runEntrypointFunc(t,
		[]string{"LOOM_LOCAL_MODE_EXTRA_AGENTS=critic:critic,second"},
		splitKeys(routingKeys), "", "seed_extra_agents")
	if !ok {
		t.Fatalf("seed_extra_agents failed: %s", out)
	}
	// A custom role must be created with a prompt file: a role without one
	// fails daemon creation, which stops every agent, not just this one.
	if !strings.Contains(calls, "role add critic --kind worker --prompt-file prompts/critic.md") {
		t.Errorf("critic role was not created with a prompt file; calls:\n%s", calls)
	}
	if !strings.Contains(calls, "agentdef add critic --role critic") {
		t.Errorf("critic agent was not created; calls:\n%s", calls)
	}
	// A bare name inherits the code role and needs no generated prompt.
	if !strings.Contains(calls, "agentdef add second --role task") {
		t.Errorf("bare agent name did not inherit the code role; calls:\n%s", calls)
	}
	if strings.Contains(calls, "role add task ") {
		t.Errorf("built-in role must not be recreated; calls:\n%s", calls)
	}
}

func TestEntrypointSkipsSeedingWhenKnobsAreUnset(t *testing.T) {
	t.Parallel()
	out, calls, ok := runEntrypointFunc(t, nil, splitKeys(routingKeys), "",
		"seed_extra_agents; seed_extra_repos; seed_pipeline")
	if !ok {
		t.Fatalf("seeding failed with no knobs set: %s", out)
	}
	if strings.TrimSpace(calls) != "" {
		t.Errorf("unset knobs must be a no-op, got calls:\n%s", calls)
	}
}

func TestEntrypointWiresPipelineRoutingAndHooks(t *testing.T) {
	t.Parallel()
	env := []string{
		"LOOM_LOCAL_MODE_PIPELINE=1",
		"LOOM_LOCAL_MODE_PIPELINE_STAGE=critic:criticized:criticized",
		"LOOM_LOCAL_MODE_PIPELINE_CYCLE=plan:3:criticized:ready-to-implement",
		"LOOM_LOCAL_MODE_PIPELINE_SHIP_ROLE=task",
	}
	out, calls, ok := runEntrypointFunc(t, env, splitKeys(routingKeys), "", "seed_pipeline")
	if !ok {
		t.Fatalf("seed_pipeline failed: %s", out)
	}
	want := []string{
		// The claim gate. Without these the pipeline labels everything and
		// routes nothing.
		"role set critic exclude_labels criticized",
		"role set plan labels criticized",
		"role set task labels ready-to-implement",
		// The label writes.
		"agentdef update local-planner --on-complete-cycle 3:criticized:ready-to-implement",
	}
	for _, w := range want {
		if !strings.Contains(calls, w) {
			t.Errorf("missing wiring call %q; calls:\n%s", w, calls)
		}
	}
	// The stage role maps to its agent, not to the role name.
	if !strings.Contains(calls, "--on-complete-add-label criticized") {
		t.Errorf("stage stamp was not wired; calls:\n%s", calls)
	}
}

// A build whose `loom role set` has no label keys cannot gate a claim. The
// entrypoint must refuse before wiring anything, rather than produce a stack
// that stamps labels and routes nothing.
func TestEntrypointRefusesPipelineWithoutRoleLabelKeys(t *testing.T) {
	t.Parallel()
	env := []string{
		"LOOM_LOCAL_MODE_PIPELINE=1",
		"LOOM_LOCAL_MODE_PIPELINE_STAGE=critic:criticized:criticized",
	}
	out, calls, ok := runEntrypointFunc(t, env, splitKeys("task_filter prompt_file"), "", "seed_pipeline")
	if ok {
		t.Fatalf("seed_pipeline succeeded without label routing support:\n%s", out)
	}
	// The message must name the missing key and the knob that needs it, so
	// the fix is obvious without reading the entrypoint.
	if !strings.Contains(out, "labels key") || !strings.Contains(out, "LOOM_LOCAL_MODE_PIPELINE") {
		t.Errorf("failure does not name the missing capability:\n%s", out)
	}
	if strings.Contains(calls, "role set critic") {
		t.Errorf("wiring started before the capability probe failed; calls:\n%s", calls)
	}
}

// A wiring call that fails must fail the whole stack. A half-wired pipeline
// still starts agents and still moves tasks, so a fix "validated" against it
// was validated against routing that does not exist.
func TestEntrypointRefusesHalfWiredPipeline(t *testing.T) {
	t.Parallel()
	env := []string{
		"LOOM_LOCAL_MODE_PIPELINE=1",
		"LOOM_LOCAL_MODE_PIPELINE_STAGE=critic:criticized:criticized",
		"LOOM_LOCAL_MODE_PIPELINE_CYCLE=plan:3:criticized:ready-to-implement",
	}
	out, _, ok := runEntrypointFunc(t, env, splitKeys(routingKeys), "role set plan labels", "seed_pipeline")
	if ok {
		t.Fatalf("seed_pipeline succeeded after a failed wiring call:\n%s", out)
	}
	if !strings.Contains(out, "refusing to start a half-wired stack") {
		t.Errorf("failure is not explained:\n%s", out)
	}
}

// max_agents is read from the knob, not hard-wired to 2: exceeding it fails
// daemon creation, which stops every agent rather than just the extra one.
func TestEntrypointMaxAgentsIsConfigurable(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile(repoRoot(t) + "/test/local-mode/local-mode-entrypoint")
	if err != nil {
		t.Fatalf("read entrypoint: %v", err)
	}
	s := string(content)
	if !strings.Contains(s, `loom daemon profile set max_agents "$MAX_AGENTS"`) {
		t.Error("max_agents is not driven by MAX_AGENTS")
	}
	if !strings.Contains(s, `MAX_AGENTS="${LOOM_LOCAL_MODE_MAX_AGENTS:-2}"`) {
		t.Error("MAX_AGENTS does not default to the demo stack's 2")
	}
}
