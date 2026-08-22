package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/workflows"
	"github.com/tysonthomas9/loomcli/internal/workflows/packaged"
)

const fakeServerMJS = "import { createRequire } from \"node:module\";\nimport { createLoomDriverClient } from \"@loom/sdk/driver\";\nexport {};\n"

// packageBuiltinFixture is one packager invocation's inputs: a fake Flue
// dist, a fake @loom/sdk runtime dir, and a fresh --out.
type packageBuiltinFixture struct {
	dist string
	sdk  string
	out  string
}

// setupPackageBuiltin pins every env var and flag the packager reads so the
// desktop app's shell environment cannot leak in, and returns a fixture for
// name whose dist carries a matching source-digest.txt.
func setupPackageBuiltin(t *testing.T, name string) packageBuiltinFixture {
	t.Helper()
	resetWorkflowCommandGlobals()
	t.Cleanup(resetWorkflowCommandGlobals)
	t.Setenv("LOOM_LOCAL_RUNTIME", "")
	t.Setenv("LOOM_BUILTIN_ARTIFACTS_DIR", "")
	t.Setenv("LOOM_NODE_BIN", "")
	t.Setenv("LOOM_SDK_ROOT", "")
	t.Setenv("LOOM_REAL_FLUE_CMD", "")
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", "")
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")
	t.Setenv("DAYTONA_SDK_ROOT", "")
	origDigest := packaged.ExpectedIndexDigest
	packaged.ExpectedIndexDigest = ""
	t.Cleanup(func() { packaged.ExpectedIndexDigest = origDigest })
	fx := packageBuiltinFixture{
		dist: writeFakeDist(t, name, fakeServerMJS),
		sdk:  writeFakeLoomSDK(t),
		out:  filepath.Join(t.TempDir(), "builtin-workflows"),
	}
	workflowPackageLoomSDK = fx.sdk
	workflowPackageJSON = true
	return fx
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeFakeDist lays out server.mjs, a source map, an asset, and a
// source-digest.txt attesting the embedded spec digest for name.
func writeFakeDist(t *testing.T, name, server string) string {
	t.Helper()
	dist := filepath.Join(t.TempDir(), "dist")
	writeTestFile(t, filepath.Join(dist, "server.mjs"), server)
	writeTestFile(t, filepath.Join(dist, "server.mjs.map"), `{"version":3,"sources":[]}`+"\n")
	writeTestFile(t, filepath.Join(dist, "assets", "x.txt"), "asset\n")
	sourceDigest, _, ok := workflows.BuiltinArtifactExpectation(name)
	if !ok {
		t.Fatalf("no built-in artifact expectation for %q", name)
	}
	writeTestFile(t, filepath.Join(dist, "source-digest.txt"), sourceDigest+"\n")
	return dist
}

func writeFakeLoomSDK(t *testing.T) string {
	t.Helper()
	sdk := filepath.Join(t.TempDir(), "sdk")
	for _, rel := range packaged.LoomSDKRuntimeFiles {
		content := "export {};\n"
		if rel == "package.json" {
			content = `{"name":"@loom/sdk"}` + "\n"
		}
		writeTestFile(t, filepath.Join(sdk, rel), content)
	}
	return sdk
}

func runPackageBuiltin(t *testing.T, name string, fx packageBuiltinFixture) (packageBuiltinOutput, error) {
	t.Helper()
	workflowPackageDist = fx.dist
	workflowPackageOut = fx.out
	workflowPackageJSON = true
	stdout, err := captureWorkflowStdout(t, func() error {
		return runWorkflowPackageBuiltin(&cobra.Command{}, []string{name})
	})
	if err != nil {
		return packageBuiltinOutput{}, err
	}
	var out packageBuiltinOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("decode package-builtin JSON %q: %v", stdout, err)
	}
	return out, nil
}

func mustRunPackageBuiltin(t *testing.T, name string, fx packageBuiltinFixture) packageBuiltinOutput {
	t.Helper()
	out, err := runPackageBuiltin(t, name, fx)
	if err != nil {
		t.Fatalf("runWorkflowPackageBuiltin(%s): %v", name, err)
	}
	return out
}

// assertNothingCommitted checks a failed run left <out> without the builtin
// tree, without index.json, and without a leaked staging dir.
func assertNothingCommitted(t *testing.T, out, name string) {
	t.Helper()
	for _, rel := range []string{name, packaged.IndexFileName} {
		if _, err := os.Lstat(filepath.Join(out, rel)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s/%s exists after failed run (err=%v), want absent", out, rel, err)
		}
	}
	assertNothingStaged(t, out)
}

func readIndexFile(t *testing.T, out string) ([]byte, packaged.Index) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(out, packaged.IndexFileName))
	if err != nil {
		t.Fatalf("read index.json: %v", err)
	}
	var idx packaged.Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatalf("decode index.json %s: %v", raw, err)
	}
	return raw, idx
}

func expectedEpicRunnerIndex(t *testing.T, artifactDigest string) []byte {
	t.Helper()
	spec, _ := workflows.BuiltinWorkflow(workflows.BuiltinEpicRunnerWorkflowName)
	sourceDigest, runners, _ := workflows.BuiltinArtifactExpectation(workflows.BuiltinEpicRunnerWorkflowName)
	encoded, err := packaged.EncodeIndex(packaged.Index{
		SchemaVersion: packaged.SchemaVersion,
		FlueCommit:    workflows.PinnedFlueCommit,
		NodeVersion:   workflows.PinnedNodeVersion,
		Target:        packaged.HostTargetTriple(),
		Builtins: map[string]packaged.Entry{
			workflows.BuiltinEpicRunnerWorkflowName: {
				Path:           workflows.BuiltinEpicRunnerWorkflowName,
				Entrypoint:     spec.Entrypoint,
				SourceDigest:   sourceDigest,
				ArtifactDigest: artifactDigest,
				Runners:        runners,
			},
		},
	})
	if err != nil {
		t.Fatalf("encode expected index: %v", err)
	}
	return encoded
}

func TestWorkflowPackageBuiltinWritesTreeAndIndex(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	out := mustRunPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)

	for _, rel := range []string{
		"epic-runner/dist/server.mjs",
		"epic-runner/dist/server.mjs.map",
		"epic-runner/dist/assets/x.txt",
		"epic-runner/dist/node_modules/@loom/sdk/driver.js",
		"epic-runner/dist/node_modules/@loom/sdk/package.json",
		"epic-runner/source-digest.txt",
		packaged.IndexFileName,
	} {
		if info, err := os.Lstat(filepath.Join(fx.out, filepath.FromSlash(rel))); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("expected regular file %s in out: err=%v", rel, err)
		}
	}
	raw, _ := readIndexFile(t, fx.out)
	if want := expectedEpicRunnerIndex(t, out.ArtifactDigest); !bytes.Equal(raw, want) {
		t.Fatalf("index.json = %s, want canonical %s", raw, want)
	}
	if out.IndexDigest != packaged.IndexDigest(raw) {
		t.Fatalf("index_digest = %s, want %s", out.IndexDigest, packaged.IndexDigest(raw))
	}
	if !out.SourceDigestAttested || out.Name != workflows.BuiltinEpicRunnerWorkflowName || out.Target != packaged.HostTargetTriple() {
		t.Fatalf("output = %+v, want attested epic-runner on host target", out)
	}
	if out.FlueCommit != workflows.PinnedFlueCommit || out.NodeVersion != workflows.PinnedNodeVersion {
		t.Fatalf("pins = %s/%s, want %s/%s", out.FlueCommit, out.NodeVersion, workflows.PinnedFlueCommit, workflows.PinnedNodeVersion)
	}
	assertNothingStaged(t, fx.out)
}

// assertNothingStaged checks no .package-builtin-* staging dir remains in
// out (which may not exist at all after an early failure).
func assertNothingStaged(t *testing.T, out string) {
	t.Helper()
	entries, err := os.ReadDir(out)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read %s: %v", out, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".package-builtin-") {
			t.Fatalf("staging dir %s leaked into %s", entry.Name(), out)
		}
	}
}

func TestWorkflowPackageBuiltinIsDeterministic(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	first := mustRunPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	firstRaw, _ := readIndexFile(t, fx.out)

	fx.out = filepath.Join(t.TempDir(), "builtin-workflows-2")
	second := mustRunPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	secondRaw, _ := readIndexFile(t, fx.out)

	if !bytes.Equal(firstRaw, secondRaw) {
		t.Fatalf("index.json differs between runs:\n%s\n---\n%s", firstRaw, secondRaw)
	}
	if first.IndexDigest != second.IndexDigest || first.ArtifactDigest != second.ArtifactDigest {
		t.Fatalf("digests differ between runs: %+v vs %+v", first, second)
	}
}

func TestWorkflowPackageBuiltinRoundTripsThroughLookup(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	out := mustRunPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)

	packaged.ExpectedIndexDigest = out.IndexDigest
	t.Setenv("LOOM_BUILTIN_ARTIFACTS_DIR", fx.out)
	sourceDigest, runners, _ := workflows.BuiltinArtifactExpectation(workflows.BuiltinEpicRunnerWorkflowName)
	art, err := packaged.Lookup(workflows.BuiltinEpicRunnerWorkflowName, sourceDigest, runners)
	if err != nil {
		t.Fatalf("packaged.Lookup: %v", err)
	}
	if art.ArtifactDigest != out.ArtifactDigest || art.IndexDigest != out.IndexDigest || art.SourceDigest != out.SourceDigest {
		t.Fatalf("lookup = %+v, want digests from packager output %+v", art, out)
	}
	if art.DistPath != filepath.Join(fx.out, "epic-runner", "dist") {
		t.Fatalf("lookup dist = %s, want under %s", art.DistPath, fx.out)
	}
}

func TestWorkflowPackageBuiltinRefusesSourceDigestDrift(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	writeTestFile(t, filepath.Join(fx.dist, "source-digest.txt"), "sha256:drifted\n")

	_, err := runPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	if err == nil || !strings.Contains(err.Error(), "source digest drift") {
		t.Fatalf("err = %v, want source digest drift", err)
	}
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	assertNothingCommitted(t, fx.out, workflows.BuiltinEpicRunnerWorkflowName)
}

func TestWorkflowPackageBuiltinMissingSourceDigestIsUnattested(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	if err := os.Remove(filepath.Join(fx.dist, "source-digest.txt")); err != nil {
		t.Fatalf("remove source-digest.txt: %v", err)
	}

	out := mustRunPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	if out.SourceDigestAttested {
		t.Fatalf("output = %+v, want source_digest_attested=false", out)
	}
	sourceDigest, _, _ := workflows.BuiltinArtifactExpectation(workflows.BuiltinEpicRunnerWorkflowName)
	if out.SourceDigest != sourceDigest {
		t.Fatalf("source_digest = %s, want embedded %s", out.SourceDigest, sourceDigest)
	}
	if _, err := os.Stat(filepath.Join(fx.out, "epic-runner", "source-digest.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source-digest.txt stat err = %v, want absent when dist has none", err)
	}
}

func TestWorkflowPackageBuiltinAuditFailuresDoNotCommit(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(t *testing.T, dist string)
		wantErr []string
	}{
		{"native file", func(t *testing.T, dist string) {
			writeTestFile(t, filepath.Join(dist, "x.node"), "\x7fELF")
		}, []string{"artifact audit", "native_files", "x.node"}},
		{"daytona import", func(t *testing.T, dist string) {
			writeTestFile(t, filepath.Join(dist, "server.mjs"), "import x from \"@daytona/sdk\";\nexport {};\n")
		}, []string{"bare_specifiers", "@daytona/sdk"}},
		{"hono side-effect import", func(t *testing.T, dist string) {
			writeTestFile(t, filepath.Join(dist, "server.mjs"), "import \"hono\";\nexport {};\n")
		}, []string{"bare_specifiers", "hono"}},
		{"flue runtime require", func(t *testing.T, dist string) {
			writeTestFile(t, filepath.Join(dist, "server.mjs"), "const r = require(\"@flue/runtime/internal\");\nexport {};\n")
		}, []string{"bare_specifiers", "@flue/runtime/internal"}},
		{"dlopen call", func(t *testing.T, dist string) {
			writeTestFile(t, filepath.Join(dist, "server.mjs"), "process.dlopen(module, \"x.node\");\nexport {};\n")
		}, []string{"artifact audit", "dlopen"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
			tc.mutate(t, fx.dist)
			_, err := runPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
			if err == nil {
				t.Fatalf("runWorkflowPackageBuiltin succeeded, want audit failure")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want it to contain %q", err, want)
				}
			}
			assertNothingCommitted(t, fx.out, workflows.BuiltinEpicRunnerWorkflowName)
		})
	}
}

// Staging dereferences every symlink, so a dangling link in --dist cannot be
// copied and the run fails before commit; a symlink that survives into a
// staged tree is rule (v), covered by TestAuditArtifactRejectsSymlinks.
func TestWorkflowPackageBuiltinDanglingSymlinkDoesNotCommit(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	if err := os.Symlink(filepath.Join(fx.dist, "missing-target"), filepath.Join(fx.dist, "dangling")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	_, err := runPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	if err == nil || !strings.Contains(err.Error(), "stage dist") {
		t.Fatalf("err = %v, want staging failure", err)
	}
	assertNothingCommitted(t, fx.out, workflows.BuiltinEpicRunnerWorkflowName)
}

func TestAuditArtifactRejectsSymlinks(t *testing.T) {
	dist := filepath.Join(t.TempDir(), "dist")
	writeTestFile(t, filepath.Join(dist, "server.mjs"), "export {};\n")
	writeTestFile(t, filepath.Join(dist, "node_modules", "@loom", "sdk", "package.json"), `{"name":"@loom/sdk"}`)
	if err := os.Symlink("server.mjs", filepath.Join(dist, "linked.mjs")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	report, err := auditArtifact(dist)
	if err == nil || !strings.Contains(err.Error(), "symlinks") || !strings.Contains(err.Error(), "linked.mjs") {
		t.Fatalf("auditArtifact err = %v, want symlinks linked.mjs", err)
	}
	if len(report.Symlinks) != 1 || report.Symlinks[0] != "linked.mjs" {
		t.Fatalf("report.Symlinks = %v, want [linked.mjs]", report.Symlinks)
	}
}

func TestWorkflowPackageBuiltinReportsDynamicBareSpecifiers(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	writeTestFile(t, filepath.Join(fx.dist, "server.mjs"), "const xz = await import(\"node-liblzma\").catch(() => null);\nexport {};\n")

	out := mustRunPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	if len(out.Audit.DynamicBareSpecifiers) != 1 || out.Audit.DynamicBareSpecifiers[0] != "node-liblzma" {
		t.Fatalf("audit.dynamic_bare_specifiers = %v, want [node-liblzma]", out.Audit.DynamicBareSpecifiers)
	}
	if len(out.Audit.BareSpecifiers) != 0 || out.Audit.Dlopen || len(out.Audit.NativeFiles) != 0 || len(out.Audit.Symlinks) != 0 {
		t.Fatalf("audit = %+v, want only a dynamic specifier reported", out.Audit)
	}
}

func TestWorkflowPackageBuiltinCountsCreateRequire(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	writeTestFile(t, filepath.Join(fx.dist, "server.mjs"), "import { createRequire } from \"node:module\";\nexport {};\n")

	out := mustRunPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	if out.Audit.CreateRequireCount != 1 {
		t.Fatalf("audit.create_require_count = %d, want 1", out.Audit.CreateRequireCount)
	}
	if len(out.Audit.BareSpecifiers) != 0 || len(out.Audit.DynamicBareSpecifiers) != 0 {
		t.Fatalf("audit = %+v, want no bare specifiers", out.Audit)
	}
}

func TestWorkflowPackageBuiltinDereferencesNestedSDKSymlink(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	scope := filepath.Join(fx.dist, "node_modules", "@loom")
	if err := os.MkdirAll(scope, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", scope, err)
	}
	if err := os.Symlink(fx.sdk, filepath.Join(scope, "sdk")); err != nil {
		t.Fatalf("symlink sdk: %v", err)
	}

	mustRunPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	for _, rel := range []string{"node_modules/@loom/sdk", "node_modules/@loom/sdk/driver.js"} {
		info, err := os.Lstat(filepath.Join(fx.out, "epic-runner", "dist", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("lstat %s: %v", rel, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("%s is a symlink in the packaged tree, want real file", rel)
		}
	}
	data, err := os.ReadFile(filepath.Join(fx.out, "epic-runner", "dist", "node_modules", "@loom", "sdk", "package.json"))
	if err != nil || !strings.Contains(string(data), "@loom/sdk") {
		t.Fatalf("nested package.json = %q (err=%v), want @loom/sdk", data, err)
	}
}

// A directory symlink that points back at an ancestor must be reported as a
// cycle rather than walked forever.
func TestWorkflowPackageBuiltinDirectorySymlinkCycleDoesNotCommit(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	if err := os.Symlink(fx.dist, filepath.Join(fx.dist, "loop")); err != nil {
		t.Fatalf("symlink loop: %v", err)
	}

	_, err := runPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	if err == nil || !strings.Contains(err.Error(), "symlink cycle") {
		t.Fatalf("err = %v, want symlink cycle", err)
	}
	assertNothingCommitted(t, fx.out, workflows.BuiltinEpicRunnerWorkflowName)
}

// Two directory symlinks to the same target are not a cycle (pnpm-style
// layouts do this); both get copied as real trees.
func TestWorkflowPackageBuiltinSharedSymlinkTargetIsNotACycle(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatalf("mkdir shared: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shared, "lib.js"), []byte("export const x = 1;\n"), 0o644); err != nil {
		t.Fatalf("write lib.js: %v", err)
	}
	for _, link := range []string{"a", "b"} {
		if err := os.Symlink(shared, filepath.Join(fx.dist, link)); err != nil {
			t.Fatalf("symlink %s: %v", link, err)
		}
	}

	mustRunPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	for _, rel := range []string{"a/lib.js", "b/lib.js"} {
		info, err := os.Lstat(filepath.Join(fx.out, "epic-runner", "dist", filepath.FromSlash(rel)))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("%s: err=%v mode=%v, want regular file", rel, err, info)
		}
	}
}

// A failed commit must leave the previously packaged tree in place and leave
// no staging residue: here the index write fails (index.json is a directory)
// after the new tree was already swapped in, so the swap must roll back.
func TestWorkflowPackageBuiltinFailedCommitKeepsPreviousArtifact(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	first := mustRunPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	if err := os.WriteFile(filepath.Join(fx.dist, "extra.js"), []byte("export const extra = 1;\n"), 0o644); err != nil {
		t.Fatalf("write extra.js: %v", err)
	}
	indexPath := filepath.Join(fx.out, "index.json")
	if err := os.Remove(indexPath); err != nil {
		t.Fatalf("remove index: %v", err)
	}
	if err := os.Mkdir(indexPath, 0o755); err != nil {
		t.Fatalf("mkdir index: %v", err)
	}

	_, err := runPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	if err == nil || !strings.Contains(err.Error(), "index.json") {
		t.Fatalf("err = %v, want index.json write failure", err)
	}
	got, err := packagedDistDigest(fx.out, workflows.BuiltinEpicRunnerWorkflowName)
	if err != nil || got != first.ArtifactDigest {
		t.Fatalf("artifact digest after failed commit = %q (err=%v), want previous %q", got, err, first.ArtifactDigest)
	}
	entries, err := os.ReadDir(fx.out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".package-builtin-") {
			t.Fatalf("staging residue left behind: %s", entry.Name())
		}
	}
}

func packagedDistDigest(out, name string) (string, error) {
	return driver.DigestDirectory(filepath.Join(out, name, "dist"))
}

func TestWorkflowPackageBuiltinRequireAll(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinGitHubReviewAgentWorkflowName)
	workflowPackageRequireAll = true

	_, err := runPackageBuiltin(t, workflows.BuiltinGitHubReviewAgentWorkflowName, fx)
	if err == nil || !strings.Contains(err.Error(), "required built-in epic-runner is not packaged") {
		t.Fatalf("err = %v, want required built-in epic-runner missing", err)
	}
	assertNothingCommitted(t, fx.out, workflows.BuiltinGitHubReviewAgentWorkflowName)

	fx.dist = writeFakeDist(t, workflows.BuiltinEpicRunnerWorkflowName, fakeServerMJS)
	out := mustRunPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	_, idx := readIndexFile(t, fx.out)
	if _, ok := idx.Builtins[workflows.BuiltinEpicRunnerWorkflowName]; !ok || out.Name != workflows.BuiltinEpicRunnerWorkflowName {
		t.Fatalf("index builtins = %v, want epic-runner after --require-all success", idx.Builtins)
	}
}

func writeHandIndex(t *testing.T, out, target string) {
	t.Helper()
	encoded, err := packaged.EncodeIndex(packaged.Index{
		SchemaVersion: packaged.SchemaVersion,
		FlueCommit:    workflows.PinnedFlueCommit,
		NodeVersion:   workflows.PinnedNodeVersion,
		Target:        target,
		Builtins: map[string]packaged.Entry{
			workflows.BuiltinGitHubReviewAgentWorkflowName: {
				Path:           workflows.BuiltinGitHubReviewAgentWorkflowName,
				Entrypoint:     "workflows/github-review-agent.ts",
				SourceDigest:   "sha256:source",
				ArtifactDigest: "sha256:artifact",
			},
		},
	})
	if err != nil {
		t.Fatalf("encode hand index: %v", err)
	}
	writeTestFile(t, filepath.Join(out, packaged.IndexFileName), string(encoded))
}

func TestWorkflowPackageBuiltinMergesExistingIndex(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	writeHandIndex(t, fx.out, packaged.HostTargetTriple())

	mustRunPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	_, idx := readIndexFile(t, fx.out)
	for _, name := range []string{workflows.BuiltinEpicRunnerWorkflowName, workflows.BuiltinGitHubReviewAgentWorkflowName} {
		if _, ok := idx.Builtins[name]; !ok {
			t.Fatalf("index builtins = %v, want %s preserved/added on merge", idx.Builtins, name)
		}
	}
	if got := idx.Builtins[workflows.BuiltinGitHubReviewAgentWorkflowName].ArtifactDigest; got != "sha256:artifact" {
		t.Fatalf("merged github-review-agent artifact_digest = %s, want hand-written entry untouched", got)
	}
}

func TestWorkflowPackageBuiltinRejectsIndexTargetMismatch(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	other := "x86_64-pc-windows-msvc"
	if other == packaged.HostTargetTriple() {
		other = "aarch64-apple-darwin"
	}
	writeHandIndex(t, fx.out, other)

	_, err := runPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	if err == nil || !strings.Contains(err.Error(), "index target mismatch") {
		t.Fatalf("err = %v, want index target mismatch", err)
	}
	if _, err := os.Stat(filepath.Join(fx.out, workflows.BuiltinEpicRunnerWorkflowName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("epic-runner dir stat err = %v, want absent after target mismatch", err)
	}
}

func TestWorkflowPackageBuiltinPinDrift(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	workflowPackageFlueCommit = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	_, err := runPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	if err == nil || !strings.Contains(err.Error(), "pin drift") {
		t.Fatalf("err = %v, want pin drift", err)
	}
	assertNothingCommitted(t, fx.out, workflows.BuiltinEpicRunnerWorkflowName)

	workflowPackageAllowDrift = true
	out := mustRunPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	_, idx := readIndexFile(t, fx.out)
	if out.FlueCommit != workflowPackageFlueCommit || idx.FlueCommit != workflowPackageFlueCommit {
		t.Fatalf("flue_commit = %s / index %s, want %s", out.FlueCommit, idx.FlueCommit, workflowPackageFlueCommit)
	}
	if idx.NodeVersion != workflows.PinnedNodeVersion {
		t.Fatalf("node_version = %s, want pinned %s", idx.NodeVersion, workflows.PinnedNodeVersion)
	}
}

func TestWorkflowPackageBuiltinRejectsOutInsideDist(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	for _, out := range []string{fx.dist, filepath.Join(fx.dist, "nested")} {
		fx.out = out
		_, err := runPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
		if err == nil || !strings.Contains(err.Error(), "--out must not be inside --dist") {
			t.Fatalf("out=%s err = %v, want --out inside --dist rejection", out, err)
		}
	}
}

// A symlinked --out that resolves into --dist is still "inside --dist".
func TestWorkflowPackageBuiltinRejectsSymlinkedOutInsideDist(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	link := filepath.Join(t.TempDir(), "out-link")
	if err := os.Symlink(fx.dist, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	fx.out = filepath.Join(link, "bw")

	_, err := runPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName, fx)
	if err == nil || !strings.Contains(err.Error(), "--out must not be inside --dist") {
		t.Fatalf("err = %v, want --out inside --dist rejection", err)
	}
}

func TestWorkflowPackageBuiltinUnknownWorkflowReturnsNotFound(t *testing.T) {
	fx := setupPackageBuiltin(t, workflows.BuiltinEpicRunnerWorkflowName)
	_, err := runPackageBuiltin(t, "missing-workflow", fx)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	assertNothingCommitted(t, fx.out, "missing-workflow")
}
