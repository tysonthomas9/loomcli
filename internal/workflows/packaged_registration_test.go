package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/workflows/packaged"
)

// setExpectedIndexDigest overrides the -ldflags-baked index digest for the
// duration of the test and restores the previous value on cleanup.
func setExpectedIndexDigest(t *testing.T, value string) {
	t.Helper()
	prev := packaged.ExpectedIndexDigest
	packaged.ExpectedIndexDigest = value
	t.Cleanup(func() { packaged.ExpectedIndexDigest = prev })
}

// isolatePackagedEnv pins every env var the packaged and compile lanes read
// so a desktop-spawned shell (LOOM_LOCAL_RUNTIME=desktop, baked artifact
// dirs, a real toolchain) cannot leak in. The compiler is /usr/bin/false and
// the cwd has no ./sdk, so any compile attempt fails loudly instead of
// succeeding by accident.
func isolatePackagedEnv(t *testing.T) {
	t.Helper()
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

// installPackagedEpicRunner builds a verified packaged tree for epic-runner
// at a fresh root, points LOOM_BUILTIN_ARTIFACTS_DIR at it, and bakes the
// index digest as if this were a packaged build. Returns the root and the
// baked index digest.
func installPackagedEpicRunner(t *testing.T) (root, indexDigest string) {
	t.Helper()
	return installPackagedEpicRunnerBuild(t, "export {};\n")
}

// installPackagedEpicRunnerBuild is installPackagedEpicRunner with a custom
// server.mjs body, so two calls can stand in for two distinct app builds.
func installPackagedEpicRunnerBuild(t *testing.T, serverSource string) (root, indexDigest string) {
	t.Helper()
	return installPackagedBuiltinsBuild(t, map[string]string{BuiltinEpicRunnerWorkflowName: serverSource})
}

// installPackagedBuiltins builds one verified packaged tree holding every
// named built-in (each with its own stub server.mjs, nested @loom/sdk, and
// the embedded spec's source digest + derived runner set), points
// LOOM_BUILTIN_ARTIFACTS_DIR at it, and bakes the index digest as if this
// were a packaged build. Returns the root and the baked index digest.
func installPackagedBuiltins(t *testing.T, names ...string) (root, indexDigest string) {
	t.Helper()
	sources := make(map[string]string, len(names))
	for _, name := range names {
		sources[name] = "// " + name + "\nexport {};\n"
	}
	return installPackagedBuiltinsBuild(t, sources)
}

// installPackagedBuiltinsBuild is installPackagedBuiltins with a server.mjs
// body per name.
func installPackagedBuiltinsBuild(t *testing.T, sources map[string]string) (root, indexDigest string) {
	t.Helper()
	isolatePackagedEnv(t)
	root = t.TempDir()
	idx := packaged.Index{
		SchemaVersion: packaged.SchemaVersion,
		FlueCommit:    PinnedFlueCommit,
		NodeVersion:   PinnedNodeVersion,
		Target:        packaged.HostTargetTriple(),
		Builtins:      map[string]packaged.Entry{},
	}
	for name, serverSource := range sources {
		dist := filepath.Join(root, name, "dist")
		writePackagedDist(t, dist)
		if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte(serverSource), 0o644); err != nil {
			t.Fatalf("write server.mjs: %v", err)
		}
		artifactDigest, err := driverpkg.DigestDirectory(dist)
		if err != nil {
			t.Fatalf("digest packaged dist: %v", err)
		}
		spec, ok := BuiltinWorkflow(name)
		if !ok {
			t.Fatalf("built-in %q missing", name)
		}
		sourceDigest, runners, ok := BuiltinArtifactExpectation(name)
		if !ok {
			t.Fatalf("built-in %q has no artifact expectation", name)
		}
		idx.Builtins[name] = packaged.Entry{
			Path:           name,
			Entrypoint:     spec.Entrypoint,
			SourceDigest:   sourceDigest,
			ArtifactDigest: artifactDigest,
			Runners:        runners,
		}
	}
	raw, err := packaged.EncodeIndex(idx)
	if err != nil {
		t.Fatalf("encode index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, packaged.IndexFileName), raw, 0o644); err != nil {
		t.Fatalf("write index.json: %v", err)
	}
	t.Setenv("LOOM_BUILTIN_ARTIFACTS_DIR", root)
	indexDigest = packaged.IndexDigest(raw)
	setExpectedIndexDigest(t, indexDigest)
	return root, indexDigest
}

// newBuiltinStore returns a memstore with the BUILTIN workspace created.
func newBuiltinStore(t *testing.T) store.Store {
	t.Helper()
	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "BUILTIN", Name: "Builtins"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return st
}

// activeBuiltinVersion reads the active driver version for a builtin.
func activeBuiltinVersion(t *testing.T, st store.Store, name string) *domain.DriverVersion {
	t.Helper()
	ctx := context.Background()
	driver, err := st.Drivers().Get(ctx, "BUILTIN", name)
	if err != nil {
		t.Fatalf("get driver %q: %v", name, err)
	}
	if driver.ActiveVersionID == "" {
		t.Fatalf("driver %q has no active version", name)
	}
	version, err := st.DriverVersions().Get(ctx, "BUILTIN", driver.ActiveVersionID)
	if err != nil {
		t.Fatalf("get active version %q: %v", driver.ActiveVersionID, err)
	}
	return version
}

// assertNoBuiltinDriver asserts that no driver row exists for name.
func assertNoBuiltinDriver(t *testing.T, st store.Store, name string) {
	t.Helper()
	if _, err := st.Drivers().Get(context.Background(), "BUILTIN", name); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("driver row for %q must not exist on a fail-closed path, got err=%v", name, err)
	}
}

// assertNoCompileAttempt asserts the error did not come from the compile
// lane: neither the fake compiler (/usr/bin/false -> "flue build failed") nor
// its toolchain resolution ("local @loom/sdk ... set LOOM_SDK_ROOT") ran.
func assertNoCompileAttempt(t *testing.T, err error) {
	t.Helper()
	msg := err.Error()
	for _, marker := range []string{"flue build", "local @loom/sdk", "LOOM_SDK_ROOT"} {
		if strings.Contains(msg, marker) {
			t.Fatalf("compile lane must never run on this path; error mentions %q: %v", marker, err)
		}
	}
}

// assertFailsClosed asserts a fail-closed packaged-lane error carrying every
// marker, with no compile attempt and no driver row left behind.
func assertFailsClosed(t *testing.T, st store.Store, err error, markers ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("EnsureBuiltinWorkflow must fail closed, got nil")
	}
	for _, marker := range markers {
		if !strings.Contains(err.Error(), marker) {
			t.Fatalf("error must mention %q, got: %v", marker, err)
		}
	}
	assertNoCompileAttempt(t, err)
	assertNoBuiltinDriver(t, st, BuiltinEpicRunnerWorkflowName)
}

// assertManifestRunners asserts the registered runner manifest equals the
// runner set derived from the embedded epic-runner source tree.
func assertManifestRunners(t *testing.T, manifest map[string]string) {
	t.Helper()
	var got []driverpkg.DriverRunnerSpec
	if err := json.Unmarshal([]byte(manifest["runners"]), &got); err != nil {
		t.Fatalf("decode manifest runners %q: %v", manifest["runners"], err)
	}
	_, want, ok := BuiltinArtifactExpectation(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("epic-runner builtin missing")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest runners = %+v, want derived set %+v", got, want)
	}
	names := make([]string, 0, len(got))
	for _, runner := range got {
		names = append(names, runner.Name)
	}
	if strings.Join(names, ",") != "daytona-task-runner,local-task-runner" {
		t.Fatalf("manifest runner names = %v, want [daytona-task-runner local-task-runner]", names)
	}
}

// assertStagedBundle asserts the packaged dist (including the nested
// @loom/sdk) was staged under the workspace runtime dir at bundleRef.
func assertStagedBundle(t *testing.T, bundleRef string) {
	t.Helper()
	if bundleRef == "" || filepath.IsAbs(bundleRef) {
		t.Fatalf("bundle ref %q must be workdir-relative", bundleRef)
	}
	root := filepath.Join(os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR"), filepath.FromSlash(bundleRef))
	for _, rel := range []string{
		filepath.Join("dist", "server.mjs"),
		filepath.Join("dist", "node_modules", "@loom", "sdk", "driver.js"),
	} {
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("staged bundle missing %s: %v", rel, err)
		}
		if info.IsDir() {
			t.Fatalf("staged bundle %s is a directory", rel)
		}
	}
}

// flipFirstByte tampers with a file in place by changing its first byte.
func flipFirstByte(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // path is a test-owned temp file.
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(data) == 0 {
		t.Fatalf("%s is empty; nothing to tamper", path)
	}
	data[0] ^= 0x20
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write tampered %s: %v", path, err)
	}
}

// TestEnsureBuiltinWorkflowUsesPackagedArtifact: a packaged build with a
// verified artifact registers epic-runner WITHOUT the compiler (the compiler
// is /usr/bin/false) and stamps packaged provenance on the active version.
func TestEnsureBuiltinWorkflowUsesPackagedArtifact(t *testing.T) {
	ctx := context.Background()
	_, indexDigest := installPackagedEpicRunner(t)
	st := newBuiltinStore(t)

	if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName); err != nil {
		t.Fatalf("EnsureBuiltinWorkflow: %v", err)
	}

	version := activeBuiltinVersion(t, st, BuiltinEpicRunnerWorkflowName)
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("epic-runner builtin missing")
	}
	want := map[string]string{
		"provenance":            packaged.ProvenancePackagedBuiltin,
		"trust_level":           string(domain.DriverTrustTrusted),
		"source_digest":         SourceDigest(spec.Files),
		"flue_commit":           PinnedFlueCommit,
		"node_version":          PinnedNodeVersion,
		"packaged_index_digest": indexDigest,
	}
	for key, value := range want {
		if got := version.Manifest[key]; got != value {
			t.Errorf("manifest[%s] = %q, want %q", key, got, value)
		}
	}
	assertManifestRunners(t, version.Manifest)
	assertStagedBundle(t, version.BundleRef)
}

// TestEnsureBuiltinWorkflowPackagedIsIdempotent: once registered from the
// packaged artifact, a second call reuses the active version through the
// fast path BEFORE Lookup runs, so the artifact tree is no longer needed.
func TestEnsureBuiltinWorkflowPackagedIsIdempotent(t *testing.T) {
	ctx := context.Background()
	root, _ := installPackagedEpicRunner(t)
	st := newBuiltinStore(t)

	if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName); err != nil {
		t.Fatalf("first EnsureBuiltinWorkflow: %v", err)
	}
	first := activeBuiltinVersion(t, st, BuiltinEpicRunnerWorkflowName)

	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("remove artifact root: %v", err)
	}
	t.Setenv("LOOM_BUILTIN_ARTIFACTS_DIR", t.TempDir())
	sourceDigest, runners, _ := BuiltinArtifactExpectation(BuiltinEpicRunnerWorkflowName)
	if _, err := packaged.Lookup(BuiltinEpicRunnerWorkflowName, sourceDigest, runners); err == nil {
		t.Fatal("packaged Lookup must fail once the artifact tree is gone")
	}

	if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName); err != nil {
		t.Fatalf("second EnsureBuiltinWorkflow must reuse the registered version, got: %v", err)
	}
	second := activeBuiltinVersion(t, st, BuiltinEpicRunnerWorkflowName)
	if second.VersionID != first.VersionID {
		t.Fatalf("active version changed from %q to %q; second call must reuse", first.VersionID, second.VersionID)
	}
}

// An app upgrade ships a new artifact under a new baked index digest: the
// previously registered packaged version must be superseded by the artifact
// this binary verified, not reused forever.
func TestEnsureBuiltinWorkflowUpgradedPackagedArtifactSupersedesRegistered(t *testing.T) {
	ctx := context.Background()
	_, firstDigest := installPackagedEpicRunnerBuild(t, "export {};\n")
	st := newBuiltinStore(t)
	if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName); err != nil {
		t.Fatalf("first EnsureBuiltinWorkflow: %v", err)
	}
	first := activeBuiltinVersion(t, st, BuiltinEpicRunnerWorkflowName)

	_, secondDigest := installPackagedEpicRunnerBuild(t, "export const build = 2;\n")
	if secondDigest == firstDigest {
		t.Fatal("test setup: the second build must carry a different index digest")
	}
	if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName); err != nil {
		t.Fatalf("second EnsureBuiltinWorkflow: %v", err)
	}
	second := activeBuiltinVersion(t, st, BuiltinEpicRunnerWorkflowName)
	if second.VersionID == first.VersionID {
		t.Fatalf("active version %q was reused across an artifact upgrade", first.VersionID)
	}
	if got := second.Manifest["packaged_index_digest"]; got != secondDigest {
		t.Fatalf("packaged_index_digest = %q, want the upgraded build %q", got, secondDigest)
	}
	assertStagedBundle(t, second.BundleRef)
}

// TestEnsureBuiltinWorkflowDesktopNotPackagedFailsClosed: a desktop process
// (LOOM_LOCAL_RUNTIME=desktop) whose binary carries no baked index digest and
// has no artifact tree must fail closed with operator guidance — never reach
// the compiler, never leave a driver row.
func TestEnsureBuiltinWorkflowDesktopNotPackagedFailsClosed(t *testing.T) {
	isolatePackagedEnv(t)
	setExpectedIndexDigest(t, "")
	t.Setenv("LOOM_LOCAL_RUNTIME", "desktop")
	st := newBuiltinStore(t)

	err := EnsureBuiltinWorkflow(context.Background(), st, "BUILTIN", BuiltinEpicRunnerWorkflowName)
	assertFailsClosed(t, st, err, "builtin_artifact_missing", "desktop packaging error")
}

// TestEnsureBuiltinWorkflowPackagedBuildNotPackagedFailsClosedWithoutEnv (B4):
// a packaged build (baked index digest) with no artifact tree fails closed
// even without the desktop marker — the policy is dual-keyed.
func TestEnsureBuiltinWorkflowPackagedBuildNotPackagedFailsClosedWithoutEnv(t *testing.T) {
	isolatePackagedEnv(t)
	setExpectedIndexDigest(t, "sha256:deadbeef")
	t.Setenv("LOOM_LOCAL_RUNTIME", "")
	st := newBuiltinStore(t)

	err := EnsureBuiltinWorkflow(context.Background(), st, "BUILTIN", BuiltinEpicRunnerWorkflowName)
	assertFailsClosed(t, st, err, "builtin_artifact_missing", "desktop packaging error")
}

// TestEnsureBuiltinWorkflowNonDesktopNotPackagedFallsBackToCompile: an
// unpackaged, non-desktop process (the developer/CI shape) keeps the legacy
// compile lane and registers with operator_registered provenance.
func TestEnsureBuiltinWorkflowNonDesktopNotPackagedFallsBackToCompile(t *testing.T) {
	isolatePackagedEnv(t)
	setExpectedIndexDigest(t, "")
	configureFakeBuiltinBundleBuild(t)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", t.TempDir())
	st := newBuiltinStore(t)

	if err := EnsureBuiltinWorkflow(context.Background(), st, "BUILTIN", BuiltinEpicRunnerWorkflowName); err != nil {
		t.Fatalf("EnsureBuiltinWorkflow must fall back to the compile lane, got: %v", err)
	}
	version := activeBuiltinVersion(t, st, BuiltinEpicRunnerWorkflowName)
	if got := version.Manifest["provenance"]; got != "operator_registered" {
		t.Fatalf("manifest[provenance] = %q, want operator_registered", got)
	}
}

// TestEnsureBuiltinWorkflowTamperedArtifactFailsClosedEverywhere: a
// verification failure (artifact digest mismatch) is fatal in EVERY mode —
// packaged+desktop, packaged with the marker unset, and packaged with the
// marker empty — and never falls back to compiling.
func TestEnsureBuiltinWorkflowTamperedArtifactFailsClosedEverywhere(t *testing.T) {
	root, _ := installPackagedEpicRunner(t)
	flipFirstByte(t, filepath.Join(root, BuiltinEpicRunnerWorkflowName, "dist", "server.mjs"))

	cases := []struct {
		name  string
		setup func(t *testing.T)
	}{
		{name: "baked+desktop", setup: func(t *testing.T) { t.Setenv("LOOM_LOCAL_RUNTIME", "desktop") }},
		{name: "baked-only", setup: func(t *testing.T) {
			t.Setenv("LOOM_LOCAL_RUNTIME", "")
			_ = os.Unsetenv("LOOM_LOCAL_RUNTIME")
		}},
		{name: "baked+empty-local-runtime", setup: func(t *testing.T) { t.Setenv("LOOM_LOCAL_RUNTIME", "") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			st := newBuiltinStore(t)
			err := EnsureBuiltinWorkflow(context.Background(), st, "BUILTIN", BuiltinEpicRunnerWorkflowName)
			assertFailsClosed(t, st, err, "builtin_artifact_invalid", "artifact_digest")
		})
	}
}

// TestEnsureBuiltinWorkflowUsesPackagedGitHubReviewAgent (DEV-V5-37): with
// both required built-ins packaged, github-review-agent registers from its
// own artifact entry WITHOUT the compiler, with packaged provenance, the
// single derived runner, and its nested @loom/sdk staged.
func TestEnsureBuiltinWorkflowUsesPackagedGitHubReviewAgent(t *testing.T) {
	ctx := context.Background()
	_, indexDigest := installPackagedBuiltins(t, packaged.RequiredBuiltins...)
	st := newBuiltinStore(t)

	if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinGitHubReviewAgentWorkflowName); err != nil {
		t.Fatalf("EnsureBuiltinWorkflow(github-review-agent): %v", err)
	}

	version := activeBuiltinVersion(t, st, BuiltinGitHubReviewAgentWorkflowName)
	spec, ok := BuiltinWorkflow(BuiltinGitHubReviewAgentWorkflowName)
	if !ok {
		t.Fatal("github-review-agent builtin missing")
	}
	want := map[string]string{
		"provenance":            packaged.ProvenancePackagedBuiltin,
		"trust_level":           string(domain.DriverTrustTrusted),
		"source_digest":         SourceDigest(spec.Files),
		"packaged_index_digest": indexDigest,
	}
	for key, value := range want {
		if got := version.Manifest[key]; got != value {
			t.Errorf("manifest[%s] = %q, want %q", key, got, value)
		}
	}
	var runners []driverpkg.DriverRunnerSpec
	if err := json.Unmarshal([]byte(version.Manifest["runners"]), &runners); err != nil {
		t.Fatalf("decode manifest runners %q: %v", version.Manifest["runners"], err)
	}
	wantRunners := []driverpkg.DriverRunnerSpec{{
		Name: BuiltinGitHubReviewTaskRunnerName, Kind: driverpkg.RunnerKindFlueWorkflow, Entrypoint: BuiltinGitHubReviewTaskRunnerName,
	}}
	if !reflect.DeepEqual(runners, wantRunners) {
		t.Fatalf("manifest runners = %+v, want %+v", runners, wantRunners)
	}
	assertStagedBundle(t, version.BundleRef)
	if !strings.Contains(filepath.ToSlash(version.BundleRef), "/drivers/"+BuiltinGitHubReviewAgentWorkflowName+"/") {
		t.Fatalf("bundle ref %q must be staged under drivers/github-review-agent", version.BundleRef)
	}
}

// TestEnsureBuiltinWorkflowBothPackagedRegisterIndependently (DEV-V5-37):
// each required built-in registers from its own index entry, in either
// order, each with its own packaged registration log line.
func TestEnsureBuiltinWorkflowBothPackagedRegisterIndependently(t *testing.T) {
	ctx := context.Background()
	installPackagedBuiltins(t, packaged.RequiredBuiltins...)
	st := newBuiltinStore(t)
	logs := captureSlog(t)

	// github-review-agent first: it must not depend on epic-runner having
	// been registered.
	for _, name := range []string{BuiltinGitHubReviewAgentWorkflowName, BuiltinEpicRunnerWorkflowName} {
		if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", name); err != nil {
			t.Fatalf("EnsureBuiltinWorkflow(%s): %v", name, err)
		}
		if got := activeBuiltinVersion(t, st, name).Manifest["provenance"]; got != packaged.ProvenancePackagedBuiltin {
			t.Fatalf("%s manifest[provenance] = %q, want packaged_builtin", name, got)
		}
	}
	out := logs.String()
	for _, name := range packaged.RequiredBuiltins {
		if !strings.Contains(out, "registered from packaged artifact") || !strings.Contains(out, "workflow="+name) {
			t.Fatalf("expected a packaged registration log line for %s, logs:\n%s", name, out)
		}
	}
}

// TestEnsureBuiltinWorkflowGitHubReviewAgentUnpackagedOnDesktopFailsClosed
// (R1, now the "required but missing" case after DEV-V5-37 widened the
// required set): a build that ships only epic-runner fails closed for
// github-review-agent with operator guidance — never compiles, never leaves
// a driver row — while epic-runner still registers from its own entry.
func TestEnsureBuiltinWorkflowGitHubReviewAgentUnpackagedOnDesktopFailsClosed(t *testing.T) {
	installPackagedEpicRunner(t)
	t.Setenv("LOOM_LOCAL_RUNTIME", "desktop")
	st := newBuiltinStore(t)

	err := EnsureBuiltinWorkflow(context.Background(), st, "BUILTIN", BuiltinGitHubReviewAgentWorkflowName)
	if err == nil {
		t.Fatal("EnsureBuiltinWorkflow must fail closed for an unpackaged built-in on desktop")
	}
	for _, marker := range []string{"builtin_artifact_missing", "reinstall Loom"} {
		if !strings.Contains(err.Error(), marker) {
			t.Fatalf("error must mention %q, got: %v", marker, err)
		}
	}
	assertNoCompileAttempt(t, err)
	assertNoBuiltinDriver(t, st, BuiltinGitHubReviewAgentWorkflowName)

	if err := EnsureBuiltinWorkflow(context.Background(), st, "BUILTIN", BuiltinEpicRunnerWorkflowName); err != nil {
		t.Fatalf("epic-runner must still register from its own entry: %v", err)
	}
}

// TestEnsureBuiltinWorkflowPackagedRegistrationErrorDoesNotCompile: a
// registration failure on the packaged lane (bundle staging cannot write the
// workspace runtime dir) is returned as-is — no compile fallback.
func TestEnsureBuiltinWorkflowPackagedRegistrationErrorDoesNotCompile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("read-only directories do not block root")
	}
	installPackagedEpicRunner(t)
	readOnly := t.TempDir()
	if err := os.Chmod(readOnly, 0o500); err != nil {
		t.Fatalf("chmod read-only runtime dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", readOnly)
	st := newBuiltinStore(t)

	err := EnsureBuiltinWorkflow(context.Background(), st, "BUILTIN", BuiltinEpicRunnerWorkflowName)
	if err == nil {
		t.Fatal("EnsureBuiltinWorkflow must surface the packaged registration failure")
	}
	if !strings.Contains(err.Error(), "register packaged built-in workflow") {
		t.Fatalf("error must come from the packaged registration, got: %v", err)
	}
	assertNoCompileAttempt(t, err)
}
