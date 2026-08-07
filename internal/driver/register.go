package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

const (
	NativeFlueSchemaVersion = "3"
	NativeFlueArtifactKind  = "flue-node-artifact"
	LoomDriverSDKPackage    = "@loom/sdk/driver"
	LoomDriverSDKVersion    = "0.1.0"

	RunnerKindFlueWorkflow = "flue-workflow"
	RunnerKindNodeModule   = "node-module"

	driverRunnersManifestKey = "runners"
	ManifestTrustLevelKey    = "trust_level"
)

type RegisterFlueOptions struct {
	WorkspaceKey string
	WorkDir      string
	DistPath     string
	ManifestPath string
	DriverName   string
	DriverID     string
	WorkflowName string
	SourceRef    string
	SourceDigest string
	CreatedBy    string
	Activate     bool
	RunnerSpecs  []DriverRunnerSpec
	// Manifest adds server-side provenance fields to the generated native
	// manifest. Values are merged before generated runtime fields are stamped;
	// generated fields and server-stamped trust still win.
	Manifest map[string]string
	// BuildDiagnostics carries redacted build output for successful generated
	// workflow builds. It is persisted on DriverVersion; failed builds never
	// reach registration.
	BuildDiagnostics string
	// Trust is the trust level the SERVER stamps on the driver row (§7 step 9
	// sandbox placement policy) — it is never read from client input or the
	// bundle manifest, so a submission cannot self-elevate. Empty defaults to
	// trusted: calling RegisterFlueDriver directly is the operator/source-tree
	// path (CLI `loom driver register`). External submission paths (the
	// workflows HTTP API via workflows.BuildAndRegister) must stamp
	// workflowcatalog.DriverTrustUntrusted explicitly.
	Trust workflowcatalog.DriverTrustLevel
}

type RegisterFlueResult struct {
	Driver         *workflowcatalog.Driver        `json:"driver"`
	Version        *workflowcatalog.DriverVersion `json:"version"`
	Bundle         *Bundle                        `json:"bundle,omitempty"`
	CreatedDriver  bool                           `json:"created_driver"`
	CreatedVersion bool                           `json:"created_version"`
	ReusedVersion  bool                           `json:"reused_version"`
	Activated      bool                           `json:"activated"`
}

// StagedFlueRegistration is the persistence-free output of preparing one
// native Flue bundle. CatalogManifest deliberately excludes trust_level:
// Workflow Catalog selects trust from its typed operator/system authority
// lane, while the on-disk manifest is stamped with the matching server-selected
// trust so the runtime bundle and durable version remain content-identical.
//
// Promote must succeed before submitting the immutable metadata through
// Workflow Catalog. A promoted bundle may remain as an idempotent orphan when
// the durable command fails; retrying the same content safely replaces it.
type StagedFlueRegistration struct {
	DriverName       string
	DriverID         string
	VersionID        string
	SourceRef        string
	SourceDigest     string
	BundleRef        string
	BundleDigest     string
	Runtime          string
	CatalogManifest  map[string]string
	BuildDiagnostics string
	Bundle           *Bundle

	staged *stagedFlueBundle
}

// StageFlueDriverBundle validates and content-addresses a built distribution
// without reading or mutating Driver/DriverVersion persistence.
func StageFlueDriverBundle(opts RegisterFlueOptions) (*StagedFlueRegistration, error) {
	if strings.TrimSpace(opts.WorkspaceKey) == "" {
		return nil, fmt.Errorf("workspace key required: %w", domain.ErrInvalid)
	}
	reg, err := resolveFlueRegistrationInput(opts)
	if err != nil {
		return nil, err
	}
	staged, err := stageFlueBundle(reg)
	if err != nil {
		if staged != nil {
			_ = os.RemoveAll(staged.tmpRoot)
		}
		return nil, err
	}
	catalogManifest := cloneStringMap(staged.manifest)
	delete(catalogManifest, ManifestTrustLevelKey)
	return &StagedFlueRegistration{
		DriverName: reg.driverName, DriverID: reg.driverID, VersionID: staged.versionID,
		SourceRef: reg.sourceRef, SourceDigest: reg.sourceDigest,
		BundleRef: staged.bundleRef, BundleDigest: staged.bundleDigest,
		Runtime: RuntimeFlueNode, CatalogManifest: catalogManifest,
		BuildDiagnostics: opts.BuildDiagnostics,
		Bundle: &Bundle{
			Root: staged.finalRoot, BundleRef: staged.bundleRef,
			SourceRef: reg.sourceRef, SourceDigest: reg.sourceDigest,
			BundleDigest: staged.bundleDigest, Manifest: cloneStringMap(staged.manifest),
		},
		staged: staged,
	}, nil
}

// Promote makes the staged content available at its durable relative
// BundleRef. It is idempotent for a successfully promoted instance.
func (staged *StagedFlueRegistration) Promote() error {
	if staged == nil || staged.staged == nil {
		return fmt.Errorf("staged Flue registration is required: %w", domain.ErrInvalid)
	}
	if staged.staged.tmpRoot == "" {
		return nil
	}
	if err := promoteFlueBundle(staged.staged.tmpRoot, staged.staged.finalRoot); err != nil {
		return err
	}
	staged.staged.tmpRoot = ""
	return nil
}

// Cleanup removes only an unpromoted temporary bundle.
func (staged *StagedFlueRegistration) Cleanup() {
	if staged == nil || staged.staged == nil || staged.staged.tmpRoot == "" {
		return
	}
	_ = os.RemoveAll(staged.staged.tmpRoot)
	staged.staged.tmpRoot = ""
}

type DriverRunnerSpec struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Entrypoint string `json:"entrypoint"`
}

func encodeDriverRunnerManifest(runners []DriverRunnerSpec) string {
	normalized := normalizeDriverRunnerSpecs(runners)
	if len(normalized) == 0 {
		return ""
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return ""
	}
	return string(data)
}

func decodeDriverRunnerManifest(manifest map[string]string) ([]DriverRunnerSpec, error) {
	raw := strings.TrimSpace(manifest[driverRunnersManifestKey])
	if raw == "" {
		return nil, nil
	}
	var runners []DriverRunnerSpec
	if err := json.Unmarshal([]byte(raw), &runners); err != nil {
		return nil, fmt.Errorf("decode driver runners manifest: %w", err)
	}
	runners = normalizeDriverRunnerSpecs(runners)
	if len(runners) == 0 {
		return nil, fmt.Errorf("driver runners manifest has no usable runner entries: %w", domain.ErrInvalid)
	}
	if err := validateDriverRunnerSpecs(runners); err != nil {
		return nil, fmt.Errorf("invalid driver runners manifest: %w", err)
	}
	return runners, nil
}

func normalizeDriverRunnerSpecs(in []DriverRunnerSpec) []DriverRunnerSpec {
	out := make([]DriverRunnerSpec, 0, len(in))
	seen := map[string]struct{}{}
	for _, runner := range in {
		runner.Name = strings.TrimSpace(runner.Name)
		runner.Kind = strings.TrimSpace(runner.Kind)
		runner.Entrypoint = strings.TrimSpace(runner.Entrypoint)
		if runner.Name == "" || runner.Kind == "" || runner.Entrypoint == "" {
			continue
		}
		if _, ok := seen[runner.Name]; ok {
			continue
		}
		seen[runner.Name] = struct{}{}
		out = append(out, runner)
	}
	return out
}

func NormalizeDriverRunnerSpecs(in []DriverRunnerSpec) []DriverRunnerSpec {
	return normalizeDriverRunnerSpecs(in)
}

// validDriverRunnerKinds is the closed set of runner kinds a driver manifest may
// declare (§4.6).
var validDriverRunnerKinds = map[string]struct{}{
	RunnerKindFlueWorkflow: {},
	RunnerKindNodeModule:   {},
}

// validateDriverRunnerSpecs rejects malformed runner specs (§4.6): empty
// name/kind/entrypoint, duplicate names, unknown kinds, and unsafe entrypoints
// (absolute paths or path traversal). It runs on the normalized list so the
// caller sees the same view the runtime will resolve against.
func validateDriverRunnerSpecs(runners []DriverRunnerSpec) error {
	seen := map[string]struct{}{}
	for _, runner := range runners {
		name := strings.TrimSpace(runner.Name)
		kind := strings.TrimSpace(runner.Kind)
		entrypoint := strings.TrimSpace(runner.Entrypoint)
		if name == "" {
			return fmt.Errorf("driver runner spec has empty name: %w", domain.ErrInvalid)
		}
		if kind == "" {
			return fmt.Errorf("driver runner %q has empty kind: %w", name, domain.ErrInvalid)
		}
		if entrypoint == "" {
			return fmt.Errorf("driver runner %q has empty entrypoint: %w", name, domain.ErrInvalid)
		}
		if _, ok := validDriverRunnerKinds[kind]; !ok {
			return fmt.Errorf("driver runner %q has unknown kind %q: %w", name, kind, domain.ErrInvalid)
		}
		if err := validateRunnerEntrypoint(name, entrypoint); err != nil {
			return err
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("driver runner name %q is declared more than once: %w", name, domain.ErrInvalid)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func ValidateDriverRunnerSpecs(runners []DriverRunnerSpec) error {
	return validateDriverRunnerSpecs(normalizeDriverRunnerSpecs(runners))
}

// validateRunnerEntrypoint rejects unsafe runner entrypoints: absolute paths and
// path traversal escape the bundle root and are never legitimate.
func validateRunnerEntrypoint(name, entrypoint string) error {
	if filepath.IsAbs(entrypoint) || strings.HasPrefix(entrypoint, "/") {
		return fmt.Errorf("driver runner %q entrypoint %q must be relative: %w", name, entrypoint, domain.ErrInvalid)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(entrypoint)))
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return fmt.Errorf("driver runner %q entrypoint %q must not contain path traversal: %w", name, entrypoint, domain.ErrInvalid)
		}
	}
	return nil
}

// OpenShellRunnerName is the fail-closed OpenShell task runner. No real
// OpenShell integration exists; the runner is excluded from manifests and
// guarded at resolve time so it can never select a fake completed path (§4.6).
const OpenShellRunnerName = "openshell-task-runner"

// ErrOpenShellRunnerUnimplemented carries the openshell_runner_unimplemented
// error class (§4.5) for the resolve-time guard.
var ErrOpenShellRunnerUnimplemented = errors.New("openshell_runner_unimplemented: openshell-task-runner is not implemented")

// ErrRunnerNotDeclared marks the specific "this driver version's manifest does
// not declare the runner" failure, distinct from malformed manifests or the
// OpenShell guard. It is the ONLY resolveDriverRunner failure that is allowed to
// trigger the workspace-global builtin fallback (GAP A, global_runner.go): every
// other failure fails closed with no fallback.
var ErrRunnerNotDeclared = errors.New("driver runner not declared by version")

func resolveDriverRunner(version *workflowcatalog.DriverVersion, runnerName string) (DriverRunnerSpec, error) {
	runnerName = strings.TrimSpace(runnerName)
	if runnerName == "" {
		return DriverRunnerSpec{}, nil
	}
	if runnerName == OpenShellRunnerName {
		return DriverRunnerSpec{}, fmt.Errorf("runner %q: %w: %w", runnerName, ErrOpenShellRunnerUnimplemented, domain.ErrInvalid)
	}
	if version == nil {
		return DriverRunnerSpec{}, fmt.Errorf("driver version required to resolve runner %q: %w", runnerName, domain.ErrInvalid)
	}
	runners, err := decodeDriverRunnerManifest(version.Manifest)
	if err != nil {
		return DriverRunnerSpec{}, err
	}
	for _, runner := range runners {
		if runner.Name == runnerName {
			return runner, nil
		}
	}
	// ErrRunnerNotDeclared distinguishes this case for the global fallback;
	// domain.ErrInvalid preserves the existing error class/HTTP mapping.
	return DriverRunnerSpec{}, fmt.Errorf("runner %q is not declared by driver version %q: %w: %w", runnerName, version.VersionID, ErrRunnerNotDeclared, domain.ErrInvalid)
}

func applyResolvedRunner(opts TaskRunRequestOptions, parent *domain.DriverRun, version *workflowcatalog.DriverVersion) (TaskRunRequestOptions, error) {
	opts.Runner = strings.TrimSpace(opts.Runner)
	if opts.Runner == "" {
		return opts, nil
	}
	spec, err := resolveDriverRunner(version, opts.Runner)
	if err != nil {
		return opts, err
	}
	opts.RunnerKind = spec.Kind
	opts.RunnerEntrypoint = spec.Entrypoint
	opts.RunnerVersionID = version.VersionID
	opts.RunnerRef = resolvedRunnerRef(parent, version, spec)
	return opts, nil
}

func resolvedRunnerRef(parent *domain.DriverRun, version *workflowcatalog.DriverVersion, spec DriverRunnerSpec) string {
	driverID := ""
	if version != nil {
		driverID = version.DriverID
	}
	if driverID == "" && parent != nil {
		driverID = parent.DriverID
	}
	versionID := ""
	if version != nil {
		versionID = version.VersionID
	}
	return strings.Join([]string{driverID, versionID, spec.Kind, spec.Entrypoint}, "#")
}

func inferNodeModuleRunnerName(path string) string {
	name := strings.TrimSuffix(filepath.Base(strings.TrimSpace(path)), filepath.Ext(path))
	return strings.TrimSpace(name)
}

type flueRegistrationInput struct {
	absWorkDir   string
	absDist      string
	manifest     map[string]string
	driverName   string
	driverID     string
	workflowName string
	sourceRef    string
	sourceDigest string
	runnerSpecs  []DriverRunnerSpec
	trust        workflowcatalog.DriverTrustLevel
}

func resolveFlueRegistrationInput(opts RegisterFlueOptions) (*flueRegistrationInput, error) {
	absWorkDir, absDist, err := resolveFlueDistPath(opts.WorkDir, opts.DistPath)
	if err != nil {
		return nil, err
	}
	externalManifest, err := readRegistrationManifest(absWorkDir, absDist, opts.ManifestPath)
	if err != nil {
		return nil, err
	}
	for key, value := range opts.Manifest {
		externalManifest[key] = value
	}
	driverName := firstNonEmpty(opts.DriverName, externalManifest["driver_name"], externalManifest["name"])
	if driverName == "" {
		driverName = strings.TrimSuffix(filepath.Base(absWorkDir), filepath.Ext(absWorkDir))
	}
	driverID := firstNonEmpty(opts.DriverID, externalManifest["driver_id"], slug(driverName))
	if driverID == "" {
		return nil, fmt.Errorf("driver name %q does not contain a usable id: %w", driverName, domain.ErrInvalid)
	}
	if err := validateFlueDriverID(driverID); err != nil {
		return nil, err
	}
	workflowName := firstNonEmpty(opts.WorkflowName, externalManifest["workflow_name"], driverID)
	if workflowName == "" {
		return nil, fmt.Errorf("workflow name required: %w", domain.ErrInvalid)
	}
	runnerSpecs := normalizeDriverRunnerSpecs(opts.RunnerSpecs)
	if err := validateDriverRunnerSpecs(runnerSpecs); err != nil {
		return nil, err
	}
	return &flueRegistrationInput{
		absWorkDir:   absWorkDir,
		absDist:      absDist,
		manifest:     externalManifest,
		driverName:   driverName,
		driverID:     driverID,
		workflowName: workflowName,
		sourceRef:    firstNonEmpty(opts.SourceRef, externalManifest["source_ref"], relativeRef(absWorkDir, absDist)),
		sourceDigest: firstNonEmpty(opts.SourceDigest, externalManifest["source_digest"]),
		runnerSpecs:  runnerSpecs,
		trust:        registrationTrust(opts.Trust),
	}, nil
}

// validateFlueDriverID keeps the durable identity a single filesystem path
// segment before stageFlueBundle derives BundleRef and finalRoot from it.
// Workflow Catalog validates the durable identifier again at its boundary,
// but that happens after promotion and therefore cannot protect local disk.
func validateFlueDriverID(driverID string) error {
	if driverID == "" || driverID != strings.TrimSpace(driverID) ||
		driverID == "." || driverID == ".." ||
		strings.ContainsAny(driverID, `/\:`) ||
		strings.ContainsRune(driverID, '\x00') {
		return fmt.Errorf("driver id %q must be a canonical path segment: %w", driverID, domain.ErrInvalid)
	}
	return nil
}

func resolveFlueDistPath(workDir, distPath string) (string, string, error) {
	if workDir == "" {
		workDir = "."
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve work dir: %w", err)
	}
	distPath = strings.TrimSpace(distPath)
	if distPath == "" {
		return "", "", fmt.Errorf("flue dist path required: %w", domain.ErrInvalid)
	}
	if !filepath.IsAbs(distPath) {
		distPath = filepath.Join(absWorkDir, distPath)
	}
	absDist, err := filepath.Abs(distPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve flue dist path: %w", err)
	}
	if info, err := os.Stat(absDist); err != nil {
		return "", "", fmt.Errorf("stat flue dist path: %w", err)
	} else if !info.IsDir() {
		return "", "", fmt.Errorf("flue dist path must be a directory: %w", domain.ErrInvalid)
	}
	if info, err := os.Stat(filepath.Join(absDist, "server.mjs")); err != nil {
		return "", "", fmt.Errorf("flue dist missing server.mjs: %w", err)
	} else if info.IsDir() {
		return "", "", fmt.Errorf("flue dist server.mjs is a directory: %w", domain.ErrInvalid)
	}
	return absWorkDir, absDist, nil
}

type stagedFlueBundle struct {
	tmpRoot      string
	finalRoot    string
	versionID    string
	bundleRef    string
	bundleDigest string
	manifest     map[string]string
}

// stageFlueBundle copies the dist tree into a temporary bundle root and
// computes digests and identifiers. On errors after the temporary root has
// been created it returns a non-nil bundle so the caller can clean it up.
func stageFlueBundle(reg *flueRegistrationInput) (*stagedFlueBundle, error) {
	driverRoot := filepath.Join(reg.absWorkDir, ".loom", "drivers")
	if err := os.MkdirAll(driverRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create driver bundle root: %w", err)
	}
	tmpRoot, err := os.MkdirTemp(driverRoot, ".register-flue-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary driver bundle: %w", err)
	}
	staged := &stagedFlueBundle{tmpRoot: tmpRoot}
	if err := copyTree(reg.absDist, filepath.Join(tmpRoot, "dist")); err != nil {
		return staged, err
	}
	artifactDigest, err := digestDirectory(filepath.Join(tmpRoot, "dist"))
	if err != nil {
		return staged, err
	}
	if reg.sourceDigest == "" {
		reg.sourceDigest = artifactDigest
	}
	staged.manifest = nativeFlueManifest(reg.driverID, reg.driverName, reg.workflowName, reg.sourceRef, reg.sourceDigest, artifactDigest, reg.manifest, reg.runnerSpecs)
	staged.manifest[ManifestTrustLevelKey] = string(reg.trust)
	manifestBytes, err := writeFlueBundleManifest(tmpRoot, staged.manifest)
	if err != nil {
		return staged, err
	}
	staged.bundleDigest, err = digestBundleTree(tmpRoot, manifestBytes)
	if err != nil {
		return staged, err
	}
	staged.versionID = reg.driverID + "-v-" + digestShort(staged.bundleDigest)
	staged.bundleRef = filepath.ToSlash(filepath.Join(".loom", "drivers", reg.driverID, staged.versionID))
	staged.finalRoot = filepath.Join(reg.absWorkDir, filepath.FromSlash(staged.bundleRef))
	return staged, nil
}

func writeFlueBundleManifest(tmpRoot string, manifest map[string]string) ([]byte, error) {
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode native Flue manifest: %w", err)
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := os.WriteFile(filepath.Join(tmpRoot, "manifest.json"), manifestBytes, 0o644); err != nil {
		return nil, fmt.Errorf("write native Flue manifest: %w", err)
	}
	return manifestBytes, nil
}

func registeredBundleMissing(root string) bool {
	for _, rel := range []string{"manifest.json", filepath.Join("dist", "server.mjs")} {
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil || info.IsDir() {
			return true
		}
	}
	return false
}

func promoteFlueBundle(tmpRoot, finalRoot string) error {
	if tmpRoot == "" {
		return nil
	}
	if err := os.RemoveAll(finalRoot); err != nil {
		return fmt.Errorf("reset native Flue bundle root: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(finalRoot), 0o755); err != nil {
		return fmt.Errorf("create native Flue bundle parent: %w", err)
	}
	if err := os.Rename(tmpRoot, finalRoot); err != nil {
		return fmt.Errorf("stage native Flue bundle: %w", err)
	}
	return nil
}

// registrationTrust resolves the trust level a registration stamps: empty
// means the operator/source-tree default (trusted). See
// RegisterFlueOptions.Trust for the contract.
func registrationTrust(trust workflowcatalog.DriverTrustLevel) workflowcatalog.DriverTrustLevel {
	if trust == "" {
		return workflowcatalog.DriverTrustTrusted
	}
	return trust
}

func nativeFlueManifest(driverID, driverName, workflowName, sourceRef, sourceDigest, artifactDigest string, external map[string]string, runnerSpecs []DriverRunnerSpec) map[string]string {
	manifest := map[string]string{}
	for k, v := range external {
		manifest[k] = v
	}
	manifest["schema_version"] = NativeFlueSchemaVersion
	manifest["artifact_kind"] = NativeFlueArtifactKind
	manifest["runtime"] = RuntimeFlueNode
	manifest["driver_id"] = driverID
	manifest["driver_name"] = driverName
	manifest["workflow_name"] = workflowName
	manifest["entrypoint"] = EntrypointRun
	manifest["artifact_ref"] = "dist"
	manifest["server_ref"] = filepath.ToSlash(filepath.Join("dist", "server.mjs"))
	manifest["source_ref"] = sourceRef
	manifest["source_digest"] = sourceDigest
	manifest["artifact_digest"] = artifactDigest
	if strings.TrimSpace(manifest[driverRunnersManifestKey]) == "" {
		// No fabrication (§4.6): when the caller supplies no runner specs and the
		// external manifest declares none, leave the runner list empty rather
		// than stamping local/daytona/openshell defaults that may not exist in
		// the bundle.
		if encoded := encodeDriverRunnerManifest(normalizeDriverRunnerSpecs(runnerSpecs)); encoded != "" {
			manifest[driverRunnersManifestKey] = encoded
		}
	}
	if strings.TrimSpace(manifest["flue_runtime_version"]) == "" {
		manifest["flue_runtime_version"] = "unknown"
	}
	if strings.TrimSpace(manifest["loom_sdk_package"]) == "" {
		manifest["loom_sdk_package"] = LoomDriverSDKPackage
	}
	if strings.TrimSpace(manifest["loom_sdk_version"]) == "" {
		manifest["loom_sdk_version"] = LoomDriverSDKVersion
	}
	if strings.TrimSpace(manifest["capabilities"]) == "" {
		manifest["capabilities"] = "task.claim_ready,task_run.request,task.complete,task.release"
	}
	if strings.TrimSpace(manifest["provenance"]) == "" {
		manifest["provenance"] = "operator_registered"
	}
	return manifest
}

func readRegistrationManifest(workDir, distPath, manifestPath string) (map[string]string, error) {
	if manifestPath == "" {
		candidate := filepath.Join(distPath, "loom-driver.json")
		if _, err := os.Stat(candidate); err == nil {
			manifestPath = candidate
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("stat native Flue manifest: %w", err)
		}
	}
	if manifestPath == "" {
		return map[string]string{}, nil
	}
	if !filepath.IsAbs(manifestPath) {
		manifestPath = filepath.Join(workDir, manifestPath)
	}
	data, err := os.ReadFile(manifestPath) //nolint:gosec // manifest path is operator-provided command input.
	if err != nil {
		return nil, fmt.Errorf("read native Flue manifest: %w", err)
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode native Flue manifest: %w", err)
	}
	if serverRef := strings.TrimSpace(raw["server_ref"]); serverRef != "" && serverRef != filepath.ToSlash(filepath.Join("dist", "server.mjs")) {
		return nil, fmt.Errorf("native Flue manifest server_ref must be dist/server.mjs: %w", domain.ErrInvalid)
	}
	for _, generatedRef := range []string{"source_bundle_ref", "workflow_ref"} {
		if strings.TrimSpace(raw[generatedRef]) != "" {
			return nil, fmt.Errorf("native Flue manifest must not contain generated-project field %s: %w", generatedRef, domain.ErrInvalid)
		}
	}
	// Trust is stamped server-side by the registration path (§7 step 9): a
	// client-supplied manifest value is ignored so a submitted bundle cannot
	// self-elevate to trusted.
	delete(raw, "trust_level")
	return raw, nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(path) //nolint:gosec // path comes from walking src.
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm()) //nolint:gosec // G304: target is dst joined with a path produced by walking src.
		if err != nil {
			_ = in.Close()
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = in.Close()
			_ = out.Close()
			return err
		}
		if err := in.Close(); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}

func digestDirectory(root string) (string, error) {
	h := sha256.New()
	_, _ = h.Write([]byte("loom-flue-artifact-v1\n"))
	paths := []string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		return "", fmt.Errorf("digest native Flue artifact: %w", err)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))) //nolint:gosec // rel is generated by WalkDir under root.
		if err != nil {
			return "", fmt.Errorf("digest native Flue artifact file %s: %w", rel, err)
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte("\n"))
		_, _ = h.Write(data)
		_, _ = h.Write([]byte("\n"))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func relativeRef(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

const (
	RuntimeFlueNode = "flue-node"
	EntrypointRun   = "run"
)

type Bundle struct {
	Root         string            `json:"root"`
	BundleRef    string            `json:"bundle_ref"`
	SourceRef    string            `json:"source_ref"`
	SourceDigest string            `json:"source_digest"`
	BundleDigest string            `json:"bundle_digest"`
	Manifest     map[string]string `json:"manifest"`
	Diagnostics  string            `json:"diagnostics,omitempty"`
}

func digestBundleTree(bundleRoot string, manifest []byte) (string, error) {
	h := sha256.New()
	_, _ = h.Write([]byte("loom-driver-bundle-v2\nmanifest\n"))
	_, _ = h.Write(manifest)
	_, _ = h.Write([]byte("\nfiles\n"))
	paths := []string{}
	for _, root := range []string{"dist"} {
		err := filepath.WalkDir(filepath.Join(bundleRoot, filepath.FromSlash(root)), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(bundleRoot, path)
			if err != nil {
				return err
			}
			paths = append(paths, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return "", fmt.Errorf("digest bundle tree: %w", err)
		}
	}
	sort.Strings(paths)
	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(bundleRoot, filepath.FromSlash(rel))) //nolint:gosec // rel is generated by WalkDir under bundleRoot.
		if err != nil {
			return "", fmt.Errorf("digest bundle file %s: %w", rel, err)
		}
		_, _ = h.Write([]byte("file\n"))
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte("\n"))
		_, _ = h.Write(data)
		_, _ = h.Write([]byte("\n"))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digestShort(digest string) string {
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

func slug(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ' || r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
