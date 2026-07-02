//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// trustedBuiltinRunnerOwner builds an in-memory trusted builtin owner (driver +
// version whose manifest declares local-task-runner) for an injected resolver.
func trustedBuiltinRunnerOwner(t *testing.T, trust domain.DriverTrustLevel) (*domain.Driver, *domain.DriverVersion) {
	t.Helper()
	runnersJSON, err := json.Marshal([]DriverRunnerSpec{{Name: "local-task-runner", Kind: RunnerKindFlueWorkflow, Entrypoint: "local-task-runner"}})
	if err != nil {
		t.Fatalf("marshal runners: %v", err)
	}
	version := &domain.DriverVersion{
		VersionID: "bug-fix-agent-v1",
		DriverID:  "bug-fix-agent",
		Manifest: map[string]string{
			"runners":             string(runnersJSON),
			ManifestTrustLevelKey: string(trust),
		},
	}
	driverRow := &domain.Driver{DriverID: "bug-fix-agent", TrustLevel: trust, ActiveVersionID: version.VersionID}
	return driverRow, version
}

// registerUntrustedCaller seeds an UNTRUSTED custom driver + version that does
// NOT declare local-task-runner, plus returns a running parent DriverRun pinned
// to it — the caller a global-fallback must resolve on behalf of.
func registerUntrustedCaller(t *testing.T, st store.Store) *domain.DriverRun {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "custom-agent", Name: "custom-agent",
		OwnerType: domain.DriverOwnerUser, Status: domain.DriverStatusActive,
		TrustLevel: domain.DriverTrustUntrusted,
	}); err != nil {
		t.Fatalf("create caller driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "custom-agent-v1", DriverID: "custom-agent", Version: 1,
		SourceDigest: "sha256:custom", BundleDigest: "sha256:custom",
		Runtime:  RuntimeFlueNode,
		Manifest: map[string]string{ManifestTrustLevelKey: string(domain.DriverTrustUntrusted)}, // declares no runners
	}); err != nil {
		t.Fatalf("create caller version: %v", err)
	}
	activeVersion := "custom-agent-v1"
	if _, err := st.Drivers().Update(ctx, "WS", "custom-agent", store.DriverUpdate{ActiveVersionID: &activeVersion}); err != nil {
		t.Fatalf("activate caller version: %v", err)
	}
	return &domain.DriverRun{RunID: "run-1", DriverID: "custom-agent", DriverVersionID: "custom-agent-v1", Status: domain.DriverRunRunning}
}

// An untrusted custom driver that never bundled local-task-runner resolves it
// through the workspace-global builtin fallback: the runner pins to the BUILTIN's
// owning version and derives TRUSTED from that version — not the untrusted caller
// — so it executes under its own trust.
func TestResolveTaskRunRequestRunnerGlobalFallbackUsesBuiltinOwner(t *testing.T) {
	st := memstore.New()
	parent := registerUntrustedCaller(t, st)
	builtinDriver, builtinVersion := trustedBuiltinRunnerOwner(t, domain.DriverTrustTrusted)

	restore := swapGlobalRunnerResolver(func(_ context.Context, _ store.Store, _, runnerName string) (*GlobalRunnerResolution, error) {
		if runnerName != "local-task-runner" {
			return nil, domain.ErrNotFound
		}
		return &GlobalRunnerResolution{Driver: builtinDriver, Version: builtinVersion, Spec: DriverRunnerSpec{Name: "local-task-runner", Kind: RunnerKindFlueWorkflow, Entrypoint: "local-task-runner"}}, nil
	})
	defer restore()

	resolved, err := resolveTaskRunRequestRunner(context.Background(), st, TaskRunRequestOptions{WorkspaceKey: "WS", Runner: "local-task-runner"}, parent)
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
	restore := swapGlobalRunnerResolver(func(_ context.Context, _ store.Store, _, _ string) (*GlobalRunnerResolution, error) {
		return nil, domain.ErrNotFound
	})
	defer restore()

	_, err := resolveTaskRunRequestRunner(context.Background(), st, TaskRunRequestOptions{WorkspaceKey: "WS", Runner: "no-such-runner"}, parent)
	if !errors.Is(err, ErrRunnerNotDeclared) || !errors.Is(err, domain.ErrInvalid) {
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
	untrustedDriver, untrustedVersion := trustedBuiltinRunnerOwner(t, domain.DriverTrustUntrusted)

	restore := swapGlobalRunnerResolver(func(_ context.Context, _ store.Store, _, runnerName string) (*GlobalRunnerResolution, error) {
		return &GlobalRunnerResolution{Driver: untrustedDriver, Version: untrustedVersion, Spec: DriverRunnerSpec{Name: runnerName, Kind: RunnerKindFlueWorkflow, Entrypoint: runnerName}}, nil
	})
	defer restore()

	_, err := resolveTaskRunRequestRunner(context.Background(), st, TaskRunRequestOptions{WorkspaceKey: "WS", Runner: "local-task-runner"}, parent)
	if !errors.Is(err, ErrRunnerNotDeclared) {
		t.Fatalf("untrusted owner err = %v, want fall-through to the not-declared error", err)
	}
}

func swapGlobalRunnerResolver(resolver GlobalRunnerResolver) func() {
	prev := globalRunnerResolver
	SetGlobalRunnerResolver(resolver)
	return func() { SetGlobalRunnerResolver(prev) }
}
