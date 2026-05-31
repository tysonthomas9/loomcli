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

	cmd := exec.Command(filepath.Join(repoRoot(t), "e2e", "stubs", "codex"), "exec", "--json") //nolint:norawexec,gosec // executes this repo's deterministic test stub.
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

func TestTSFirstLiveProviderScriptsHaveValidSyntax(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found on PATH")
	}
	root := repoRoot(t)
	for _, rel := range []string{
		"e2e/tsfirst_live_provider_connect.sh",
		"e2e/run_tsfirst_live_provider_connect_podman.sh",
	} {
		cmd := exec.Command("bash", "-n", filepath.Join(root, rel)) //nolint:gosec // validates repo-owned shell scripts.
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bash -n %s failed: %v\n%s", rel, err, out)
		}
	}
}

func TestTSFirstLiveProviderConnectScriptAcceptsTypedToolEvidence(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found on PATH")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found on PATH")
	}
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	writeExecutable(t, filepath.Join(binDir, "node"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "codex"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "loom"), `#!/bin/sh
if [ "$1" = "backend" ] && [ "$2" = "health" ]; then
  printf '[{"name":"codex","healthy":true,"installed":true,"api_key_set":true,"message":"ready"}]\n'
  exit 0
fi
if [ "$1" = "check" ]; then
  test -d "$3/.loom/agents" && test -d "$3/.loom/tools"
  exit $?
fi
if [ "$1" = "connect" ]; then
  printf '{"agent":"live-tool-agent","response":"LIVE_TYPED_TOOL_DONE: created triage","tool_runtime":{"status":"backend_typed_tool_runtime","handler_execution":"trusted_executor_configured","schema_publication":"prompt_json_contract","result_feed":"same_turn_prompt_followup"},"tool_calls":[{"name":"create_channel","status":"completed","authorization_status":"authorized","result":"created triage"}],"operation":{"tool_calls":[{"name":"create_channel"}]}}\n'
  exit 0
fi
printf 'unexpected loom command: %s\n' "$*" >&2
exit 2
`)

	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "e2e", "tsfirst_live_provider_connect.sh")) //nolint:gosec // executes repo-owned script with fake binaries.
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"RESULT_ROOT="+filepath.Join(tmp, "result"),
		"BACKEND=codex",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tsfirst_live_provider_connect.sh failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "PASS tsfirst live-provider local-connect typed-tool E2E") {
		t.Fatalf("script output missing PASS marker:\n%s", out)
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
