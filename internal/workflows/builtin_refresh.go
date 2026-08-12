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
	"github.com/tysonthomas9/loomcli/internal/store"
)

var builtinMu sync.Mutex

func EnsureBuiltinWorkflow(ctx context.Context, st DriverCatalog, ws, name string) error {
	return ensureBuiltinWorkflow(ctx, st, ws, name, false)
}

// ensureBuiltinWorkflow optionally requires a managed built-in registration
// to reach the embedded digest. Generic built-ins retain the documented
// toolchain-less availability fallback. Enabled prompt-agent bindings use the
// strict mode because dispatching an older planner would preserve obsolete
// task-mutation and success semantics during an upgrade.
func ensureBuiltinWorkflow(ctx context.Context, st DriverCatalog, ws, name string, requireManagedRefresh bool) error {
	spec, ok := BuiltinWorkflow(name)
	if !ok {
		return domain.ErrNotFound
	}

	builtinMu.Lock()
	defer builtinMu.Unlock()

	digest := SourceDigest(spec.Files)
	freshRunners := workflowRunnerNameSet(spec)
	reuse, current, reuseMissingRunners, preserveOperatorVersion, err := builtinReuseDecision(ctx, st, ws, name, freshRunners)
	if err != nil {
		return err
	}
	if preserveOperatorVersion {
		return nil
	}
	if reuse && current == digest {
		// Registrations stamped via `loom workflow digest` carry the canonical
		// SourceDigest and hit this exact-match fast path without rebuilding.
		return nil
	}
	err = registerBuiltinWorkflow(ctx, st, ws, name, spec, digest)
	if err == nil {
		return nil
	}
	return handleBuiltinWorkflowRegistrationError(
		err, name, ws, current, digest, reuse, reuseMissingRunners, requireManagedRefresh,
	)
}

func registerBuiltinWorkflow(ctx context.Context, st DriverCatalog, ws, name string, spec Spec, digest string) error {
	if found, err := registerPackagedBuiltinWorkflow(ctx, st, ws, name, spec, digest); found || err != nil {
		return err
	}
	_, _, err := BuildAndRegister(ctx, st, BuildAndRegisterOptions{
		WorkspaceKey:  ws,
		Name:          name,
		Entrypoint:    spec.Entrypoint,
		Files:         spec.Files,
		Activate:      true,
		SourceRef:     "builtin://workflows/" + name + "/versions/" + digest,
		SourceDigest:  digest,
		CreatedBy:     "system",
		WorkDir:       builtinWorkflowWorkDir(),
		DeriveRunners: true,
		Trust:         domain.DriverTrustTrusted,
	})
	return err
}

func handleBuiltinWorkflowRegistrationError(
	err error,
	name, ws, current, digest string,
	reuse bool,
	reuseMissingRunners []string,
	requireManagedRefresh bool,
) error {
	if reuse && errors.Is(err, ErrBuildToolchainUnavailable) {
		if requireManagedRefresh {
			return fmt.Errorf("refresh managed built-in workflow %q to the embedded digest: %w", name, err)
		}
		// A digest-drifted version with a complete current runner manifest and
		// an intact bundle can still serve requests. Packaged or minimal serve
		// profiles may intentionally omit the workflow build toolchain, so fail
		// open only for that explicitly-classified deployment condition. Build,
		// validation, and persistence failures remain fatal.
		slog.Warn("builtin digest refresh unavailable; reusing registered version",
			"workflow", name,
			"workspace", ws,
			"registered_digest", current,
			"embedded_digest", digest,
			"err", err.Error())
		return nil
	}
	if len(reuseMissingRunners) > 0 {
		if requireManagedRefresh {
			return fmt.Errorf("refresh managed built-in workflow %q runner manifest: %w", name, err)
		}
		slog.Warn("builtin runner manifest is missing runners and re-register failed; reusing the registered version",
			"workflow", name,
			"workspace", ws,
			"missing_runners", strings.Join(reuseMissingRunners, ","),
			"err", err.Error())
		return nil
	}
	return fmt.Errorf("register built-in workflow %q: %w", name, err)
}

// builtinReuseDecision decides whether the currently-registered builtin can be
// reused as-is. An active version is system-managed only when both its creator
// and builtin source reference say so; an operator-managed activation is
// preserved even under a reserved builtin driver name. For managed versions,
// reuse=true requires a usable bundle and the current runner set.
//
// Source-digest equality is intentionally left to EnsureBuiltinWorkflow: an
// exact match is reused immediately, while a usable mismatch is refreshed and
// may fail open only when the build toolchain is unavailable. Keeping bundle
// and manifest usability separate lets that fallback distinguish a stale but
// runnable registration from a missing or unsafe one. Refresh-on-deprecated
// (§4.6) still fires: a stale/deprecated runner manifest, or a missing bundle,
// fails this check and re-registers without the digest-drift fallback.
//
// A manifest declaring only a SUBSET of the fresh runners (e.g. a stack that
// registered epic-runner with just local-task-runner) is NOT reused as-is: a
// run requesting a missing runner would pin that version and
// applyResolvedRunner would reject the child task. missing (non-empty only
// for such usable-but-subset registrations) tells the caller to re-register —
// and to fail OPEN onto the still-usable subset version, with a warning, if
// that rebuild cannot run here (the same toolchain-less serve), rather than
// failing runs the registered driver can serve.
//
// registeredDigest is the active version's recorded source_digest (empty when
// there is no active version), so the caller can log digest drift on reuse.
func builtinReuseDecision(ctx context.Context, st DriverCatalog, ws, name string, fresh map[string]struct{}) (reuse bool, registeredDigest string, missing []string, preserveOperatorVersion bool, err error) {
	driverID, err := ResolveDriverID(ctx, st, ws, name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return false, "", nil, false, nil
		}
		return false, "", nil, false, err
	}
	current, bundleAvailable, manifest, preserveOperatorVersion, err := activeBuiltInWorkflowState(ctx, st, ws, driverID, name)
	if err != nil {
		return false, "", nil, false, err
	}
	if preserveOperatorVersion {
		if !bundleAvailable {
			return false, current, nil, false, fmt.Errorf("operator-managed active version for built-in workflow %q has no usable staged bundle; refusing to replace it", name)
		}
		return true, current, nil, true, nil
	}
	if !bundleAvailable || activeManifestRunnersAreStale(manifest, fresh) {
		return false, current, nil, false, nil
	}
	if missing = manifestMissingFreshRunners(manifest, fresh); len(missing) > 0 {
		return false, current, missing, false, nil
	}
	return true, current, nil, false, nil
}

func activeBuiltInWorkflowState(ctx context.Context, st DriverCatalog, ws, driverID, builtinName string) (string, bool, map[string]string, bool, error) {
	driverRecord, err := st.Drivers().Get(ctx, ws, driverID)
	if err != nil {
		return "", false, nil, false, fmt.Errorf("get built-in workflow driver %q: %w", driverID, err)
	}
	if strings.TrimSpace(driverRecord.ActiveVersionID) == "" {
		return "", false, nil, false, nil
	}
	version, err := st.DriverVersions().Get(ctx, ws, driverRecord.ActiveVersionID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", false, nil, false, nil
		}
		return "", false, nil, false, fmt.Errorf("get active built-in workflow version %q: %w", driverRecord.ActiveVersionID, err)
	}
	builtinPrefix := "builtin://workflows/" + strings.TrimSpace(builtinName) + "/versions/"
	operatorManaged := strings.TrimSpace(version.CreatedBy) != "system" || !strings.HasPrefix(strings.TrimSpace(version.SourceRef), builtinPrefix)
	return strings.TrimSpace(version.SourceDigest), builtInWorkflowBundleAvailable(version), version.Manifest, operatorManaged, nil
}

func builtInWorkflowBundleAvailable(version *domain.DriverVersion) bool {
	if version == nil || strings.TrimSpace(version.BundleRef) == "" || filepath.IsAbs(version.BundleRef) {
		return false
	}
	workDir := builtinWorkflowWorkDir()
	root := filepath.Clean(filepath.Join(workDir, filepath.FromSlash(version.BundleRef)))
	rel, err := filepath.Rel(workDir, root)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	for _, relFile := range []string{"manifest.json", filepath.Join("dist", "server.mjs")} {
		info, err := os.Stat(filepath.Join(root, relFile))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func builtinWorkflowWorkDir() string {
	if dir := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR")); dir != "" {
		return dir
	}
	workDir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return workDir
}

// BoundPromptAgentCatalog is the startup persistence surface needed to find
// existing prompt-agent bindings and refresh their built-in driver before any
// event dispatcher can admit a run against the previously active version.
type BoundPromptAgentCatalog interface {
	DriverCatalog
	Workspaces() store.WorkspaceStore
	TriggerBindings() store.TriggerBindingStore
}

// EnsureBoundPromptAgentWorkflows refreshes prompt-agent in every workspace
// that already has at least one binding. New bindings already pass through
// EnsureAndResolveDriver; this startup sweep closes the upgrade gap for
// bindings persisted by an older serve binary. Binding rows do not need to be
// rewritten because automation admission resolves the driver's active version
// when it dispatches each delivery.
func EnsureBoundPromptAgentWorkflows(ctx context.Context, st BoundPromptAgentCatalog) error {
	if st == nil {
		return fmt.Errorf("prompt-agent workflow catalog is required: %w", domain.ErrInvalid)
	}
	workspaces, err := st.Workspaces().List(ctx)
	if err != nil {
		return fmt.Errorf("list workspaces for prompt-agent workflow refresh: %w", err)
	}
	var refreshErrs []error
	for _, workspace := range workspaces {
		if workspace == nil || strings.TrimSpace(workspace.Key) == "" {
			continue
		}
		ws := strings.TrimSpace(workspace.Key)
		enabled := true
		bindings, err := st.TriggerBindings().List(ctx, ws, store.TriggerBindingFilter{
			DriverID: BuiltinPromptAgentWorkflowName,
			Enabled:  &enabled,
			Limit:    1,
		})
		if err != nil {
			refreshErrs = append(refreshErrs, fmt.Errorf("list prompt-agent bindings in workspace %q: %w", ws, err))
			continue
		}
		if len(bindings) == 0 {
			continue
		}
		if err := ensureBuiltinWorkflow(ctx, st, ws, BuiltinPromptAgentWorkflowName, true); err != nil {
			refreshErrs = append(refreshErrs, fmt.Errorf("refresh bound prompt-agent workflow in workspace %q: %w", ws, err))
		}
	}
	return errors.Join(refreshErrs...)
}
