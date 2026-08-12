package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCodexStubEpicRunnerPropagatesDesignLookupFailure(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}

	writeExecutable(t, filepath.Join(binDir, "loom"), `#!/bin/sh
if [ "$1" = "data" ] && [ "$2" = "--output" ] && [ "$3" = "json" ] && [ "$4" = "show" ]; then
  printf '{"design":"STUB_CODEX_WRITE=masked.txt"}\n'
  exit 42
fi
exit 0
`)
	writeExecutable(t, filepath.Join(binDir, "jq"), `#!/bin/sh
if [ "$1" = "-nc" ]; then
  printf '{"status":"completed","output":"ok"}\n'
  exit 0
fi
cat >/dev/null
exit 0
`)
	writeExecutable(t, filepath.Join(binDir, "make"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "git"), "#!/bin/sh\nexit 0\n")

	cmd := exec.Command(filepath.Join(sourceRepoRoot(t), "e2e", "stubs", "codex"), "exec", "--json") //nolint:norawexec,gosec // executes this repo's deterministic test stub.
	cmd.Dir = tmp
	cmd.Stdin = strings.NewReader("pre-assigned task: E2E-2\n")
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_CODEX_EPIC_RUNNER=1",
		"LOOM_ASSIGNED_TASK_ID=E2E-2",
		"LOOM_AGENT_NAME=worker-e2e-2-a1",
	)

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("codex stub exited successfully; masked failed design lookup. output:\n%s", out)
	}
}

func sourceRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}
