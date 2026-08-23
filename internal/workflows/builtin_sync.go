package workflows

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/workflows/packaged"
)

// BuiltinSyncOptions controls a built-in workflow sync.
type BuiltinSyncOptions struct {
	// ForceTrack, when set, overrides the resolved track. ForceTrack=auto is the
	// "return to auto / use built-in and follow updates" action: it activates
	// this binary's packaged version with {user, operator, auto}.
	ForceTrack driver.BuiltinTrack
}

// PackagedVersionInfo describes this binary's packaged built-in version as
// resolved (and possibly registered) by a sync.
type PackagedVersionInfo struct {
	VersionID      string `json:"version_id"`
	SourceDigest   string `json:"source_digest"`
	ArtifactDigest string `json:"artifact_digest"`
	FlueCommit     string `json:"flue_commit"`
	RegisteredNew  bool   `json:"registered_new"`
}

// BuiltinSyncResult is the JSON payload returned by sync (CLI/HTTP).
type BuiltinSyncResult struct {
	Workflow                string              `json:"workflow"`
	DriverID                string              `json:"driver_id"`
	Packaged                PackagedVersionInfo `json:"packaged"`
	ActiveVersionID         string              `json:"active_version_id"`
	PreviousActiveVersionID string              `json:"previous_active_version_id"`
	Track                   driver.BuiltinTrack `json:"track"`
	Activated               bool                `json:"activated"`
	UpdateAvailable         bool                `json:"update_available"`
	ActiveBundleAvailable   bool                `json:"active_bundle_available"`
	Repaired                bool                `json:"repaired"`
}

// BuiltinVersionsInfo is the read-only `builtin` block attached to the versions
// listing for built-in workflows. It never mutates and never fails because of a
// packaging problem (packaged_error carries the reason instead).
type BuiltinVersionsInfo struct {
	PackagedVersionID       string              `json:"packaged_version_id"`
	PackagedSourceDigest    string              `json:"packaged_source_digest"`
	PackagedArtifactDigest  string              `json:"packaged_artifact_digest"`
	Track                   driver.BuiltinTrack `json:"track"`
	UpdateAvailable         bool                `json:"update_available"`
	PreviousActiveVersionID string              `json:"previous_active_version_id"`
	PackagedError           string              `json:"packaged_error,omitempty"`
}

type packagedArtifactCacheEntry struct {
	art *packaged.Artifact
	err error
}

var (
	packagedArtifactCacheMu sync.Mutex
	packagedArtifactCache   = map[string]packagedArtifactCacheEntry{}
)

// lookupPackagedArtifact wraps packaged.Lookup with a per-process cache. Lookup
// re-digests the whole dist tree, so caching avoids re-verifying on every
// workflow run. The key includes every input that changes the result — the
// workflow name, the wanted source digest, the resolved artifacts directory
// (LOOM_BUILTIN_ARTIFACTS_DIR; empty resolves to the baked resource tree, stable
// per process) and the baked index digest — so a process that legitimately
// re-points at a different tree (tests, and any future in-process swap) never
// reads a result computed against the previous tree. Tests that swap tree
// CONTENT under the same path call ResetPackagedCacheForTest.
func lookupPackagedArtifact(name, digest string, wantRunners []driver.DriverRunnerSpec) (*packaged.Artifact, error) {
	key := strings.Join([]string{
		name,
		digest,
		strings.TrimSpace(os.Getenv(packaged.EnvArtifactsDir)),
		strings.TrimSpace(packaged.ExpectedIndexDigest),
	}, "\x00")
	packagedArtifactCacheMu.Lock()
	defer packagedArtifactCacheMu.Unlock()
	if entry, ok := packagedArtifactCache[key]; ok {
		return entry.art, entry.err
	}
	art, err := packaged.Lookup(name, digest, wantRunners)
	packagedArtifactCache[key] = packagedArtifactCacheEntry{art: art, err: err}
	return art, err
}

// ResetPackagedCacheForTest clears the packaged-artifact cache. Tests call it
// between resource-tree swaps within one process.
func ResetPackagedCacheForTest() {
	packagedArtifactCacheMu.Lock()
	defer packagedArtifactCacheMu.Unlock()
	packagedArtifactCache = map[string]packagedArtifactCacheEntry{}
}

// SyncBuiltinWorkflow registers this binary's packaged built-in artifact as an
// immutable version when it is not yet registered, applies the track policy
// (D1/D6) to decide activation, repairs a tampered/missing packaged bundle, and
// reports update_available. It is called from EnsureBuiltinWorkflow's packaged
// path (every run), `loom workflow sync`, and POST …/builtin/sync.
func SyncBuiltinWorkflow(ctx context.Context, st store.Store, ws, name string, opts BuiltinSyncOptions) (*BuiltinSyncResult, error) {
	spec, ok := BuiltinWorkflow(name)
	if !ok {
		return nil, domain.ErrNotFound
	}
	builtinMu.Lock()
	defer builtinMu.Unlock()
	return syncBuiltinLocked(ctx, st, ws, name, spec, opts)
}

// syncBuiltinLocked is the sync body; callers must already hold builtinMu
// (SyncBuiltinWorkflow takes it, EnsureBuiltinWorkflow already holds it).
func syncBuiltinLocked(ctx context.Context, st store.Store, ws, name string, spec Spec, opts BuiltinSyncOptions) (*BuiltinSyncResult, error) {
	digest := SourceDigest(spec.Files)
	sourceRef := "builtin://workflows/" + name + "/versions/" + digest
	wantRunners := deriveWorkflowRunnerSpecs(spec.Entrypoint, spec.Files)
	art, err := lookupPackagedArtifact(name, digest, wantRunners)
	if err != nil {
		// ErrNotPackaged and verification errors are returned unchanged; the
		// caller applies DEV-V5-31's fail-closed rules.
		return nil, err
	}

	workDir := builtinWorkflowWorkDir()
	driverID, err := ResolveDriverID(ctx, st, ws, name)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		driverID = name // no driver yet; registration creates it under this id
	}

	packagedVersion, registeredNew, repaired, err := resolvePackagedVersion(ctx, st, ws, name, driverID, sourceRef, digest, workDir, art)
	if err != nil {
		return nil, err
	}

	result := &BuiltinSyncResult{
		Workflow: name,
		DriverID: driverID,
		Packaged: PackagedVersionInfo{
			VersionID:      packagedVersion.VersionID,
			SourceDigest:   packagedVersion.SourceDigest,
			ArtifactDigest: art.ArtifactDigest,
			FlueCommit:     art.FlueCommit,
			RegisteredNew:  registeredNew,
		},
		Repaired: repaired,
	}

	// Re-read after registration so previous/active reflect the current state.
	driverRecord, err := st.Drivers().Get(ctx, ws, driverID)
	if err != nil {
		return nil, fmt.Errorf("get built-in workflow driver %q: %w", driverID, err)
	}
	previousActive := strings.TrimSpace(driverRecord.ActiveVersionID)
	result.PreviousActiveVersionID = previousActive

	var activeBefore *domain.DriverVersion
	if previousActive != "" {
		activeBefore, err = st.DriverVersions().Get(ctx, ws, previousActive)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("get active built-in workflow version %q: %w", previousActive, err)
		}
	}

	track := opts.ForceTrack
	if track == "" {
		track = driver.ResolveBuiltinTrack(driverRecord, activeBefore)
	}
	activate, updateAvailable := driver.BuiltinSyncDecision(track, previousActive, packagedVersion.VersionID)
	if opts.ForceTrack == driver.BuiltinTrackAuto {
		activate = true
	}
	if activate {
		updateAvailable = false
		actor := driver.ActivationActorSystem
		reason := driver.ActivationReasonBuiltinSync
		if opts.ForceTrack == driver.BuiltinTrackAuto {
			actor = driver.ActivationActorUser
			reason = driver.ActivationReasonOperator
		}
		if _, _, err := driver.ActivateDriverVersionWithOptions(ctx, st, ws, driverID, packagedVersion.VersionID, driver.ActivationOptions{
			Actor:  actor,
			Reason: reason,
			Track:  driver.BuiltinTrackAuto,
		}); err != nil {
			return nil, err
		}
		result.Activated = true
		result.Track = driver.BuiltinTrackAuto
	} else {
		result.Track = track
	}
	result.UpdateAvailable = updateAvailable

	// Determine the final active version and whether its bundle is on disk.
	finalActive := previousActive
	if result.Activated {
		finalActive = packagedVersion.VersionID
	}
	result.ActiveVersionID = finalActive
	if finalActive != "" {
		activeVersion, err := st.DriverVersions().Get(ctx, ws, finalActive)
		if err == nil {
			result.ActiveBundleAvailable = builtInWorkflowBundleAvailable(activeVersion)
		}
	}

	if registeredNew || result.Activated || repaired {
		slog.Info("builtin workflow synced",
			"workflow", name,
			"workspace", ws,
			"packaged_version", packagedVersion.VersionID,
			"active_version", result.ActiveVersionID,
			"track", string(result.Track),
			"registered_new", registeredNew,
			"activated", result.Activated,
			"update_available", result.UpdateAvailable)
	}
	if registeredNew && result.UpdateAvailable {
		slog.Info("builtin update available (pinned track)",
			"workflow", name,
			"workspace", ws,
			"packaged_version", packagedVersion.VersionID,
			"active_version", result.ActiveVersionID)
	}
	return result, nil
}

// resolvePackagedVersion finds this binary's packaged version among the driver's
// registered versions (by provenance + artifact digest). Missing → register it
// inactive. Present but with a missing/tampered staged bundle → repair it in
// place (re-stage from the app resource, same version id). Repair applies only
// to packaged_builtin versions; custom versions are never touched.
func resolvePackagedVersion(ctx context.Context, st store.Store, ws, name, driverID, sourceRef, digest, workDir string, art *packaged.Artifact) (version *domain.DriverVersion, registeredNew, repaired bool, err error) {
	existing, err := findPackagedVersion(ctx, st, ws, driverID, art.ArtifactDigest)
	if err != nil {
		return nil, false, false, err
	}
	if existing == nil {
		res, regErr := driver.RegisterFlueDriver(ctx, st, driver.RegisterFlueOptions{
			WorkspaceKey:           ws,
			WorkDir:                workDir,
			DistPath:               art.DistPath,
			DriverName:             name,
			DriverID:               name,
			WorkflowName:           name,
			SourceRef:              sourceRef,
			SourceDigest:           digest,
			CreatedBy:              "system",
			Activate:               false,
			RunnerSpecs:            art.Runners,
			Manifest:               packagedProvenance(art),
			Trust:                  domain.DriverTrustTrusted,
			ExpectedArtifactDigest: art.ArtifactDigest,
		})
		if regErr != nil {
			return nil, false, false, wrapPackagedRegisterErr(name, art, regErr)
		}
		return res.Version, res.CreatedVersion, false, nil
	}

	if verifyErr := driver.VerifyStagedBundle(workDir, existing); verifyErr != nil {
		if existing.Manifest["provenance"] != packaged.ProvenancePackagedBuiltin {
			// A custom (or otherwise non-packaged) version is never repaired; its
			// pristine source is not in the app resource. Runs keep failing
			// bundle_verification and the listing shows bundle_verified=false.
			return existing, false, false, nil
		}
		bundleAbs := filepath.Join(workDir, filepath.FromSlash(existing.BundleRef))
		if err := os.RemoveAll(bundleAbs); err != nil {
			return nil, false, false, fmt.Errorf("remove stale packaged bundle for %q: %w", name, err)
		}
		res, regErr := driver.RegisterFlueDriver(ctx, st, driver.RegisterFlueOptions{
			WorkspaceKey:           ws,
			WorkDir:                workDir,
			DistPath:               art.DistPath,
			DriverName:             name,
			DriverID:               name,
			WorkflowName:           name,
			SourceRef:              existing.SourceRef,
			SourceDigest:           existing.SourceDigest,
			CreatedBy:              "system",
			Activate:               false,
			RunnerSpecs:            art.Runners,
			Manifest:               existing.Manifest,
			Trust:                  domain.DriverTrustTrusted,
			ExpectedArtifactDigest: art.ArtifactDigest,
		})
		if regErr != nil {
			return nil, false, false, wrapPackagedRegisterErr(name, art, regErr)
		}
		if !res.ReusedVersion || res.Version.VersionID != existing.VersionID {
			return nil, false, false, fmt.Errorf("builtin_artifact_invalid: re-stage produced a different version for workflow %q: %w", name, domain.ErrInvalid)
		}
		return res.Version, false, true, nil
	}
	return existing, false, false, nil
}

// findPackagedVersion returns the driver's version whose manifest declares this
// lane's provenance and the given artifact digest, or nil if none is registered.
func findPackagedVersion(ctx context.Context, st store.Store, ws, driverID, artifactDigest string) (*domain.DriverVersion, error) {
	versions, err := st.DriverVersions().List(ctx, ws, store.DriverVersionFilter{DriverID: driverID})
	if err != nil {
		return nil, fmt.Errorf("list driver versions: %w", err)
	}
	for _, v := range versions {
		if v == nil || v.DriverID != driverID {
			continue
		}
		if v.Manifest["provenance"] == packaged.ProvenancePackagedBuiltin && v.Manifest["artifact_digest"] == artifactDigest {
			return v, nil
		}
	}
	return nil, nil
}

func wrapPackagedRegisterErr(name string, art *packaged.Artifact, err error) error {
	if errors.Is(err, driver.ErrStagedArtifactDigestMismatch) {
		return fmt.Errorf("register packaged built-in workflow %q: %w", name, &packaged.VerificationError{
			Name: name, Field: "artifact_digest", Want: art.ArtifactDigest, Got: "changed during staging",
		})
	}
	return fmt.Errorf("register packaged built-in workflow %q: %w", name, err)
}

// DescribeBuiltinVersions builds the read-only `builtin` block for a built-in
// workflow's versions listing. It never mutates the store and never fails
// because of a packaging problem — packaged_error carries the reason instead.
func DescribeBuiltinVersions(ctx context.Context, st store.Store, ws, name string) (*BuiltinVersionsInfo, error) {
	spec, ok := BuiltinWorkflow(name)
	if !ok {
		return nil, domain.ErrNotFound
	}
	info := &BuiltinVersionsInfo{}

	var driverRecord *domain.Driver
	var activeVersion *domain.DriverVersion
	driverID, err := ResolveDriverID(ctx, st, ws, name)
	switch {
	case err == nil:
		driverRecord, err = st.Drivers().Get(ctx, ws, driverID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
	case errors.Is(err, domain.ErrNotFound):
		driverID = ""
	default:
		return nil, err
	}
	if driverRecord != nil {
		info.PreviousActiveVersionID = driverRecord.Metadata[driver.MetadataKeyActivationPreviousVersion]
		if active := strings.TrimSpace(driverRecord.ActiveVersionID); active != "" {
			activeVersion, _ = st.DriverVersions().Get(ctx, ws, active)
		}
	}

	digest := SourceDigest(spec.Files)
	info.PackagedSourceDigest = digest
	wantRunners := deriveWorkflowRunnerSpecs(spec.Entrypoint, spec.Files)
	art, lookErr := lookupPackagedArtifact(name, digest, wantRunners)
	if lookErr != nil {
		info.PackagedError = lookErr.Error()
	} else {
		info.PackagedArtifactDigest = art.ArtifactDigest
		if driverID != "" {
			if v, err := findPackagedVersion(ctx, st, ws, driverID, art.ArtifactDigest); err == nil && v != nil {
				info.PackagedVersionID = v.VersionID
			}
		}
	}

	track := driver.ResolveBuiltinTrack(driverRecord, activeVersion)
	info.Track = track
	activeID := ""
	if driverRecord != nil {
		activeID = driverRecord.ActiveVersionID
	}
	_, info.UpdateAvailable = driver.BuiltinSyncDecision(track, activeID, info.PackagedVersionID)
	return info, nil
}
