//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// trustedBuiltinRunnerOwner builds an in-memory trusted builtin owner (driver +
// version whose manifest declares local-task-runner) for an injected resolver.
func trustedBuiltinRunnerOwner(t *testing.T, trust workflowcatalog.DriverTrustLevel) (*workflowcatalog.Driver, *workflowcatalog.DriverVersion) {
	t.Helper()
	runnersJSON, err := json.Marshal([]DriverRunnerSpec{{Name: "local-task-runner", Kind: RunnerKindFlueWorkflow, Entrypoint: "local-task-runner"}})
	if err != nil {
		t.Fatalf("marshal runners: %v", err)
	}
	version := &workflowcatalog.DriverVersion{
		VersionID: "bug-fix-agent-v1",
		DriverID:  "bug-fix-agent",
		Manifest: map[string]string{
			"runners":             string(runnersJSON),
			ManifestTrustLevelKey: string(trust),
		},
	}
	driverRow := &workflowcatalog.Driver{DriverID: "bug-fix-agent", TrustLevel: trust, ActiveVersionID: version.VersionID}
	return driverRow, version
}

// registerUntrustedCaller seeds an UNTRUSTED custom driver + version that does
// NOT declare local-task-runner, plus returns a running parent DriverRun pinned
// to it — the caller a global-fallback must resolve on behalf of.
func registerUntrustedCaller(t *testing.T, st *memstore.Store) *execution.DriverRun {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Drivers().Create(ctx, workflowcatalog.DriverCreate{
		WorkspaceKey: "WS", DriverID: "custom-agent", Name: "custom-agent",
		OwnerType: workflowcatalog.DriverOwnerUser, Status: workflowcatalog.DriverStatusActive,
		TrustLevel: workflowcatalog.DriverTrustUntrusted,
	}); err != nil {
		t.Fatalf("create caller driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "custom-agent-v1", DriverID: "custom-agent", Version: 1,
		SourceDigest: "sha256:custom", BundleDigest: "sha256:custom",
		Runtime:            RuntimeFlueNode,
		Manifest:           map[string]string{ManifestTrustLevelKey: string(workflowcatalog.DriverTrustUntrusted)}, // declares no runners
		ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
		AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable,
	}); err != nil {
		t.Fatalf("create caller version: %v", err)
	}
	if _, err := st.ApproveDriverVersionForTest(ctx, "WS", "custom-agent", "custom-agent-v1"); err != nil {
		t.Fatalf("approve caller version: %v", err)
	}
	if _, err := st.ActivateDriverVersionForTest(ctx, "WS", "custom-agent", "custom-agent-v1"); err != nil {
		t.Fatalf("activate caller version: %v", err)
	}
	if _, err := st.UnapproveDriverVersionForTest(ctx, "WS", "custom-agent", "custom-agent-v1"); err != nil {
		t.Fatalf("restore caller version to active-untrusted: %v", err)
	}
	return &execution.DriverRun{RunID: "run-1", DriverID: "custom-agent", DriverVersionID: "custom-agent-v1", Status: execution.DriverRunRunning}
}

type testTaskRunRequestCatalog struct{ store *memstore.Store }

func (catalog testTaskRunRequestCatalog) GetDriver(ctx context.Context, workspace, driverID string) (*workflowcatalog.Driver, error) {
	return catalog.store.Drivers().Get(ctx, workspace, driverID)
}

func (catalog testTaskRunRequestCatalog) GetVersion(ctx context.Context, workspace, versionID string) (*workflowcatalog.DriverVersion, error) {
	return catalog.store.DriverVersions().Get(ctx, workspace, versionID)
}

// An untrusted custom driver that never bundled local-task-runner resolves it
// through the workspace-global builtin fallback: the runner pins to the BUILTIN's
// owning version and derives TRUSTED from that version — not the untrusted caller
// — so it executes under its own trust.
func TestResolveTaskRunRequestRunnerGlobalFallbackUsesBuiltinOwner(t *testing.T) {
	st := memstore.New()
	parent := registerUntrustedCaller(t, st)
	builtinDriver, builtinVersion := trustedBuiltinRunnerOwner(t, workflowcatalog.DriverTrustTrusted)

	restore := swapGlobalRunnerResolver(func(_ context.Context, _, runnerName string) (*GlobalRunnerResolution, error) {
		if runnerName != "local-task-runner" {
			return nil, persistence.ErrNotFound
		}
		return &GlobalRunnerResolution{Driver: builtinDriver, Version: builtinVersion, Spec: DriverRunnerSpec{Name: "local-task-runner", Kind: RunnerKindFlueWorkflow, Entrypoint: "local-task-runner"}}, nil
	})
	defer restore()

	resolved, err := resolveTaskRunRequestRunner(context.Background(), testTaskRunRequestCatalog{store: st}, TaskRunRequestOptions{WorkspaceKey: "WS", Runner: "local-task-runner"}, parent)
	if err != nil {
		t.Fatalf("resolveTaskRunRequestRunner: %v", err)
	}
	if resolved.RunnerVersionID != "bug-fix-agent-v1" {
		t.Fatalf("runner version = %q, want the builtin owner bug-fix-agent-v1 (not the caller's)", resolved.RunnerVersionID)
	}
	if !resolved.RunnerTrustLevel.Trusted() {
		t.Fatalf("runner trust = %q, want trusted (derived from the runner's OWNING version)", resolved.RunnerTrustLevel)
	}
	if !strings.Contains(resolved.RunnerRef, "bug-fix-agent") {
		t.Fatalf("runner ref = %q, want the builtin owner's driver/version", resolved.RunnerRef)
	}
	if resolved.RunnerEntrypoint != "local-task-runner" {
		t.Fatalf("runner entrypoint = %q, want local-task-runner", resolved.RunnerEntrypoint)
	}
}

// An unknown runner (no builtin owns it) fails with the SAME not-declared error
// as before the fallback existed — fail closed.
func TestResolveTaskRunRequestRunnerUnknownRunnerSameError(t *testing.T) {
	st := memstore.New()
	parent := registerUntrustedCaller(t, st)
	restore := swapGlobalRunnerResolver(func(_ context.Context, _, _ string) (*GlobalRunnerResolution, error) {
		return nil, persistence.ErrNotFound
	})
	defer restore()

	_, err := resolveTaskRunRequestRunner(context.Background(), testTaskRunRequestCatalog{store: st}, TaskRunRequestOptions{WorkspaceKey: "WS", Runner: "no-such-runner"}, parent)
	if !errors.Is(err, ErrRunnerNotDeclared) || !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("unknown runner err = %v, want the original not-declared (ErrRunnerNotDeclared + ErrInvalid)", err)
	}
	if !strings.Contains(err.Error(), "custom-agent-v1") {
		t.Fatalf("err = %v, want it to reference the caller's version (same message as today)", err)
	}
}

// An UNTRUSTED owner returned by the resolver is refused by the driver-side
// trust gate (defense in depth) and falls through to the original not-declared
// error — an untrusted driver can never export a runner globally.
func TestResolveTaskRunRequestRunnerUntrustedOwnerNotResolvable(t *testing.T) {
	st := memstore.New()
	parent := registerUntrustedCaller(t, st)
	untrustedDriver, untrustedVersion := trustedBuiltinRunnerOwner(t, workflowcatalog.DriverTrustUntrusted)

	restore := swapGlobalRunnerResolver(func(_ context.Context, _, runnerName string) (*GlobalRunnerResolution, error) {
		return &GlobalRunnerResolution{Driver: untrustedDriver, Version: untrustedVersion, Spec: DriverRunnerSpec{Name: runnerName, Kind: RunnerKindFlueWorkflow, Entrypoint: runnerName}}, nil
	})
	defer restore()

	_, err := resolveTaskRunRequestRunner(context.Background(), testTaskRunRequestCatalog{store: st}, TaskRunRequestOptions{WorkspaceKey: "WS", Runner: "local-task-runner"}, parent)
	if !errors.Is(err, ErrRunnerNotDeclared) {
		t.Fatalf("untrusted owner err = %v, want fall-through to the not-declared error", err)
	}
}

func swapGlobalRunnerResolver(resolver GlobalRunnerResolver) func() {
	prev := globalRunnerResolver
	SetGlobalRunnerResolver(resolver)
	return func() { SetGlobalRunnerResolver(prev) }
}
