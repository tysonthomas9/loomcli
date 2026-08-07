package serveadapter

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type repositoryAdmissionTransportStub struct {
	infrafleetdb.RepositoryAdmissionTransport
	record *infrafleetdb.RepositoryAdmissionRecord
}

func (stub *repositoryAdmissionTransportStub) GetRepositoryAdmission(
	context.Context,
	string,
	string,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	if stub == nil || stub.record == nil {
		return nil, infrafleetdb.ErrRepositoryAdmissionNotFound
	}
	return stub.record, nil
}

type localRepositoryAdmissionResolverFunc func(
	context.Context,
	string,
) (sourcecontrol.RepositoryAdmissionLocalProjection, error)

func (function localRepositoryAdmissionResolverFunc) ResolveLocalRepositoryAdmission(
	ctx context.Context,
	admissionID string,
) (sourcecontrol.RepositoryAdmissionLocalProjection, error) {
	return function(ctx, admissionID)
}

type matchedSourceControlBroker struct{}

func (matchedSourceControlBroker) Clone(
	context.Context,
	sourcecontrol.GitCloneRequest,
) (sourcecontrol.GitCloneReceipt, error) {
	return sourcecontrol.GitCloneReceipt{}, sourcecontrol.ErrUnavailable
}

func (matchedSourceControlBroker) FetchRef(
	context.Context,
	sourcecontrol.GitFetchRequest,
) (sourcecontrol.GitFetchReceipt, error) {
	return sourcecontrol.GitFetchReceipt{}, sourcecontrol.ErrUnavailable
}

type matchedSourceControlInspector struct{}

func (matchedSourceControlInspector) CanonicalTarget(
	_ context.Context,
	_ string,
	target string,
) (string, error) {
	return target, nil
}

func (matchedSourceControlInspector) MatchRemote(
	context.Context,
	string,
	string,
	string,
) (sourcecontrol.CheckoutMatch, error) {
	return sourcecontrol.CheckoutMatched, nil
}

func (matchedSourceControlInspector) ResolveCommit(
	context.Context,
	string,
	string,
) (string, error) {
	return "", sourcecontrol.ErrUnavailable
}

func TestSourceControlRepositoryResolverResolvesNameAndStableSourceID(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	memory := memstore.New()
	if _, err := memory.Workspaces().Create(t.Context(), store.WorkspaceCreate{
		Key:  "PROOF",
		Name: "proof-workspace",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Repos().Create(t.Context(), store.RepoCreate{
		WorkspaceKey: "PROOF",
		Name:         "loomcli",
		RemoteURL:    "https://example.test/loomcli.git",
		Remote:       "upstream",
		SourceRepoID: "repo-stable-1",
		Groups:       []string{"core"},
	}); err != nil {
		t.Fatal(err)
	}

	var ensuredPath string
	var ensuredMode fs.FileMode
	workspaceRoot := t.TempDir()
	resolver := newSourceControlRepositoryResolver(
		memory.Workspaces(),
		memory.Repos(),
		func(name string) string { return filepath.Join(workspaceRoot, name) },
		func(path string, mode fs.FileMode) error {
			ensuredPath, ensuredMode = path, mode
			return nil
		},
	)

	byName, err := resolver.ResolveRepositoryCheckout(
		t.Context(),
		"PROOF",
		"test-materialization-by-name",
		"loomcli",
	)
	if err != nil {
		t.Fatalf("resolve by name: %v", err)
	}
	if byName.RepositoryRef != "loomcli" || byName.CheckoutName != "loomcli" ||
		byName.RemoteURL != "https://example.test/loomcli.git" ||
		byName.RemoteName != "upstream" ||
		byName.WorkspacePath != ensuredPath || ensuredMode != 0o700 {
		t.Fatalf("checkout by name = %+v, ensure = %q %o", byName, ensuredPath, ensuredMode)
	}
	targetPath := filepath.Join(byName.WorkspacePath, byName.CheckoutName)
	for range 2 {
		if err := resolver.RecordRepositoryCheckout(t.Context(), byName, targetPath); err != nil {
			t.Fatalf("record checkout: %v", err)
		}
	}
	stateCache, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load recorded checkout: %v", err)
	}
	local := stateCache.Workspaces["PROOF"]
	if local.Path != byName.WorkspacePath || local.Repos["loomcli"] != targetPath {
		t.Fatalf("recorded local projection = %#v, want path %q repo %q", local, byName.WorkspacePath, targetPath)
	}
	if err := resolver.RecordRepositoryCheckout(
		t.Context(),
		byName,
		filepath.Join(byName.WorkspacePath, "other"),
	); !errors.Is(err, sourcecontrol.ErrInvalidMaterialization) {
		t.Fatalf("record different target error = %v, want %v", err, sourcecontrol.ErrInvalidMaterialization)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := resolver.RecordRepositoryCheckout(
		cancelled,
		byName,
		targetPath,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled record error = %v, want %v", err, context.Canceled)
	}

	byStableID, err := resolver.ResolveRepositoryCheckout(
		t.Context(),
		"PROOF",
		"test-materialization-by-stable-id",
		"repo-stable-1",
	)
	if err != nil {
		t.Fatalf("resolve by source_repo_id: %v", err)
	}
	if byStableID.RepositoryRef != "repo-stable-1" ||
		byStableID.CheckoutName != "loomcli" ||
		byStableID.RemoteURL != byName.RemoteURL ||
		byStableID.WorkspacePath != byName.WorkspacePath {
		t.Fatalf("checkout by source_repo_id = %+v", byStableID)
	}
}

func TestSourceControlRepositoryAdmissionProjectionIsDurableAndExactAdmissionBound(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	memory := memstore.New()
	if _, err := memory.Workspaces().Create(t.Context(), store.WorkspaceCreate{
		Key: "PROOF", Name: "proof-workspace",
	}); err != nil {
		t.Fatal(err)
	}
	workspaceRoot := t.TempDir()
	const (
		admissionID = "0123456789abcdef0123456789abcdef"
		fingerprint = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		ownerID     = "loom-workspace-admission-owner"
		generation  = "abcdef0123456789abcdef0123456789"
	)
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	workspacePath := filepath.Join(workspaceRoot, "proof-workspace")
	localAvailable := true
	localActive := true
	localResolver := localRepositoryAdmissionResolverFunc(func(
		context.Context,
		string,
	) (sourcecontrol.RepositoryAdmissionLocalProjection, error) {
		if !localAvailable {
			return sourcecontrol.RepositoryAdmissionLocalProjection{},
				sourcecontrol.ErrRepositoryAdmissionNotFound
		}
		projection := sourcecontrol.RepositoryAdmissionLocalProjection{
			WorkspaceKey: "PROOF", OperationID: "admission-operation-1",
			AdmissionID: admissionID, SpecFingerprint: fingerprint,
			WorkspacePath: workspacePath,
		}
		if localActive {
			projection.OwnerID = ownerID
			projection.OwnerGenerationID = generation
		}
		return projection, nil
	})
	admissions := &repositoryAdmissionTransportStub{
		record: &infrafleetdb.RepositoryAdmissionRecord{
			AdmissionID: admissionID, WorkspaceKey: "PROOF",
			OperationID: "admission-operation-1", OwnerID: ownerID,
			OwnerGenerationID:   generation,
			OwnerLeaseExpiresAt: now.Add(time.Minute),
			SpecFingerprint:     fingerprint, State: "pending", Version: 1,
			CreatedAt: now, UpdatedAt: now,
			Spec: infrafleetdb.RepositoryAdmissionSpec{
				WorkspaceKey: "PROOF", OperationID: "admission-operation-1",
				Repositories: []infrafleetdb.RepositoryAdmissionRepoSpec{{
					Name: "pending-repo", RemoteURL: "https://example.test/pending-repo.git",
					Remote: "origin", SourceRepoID: "pending-repo",
				}},
			},
		},
	}
	resolver, ok := newSourceControlRepositoryResolverWithAdmissions(
		memory.Workspaces(),
		memory.Repos(),
		admissions,
		localResolver,
		func(name string) string { return filepath.Join(workspaceRoot, name) },
		func(string, fs.FileMode) error { return nil },
	).(*sourceControlRepositoryResolver)
	if !ok {
		t.Fatal("resolver does not expose durable admission resolution")
	}
	admissionCommand := sourcecontrol.RepositoryAdmissionCheckoutCommand{
		WorkspaceKey: "PROOF", AdmissionID: admissionID,
		RepositoryRef: "pending-repo", OwnerID: ownerID,
		OwnerGenerationID: generation, SpecFingerprint: fingerprint,
	}
	materializationID, err :=
		sourcecontrol.RepositoryAdmissionMaterializationID(admissionCommand)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := resolver.ResolveRepositoryCheckout(
		t.Context(),
		"PROOF",
		"task-run:unrelated:checkout",
		"pending-repo",
	); !errors.Is(err, workspacemodule.ErrNotFound) {
		t.Fatalf("unrelated operation resolved pending repository: %v", err)
	}
	if _, err := memory.Repos().Create(t.Context(), store.RepoCreate{
		WorkspaceKey: "PROOF",
		Name:         "pending-repo",
		RemoteURL:    "https://example.test/concurrently-committed.git",
		Remote:       "origin",
	}); err != nil {
		t.Fatalf("create concurrently committed repository: %v", err)
	}
	checkout, err := resolver.ResolveRepositoryCheckout(
		t.Context(),
		"PROOF",
		materializationID,
		"pending-repo",
	)
	if err != nil {
		t.Fatalf("resolve exact admission operation: %v", err)
	}
	if checkout.RepositoryRef != "pending-repo" ||
		checkout.RemoteURL != "https://example.test/pending-repo.git" ||
		checkout.CheckoutName != "pending-repo" ||
		checkout.WorkspacePath != workspacePath {
		t.Fatalf("exact admission projection = %#v", checkout)
	}
	localActive = false
	admissions.record.CreatedAt = now.Add(-3 * time.Minute)
	admissions.record.UpdatedAt = now.Add(-2 * time.Minute)
	admissions.record.OwnerLeaseExpiresAt = now.Add(-time.Minute)
	if _, err := resolver.ResolveRepositoryCheckout(
		context.Background(),
		"PROOF",
		materializationID,
		"pending-repo",
	); !errors.Is(err, sourcecontrol.ErrInvalidMaterialization) {
		t.Fatalf("historical lease without live local authority resolved: %v", err)
	}
	localActive = true
	for _, skew := range []time.Duration{-24 * time.Hour, 24 * time.Hour} {
		serverNow := now.Add(skew)
		admissions.record.CreatedAt = serverNow.Add(-time.Minute)
		admissions.record.UpdatedAt = serverNow
		admissions.record.OwnerLeaseExpiresAt = serverNow.Add(time.Minute)
		if _, err := resolver.ResolveRepositoryCheckout(
			t.Context(),
			"PROOF",
			materializationID,
			"pending-repo",
		); err != nil {
			t.Fatalf("resolve exact admission with host skew %s: %v", skew, err)
		}
	}
	admissions.record.CreatedAt = now
	admissions.record.UpdatedAt = now
	admissions.record.OwnerLeaseExpiresAt = now.Add(time.Minute)
	issuer := authority.NewIssuer()
	admissionPolicy, err := issuer.NewAdmission(sourcecontrol.OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	serviceAPI, err := sourcecontrol.New(
		resolver,
		matchedSourceControlBroker{},
		matchedSourceControlInspector{},
		admissionPolicy,
	)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "source-control-adapter-proof", Class: authority.ClassSystem,
		Workspace: "PROOF",
		Actions:   []authority.Action{sourcecontrol.ActionMaterializeWorkspace},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	materializeAuthority, err := issuer.IssueSystem(
		principal,
		"PROOF",
		sourcecontrol.ActionMaterializeWorkspace,
		"test adapter publication boundary",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serviceAPI.MaterializeWorkspace(
		t.Context(),
		materializeAuthority,
		sourcecontrol.MaterializeCommand{
			WorkspaceKey: "PROOF", MaterializationID: materializationID,
			RepositoryRef: "pending-repo",
		},
	); err != nil {
		t.Fatalf("materialize admission checkout: %v", err)
	}
	stateCache, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state after admission checkout: %v", err)
	}
	if local, exists := stateCache.Workspaces["PROOF"]; exists &&
		len(local.Repos) != 0 {
		t.Fatalf("admission checkout leaked partial local repo projection: %#v", local)
	}
	if _, err := serviceAPI.MaterializeWorkspace(
		t.Context(),
		materializeAuthority,
		sourcecontrol.MaterializeCommand{
			WorkspaceKey:      "PROOF",
			MaterializationID: "task-run:0123456789abcdef:checkout",
			RepositoryRef:     "pending-repo",
		},
	); err != nil {
		t.Fatalf("materialize ordinary task checkout: %v", err)
	}
	stateCache, err = bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state after task checkout: %v", err)
	}
	if local := stateCache.Workspaces["PROOF"]; local.Repos["pending-repo"] !=
		filepath.Join(workspacePath, "pending-repo") {
		t.Fatalf("ordinary checkout did not publish local projection: %#v", local)
	}

	admissions.record.OwnerGenerationID = "11111111111111111111111111111111"
	if _, err := resolver.ResolveRepositoryCheckout(
		t.Context(),
		"PROOF",
		materializationID,
		"pending-repo",
	); !errors.Is(err, sourcecontrol.ErrInvalidMaterialization) {
		t.Fatalf("stale owner generation materialization error = %v", err)
	}
	admissions.record.OwnerGenerationID = generation
	admissions.record.OwnerLeaseExpiresAt = now
	if _, err := resolver.ResolveRepositoryCheckout(
		t.Context(),
		"PROOF",
		materializationID,
		"pending-repo",
	); !errors.Is(err, sourcecontrol.ErrInvalidMaterialization) {
		t.Fatalf("expired owner lease materialization error = %v", err)
	}
	admissions.record.OwnerLeaseExpiresAt = now.Add(time.Minute)
	admissions.record.OwnerLeaseExpiresAt = now.Add(
		sourceControlRepositoryAdmissionMaximumLeaseWindow + time.Second,
	)
	if _, err := resolver.ResolveRepositoryCheckout(
		t.Context(),
		"PROOF",
		materializationID,
		"pending-repo",
	); !errors.Is(err, sourcecontrol.ErrInvalidMaterialization) {
		t.Fatalf("oversized server-relative lease error = %v", err)
	}
	admissions.record.OwnerLeaseExpiresAt = now.Add(time.Minute)

	fenceCause := errors.New("repository admission fence lost")
	ensureCalls := 0
	ctxCanceledDuringEnsure, cancelDuringEnsure :=
		context.WithCancelCause(t.Context())
	resolver.ensureDir = func(string, fs.FileMode) error {
		ensureCalls++
		cancelDuringEnsure(fenceCause)
		return nil
	}
	if _, err := resolver.ResolveRepositoryCheckout(
		ctxCanceledDuringEnsure,
		"PROOF",
		materializationID,
		"pending-repo",
	); !errors.Is(err, fenceCause) {
		t.Fatalf("cancellation during ensureDir = %v, want %v", err, fenceCause)
	}
	if ensureCalls != 1 {
		t.Fatalf("ensureDir calls during cancellation = %d, want 1", ensureCalls)
	}
	ctxCanceledBeforeEnsure, cancelBeforeEnsure :=
		context.WithCancelCause(t.Context())
	cancelBeforeEnsure(fenceCause)
	ensureCalls = 0
	if _, err := resolver.ResolveRepositoryCheckout(
		ctxCanceledBeforeEnsure,
		"PROOF",
		materializationID,
		"pending-repo",
	); !errors.Is(err, fenceCause) {
		t.Fatalf("cancellation before ensureDir = %v, want %v", err, fenceCause)
	}
	if ensureCalls != 0 {
		t.Fatalf("pre-canceled operation reached ensureDir %d times", ensureCalls)
	}
	resolver.ensureDir = func(string, fs.FileMode) error { return nil }

	localAvailable = false
	if _, err := resolver.ResolveRepositoryCheckout(
		t.Context(),
		"PROOF",
		materializationID,
		"pending-repo",
	); !errors.Is(err, sourcecontrol.ErrRepositoryAdmissionNotFound) {
		t.Fatalf("missing durable local admission fell back to committed repo: %v", err)
	}
	committed, err := resolver.ResolveRepositoryCheckout(
		t.Context(),
		"PROOF",
		"task-run:committed:checkout",
		"pending-repo",
	)
	if err != nil {
		t.Fatalf("resolve committed repository after release: %v", err)
	}
	if committed.RemoteURL != "https://example.test/concurrently-committed.git" {
		t.Fatalf("released admission shadowed committed repository: %#v", committed)
	}
}

func TestSourceControlRepositoryResolverRejectsAmbiguousAndUnsafeState(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	memory := memstore.New()
	if _, err := memory.Workspaces().Create(t.Context(), store.WorkspaceCreate{
		Key:  "PROOF",
		Name: "proof-workspace",
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"loomcli", "fleet-db"} {
		if _, err := memory.Repos().Create(t.Context(), store.RepoCreate{
			WorkspaceKey: "PROOF",
			Name:         name,
			RemoteURL:    "https://example.test/" + name + ".git",
			SourceRepoID: "shared-ref",
		}); err != nil {
			t.Fatal(err)
		}
	}

	resolver := newSourceControlRepositoryResolver(
		memory.Workspaces(),
		memory.Repos(),
		func(string) string { return t.TempDir() },
		func(string, fs.FileMode) error { return nil },
	)
	if _, err := resolver.ResolveRepositoryCheckout(
		t.Context(),
		"PROOF",
		"test-materialization-ambiguous",
		"shared-ref",
	); !errors.Is(err, sourcecontrol.ErrInvalid) {
		t.Fatalf("ambiguous repository error = %v, want %v", err, sourcecontrol.ErrInvalid)
	}

	ensureCalled := false
	unsafeResolver := newSourceControlRepositoryResolver(
		memory.Workspaces(),
		memory.Repos(),
		func(string) string { return string(filepath.Separator) },
		func(string, fs.FileMode) error {
			ensureCalled = true
			return nil
		},
	)
	if _, err := unsafeResolver.ResolveRepositoryCheckout(
		t.Context(),
		"PROOF",
		"test-materialization-unsafe-root",
		"loomcli",
	); !errors.Is(err, sourcecontrol.ErrInvalid) {
		t.Fatalf("unsafe root error = %v, want %v", err, sourcecontrol.ErrInvalid)
	}
	if ensureCalled {
		t.Fatal("unsafe workspace root reached directory creation")
	}

	if _, err := memory.Repos().Create(t.Context(), store.RepoCreate{
		WorkspaceKey: "PROOF",
		Name:         "credentialed",
		RemoteURL:    "https://token@example.test/private.git",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveRepositoryCheckout(
		t.Context(),
		"PROOF",
		"test-materialization-credentialed",
		"credentialed",
	); !errors.Is(err, sourcecontrol.ErrInvalid) {
		t.Fatalf("credentialed remote error = %v, want %v", err, sourcecontrol.ErrInvalid)
	}

	if _, err := memory.Workspaces().Create(t.Context(), store.WorkspaceCreate{
		Key:  "ESCAPE",
		Name: "../escape",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Repos().Create(t.Context(), store.RepoCreate{
		WorkspaceKey: "ESCAPE",
		Name:         "repo",
		RemoteURL:    "https://example.test/repo.git",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveRepositoryCheckout(
		t.Context(),
		"ESCAPE",
		"test-materialization-traversal",
		"repo",
	); !errors.Is(err, sourcecontrol.ErrInvalid) {
		t.Fatalf("traversal workspace error = %v, want %v", err, sourcecontrol.ErrInvalid)
	}
}

func TestAgentProvisioningWorkspaceListerReturnsFreshSortedKeys(t *testing.T) {
	memory := memstore.New()
	lister := newAgentProvisioningWorkspaceLister(memory.Workspaces())
	for _, workspace := range []store.WorkspaceCreate{
		{Key: "ZETA", Name: "zeta"},
		{Key: "ALPHA", Name: "alpha"},
	} {
		if _, err := memory.Workspaces().Create(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
	}
	got, err := lister.ListWorkspaceKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "ALPHA" || got[1] != "ZETA" {
		t.Fatalf("workspace keys = %v", got)
	}
	if _, err := memory.Workspaces().Create(t.Context(), store.WorkspaceCreate{
		Key:  "MIDDLE",
		Name: "middle",
	}); err != nil {
		t.Fatal(err)
	}
	got, err = lister.ListWorkspaceKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[1] != "MIDDLE" {
		t.Fatalf("fresh workspace keys = %v", got)
	}
}

func TestAgentProvisioningWorkspaceListerFailsClosed(t *testing.T) {
	if lister := newAgentProvisioningWorkspaceLister(nil); lister != nil {
		t.Fatalf("nil store produced lister %T", lister)
	}
	var lister *agentProvisioningWorkspaceLister
	if _, err := lister.ListWorkspaceKeys(context.Background()); !errors.Is(
		err,
		agentprovisioning.ErrUnavailable,
	) {
		t.Fatalf("nil lister error = %v, want %v", err, agentprovisioning.ErrUnavailable)
	}
}
