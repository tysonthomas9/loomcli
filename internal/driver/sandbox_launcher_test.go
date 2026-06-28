//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestProcessLauncherPassesLaunchSpecEnvVerbatim(t *testing.T) {
	t.Setenv("SANDBOX_LAUNCHER_LEAK_PROBE", "leaked-from-host")
	root := t.TempDir()
	nodePath := writeExecutable(t, root, "fake-node",
		"#!/bin/sh\nprintf 'probe=%s leak=%s\\n' \"$SANDBOX_LAUNCHER_SPEC_PROBE\" \"$SANDBOX_LAUNCHER_LEAK_PROBE\"\n")
	process, err := processLauncher{NodePath: nodePath}.Launch(context.Background(), LaunchSpec{
		BundleRoot: root,
		Env:        []string{"SANDBOX_LAUNCHER_SPEC_PROBE=from-spec"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	exit, err := process.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := strings.TrimSpace(exit.Stdout); got != "probe=from-spec leak=" {
		t.Fatalf("runtime env = %q, want spec env verbatim with no host inheritance", got)
	}
}

func TestProcessLauncherKillTerminatesRuntime(t *testing.T) {
	root := t.TempDir()
	nodePath := writeExecutable(t, root, "fake-node", "#!/bin/sh\nexec sleep 30\n")
	process, err := processLauncher{NodePath: nodePath}.Launch(context.Background(), LaunchSpec{
		BundleRoot: root,
		Env:        []string{},
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

func TestProcessLauncherRecordsProcessPlacement(t *testing.T) {
	root := t.TempDir()
	nodePath := writeExecutable(t, root, "fake-node", "#!/bin/sh\nexit 0\n")
	process, err := processLauncher{NodePath: nodePath}.Launch(context.Background(), LaunchSpec{
		BundleRoot: root,
		WorkDir:    root,
		Env:        []string{},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	placement := process.Placement()
	if placement.Provider != SandboxProviderProcess {
		t.Fatalf("placement.Provider = %q, want %q", placement.Provider, SandboxProviderProcess)
	}
	if placement.ProcessRef == "" || placement.CWD != root || placement.StartedAt.IsZero() {
		t.Fatalf("placement = %+v, want pid ref, cwd %q and started-at", placement, root)
	}
	if _, err := process.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestProcessLauncherStartFailureSurfacesFromWait(t *testing.T) {
	process, err := processLauncher{NodePath: filepath.Join(t.TempDir(), "missing-node")}.Launch(context.Background(), LaunchSpec{
		BundleRoot: t.TempDir(),
		Env:        []string{},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if !process.Placement().Empty() {
		t.Fatalf("placement = %+v, want empty for a runtime that never started", process.Placement())
	}
	if _, waitErr := process.Wait(); waitErr == nil {
		t.Fatal("Wait err = nil, want start failure (pre-seam cmd.Run parity)")
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("Kill after start failure: %v", err)
	}
}

type stubSandboxProcess struct {
	exit      SandboxExit
	err       error
	placement domain.TaskRunPlacement
}

func (s *stubSandboxProcess) Wait() (SandboxExit, error)         { return s.exit, s.err }
func (s *stubSandboxProcess) Kill() error                        { return nil }
func (s *stubSandboxProcess) Placement() domain.TaskRunPlacement { return s.placement }

type stubSandboxLauncher struct {
	spec    LaunchSpec
	process SandboxProcess
	err     error
}

func (s *stubSandboxLauncher) Launch(_ context.Context, spec LaunchSpec) (SandboxProcess, error) {
	s.spec = spec
	if s.err != nil {
		return nil, s.err
	}
	return s.process, nil
}

func sandboxSeamRunRequest(root string) RunRequest {
	return RunRequest{
		Run: &domain.DriverRun{
			WorkspaceKey:    "TEST",
			RunID:           "run-seam",
			NodeID:          "node-1",
			LeaseID:         "lease-1",
			FencingToken:    7,
			Payload:         json.RawMessage(`{"hello":"sandbox"}`),
			DriverID:        "driver-1",
			DriverVersionID: "version-1",
		},
		BundleRoot: root,
		ServerPath: filepath.Join(root, "dist", "server.mjs"),
		Manifest:   map[string]string{"workflow_name": "epic-runner"},
		// Seam tests exercise launch mechanics, not the SB3 trust gate (see
		// sandbox_policy_test.go): a trusted request passes every launcher.
		TrustLevel: domain.DriverTrustTrusted,
	}
}

func TestNodeRunnerRoutesThroughInjectedSandboxLauncher(t *testing.T) {
	root := t.TempDir()
	launcher := &stubSandboxLauncher{process: &stubSandboxProcess{
		exit:      SandboxExit{Stdout: `{"status":"completed","summary":"container ok"}` + "\n"},
		placement: domain.TaskRunPlacement{Provider: "container", ImageOrSnapshot: "loom-sandbox:1"},
	}}
	result, err := (NodeRunner{Launcher: launcher}).Run(context.Background(), sandboxSeamRunRequest(root))
	if err != nil {
		t.Fatalf("NodeRunner.Run: %v", err)
	}
	if result.Status != domain.DriverRunCompleted || result.Summary != "container ok" {
		t.Fatalf("result = %+v, want completed container ok", result)
	}
	spec := launcher.spec
	if spec.BundleRoot != root || spec.WorkDir != root || spec.ServerPath != filepath.Join(root, "dist", "server.mjs") {
		t.Fatalf("spec = %+v, want bundle root, work dir and server path from the run request", spec)
	}
	if spec.Manifest["workflow_name"] != "epic-runner" {
		t.Fatalf("spec.Manifest = %+v, want run request manifest", spec.Manifest)
	}
	wantEnv := []string{
		"LOOM_DRIVER_RUN_ID=run-seam",
		`LOOM_FLUE_INVOKE_PAYLOAD={"hello":"sandbox"}`,
		"LOOM_FLUE_WORKFLOW_NAME=epic-runner",
	}
	for _, entry := range wantEnv {
		found := false
		for _, got := range spec.Env {
			if got == entry {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("spec.Env missing %q (env = %v)", entry, spec.Env)
		}
	}
	var placement domain.TaskRunPlacement
	if err := json.Unmarshal([]byte(result.Output[SandboxPlacementOutputKey]), &placement); err != nil {
		t.Fatalf("decode %s output: %v (output = %+v)", SandboxPlacementOutputKey, err, result.Output)
	}
	if placement.Provider != "container" || placement.ImageOrSnapshot != "loom-sandbox:1" {
		t.Fatalf("recorded placement = %+v, want the launcher's container descriptor", placement)
	}
}

func TestNodeRunnerSurfacesSandboxLaunchError(t *testing.T) {
	launcher := &stubSandboxLauncher{err: fmt.Errorf("podman not installed")}
	_, err := (NodeRunner{Launcher: launcher}).Run(context.Background(), sandboxSeamRunRequest(t.TempDir()))
	if err == nil || !strings.Contains(err.Error(), "podman not installed") {
		t.Fatalf("err = %v, want launch error surfaced", err)
	}
}

func TestNodeRunnerDefaultLauncherRecordsProcessPlacement(t *testing.T) {
	root := t.TempDir()
	nodePath := writeExecutable(t, root, "fake-node",
		"#!/bin/sh\nprintf '%s\\n' '{\"status\":\"completed\",\"summary\":\"ok\"}'\n")
	result, err := (NodeRunner{NodePath: nodePath}).Run(context.Background(), sandboxSeamRunRequest(root))
	if err != nil {
		t.Fatalf("NodeRunner.Run: %v", err)
	}
	if result.Status != domain.DriverRunCompleted {
		t.Fatalf("result = %+v, want completed", result)
	}
	var placement domain.TaskRunPlacement
	if err := json.Unmarshal([]byte(result.Output[SandboxPlacementOutputKey]), &placement); err != nil {
		t.Fatalf("decode %s output: %v (output = %+v)", SandboxPlacementOutputKey, err, result.Output)
	}
	if placement.Provider != SandboxProviderProcess || placement.ProcessRef == "" || placement.CWD != root {
		t.Fatalf("placement = %+v, want process provider with pid ref and bundle-root cwd", placement)
	}
}
