package workflowcatalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type authoringFake struct {
	mutations []AuthoringMutation
	result    func(AuthoringMutation) (*AuthoringResult, error)
	err       error
}

func (fake *authoringFake) AuthorVersion(_ context.Context, mutation AuthoringMutation) (*AuthoringResult, error) {
	fake.mutations = append(fake.mutations, mutation)
	if fake.err != nil {
		return nil, fake.err
	}
	if fake.result != nil {
		return fake.result(mutation)
	}
	trust := DriverTrustUntrusted
	if mutation.Managed {
		trust = DriverTrustTrusted
	}
	manifest := cloneStringMap(mutation.Manifest)
	if manifest == nil {
		manifest = map[string]string{}
	}
	manifest[ManifestTrustLevelKey] = string(trust)
	return &AuthoringResult{
		Driver: &Driver{
			WorkspaceKey: mutation.WorkspaceKey,
			DriverID:     mutation.DriverID, Name: mutation.DriverName,
			Status: DriverStatusDraft, TrustLevel: trust,
			Revision: mutation.ExpectedRevision + 1,
		},
		Version: &DriverVersion{
			WorkspaceKey: mutation.WorkspaceKey,
			DriverID:     mutation.DriverID, VersionID: mutation.VersionID,
			Version: 1, SourceRef: mutation.SourceRef, SourceDigest: mutation.SourceDigest,
			BundleRef: mutation.BundleRef, BundleDigest: mutation.BundleDigest,
			Runtime: mutation.Runtime, Manifest: manifest,
			BuildDiagnostics:   mutation.BuildDiagnostics,
			ValidationStatus:   DriverVersionValidationPassed,
			AvailabilityStatus: DriverVersionAvailabilityPending,
			CreatedBy:          mutation.AuditActor,
		},
		CreatedDriver: true, CreatedVersion: true,
		CommittedRevision: mutation.ExpectedRevision + 1,
		SemanticImpact:    SemanticImpactVersionAuthored,
	}, nil
}

type authoringFixture struct {
	service   *Service
	store     *authoringFake
	issuer    *authority.Issuer
	expiresAt time.Time
}

func newAuthoringFixture(t *testing.T, withStore bool) *authoringFixture {
	t.Helper()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	admission, err := issuer.NewAdmission(
		authority.Allow(ActionAuthorVersion, authority.ClassOperator),
		authority.Allow(ActionAuthorManagedVersion, authority.ClassSystem),
	)
	if err != nil {
		t.Fatal(err)
	}
	var store *authoringFake
	var authoring AuthoringStore
	if withStore {
		store = &authoringFake{}
		authoring = store
	}
	return &authoringFixture{
		service: NewWithAuthoring(nil, nil, authoring, admission),
		store:   store, issuer: issuer, expiresAt: now.Add(time.Hour),
	}
}

func (fixture *authoringFixture) operator(t *testing.T, action authority.Action, workspace string) authority.OperatorAuthority {
	t.Helper()
	principal, err := fixture.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "operator", Class: authority.ClassOperator, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: fixture.expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := fixture.issuer.IssueOperator(principal, workspace, action)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func (fixture *authoringFixture) system(t *testing.T, action authority.Action, workspace string) authority.SystemAuthority {
	t.Helper()
	principal, err := fixture.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "builtin-distribution", Class: authority.ClassSystem, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: fixture.expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := fixture.issuer.IssueSystem(principal, workspace, action, "refresh managed builtin")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func validAuthorVersionCommand() AuthorVersionCommand {
	return AuthorVersionCommand{
		WorkspaceKey: "TEST", RequestID: "author-request-1", ExpectedRevision: 7,
		DriverID: "demo", DriverName: "Demo workflow", VersionID: "demo-v-1234",
		SourceRef:    "api://workflows/demo/versions/sha256",
		SourceDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BundleRef:    ".loom/drivers/demo/demo-v-1234",
		BundleDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Runtime:      "flue-node", Manifest: map[string]string{"entrypoint": "run"},
		BuildDiagnostics: "built",
	}
}

func validManagedAuthorVersionCommand() AuthorVersionCommand {
	command := validAuthorVersionCommand()
	command.DriverID = BuiltinEpicRunnerWorkflowName
	command.DriverName = BuiltinEpicRunnerWorkflowName
	command.SourceRef = BuiltinSourceRef(command.DriverID, command.SourceDigest)
	command.VersionID = BuiltinVersionID(command.DriverID, command.BundleDigest)
	command.BundleRef = BuiltinBundleRef(command.DriverID, command.VersionID)
	command.Manifest = map[string]string{
		"driver_id":     command.DriverID,
		"driver_name":   command.DriverName,
		"workflow_name": command.DriverID,
		"source_ref":    command.SourceRef,
		"source_digest": command.SourceDigest,
		"runtime":       command.Runtime,
		"provenance":    ManagedBuiltinProvenance,
		"entrypoint":    "run",
	}
	return command
}

func TestAuthorVersionForcesOperatorLaneUntrustedAndInactive(t *testing.T) {
	fixture := newAuthoringFixture(t, true)
	command := validAuthorVersionCommand()

	result, err := fixture.service.AuthorVersion(
		context.Background(),
		fixture.operator(t, ActionAuthorVersion, "TEST"),
		command,
	)
	if err != nil {
		t.Fatalf("AuthorVersion: %v", err)
	}
	if result.Action != ActionAuthorVersion || result.Version.AvailabilityStatus != DriverVersionAvailabilityPending {
		t.Fatalf("result = %+v", result)
	}
	if len(fixture.store.mutations) != 1 {
		t.Fatalf("mutations = %d, want 1", len(fixture.store.mutations))
	}
	mutation := fixture.store.mutations[0]
	if mutation.Managed {
		t.Fatalf("operator mutation elevated: %+v", mutation)
	}
	if mutation.AuditActor != "operator" || result.Version.CreatedBy != "operator" {
		t.Fatalf("audit actor mutation=%q persisted=%q, want authority subject", mutation.AuditActor, result.Version.CreatedBy)
	}
	if result.Version.Manifest[ManifestTrustLevelKey] != string(DriverTrustUntrusted) ||
		result.Driver.TrustLevel != DriverTrustUntrusted {
		t.Fatalf("operator result trust = driver %q version %q", result.Driver.TrustLevel, result.Version.Manifest[ManifestTrustLevelKey])
	}

	// Both command and result are defensive copies.
	command.Manifest["entrypoint"] = "changed"
	if fixture.store.mutations[0].Manifest["entrypoint"] != "run" {
		t.Fatal("authoring mutation aliases caller manifest")
	}
	result.Version.Manifest["entrypoint"] = "changed"
	if fixture.store.mutations[0].Manifest["entrypoint"] != "run" {
		t.Fatal("authoring result aliases persistence input")
	}
}

func TestAuthorManagedVersionSelectsTrustedPendingVersion(t *testing.T) {
	fixture := newAuthoringFixture(t, true)
	result, err := fixture.service.AuthorManagedVersion(
		context.Background(),
		fixture.system(t, ActionAuthorManagedVersion, "TEST"),
		validManagedAuthorVersionCommand(),
	)
	if err != nil {
		t.Fatalf("AuthorManagedVersion: %v", err)
	}
	mutation := fixture.store.mutations[0]
	if !mutation.Managed {
		t.Fatalf("managed mutation = %+v", mutation)
	}
	if mutation.AuditActor != "builtin-distribution" {
		t.Fatalf("managed audit actor = %q, want authority subject", mutation.AuditActor)
	}
	if result.Action != ActionAuthorManagedVersion ||
		result.Driver.ActiveVersionID != "" ||
		result.Version.AvailabilityStatus != DriverVersionAvailabilityPending ||
		result.Version.Manifest[ManifestTrustLevelKey] != string(DriverTrustTrusted) {
		t.Fatalf("managed result = %+v", result)
	}
}

func TestAuthorManagedVersionRejectsNonBuiltinIdentityAndProvenance(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AuthorVersionCommand)
	}{
		{name: "custom driver", mutate: func(command *AuthorVersionCommand) { command.DriverID = "custom"; command.DriverName = "custom" }},
		{name: "display name", mutate: func(command *AuthorVersionCommand) { command.DriverName = "Epic runner" }},
		{name: "source ref", mutate: func(command *AuthorVersionCommand) { command.SourceRef = "api://workflows/epic-runner" }},
		{name: "version id", mutate: func(command *AuthorVersionCommand) { command.VersionID = "epic-runner-v-user-selected" }},
		{name: "bundle ref", mutate: func(command *AuthorVersionCommand) { command.BundleRef = ".loom/drivers/other/version" }},
		{name: "manifest provenance", mutate: func(command *AuthorVersionCommand) { command.Manifest["provenance"] = "operator_registered" }},
		{name: "manifest source", mutate: func(command *AuthorVersionCommand) { command.Manifest["source_ref"] = "builtin://wrong" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAuthoringFixture(t, true)
			command := validManagedAuthorVersionCommand()
			test.mutate(&command)
			_, err := fixture.service.AuthorManagedVersion(
				context.Background(),
				fixture.system(t, ActionAuthorManagedVersion, "TEST"),
				command,
			)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("AuthorManagedVersion err = %v, want ErrInvalid", err)
			}
			if len(fixture.store.mutations) != 0 {
				t.Fatal("invalid managed identity reached authoring store")
			}
		})
	}
}

func TestAuthoringFailsClosedWithoutAtomicStore(t *testing.T) {
	fixture := newAuthoringFixture(t, false)
	_, err := fixture.service.AuthorVersion(
		context.Background(),
		fixture.operator(t, ActionAuthorVersion, "TEST"),
		validAuthorVersionCommand(),
	)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("AuthorVersion err = %v, want ErrUnavailable", err)
	}
}

func TestAuthoringRejectsCallerSelectedTrustAndReservedApprovalMetadata(t *testing.T) {
	fixture := newAuthoringFixture(t, true)
	for _, key := range []string{ManifestTrustLevelKey, ApprovedVersionMetadataKey("demo-v-1234")} {
		t.Run(key, func(t *testing.T) {
			command := validAuthorVersionCommand()
			command.Manifest[key] = "trusted"
			_, err := fixture.service.AuthorVersion(
				context.Background(),
				fixture.operator(t, ActionAuthorVersion, "TEST"),
				command,
			)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("AuthorVersion err = %v, want ErrInvalid", err)
			}
			if len(fixture.store.mutations) != 0 {
				t.Fatalf("mutation reached store for reserved key %q", key)
			}
		})
	}
}

func TestAuthoringRejectsWrongAuthorityBeforeStore(t *testing.T) {
	fixture := newAuthoringFixture(t, true)
	_, err := fixture.service.AuthorVersion(
		context.Background(),
		fixture.operator(t, ActionAuthorManagedVersion, "TEST"),
		validAuthorVersionCommand(),
	)
	if !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("AuthorVersion err = %v, want admission denial", err)
	}
	if len(fixture.store.mutations) != 0 {
		t.Fatal("wrong authority reached authoring store")
	}
}

func TestAuthoringRejectsDivergentPersistedVersion(t *testing.T) {
	fixture := newAuthoringFixture(t, true)
	fixture.store.result = func(mutation AuthoringMutation) (*AuthoringResult, error) {
		base, err := (&authoringFake{}).AuthorVersion(context.Background(), mutation)
		if err != nil {
			return nil, err
		}
		base.Version.BundleDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		return base, nil
	}
	_, err := fixture.service.AuthorVersion(
		context.Background(),
		fixture.operator(t, ActionAuthorVersion, "TEST"),
		validAuthorVersionCommand(),
	)
	if !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("AuthorVersion err = %v, want invalid persisted state", err)
	}
}

func TestAuthoringReusePreservesOriginalAuditFields(t *testing.T) {
	fixture := newAuthoringFixture(t, true)
	fixture.store.result = func(mutation AuthoringMutation) (*AuthoringResult, error) {
		base, err := (&authoringFake{}).AuthorVersion(context.Background(), mutation)
		if err != nil {
			return nil, err
		}
		base.CreatedDriver = false
		base.CreatedVersion = false
		base.ReusedVersion = true
		base.Version.CreatedBy = "original-operator"
		base.Version.BuildDiagnostics = "original redacted build diagnostics"
		return base, nil
	}
	result, err := fixture.service.AuthorVersion(
		context.Background(),
		fixture.operator(t, ActionAuthorVersion, "TEST"),
		validAuthorVersionCommand(),
	)
	if err != nil {
		t.Fatalf("AuthorVersion reuse: %v", err)
	}
	if !result.ReusedVersion || result.Version.CreatedBy != "original-operator" ||
		result.Version.BuildDiagnostics != "original redacted build diagnostics" {
		t.Fatalf("reused result rewrote original audit fields: %+v", result)
	}
}

func TestAuthoringCreatedVersionRequiresAuthorityAuditActor(t *testing.T) {
	fixture := newAuthoringFixture(t, true)
	fixture.store.result = func(mutation AuthoringMutation) (*AuthoringResult, error) {
		base, err := (&authoringFake{}).AuthorVersion(context.Background(), mutation)
		if err != nil {
			return nil, err
		}
		base.Version.CreatedBy = "request-payload-actor"
		return base, nil
	}
	_, err := fixture.service.AuthorVersion(
		context.Background(),
		fixture.operator(t, ActionAuthorVersion, "TEST"),
		validAuthorVersionCommand(),
	)
	if !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("AuthorVersion err = %v, want invalid persisted audit actor", err)
	}
}

func TestAuthoringRejectsDivergentDriverIdentityAndInvalidVersionSequence(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*AuthoringResult)
	}{
		{name: "driver name", mutate: func(result *AuthoringResult) { result.Driver.Name = "Different workflow" }},
		{name: "version sequence", mutate: func(result *AuthoringResult) { result.Version.Version = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAuthoringFixture(t, true)
			fixture.store.result = func(mutation AuthoringMutation) (*AuthoringResult, error) {
				base, err := (&authoringFake{}).AuthorVersion(context.Background(), mutation)
				if err != nil {
					return nil, err
				}
				test.mutate(base)
				return base, nil
			}
			_, err := fixture.service.AuthorVersion(
				context.Background(),
				fixture.operator(t, ActionAuthorVersion, "TEST"),
				validAuthorVersionCommand(),
			)
			if !errors.Is(err, ErrInvalidPersistedState) {
				t.Fatalf("AuthorVersion err = %v, want invalid persisted state", err)
			}
		})
	}
}

func TestAuthoringRequiresCanonicalContainedBundleReference(t *testing.T) {
	fixture := newAuthoringFixture(t, true)
	for _, ref := range []string{"/tmp/bundle", "../outside", `..\outside`, ".loom/../outside", `.loom\drivers\demo`, ".loom//drivers/demo"} {
		t.Run(ref, func(t *testing.T) {
			command := validAuthorVersionCommand()
			command.BundleRef = ref
			_, err := fixture.service.AuthorVersion(
				context.Background(),
				fixture.operator(t, ActionAuthorVersion, "TEST"),
				command,
			)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("AuthorVersion err = %v, want ErrInvalid", err)
			}
		})
	}
}
