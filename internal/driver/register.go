package driver

import (
	"context"
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

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	NativeFlueSchemaVersion = "3"
	NativeFlueArtifactKind  = "flue-node-artifact"
	LoomFlueSDKPackage      = "@loom/sdk/flue"
	LoomFlueSDKVersion      = "0.1.0"
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
}

type RegisterFlueResult struct {
	Driver         *domain.Driver        `json:"driver"`
	Version        *domain.DriverVersion `json:"version"`
	Bundle         *Bundle               `json:"bundle,omitempty"`
	CreatedDriver  bool                  `json:"created_driver"`
	CreatedVersion bool                  `json:"created_version"`
	ReusedVersion  bool                  `json:"reused_version"`
	Activated      bool                  `json:"activated"`
}

func RegisterFlueDriver(ctx context.Context, s store.Store, opts RegisterFlueOptions) (*RegisterFlueResult, error) {
	if s == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	input, err := resolveRegisterFlueInput(opts)
	if err != nil {
		return nil, err
	}

	result := &RegisterFlueResult{}
	driver, createdDriver, err := ensureRegisteredDriver(ctx, s, opts.WorkspaceKey, input.driverID, input.driverName, input.sourceRef)
	if err != nil {
		return nil, err
	}
	result.Driver = driver
	result.CreatedDriver = createdDriver

	stage, cleanupStage, err := stageNativeFlueBundle(input)
	if err != nil {
		return result, err
	}
	defer cleanupStage()
	if reused, err := reuseRegisteredDriverVersion(ctx, s, opts, input, stage, driver, result); err != nil || reused {
		return result, err
	}
	err = createRegisteredDriverVersion(ctx, s, opts, input, stage, result)
	if err != nil {
		return result, err
	}
	return result, nil
}

type registerFlueInput struct {
	absWorkDir       string
	absDist          string
	externalManifest map[string]string
	driverName       string
	driverID         string
	workflowName     string
	sourceRef        string
	sourceDigest     string
}

type stagedNativeFlueBundle struct {
	tmpRoot        string
	manifest       map[string]string
	artifactDigest string
	sourceDigest   string
	bundleDigest   string
	manifestBytes  []byte
}

func resolveRegisterFlueInput(opts RegisterFlueOptions) (registerFlueInput, error) {
	if strings.TrimSpace(opts.WorkspaceKey) == "" {
		return registerFlueInput{}, fmt.Errorf("workspace key required: %w", domain.ErrInvalid)
	}
	absWorkDir, absDist, err := resolveRegisterFluePaths(opts)
	if err != nil {
		return registerFlueInput{}, err
	}
	externalManifest, err := readRegistrationManifest(absWorkDir, absDist, opts.ManifestPath)
	if err != nil {
		return registerFlueInput{}, err
	}
	driverName := firstNonEmpty(opts.DriverName, externalManifest["driver_name"], externalManifest["name"])
	if driverName == "" {
		driverName = strings.TrimSuffix(filepath.Base(absWorkDir), filepath.Ext(absWorkDir))
	}
	driverID := firstNonEmpty(opts.DriverID, externalManifest["driver_id"], slug(driverName))
	if driverID == "" {
		return registerFlueInput{}, fmt.Errorf("driver name %q does not contain a usable id: %w", driverName, domain.ErrInvalid)
	}
	workflowName := firstNonEmpty(opts.WorkflowName, externalManifest["workflow_name"], driverID)
	if workflowName == "" {
		return registerFlueInput{}, fmt.Errorf("workflow name required: %w", domain.ErrInvalid)
	}
	return registerFlueInput{
		absWorkDir:       absWorkDir,
		absDist:          absDist,
		externalManifest: externalManifest,
		driverName:       driverName,
		driverID:         driverID,
		workflowName:     workflowName,
		sourceRef:        firstNonEmpty(opts.SourceRef, externalManifest["source_ref"], relativeRef(absWorkDir, absDist)),
		sourceDigest:     firstNonEmpty(opts.SourceDigest, externalManifest["source_digest"]),
	}, nil
}

func resolveRegisterFluePaths(opts RegisterFlueOptions) (string, string, error) {
	workDir := firstNonEmpty(opts.WorkDir, ".")
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve work dir: %w", err)
	}
	distPath := strings.TrimSpace(opts.DistPath)
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
	return absWorkDir, absDist, validateNativeFlueDist(absDist)
}

func validateNativeFlueDist(absDist string) error {
	if info, err := os.Stat(absDist); err != nil {
		return fmt.Errorf("stat flue dist path: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("flue dist path must be a directory: %w", domain.ErrInvalid)
	}
	if info, err := os.Stat(filepath.Join(absDist, "server.mjs")); err != nil {
		return fmt.Errorf("flue dist missing server.mjs: %w", err)
	} else if info.IsDir() {
		return fmt.Errorf("flue dist server.mjs is a directory: %w", domain.ErrInvalid)
	}
	return nil
}

func stageNativeFlueBundle(input registerFlueInput) (*stagedNativeFlueBundle, func(), error) {
	driverRoot := filepath.Join(input.absWorkDir, ".loom", "drivers")
	if err := os.MkdirAll(driverRoot, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create driver bundle root: %w", err)
	}
	tmpRoot, err := os.MkdirTemp(driverRoot, ".register-flue-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create temporary driver bundle: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpRoot) }
	stage, err := populateNativeFlueBundle(input, tmpRoot)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return stage, cleanup, nil
}

func populateNativeFlueBundle(input registerFlueInput, tmpRoot string) (*stagedNativeFlueBundle, error) {
	if err := copyTree(input.absDist, filepath.Join(tmpRoot, "dist")); err != nil {
		return nil, err
	}
	artifactDigest, err := digestDirectory(filepath.Join(tmpRoot, "dist"))
	if err != nil {
		return nil, err
	}
	sourceDigest := firstNonEmpty(input.sourceDigest, artifactDigest)
	manifest := nativeFlueManifest(input.driverID, input.driverName, input.workflowName, input.sourceRef, sourceDigest, artifactDigest, input.externalManifest)
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode native Flue manifest: %w", err)
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := os.WriteFile(filepath.Join(tmpRoot, "manifest.json"), manifestBytes, 0o644); err != nil {
		return nil, fmt.Errorf("write native Flue manifest: %w", err)
	}
	bundleDigest, err := digestBundleTree(tmpRoot, manifestBytes)
	if err != nil {
		return nil, err
	}
	return &stagedNativeFlueBundle{tmpRoot: tmpRoot, manifest: manifest, artifactDigest: artifactDigest, sourceDigest: sourceDigest, bundleDigest: bundleDigest, manifestBytes: manifestBytes}, nil
}

func reuseRegisteredDriverVersion(ctx context.Context, s store.Store, opts RegisterFlueOptions, input registerFlueInput, stage *stagedNativeFlueBundle, driver *domain.Driver, result *RegisterFlueResult) (bool, error) {
	versionID, _, finalRoot := nativeFlueBundleRefs(input, stage)
	existing, err := s.DriverVersions().Get(ctx, opts.WorkspaceKey, versionID)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get driver version: %w", err)
	}
	result.Version = existing
	result.ReusedVersion = true
	if existing.DriverID != input.driverID || existing.BundleDigest != stage.bundleDigest {
		return true, fmt.Errorf("driver version %q already exists with different content: %w", versionID, domain.ErrAlreadyExists)
	}
	if opts.Activate && driver.ActiveVersionID != existing.VersionID {
		if err := activateRegisteredDriver(ctx, s, result, opts.WorkspaceKey, input.driverID, existing.VersionID, stage.manifest); err != nil {
			return true, err
		}
	}
	result.Bundle = &Bundle{Root: finalRoot, BundleRef: existing.BundleRef, SourceRef: existing.SourceRef, SourceDigest: existing.SourceDigest, BundleDigest: existing.BundleDigest, Manifest: existing.Manifest}
	return true, nil
}

func createRegisteredDriverVersion(ctx context.Context, s store.Store, opts RegisterFlueOptions, input registerFlueInput, stage *stagedNativeFlueBundle, result *RegisterFlueResult) error {
	versionID, bundleRef, finalRoot := nativeFlueBundleRefs(input, stage)
	nextVersion, err := nextDriverVersion(ctx, s, opts.WorkspaceKey, input.driverID)
	if err != nil {
		return err
	}
	if err := installNativeFlueBundle(stage.tmpRoot, finalRoot); err != nil {
		return err
	}
	version, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     opts.WorkspaceKey,
		VersionID:        versionID,
		DriverID:         input.driverID,
		Version:          nextVersion,
		SourceRef:        input.sourceRef,
		SourceDigest:     stage.sourceDigest,
		BundleRef:        bundleRef,
		BundleDigest:     stage.bundleDigest,
		Runtime:          RuntimeFlueNode,
		Manifest:         stage.manifest,
		ValidationStatus: domain.DriverVersionValidationPassed,
		CreatedBy:        opts.CreatedBy,
	})
	if err != nil {
		return fmt.Errorf("create native Flue driver version: %w", err)
	}
	result.Version = version
	result.CreatedVersion = true
	result.Bundle = &Bundle{Root: finalRoot, BundleRef: bundleRef, SourceRef: input.sourceRef, SourceDigest: stage.sourceDigest, BundleDigest: stage.bundleDigest, Manifest: stage.manifest}
	if opts.Activate {
		return activateRegisteredDriver(ctx, s, result, opts.WorkspaceKey, input.driverID, versionID, stage.manifest)
	}
	return nil
}

func nativeFlueBundleRefs(input registerFlueInput, stage *stagedNativeFlueBundle) (string, string, string) {
	versionID := input.driverID + "-v-" + digestShort(stage.bundleDigest)
	bundleRef := filepath.ToSlash(filepath.Join(".loom", "drivers", input.driverID, versionID))
	finalRoot := filepath.Join(input.absWorkDir, filepath.FromSlash(bundleRef))
	return versionID, bundleRef, finalRoot
}

func installNativeFlueBundle(tmpRoot, finalRoot string) error {
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

func ensureRegisteredDriver(ctx context.Context, s store.Store, ws, driverID, driverName, sourceRef string) (*domain.Driver, bool, error) {
	driver, err := s.Drivers().Get(ctx, ws, driverID)
	if err == nil {
		return driver, false, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, false, fmt.Errorf("get driver: %w", err)
	}
	driver, err = s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: ws,
		DriverID:     driverID,
		Name:         driverName,
		OwnerType:    domain.DriverOwnerUser,
		Description:  "Native Flue driver registered from " + sourceRef,
		Status:       domain.DriverStatusDraft,
		Metadata: map[string]string{
			"source_ref":    sourceRef,
			"runtime":       RuntimeFlueNode,
			"entrypoint":    EntrypointRun,
			"artifact_kind": NativeFlueArtifactKind,
		},
	})
	if err != nil {
		return nil, false, fmt.Errorf("create driver: %w", err)
	}
	return driver, true, nil
}

func activateRegisteredDriver(ctx context.Context, s store.Store, result *RegisterFlueResult, ws, driverID, versionID string, manifest map[string]string) error {
	active := versionID
	status := domain.DriverStatusActive
	driver, err := s.Drivers().Update(ctx, ws, driverID, store.DriverUpdate{
		ActiveVersionID: &active,
		Status:          &status,
		Metadata:        &manifest,
	})
	if err != nil {
		return fmt.Errorf("activate native Flue driver version: %w", err)
	}
	result.Driver = driver
	result.Activated = true
	return nil
}

func nextDriverVersion(ctx context.Context, s store.Store, ws, driverID string) (int, error) {
	versions, err := s.DriverVersions().List(ctx, ws, store.DriverVersionFilter{DriverID: driverID})
	if err != nil {
		return 0, fmt.Errorf("list driver versions: %w", err)
	}
	maxVersion := 0
	for _, version := range versions {
		if version != nil && version.DriverID == driverID && version.Version > maxVersion {
			maxVersion = version.Version
		}
	}
	return maxVersion + 1, nil
}

func nativeFlueManifest(driverID, driverName, workflowName, sourceRef, sourceDigest, artifactDigest string, external map[string]string) map[string]string {
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
	if strings.TrimSpace(manifest["flue_runtime_version"]) == "" {
		manifest["flue_runtime_version"] = "unknown"
	}
	if strings.TrimSpace(manifest["loom_sdk_package"]) == "" {
		manifest["loom_sdk_package"] = LoomFlueSDKPackage
	}
	if strings.TrimSpace(manifest["loom_sdk_version"]) == "" {
		manifest["loom_sdk_version"] = LoomFlueSDKVersion
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
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm()) //nolint:gosec // target is dst plus a relative path produced by WalkDir under src.
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
