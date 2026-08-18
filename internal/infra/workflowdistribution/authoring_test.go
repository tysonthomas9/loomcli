package workflowdistribution

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type authoringAPISpy struct {
	operatorCommand      *workflowcatalog.AuthorVersionCommand
	managedCommand       *workflowcatalog.AuthorVersionCommand
	managedCommands      []workflowcatalog.AuthorVersionCommand
	availabilityCommands []workflowcatalog.AvailabilityCommand
	managedLifecycle     []authority.Action
}

type builtinCatalogStub struct {
	workflowcatalog.API
	driver       *workflowcatalog.Driver
	version      *workflowcatalog.DriverVersion
	getDriverErr error
}

func (stub builtinCatalogStub) GetDriver(
	context.Context,
	string,
	string,
) (*workflowcatalog.Driver, error) {
	return stub.driver, stub.getDriverErr
}

func (stub builtinCatalogStub) GetVersion(
	context.Context,
	string,
	string,
) (*workflowcatalog.DriverVersion, error) {
	return stub.version, nil
}

type managedBuiltinAuthoritySpy struct {
	calls     int
	workspace string
	reason    string
}

func (spy *managedBuiltinAuthoritySpy) AuthorityForManagedBuiltin(
	_ context.Context,
	workspace string,
	reason string,
) (authority.SystemAuthority, error) {
	spy.calls++
	spy.workspace = workspace
	spy.reason = reason
	return authority.SystemAuthority{}, nil
}

func (spy *managedBuiltinAuthoritySpy) AuthorityForVersionAvailability(
	_ context.Context, workspace, reason string,
) (authority.SystemAuthority, error) {
	spy.workspace, spy.reason = workspace, reason
	return authority.SystemAuthority{}, nil
}

func (spy *managedBuiltinAuthoritySpy) AuthorityForManagedVersionLifecycle(
	_ context.Context, workspace string, _ authority.Action, reason string,
) (authority.SystemAuthority, error) {
	spy.workspace, spy.reason = workspace, reason
	return authority.SystemAuthority{}, nil
}

func (spy *authoringAPISpy) AuthorVersion(
	_ context.Context,
	_ authority.OperatorAuthority,
	command workflowcatalog.AuthorVersionCommand,
) (*workflowcatalog.AuthorVersionResult, error) {
	spy.operatorCommand = &command
	return authoredResult(command), nil
}

func (spy *authoringAPISpy) AuthorManagedVersion(
	_ context.Context,
	_ authority.SystemAuthority,
	command workflowcatalog.AuthorVersionCommand,
) (*workflowcatalog.AuthorVersionResult, error) {
	spy.managedCommand = &command
	spy.managedCommands = append(spy.managedCommands, command)
	return authoredResult(command), nil
}

func (spy *authoringAPISpy) RecordVersionAvailability(
	_ context.Context, _ authority.SystemAuthority, command workflowcatalog.AvailabilityCommand,
) (*workflowcatalog.AvailabilityResult, error) {
	spy.availabilityCommands = append(spy.availabilityCommands, command)
	status := workflowcatalog.DriverVersionAvailabilityPending
	if command.Outcome == workflowcatalog.AvailabilityOutcomeAvailable {
		status = workflowcatalog.DriverVersionAvailabilityAvailable
	}
	return &workflowcatalog.AvailabilityResult{
		Driver: &workflowcatalog.Driver{WorkspaceKey: command.WorkspaceKey, DriverID: command.DriverID, Revision: command.ExpectedRevision + 1},
		Version: &workflowcatalog.DriverVersion{
			WorkspaceKey: command.WorkspaceKey, DriverID: command.DriverID, VersionID: command.VersionID,
			SourceDigest: command.SourceDigest, BundleDigest: command.BundleDigest,
			ValidationStatus: workflowcatalog.DriverVersionValidationPassed, AvailabilityStatus: status,
		},
		CommittedRevision: command.ExpectedRevision + 1,
	}, nil
}

func (spy *authoringAPISpy) ApproveManagedVersion(
	_ context.Context, _ authority.SystemAuthority, command workflowcatalog.VersionCommand,
) (*workflowcatalog.VersionResult, error) {
	spy.managedLifecycle = append(spy.managedLifecycle, workflowcatalog.ActionApproveManagedVersion)
	return managedLifecycleResult(command, false), nil
}

func (spy *authoringAPISpy) ActivateManagedVersion(
	_ context.Context, _ authority.SystemAuthority, command workflowcatalog.VersionCommand,
) (*workflowcatalog.VersionResult, error) {
	spy.managedLifecycle = append(spy.managedLifecycle, workflowcatalog.ActionActivateManagedVersion)
	return managedLifecycleResult(command, true), nil
}

type boundPromptAgentIndexStub struct {
	workspaces []string
	enabled    map[string]bool
}

func (stub boundPromptAgentIndexStub) ListWorkspaceKeys(context.Context) ([]string, error) {
	return append([]string(nil), stub.workspaces...), nil
}

func (stub boundPromptAgentIndexStub) HasEnabledPromptAgentBinding(_ context.Context, workspace string) (bool, error) {
	return stub.enabled[workspace], nil
}

func authoredResult(command workflowcatalog.AuthorVersionCommand) *workflowcatalog.AuthorVersionResult {
	revision := command.ExpectedRevision + 1
	return &workflowcatalog.AuthorVersionResult{
		Driver: &workflowcatalog.Driver{
			WorkspaceKey: command.WorkspaceKey,
			DriverID:     command.DriverID,
			Name:         command.DriverName,
			Revision:     revision,
		},
		Version: &workflowcatalog.DriverVersion{
			WorkspaceKey:       command.WorkspaceKey,
			DriverID:           command.DriverID,
			VersionID:          command.VersionID,
			SourceRef:          command.SourceRef,
			SourceDigest:       command.SourceDigest,
			BundleRef:          command.BundleRef,
			BundleDigest:       command.BundleDigest,
			Runtime:            command.Runtime,
			Manifest:           command.Manifest,
			BuildDiagnostics:   command.BuildDiagnostics,
			ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
			AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityPending,
		},
		CreatedDriver: true, CreatedVersion: true, CommittedRevision: revision,
	}
}

func managedLifecycleResult(command workflowcatalog.VersionCommand, active bool) *workflowcatalog.VersionResult {
	return &workflowcatalog.VersionResult{
		Driver:  &workflowcatalog.Driver{WorkspaceKey: command.WorkspaceKey, DriverID: command.DriverID, Revision: command.ExpectedRevision + 1, ActiveVersionID: map[bool]string{true: command.VersionID}[active]},
		Version: &workflowcatalog.DriverVersion{WorkspaceKey: command.WorkspaceKey, DriverID: command.DriverID, VersionID: command.VersionID, AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable},
		Active:  active, CommittedRevision: command.ExpectedRevision + 1,
	}
}

func TestBuildAndAuthorSubmitsOneUntrustedInactiveCatalogCommand(t *testing.T) {
	installFakeWorkflowBuildDeps(t)
	api := &authoringAPISpy{}
	workDir := t.TempDir()

	authorities := &managedBuiltinAuthoritySpy{}
	coordinator, err := appworkflowauthoring.New(NewBundleStager(), authorities)
	if err != nil {
		t.Fatal(err)
	}
	result, diagnostics, err := coordinator.AuthorOperator(
		context.Background(),
		api,
		authority.OperatorAuthority{},
		appworkflowauthoring.BuildOptions{
			WorkspaceKey: "TEST",
			Name:         "custom-flow",
			Entrypoint:   "workflows/custom-flow.ts",
			Files: map[string]string{
				"workflows/custom-flow.ts": "export async function run() { return {}; }\n",
			},
			SourceRef: "file:///tmp/custom-flow",
			WorkDir:   workDir,
		},
	)
	if err != nil {
		t.Fatalf("BuildAndAuthor diagnostics:\n%s\nerr: %v", diagnostics, err)
	}
	if api.operatorCommand == nil || api.managedCommand != nil {
		t.Fatalf("authoring calls = operator:%+v managed:%+v, want one operator call", api.operatorCommand, api.managedCommand)
	}
	command := api.operatorCommand
	if command.WorkspaceKey != "TEST" || command.DriverID != "custom-flow" ||
		command.VersionID == "" || command.RequestID == "" || command.ExpectedRevision != 0 {
		t.Fatalf("author command = %+v", command)
	}
	if _, ok := command.Manifest[driverpkg.ManifestTrustLevelKey]; ok {
		t.Fatalf("catalog manifest = %+v, trust must come from the operator lane", command.Manifest)
	}
	if result.Activated {
		t.Fatal("operator-authored result was activated")
	}
	if got := result.Bundle.Manifest[driverpkg.ManifestTrustLevelKey]; got != string(workflowcatalog.DriverTrustUntrusted) {
		t.Fatalf("bundle trust = %q, want untrusted", got)
	}
	if _, err := os.Stat(filepath.Join(result.Bundle.Root, "dist", "server.mjs")); err != nil {
		t.Fatalf("promoted bundle server.mjs: %v", err)
	}
}

func TestBuildAndAuthorManagedStampsCanonicalProvenance(t *testing.T) {
	installFakeWorkflowBuildDeps(t)
	api := &authoringAPISpy{}
	spec, ok := BuiltinWorkflow(BuiltinLocalReviewAgentWorkflowName)
	if !ok {
		t.Fatal("local-review builtin missing")
	}

	authorities := &managedBuiltinAuthoritySpy{}
	coordinator, err := appworkflowauthoring.New(NewBundleStager(), authorities)
	if err != nil {
		t.Fatal(err)
	}
	result, diagnostics, err := coordinator.AuthorManaged(
		context.Background(),
		api,
		authority.SystemAuthority{},
		appworkflowauthoring.BuildOptions{
			WorkspaceKey:  "TEST",
			Name:          BuiltinLocalReviewAgentWorkflowName,
			Entrypoint:    spec.Entrypoint,
			Files:         spec.Files,
			Activate:      true,
			WorkDir:       t.TempDir(),
			DeriveRunners: true,
		},
	)
	if err != nil {
		t.Fatalf("BuildAndAuthorManaged diagnostics:\n%s\nerr: %v", diagnostics, err)
	}
	if api.managedCommand == nil || api.operatorCommand != nil {
		t.Fatalf("authoring calls = operator:%+v managed:%+v, want one managed call", api.operatorCommand, api.managedCommand)
	}
	if api.managedCommand.Manifest["provenance"] != workflowcatalog.ManagedBuiltinProvenance ||
		!result.Activated || len(api.managedLifecycle) != 2 {
		t.Fatalf("managed command = %+v", api.managedCommand)
	}
	if got := result.Bundle.Manifest[driverpkg.ManifestTrustLevelKey]; got != string(workflowcatalog.DriverTrustTrusted) {
		t.Fatalf("managed bundle trust = %q, want trusted", got)
	}
}

func TestBuildAndAuthorRejectsOperatorTrustAndActivationSelection(t *testing.T) {
	for name, opts := range map[string]appworkflowauthoring.BuildOptions{
		"activate": {Activate: true},
		"trusted":  {Trust: workflowcatalog.DriverTrustTrusted},
	} {
		t.Run(name, func(t *testing.T) {
			api := &authoringAPISpy{}
			authorities := &managedBuiltinAuthoritySpy{}
			coordinator, err := appworkflowauthoring.New(NewBundleStager(), authorities)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := coordinator.AuthorOperator(context.Background(), api, authority.OperatorAuthority{}, opts); err == nil {
				t.Fatal("BuildAndAuthor succeeded")
			}
			if api.operatorCommand != nil || api.managedCommand != nil {
				t.Fatalf("authoring API called after rejected intent: %+v", api)
			}
		})
	}
}

func TestEnsureBuiltinRefreshesCurrentDigestWithRetiredRunnerThroughManagedAuthoring(t *testing.T) {
	installFakeWorkflowBuildDeps(t)
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	bundleRef := filepath.ToSlash(filepath.Join("bundles", "old"))
	bundleRoot := filepath.Join(runtimeDir, filepath.FromSlash(bundleRef))
	if err := os.MkdirAll(filepath.Join(bundleRoot, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(bundleRoot, "manifest.json"):      "{}\n",
		filepath.Join(bundleRoot, "dist", "server.mjs"): "export {};\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	name := BuiltinGitHubReviewAgentWorkflowName
	spec, ok := BuiltinWorkflow(name)
	if !ok {
		t.Fatal("github review builtin missing")
	}
	digest, err := SourceDigest(spec.Files)
	if err != nil {
		t.Fatal(err)
	}
	retiredRunners, err := json.Marshal([]driverpkg.DriverRunnerSpec{
		{Name: BuiltinGitHubReviewTaskRunnerName, Kind: driverpkg.RunnerKindFlueWorkflow, Entrypoint: BuiltinGitHubReviewTaskRunnerName},
		{Name: driverpkg.OpenShellRunnerName, Kind: driverpkg.RunnerKindFlueWorkflow, Entrypoint: driverpkg.OpenShellRunnerName},
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog := builtinCatalogStub{
		driver: &workflowcatalog.Driver{
			WorkspaceKey: "TEST", DriverID: name, Name: name,
			ActiveVersionID: "old-version", Revision: 7,
		},
		version: &workflowcatalog.DriverVersion{
			WorkspaceKey: "TEST", DriverID: name, VersionID: "old-version",
			SourceRef: workflowcatalog.BuiltinSourceRef(name, digest), SourceDigest: digest,
			BundleRef: bundleRef, CreatedBy: "system",
			Manifest: map[string]string{"runners": string(retiredRunners)},
		},
	}
	api := &authoringAPISpy{}
	authorities := &managedBuiltinAuthoritySpy{}
	coordinator, err := appworkflowauthoring.New(NewBundleStager(), authorities)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.EnsureBuiltin(
		t.Context(), catalog, api, authorities, NewBuiltinSupport(), "TEST", name,
	); err != nil {
		t.Fatal(err)
	}
	if api.managedCommand == nil || api.managedCommand.ExpectedRevision != 7 || len(api.managedLifecycle) != 2 {
		t.Fatalf("managed refresh command = %+v", api.managedCommand)
	}
	if authorities.calls != 1 || authorities.workspace != "TEST" {
		t.Fatalf("authority calls/workspace = %d/%q", authorities.calls, authorities.workspace)
	}
	if strings.Contains(api.managedCommand.Manifest["runners"], driverpkg.OpenShellRunnerName) {
		t.Fatalf("managed refresh retained retired runner: %+v", api.managedCommand.Manifest)
	}
}

func TestRefreshBoundPromptAgentWorkflowsUsesCurrentIndexAndManagedInterface(t *testing.T) {
	installFakeWorkflowBuildDeps(t)
	api := &authoringAPISpy{}
	authorities := &managedBuiltinAuthoritySpy{}
	coordinator, err := appworkflowauthoring.New(NewBundleStager(), authorities)
	if err != nil {
		t.Fatal(err)
	}
	index := boundPromptAgentIndexStub{
		workspaces: []string{"ENABLED", "DISABLED"},
		enabled:    map[string]bool{"ENABLED": true},
	}
	if err := coordinator.RefreshBoundPromptAgentWorkflows(
		t.Context(), index,
		builtinCatalogStub{getDriverErr: workflowcatalog.ErrNotFound},
		api, authorities, NewBuiltinSupport(),
	); err != nil {
		t.Fatal(err)
	}
	if len(api.managedCommands) != 1 ||
		api.managedCommands[0].WorkspaceKey != "ENABLED" ||
		api.managedCommands[0].DriverID != BuiltinPromptAgentWorkflowName {
		t.Fatalf("managed commands = %+v", api.managedCommands)
	}
}
