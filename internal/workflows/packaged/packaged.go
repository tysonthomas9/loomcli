// Package packaged implements the packaged built-in workflow artifact contract
// (DEV-V5-31 rev-2): a `builtin-workflows/` resource tree — `index.json` plus
// one pre-built Flue `dist/` per built-in — that a packaged Loom build verifies
// and registers WITHOUT invoking the compiler.
//
// Trust chain: the `-ldflags -X`-baked ExpectedIndexDigest pins the raw bytes
// of index.json; the index pins each artifact's directory digest, source
// digest, and runner set. Lookup verifies the whole chain before anything is
// staged. Import direction: this package imports driver (+ domain) and must
// never import workflows; driver must never import this package.
package packaged

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
)

// ExpectedIndexDigest is baked by the release build:
//
//	-ldflags "-X github.com/tysonthomas9/loomcli/internal/workflows/packaged.ExpectedIndexDigest=sha256:<hex>"
//
// Non-empty means "this is a packaged build". It cannot be set by env.
var ExpectedIndexDigest string

// RequiredBuiltins are the built-ins a desktop/packaged build must ship for
// builtin_runtime_ready to be true. String literals: this package must not
// import workflows. Adding a desktop built-in = append here AND package it.
var RequiredBuiltins = []string{"epic-runner"}

const (
	// SchemaVersion is the only index.json schema this binary accepts.
	SchemaVersion = "1"
	// EnvArtifactsDir overrides the resource root (developer escape hatch).
	// Root and Describe honor it on any build so readyz can report the tree;
	// Lookup only verifies/uses it when ExpectedIndexDigest is baked in.
	EnvArtifactsDir = "LOOM_BUILTIN_ARTIFACTS_DIR"
	// EnvLocalRuntime is the desktop launcher's marker (value "desktop").
	EnvLocalRuntime = "LOOM_LOCAL_RUNTIME"
	// ProvenancePackagedBuiltin is stamped on registrations from this lane.
	ProvenancePackagedBuiltin = "packaged_builtin"
	// IndexFileName is the index file at the resource root.
	IndexFileName = "index.json"
	// ResourceDirName is the resource root directory name probed next to the
	// executable and under the app bundle's Resources directory.
	ResourceDirName = "builtin-workflows"
	// FailClosedGuidance is the operator-facing suffix every fail-closed
	// message carries (DEV-V5-31 §3c).
	FailClosedGuidance = "desktop packaging error; reinstall Loom"
)

// LoomSDKRuntimeFiles are the @loom/sdk runtime files a packaged dist ships
// under dist/node_modules/@loom/sdk (the SDK is EXTERNAL in server.mjs).
var LoomSDKRuntimeFiles = []string{"package.json", "index.js", "internal.js", "driver.js", "runner.js", "runtime-adapters.js"}

// ErrNotPackaged is the PLAIN sentinel for "nothing to verify here". It must
// not wrap domain.ErrNotFound: a fail-closed error chaining it has to surface
// its real message (HTTP 500), not collapse to a 404 "workflow not found".
var ErrNotPackaged = errors.New("builtin_artifact_missing: no packaged built-in workflow artifacts in this build")

// VerificationError is a failed artifact check. It is fatal in every mode —
// a caller must never fall back to compiling on one.
type VerificationError struct {
	Name  string
	Field string
	Want  string
	Got   string
}

func (e *VerificationError) Error() string {
	return fmt.Sprintf("builtin_artifact_invalid: %s: %s mismatch (want %s, got %s) — %s", e.Name, e.Field, e.Want, e.Got, FailClosedGuidance)
}

func (e *VerificationError) Unwrap() error { return domain.ErrInvalid }

// Index is index.json (schema 1). Field order is the canonical encoding order.
type Index struct {
	SchemaVersion string           `json:"schema_version"`
	FlueCommit    string           `json:"flue_commit"`
	NodeVersion   string           `json:"node_version"`
	Target        string           `json:"target"`
	Builtins      map[string]Entry `json:"builtins"`
}

// Entry is one packaged built-in.
type Entry struct {
	Path           string                    `json:"path"`
	Entrypoint     string                    `json:"entrypoint"`
	SourceDigest   string                    `json:"source_digest"`
	ArtifactDigest string                    `json:"artifact_digest"`
	Runners        []driver.DriverRunnerSpec `json:"runners"`
}

// Artifact is a verified packaged built-in ready for registration.
type Artifact struct {
	Name           string
	Root           string
	DistPath       string
	SourceDigest   string
	ArtifactDigest string
	FlueCommit     string
	NodeVersion    string
	IndexDigest    string
	Target         string
	Runners        []driver.DriverRunnerSpec
}

// executablePath is a seam for tests.
var executablePath = os.Executable

// IsDesktop reports whether the desktop launcher started this process.
func IsDesktop() bool { return os.Getenv(EnvLocalRuntime) == "desktop" }

// IsPackagedBuild reports whether an index digest was baked into this binary.
func IsPackagedBuild() bool { return strings.TrimSpace(ExpectedIndexDigest) != "" }

// FailClosed is the dual-keyed policy: a packaged build OR a desktop process
// must never reach the compiler.
func FailClosed() bool { return IsPackagedBuild() || IsDesktop() }

// IsRequired reports whether name is in RequiredBuiltins.
func IsRequired(name string) bool {
	for _, required := range RequiredBuiltins {
		if required == name {
			return true
		}
	}
	return false
}

// IndexDigest is the raw-byte digest of index.json that ExpectedIndexDigest pins.
func IndexDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// EncodeIndex produces the canonical index.json bytes: two-space indent, no
// HTML escaping, builtins sorted by name (map keys), runners normalized and
// sorted by name, and exactly one trailing newline. Identical inputs yield
// byte-identical output.
func EncodeIndex(idx Index) ([]byte, error) {
	canonical := Index{
		SchemaVersion: idx.SchemaVersion,
		FlueCommit:    idx.FlueCommit,
		NodeVersion:   idx.NodeVersion,
		Target:        idx.Target,
		Builtins:      make(map[string]Entry, len(idx.Builtins)),
	}
	for name, entry := range idx.Builtins {
		entry.Runners = sortedRunners(entry.Runners)
		canonical.Builtins[name] = entry
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(canonical); err != nil {
		return nil, fmt.Errorf("encode builtin index: %w", err)
	}
	return buf.Bytes(), nil
}

func sortedRunners(in []driver.DriverRunnerSpec) []driver.DriverRunnerSpec {
	out := driver.NormalizeDriverRunnerSpecs(in)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if out == nil {
		out = []driver.DriverRunnerSpec{}
	}
	return out
}

func renderRunners(runners []driver.DriverRunnerSpec) string {
	parts := make([]string, 0, len(runners))
	for _, runner := range runners {
		parts = append(parts, runner.Name+":"+runner.Kind+":"+runner.Entrypoint)
	}
	if len(parts) == 0 {
		return "<none>"
	}
	return strings.Join(parts, ",")
}

func equalRunnerSets(a, b []driver.DriverRunnerSpec) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Root locates the builtin-workflows resource root. An explicit
// LOOM_BUILTIN_ARTIFACTS_DIR that lacks index.json is an error naming the
// dir — it never silently falls back to the executable-relative probes.
func Root() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(EnvArtifactsDir)); dir != "" {
		if !regularFile(filepath.Join(dir, IndexFileName)) {
			return "", fmt.Errorf("builtin_artifact_missing: %s=%s has no %s: %w", EnvArtifactsDir, dir, IndexFileName, ErrNotPackaged)
		}
		return dir, nil
	}
	for _, candidate := range exeRelativeRoots() {
		if regularFile(filepath.Join(candidate, IndexFileName)) {
			return candidate, nil
		}
	}
	return "", ErrNotPackaged
}

func exeRelativeRoots() []string {
	exe, err := executablePath()
	if err != nil || strings.TrimSpace(exe) == "" {
		return nil
	}
	exeDir := filepath.Dir(exe)
	return []string{
		filepath.Join(exeDir, ResourceDirName),
		filepath.Join(exeDir, "..", "Resources", ResourceDirName),
		filepath.Join(exeDir, "..", "Resources", "resources", ResourceDirName),
	}
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// validEntryPath accepts only a single plain path component.
func validEntryPath(path string) bool {
	if path == "" || path == "." || path == ".." {
		return false
	}
	if strings.ContainsAny(path, `/\`) || filepath.IsAbs(path) {
		return false
	}
	return path == filepath.Base(path)
}

// readIndex reads and digest-checks the raw index at root.
func readIndex(root, name string) ([]byte, *Index, error) {
	raw, err := os.ReadFile(filepath.Join(root, IndexFileName)) //nolint:gosec // root is the resolved resource root.
	if err != nil {
		return nil, nil, &VerificationError{Name: name, Field: "index_json", Want: "readable", Got: err.Error()}
	}
	if got := IndexDigest(raw); got != strings.TrimSpace(ExpectedIndexDigest) {
		return raw, nil, &VerificationError{Name: name, Field: "index_digest", Want: strings.TrimSpace(ExpectedIndexDigest), Got: got}
	}
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return raw, nil, &VerificationError{Name: name, Field: "index_json", Want: "parseable", Got: err.Error()}
	}
	if idx.SchemaVersion != SchemaVersion {
		return raw, &idx, &VerificationError{Name: name, Field: "schema_version", Want: SchemaVersion, Got: idx.SchemaVersion}
	}
	return raw, &idx, nil
}

// Lookup verifies the packaged artifact for name against this binary's
// expectations: index digest → schema → entry → path → server.mjs → nested
// @loom/sdk → artifact digest → source digest → runner set. It returns
// ErrNotPackaged (possibly wrapped) when there is nothing to verify, a
// *VerificationError when something is wrong, and the Artifact otherwise.
// Verification completes BEFORE anything is staged, so a tampered tree is
// never copied.
func Lookup(name, wantSourceDigest string, wantRunners []driver.DriverRunnerSpec) (*Artifact, error) {
	if !IsPackagedBuild() {
		if root, err := Root(); err == nil {
			slog.Debug("packaged built-in artifacts present but this binary carries no expected index digest", "root", root)
		}
		return nil, ErrNotPackaged
	}
	root, err := Root()
	if err != nil {
		return nil, err
	}
	raw, idx, err := readIndex(root, name)
	if err != nil {
		return nil, err
	}
	entry, ok := idx.Builtins[name]
	if !ok {
		return nil, fmt.Errorf("builtin_artifact_missing: %s is not in the packaged index: %w", name, ErrNotPackaged)
	}
	dist, artifactDigest, err := verifyEntryTree(name, root, entry)
	if err != nil {
		return nil, err
	}
	if entry.SourceDigest != wantSourceDigest {
		return nil, &VerificationError{Name: name, Field: "source_digest", Want: wantSourceDigest, Got: entry.SourceDigest}
	}
	have, want := sortedRunners(entry.Runners), sortedRunners(wantRunners)
	if !equalRunnerSets(have, want) {
		return nil, &VerificationError{Name: name, Field: "runners", Want: renderRunners(want), Got: renderRunners(have)}
	}
	return &Artifact{
		Name:           name,
		Root:           root,
		DistPath:       dist,
		SourceDigest:   entry.SourceDigest,
		ArtifactDigest: artifactDigest,
		FlueCommit:     idx.FlueCommit,
		NodeVersion:    idx.NodeVersion,
		IndexDigest:    IndexDigest(raw),
		Target:         idx.Target,
		Runners:        have,
	}, nil
}

// verifyEntryTree checks the entry path, the dist layout (server.mjs + nested
// @loom/sdk), and the artifact digest; it returns the dist dir and its digest.
func verifyEntryTree(name, root string, entry Entry) (dist, artifactDigest string, err error) {
	if !validEntryPath(entry.Path) {
		return "", "", &VerificationError{Name: name, Field: "path", Want: "single plain path component", Got: fmt.Sprintf("%q", entry.Path)}
	}
	dist = filepath.Join(root, entry.Path, "dist")
	if !regularFile(filepath.Join(dist, "server.mjs")) {
		return "", "", &VerificationError{Name: name, Field: "server_mjs", Want: "regular file", Got: "missing"}
	}
	if !regularFile(filepath.Join(dist, "node_modules", "@loom", "sdk", "package.json")) {
		return "", "", &VerificationError{Name: name, Field: "loom_sdk", Want: "dist/node_modules/@loom/sdk/package.json", Got: "missing"}
	}
	artifactDigest, err = driver.DigestDirectory(dist)
	if err != nil {
		return "", "", &VerificationError{Name: name, Field: "artifact_digest", Want: entry.ArtifactDigest, Got: err.Error()}
	}
	if artifactDigest != entry.ArtifactDigest {
		return "", "", &VerificationError{Name: name, Field: "artifact_digest", Want: entry.ArtifactDigest, Got: artifactDigest}
	}
	return dist, artifactDigest, nil
}

// HostTargetTriple maps the running GOOS/GOARCH onto the sidecar triple
// convention used by desktop/scripts/prepare-sidecar.sh.
func HostTargetTriple() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "aarch64-apple-darwin"
	case "darwin/amd64":
		return "x86_64-apple-darwin"
	case "linux/amd64":
		return "x86_64-unknown-linux-gnu"
	case "linux/arm64":
		return "aarch64-unknown-linux-gnu"
	case "windows/amd64":
		return "x86_64-pc-windows-msvc"
	}
	return runtime.GOARCH + "-" + runtime.GOOS
}
