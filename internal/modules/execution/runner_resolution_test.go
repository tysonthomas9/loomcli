package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type runnerCatalogFake struct {
	candidates []RunnerCatalogCandidate
	err        error
	calls      int
}

func (fake *runnerCatalogFake) ActiveBuiltinCandidates(_ context.Context, _, _ string) ([]RunnerCatalogCandidate, error) {
	fake.calls++
	return fake.candidates, fake.err
}

func runnerResolutionFixture(t *testing.T, catalog RunnerCatalog) (*RunnerResolutionService, *authority.Issuer, time.Time) {
	t.Helper()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	admission, err := issuer.NewAdmission(authority.Allow(ActionResolveTrustedRunner, authority.ClassSystem))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewRunnerResolutionService(catalog, admission)
	if err != nil {
		t.Fatal(err)
	}
	return service, issuer, now
}

func runnerResolutionAuthority(t *testing.T, issuer *authority.Issuer, now time.Time, workspace string) authority.SystemAuthority {
	t.Helper()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "execution-runner-resolution", Class: authority.ClassSystem,
		Workspace: workspace, Actions: []authority.Action{ActionResolveTrustedRunner},
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := issuer.IssueSystem(principal, workspace, ActionResolveTrustedRunner, "resolve trusted builtin runner")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRunnerResolutionSelectsDeterministicTrustedManagedOwner(t *testing.T) {
	catalog := &runnerCatalogFake{candidates: []RunnerCatalogCandidate{
		{
			WorkspaceKey: "TEST", DriverID: "z-owner", VersionID: "z-v1",
			ManagedBuiltin: true, Trusted: true,
			Manifest: map[string]string{runnerManifestKey: `[{"name":"local-task-runner","kind":"flue-workflow","entrypoint":"local-task-runner"}]`},
		},
		{
			WorkspaceKey: "TEST", DriverID: "a-owner", VersionID: "a-v1",
			ManagedBuiltin: true, Trusted: true,
			Manifest: map[string]string{runnerManifestKey: `[{"name":"local-task-runner","kind":"flue-workflow","entrypoint":"local-task-runner"}]`},
		},
	}}
	service, issuer, now := runnerResolutionFixture(t, catalog)
	result, err := service.ResolveTrustedRunner(
		context.Background(),
		runnerResolutionAuthority(t, issuer, now, "TEST"),
		ResolveTrustedRunnerCommand{WorkspaceKey: "TEST", RunnerName: "local-task-runner"},
	)
	if err != nil {
		t.Fatalf("ResolveTrustedRunner: %v", err)
	}
	if result.DriverID != "a-owner" || result.VersionID != "a-v1" || result.Spec.Entrypoint != "local-task-runner" {
		t.Fatalf("result = %+v", result)
	}
	if catalog.candidates[0].DriverID != "z-owner" {
		t.Fatal("resolution sorted the catalog-owned candidate slice in place")
	}
}

func TestRunnerResolutionRejectsUntrustedCustomAndMalformedCandidates(t *testing.T) {
	candidates := []RunnerCatalogCandidate{
		{
			WorkspaceKey: "TEST", DriverID: "custom", VersionID: "custom-v1",
			ManagedBuiltin: false, Trusted: true,
			Manifest: map[string]string{runnerManifestKey: `[{"name":"local-task-runner","kind":"flue-workflow","entrypoint":"local-task-runner"}]`},
		},
		{
			WorkspaceKey: "TEST", DriverID: "builtin-untrusted", VersionID: "v1",
			ManagedBuiltin: true, Trusted: false,
			Manifest: map[string]string{runnerManifestKey: `[{"name":"local-task-runner","kind":"flue-workflow","entrypoint":"local-task-runner"}]`},
		},
		{
			WorkspaceKey: "TEST", DriverID: "builtin-escape", VersionID: "v1",
			ManagedBuiltin: true, Trusted: true,
			Manifest: map[string]string{runnerManifestKey: `[{"name":"local-task-runner","kind":"flue-workflow","entrypoint":"../escape"}]`},
		},
		{
			WorkspaceKey: "TEST", DriverID: "builtin-windows-escape", VersionID: "v1",
			ManagedBuiltin: true, Trusted: true,
			Manifest: map[string]string{runnerManifestKey: `[{"name":"local-task-runner","kind":"flue-workflow","entrypoint":"..\\escape"}]`},
		},
	}
	service, issuer, now := runnerResolutionFixture(t, &runnerCatalogFake{candidates: candidates})
	_, err := service.ResolveTrustedRunner(
		context.Background(),
		runnerResolutionAuthority(t, issuer, now, "TEST"),
		ResolveTrustedRunnerCommand{WorkspaceKey: "TEST", RunnerName: "local-task-runner"},
	)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolveTrustedRunner err = %v, want ErrNotFound", err)
	}
}

func TestRunnerResolutionAuthorityAndScopeFailBeforeCatalog(t *testing.T) {
	catalog := &runnerCatalogFake{}
	service, issuer, now := runnerResolutionFixture(t, catalog)

	_, err := service.ResolveTrustedRunner(
		context.Background(),
		runnerResolutionAuthority(t, issuer, now, "OTHER"),
		ResolveTrustedRunnerCommand{WorkspaceKey: "TEST", RunnerName: "local-task-runner"},
	)
	if !errors.Is(err, authority.ErrAdmissionDenied) || catalog.calls != 0 {
		t.Fatalf("wrong-scope err=%v calls=%d", err, catalog.calls)
	}

	_, err = service.ResolveTrustedRunner(
		context.Background(),
		runnerResolutionAuthority(t, issuer, now, "TEST"),
		ResolveTrustedRunnerCommand{WorkspaceKey: " TEST ", RunnerName: "local-task-runner"},
	)
	if !errors.Is(err, ErrInvalid) || catalog.calls != 0 {
		t.Fatalf("noncanonical command err=%v calls=%d", err, catalog.calls)
	}
}

func TestRunnerResolutionFailsClosedOnCatalogError(t *testing.T) {
	catalog := &runnerCatalogFake{err: ErrUnavailable}
	service, issuer, now := runnerResolutionFixture(t, catalog)
	_, err := service.ResolveTrustedRunner(
		context.Background(),
		runnerResolutionAuthority(t, issuer, now, "TEST"),
		ResolveTrustedRunnerCommand{WorkspaceKey: "TEST", RunnerName: "local-task-runner"},
	)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ResolveTrustedRunner err = %v, want unavailable", err)
	}
}

func TestRunnerResolutionRejectsDeprecatedOpenShellWithoutCatalogLookup(t *testing.T) {
	catalog := &runnerCatalogFake{candidates: []RunnerCatalogCandidate{{
		WorkspaceKey: "TEST", DriverID: "epic-runner", VersionID: "epic-v1",
		ManagedBuiltin: true, Trusted: true,
		Manifest: map[string]string{runnerManifestKey: `[{"name":"openshell-task-runner","kind":"flue-workflow","entrypoint":"openshell-task-runner"}]`},
	}}}
	service, issuer, now := runnerResolutionFixture(t, catalog)
	_, err := service.ResolveTrustedRunner(
		context.Background(),
		runnerResolutionAuthority(t, issuer, now, "TEST"),
		ResolveTrustedRunnerCommand{WorkspaceKey: "TEST", RunnerName: "openshell-task-runner"},
	)
	if !errors.Is(err, ErrNotFound) || catalog.calls != 0 {
		t.Fatalf("ResolveTrustedRunner err=%v calls=%d, want fail-closed before catalog", err, catalog.calls)
	}
}

func TestRunnerResolutionNilFacadeFailsClosed(t *testing.T) {
	var service *RunnerResolutionService
	_, err := service.ResolveTrustedRunner(
		context.Background(),
		authority.SystemAuthority{},
		ResolveTrustedRunnerCommand{WorkspaceKey: "TEST", RunnerName: "local-task-runner"},
	)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ResolveTrustedRunner nil service err = %v, want ErrUnavailable", err)
	}
	_, err = service.ResolveTrustedRunner(
		context.Background(),
		authority.SystemAuthority{},
		ResolveTrustedRunnerCommand{WorkspaceKey: " TEST ", RunnerName: "local-task-runner"},
	)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("ResolveTrustedRunner nil service noncanonical err = %v, want ErrInvalid", err)
	}
}
