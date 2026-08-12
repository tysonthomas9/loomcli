package workflowcatalog

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type readerFake struct {
	drivers      map[string]*Driver
	names        map[string]string
	versions     map[string]*DriverVersion
	driverOrder  []string
	versionOrder map[string][]string
	calls        []string
	forcedErr    error
}

func (f *readerFake) GetDriver(_ context.Context, workspace, driverID string) (*Driver, error) {
	f.calls = append(f.calls, "get-driver:"+workspace+":"+driverID)
	if f.forcedErr != nil {
		return nil, f.forcedErr
	}
	driver := f.drivers[driverID]
	if driver == nil {
		return nil, ErrNotFound
	}
	return driver, nil
}

func (f *readerFake) FindDriverByName(_ context.Context, workspace, name string) (*Driver, error) {
	f.calls = append(f.calls, "find-driver:"+workspace+":"+name)
	if f.forcedErr != nil {
		return nil, f.forcedErr
	}
	driver := f.drivers[f.names[name]]
	if driver == nil {
		return nil, ErrNotFound
	}
	return driver, nil
}

func (f *readerFake) ListDrivers(_ context.Context, workspace string) ([]*Driver, error) {
	f.calls = append(f.calls, "list-drivers:"+workspace)
	if f.forcedErr != nil {
		return nil, f.forcedErr
	}
	out := make([]*Driver, 0, len(f.driverOrder))
	for _, id := range f.driverOrder {
		out = append(out, f.drivers[id])
	}
	return out, nil
}

func (f *readerFake) GetVersion(_ context.Context, workspace, versionID string) (*DriverVersion, error) {
	f.calls = append(f.calls, "get-version:"+workspace+":"+versionID)
	if f.forcedErr != nil {
		return nil, f.forcedErr
	}
	version := f.versions[versionID]
	if version == nil {
		return nil, ErrNotFound
	}
	return version, nil
}

func (f *readerFake) ListVersions(_ context.Context, workspace, driverID string) ([]*DriverVersion, error) {
	f.calls = append(f.calls, "list-versions:"+workspace+":"+driverID)
	if f.forcedErr != nil {
		return nil, f.forcedErr
	}
	ids := f.versionOrder[driverID]
	out := make([]*DriverVersion, 0, len(ids))
	for _, id := range ids {
		out = append(out, f.versions[id])
	}
	return out, nil
}

type lifecycleFake struct {
	reader    *readerFake
	calls     []authority.Action
	mutations []LifecycleMutation
	err       error
	result    func(authority.Action, LifecycleMutation) (*LifecycleResult, error)
}

func (f *lifecycleFake) ApproveVersion(_ context.Context, mutation LifecycleMutation) (*LifecycleResult, error) {
	return f.apply(ActionApproveVersion, mutation)
}

func (f *lifecycleFake) UnapproveVersion(_ context.Context, mutation LifecycleMutation) (*LifecycleResult, error) {
	return f.apply(ActionUnapproveVersion, mutation)
}

func (f *lifecycleFake) ActivateVersion(_ context.Context, mutation LifecycleMutation) (*LifecycleResult, error) {
	return f.apply(ActionActivateVersion, mutation)
}

func (f *lifecycleFake) apply(action authority.Action, mutation LifecycleMutation) (*LifecycleResult, error) {
	f.calls = append(f.calls, action)
	f.mutations = append(f.mutations, mutation)
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result(action, mutation)
	}
	driver := cloneDriver(f.reader.drivers[mutation.DriverID])
	version := cloneVersion(f.reader.versions[mutation.VersionID])
	if action == ActionActivateVersion && !VersionApproved(driver, version) {
		return nil, ErrVersionNotApproved
	}
	driver.Revision++
	if driver.Metadata == nil {
		driver.Metadata = map[string]string{}
	}
	switch action {
	case ActionApproveVersion:
		driver.Metadata[ApprovedVersionMetadataKey(version.VersionID)] = version.SourceDigest
	case ActionUnapproveVersion:
		delete(driver.Metadata, ApprovedVersionMetadataKey(version.VersionID))
	case ActionActivateVersion:
		for key, value := range version.Manifest {
			if !strings.HasPrefix(key, ApprovedVersionMetadataPrefix) {
				driver.Metadata[key] = value
			}
		}
		driver.ActiveVersionID = version.VersionID
		driver.Status = DriverStatusActive
	}
	return &LifecycleResult{
		Driver:            driver,
		Version:           version,
		CommittedRevision: driver.Revision,
		SemanticImpact:    semanticImpactFor(action),
	}, nil
}

type catalogFixture struct {
	service   *Service
	reader    *readerFake
	lifecycle *lifecycleFake
	issuer    *authority.Issuer
	now       *time.Time
}

func newCatalogFixture(t *testing.T) *catalogFixture {
	t.Helper()
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewIssuerWithClock: %v", err)
	}
	admission, err := issuer.NewAdmission(
		authority.Allow(ActionResolveEffectiveVersion, authority.ClassSystem),
		authority.OperatorOnly(ActionResolveRequestedVersion),
		authority.OperatorOnly(ActionApproveVersion),
		authority.OperatorOnly(ActionUnapproveVersion),
		authority.OperatorOnly(ActionActivateVersion),
	)
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	reader := &readerFake{
		drivers: map[string]*Driver{
			"driver-1": {
				WorkspaceKey: "TEST", DriverID: "driver-1", Name: "demo",
				ActiveVersionID: "v1", Status: DriverStatusActive,
				TrustLevel: DriverTrustUntrusted, Revision: 7,
				Metadata: map[string]string{ApprovedVersionMetadataKey("v1"): "digest-v1", "unrelated": "keep"},
			},
		},
		names: map[string]string{"demo": "driver-1"},
		versions: map[string]*DriverVersion{
			"v1": {WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v1", Version: 1, SourceDigest: "digest-v1", Manifest: map[string]string{ManifestTrustLevelKey: "untrusted"}, ValidationStatus: DriverVersionValidationPassed},
			"v2": {WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v2", Version: 2, SourceDigest: "digest-v2", Manifest: map[string]string{ManifestTrustLevelKey: "untrusted"}, ValidationStatus: DriverVersionValidationPassed},
		},
		driverOrder:  []string{"driver-1"},
		versionOrder: map[string][]string{"driver-1": {"v2", "v1"}},
	}
	lifecycle := &lifecycleFake{reader: reader}
	return &catalogFixture{
		service: New(reader, lifecycle, admission), reader: reader, lifecycle: lifecycle,
		issuer: issuer, now: &now,
	}
}

func (f *catalogFixture) operator(t *testing.T, workspace string, action authority.Action) authority.OperatorAuthority {
	t.Helper()
	principal, err := f.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "operator-1", Class: authority.ClassOperator, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: f.now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("DeriveVerifiedPrincipal: %v", err)
	}
	auth, err := f.issuer.IssueOperator(principal, workspace, action)
	if err != nil {
		t.Fatalf("IssueOperator: %v", err)
	}
	return auth
}

func (f *catalogFixture) system(t *testing.T, workspace string, action authority.Action) authority.SystemAuthority {
	t.Helper()
	principal, err := f.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "automation-1", Class: authority.ClassSystem, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: f.now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("DeriveVerifiedPrincipal: %v", err)
	}
	auth, err := f.issuer.IssueSystem(principal, workspace, action, "automation dispatch version resolution")
	if err != nil {
		t.Fatalf("IssueSystem: %v", err)
	}
	return auth
}

func TestQueriesResolveIDsNamesAndReturnDefensiveCopies(t *testing.T) {
	fixture := newCatalogFixture(t)
	ctx := context.Background()

	byID, err := fixture.service.GetDriver(ctx, " TEST ", " driver-1 ")
	if err != nil {
		t.Fatalf("GetDriver by ID: %v", err)
	}
	byName, err := fixture.service.GetDriver(ctx, "TEST", "demo")
	if err != nil {
		t.Fatalf("GetDriver by name: %v", err)
	}
	if byID.DriverID != "driver-1" || byName.DriverID != "driver-1" {
		t.Fatalf("resolved drivers = %+v, %+v", byID, byName)
	}
	if got := fixture.reader.calls; len(got) < 3 || got[1] != "get-driver:TEST:demo" || got[2] != "find-driver:TEST:demo" {
		t.Fatalf("reader calls = %v, want ID-first name fallback", got)
	}

	byID.Metadata["unrelated"] = "changed"
	if fixture.reader.drivers["driver-1"].Metadata["unrelated"] != "keep" {
		t.Fatal("GetDriver returned aliased metadata")
	}
	version, err := fixture.service.GetVersion(ctx, "TEST", "v1")
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	version.Manifest[ManifestTrustLevelKey] = "trusted"
	if fixture.reader.versions["v1"].Manifest[ManifestTrustLevelKey] != "untrusted" {
		t.Fatal("GetVersion returned aliased manifest")
	}

	drivers, err := fixture.service.ListDrivers(ctx, "TEST")
	if err != nil || len(drivers) != 1 {
		t.Fatalf("ListDrivers = %+v, %v", drivers, err)
	}
	set, err := fixture.service.ListVersions(ctx, "TEST", "demo")
	if err != nil || len(set.Versions) != 2 || set.Versions[0].VersionID != "v2" {
		t.Fatalf("ListVersions = %+v, %v", set, err)
	}
	if len(fixture.lifecycle.calls) != 0 {
		t.Fatalf("queries performed lifecycle writes: %v", fixture.lifecycle.calls)
	}
}

func TestGetDriverFallsBackOnlyOnNotFound(t *testing.T) {
	fixture := newCatalogFixture(t)
	backendErr := errors.New("backend unavailable")
	fixture.reader.forcedErr = backendErr
	_, err := fixture.service.GetDriver(context.Background(), "TEST", "demo")
	if !errors.Is(err, backendErr) {
		t.Fatalf("GetDriver error = %v, want backend error", err)
	}
	if len(fixture.reader.calls) != 1 {
		t.Fatalf("calls = %v, name fallback must not hide operational errors", fixture.reader.calls)
	}
}

func TestQueriesRejectWrongWorkspaceAndOwnership(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*catalogFixture)
		call   func(*Service) error
		want   error
	}{
		{
			name: "driver workspace", want: ErrWrongWorkspace,
			mutate: func(f *catalogFixture) { f.reader.drivers["driver-1"].WorkspaceKey = "OTHER" },
			call:   func(s *Service) error { _, err := s.GetDriver(context.Background(), "TEST", "driver-1"); return err },
		},
		{
			name: "version workspace", want: ErrWrongWorkspace,
			mutate: func(f *catalogFixture) { f.reader.versions["v1"].WorkspaceKey = "OTHER" },
			call:   func(s *Service) error { _, err := s.GetVersion(context.Background(), "TEST", "v1"); return err },
		},
		{
			name: "listed version owner", want: ErrVersionOwnership,
			mutate: func(f *catalogFixture) { f.reader.versions["v2"].DriverID = "driver-2" },
			call:   func(s *Service) error { _, err := s.ListVersions(context.Background(), "TEST", "driver-1"); return err },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCatalogFixture(t)
			test.mutate(fixture)
			if err := test.call(fixture.service); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestResolveEffectiveVersionIsActiveOnlySystemAuthorizedAndPure(t *testing.T) {
	fixture := newCatalogFixture(t)
	ctx := context.Background()
	auth := fixture.system(t, "TEST", ActionResolveEffectiveVersion)

	active, err := fixture.service.ResolveEffectiveVersion(ctx, auth, "TEST", "demo")
	if err != nil {
		t.Fatalf("ResolveEffectiveVersion: %v", err)
	}
	if active.Version.VersionID != "v1" || active.Driver.ActiveVersionID != active.Version.VersionID || !active.Approved || active.EffectiveTrust != DriverTrustTrusted {
		t.Fatalf("active resolution = %+v", active)
	}
	if len(fixture.lifecycle.calls) != 0 {
		t.Fatalf("ResolveEffectiveVersion wrote state: %v", fixture.lifecycle.calls)
	}
	for _, call := range fixture.reader.calls {
		if call == "get-version:TEST:v2" {
			t.Fatalf("effective resolution read inactive requested version: %v", fixture.reader.calls)
		}
	}
}

func TestResolveEffectiveVersionReportsActiveButUnapprovedTrust(t *testing.T) {
	fixture := newCatalogFixture(t)
	delete(fixture.reader.drivers["driver-1"].Metadata, ApprovedVersionMetadataKey("v1"))
	auth := fixture.system(t, "TEST", ActionResolveEffectiveVersion)

	active, err := fixture.service.ResolveEffectiveVersion(context.Background(), auth, "TEST", "driver-1")
	if err != nil {
		t.Fatalf("ResolveEffectiveVersion: %v", err)
	}
	if active.Approved || active.EffectiveTrust != DriverTrustUntrusted || active.Driver.ActiveVersionID != active.Version.VersionID {
		t.Fatalf("active unapproved resolution = %+v", active)
	}
}

func TestResolveRequestedVersionPreservesInactiveOperatorPreview(t *testing.T) {
	fixture := newCatalogFixture(t)
	auth := fixture.operator(t, "TEST", ActionResolveRequestedVersion)

	requested, err := fixture.service.ResolveRequestedVersion(context.Background(), auth, "TEST", "driver-1", "v2")
	if err != nil {
		t.Fatalf("ResolveRequestedVersion: %v", err)
	}
	if requested.Active || requested.Version.VersionID != "v2" || requested.Approved || requested.EffectiveTrust != DriverTrustUntrusted {
		t.Fatalf("requested resolution = %+v", requested)
	}
	if len(fixture.lifecycle.calls) != 0 {
		t.Fatalf("ResolveRequestedVersion wrote state: %v", fixture.lifecycle.calls)
	}

	fixture.reader.drivers["driver-1"].ActiveVersionID = ""
	requested, err = fixture.service.ResolveRequestedVersion(context.Background(), auth, "TEST", "driver-1", "v2")
	if err != nil || requested.Active || requested.Version.VersionID != "v2" {
		t.Fatalf("requested resolution without active version = %+v, %v", requested, err)
	}
}

func TestResolveEffectiveVersionRejectsInvalidState(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*catalogFixture)
		want   error
	}{
		{name: "no active version", mutate: func(f *catalogFixture) { f.reader.drivers["driver-1"].ActiveVersionID = "" }, want: ErrInvalid},
		{name: "wrong owner", mutate: func(f *catalogFixture) { f.reader.versions["v1"].DriverID = "driver-2" }, want: ErrVersionOwnership},
		{name: "validation pending", mutate: func(f *catalogFixture) { f.reader.versions["v1"].ValidationStatus = DriverVersionValidationPending }, want: ErrVersionNotValidated},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCatalogFixture(t)
			test.mutate(fixture)
			auth := fixture.system(t, "TEST", ActionResolveEffectiveVersion)
			_, err := fixture.service.ResolveEffectiveVersion(context.Background(), auth, "TEST", "driver-1")
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if len(fixture.lifecycle.calls) != 0 {
				t.Fatalf("invalid query wrote state: %v", fixture.lifecycle.calls)
			}
		})
	}
}

func TestResolveRequestedVersionRejectsInvalidState(t *testing.T) {
	for _, test := range []struct {
		name      string
		versionID string
		mutate    func(*catalogFixture)
		want      error
	}{
		{name: "empty version", versionID: " ", want: ErrInvalid},
		{name: "noncanonical version", versionID: " v2 ", want: ErrInvalid},
		{name: "wrong owner", versionID: "v2", mutate: func(f *catalogFixture) { f.reader.versions["v2"].DriverID = "driver-2" }, want: ErrVersionOwnership},
		{name: "validation pending", versionID: "v2", mutate: func(f *catalogFixture) { f.reader.versions["v2"].ValidationStatus = DriverVersionValidationPending }, want: ErrVersionNotValidated},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCatalogFixture(t)
			if test.mutate != nil {
				test.mutate(fixture)
			}
			auth := fixture.operator(t, "TEST", ActionResolveRequestedVersion)
			_, err := fixture.service.ResolveRequestedVersion(context.Background(), auth, "TEST", "driver-1", test.versionID)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if len(fixture.lifecycle.calls) != 0 {
				t.Fatalf("invalid requested query wrote state: %v", fixture.lifecycle.calls)
			}
		})
	}
}

func TestVersionResolversRejectInvalidAuthorityBeforeReading(t *testing.T) {
	t.Run("effective zero system authority", func(t *testing.T) {
		fixture := newCatalogFixture(t)
		_, err := fixture.service.ResolveEffectiveVersion(context.Background(), authority.SystemAuthority{}, "TEST", "driver-1")
		assertAdmissionDenied(t, err, authority.DenialInvalidAuthority)
		if len(fixture.reader.calls) != 0 {
			t.Fatalf("zero system authority reached reader: %v", fixture.reader.calls)
		}
	})

	t.Run("effective wrong workspace", func(t *testing.T) {
		fixture := newCatalogFixture(t)
		auth := fixture.system(t, "OTHER", ActionResolveEffectiveVersion)
		_, err := fixture.service.ResolveEffectiveVersion(context.Background(), auth, "TEST", "driver-1")
		assertAdmissionDenied(t, err, authority.DenialWrongWorkspace)
		if len(fixture.reader.calls) != 0 {
			t.Fatalf("wrong-workspace system authority reached reader: %v", fixture.reader.calls)
		}
	})

	t.Run("effective wrong action", func(t *testing.T) {
		fixture := newCatalogFixture(t)
		auth := fixture.system(t, "TEST", ActionResolveRequestedVersion)
		_, err := fixture.service.ResolveEffectiveVersion(context.Background(), auth, "TEST", "driver-1")
		assertAdmissionDenied(t, err, authority.DenialWrongAction)
		if len(fixture.reader.calls) != 0 {
			t.Fatalf("wrong-action system authority reached reader: %v", fixture.reader.calls)
		}
	})

	t.Run("effective expired", func(t *testing.T) {
		fixture := newCatalogFixture(t)
		auth := fixture.system(t, "TEST", ActionResolveEffectiveVersion)
		*fixture.now = fixture.now.Add(2 * time.Hour)
		_, err := fixture.service.ResolveEffectiveVersion(context.Background(), auth, "TEST", "driver-1")
		assertAdmissionDenied(t, err, authority.DenialExpired)
		if len(fixture.reader.calls) != 0 {
			t.Fatalf("expired system authority reached reader: %v", fixture.reader.calls)
		}
	})

	t.Run("effective foreign issuer", func(t *testing.T) {
		fixture := newCatalogFixture(t)
		other, err := authority.NewIssuerWithClock(func() time.Time { return *fixture.now })
		if err != nil {
			t.Fatalf("NewIssuerWithClock: %v", err)
		}
		principal, err := other.DeriveVerifiedPrincipal(authority.PrincipalClaims{
			Subject: "foreign-automation", Class: authority.ClassSystem, Workspace: "TEST",
			Actions: []authority.Action{ActionResolveEffectiveVersion}, ExpiresAt: fixture.now.Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("DeriveVerifiedPrincipal: %v", err)
		}
		auth, err := other.IssueSystem(principal, "TEST", ActionResolveEffectiveVersion, "foreign automation dispatch")
		if err != nil {
			t.Fatalf("IssueSystem: %v", err)
		}
		_, err = fixture.service.ResolveEffectiveVersion(context.Background(), auth, "TEST", "driver-1")
		assertAdmissionDenied(t, err, authority.DenialInvalidAuthority)
		if len(fixture.reader.calls) != 0 {
			t.Fatalf("foreign system authority reached reader: %v", fixture.reader.calls)
		}
	})

	t.Run("requested wrong action", func(t *testing.T) {
		fixture := newCatalogFixture(t)
		auth := fixture.operator(t, "TEST", ActionApproveVersion)
		_, err := fixture.service.ResolveRequestedVersion(context.Background(), auth, "TEST", "driver-1", "v2")
		assertAdmissionDenied(t, err, authority.DenialWrongAction)
		if len(fixture.reader.calls) != 0 {
			t.Fatalf("wrong-action operator authority reached reader: %v", fixture.reader.calls)
		}
	})
}

func TestLifecycleCommandsPreserveSemanticsAndMutationCoordinates(t *testing.T) {
	t.Run("approve", func(t *testing.T) {
		fixture := newCatalogFixture(t)
		auth := fixture.operator(t, "TEST", ActionApproveVersion)
		result, err := fixture.service.ApproveVersion(context.Background(), auth, VersionCommand{
			WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v2", ExpectedRevision: 7,
		})
		if err != nil {
			t.Fatalf("ApproveVersion: %v", err)
		}
		if !result.Approved || result.EffectiveTrust != DriverTrustTrusted || result.Active {
			t.Fatalf("approve result = %+v", result)
		}
		assertVersionResultContract(t, result, ActionApproveVersion, 8, SemanticImpactVersionTrustChanged)
		assertMutation(t, fixture.lifecycle, ActionApproveVersion, "v2")
	})

	t.Run("unapprove active leaves active but untrusted", func(t *testing.T) {
		fixture := newCatalogFixture(t)
		auth := fixture.operator(t, "TEST", ActionUnapproveVersion)
		result, err := fixture.service.UnapproveVersion(context.Background(), auth, VersionCommand{
			WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v1", ExpectedRevision: 7,
		})
		if err != nil {
			t.Fatalf("UnapproveVersion: %v", err)
		}
		if !result.Active || result.Approved || result.EffectiveTrust != DriverTrustUntrusted || result.Driver.Metadata["unrelated"] != "keep" {
			t.Fatalf("unapprove result = %+v", result)
		}
		assertVersionResultContract(t, result, ActionUnapproveVersion, 8, SemanticImpactVersionTrustChanged)
		assertMutation(t, fixture.lifecycle, ActionUnapproveVersion, "v1")
	})

	t.Run("activate requires and preserves approval", func(t *testing.T) {
		fixture := newCatalogFixture(t)
		fixture.reader.drivers["driver-1"].Metadata[ApprovedVersionMetadataKey("v2")] = "digest-v2"
		auth := fixture.operator(t, "TEST", ActionActivateVersion)
		result, err := fixture.service.ActivateVersion(context.Background(), auth, VersionCommand{
			WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v2", ExpectedRevision: 7,
		})
		if err != nil {
			t.Fatalf("ActivateVersion: %v", err)
		}
		if !result.Active || !result.Approved || result.Driver.ActiveVersionID != "v2" || result.Driver.Metadata["unrelated"] != "keep" || result.Driver.Metadata[ManifestTrustLevelKey] != "untrusted" {
			t.Fatalf("activate result = %+v", result)
		}
		assertVersionResultContract(t, result, ActionActivateVersion, 8, SemanticImpactEffectiveVersionChanged)
		assertMutation(t, fixture.lifecycle, ActionActivateVersion, "v2")
	})
}

func TestVersionScopedApprovalDoesNotTrustSiblingVersions(t *testing.T) {
	fixture := newCatalogFixture(t)
	fixture.reader.versions["v3"] = &DriverVersion{
		WorkspaceKey:     "TEST",
		DriverID:         "driver-1",
		VersionID:        "v3",
		Version:          3,
		SourceDigest:     "digest-v3",
		Manifest:         map[string]string{ManifestTrustLevelKey: "untrusted"},
		ValidationStatus: DriverVersionValidationPassed,
	}
	ctx := context.Background()

	approve, err := fixture.service.ApproveVersion(ctx, fixture.operator(t, "TEST", ActionApproveVersion), VersionCommand{
		WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v2", ExpectedRevision: 7,
	})
	if err != nil {
		t.Fatalf("ApproveVersion: %v", err)
	}
	if !VersionApproved(approve.Driver, fixture.reader.versions["v1"]) {
		t.Fatal("approving v2 removed the existing v1 approval")
	}
	if !VersionApproved(approve.Driver, fixture.reader.versions["v2"]) {
		t.Fatal("v2 is not approved after its approval command")
	}
	if VersionApproved(approve.Driver, fixture.reader.versions["v3"]) || EffectiveTrust(approve.Driver, fixture.reader.versions["v3"]) != DriverTrustUntrusted {
		t.Fatal("v3 inherited trust from the v2 approval")
	}

	fixture.reader.drivers["driver-1"] = cloneDriver(approve.Driver)
	activate, err := fixture.service.ActivateVersion(ctx, fixture.operator(t, "TEST", ActionActivateVersion), VersionCommand{
		WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v2", ExpectedRevision: 8,
	})
	if err != nil {
		t.Fatalf("ActivateVersion: %v", err)
	}
	if activate.Driver.ActiveVersionID != "v2" || !VersionApproved(activate.Driver, fixture.reader.versions["v1"]) || !VersionApproved(activate.Driver, fixture.reader.versions["v2"]) {
		t.Fatalf("activation did not preserve version-scoped approvals: %+v", activate.Driver)
	}
	if VersionApproved(activate.Driver, fixture.reader.versions["v3"]) {
		t.Fatal("activating v2 implicitly approved v3")
	}

	fixture.reader.drivers["driver-1"] = cloneDriver(activate.Driver)
	unapprove, err := fixture.service.UnapproveVersion(ctx, fixture.operator(t, "TEST", ActionUnapproveVersion), VersionCommand{
		WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v2", ExpectedRevision: 9,
	})
	if err != nil {
		t.Fatalf("UnapproveVersion: %v", err)
	}
	if VersionApproved(unapprove.Driver, fixture.reader.versions["v2"]) {
		t.Fatal("v2 remains approved after its unapproval command")
	}
	if !VersionApproved(unapprove.Driver, fixture.reader.versions["v1"]) {
		t.Fatal("unapproving v2 removed the sibling v1 approval")
	}
}

func assertVersionResultContract(t *testing.T, result *VersionResult, action authority.Action, revision uint64, impact string) {
	t.Helper()
	if result.Action != action || result.CommittedRevision != revision || result.Driver.Revision != revision || result.SemanticImpact != impact {
		t.Fatalf("version result contract = %+v, want action=%q revision=%d impact=%q", result, action, revision, impact)
	}
}

func assertMutation(t *testing.T, lifecycle *lifecycleFake, action authority.Action, versionID string) {
	t.Helper()
	if len(lifecycle.calls) != 1 || lifecycle.calls[0] != action || len(lifecycle.mutations) != 1 {
		t.Fatalf("lifecycle calls = %v mutations=%+v", lifecycle.calls, lifecycle.mutations)
	}
	mutation := lifecycle.mutations[0]
	if mutation.WorkspaceKey != "TEST" || mutation.DriverID != "driver-1" || mutation.VersionID != versionID || mutation.ExpectedRevision != 7 {
		t.Fatalf("mutation = %+v", mutation)
	}
}

func TestLifecycleCommandsRejectCorePreconditionsWithoutWriting(t *testing.T) {
	for _, test := range []struct {
		name    string
		action  authority.Action
		mutate  func(*catalogFixture)
		command VersionCommand
		want    error
	}{
		{name: "wrong driver workspace", action: ActionApproveVersion, mutate: func(f *catalogFixture) { f.reader.drivers["driver-1"].WorkspaceKey = "OTHER" }, command: command("v2", 7), want: ErrWrongWorkspace},
		{name: "wrong version workspace", action: ActionApproveVersion, mutate: func(f *catalogFixture) { f.reader.versions["v2"].WorkspaceKey = "OTHER" }, command: command("v2", 7), want: ErrWrongWorkspace},
		{name: "wrong owner", action: ActionApproveVersion, mutate: func(f *catalogFixture) { f.reader.versions["v2"].DriverID = "driver-2" }, command: command("v2", 7), want: ErrVersionOwnership},
		{name: "validation failed", action: ActionApproveVersion, mutate: func(f *catalogFixture) { f.reader.versions["v2"].ValidationStatus = DriverVersionValidationFailed }, command: command("v2", 7), want: ErrVersionNotValidated},
		{name: "activate validation failed", action: ActionActivateVersion, mutate: func(f *catalogFixture) {
			f.reader.versions["v2"].ValidationStatus = DriverVersionValidationFailed
			f.reader.drivers["driver-1"].Metadata[ApprovedVersionMetadataKey("v2")] = "digest-v2"
		}, command: command("v2", 7), want: ErrVersionNotValidated},
		{name: "zero expected revision", action: ActionApproveVersion, command: command("v2", 0), want: ErrInvalid},
		{name: "maximum signed expected revision", action: ActionApproveVersion, command: command("v2", uint64(math.MaxInt64)), want: ErrInvalid},
		{name: "maximum unsigned expected revision", action: ActionApproveVersion, command: command("v2", ^uint64(0)), want: ErrInvalid},
		{name: "noncanonical driver id", action: ActionApproveVersion, command: VersionCommand{WorkspaceKey: "TEST", DriverID: " driver-1", VersionID: "v2", ExpectedRevision: 7}, want: ErrInvalid},
		{name: "reserved driver id delimiter", action: ActionApproveVersion, command: VersionCommand{WorkspaceKey: "TEST", DriverID: "driver:versions", VersionID: "v2", ExpectedRevision: 7}, want: ErrInvalid},
		{name: "noncanonical version id", action: ActionApproveVersion, command: command(" v2 ", 7), want: ErrInvalid},
		{name: "persisted driver id uses reserved delimiter", action: ActionApproveVersion, mutate: func(f *catalogFixture) { f.reader.drivers["driver-1"].DriverID = "driver:versions" }, command: command("v2", 7), want: ErrInvalidPersistedState},
		{name: "persisted version owner uses reserved delimiter", action: ActionApproveVersion, mutate: func(f *catalogFixture) { f.reader.versions["v2"].DriverID = "driver:versions" }, command: command("v2", 7), want: ErrInvalidPersistedState},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCatalogFixture(t)
			if test.mutate != nil {
				test.mutate(fixture)
			}
			auth := fixture.operator(t, "TEST", test.action)
			var err error
			switch test.action {
			case ActionApproveVersion:
				_, err = fixture.service.ApproveVersion(context.Background(), auth, test.command)
			case ActionActivateVersion:
				_, err = fixture.service.ActivateVersion(context.Background(), auth, test.command)
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if len(fixture.lifecycle.calls) != 0 {
				t.Fatalf("precondition failure wrote state: %v", fixture.lifecycle.calls)
			}
		})
	}
}

func TestLifecycleCommandAcceptsLargestAdvanceableRevision(t *testing.T) {
	fixture := newCatalogFixture(t)
	fixture.reader.drivers["driver-1"].Revision = MaxExpectedRevision
	auth := fixture.operator(t, "TEST", ActionApproveVersion)

	result, err := fixture.service.ApproveVersion(context.Background(), auth, command("v2", MaxExpectedRevision))
	if err != nil {
		t.Fatalf("ApproveVersion(max advanceable revision): %v", err)
	}
	if result.CommittedRevision != uint64(math.MaxInt64) || result.Driver.Revision != uint64(math.MaxInt64) {
		t.Fatalf("result revision = committed %d driver %d, want %d", result.CommittedRevision, result.Driver.Revision, uint64(math.MaxInt64))
	}
	if len(fixture.lifecycle.mutations) != 1 || fixture.lifecycle.mutations[0].ExpectedRevision != MaxExpectedRevision {
		t.Fatalf("lifecycle mutations = %+v, want expected revision %d", fixture.lifecycle.mutations, MaxExpectedRevision)
	}
}

func TestActivateRequiresDurableApprovalPrecondition(t *testing.T) {
	fixture := newCatalogFixture(t)
	auth := fixture.operator(t, "TEST", ActionActivateVersion)
	_, err := fixture.service.ActivateVersion(context.Background(), auth, command("v2", 7))
	if !errors.Is(err, ErrVersionNotApproved) {
		t.Fatalf("ActivateVersion error = %v, want ErrVersionNotApproved", err)
	}
	if len(fixture.lifecycle.calls) != 1 || fixture.lifecycle.calls[0] != ActionActivateVersion {
		t.Fatalf("lifecycle calls = %v, durable port must enforce approval", fixture.lifecycle.calls)
	}
}

func TestUnapproveDoesNotRequirePassedValidation(t *testing.T) {
	fixture := newCatalogFixture(t)
	fixture.reader.versions["v1"].ValidationStatus = DriverVersionValidationFailed
	auth := fixture.operator(t, "TEST", ActionUnapproveVersion)
	result, err := fixture.service.UnapproveVersion(context.Background(), auth, command("v1", 7))
	if err != nil {
		t.Fatalf("UnapproveVersion: %v", err)
	}
	if !result.Active || result.Approved || result.EffectiveTrust != DriverTrustUntrusted {
		t.Fatalf("unapprove failed-validation result = %+v", result)
	}
	assertVersionResultContract(t, result, ActionUnapproveVersion, 8, SemanticImpactVersionTrustChanged)
}

func TestLifecycleCommandsPropagateDurablePortErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		revision uint64
		portErr  error
	}{
		{name: "stale revision", revision: 6, portErr: fmt.Errorf("backend CAS: %w", ErrStaleRevision)},
		{name: "durable precondition", revision: 7, portErr: fmt.Errorf("backend precondition: %w", ErrVersionNotApproved)},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCatalogFixture(t)
			fixture.lifecycle.err = test.portErr
			auth := fixture.operator(t, "TEST", ActionApproveVersion)
			_, err := fixture.service.ApproveVersion(context.Background(), auth, command("v2", test.revision))
			if !errors.Is(err, test.portErr) {
				t.Fatalf("error = %v, want wrapped port error %v", err, test.portErr)
			}
			if len(fixture.lifecycle.mutations) != 1 || fixture.lifecycle.mutations[0].ExpectedRevision != test.revision {
				t.Fatalf("mutation = %+v, durable port must decide CAS/preconditions", fixture.lifecycle.mutations)
			}
		})
	}
}

func TestLifecycleDuplicateRetryReachesDurablePort(t *testing.T) {
	fixture := newCatalogFixture(t)
	// The original unapprove committed revision 8. The aggregate then advanced,
	// activated v2, and re-approved v1 before the lost-response retry arrived.
	fixture.reader.drivers["driver-1"].Revision = 10
	fixture.reader.drivers["driver-1"].ActiveVersionID = "v2"
	fixture.reader.drivers["driver-1"].Metadata[ApprovedVersionMetadataKey("v2")] = "digest-v2"
	fixture.reader.drivers["driver-1"].Metadata["later"] = "state"
	fixture.lifecycle.result = func(action authority.Action, mutation LifecycleMutation) (*LifecycleResult, error) {
		if action != ActionUnapproveVersion || mutation.ExpectedRevision != 7 {
			t.Fatalf("retry mutation = %q %+v", action, mutation)
		}
		return &LifecycleResult{
			Driver:            cloneDriver(fixture.reader.drivers["driver-1"]),
			Version:           cloneVersion(fixture.reader.versions["v1"]),
			Replayed:          true,
			CommittedRevision: 8,
			SemanticImpact:    SemanticImpactVersionTrustChanged,
		}, nil
	}
	auth := fixture.operator(t, "TEST", ActionUnapproveVersion)
	result, err := fixture.service.UnapproveVersion(context.Background(), auth, command("v1", 7))
	if err != nil {
		t.Fatalf("UnapproveVersion replay: %v", err)
	}
	if !result.Replayed || !result.Approved || result.Active || result.Driver.Revision != 10 || result.CommittedRevision != 8 || len(fixture.lifecycle.calls) != 1 {
		t.Fatalf("replay result = %+v calls=%v", result, fixture.lifecycle.calls)
	}
}

func TestLifecycleCommandAcceptsLaterPostCommitRead(t *testing.T) {
	fixture := newCatalogFixture(t)
	fixture.lifecycle.result = func(action authority.Action, mutation LifecycleMutation) (*LifecycleResult, error) {
		if action != ActionApproveVersion || mutation.ExpectedRevision != 7 {
			t.Fatalf("mutation = %q %+v", action, mutation)
		}
		// Approve committed revision 8, then another command unapproved the
		// version at revision 9 before FleetDB's post-commit read completed.
		driver := cloneDriver(fixture.reader.drivers["driver-1"])
		driver.Revision = 9
		delete(driver.Metadata, ApprovedVersionMetadataKey("v2"))
		driver.Metadata["later"] = "state"
		return &LifecycleResult{
			Driver:            driver,
			Version:           cloneVersion(fixture.reader.versions["v2"]),
			CommittedRevision: 8,
			SemanticImpact:    SemanticImpactVersionTrustChanged,
		}, nil
	}
	auth := fixture.operator(t, "TEST", ActionApproveVersion)
	result, err := fixture.service.ApproveVersion(context.Background(), auth, command("v2", 7))
	if err != nil {
		t.Fatalf("ApproveVersion post-commit read: %v", err)
	}
	if result.Replayed || result.CommittedRevision != 8 || result.Driver.Revision != 9 || result.Approved || result.Driver.Metadata["later"] != "state" {
		t.Fatalf("post-commit result = %+v", result)
	}
}

func TestLifecycleCommandsRejectInvalidDurableResults(t *testing.T) {
	validApproveResult := func(f *catalogFixture) *LifecycleResult {
		driver := cloneDriver(f.reader.drivers["driver-1"])
		driver.Revision = 8
		driver.Metadata[ApprovedVersionMetadataKey("v2")] = "digest-v2"
		return &LifecycleResult{
			Driver: driver, Version: cloneVersion(f.reader.versions["v2"]),
			CommittedRevision: 8, SemanticImpact: SemanticImpactVersionTrustChanged,
		}
	}
	for _, test := range []struct {
		name   string
		result func(*catalogFixture) *LifecycleResult
	}{
		{name: "nil result", result: func(*catalogFixture) *LifecycleResult { return nil }},
		{
			name: "missing committed revision",
			result: func(f *catalogFixture) *LifecycleResult {
				result := validApproveResult(f)
				result.CommittedRevision = 0
				return result
			},
		},
		{
			name: "committed revision did not advance",
			result: func(f *catalogFixture) *LifecycleResult {
				result := validApproveResult(f)
				result.CommittedRevision = 7
				return result
			},
		},
		{
			name: "committed revision skipped",
			result: func(f *catalogFixture) *LifecycleResult {
				result := validApproveResult(f)
				result.CommittedRevision = 9
				result.Driver.Revision = 9
				return result
			},
		},
		{
			name: "replay reports another commit",
			result: func(f *catalogFixture) *LifecycleResult {
				result := validApproveResult(f)
				result.Replayed = true
				result.CommittedRevision = 9
				result.Driver.Revision = 10
				return result
			},
		},
		{
			name: "current revision precedes commit",
			result: func(f *catalogFixture) *LifecycleResult {
				result := validApproveResult(f)
				result.Driver.Revision = 7
				return result
			},
		},
		{
			name: "replay current revision precedes commit",
			result: func(f *catalogFixture) *LifecycleResult {
				result := validApproveResult(f)
				result.Replayed = true
				result.Driver.Revision = 7
				return result
			},
		},
		{
			name: "wrong semantic impact",
			result: func(f *catalogFixture) *LifecycleResult {
				result := validApproveResult(f)
				result.SemanticImpact = SemanticImpactEffectiveVersionChanged
				return result
			},
		},
		{
			name: "approve remains unapproved",
			result: func(f *catalogFixture) *LifecycleResult {
				result := validApproveResult(f)
				delete(result.Driver.Metadata, ApprovedVersionMetadataKey("v2"))
				return result
			},
		},
		{
			name: "drops unrelated metadata",
			result: func(f *catalogFixture) *LifecycleResult {
				result := validApproveResult(f)
				result.Driver.Metadata = map[string]string{ApprovedVersionMetadataKey("v2"): "digest-v2"}
				return result
			},
		},
		{
			name: "adds unrelated metadata",
			result: func(f *catalogFixture) *LifecycleResult {
				result := validApproveResult(f)
				result.Driver.Metadata["injected"] = "value"
				return result
			},
		},
		{
			name: "adds sibling approval metadata",
			result: func(f *catalogFixture) *LifecycleResult {
				result := validApproveResult(f)
				result.Driver.Metadata[ApprovedVersionMetadataKey("v3")] = "digest-v3"
				return result
			},
		},
		{
			name: "stores legacy approval marker instead of source digest",
			result: func(f *catalogFixture) *LifecycleResult {
				result := validApproveResult(f)
				result.Driver.Metadata[ApprovedVersionMetadataKey("v2")] = "trusted"
				return result
			},
		},
		{
			name: "rewrites immutable version",
			result: func(f *catalogFixture) *LifecycleResult {
				result := validApproveResult(f)
				result.Driver.Metadata[ApprovedVersionMetadataKey("v2")] = "changed-digest"
				result.Version.SourceDigest = "changed-digest"
				return result
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCatalogFixture(t)
			fixture.lifecycle.result = func(authority.Action, LifecycleMutation) (*LifecycleResult, error) {
				return test.result(fixture), nil
			}
			auth := fixture.operator(t, "TEST", ActionApproveVersion)
			_, err := fixture.service.ApproveVersion(context.Background(), auth, command("v2", 7))
			if !errors.Is(err, ErrInvalidPersistedState) {
				t.Fatalf("error = %v, want ErrInvalidPersistedState", err)
			}
		})
	}
}

func TestLifecycleCommandsRejectActionUnexpectedMetadataResults(t *testing.T) {
	t.Run("unapprove must remove the approval key", func(t *testing.T) {
		fixture := newCatalogFixture(t)
		fixture.lifecycle.result = func(authority.Action, LifecycleMutation) (*LifecycleResult, error) {
			driver := cloneDriver(fixture.reader.drivers["driver-1"])
			driver.Revision = 8
			driver.Metadata[ApprovedVersionMetadataKey("v1")] = "wrong-digest"
			return &LifecycleResult{
				Driver: driver, Version: cloneVersion(fixture.reader.versions["v1"]),
				CommittedRevision: 8, SemanticImpact: SemanticImpactVersionTrustChanged,
			}, nil
		}
		auth := fixture.operator(t, "TEST", ActionUnapproveVersion)
		_, err := fixture.service.UnapproveVersion(context.Background(), auth, command("v1", 7))
		if !errors.Is(err, ErrInvalidPersistedState) {
			t.Fatalf("error = %v, want ErrInvalidPersistedState", err)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*catalogFixture, *Driver)
	}{
		{
			name: "activate adds unrelated metadata",
			mutate: func(_ *catalogFixture, driver *Driver) {
				driver.Metadata["injected"] = "value"
			},
		},
		{
			name: "activate writes a non-manifest value",
			mutate: func(_ *catalogFixture, driver *Driver) {
				driver.Metadata[ManifestTrustLevelKey] = "trusted"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCatalogFixture(t)
			fixture.reader.drivers["driver-1"].Metadata[ApprovedVersionMetadataKey("v2")] = "digest-v2"
			fixture.lifecycle.result = func(authority.Action, LifecycleMutation) (*LifecycleResult, error) {
				driver := cloneDriver(fixture.reader.drivers["driver-1"])
				driver.Revision = 8
				driver.ActiveVersionID = "v2"
				driver.Status = DriverStatusActive
				driver.Metadata[ManifestTrustLevelKey] = "untrusted"
				test.mutate(fixture, driver)
				return &LifecycleResult{
					Driver: driver, Version: cloneVersion(fixture.reader.versions["v2"]),
					CommittedRevision: 8, SemanticImpact: SemanticImpactEffectiveVersionChanged,
				}, nil
			}
			auth := fixture.operator(t, "TEST", ActionActivateVersion)
			_, err := fixture.service.ActivateVersion(context.Background(), auth, command("v2", 7))
			if !errors.Is(err, ErrInvalidPersistedState) {
				t.Fatalf("error = %v, want ErrInvalidPersistedState", err)
			}
		})
	}
}

func TestLifecycleCommandsRejectInvalidAuthority(t *testing.T) {
	t.Run("zero value", func(t *testing.T) {
		fixture := newCatalogFixture(t)
		_, err := fixture.service.ApproveVersion(context.Background(), authority.OperatorAuthority{}, command("v2", 7))
		assertAdmissionDenied(t, err, authority.DenialInvalidAuthority)
		if len(fixture.lifecycle.calls) != 0 {
			t.Fatal("zero authority reached lifecycle port")
		}
	})

	t.Run("wrong workspace", func(t *testing.T) {
		fixture := newCatalogFixture(t)
		auth := fixture.operator(t, "OTHER", ActionApproveVersion)
		_, err := fixture.service.ApproveVersion(context.Background(), auth, command("v2", 7))
		assertAdmissionDenied(t, err, authority.DenialWrongWorkspace)
	})

	t.Run("wrong action", func(t *testing.T) {
		fixture := newCatalogFixture(t)
		auth := fixture.operator(t, "TEST", ActionApproveVersion)
		_, err := fixture.service.ActivateVersion(context.Background(), auth, command("v2", 7))
		assertAdmissionDenied(t, err, authority.DenialWrongAction)
	})

	t.Run("expired", func(t *testing.T) {
		fixture := newCatalogFixture(t)
		auth := fixture.operator(t, "TEST", ActionApproveVersion)
		*fixture.now = fixture.now.Add(2 * time.Hour)
		_, err := fixture.service.ApproveVersion(context.Background(), auth, command("v2", 7))
		assertAdmissionDenied(t, err, authority.DenialExpired)
	})

	t.Run("unregistered operation", func(t *testing.T) {
		fixture := newCatalogFixture(t)
		admission, err := fixture.issuer.NewAdmission(authority.OperatorOnly(ActionApproveVersion))
		if err != nil {
			t.Fatalf("NewAdmission: %v", err)
		}
		fixture.service.admission = admission
		auth := fixture.operator(t, "TEST", ActionActivateVersion)
		_, err = fixture.service.ActivateVersion(context.Background(), auth, command("v2", 7))
		assertAdmissionDenied(t, err, authority.DenialUnknownOperation)
	})
}

func assertAdmissionDenied(t *testing.T, err error, reason authority.DenialReason) {
	t.Helper()
	if !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("error = %v, want ErrAdmissionDenied", err)
	}
	var admissionErr *authority.AdmissionError
	if !errors.As(err, &admissionErr) || admissionErr.Reason != reason {
		t.Fatalf("error = %#v, want reason %q", err, reason)
	}
}

func command(versionID string, revision uint64) VersionCommand {
	return VersionCommand{WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: versionID, ExpectedRevision: revision}
}

func TestServiceFailsClosedWhenDependenciesAreMissing(t *testing.T) {
	fixture := newCatalogFixture(t)
	if _, err := New(nil, nil, nil).GetDriver(context.Background(), "TEST", "driver-1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil reader error = %v", err)
	}
	auth := fixture.operator(t, "TEST", ActionApproveVersion)
	if _, err := New(fixture.reader, nil, fixture.service.admission).ApproveVersion(context.Background(), auth, command("v2", 7)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil lifecycle error = %v", err)
	}
	if _, err := New(fixture.reader, fixture.lifecycle, nil).ApproveVersion(context.Background(), auth, command("v2", 7)); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("nil admission error = %v", err)
	}
}
