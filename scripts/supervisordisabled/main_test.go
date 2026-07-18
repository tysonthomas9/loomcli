package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

const validRedMatrix = `schema_version: 1
suite: supervisor-disabled
rows:
  - id: deterministic-plan-coder
    phase: execution
    state: red
    owner: execution-reliability-lane
    blocker: deterministic TS execution is not available
    coordinates:
      depth: end-to-end
      realness: deterministic
      provisioning: compose
      polarity: positive-and-negative
      target: loom-serve-ts-plane
    env:
      LOOM_LOCAL_MODE_PLANE: "ts"
      LOOM_TASK_READY_EVENTS: "1"
      LOCAL_MODE_COMPOSE_FILES: test/local-mode/docker-compose.workflow-build.yml
      LOCAL_MODE_COMPOSE_UP_FLAGS: --build -d
    setup:
      - id: clean-project
        argv: [make, local-mode-down]
        timeout_seconds: 180
      - id: workflow-toolchain
        argv: [make, local-mode-workflow-build-check]
        timeout_seconds: 120
      - id: stack
        argv: [make, local-mode-up]
        timeout_seconds: 900
    verify:
      - id: deterministic-evidence
        argv: [make, local-mode-verify]
        timeout_seconds: 300
    teardown:
      - id: cleanup-stack
        argv: [make, local-mode-down]
        timeout_seconds: 180
    assertions:
      - local-mode-plane-ts
      - task-ready-events-enabled
      - zero-auto-agentdefs
      - zero-daemon-processes
      - zero-daemon-sockets
      - public-api-plan-agent
      - public-api-task-agent
      - planner-review-design
      - coder-completion
      - planner-transcript
      - coder-transcript
      - coder-diff
`

type fakeExecutor struct {
	calls []string
	errs  map[string]error
}

func (f *fakeExecutor) Run(_ context.Context, _ string, command step, _ map[string]string, _, _ io.Writer) error {
	f.calls = append(f.calls, command.ID)
	return f.errs[command.ID]
}

func TestCheckedInMatrixIsValid(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	m, err := loadMatrix(filepath.Join(root, defaultManifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMatrix(m); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMatrixRejectsUnknownFields(t *testing.T) {
	path := writeMatrix(t, strings.Replace(validRedMatrix, "suite: supervisor-disabled", "suite: supervisor-disabled\nunknown: true", 1))
	_, err := loadMatrix(path)
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("loadMatrix error = %v, want strict unknown-field failure", err)
	}
}

func TestValidateMatrixRejectsMissingRequiredContract(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "wrong plane",
			content: strings.Replace(validRedMatrix, `LOOM_LOCAL_MODE_PLANE: "ts"`, `LOOM_LOCAL_MODE_PLANE: "go"`, 1),
			want:    "LOOM_LOCAL_MODE_PLANE must be \"ts\"",
		},
		{
			name:    "missing assertion",
			content: strings.Replace(validRedMatrix, "      - zero-daemon-sockets\n", "", 1),
			want:    `missing required assertion "zero-daemon-sockets"`,
		},
		{
			name:    "unknown assertion",
			content: strings.Replace(validRedMatrix, "      - coder-diff\n", "      - coder-diff\n      - typo-assertion\n", 1),
			want:    `unsupported assertion "typo-assertion"`,
		},
		{
			name:    "red without blocker",
			content: strings.Replace(validRedMatrix, "    blocker: deterministic TS execution is not available\n", "", 1),
			want:    "declared red requires a blocker",
		},
		{
			name:    "duplicate step id",
			content: strings.Replace(validRedMatrix, "      - id: cleanup-stack", "      - id: clean-project", 1),
			want:    `duplicate step id "clean-project"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := loadMatrix(writeMatrix(t, tt.content))
			if err != nil {
				t.Fatal(err)
			}
			err = validateMatrix(m)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateMatrix error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateMatrixPinsAuthoritativeRowContract(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "required row missing",
			content: strings.Replace(validRedMatrix, "id: deterministic-plan-coder", "id: replacement-row", 1),
			want:    `required authoritative row "deterministic-plan-coder" is missing`,
		},
		{
			name:    "setup command weakened",
			content: strings.Replace(validRedMatrix, "argv: [make, local-mode-up]", "argv: [true]", 1),
			want:    "setup[2] contract drifted",
		},
		{
			name:    "compose profile removed",
			content: strings.Replace(validRedMatrix, "      LOCAL_MODE_COMPOSE_FILES: test/local-mode/docker-compose.workflow-build.yml\n", "", 1),
			want:    "env contract has 3 entries, want exactly 4",
		},
		{
			name:    "extra environment override",
			content: strings.Replace(validRedMatrix, "      LOOM_LOCAL_MODE_PLANE: \"ts\"\n", "      LOOM_LOCAL_MODE_PLANE: \"ts\"\n      PATH: /tmp/untrusted\n", 1),
			want:    `env key "PATH" is not supported by the clean-environment contract`,
		},
		{
			name:    "compose project override added",
			content: strings.Replace(validRedMatrix, "      LOOM_LOCAL_MODE_PLANE: \"ts\"\n", "      LOOM_LOCAL_MODE_PLANE: \"ts\"\n      LOCAL_MODE_COMPOSE_PROJECT: unrelated-stack\n", 1),
			want:    `env key "LOCAL_MODE_COMPOSE_PROJECT" is not supported by the clean-environment contract`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := loadMatrix(writeMatrix(t, tt.content))
			if err != nil {
				t.Fatal(err)
			}
			err = validateMatrix(m)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateMatrix error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateRowRejectsMalformedExplicitEnvironment(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "invalid key", key: "BAD=KEY", value: "value", want: "without '=' or NUL"},
		{name: "NUL value", key: "VALID_KEY", value: "bad\x00value", want: "value must not contain NUL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := mustLoadMatrix(t, validRedMatrix)
			m.Rows[0].Env[test.key] = test.value
			err := validateRow(m.Rows[0])
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateRow error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateOnlyAcceptsDeclaredRedWithoutExecuting(t *testing.T) {
	path := writeMatrix(t, validRedMatrix)
	executor := &fakeExecutor{}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--manifest", path, "--validate"}, &out, io.Discard, executor); err != nil {
		t.Fatal(err)
	}
	if len(executor.calls) != 0 {
		t.Fatalf("validate-only executed commands: %v", executor.calls)
	}
	if got, want := out.String(), "[supervisor-disabled] validation ok suite=supervisor-disabled rows=1\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestDeclaredRedFailsProofWithoutProvisioning(t *testing.T) {
	path := writeMatrix(t, validRedMatrix)
	executor := &fakeExecutor{}
	var out bytes.Buffer
	err := run(context.Background(), []string{"--manifest", path}, &out, io.Discard, executor)
	if !errors.Is(err, errProofFailed) {
		t.Fatalf("run error = %v, want errProofFailed", err)
	}
	if len(executor.calls) != 0 {
		t.Fatalf("declared-red row executed commands: %v", executor.calls)
	}
	want := "[supervisor-disabled] RED row=deterministic-plan-coder owner=execution-reliability-lane\n" +
		"[supervisor-disabled] blocker=deterministic TS execution is not available\n" +
		"[supervisor-disabled] setup=not-run verify=not-run reason=declared-red\n" +
		"[supervisor-disabled] summary green=0 red=1 failed=0 total=1\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestGreenRowRunsEveryTeardownAfterVerifyFailure(t *testing.T) {
	m := mustLoadMatrix(t, strings.Replace(validRedMatrix,
		"    state: red\n    owner: execution-reliability-lane\n    blocker: deterministic TS execution is not available\n",
		"    state: green\n    owner: execution-reliability-lane\n", 1))
	m.Rows[0].Teardown = append(m.Rows[0].Teardown, step{ID: "teardown-two", Argv: []string{"make", "local-mode-down"}, TimeoutSeconds: 1})
	executor := &fakeExecutor{errs: map[string]error{
		"deterministic-evidence": errors.New("verification failed"),
		"cleanup-stack":          errors.New("first cleanup failed"),
	}}
	var out bytes.Buffer
	err := executeMatrix(context.Background(), m, t.TempDir(), &out, io.Discard, executor)
	if !errors.Is(err, errProofFailed) {
		t.Fatalf("executeMatrix error = %v, want errProofFailed", err)
	}
	if got, want := strings.Join(executor.calls, ","), "clean-project,workflow-toolchain,stack,deterministic-evidence,cleanup-stack,teardown-two"; got != want {
		t.Fatalf("execution order = %q, want %q", got, want)
	}
	if !strings.Contains(out.String(), "FAIL row=deterministic-plan-coder stage=verify step=deterministic-evidence") {
		t.Fatalf("missing stable verify failure: %s", out.String())
	}
	if !strings.Contains(out.String(), "PASS row=deterministic-plan-coder stage=teardown step=teardown-two") {
		t.Fatalf("second teardown did not run: %s", out.String())
	}
}

func TestGreenRowRunsTeardownAfterTimeout(t *testing.T) {
	m := mustLoadMatrix(t, strings.Replace(validRedMatrix,
		"    state: red\n    owner: execution-reliability-lane\n    blocker: deterministic TS execution is not available\n",
		"    state: green\n    owner: execution-reliability-lane\n", 1))
	executor := &fakeExecutor{errs: map[string]error{"clean-project": context.DeadlineExceeded}}
	var out bytes.Buffer
	err := executeMatrix(context.Background(), m, t.TempDir(), &out, io.Discard, executor)
	if !errors.Is(err, errProofFailed) {
		t.Fatalf("executeMatrix error = %v, want errProofFailed", err)
	}
	if got, want := strings.Join(executor.calls, ","), "clean-project,cleanup-stack"; got != want {
		t.Fatalf("execution order = %q, want %q", got, want)
	}
	if !strings.Contains(out.String(), "reason=timeout timeout_seconds=1") {
		t.Fatalf("missing stable timeout failure: %s", out.String())
	}
}

func TestOSExecutorTimeoutKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group assertion is Unix-specific")
	}
	marker := filepath.Join(t.TempDir(), "leaked-child")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := (osExecutor{}).Run(ctx, "", step{
		ID:   "timeout",
		Argv: []string{"sh", "-c", `(sleep 0.2; : > "$SUPERVISOR_DISABLED_MARKER") & wait`},
	}, map[string]string{"SUPERVISOR_DISABLED_MARKER": marker}, io.Discard, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want DeadlineExceeded", err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("timed-out command leaked a child process; marker stat error = %v", err)
	}
}

func TestOSExecutorStripsAmbientRuntimeSelectors(t *testing.T) {
	ambient := map[string]string{
		"AWS_SECRET_ACCESS_KEY":       "ambient-secret",
		"COMPOSE_PROJECT_NAME":        "ambient-compose-project",
		"CONTAINER_HOST":              "unix:///ambient/podman.sock",
		"DOCKER_HOST":                 "tcp://ambient-docker:2375",
		"LOCAL_MODE_API_PORT":         "61991",
		"LOCAL_MODE_CHECKOUT_ID":      "ambient-checkout",
		"LOCAL_MODE_COMPOSE":          "ambient compose",
		"LOCAL_MODE_COMPOSE_FILES":    "/tmp/ambient-compose.yml",
		"LOCAL_MODE_COMPOSE_PROJECT":  "ambient-local-mode",
		"LOCAL_MODE_COMPOSE_UP_FLAGS": "--no-build",
		"LOCAL_MODE_FLEETDB_PORT":     "61990",
		"LOCAL_MODE_RUN_ID":           "ambient-run",
		"LOCAL_MODE_SOURCE_ROOT":      "/tmp/ambient-source",
		"LOCAL_MODE_UI_PORT":          "61992",
		"LOOM_CONFIG_DIR":             "/tmp/ambient-loom-config",
		"LOOM_LOCAL_MODE_PLANE":       "go",
		"LOOM_TASK_READY_EVENTS":      "0",
		"LOOM_WORKSPACE":              "AMBIENT",
	}
	for key, value := range ambient {
		t.Setenv(key, value)
	}
	t.Setenv("PATH", "/ambient/bin")
	t.Setenv("HOME", "/ambient/home")

	explicit := map[string]string{
		"SUPERVISOR_DISABLED_ENV_PROBE": "1",
		"LOOM_LOCAL_MODE_PLANE":         "ts",
		"LOOM_TASK_READY_EVENTS":        "1",
		"LOCAL_MODE_COMPOSE_FILES":      "test/local-mode/docker-compose.workflow-build.yml",
		"LOCAL_MODE_COMPOSE_UP_FLAGS":   "--build -d",
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	err = (osExecutor{}).Run(context.Background(), workDir, step{
		ID:   "environment-probe",
		Argv: []string{executable, "-test.run=^TestSupervisorDisabledEnvironmentProbe$"},
	}, explicit, &stdout, &stderr)
	if err != nil {
		t.Fatalf("environment probe failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	got := parseEnvironmentProbe(stdout.String())
	for key, want := range explicit {
		if value := got[key]; value != want {
			t.Fatalf("explicit environment %s = %q, want %q; child env = %v", key, value, want, got)
		}
	}
	for _, key := range []string{
		"AWS_SECRET_ACCESS_KEY", "COMPOSE_PROJECT_NAME", "CONTAINER_HOST", "DOCKER_HOST",
		"LOCAL_MODE_API_PORT", "LOCAL_MODE_CHECKOUT_ID", "LOCAL_MODE_COMPOSE",
		"LOCAL_MODE_COMPOSE_PROJECT", "LOCAL_MODE_FLEETDB_PORT", "LOCAL_MODE_RUN_ID",
		"LOCAL_MODE_SOURCE_ROOT", "LOCAL_MODE_UI_PORT", "LOOM_CONFIG_DIR", "LOOM_WORKSPACE",
	} {
		if value, ok := got[key]; ok {
			t.Fatalf("ambient runtime selector %s leaked to executor command with value %q", key, value)
		}
	}
	if got["PATH"] != "/ambient/bin" || got["HOME"] != "/ambient/home" {
		t.Fatalf("host allowlist was not preserved: PATH=%q HOME=%q", got["PATH"], got["HOME"])
	}
	if got["PWD"] != workDir {
		t.Fatalf("runner-owned PWD = %q, want %q", got["PWD"], workDir)
	}
}

func TestProofEnvironmentOverridesAreExplicitAndValidated(t *testing.T) {
	sourceRoot := t.TempDir()
	got, err := proofEnvironmentOverrides(sourceRoot, "4380", "4382", "4383")
	if err != nil {
		t.Fatalf("proofEnvironmentOverrides: %v", err)
	}
	want := map[string]string{
		"LOCAL_MODE_FLEETDB_SOURCE_ROOT": sourceRoot,
		"LOCAL_MODE_FLEETDB_PORT":        "4380",
		"LOCAL_MODE_API_PORT":            "4382",
		"LOCAL_MODE_UI_PORT":             "4383",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overrides = %#v, want %#v", got, want)
	}

	for _, test := range []struct {
		name           string
		fleet, api, ui string
	}{
		{name: "invalid", fleet: "abc", api: "4382", ui: "4383"},
		{name: "privileged", fleet: "80", api: "4382", ui: "4383"},
		{name: "duplicate", fleet: "4380", api: "4380", ui: "4383"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := proofEnvironmentOverrides(sourceRoot, test.fleet, test.api, test.ui); err == nil {
				t.Fatal("proofEnvironmentOverrides returned nil error")
			}
		})
	}
}

func TestSupervisorDisabledEnvironmentProbe(t *testing.T) {
	if os.Getenv("SUPERVISOR_DISABLED_ENV_PROBE") != "1" {
		return
	}
	for _, entry := range os.Environ() {
		fmt.Println(entry)
	}
}

func parseEnvironmentProbe(output string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}

func writeMatrix(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "matrix.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustLoadMatrix(t *testing.T, content string) matrix {
	t.Helper()
	m, err := loadMatrix(writeMatrix(t, content))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMatrix(m); err != nil {
		t.Fatal(err)
	}
	return m
}
