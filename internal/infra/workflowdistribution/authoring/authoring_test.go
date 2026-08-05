package authoring

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type authoringAPISpy struct {
	operatorCommand *workflowcatalog.AuthorVersionCommand
	managedCommand  *workflowcatalog.AuthorManagedVersionCommand
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

func (spy *authoringAPISpy) AuthorVersion(
	_ context.Context,
	_ authority.OperatorAuthority,
	command workflowcatalog.AuthorVersionCommand,
) (*workflowcatalog.AuthorVersionResult, error) {
	spy.operatorCommand = &command
	return authoredResult(command, false), nil
}

func (spy *authoringAPISpy) AuthorManagedVersion(
	_ context.Context,
	_ authority.SystemAuthority,
	command workflowcatalog.AuthorManagedVersionCommand,
) (*workflowcatalog.AuthorVersionResult, error) {
	spy.managedCommand = &command
	return authoredResult(command.AuthorVersionCommand, command.Activate), nil
}

func authoredResult(command workflowcatalog.AuthorVersionCommand, activated bool) *workflowcatalog.AuthorVersionResult {
	return &workflowcatalog.AuthorVersionResult{
		Driver: &workflowcatalog.Driver{
			WorkspaceKey: command.WorkspaceKey,
			DriverID:     command.DriverID,
			Name:         command.DriverName,
		},
		Version: &workflowcatalog.DriverVersion{
			WorkspaceKey:     command.WorkspaceKey,
			DriverID:         command.DriverID,
			VersionID:        command.VersionID,
			SourceRef:        command.SourceRef,
			SourceDigest:     command.SourceDigest,
			BundleRef:        command.BundleRef,
			BundleDigest:     command.BundleDigest,
			Runtime:          command.Runtime,
			Manifest:         command.Manifest,
			BuildDiagnostics: command.BuildDiagnostics,
		},
		CreatedDriver: true, CreatedVersion: true, Activated: activated,
	}
}

func TestBuildAndAuthorSubmitsOneUntrustedInactiveCatalogCommand(t *testing.T) {
	installFakeWorkflowBuildDeps(t)
	api := &authoringAPISpy{}
	workDir := t.TempDir()

	result, diagnostics, err := BuildAndAuthor(
		context.Background(),
		api,
		authority.OperatorAuthority{},
		BuildAndRegisterOptions{
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
	if got := result.Bundle.Manifest[driverpkg.ManifestTrustLevelKey]; got != string(domain.DriverTrustUntrusted) {
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

	result, diagnostics, err := BuildAndAuthorManaged(
		context.Background(),
		api,
		authority.SystemAuthority{},
		BuildAndRegisterOptions{
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
	if !api.managedCommand.Activate ||
		api.managedCommand.Manifest["provenance"] != workflowcatalog.ManagedBuiltinProvenance {
		t.Fatalf("managed command = %+v", api.managedCommand)
	}
	if got := result.Bundle.Manifest[driverpkg.ManifestTrustLevelKey]; got != string(domain.DriverTrustTrusted) {
		t.Fatalf("managed bundle trust = %q, want trusted", got)
	}
}

func TestBuildAndAuthorRejectsOperatorTrustAndActivationSelection(t *testing.T) {
	for name, opts := range map[string]BuildAndRegisterOptions{
		"activate": {Activate: true},
		"trusted":  {Trust: domain.DriverTrustTrusted},
	} {
		t.Run(name, func(t *testing.T) {
			api := &authoringAPISpy{}
			if _, _, err := BuildAndAuthor(context.Background(), api, authority.OperatorAuthority{}, opts); err == nil {
				t.Fatal("BuildAndAuthor succeeded")
			}
			if api.operatorCommand != nil || api.managedCommand != nil {
				t.Fatalf("authoring API called after rejected intent: %+v", api)
			}
		})
	}
}

func TestEnsureBuiltinWorkflowAuthoredUsesManagedAtomicPort(t *testing.T) {
	installFakeWorkflowBuildDeps(t)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", t.TempDir())
	api := &authoringAPISpy{}
	authorities := &managedBuiltinAuthoritySpy{}
	catalog := builtinCatalogStub{getDriverErr: workflowcatalog.ErrNotFound}

	if err := EnsureBuiltinWorkflowAuthored(
		context.Background(),
		catalog,
		api,
		authorities,
		"TEST",
		BuiltinPromptAgentWorkflowName,
	); err != nil {
		t.Fatalf("EnsureBuiltinWorkflowAuthored: %v", err)
	}
	if api.managedCommand == nil || api.operatorCommand != nil {
		t.Fatalf("authoring calls = operator:%+v managed:%+v", api.operatorCommand, api.managedCommand)
	}
	if !api.managedCommand.Activate || api.managedCommand.ExpectedRevision != 0 ||
		api.managedCommand.DriverID != BuiltinPromptAgentWorkflowName ||
		api.managedCommand.Manifest["provenance"] != workflowcatalog.ManagedBuiltinProvenance {
		t.Fatalf("managed command = %+v", api.managedCommand)
	}
	if authorities.calls != 1 || authorities.workspace != "TEST" || authorities.reason == "" {
		t.Fatalf("managed authority calls = %+v, want one scoped TEST grant", authorities)
	}
}
