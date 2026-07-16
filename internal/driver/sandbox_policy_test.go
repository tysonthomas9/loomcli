//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// countingSandboxLauncher records Launch calls so refusal tests can prove no
// process was spawned. isolates toggles the SB3 IsolatingLauncher marker.
type countingSandboxLauncher struct {
	stubSandboxLauncher
	isolates bool
	launches int
}

func (c *countingSandboxLauncher) Launch(ctx context.Context, spec LaunchSpec) (SandboxProcess, error) {
	c.launches++
	return c.stubSandboxLauncher.Launch(ctx, spec)
}

func (c *countingSandboxLauncher) Isolates() bool { return c.isolates }

func completedStubProcess(summary string) *stubSandboxProcess {
	return &stubSandboxProcess{
		exit:      SandboxExit{Stdout: `{"status":"completed","summary":"` + summary + `"}` + "\n"},
		placement: domain.TaskRunPlacement{Provider: "container", SandboxID: "sbx-1"},
	}
}

func TestTrustPlacementPolicyGatesLaunch(t *testing.T) {
	cases := []struct {
		name         string
		trust        domain.DriverTrustLevel
		isolates     bool
		wantRefused  bool
		wantLaunches int
	}{
		{name: "untrusted + non-isolating launcher refused", trust: domain.DriverTrustUntrusted, isolates: false, wantRefused: true},
		{name: "unknown trust fails closed", trust: "", isolates: false, wantRefused: true},
		{name: "bogus trust fails closed", trust: "sorta-trusted", isolates: false, wantRefused: true},
		{name: "untrusted + isolating launcher launches", trust: domain.DriverTrustUntrusted, isolates: true, wantLaunches: 1},
		{name: "trusted + non-isolating launcher unchanged", trust: domain.DriverTrustTrusted, isolates: false, wantLaunches: 1},
		{name: "trusted + isolating launcher launches", trust: domain.DriverTrustTrusted, isolates: true, wantLaunches: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			launcher := &countingSandboxLauncher{
				stubSandboxLauncher: stubSandboxLauncher{process: completedStubProcess("sandbox ok")},
				isolates:            tc.isolates,
			}
			req := sandboxSeamRunRequest(t.TempDir())
			req.TrustLevel = tc.trust
			result, err := (NodeRunner{Launcher: launcher}).Run(context.Background(), req)
			if err != nil {
				t.Fatalf("NodeRunner.Run: %v", err)
			}
			if launcher.launches != tc.wantLaunches {
				t.Fatalf("launches = %d, want %d", launcher.launches, tc.wantLaunches)
			}
			if tc.wantRefused {
				if result.Status != domain.DriverRunFailed || result.ErrorClass != ErrorClassSandboxRequired {
					t.Fatalf("result = %+v, want failed %s", result, ErrorClassSandboxRequired)
				}
				if result.Output[ErrorCodeOutputKey] != ErrorClassSandboxRequired || result.Output[RetryableOutputKey] != "false" {
					t.Fatalf("output = %+v, want structured {code:sandbox_required, retryable:false}", result.Output)
				}
				if result.Output[TrustLevelOutputKey] != string(domain.DriverTrustUntrusted) {
					t.Fatalf("output trust = %q, want untrusted audit", result.Output[TrustLevelOutputKey])
				}
				return
			}
			if result.Status != domain.DriverRunCompleted || result.Summary != "sandbox ok" {
				t.Fatalf("result = %+v, want completed sandbox ok", result)
			}
			wantTrust := domain.DriverTrustUntrusted
			if tc.trust.Trusted() {
				wantTrust = domain.DriverTrustTrusted
			}
			if result.Output[TrustLevelOutputKey] != string(wantTrust) {
				t.Fatalf("output trust = %q, want %q audit", result.Output[TrustLevelOutputKey], wantTrust)
			}
			if result.Output[SandboxLauncherOutputKey] == "" {
				t.Fatalf("output = %+v, want launcher audit", result.Output)
			}
		})
	}
}

func TestTrustPlacementPolicyRefusesDefaultProcessLauncher(t *testing.T) {
	// NodePath points at nothing executable: if the policy ever let the
	// default process launcher run, the result would be a driver_runtime
	// start failure instead of sandbox_required.
	req := sandboxSeamRunRequest(t.TempDir())
	req.TrustLevel = domain.DriverTrustUntrusted
	result, err := (NodeRunner{NodePath: "/nonexistent/loom-test-node"}).Run(context.Background(), req)
	if err != nil {
		t.Fatalf("NodeRunner.Run: %v", err)
	}
	if result.Status != domain.DriverRunFailed || result.ErrorClass != ErrorClassSandboxRequired {
		t.Fatalf("result = %+v, want failed %s with no process spawned", result, ErrorClassSandboxRequired)
	}
	if result.Output[SandboxLauncherOutputKey] != SandboxProviderProcess {
		t.Fatalf("output launcher = %q, want %q audit", result.Output[SandboxLauncherOutputKey], SandboxProviderProcess)
	}
	if !strings.Contains(result.Summary, "untrusted") {
		t.Fatalf("summary = %q, want untrusted refusal explanation", result.Summary)
	}
}

func setupTrustPolicyExecutorRun(t *testing.T, trust domain.DriverTrustLevel) (context.Context, store.Store, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true, Trust: trust})
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{
		WorkspaceKey:   "TEST",
		DriverID:       registered.Driver.DriverID,
		EpicID:         "TEST-1",
		RunID:          "run-trust",
		IdempotencyKey: "idem-trust",
	}); err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}
	return ctx, st, root
}

func TestExecutorRefusesUntrustedRunOnDefaultProcessLauncher(t *testing.T) {
	ctx, st, root := setupTrustPolicyExecutorRun(t, domain.DriverTrustUntrusted)
	result, err := (&Executor{
		Store:             st,
		WorkspaceKey:      "TEST",
		WorkDir:           root,
		NodeID:            "node-1",
		LeaseID:           "lease-1",
		HeartbeatInterval: -1,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	final := result.Final
	if final == nil || final.Status != domain.DriverRunFailed || final.ErrorClass != ErrorClassSandboxRequired {
		t.Fatalf("final = %+v, want failed %s", final, ErrorClassSandboxRequired)
	}
	if final.Output[ErrorCodeOutputKey] != ErrorClassSandboxRequired || final.Output[RetryableOutputKey] != "false" {
		t.Fatalf("final output = %+v, want persisted structured refusal", final.Output)
	}
	if final.Output[TrustLevelOutputKey] != string(domain.DriverTrustUntrusted) {
		t.Fatalf("final output trust = %q, want untrusted audit", final.Output[TrustLevelOutputKey])
	}
	if final.Output[SandboxPlacementOutputKey] != "" {
		t.Fatalf("final output placement = %q, want none (nothing launched)", final.Output[SandboxPlacementOutputKey])
	}
}

func TestExecutorLaunchesUntrustedRunThroughIsolatingLauncher(t *testing.T) {
	ctx, st, root := setupTrustPolicyExecutorRun(t, domain.DriverTrustUntrusted)
	launcher := &countingSandboxLauncher{
		stubSandboxLauncher: stubSandboxLauncher{process: completedStubProcess("isolated ok")},
		isolates:            true,
	}
	result, err := (&Executor{
		Store:             st,
		WorkspaceKey:      "TEST",
		WorkDir:           root,
		NodeID:            "node-1",
		LeaseID:           "lease-1",
		HeartbeatInterval: -1,
		SandboxLauncher:   launcher,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if launcher.launches != 1 {
		t.Fatalf("launches = %d, want 1", launcher.launches)
	}
	final := result.Final
	if final == nil || final.Status != domain.DriverRunCompleted || final.Summary != "isolated ok" {
		t.Fatalf("final = %+v, want completed via isolating launcher", final)
	}
	if final.Output[TrustLevelOutputKey] != string(domain.DriverTrustUntrusted) {
		t.Fatalf("final output trust = %q, want untrusted audit", final.Output[TrustLevelOutputKey])
	}
}

func TestRegistrationStampsTrustServerSide(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}

	// Operator/source-tree path (CLI): default trusted.
	rootTrusted := t.TempDir()
	writeFlueDist(t, rootTrusted, "operator-flow", "done")
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{WorkspaceKey: "TEST", WorkDir: rootTrusted, DistPath: "dist", DriverName: "operator-flow", CreatedBy: "tester", Activate: true})
	if err != nil {
		t.Fatalf("RegisterFlueDriver (operator): %v", err)
	}
	if registered.Driver.TrustLevel != domain.DriverTrustTrusted {
		t.Fatalf("operator-registered trust = %q, want trusted", registered.Driver.TrustLevel)
	}
	if registered.Version.Manifest[ManifestTrustLevelKey] != string(domain.DriverTrustTrusted) {
		t.Fatalf("operator version trust = %q, want trusted", registered.Version.Manifest[ManifestTrustLevelKey])
	}

	// External submission path: server stamps untrusted, and a client manifest
	// claiming trusted is ignored (no self-elevation).
	rootUser := t.TempDir()
	writeFlueDist(t, rootUser, "user-flow", "done")
	if err := os.WriteFile(filepath.Join(rootUser, "dist", "loom-driver.json"), []byte(`{"trust_level":"trusted"}`), 0o644); err != nil {
		t.Fatalf("write client manifest: %v", err)
	}
	submitted, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST", WorkDir: rootUser, DistPath: "dist",
		DriverName: "user-flow", CreatedBy: "api", Activate: true,
		Trust: domain.DriverTrustUntrusted,
	})
	if err != nil {
		t.Fatalf("RegisterFlueDriver (submission): %v", err)
	}
	if submitted.Driver.TrustLevel != domain.DriverTrustUntrusted {
		t.Fatalf("submitted trust = %q, want untrusted (client manifest ignored)", submitted.Driver.TrustLevel)
	}
	if submitted.Version.Manifest[ManifestTrustLevelKey] != string(domain.DriverTrustUntrusted) {
		t.Fatalf("version manifest trust = %q, want server-stamped untrusted", submitted.Version.Manifest[ManifestTrustLevelKey])
	}
}

func TestReRegistrationNeverElevatesAndUntrustedSubmissionDemotes(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	root := t.TempDir()
	writeFlueDist(t, root, "epic-runner", "v1")
	if _, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true}); err != nil {
		t.Fatalf("RegisterFlueDriver (trusted v1): %v", err)
	}

	// Untrusted submission onto the existing trusted driver demotes it: the
	// driver's newest content came through the untrusted path.
	writeFlueDist(t, root, "epic-runner", "v2")
	demoted, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "api", Activate: true, Trust: domain.DriverTrustUntrusted})
	if err != nil {
		t.Fatalf("RegisterFlueDriver (untrusted v2): %v", err)
	}
	if demoted.Driver.TrustLevel != domain.DriverTrustUntrusted {
		t.Fatalf("driver trust after untrusted re-registration = %q, want demoted untrusted", demoted.Driver.TrustLevel)
	}

	// A later trusted registration does NOT silently re-elevate: elevation is
	// an explicit ops action via driver update, never a registration side
	// effect.
	writeFlueDist(t, root, "epic-runner", "v3")
	stillUntrusted, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true})
	if err != nil {
		t.Fatalf("RegisterFlueDriver (trusted v3): %v", err)
	}
	if stillUntrusted.Driver.TrustLevel != domain.DriverTrustUntrusted {
		t.Fatalf("driver trust after trusted re-registration = %q, want still untrusted", stillUntrusted.Driver.TrustLevel)
	}
}
