package packaged

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
)

const (
	testFlueCommit   = "0123456789abcdef0123456789abcdef01234567"
	testSourceDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testServerBody   = "export default async function run() { return 'ok'; }\n"
)

func testRunners() []driver.DriverRunnerSpec {
	return []driver.DriverRunnerSpec{
		{Name: "daytona-task-runner", Kind: "flue-workflow", Entrypoint: "daytona-task-runner"},
		{Name: "local-task-runner", Kind: "flue-workflow", Entrypoint: "local-task-runner"},
	}
}

// testGHSourceDigest/testGHRunners are the github-review-agent fixture
// expectations (one derived runner, its own source digest).
const testGHSourceDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func testGHRunners() []driver.DriverRunnerSpec {
	return []driver.DriverRunnerSpec{
		{Name: "github-review-task-runner", Kind: "flue-workflow", Entrypoint: "github-review-task-runner"},
	}
}

// setup pins the env inputs and saves/restores the baked digest and the
// executablePath seam. Tests start in "not packaged, not desktop" mode.
func setup(t *testing.T) {
	t.Helper()
	origDigest, origExe := ExpectedIndexDigest, executablePath
	t.Cleanup(func() {
		ExpectedIndexDigest = origDigest
		executablePath = origExe
	})
	ExpectedIndexDigest = ""
	executablePath = func() (string, error) { return "", errors.New("no executable in tests") }
	t.Setenv(EnvArtifactsDir, "")
	t.Setenv(EnvLocalRuntime, "")
}

func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test fixture path.
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return data
}

// writeIndex encodes idx to <root>/index.json and returns its raw digest.
func writeIndex(t *testing.T, root string, idx Index) string {
	t.Helper()
	raw, err := EncodeIndex(idx)
	if err != nil {
		t.Fatalf("EncodeIndex: %v", err)
	}
	writeFile(t, filepath.Join(root, IndexFileName), raw)
	return IndexDigest(raw)
}

// readIndexFile parses the index currently on disk at root.
func readIndexFile(t *testing.T, root string) Index {
	t.Helper()
	var idx Index
	if err := json.Unmarshal(readFile(t, filepath.Join(root, IndexFileName)), &idx); err != nil {
		t.Fatalf("Unmarshal index.json: %v", err)
	}
	return idx
}

// rewriteIndex re-encodes the on-disk index after mutate and returns the new
// raw digest.
func rewriteIndex(t *testing.T, root string, mutate func(*Index)) string {
	t.Helper()
	idx := readIndexFile(t, root)
	mutate(&idx)
	return writeIndex(t, root, idx)
}

// fakeBuiltin is one packaged built-in fixture for writeFakeTreeWith.
type fakeBuiltin struct {
	name         string
	serverBody   string
	sourceDigest string
	runners      []driver.DriverRunnerSpec
}

func fakeEpicRunner() fakeBuiltin {
	return fakeBuiltin{name: "epic-runner", serverBody: testServerBody, sourceDigest: testSourceDigest, runners: testRunners()}
}

func fakeGitHubReviewAgent() fakeBuiltin {
	return fakeBuiltin{name: "github-review-agent", serverBody: "export const review = true;\n", sourceDigest: testGHSourceDigest, runners: testGHRunners()}
}

// writeFakeDist writes <root>/<name>/dist (server.mjs + nested @loom/sdk)
// and returns its artifact digest.
func writeFakeDist(t *testing.T, root, name, serverBody string) string {
	t.Helper()
	dist := filepath.Join(root, name, "dist")
	writeFile(t, filepath.Join(dist, "server.mjs"), []byte(serverBody))
	for _, file := range LoomSDKRuntimeFiles {
		body := "// " + file + "\n"
		if file == "package.json" {
			body = `{"name":"@loom/sdk"}`
		}
		writeFile(t, filepath.Join(dist, "node_modules", "@loom", "sdk", file), []byte(body))
	}
	artifactDigest, err := driver.DigestDirectory(dist)
	if err != nil {
		t.Fatalf("DigestDirectory(%s): %v", dist, err)
	}
	return artifactDigest
}

// writeFakeTreeWith writes a packaged dist per built-in under root plus a
// canonical index.json listing all of them, and returns the raw index digest
// a build would bake.
func writeFakeTreeWith(t *testing.T, root string, builtins ...fakeBuiltin) string {
	t.Helper()
	idx := Index{
		SchemaVersion: SchemaVersion,
		FlueCommit:    testFlueCommit,
		NodeVersion:   "22.20.0",
		Target:        HostTargetTriple(),
		Builtins:      map[string]Entry{},
	}
	for _, b := range builtins {
		idx.Builtins[b.name] = Entry{
			Path:           b.name,
			Entrypoint:     "workflows/" + b.name + ".ts",
			SourceDigest:   b.sourceDigest,
			ArtifactDigest: writeFakeDist(t, root, b.name, b.serverBody),
			Runners:        b.runners,
		}
	}
	return writeIndex(t, root, idx)
}

// writeFakeTree writes a single packaged built-in tree (see writeFakeTreeWith).
func writeFakeTree(t *testing.T, root, name, serverBody, sourceDigest string, runners []driver.DriverRunnerSpec) string {
	t.Helper()
	return writeFakeTreeWith(t, root, fakeBuiltin{name: name, serverBody: serverBody, sourceDigest: sourceDigest, runners: runners})
}

// packagedTree writes a verified epic-runner-only tree and points the binary
// at it (the Slice 1 shape; github-review-agent is absent on purpose).
func packagedTree(t *testing.T) string {
	t.Helper()
	return packagedTreeWith(t, fakeEpicRunner())
}

// packagedTreeWith writes a verified tree holding the given built-ins and
// points the binary at it.
func packagedTreeWith(t *testing.T, builtins ...fakeBuiltin) string {
	t.Helper()
	root := t.TempDir()
	ExpectedIndexDigest = writeFakeTreeWith(t, root, builtins...)
	t.Setenv(EnvArtifactsDir, root)
	return root
}

func lookupEpicRunner() (*Artifact, error) {
	return Lookup("epic-runner", testSourceDigest, testRunners())
}

// assertVerification checks the full VerificationError contract.
func assertVerification(t *testing.T, err error, field string) *VerificationError {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want *VerificationError with field %q", field)
	}
	var verr *VerificationError
	if !errors.As(err, &verr) {
		t.Fatalf("error = %v (%T), want *VerificationError", err, err)
	}
	if verr.Field != field {
		t.Fatalf("VerificationError.Field = %q, want %q (err: %v)", verr.Field, field, err)
	}
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("error %v does not wrap domain.ErrInvalid", err)
	}
	for _, fragment := range []string{"builtin_artifact_invalid", FailClosedGuidance, "desktop packaging error; reinstall Loom"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error %q does not contain %q", err.Error(), fragment)
		}
	}
	return verr
}

func assertNotPackaged(t *testing.T, err error, fragments ...string) {
	t.Helper()
	if !errors.Is(err, ErrNotPackaged) {
		t.Fatalf("error = %v, want errors.Is ErrNotPackaged", err)
	}
	if IsVerificationError(err) {
		t.Fatalf("error = %v must not be a VerificationError", err)
	}
	for _, fragment := range fragments {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error %q does not contain %q", err.Error(), fragment)
		}
	}
}

func flipFirstByte(t *testing.T, path string) {
	t.Helper()
	data := readFile(t, path)
	data[0] ^= 0xFF
	writeFile(t, path, data)
}

func TestLookupHappyPath(t *testing.T) {
	setup(t)
	root := packagedTree(t)
	reversed := []driver.DriverRunnerSpec{testRunners()[1], testRunners()[0]}

	art, err := Lookup("epic-runner", testSourceDigest, reversed)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	dist := filepath.Join(root, "epic-runner", "dist")
	wantArtifactDigest, err := driver.DigestDirectory(dist)
	if err != nil {
		t.Fatalf("DigestDirectory: %v", err)
	}
	if art.Name != "epic-runner" || art.Root != root || art.DistPath != dist {
		t.Fatalf("Artifact = %+v, want name/root/dist %s", art, dist)
	}
	if art.SourceDigest != testSourceDigest || art.ArtifactDigest != wantArtifactDigest {
		t.Fatalf("Artifact digests = %q/%q, want %q/%q", art.SourceDigest, art.ArtifactDigest, testSourceDigest, wantArtifactDigest)
	}
	if art.FlueCommit != testFlueCommit || art.NodeVersion != "22.20.0" || art.Target != HostTargetTriple() {
		t.Fatalf("Artifact metadata = %+v, want flue commit/node version/target from the index", art)
	}
	if art.IndexDigest != ExpectedIndexDigest || art.IndexDigest != IndexDigest(readFile(t, filepath.Join(root, IndexFileName))) {
		t.Fatalf("Artifact.IndexDigest = %q, want %q", art.IndexDigest, ExpectedIndexDigest)
	}
	if len(art.Runners) != 2 || art.Runners[0].Name != "daytona-task-runner" || art.Runners[1].Name != "local-task-runner" {
		t.Fatalf("Artifact.Runners = %+v, want sorted by name", art.Runners)
	}
}

func TestLookupTamperedServerMJS(t *testing.T) {
	setup(t)
	root := packagedTree(t)
	flipFirstByte(t, filepath.Join(root, "epic-runner", "dist", "server.mjs"))

	_, err := lookupEpicRunner()
	verr := assertVerification(t, err, "artifact_digest")
	if verr.Name != "epic-runner" || verr.Want == verr.Got {
		t.Fatalf("VerificationError = %+v, want epic-runner with differing digests", verr)
	}
}

func TestLookupTamperedIndex(t *testing.T) {
	setup(t)
	root := packagedTree(t)
	indexPath := filepath.Join(root, IndexFileName)
	original := readFile(t, indexPath)

	t.Run("byte flipped", func(t *testing.T) {
		flipFirstByte(t, indexPath)
		_, err := lookupEpicRunner()
		assertVerification(t, err, "index_digest")
	})
	t.Run("semantically equal reindent", func(t *testing.T) {
		var reindented bytes.Buffer
		if err := json.Indent(&reindented, original, "", "    "); err != nil {
			t.Fatalf("Indent: %v", err)
		}
		if bytes.Equal(reindented.Bytes(), original) {
			t.Fatal("reindented index is byte-identical; fixture is not a tamper")
		}
		writeFile(t, indexPath, reindented.Bytes())
		_, err := lookupEpicRunner()
		verr := assertVerification(t, err, "index_digest")
		if verr.Want != ExpectedIndexDigest || verr.Got != IndexDigest(reindented.Bytes()) {
			t.Fatalf("VerificationError = %+v, want expected vs on-disk digest", verr)
		}
	})
}

func TestLookupSchemaVersion(t *testing.T) {
	setup(t)
	root := packagedTree(t)
	ExpectedIndexDigest = rewriteIndex(t, root, func(idx *Index) { idx.SchemaVersion = "2" })

	_, err := lookupEpicRunner()
	verr := assertVerification(t, err, "schema_version")
	if verr.Want != SchemaVersion || verr.Got != "2" {
		t.Fatalf("VerificationError = %+v, want schema 1 vs 2", verr)
	}
}

func TestLookupWrongSourceDigest(t *testing.T) {
	setup(t)
	packagedTree(t)

	_, err := Lookup("epic-runner", "sha256:"+strings.Repeat("b", 64), testRunners())
	verr := assertVerification(t, err, "source_digest")
	if verr.Got != testSourceDigest {
		t.Fatalf("VerificationError.Got = %q, want packaged source digest", verr.Got)
	}
}

func TestLookupRunnerDrift(t *testing.T) {
	setup(t)
	packagedTree(t)
	openshell := driver.DriverRunnerSpec{Name: "openshell-task-runner", Kind: "flue-workflow", Entrypoint: "openshell-task-runner"}

	t.Run("extra runner wanted", func(t *testing.T) {
		_, err := Lookup("epic-runner", testSourceDigest, append(testRunners(), openshell))
		verr := assertVerification(t, err, "runners")
		if !strings.Contains(verr.Want, "openshell-task-runner") || strings.Contains(verr.Got, "openshell-task-runner") {
			t.Fatalf("VerificationError = %+v, want openshell only on the want side", verr)
		}
	})
	t.Run("missing runner wanted", func(t *testing.T) {
		_, err := Lookup("epic-runner", testSourceDigest, testRunners()[:1])
		assertVerification(t, err, "runners")
	})
	t.Run("no runners wanted", func(t *testing.T) {
		_, err := Lookup("epic-runner", testSourceDigest, nil)
		verr := assertVerification(t, err, "runners")
		if verr.Want != "<none>" {
			t.Fatalf("VerificationError.Want = %q, want <none>", verr.Want)
		}
	})
	t.Run("same set different order", func(t *testing.T) {
		reversed := []driver.DriverRunnerSpec{testRunners()[1], testRunners()[0]}
		if _, err := Lookup("epic-runner", testSourceDigest, reversed); err != nil {
			t.Fatalf("Lookup with reordered runners: %v", err)
		}
	})
}

func TestLookupMissingServerMJS(t *testing.T) {
	setup(t)
	root := packagedTree(t)
	if err := os.Remove(filepath.Join(root, "epic-runner", "dist", "server.mjs")); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, err := lookupEpicRunner()
	assertVerification(t, err, "server_mjs")
}

func TestLookupMissingLoomSDK(t *testing.T) {
	setup(t)
	root := packagedTree(t)
	if err := os.Remove(filepath.Join(root, "epic-runner", "dist", "node_modules", "@loom", "sdk", "package.json")); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, err := lookupEpicRunner()
	assertVerification(t, err, "loom_sdk")
}

func TestLookupInvalidEntryPath(t *testing.T) {
	setup(t)
	root := packagedTree(t)
	for _, bad := range []string{"../x", "a/b", "/abs", "", ".", ".."} {
		t.Run(bad, func(t *testing.T) {
			ExpectedIndexDigest = rewriteIndex(t, root, func(idx *Index) {
				entry := idx.Builtins["epic-runner"]
				entry.Path = bad
				idx.Builtins["epic-runner"] = entry
			})
			_, err := lookupEpicRunner()
			assertVerification(t, err, "path")
		})
	}
}

func TestLookupEntryMissing(t *testing.T) {
	setup(t)
	packagedTree(t)

	_, err := Lookup("github-review-agent", testSourceDigest, testRunners())
	assertNotPackaged(t, err, "builtin_artifact_missing", "github-review-agent")
}

func TestLookupNotPackagedBuild(t *testing.T) {
	setup(t)
	packagedTree(t)
	ExpectedIndexDigest = ""

	art, err := lookupEpicRunner()
	if art != nil {
		t.Fatalf("Lookup returned %+v on a non-packaged build", art)
	}
	assertNotPackaged(t, err, "builtin_artifact_missing")
	if IsPackagedBuild() || FailClosed() {
		t.Fatal("IsPackagedBuild/FailClosed must be false without a baked digest")
	}
}

func TestRootEnvDirWithoutIndexSkipsExeProbe(t *testing.T) {
	setup(t)
	exeDir := t.TempDir()
	ExpectedIndexDigest = writeFakeTree(t, filepath.Join(exeDir, ResourceDirName), "epic-runner", testServerBody, testSourceDigest, testRunners())
	executablePath = func() (string, error) { return filepath.Join(exeDir, "loom"), nil }
	empty := t.TempDir()
	t.Setenv(EnvArtifactsDir, empty)

	if _, err := Root(); err == nil {
		t.Fatal("Root() succeeded, want error for empty LOOM_BUILTIN_ARTIFACTS_DIR")
	} else {
		assertNotPackaged(t, err, EnvArtifactsDir, empty, IndexFileName)
	}
	_, err := lookupEpicRunner()
	assertNotPackaged(t, err, empty)

	// Sanity: without the env override the same tree is discovered.
	t.Setenv(EnvArtifactsDir, "")
	if _, err := lookupEpicRunner(); err != nil {
		t.Fatalf("Lookup via exe-relative probe: %v", err)
	}
}

func TestRootExeRelativeProbes(t *testing.T) {
	setup(t)
	t.Run("next to executable", func(t *testing.T) {
		exeDir := t.TempDir()
		want := filepath.Join(exeDir, ResourceDirName)
		ExpectedIndexDigest = writeFakeTree(t, want, "epic-runner", testServerBody, testSourceDigest, testRunners())
		executablePath = func() (string, error) { return filepath.Join(exeDir, "loom"), nil }
		root, err := Root()
		if err != nil || root != want {
			t.Fatalf("Root() = %q, %v; want %q", root, err, want)
		}
		art, err := lookupEpicRunner()
		if err != nil || art.Root != want {
			t.Fatalf("Lookup = %+v, %v; want root %q", art, err, want)
		}
	})
	t.Run("app bundle Resources", func(t *testing.T) {
		contents := t.TempDir()
		exeDir := filepath.Join(contents, "MacOS")
		want := filepath.Join(contents, "Resources", ResourceDirName)
		ExpectedIndexDigest = writeFakeTree(t, want, "epic-runner", testServerBody, testSourceDigest, testRunners())
		executablePath = func() (string, error) { return filepath.Join(exeDir, "loom"), nil }
		root, err := Root()
		if err != nil || root != want {
			t.Fatalf("Root() = %q, %v; want %q", root, err, want)
		}
	})
	t.Run("nothing anywhere", func(t *testing.T) {
		executablePath = func() (string, error) { return filepath.Join(t.TempDir(), "loom"), nil }
		if _, err := Root(); !errors.Is(err, ErrNotPackaged) {
			t.Fatalf("Root() error = %v, want ErrNotPackaged", err)
		}
	})
}

func TestFailClosedTruthTable(t *testing.T) {
	setup(t)
	cases := []struct {
		name    string
		baked   string
		runtime string
		want    bool
	}{
		{name: "neither", baked: "", runtime: "", want: false},
		{name: "baked only", baked: "sha256:abc", runtime: "", want: true},
		{name: "desktop only", baked: "", runtime: "desktop", want: true},
		{name: "both", baked: "sha256:abc", runtime: "desktop", want: true},
		{name: "other runtime", baked: "", runtime: "cli", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ExpectedIndexDigest = tc.baked
			t.Setenv(EnvLocalRuntime, tc.runtime)
			if got := FailClosed(); got != tc.want {
				t.Fatalf("FailClosed() = %v, want %v", got, tc.want)
			}
			if IsPackagedBuild() != (tc.baked != "") || IsDesktop() != (tc.runtime == "desktop") {
				t.Fatalf("IsPackagedBuild/IsDesktop = %v/%v, inconsistent with inputs", IsPackagedBuild(), IsDesktop())
			}
		})
	}
}

func describeWant() map[string]Want {
	return map[string]Want{
		"epic-runner":         {SourceDigest: testSourceDigest, Runners: testRunners()},
		"github-review-agent": {SourceDigest: testGHSourceDigest, Runners: testGHRunners()},
	}
}

func TestDescribeRequiredAndUnknown(t *testing.T) {
	setup(t)
	root := packagedTreeWith(t, fakeEpicRunner(), fakeGitHubReviewAgent())
	ExpectedIndexDigest = rewriteIndex(t, root, func(idx *Index) {
		idx.Builtins["mystery"] = Entry{Path: "mystery", SourceDigest: "sha256:m", ArtifactDigest: "sha256:a"}
	})

	report := Describe([]string{"epic-runner", "github-review-agent"}, describeWant())
	if report.Root != root || report.IndexDigest != ExpectedIndexDigest || !report.PackagedBuild || report.Desktop {
		t.Fatalf("Report = %+v, want root/index digest/packaged build", report)
	}
	if report.FlueCommit != testFlueCommit || report.NodeVersion != "22.20.0" || report.Target != HostTargetTriple() {
		t.Fatalf("Report metadata = %+v", report)
	}
	if strings.Join(report.Required, ",") != "epic-runner,github-review-agent" {
		t.Fatalf("Report.Required = %v, want both built-ins in sorted order", report.Required)
	}
	epic := report.Artifacts["epic-runner"]
	if !epic.Required || !epic.Packaged || !epic.Verified || epic.Error != "" || epic.ExpectedSourceDigest != testSourceDigest {
		t.Fatalf("epic-runner status = %+v, want required+packaged+verified", epic)
	}
	gh := report.Artifacts["github-review-agent"]
	if !gh.Required || !gh.Packaged || !gh.Verified || gh.Error != "" || gh.ExpectedSourceDigest != testGHSourceDigest {
		t.Fatalf("github-review-agent status = %+v, want required+packaged+verified", gh)
	}
	mystery := report.Artifacts["mystery"]
	if mystery.Error != "unknown built-in" || !mystery.Packaged || mystery.Verified || mystery.SourceDigest != "sha256:m" {
		t.Fatalf("mystery status = %+v, want unknown built-in", mystery)
	}
	if !report.AllRequiredVerified() {
		t.Fatalf("AllRequiredVerified() = false, report = %+v", report)
	}
}

// TestDescribeOnlyEpicRunnerPackagedIsNotReady (R1 after widening): an
// index that ships only epic-runner leaves github-review-agent required but
// unverified, so the roll-up is false even though epic-runner verifies.
func TestDescribeOnlyEpicRunnerPackagedIsNotReady(t *testing.T) {
	setup(t)
	packagedTree(t)

	report := Describe([]string{"epic-runner", "github-review-agent"}, describeWant())
	epic := report.Artifacts["epic-runner"]
	if !epic.Required || !epic.Verified {
		t.Fatalf("epic-runner status = %+v, want required+verified", epic)
	}
	gh := report.Artifacts["github-review-agent"]
	if !gh.Required || gh.Packaged || gh.Verified || !strings.Contains(gh.Error, "builtin_artifact_missing") {
		t.Fatalf("github-review-agent status = %+v, want required, not packaged, missing error", gh)
	}
	if !strings.Contains(gh.Error, FailClosedGuidance) {
		t.Fatalf("github-review-agent error %q must carry the fail-closed guidance on a packaged build", gh.Error)
	}
	if report.AllRequiredVerified() {
		t.Fatal("AllRequiredVerified() = true with github-review-agent missing")
	}
}

// TestLookupGitHubReviewAgentIndependentOfEpicRunner: each entry verifies on
// its own — a tampered epic-runner does not block github-review-agent and
// vice versa.
func TestLookupGitHubReviewAgentIndependentOfEpicRunner(t *testing.T) {
	setup(t)
	root := packagedTreeWith(t, fakeEpicRunner(), fakeGitHubReviewAgent())

	art, err := Lookup("github-review-agent", testGHSourceDigest, testGHRunners())
	if err != nil {
		t.Fatalf("Lookup(github-review-agent): %v", err)
	}
	if art.DistPath != filepath.Join(root, "github-review-agent", "dist") || len(art.Runners) != 1 || art.Runners[0].Name != "github-review-task-runner" {
		t.Fatalf("Artifact = %+v, want gh dist with its single runner", art)
	}

	flipFirstByte(t, filepath.Join(root, "epic-runner", "dist", "server.mjs"))
	if _, err := Lookup("github-review-agent", testGHSourceDigest, testGHRunners()); err != nil {
		t.Fatalf("Lookup(github-review-agent) after tampering epic-runner: %v", err)
	}
	_, err = lookupEpicRunner()
	assertVerification(t, err, "artifact_digest")
}

func TestDescribeTamperedArtifact(t *testing.T) {
	setup(t)
	root := packagedTree(t)
	flipFirstByte(t, filepath.Join(root, "epic-runner", "dist", "server.mjs"))

	report := Describe([]string{"epic-runner"}, describeWant())
	epic := report.Artifacts["epic-runner"]
	if !epic.Packaged || epic.Verified || !strings.Contains(epic.Error, "artifact_digest") {
		t.Fatalf("epic-runner status = %+v, want packaged but unverified with artifact_digest error", epic)
	}
	if report.AllRequiredVerified() {
		t.Fatal("AllRequiredVerified() = true with a tampered required artifact")
	}
}

func TestDescribeFailClosedWithoutTree(t *testing.T) {
	setup(t)
	t.Setenv(EnvLocalRuntime, "desktop")

	report := Describe([]string{"epic-runner"}, describeWant())
	if !report.Desktop || report.PackagedBuild || report.Root != "" {
		t.Fatalf("Report = %+v, want desktop without root", report)
	}
	epic := report.Artifacts["epic-runner"]
	if epic.Packaged || epic.Verified || !epic.Required {
		t.Fatalf("epic-runner status = %+v, want required and missing", epic)
	}
	for _, fragment := range []string{"builtin_artifact_missing", "desktop packaging error"} {
		if !strings.Contains(epic.Error, fragment) {
			t.Fatalf("epic-runner error %q does not contain %q", epic.Error, fragment)
		}
	}
	if report.AllRequiredVerified() {
		t.Fatal("AllRequiredVerified() = true with nothing packaged")
	}
	if strings.Join(report.Required, ",") != "epic-runner,github-review-agent" || len(report.Required) != len(RequiredBuiltins) {
		t.Fatalf("Report.Required = %v, want %v", report.Required, RequiredBuiltins)
	}
}

func TestDescribeNeverPanics(t *testing.T) {
	setup(t)
	t.Run("root is a file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "not-a-dir")
		writeFile(t, file, []byte("x"))
		ExpectedIndexDigest = "sha256:abc"
		t.Setenv(EnvArtifactsDir, file)
		report := Describe([]string{"epic-runner"}, describeWant())
		if report.Root != "" || report.Artifacts["epic-runner"].Verified {
			t.Fatalf("Report = %+v, want no root and unverified", report)
		}
	})
	t.Run("garbage index matching digest", func(t *testing.T) {
		root := t.TempDir()
		garbage := []byte("{not json")
		writeFile(t, filepath.Join(root, IndexFileName), garbage)
		ExpectedIndexDigest = IndexDigest(garbage)
		t.Setenv(EnvArtifactsDir, root)
		report := Describe([]string{"epic-runner"}, describeWant())
		epic := report.Artifacts["epic-runner"]
		if report.Root != root || report.IndexDigest != ExpectedIndexDigest || epic.Verified {
			t.Fatalf("Report = %+v, want root and index digest but unverified", report)
		}
		if !strings.Contains(epic.Error, "index_json") {
			t.Fatalf("epic-runner error = %q, want index_json", epic.Error)
		}
	})
	t.Run("garbage index wrong digest", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, IndexFileName), []byte("{not json"))
		ExpectedIndexDigest = "sha256:abc"
		t.Setenv(EnvArtifactsDir, root)
		report := Describe([]string{"epic-runner"}, describeWant())
		if !strings.Contains(report.Artifacts["epic-runner"].Error, "index_digest") {
			t.Fatalf("Report = %+v, want index_digest error", report)
		}
	})
}

func TestEncodeIndexStable(t *testing.T) {
	unsorted := []driver.DriverRunnerSpec{
		{Name: "local-task-runner", Kind: "flue-workflow", Entrypoint: "local-task-runner"},
		{Name: "  daytona-task-runner ", Kind: "flue-workflow", Entrypoint: "daytona-task-runner"},
		{Name: "", Kind: "flue-workflow", Entrypoint: "dropped"},
	}
	idx := Index{SchemaVersion: SchemaVersion, FlueCommit: testFlueCommit, NodeVersion: "22.20.0", Target: "t", Builtins: map[string]Entry{
		"zeta":  {Path: "zeta", Runners: unsorted},
		"alpha": {Path: "alpha", Runners: nil},
	}}
	first, err := EncodeIndex(idx)
	if err != nil {
		t.Fatalf("EncodeIndex: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := EncodeIndex(idx)
		if err != nil || !bytes.Equal(first, again) {
			t.Fatalf("EncodeIndex call %d differs (err %v):\n%s\n---\n%s", i, err, first, again)
		}
	}
	if !bytes.HasSuffix(first, []byte("\n")) || bytes.HasSuffix(first, []byte("\n\n")) {
		t.Fatalf("EncodeIndex output must end with exactly one newline: %q", first)
	}
	text := string(first)
	if strings.Index(text, `"alpha"`) > strings.Index(text, `"zeta"`) {
		t.Fatalf("builtins not sorted by name:\n%s", text)
	}
	if strings.Index(text, "daytona-task-runner") > strings.Index(text, "local-task-runner") || strings.Contains(text, "dropped") {
		t.Fatalf("runners not normalized and sorted:\n%s", text)
	}
	if !strings.Contains(text, `"runners": []`) {
		t.Fatalf("nil runners must encode as an empty list:\n%s", text)
	}
	if !strings.HasPrefix(text, "{\n  \"schema_version\": \"1\",\n  \"flue_commit\"") {
		t.Fatalf("unexpected canonical layout:\n%s", text)
	}
}

func TestHostTargetTriple(t *testing.T) {
	if got := HostTargetTriple(); strings.TrimSpace(got) == "" {
		t.Fatal("HostTargetTriple() is empty")
	}
}

func TestVerificationErrorContract(t *testing.T) {
	err := &VerificationError{Name: "epic-runner", Field: "index_digest", Want: "a", Got: "b"}
	assertVerification(t, err, "index_digest")
	if !IsVerificationError(err) {
		t.Fatal("IsVerificationError = false")
	}
	if errors.Is(err, ErrNotPackaged) || errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("VerificationError %v must not match ErrNotPackaged or ErrNotFound", err)
	}
	if errors.Is(ErrNotPackaged, domain.ErrNotFound) {
		t.Fatal("ErrNotPackaged must not wrap domain.ErrNotFound")
	}
	if !strings.Contains(err.Error(), "epic-runner") || !strings.Contains(err.Error(), "want a, got b") {
		t.Fatalf("VerificationError message = %q", err.Error())
	}
}
