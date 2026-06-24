package sandbox

// SB2 container-launcher tests. The unit tests need no container engine:
// argv construction is golden-tested and Launch runs against a fake engine
// script. The real rootless-podman integration test is gated:
//
//	LOOM_SANDBOX_PODMAN_TEST=1 go test ./internal/driver -run TestContainerLauncherPodmanIntegration -v
//
// Requirements: rootless podman on PATH (on macOS a running podman machine
// with the default /Users + /var/folders shares); the first run pulls
// docker.io/library/node:22-slim. The container is capped (--memory/--cpus/
// --pids-limit defaults), per the fork-bomb runbook's capped-run recipe.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestResolveSandboxLauncher(t *testing.T) {
	cases := []struct {
		name      string
		mode      string
		container bool
		wantErr   bool
	}{
		{name: "default is process", mode: ""},
		{name: "process explicit", mode: "process"},
		{name: "container", mode: "container", container: true},
		{name: "container case-insensitive", mode: " Container ", container: true},
		{name: "unknown mode rejected", mode: "vm", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(SandboxModeEnvVar, tc.mode)
			launcher, err := ResolveSandboxLauncher()
			if tc.wantErr {
				if !errors.Is(err, domain.ErrInvalid) {
					t.Fatalf("err = %v, want domain.ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveSandboxLauncher: %v", err)
			}
			if got := launcher != nil; got != tc.container {
				t.Fatalf("launcher = %v, want container=%v", launcher, tc.container)
			}
		})
	}
}

func TestResolveSandboxLauncherReadsContainerConfigEnv(t *testing.T) {
	t.Setenv(SandboxModeEnvVar, "container")
	t.Setenv(SandboxBinaryEnvVar, "docker")
	t.Setenv(SandboxImageEnvVar, "registry.example.com/loom/sandbox:9")
	t.Setenv(SandboxRuntimeEnvVar, "runsc")
	t.Setenv(SandboxMemoryEnvVar, "2g")
	t.Setenv(SandboxCPUsEnvVar, "0.5")
	t.Setenv(SandboxPidsLimitEnvVar, "64")
	resolved, err := ResolveSandboxLauncher()
	if err != nil {
		t.Fatalf("ResolveSandboxLauncher: %v", err)
	}
	launcher, ok := resolved.(*containerLauncher)
	if !ok {
		t.Fatalf("launcher = %T, want *containerLauncher", resolved)
	}
	want := &containerLauncher{
		Binary:    "docker",
		Image:     "registry.example.com/loom/sandbox:9",
		Runtime:   "runsc",
		Memory:    "2g",
		CPUs:      "0.5",
		PidsLimit: 64,
	}
	if !reflect.DeepEqual(launcher, want) {
		t.Fatalf("launcher = %+v, want %+v", launcher, want)
	}
}

func TestContainerLauncherRunArgsGolden(t *testing.T) {
	spec := LaunchSpec{BundleRoot: "/work/bundles/v1", WorkDir: "/work/bundles/v1"}
	cases := []struct {
		name     string
		launcher *containerLauncher
		passKeys []string
		want     []string
	}{
		{
			name:     "defaults",
			launcher: &containerLauncher{Binary: "podman"},
			want: []string{
				"run", "--rm", "-i",
				"--name", "sbx-1",
				"--read-only",
				"--security-opt", "no-new-privileges",
				"--memory", "1g",
				"--cpus", "1.0",
				"--pids-limit", "256",
				"--mount", "type=bind,src=/work/bundles/v1,dst=/work/bundles/v1,ro",
				"--mount", "type=bind,src=/tmp/launcher.mjs,dst=/tmp/launcher.mjs,ro",
				"--workdir", "/work/bundles/v1",
				"--env-file", "/tmp/run.env",
				"docker.io/library/node:22-slim",
				"node", "/tmp/launcher.mjs",
			},
		},
		{
			name: "gvisor runtime custom caps and pass-keys",
			launcher: &containerLauncher{
				Binary:    "docker",
				Image:     "registry.example.com/loom/sandbox:9",
				Runtime:   "runsc",
				Memory:    "512m",
				CPUs:      "2",
				PidsLimit: 32,
			},
			passKeys: []string{"LOOM_FLUE_INVOKE_PAYLOAD", "EXTRA_KEY"},
			want: []string{
				"run", "--rm", "-i",
				"--name", "sbx-1",
				"--read-only",
				"--security-opt", "no-new-privileges",
				"--memory", "512m",
				"--cpus", "2",
				"--pids-limit", "32",
				"--mount", "type=bind,src=/work/bundles/v1,dst=/work/bundles/v1,ro",
				"--mount", "type=bind,src=/tmp/launcher.mjs,dst=/tmp/launcher.mjs,ro",
				"--workdir", "/work/bundles/v1",
				"--env-file", "/tmp/run.env",
				"--runtime", "runsc",
				"--env", "LOOM_FLUE_INVOKE_PAYLOAD",
				"--env", "EXTRA_KEY",
				"registry.example.com/loom/sandbox:9",
				"node", "/tmp/launcher.mjs",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.launcher.runArgs("sbx-1", spec, "/tmp/launcher.mjs", "/tmp/run.env", tc.passKeys, nil)
			if err != nil {
				t.Fatalf("runArgs: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("runArgs = %q\nwant     %q", got, tc.want)
			}
		})
	}
}

func TestContainerLauncherRunArgsRejectsCommaMountSource(t *testing.T) {
	launcher := &containerLauncher{Binary: "podman"}
	_, err := launcher.runArgs("sbx-1", LaunchSpec{BundleRoot: "/work/a,b"}, "/tmp/launcher.mjs", "/tmp/run.env", nil, nil)
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("err = %v, want domain.ErrInvalid for comma in mount source", err)
	}
}

func TestSplitContainerEnv(t *testing.T) {
	cases := []struct {
		name     string
		env      []string
		wantFile []string
		wantPass []string
		wantErr  bool
	}{
		{
			name:     "plain entries ride the env file",
			env:      []string{"A=1", `PAYLOAD={"summary":"a # b","price":"$10"}`},
			wantFile: []string{"A=1", `PAYLOAD={"summary":"a # b","price":"$10"}`},
		},
		{
			name:     "newline values pass via client env",
			env:      []string{"A=1", "NL=line1\nline2", "B=2"},
			wantFile: []string{"A=1", "B=2"},
			wantPass: []string{"NL=line1\nline2"},
		},
		{name: "missing separator rejected", env: []string{"BROKEN"}, wantErr: true},
		{name: "empty name rejected", env: []string{"=value"}, wantErr: true},
		{name: "name with hash rejected", env: []string{"BAD#NAME=1"}, wantErr: true},
		{name: "name with space rejected", env: []string{"BAD NAME=1"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fileEnv, passEnv, err := splitContainerEnv(tc.env)
			if tc.wantErr {
				if !errors.Is(err, domain.ErrInvalid) {
					t.Fatalf("err = %v, want domain.ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitContainerEnv: %v", err)
			}
			if !reflect.DeepEqual(fileEnv, tc.wantFile) || !reflect.DeepEqual(passEnv, tc.wantPass) {
				t.Fatalf("split = (%q, %q), want (%q, %q)", fileEnv, passEnv, tc.wantFile, tc.wantPass)
			}
		})
	}
}

func TestWriteContainerEnvFileExcludesParentEnv(t *testing.T) {
	t.Setenv("SANDBOX_ENVFILE_HOST_LEAK", "leaked-from-host")
	specEnv := []string{"LOOM_RUN_TOKEN=tok-123", `LOOM_FLUE_INVOKE_PAYLOAD={"q":"he said \"hi\""}`}
	path, err := writeContainerEnvFile(specEnv)
	if err != nil {
		t.Fatalf("writeContainerEnvFile: %v", err)
	}
	defer func() { _ = os.Remove(path) }()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat env file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("env file perm = %o, want 0600", perm)
	}
	content, err := os.ReadFile(path) //nolint:gosec // temp file created by this test.
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	want := strings.Join(specEnv, "\n") + "\n"
	if string(content) != want {
		t.Fatalf("env file content = %q, want exactly the spec env %q", content, want)
	}
}

func TestMergeEnvOverride(t *testing.T) {
	base := []string{"HOST_ONLY=keep", "SHARED=host-value", "SHARED=host-dup"}
	got := mergeEnvOverride(base, []string{"SHARED=spec-value", "SPEC_ONLY=new"})
	want := []string{"HOST_ONLY=keep", "SHARED=spec-value", "SPEC_ONLY=new"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeEnvOverride = %q, want %q (override replaces every duplicate)", got, want)
	}
	if before := []string{"A=1"}; !reflect.DeepEqual(mergeEnvOverride(before, nil), before) {
		t.Fatal("mergeEnvOverride without overrides must return base unchanged")
	}
}

// writeFakeEngine writes a stand-in container engine script: a "run" call
// captures argv, its own environment and a copy of the --env-file, then
// emits a terminal result frame; "rm" (the Kill/cleanup path) exits quietly.
func writeFakeEngine(t *testing.T, dir, captureDir, body string) string {
	t.Helper()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"rm\" ]; then exit 0; fi\n" +
		"out=\"" + captureDir + "\"\n" +
		"printf '%s\\n' \"$@\" > \"$out/argv\"\n" +
		"env > \"$out/clientenv\"\n" +
		"prev=\"\"\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"--env-file\" ]; then cp \"$a\" \"$out/envfile\"; fi\n" +
		"  prev=\"$a\"\n" +
		"done\n" +
		body
	return writeExecutable(t, dir, "fake-engine", script)
}

func TestContainerLauncherLaunchRoutesThroughFakeEngine(t *testing.T) {
	t.Setenv("SANDBOX_CONTAINER_HOST_LEAK", "leaked-from-host")
	captureDir := t.TempDir()
	bundleRoot := t.TempDir()
	engine := writeFakeEngine(t, t.TempDir(), captureDir,
		"printf '%s\\n' '{\"status\":\"completed\",\"summary\":\"fake container ok\"}'\n")
	launcher := &containerLauncher{Binary: engine}
	spec := LaunchSpec{
		BundleRoot: bundleRoot,
		Env: []string{
			"LOOM_RUN_TOKEN=secret-token-value",
			`LOOM_FLUE_INVOKE_PAYLOAD={"summary":"a # b"}`,
			"SANDBOX_MULTILINE=line1\nline2",
		},
	}
	process, err := launcher.Launch(context.Background(), spec)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	exit, err := process.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	assertFakeEngineResultAndPlacement(t, process, exit, bundleRoot)
	assertFakeEngineArgv(t, captureDir, bundleRoot)
	assertFakeEngineEnv(t, captureDir)
}

func assertFakeEngineResultAndPlacement(t *testing.T, process SandboxProcess, exit SandboxExit, bundleRoot string) {
	t.Helper()
	var frame SandboxFrame
	if err := json.Unmarshal([]byte(strings.TrimSpace(exit.Stdout)), &frame); err != nil {
		t.Fatalf("decode result frame from stdout %q: %v", exit.Stdout, err)
	}
	if frame.Status != "completed" || frame.Summary != "fake container ok" {
		t.Fatalf("result frame = %+v, want completed fake container ok", frame)
	}
	placement := process.Placement()
	if placement.Provider != SandboxProviderContainer {
		t.Fatalf("placement.Provider = %q, want %q", placement.Provider, SandboxProviderContainer)
	}
	if !strings.HasPrefix(placement.SandboxID, "loom-sandbox-") || placement.ImageOrSnapshot != DefaultSandboxImage {
		t.Fatalf("placement = %+v, want loom-sandbox-* id and default image", placement)
	}
	if placement.ProcessRef == "" || placement.CWD != bundleRoot || placement.StartedAt.IsZero() {
		t.Fatalf("placement = %+v, want client pid, bundle-root cwd and started-at", placement)
	}
}

func assertFakeEngineArgv(t *testing.T, captureDir, bundleRoot string) {
	t.Helper()
	argvRaw, err := os.ReadFile(filepath.Join(captureDir, "argv")) //nolint:gosec // test capture file.
	if err != nil {
		t.Fatalf("read captured argv: %v", err)
	}
	argv := string(argvRaw)
	for _, want := range []string{
		"--read-only\n",
		"--memory\n" + DefaultSandboxMemory + "\n",
		"--cpus\n" + DefaultSandboxCPUs + "\n",
		"--pids-limit\n256\n",
		"type=bind,src=" + bundleRoot + ",dst=" + bundleRoot + ",ro\n",
		"--env\nSANDBOX_MULTILINE\n",
		DefaultSandboxImage + "\nnode\n",
	} {
		if !strings.Contains(argv, want) {
			t.Fatalf("argv missing %q:\n%s", want, argv)
		}
	}
	// No env value ever rides argv — only the env-file path and bare names.
	for _, secret := range []string{"secret-token-value", "line1", `a # b`} {
		if strings.Contains(argv, secret) {
			t.Fatalf("argv leaks env value %q:\n%s", secret, argv)
		}
	}
	envFilePath := capturedFlagValue(argv, "--env-file")
	if envFilePath == "" {
		t.Fatalf("argv missing --env-file:\n%s", argv)
	}
	if _, err := os.Stat(envFilePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("env file %q still exists after Wait (stat err = %v), want deleted", envFilePath, err)
	}
}

func assertFakeEngineEnv(t *testing.T, captureDir string) {
	t.Helper()
	envFile, err := os.ReadFile(filepath.Join(captureDir, "envfile")) //nolint:gosec // test capture file.
	if err != nil {
		t.Fatalf("read captured env file: %v", err)
	}
	want := "LOOM_RUN_TOKEN=secret-token-value\n" + `LOOM_FLUE_INVOKE_PAYLOAD={"summary":"a # b"}` + "\n"
	if string(envFile) != want {
		t.Fatalf("env file = %q, want exactly the newline-free spec env %q (no parent env)", envFile, want)
	}
	clientEnv, err := os.ReadFile(filepath.Join(captureDir, "clientenv")) //nolint:gosec // test capture file.
	if err != nil {
		t.Fatalf("read captured client env: %v", err)
	}
	if !strings.Contains(string(clientEnv), "SANDBOX_MULTILINE=line1") {
		t.Fatal("client env missing the newline-value pass-through entry")
	}
}

func capturedFlagValue(argvLines, flag string) string {
	lines := strings.Split(argvLines, "\n")
	for i, line := range lines {
		if line == flag && i+1 < len(lines) {
			return lines[i+1]
		}
	}
	return ""
}

func TestContainerSandboxKillTerminatesClient(t *testing.T) {
	captureDir := t.TempDir()
	engine := writeFakeEngine(t, t.TempDir(), captureDir, "exec sleep 30\n")
	process, err := (&containerLauncher{Binary: engine}).Launch(context.Background(), LaunchSpec{
		BundleRoot: t.TempDir(),
		Env:        []string{"A=1"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, waitErr := process.Wait()
		done <- waitErr
	}()
	select {
	case waitErr := <-done:
		if waitErr == nil {
			t.Fatal("Wait err = nil, want kill-induced exit error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Wait did not return after Kill")
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("second Kill after exit: %v", err)
	}
}

func TestContainerLauncherStartFailureSurfacesFromWait(t *testing.T) {
	launcher := &containerLauncher{Binary: filepath.Join(t.TempDir(), "missing-engine")}
	process, err := launcher.Launch(context.Background(), LaunchSpec{BundleRoot: t.TempDir(), Env: []string{"A=1"}})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if !process.Placement().Empty() {
		t.Fatalf("placement = %+v, want empty for a runtime that never started", process.Placement())
	}
	if _, waitErr := process.Wait(); waitErr == nil {
		t.Fatal("Wait err = nil, want start failure (process-launcher parity)")
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("Kill after start failure: %v", err)
	}
}

// integrationEchoServer is the trivial IPC bundle server: on invoke it echoes
// the probes the test cares about (env visibility, host FS reachability,
// bundle writability, payload round-trip) through the result frame summary.
const integrationEchoServer = `
import { readFileSync, writeFileSync } from 'node:fs';
process.on('message', (message) => {
  if (!message || message.type !== 'invoke') return;
  let hostProbe = 'unreadable';
  try { readFileSync(process.env.SANDBOX_HOST_PROBE, 'utf8'); hostProbe = 'readable'; } catch {}
  let bundleWrite = 'denied';
  try { writeFileSync(process.env.LOOM_FLUE_BUNDLE_ROOT + '/intruder.txt', 'x'); bundleWrite = 'allowed'; } catch {}
  const summary = JSON.stringify({
    marker: process.env.SANDBOX_MARKER || '',
    leak: process.env.SANDBOX_CONTAINER_IT_HOST_LEAK || '',
    multiline: process.env.SANDBOX_MULTILINE || '',
    payload: message.payload,
    hostProbe,
    bundleWrite,
  });
  process.send({ type: 'result', result: { status: 'completed', summary } });
});
process.send({ type: 'ready' });
`

// TestContainerLauncherPodmanIntegration exercises the real rootless-podman
// path end to end. Gated: set LOOM_SANDBOX_PODMAN_TEST=1 to run (see the
// file header for the recipe). The funky invoke payload pins the env-file
// verbatim contract (no quote/comment/$ processing by the engine).
func TestContainerLauncherPodmanIntegration(t *testing.T) {
	if os.Getenv("LOOM_SANDBOX_PODMAN_TEST") != "1" {
		t.Skip("set LOOM_SANDBOX_PODMAN_TEST=1 to run the rootless-podman sandbox integration test")
	}
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not installed")
	}
	t.Setenv("SANDBOX_CONTAINER_IT_HOST_LEAK", "leaked-from-host")
	bundleRoot := t.TempDir()
	serverPath := filepath.Join(bundleRoot, "dist", "server.mjs")
	if err := os.MkdirAll(filepath.Dir(serverPath), 0o750); err != nil {
		t.Fatalf("mkdir bundle dist: %v", err)
	}
	if err := os.WriteFile(serverPath, []byte(integrationEchoServer), 0o600); err != nil {
		t.Fatalf("write bundle server: %v", err)
	}
	hostProbe := filepath.Join(t.TempDir(), "host-only.txt")
	if err := os.WriteFile(hostProbe, []byte("host secret"), 0o600); err != nil {
		t.Fatalf("write host probe: %v", err)
	}
	payload := `{"ping":"pong # $not_expanded \"quoted\""}`
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	process, err := (&containerLauncher{Binary: "podman"}).Launch(ctx, LaunchSpec{
		BundleRoot: bundleRoot,
		ServerPath: serverPath,
		Env: []string{
			"PATH=/usr/local/bin:/usr/bin:/bin",
			"LOOM_FLUE_SERVER_PATH=" + serverPath,
			"LOOM_FLUE_BUNDLE_ROOT=" + bundleRoot,
			"LOOM_FLUE_WORKFLOW_NAME=echo-env",
			"LOOM_FLUE_INVOKE_PAYLOAD=" + payload,
			"LOOM_DRIVER_RUN_ID=run-sandbox-it",
			"SANDBOX_MARKER=present",
			"SANDBOX_HOST_PROBE=" + hostProbe,
			"SANDBOX_MULTILINE=line1\nline2",
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	exit, err := process.Wait()
	if err != nil {
		t.Fatalf("Wait: %v (stderr: %s)", err, exit.Stderr)
	}
	assertPodmanIntegrationResult(t, process, exit, payload)
	if _, err := os.Stat(filepath.Join(bundleRoot, "intruder.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bundle intruder.txt stat err = %v, want not-exist (read-only mount)", err)
	}
}

func assertPodmanIntegrationResult(t *testing.T, process SandboxProcess, exit SandboxExit, payload string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(exit.Stdout), "\n")
	var frame SandboxFrame
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &frame); err != nil {
		t.Fatalf("decode result frame from stdout %q: %v (stderr: %s)", exit.Stdout, err, exit.Stderr)
	}
	if frame.Status != "completed" {
		t.Fatalf("result frame = %+v, want completed (stderr: %s)", frame, exit.Stderr)
	}
	var probes struct {
		Marker      string          `json:"marker"`
		Leak        string          `json:"leak"`
		Multiline   string          `json:"multiline"`
		Payload     json.RawMessage `json:"payload"`
		HostProbe   string          `json:"hostProbe"`
		BundleWrite string          `json:"bundleWrite"`
	}
	if err := json.Unmarshal([]byte(frame.Summary), &probes); err != nil {
		t.Fatalf("decode probe summary %q: %v", frame.Summary, err)
	}
	if probes.Marker != "present" || probes.Leak != "" {
		t.Fatalf("probes = %+v, want spec env visible and zero host env leak", probes)
	}
	if probes.Multiline != "line1\nline2" {
		t.Fatalf("multiline env = %q, want newline value intact via the pass-through path", probes.Multiline)
	}
	var wantPayload, gotPayload any
	if err := json.Unmarshal([]byte(payload), &wantPayload); err != nil {
		t.Fatalf("decode want payload: %v", err)
	}
	if err := json.Unmarshal(probes.Payload, &gotPayload); err != nil {
		t.Fatalf("decode echoed payload %s: %v", probes.Payload, err)
	}
	if !reflect.DeepEqual(gotPayload, wantPayload) {
		t.Fatalf("payload round-trip = %s, want %s (env-file must be verbatim)", probes.Payload, payload)
	}
	if probes.HostProbe != "unreadable" {
		t.Fatal("host-only file was readable inside the container sandbox")
	}
	if probes.BundleWrite != "denied" {
		t.Fatal("bundle mount was writable inside the container sandbox, want read-only")
	}
	if got := process.Placement(); got.Provider != SandboxProviderContainer || got.ImageOrSnapshot != DefaultSandboxImage {
		t.Fatalf("placement = %+v, want container provider with default image", got)
	}
}
