package tsruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/workflows"
	"github.com/tysonthomas9/loomcli/internal/workflows/packaged"
)

// resetBundleResolution clears the once-per-process bundle resolution (and
// the cli workspace runtime dir cache it reads) before and after a test.
func resetBundleResolution(t *testing.T) {
	t.Helper()
	resetTaskRunnerBundleForTest()
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(func() {
		resetTaskRunnerBundleForTest()
		cli.ResetWorkspaceRuntimeDirCache()
	})
}

// setExpectedIndexDigest overrides the -ldflags-baked index digest for the
// duration of the test and restores the previous value on cleanup.
func setExpectedIndexDigest(t *testing.T, value string) {
	t.Helper()
	prev := packaged.ExpectedIndexDigest
	packaged.ExpectedIndexDigest = value
	t.Cleanup(func() { packaged.ExpectedIndexDigest = prev })
}

// isolateBundleEnv pins every env var the packaged and build lanes read so a
// desktop-spawned shell cannot leak in; the compiler is /usr/bin/false and
// the cwd has no ./sdk, so a build attempt fails loudly.
func isolateBundleEnv(t *testing.T) {
	t.Helper()
	resetBundleResolution(t)
	t.Setenv("LOOM_LOCAL_RUNTIME", "")
	t.Setenv("LOOM_BUILTIN_ARTIFACTS_DIR", "")
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", t.TempDir())
	t.Setenv("LOOM_REAL_FLUE_CMD", "/usr/bin/false")
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", "")
	t.Setenv("LOOM_SDK_ROOT", "")
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")
	t.Setenv("DAYTONA_SDK_ROOT", "")
	t.Chdir(t.TempDir())
}

// writePackagedDist lays out a minimal packaged dist: server.mjs plus the
// nested @loom/sdk runtime files Lookup requires.
func writePackagedDist(t *testing.T, dist string) {
	t.Helper()
	sdkDir := filepath.Join(dist, "node_modules", "@loom", "sdk")
	if err := os.MkdirAll(sdkDir, 0o755); err != nil {
		t.Fatalf("create packaged dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatalf("write server.mjs: %v", err)
	}
	for _, name := range packaged.LoomSDKRuntimeFiles {
		content := "export {};\n"
		if name == "package.json" {
			content = `{"name":"@loom/sdk"}` + "\n"
		}
		if err := os.WriteFile(filepath.Join(sdkDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write @loom/sdk %s: %v", name, err)
		}
	}
}

// installPackagedEpicRunner builds a verified packaged tree for epic-runner,
// points LOOM_BUILTIN_ARTIFACTS_DIR at it, and bakes the index digest as if
// this were a packaged build. Returns the root.
func installPackagedEpicRunner(t *testing.T) string {
	t.Helper()
	isolateBundleEnv(t)
	root := t.TempDir()
	dist := filepath.Join(root, workflows.BuiltinEpicRunnerWorkflowName, "dist")
	writePackagedDist(t, dist)
	artifactDigest, err := driver.DigestDirectory(dist)
	if err != nil {
		t.Fatalf("digest packaged dist: %v", err)
	}
	sourceDigest, runners, ok := workflows.BuiltinArtifactExpectation(workflows.BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("epic-runner builtin missing")
	}
	raw, err := packaged.EncodeIndex(packaged.Index{
		SchemaVersion: packaged.SchemaVersion,
		FlueCommit:    workflows.PinnedFlueCommit,
		NodeVersion:   workflows.PinnedNodeVersion,
		Target:        packaged.HostTargetTriple(),
		Builtins: map[string]packaged.Entry{
			workflows.BuiltinEpicRunnerWorkflowName: {
				Path:           workflows.BuiltinEpicRunnerWorkflowName,
				Entrypoint:     "workflows/epic-runner.ts",
				SourceDigest:   sourceDigest,
				ArtifactDigest: artifactDigest,
				Runners:        runners,
			},
		},
	})
	if err != nil {
		t.Fatalf("encode index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, packaged.IndexFileName), raw, 0o644); err != nil {
		t.Fatalf("write index.json: %v", err)
	}
	t.Setenv("LOOM_BUILTIN_ARTIFACTS_DIR", root)
	setExpectedIndexDigest(t, packaged.IndexDigest(raw))
	return root
}

// TestTaskRunnerBundleServerPathUsesPackagedArtifact: a verified packaged
// artifact is used read-only, in place, without building anything.
func TestTaskRunnerBundleServerPathUsesPackagedArtifact(t *testing.T) {
	root := installPackagedEpicRunner(t)

	got, err := taskRunnerBundleServerPath()
	if err != nil {
		t.Fatalf("taskRunnerBundleServerPath: %v", err)
	}
	want := filepath.Join(root, workflows.BuiltinEpicRunnerWorkflowName, "dist", "server.mjs")
	if got != want {
		t.Fatalf("server path = %q, want packaged artifact %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR"), "ts-runtime-bundle")); !os.IsNotExist(err) {
		t.Fatalf("packaged lane must not build into the workspace runtime dir, stat err=%v", err)
	}
}

// TestTaskRunnerBundleServerPathPackagedBuildWithoutArtifactFailsClosed: a
// packaged build (baked index digest) with no artifact tree fails closed even
// without the desktop marker — it never falls through to the build lane.
func TestTaskRunnerBundleServerPathPackagedBuildWithoutArtifactFailsClosed(t *testing.T) {
	isolateBundleEnv(t)
	setExpectedIndexDigest(t, "sha256:deadbeef")

	_, err := taskRunnerBundleServerPath()
	if err == nil {
		t.Fatal("taskRunnerBundleServerPath must fail closed on a packaged build without artifacts")
	}
	for _, marker := range []string{"ts-leaf task runner bundle", "builtin_artifact_missing", "desktop packaging error"} {
		if !strings.Contains(err.Error(), marker) {
			t.Fatalf("error must mention %q, got: %v", marker, err)
		}
	}
	for _, marker := range []string{"flue build", "local @loom/sdk", "LOOM_SDK_ROOT"} {
		if strings.Contains(err.Error(), marker) {
			t.Fatalf("build lane must never run on a fail-closed path; error mentions %q: %v", marker, err)
		}
	}
}

// TestTaskRunnerBundleServerPathFallsBackToBuildWhenNotPackaged: an
// unpackaged, non-desktop process reaches the build lane. With no @loom/sdk
// on disk the build fails at toolchain resolution, proving the fallback ran.
func TestTaskRunnerBundleServerPathFallsBackToBuildWhenNotPackaged(t *testing.T) {
	isolateBundleEnv(t)
	setExpectedIndexDigest(t, "")

	_, err := taskRunnerBundleServerPath()
	if err == nil {
		t.Fatal("build lane must fail without a local @loom/sdk")
	}
	msg := err.Error()
	if !strings.Contains(msg, "local @loom/sdk") && !strings.Contains(msg, "flue build") {
		t.Fatalf("error must come from the build lane, got: %v", err)
	}
	if strings.Contains(msg, "builtin_artifact") {
		t.Fatalf("unpackaged non-desktop process must not fail closed on the packaged lane, got: %v", err)
	}
}
