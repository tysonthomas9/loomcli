package driver

import (
	"context"
	"strings"
	"testing"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return strings.TrimPrefix(kv, prefix), true
		}
	}
	return "", false
}

// The stacked delivery push leases against the head the stack node was last
// published at. Without carrying OutputSHA to the runner, the push has nothing
// to assert and falls back to overwriting whatever it observes — which is how a
// restack or a reviewer commit gets discarded.
func TestStackBindingCarriesRecordedOutputSHA(t *testing.T) {
	ctx := context.Background()
	store := stackstore.New(t.TempDir())
	const ws, repo = "WS", "acme/widgets"

	if err := store.EnsureStack(ctx, sl.Stack{ID: "epic:E", WorkspaceKey: ws, RepoName: repo, RootBase: "main"}); err != nil {
		t.Fatalf("ensure stack: %v", err)
	}
	if _, err := store.AddNode(ctx, ws, "epic:E", "A", "", ""); err != nil {
		t.Fatalf("add A: %v", err)
	}
	if _, err := store.AddNode(ctx, ws, "epic:E", "B", "A", ""); err != nil {
		t.Fatalf("add B: %v", err)
	}

	// Before B ever publishes there is no recorded head: the push must lease
	// "ref must not exist" rather than adopt whatever is on the remote.
	binding, ok, err := stackBindingForTask(ctx, store, ws, repo, "B")
	if err != nil || !ok {
		t.Fatalf("stackBindingForTask(B) = (%v,%v), want ok", ok, err)
	}
	if binding.OutputSHA != "" {
		t.Fatalf("unpublished node OutputSHA = %q, want empty", binding.OutputSHA)
	}

	// After B publishes, the recorded head becomes the lease expectation.
	if _, err := recordStackOutput(ctx, store, ws, repo, "B", sl.NodeStatePublished, "cafebabe"); err != nil {
		t.Fatalf("recordStackOutput(B): %v", err)
	}
	binding, ok, err = stackBindingForTask(ctx, store, ws, repo, "B")
	if err != nil || !ok {
		t.Fatalf("stackBindingForTask(B) after publish = (%v,%v), want ok", ok, err)
	}
	if binding.OutputSHA != "cafebabe" {
		t.Fatalf("OutputSHA = %q, want %q", binding.OutputSHA, "cafebabe")
	}
}

// The runner reads the lease expectation from LOOM_TASK_RUN_OUTPUT_SHA, so the
// host must export it alongside the other stack signals.
func TestTaskRunnerEnvExportsStackOutputSHA(t *testing.T) {
	exec := HostBridgeTaskExecutor{
		stackBinding: &TaskLineage{
			StackID:      "epic:E",
			BaseRef:      "loom/stack/epic-E/A",
			OutputBranch: "loom/stack/epic-E/B",
			OutputSHA:    "cafebabe",
		},
	}
	env := exec.taskRunnerEnv(TaskExecRequest{WorkspaceKey: "WS", TaskID: "B"}, "{}")

	got, ok := envValue(env, "LOOM_TASK_RUN_OUTPUT_SHA")
	if !ok {
		t.Fatalf("LOOM_TASK_RUN_OUTPUT_SHA not exported; env=%v", env)
	}
	if got != "cafebabe" {
		t.Fatalf("LOOM_TASK_RUN_OUTPUT_SHA = %q, want %q", got, "cafebabe")
	}
}

// An unpublished node must export an empty value, not be omitted in a way that
// leaves a stale value inherited from the parent environment.
func TestTaskRunnerEnvExportsEmptyStackOutputSHAWhenUnpublished(t *testing.T) {
	exec := HostBridgeTaskExecutor{
		stackBinding: &TaskLineage{
			StackID:      "epic:E",
			OutputBranch: "loom/stack/epic-E/B",
		},
	}
	env := exec.taskRunnerEnv(TaskExecRequest{WorkspaceKey: "WS", TaskID: "B"}, "{}")

	got, ok := envValue(env, "LOOM_TASK_RUN_OUTPUT_SHA")
	if !ok {
		t.Fatalf("LOOM_TASK_RUN_OUTPUT_SHA not exported; env=%v", env)
	}
	if got != "" {
		t.Fatalf("LOOM_TASK_RUN_OUTPUT_SHA = %q, want empty", got)
	}
}

// Lineage travels to sandboxed runners inside the Input payload, so the recorded
// head must survive the round trip too.
func TestWithLineageCarriesOutputSHA(t *testing.T) {
	merged, err := WithLineage(nil, TaskLineage{StackID: "epic:E", OutputBranch: "b", OutputSHA: "cafebabe"})
	if err != nil {
		t.Fatalf("WithLineage: %v", err)
	}
	if !strings.Contains(string(merged), "cafebabe") {
		t.Fatalf("lineage payload dropped OutputSHA: %s", merged)
	}
}
