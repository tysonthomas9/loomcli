package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEpicRunnerPodmanHarnessRuntimeContract(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	dockerfile := mustReadHarnessFile(t, filepath.Join(root, "e2e", "Dockerfile"))
	runner := mustReadHarnessFile(t, filepath.Join(root, "e2e", "run_epic_runner_codex_podman.sh"))
	scenario := mustReadHarnessFile(t, filepath.Join(root, "e2e", "epic_runner_codex.sh"))
	codexStub := mustReadHarnessFile(t, filepath.Join(root, "e2e", "stubs", "codex"))

	for _, required := range []string{
		"FROM node:22-alpine AS flue-toolchain",
		"COPY --from=flue packages/cli/",
		"LOOM_SDK_ROOT=/src/sdk",
		"LOOM_FLUE_RUNTIME_ROOT=/opt/flue/packages/runtime",
		"DAYTONA_SDK_ROOT=/opt/flue/examples/hello-world/node_modules/@daytona/sdk",
		"LOOM_REAL_FLUE_CMD_JSON",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("e2e/Dockerfile missing %q", required)
		}
	}

	for _, required := range []string{
		"--build-context \"flue=$FLUE_REPO\"",
		"internal/workflows/FLUE_COMMIT",
	} {
		if !strings.Contains(runner, required) {
			t.Errorf("run_epic_runner_codex_podman.sh missing %q", required)
		}
	}

	if strings.Contains(scenario, "--role task") {
		t.Error("epic runner E2E still passes removed --role task flag")
	}
	for _, required := range []string{"loom serve --port", "--detach"} {
		if !strings.Contains(scenario, required) {
			t.Errorf("epic runner E2E missing v5 driver API runtime %q", required)
		}
	}
	agentDefinition := strings.Index(scenario, "loom agentdef add nova")
	daemonStart := strings.Index(scenario, "loom daemon >")
	if agentDefinition < 0 || daemonStart < 0 || agentDefinition > daemonStart {
		t.Error("epic runner E2E must configure its lead before starting the daemon")
	}
	if !strings.Contains(scenario, "OPENAI_API_KEY") {
		t.Error("epic runner E2E must satisfy local Codex preflight while using the stub CLI")
	}
	for _, stale := range []string{"loom push \"$agent_name\"", "loom data close \"$task_id\"", "loom complete"} {
		if strings.Contains(codexStub, stale) {
			t.Errorf("epic runner Codex stub bypasses v5 driver completion with %q", stale)
		}
	}
}

func TestCodexStubRecognizesEpicRunnerDirectivesFromPrompt(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	mustWriteHarnessFile(t, filepath.Join(repo, "Makefile"), ".PHONY: gate\ngate:\n\t@test -f README.md\n")
	mustWriteHarnessFile(t, filepath.Join(repo, "README.md"), "# fixture\n")
	runHarnessCommand(t, repo, "git", "init", "-b", "main")
	runHarnessCommand(t, repo, "git", "add", "README.md", "Makefile")
	runHarnessCommand(t, repo, "git", "-c", "user.name=Loom E2E", "-c", "user.email=loom@example.test", "commit", "-m", "fixture")

	invocations := filepath.Join(t.TempDir(), "invocations.log")
	stub := filepath.Join(repoRoot(t), "e2e", "stubs", "codex")
	prompt := "Task ID: TEST-2\nSTUB_CODEX_WRITE=epic-runner-output/task-a.txt\n"
	cmd := exec.CommandContext(t.Context(), stub, "exec", "--json") //nolint:norawexec,gosec // repository-owned E2E stub
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = append(os.Environ(),
		"LOOM_TASK_ID=TEST-2",
		"STUB_CODEX_EPIC_RUNNER=0",
		"STUB_CODEX_INVOCATIONS="+invocations,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run Codex E2E stub: %v\n%s", err, output)
	}

	output := mustReadHarnessFile(t, filepath.Join(repo, "epic-runner-output", "task-a.txt"))
	if !strings.Contains(output, "task=TEST-2") {
		t.Fatalf("stub output = %q, want task directive from prompt", output)
	}
	if got := mustReadHarnessFile(t, invocations); !strings.Contains(got, "task=TEST-2") {
		t.Fatalf("stub invocation log = %q", got)
	}
}

func mustReadHarnessFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func mustWriteHarnessFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runHarnessCommand(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...) //nolint:norawexec,gosec // fixed test fixture commands
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run %s %v: %v\n%s", name, args, err, output)
	}
}
