package authoring

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

var errNativeAuthoringLostResponse = errors.New("simulated committed command with lost response")

type nativeAuthoringStateFake struct {
	workflowcatalog.API
	workflowcatalog.VersionAuthoringAPI

	driver  *workflowcatalog.Driver
	version *workflowcatalog.DriverVersion
	receipt *workflowcatalog.Driver

	authorCalls   int
	approveCalls  int
	activateCalls int
	authorCAS     []uint64
	approveCAS    []uint64
	activateCAS   []uint64
	actors        []string

	loseAuthorOnce          bool
	loseApproveOnce         bool
	loseActivateOnce        bool
	advanceAfterAuthorOnce  bool
	conflictBeforeApprove   bool
	authorResponseWasLost   bool
	approveResponseWasLost  bool
	activateResponseWasLost bool
	advancedAfterAuthor     bool
	conflictedBeforeApprove bool
}

func (fake *nativeAuthoringStateFake) GetDriver(
	_ context.Context,
	workspace, driverRef string,
) (*workflowcatalog.Driver, error) {
	if fake.driver == nil {
		return nil, workflowcatalog.ErrNotFound
	}
	if workspace != fake.driver.WorkspaceKey ||
		(driverRef != fake.driver.DriverID && driverRef != fake.driver.Name) {
		return nil, workflowcatalog.ErrNotFound
	}
	return cloneNativeDriver(fake.driver), nil
}

func (fake *nativeAuthoringStateFake) GetVersion(
	_ context.Context,
	workspace, versionID string,
) (*workflowcatalog.DriverVersion, error) {
	if fake.version == nil || workspace != fake.version.WorkspaceKey || versionID != fake.version.VersionID {
		return nil, workflowcatalog.ErrNotFound
	}
	return cloneNativeVersion(fake.version), nil
}

func (fake *nativeAuthoringStateFake) AuthorVersion(
	_ context.Context,
	auth authority.OperatorAuthority,
	command workflowcatalog.AuthorVersionCommand,
) (*workflowcatalog.AuthorVersionResult, error) {
	fake.authorCalls++
	fake.authorCAS = append(fake.authorCAS, command.ExpectedRevision)
	fake.actors = append(fake.actors, auth.Subject())
	if auth.Action() != workflowcatalog.ActionAuthorVersion ||
		auth.Workspace() != command.WorkspaceKey {
		return nil, authority.ErrAdmissionDenied
	}
	created := fake.driver == nil
	if created {
		if command.ExpectedRevision != 0 {
			return nil, workflowcatalog.ErrStaleRevision
		}
		fake.driver = &workflowcatalog.Driver{
			WorkspaceKey: command.WorkspaceKey,
			DriverID:     command.DriverID,
			Name:         command.DriverName,
			Status:       workflowcatalog.DriverStatusDraft,
			TrustLevel:   workflowcatalog.DriverTrustUntrusted,
			Metadata:     map[string]string{},
			Revision:     1,
		}
		fake.version = &workflowcatalog.DriverVersion{
			WorkspaceKey:     command.WorkspaceKey,
			DriverID:         command.DriverID,
			VersionID:        command.VersionID,
			Version:          1,
			SourceRef:        command.SourceRef,
			SourceDigest:     command.SourceDigest,
			BundleRef:        command.BundleRef,
			BundleDigest:     command.BundleDigest,
			Runtime:          command.Runtime,
			Manifest:         cloneNativeMap(command.Manifest),
			ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
			CreatedBy:        auth.Subject(),
		}
		fake.version.Manifest[workflowcatalog.ManifestTrustLevelKey] = string(workflowcatalog.DriverTrustUntrusted)
		fake.receipt = cloneNativeDriver(fake.driver)
	}
	if fake.advanceAfterAuthorOnce && !fake.advancedAfterAuthor {
		fake.advancedAfterAuthor = true
		fake.driver.Revision++
	}
	result := &workflowcatalog.AuthorVersionResult{
		Action:            workflowcatalog.ActionAuthorVersion,
		Driver:            cloneNativeDriver(fake.receipt),
		Version:           cloneNativeVersion(fake.version),
		CreatedDriver:     created,
		CreatedVersion:    created,
		ReusedVersion:     !created,
		Replayed:          !created,
		CommittedRevision: 1,
		SemanticImpact:    workflowcatalog.SemanticImpactVersionAuthored,
	}
	if fake.loseAuthorOnce && !fake.authorResponseWasLost {
		fake.authorResponseWasLost = true
		return nil, errNativeAuthoringLostResponse
	}
	return result, nil
}

func (*nativeAuthoringStateFake) AuthorManagedVersion(
	context.Context,
	authority.SystemAuthority,
	workflowcatalog.AuthorManagedVersionCommand,
) (*workflowcatalog.AuthorVersionResult, error) {
	return nil, errors.New("unexpected managed authoring")
}

func (fake *nativeAuthoringStateFake) ApproveVersion(
	_ context.Context,
	auth authority.OperatorAuthority,
	command workflowcatalog.VersionCommand,
) (*workflowcatalog.VersionResult, error) {
	fake.approveCalls++
	fake.approveCAS = append(fake.approveCAS, command.ExpectedRevision)
	fake.actors = append(fake.actors, auth.Subject())
	if auth.Action() != workflowcatalog.ActionApproveVersion ||
		auth.Workspace() != command.WorkspaceKey {
		return nil, authority.ErrAdmissionDenied
	}
	if fake.conflictBeforeApprove && !fake.conflictedBeforeApprove {
		fake.conflictedBeforeApprove = true
		fake.driver.Revision++
	}
	if command.ExpectedRevision != fake.driver.Revision {
		return nil, workflowcatalog.ErrStaleRevision
	}
	fake.driver.Metadata[workflowcatalog.ApprovedVersionMetadataKey(fake.version.VersionID)] = fake.version.SourceDigest
	fake.driver.Revision++
	result := fake.versionResult(workflowcatalog.ActionApproveVersion)
	if fake.loseApproveOnce && !fake.approveResponseWasLost {
		fake.approveResponseWasLost = true
		return nil, errNativeAuthoringLostResponse
	}
	return result, nil
}

func (*nativeAuthoringStateFake) UnapproveVersion(
	context.Context,
	authority.OperatorAuthority,
	workflowcatalog.VersionCommand,
) (*workflowcatalog.VersionResult, error) {
	return nil, errors.New("unexpected unapprove")
}

func (fake *nativeAuthoringStateFake) ActivateVersion(
	_ context.Context,
	auth authority.OperatorAuthority,
	command workflowcatalog.VersionCommand,
) (*workflowcatalog.VersionResult, error) {
	fake.activateCalls++
	fake.activateCAS = append(fake.activateCAS, command.ExpectedRevision)
	fake.actors = append(fake.actors, auth.Subject())
	if auth.Action() != workflowcatalog.ActionActivateVersion ||
		auth.Workspace() != command.WorkspaceKey {
		return nil, authority.ErrAdmissionDenied
	}
	if command.ExpectedRevision != fake.driver.Revision {
		return nil, workflowcatalog.ErrStaleRevision
	}
	if !workflowcatalog.VersionApproved(fake.driver, fake.version) {
		return nil, workflowcatalog.ErrVersionNotApproved
	}
	fake.driver.ActiveVersionID = fake.version.VersionID
	fake.driver.Status = workflowcatalog.DriverStatusActive
	fake.driver.Revision++
	result := fake.versionResult(workflowcatalog.ActionActivateVersion)
	if fake.loseActivateOnce && !fake.activateResponseWasLost {
		fake.activateResponseWasLost = true
		return nil, errNativeAuthoringLostResponse
	}
	return result, nil
}

func (fake *nativeAuthoringStateFake) versionResult(action authority.Action) *workflowcatalog.VersionResult {
	return &workflowcatalog.VersionResult{
		Action:            action,
		Driver:            cloneNativeDriver(fake.driver),
		Version:           cloneNativeVersion(fake.version),
		Active:            fake.driver.ActiveVersionID == fake.version.VersionID,
		Approved:          workflowcatalog.VersionApproved(fake.driver, fake.version),
		EffectiveTrust:    workflowcatalog.EffectiveTrust(fake.driver, fake.version),
		CommittedRevision: fake.driver.Revision,
	}
}

func TestAuthorNativeFlueDriverConvergesAfterEachCommittedLostResponseSplit(t *testing.T) {
	tests := []struct {
		name          string
		configure     func(*nativeAuthoringStateFake)
		options       func(appworkflowauthoring.NativeOptions) appworkflowauthoring.NativeOptions
		wantApprove   int
		wantActivate  int
		wantActivated bool
	}{
		{
			name:      "author",
			configure: func(fake *nativeAuthoringStateFake) { fake.loseAuthorOnce = true },
			options:   func(opts appworkflowauthoring.NativeOptions) appworkflowauthoring.NativeOptions { return opts },
		},
		{
			name: "approve before activate",
			configure: func(fake *nativeAuthoringStateFake) {
				fake.loseApproveOnce = true
			},
			options: func(opts appworkflowauthoring.NativeOptions) appworkflowauthoring.NativeOptions {
				opts.Trust, opts.Activate = workflowcatalog.DriverTrustTrusted, true
				return opts
			},
			wantApprove: 1, wantActivate: 1, wantActivated: true,
		},
		{
			name: "activate",
			configure: func(fake *nativeAuthoringStateFake) {
				fake.loseActivateOnce = true
			},
			options: func(opts appworkflowauthoring.NativeOptions) appworkflowauthoring.NativeOptions {
				opts.Trust, opts.Activate = workflowcatalog.DriverTrustTrusted, true
				return opts
			},
			wantApprove: 1, wantActivate: 1, wantActivated: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &nativeAuthoringStateFake{}
			test.configure(fake)
			authorities := nativeTestAuthorities(t, "TEST", "operator-1")
			options := test.options(nativeAuthoringOptions(t))

			if _, err := authorNativeForTest(
				context.Background(), fake, fake, authorities, options,
			); !errors.Is(err, errNativeAuthoringLostResponse) {
				t.Fatalf("first attempt error = %v, want lost response", err)
			}
			result, err := authorNativeForTest(
				context.Background(), fake, fake, authorities, options,
			)
			if err != nil {
				t.Fatalf("retry: %v", err)
			}
			if fake.authorCalls != 2 || fake.approveCalls != test.wantApprove ||
				fake.activateCalls != test.wantActivate || result.Activated != test.wantActivated {
				t.Fatalf(
					"calls author=%d approve=%d activate=%d activated=%t",
					fake.authorCalls, fake.approveCalls, fake.activateCalls, result.Activated,
				)
			}
			if result.Driver.Revision != fake.driver.Revision ||
				result.Driver.ActiveVersionID != fake.driver.ActiveVersionID {
				t.Fatalf("result did not converge to durable state: result=%+v durable=%+v", result.Driver, fake.driver)
			}
		})
	}
}

func TestAuthorNativeFlueDriverReReadsDurableRevisionAfterOlderAuthorReceipt(t *testing.T) {
	fake := &nativeAuthoringStateFake{advanceAfterAuthorOnce: true}
	options := nativeAuthoringOptions(t)
	options.Trust = workflowcatalog.DriverTrustTrusted
	result, err := authorNativeForTest(
		context.Background(),
		fake,
		fake,
		nativeTestAuthorities(t, "TEST", "operator-1"),
		options,
	)
	if err != nil {
		t.Fatalf("AuthorNativeFlueDriver: %v", err)
	}
	if len(fake.approveCAS) != 1 || fake.approveCAS[0] != 2 {
		t.Fatalf("approve CAS = %v, want durable revision 2 instead of author receipt revision 1", fake.approveCAS)
	}
	if result.Driver.Revision != 3 || !workflowcatalog.VersionApproved(result.Driver, result.Version) {
		t.Fatalf("result = %+v version=%+v", result.Driver, result.Version)
	}
}

func TestAuthorNativeFlueDriverConcurrentWriterConflictFailsClosed(t *testing.T) {
	fake := &nativeAuthoringStateFake{conflictBeforeApprove: true}
	options := nativeAuthoringOptions(t)
	options.Trust = workflowcatalog.DriverTrustTrusted
	_, err := authorNativeForTest(
		context.Background(),
		fake,
		fake,
		nativeTestAuthorities(t, "TEST", "operator-1"),
		options,
	)
	if !errors.Is(err, workflowcatalog.ErrStaleRevision) {
		t.Fatalf("concurrent writer error = %v, want ErrStaleRevision", err)
	}
	if fake.approveCalls != 1 || fake.activateCalls != 0 {
		t.Fatalf("conflict calls approve=%d activate=%d; workflow retried or advanced", fake.approveCalls, fake.activateCalls)
	}
}

func TestAuthorNativeFlueDriverRejectsIncompleteAuthoritySetBeforeStaging(t *testing.T) {
	options := nativeAuthoringOptions(t)
	options.Trust = workflowcatalog.DriverTrustTrusted
	authorities := nativeTestAuthorities(t, "TEST", "operator-1")
	authorities.Approve = nil
	fake := &nativeAuthoringStateFake{}
	_, err := authorNativeForTest(
		context.Background(),
		fake,
		fake,
		authorities,
		options,
	)
	if !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("incomplete authorities error = %v", err)
	}
	if fake.authorCalls != 0 {
		t.Fatalf("author calls = %d, want 0", fake.authorCalls)
	}
	if _, statErr := os.Stat(filepath.Join(options.WorkDir, ".loom")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("authority denial staged filesystem content: stat err=%v", statErr)
	}
}

func nativeAuthoringOptions(t *testing.T) appworkflowauthoring.NativeOptions {
	t.Helper()
	workDir := t.TempDir()
	dist := filepath.Join(workDir, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return appworkflowauthoring.NativeOptions{
		WorkspaceKey: "TEST",
		WorkDir:      workDir,
		DistPath:     dist,
		DriverName:   "demo",
		DriverID:     "demo",
		WorkflowName: "demo",
		Trust:        workflowcatalog.DriverTrustUntrusted,
	}
}

func nativeTestAuthorities(t *testing.T, workspace, subject string) appworkflowauthoring.NativeAuthoringAuthorities {
	t.Helper()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	actions := []authority.Action{
		workflowcatalog.ActionAuthorVersion,
		workflowcatalog.ActionApproveVersion,
		workflowcatalog.ActionActivateVersion,
	}
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: subject, Class: authority.ClassOperator, Workspace: workspace,
		Actions: actions, ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	author, err := issuer.IssueOperator(principal, workspace, workflowcatalog.ActionAuthorVersion)
	if err != nil {
		t.Fatal(err)
	}
	approve, err := issuer.IssueOperator(principal, workspace, workflowcatalog.ActionApproveVersion)
	if err != nil {
		t.Fatal(err)
	}
	activate, err := issuer.IssueOperator(principal, workspace, workflowcatalog.ActionActivateVersion)
	if err != nil {
		t.Fatal(err)
	}
	return appworkflowauthoring.NativeAuthoringAuthorities{Author: author, Approve: &approve, Activate: &activate}
}

func authorNativeForTest(
	ctx context.Context,
	catalog workflowcatalog.API,
	authoring workflowcatalog.VersionAuthoringAPI,
	authorities appworkflowauthoring.NativeAuthoringAuthorities,
	options appworkflowauthoring.NativeOptions,
) (*appworkflowauthoring.Result, error) {
	coordinator, err := appworkflowauthoring.NewWithNative(NewBundleStager(), NewNativeBundleStager())
	if err != nil {
		return nil, err
	}
	return coordinator.AuthorNative(ctx, catalog, authoring, authorities, options)
}

func cloneNativeDriver(input *workflowcatalog.Driver) *workflowcatalog.Driver {
	if input == nil {
		return nil
	}
	output := *input
	output.Metadata = cloneNativeMap(input.Metadata)
	return &output
}

func cloneNativeVersion(input *workflowcatalog.DriverVersion) *workflowcatalog.DriverVersion {
	if input == nil {
		return nil
	}
	output := *input
	output.Manifest = cloneNativeMap(input.Manifest)
	return &output
}

func cloneNativeMap(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
