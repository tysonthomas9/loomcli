package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCodexStubEpicRunnerReadsDirectivesFromPrompt(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}

	writeExecutable(t, filepath.Join(binDir, "loom"), `#!/bin/sh
echo "unexpected loom call: $*" >&2
exit 42
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

	cmd := exec.Command(filepath.Join(repoRoot(t), "e2e", "stubs", "codex"), "exec", "--json") //nolint:norawexec,gosec // executes this repo's deterministic test stub.
	cmd.Dir = tmp
	cmd.Stdin = strings.NewReader("pre-assigned task: E2E-2\nSTUB_CODEX_WRITE=epic-runner-output/prompt-output.txt\n")
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_CODEX_EPIC_RUNNER=1",
		"LOOM_ASSIGNED_TASK_ID=E2E-2",
		"LOOM_AGENT_NAME=worker-e2e-2-a1",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex stub failed: %v\n%s", err, out)
	}
	written, err := os.ReadFile(filepath.Join(tmp, "epic-runner-output", "prompt-output.txt"))
	if err != nil {
		t.Fatalf("read prompt output: %v", err)
	}
	if !strings.Contains(string(written), "task=E2E-2") {
		t.Fatalf("prompt output = %q, want task id", written)
	}
}

func repoRoot(t *testing.T) string {
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
