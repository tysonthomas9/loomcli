package authoring

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// activeManifestRunnersAreStale forces a refresh when the active manifest
// declares a deprecated runner (openshell) or a runner the freshly-derived set
// no longer contains (§4.6).
func TestActiveManifestRunnersAreStale(t *testing.T) {
	fresh := map[string]struct{}{
		"local-task-runner":   {},
		"daytona-task-runner": {},
	}
	cases := []struct {
		name      string
		manifest  map[string]string
		fresh     map[string]struct{}
		wantStale bool
	}{
		{
			name:      "missing runners is stale when builtin derives runners",
			manifest:  map[string]string{},
			wantStale: true,
		},
		{
			name:      "missing runners is not stale when builtin has no runners",
			manifest:  map[string]string{},
			wantStale: false,
			fresh:     map[string]struct{}{},
		},
		{
			name:      "current runners are not stale",
			manifest:  map[string]string{"runners": `[{"name":"local-task-runner"},{"name":"daytona-task-runner"}]`},
			wantStale: false,
		},
		{
			name:      "deprecated openshell is stale",
			manifest:  map[string]string{"runners": `[{"name":"local-task-runner"},{"name":"openshell-task-runner"}]`},
			wantStale: true,
		},
		{
			name:      "runner not in fresh set is stale",
			manifest:  map[string]string{"runners": `[{"name":"local-task-runner"},{"name":"gone-task-runner"}]`},
			wantStale: true,
		},
		{
			name:      "undecodable runner list is stale",
			manifest:  map[string]string{"runners": `not-json`},
			wantStale: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			freshSet := fresh
			if tc.fresh != nil {
				freshSet = tc.fresh
			}
			if got := activeManifestRunnersAreStale(tc.manifest, freshSet); got != tc.wantStale {
				t.Fatalf("activeManifestRunnersAreStale = %v, want %v", got, tc.wantStale)
			}
		})
	}
}

// workflowRunnerNameSet derives the same deny-listed set workflowRunnerSpecs
// produces for the epic-runner builtin (no openshell).
func TestWorkflowRunnerNameSetExcludesOpenShell(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("epic-runner builtin missing")
	}
	set := workflowRunnerNameSet(spec)
	if _, ok := set["openshell-task-runner"]; ok {
		t.Fatalf("derived runner set must not include openshell-task-runner: %+v", set)
	}
	for _, want := range []string{"local-task-runner", "daytona-task-runner"} {
		if _, ok := set[want]; !ok {
			t.Fatalf("derived runner set missing %q: %+v", want, set)
		}
	}
}

func installFakeWorkflowBuildDeps(t *testing.T) {
	t.Helper()

	root := t.TempDir()
	sdkRoot := filepath.Join(root, "sdk")
	if err := os.MkdirAll(sdkRoot, 0o755); err != nil {
		t.Fatalf("create fake sdk root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sdkRoot, "package.json"), []byte(`{"name":"@loom/sdk"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write fake sdk package: %v", err)
	}

	runtimeRoot := filepath.Join(root, "runtime")
	for _, dir := range []string{
		runtimeRoot,
		filepath.Join(runtimeRoot, "node_modules", "@hono", "node-server"),
		filepath.Join(runtimeRoot, "node_modules", "hono"),
		filepath.Join(runtimeRoot, "node_modules", "valibot"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create fake runtime dependency %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "package.json"), []byte(`{"name":"@flue/runtime"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write fake runtime package: %v", err)
	}

	flue := filepath.Join(root, "fake-flue.sh")
	script := `#!/bin/sh
set -eu
out=""
prev=""
for arg in "$@"; do
	if [ "$prev" = "--output" ]; then
		out="$arg"
		break
	fi
	prev="$arg"
done
if [ -z "$out" ]; then
	echo "missing --output" >&2
	exit 1
fi
mkdir -p "$out"
printf 'export {};\n' > "$out/server.mjs"
echo "fake flue build"
`
	if err := os.WriteFile(flue, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake flue: %v", err)
	}
	cmd, err := json.Marshal([]string{flue})
	if err != nil {
		t.Fatalf("encode fake flue command: %v", err)
	}
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", string(cmd))
	t.Setenv("LOOM_REAL_FLUE_CMD", "")
	t.Setenv("LOOM_SDK_ROOT", sdkRoot)
	t.Setenv("FLUE_RUNTIME_ROOT", runtimeRoot)
}
