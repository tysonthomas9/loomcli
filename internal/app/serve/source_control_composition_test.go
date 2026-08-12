package serve

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/gitauth"
	infralocalgit "github.com/tysonthomas9/loomcli/internal/infra/localgit"
	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type sourceControlRepositoryResolverFunc func(context.Context, string, string) (sourcecontrol.RepositoryCheckout, error)

type sourceControlGrantStoreStub struct{}

func (sourceControlGrantStoreStub) CreateGrant(context.Context, connectors.CreateGrantMutation) (*connectors.ConnectorGrant, error) {
	return nil, connectors.ErrUnavailable
}

func (sourceControlGrantStoreStub) ListGrantsByBinding(context.Context, string, string) ([]*connectors.ConnectorGrant, error) {
	return []*connectors.ConnectorGrant{}, nil
}

func newSourceControlCapability(
	issuer *authority.Issuer,
	credentialSource gitauth.Source,
	repositories sourcecontrol.RepositoryResolver,
	inspector sourcecontrol.CheckoutInspector,
	now func() time.Time,
) (*SourceControlCapability, error) {
	return newSourceControlCapabilityWithGrants(
		issuer,
		credentialSource,
		repositories,
		inspector,
		sourceControlGrantStoreStub{},
		now,
	)
}

func (function sourceControlRepositoryResolverFunc) ResolveRepositoryCheckout(
	ctx context.Context,
	workspace string,
	_ string,
	repositoryRef string,
) (sourcecontrol.RepositoryCheckout, error) {
	return function(ctx, workspace, repositoryRef)
}

func (sourceControlRepositoryResolverFunc) RecordRepositoryCheckout(
	context.Context,
	sourcecontrol.RepositoryCheckout,
	string,
) error {
	return nil
}

type sourceControlAPIRecorder struct {
	calls int
}

func (recorder *sourceControlAPIRecorder) MaterializeWorkspace(
	context.Context,
	authority.SystemAuthority,
	sourcecontrol.MaterializeCommand,
) (*sourcecontrol.Materialization, error) {
	recorder.calls++
	return nil, errors.New("unexpected materialization")
}

func (recorder *sourceControlAPIRecorder) FetchRepositoryRef(
	context.Context,
	authority.SystemAuthority,
	sourcecontrol.FetchRefCommand,
) (*sourcecontrol.FetchedRef, error) {
	recorder.calls++
	return nil, errors.New("unexpected fetch")
}

func TestSourceControlApplicationMaterializerClonesMissingCheckoutFetchesRefsAndReuses(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	sourceControlRunGit(t, "", "init", "--bare", remote)
	sourceControlRunGit(t, "", "init", "-b", "main", seed)
	sourceControlRunGit(t, seed, "config", "user.name", "Test User")
	sourceControlRunGit(t, seed, "config", "user.email", "test@example.test")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("phase 5 materializer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceControlRunGit(t, seed, "add", "README.md")
	sourceControlRunGit(t, seed, "commit", "-m", "seed")
	sourceControlRunGit(t, seed, "remote", "add", "origin", remote)
	sourceControlRunGit(t, seed, "push", "origin", "main")
	sourceControlRunGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")

	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	capability, err := newSourceControlCapability(
		issuer,
		nil,
		sourceControlRepositoryResolverFunc(func(_ context.Context, workspaceKey, repositoryRef string) (sourcecontrol.RepositoryCheckout, error) {
			checkoutName := "repo"
			remoteName := ""
			if repositoryRef == "repo-pr" {
				checkoutName = "repo-pr"
				remoteName = "upstream"
			}
			return sourcecontrol.RepositoryCheckout{
				WorkspaceKey: workspaceKey, RepositoryRef: repositoryRef,
				RemoteURL: remote, RemoteName: remoteName,
				WorkspacePath: workspace, CheckoutName: checkoutName,
			}, nil
		}),
		infralocalgit.Inspector{},
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("newSourceControlCapability: %v", err)
	}
	checkoutPath := filepath.Join(workspace, "repo")
	if _, err := os.Stat(checkoutPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checkout exists before task materialization: %v", err)
	}
	repositoryCheckout, err := capability.prepareRepositoryCheckout(
		t.Context(),
		sourcecontrol.RepositoryAdmissionCheckoutCommand{
			WorkspaceKey:      "WS-1",
			AdmissionID:       "0123456789abcdef0123456789abcdef",
			RepositoryRef:     "repo-1",
			OwnerID:           "loom-workspace-admission-owner",
			OwnerGenerationID: "abcdef0123456789abcdef0123456789",
			SpecFingerprint:   "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
	)
	if err != nil {
		t.Fatalf("prepare repository admission checkout: %v", err)
	}
	if repositoryCheckout.CheckoutPath != checkoutPath ||
		repositoryCheckout.Reused ||
		repositoryCheckout.RepositoryRef != "repo-1" {
		t.Fatalf("repository admission checkout = %#v", repositoryCheckout)
	}
	taskCheckout, err := capability.SourceControlMaterializer().PrepareTaskCheckout(
		t.Context(),
		sourcecontrol.TaskCheckoutCommand{
			WorkspaceKey: "WS-1", TaskRunID: "task-run-1",
			RepositoryRef: "repo-1", BaseBranch: "main",
		},
	)
	if err != nil {
		t.Fatalf("prepare task checkout from missing base: %v", err)
	}
	mainCommit := strings.TrimSpace(sourceControlGitOutput(t, seed, "rev-parse", "main"))
	if taskCheckout.CheckoutPath != checkoutPath ||
		!strings.HasPrefix(taskCheckout.BaseRef, "refs/loom/task-runs/") ||
		taskCheckout.BaseCommit != mainCommit {
		t.Fatalf("task checkout = %#v, main %q", taskCheckout, mainCommit)
	}
	if got := strings.TrimSpace(sourceControlGitOutput(t, checkoutPath, "show", "HEAD:README.md")); got != "phase 5 materializer" {
		t.Fatalf("task-materialized README = %q", got)
	}

	auth, err := capability.issueMaterializeAuthority("WS-1", "test local materialization reuse")
	if err != nil {
		t.Fatalf("issue materialization authority: %v", err)
	}
	command := sourcecontrol.MaterializeCommand{
		WorkspaceKey: "WS-1", MaterializationID: "materialize-1", RepositoryRef: "repo-1",
	}
	first, err := capability.SourceControlAPI().MaterializeWorkspace(t.Context(), auth, command)
	if err != nil {
		t.Fatalf("first materialization: %v", err)
	}
	if !first.Reused || first.CheckoutPath != checkoutPath {
		t.Fatalf("first result = %#v", first)
	}

	auth, err = capability.issueMaterializeAuthority("WS-1", "test idempotent retry")
	if err != nil {
		t.Fatal(err)
	}
	second, err := capability.SourceControlAPI().MaterializeWorkspace(t.Context(), auth, command)
	if err != nil {
		t.Fatalf("second materialization: %v", err)
	}
	if !second.Reused || second.CheckoutPath != first.CheckoutPath {
		t.Fatalf("second result = %#v, first %#v", second, first)
	}

	if err := os.WriteFile(filepath.Join(seed, "PR.md"), []byte("review me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceControlRunGit(t, seed, "add", "PR.md")
	sourceControlRunGit(t, seed, "commit", "-m", "PR head")
	headCommit := strings.TrimSpace(sourceControlGitOutput(t, seed, "rev-parse", "HEAD"))
	sourceControlRunGit(t, seed, "push", "origin", "HEAD:refs/pull/7/head")
	prCheckoutPath := filepath.Join(workspace, "repo-pr")
	if _, err := os.Stat(prCheckoutPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("PR checkout exists before PR materialization: %v", err)
	}
	prCheckout, err := capability.SourceControlMaterializer().PreparePullRequestCheckout(
		t.Context(),
		sourcecontrol.PullRequestCheckoutCommand{
			WorkspaceKey: "WS-1", ReviewID: "review-acme-repo-pr-7",
			RepositoryRef: "repo-pr", Number: 7,
			HeadCommit: headCommit, BaseBranch: "main",
		},
	)
	if err != nil {
		t.Fatalf("prepare PR checkout: %v", err)
	}
	if prCheckout.CheckoutPath != prCheckoutPath ||
		prCheckout.HeadCommit != headCommit ||
		prCheckout.BaseCommit != mainCommit ||
		!strings.HasPrefix(prCheckout.HeadRef, "refs/loom/pr-reviews/") ||
		!strings.HasPrefix(prCheckout.BaseRef, "refs/loom/pr-reviews/") {
		t.Fatalf("PR checkout = %#v", prCheckout)
	}
}

func TestSourceControlCompositionAuthoritiesAreExactAndFailClosed(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	capability, err := newSourceControlCapability(
		issuer,
		nil,
		sourceControlRepositoryResolverFunc(func(context.Context, string, string) (sourcecontrol.RepositoryCheckout, error) {
			return sourcecontrol.RepositoryCheckout{}, nil
		}),
		infralocalgit.Inspector{},
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := capability.issueMaterializeAuthority("WS-1", "materialize exact repository")
	if err != nil {
		t.Fatal(err)
	}
	if auth.Action() != sourcecontrol.ActionMaterializeWorkspace ||
		auth.Workspace() != "WS-1" ||
		auth.Subject() != sourceControlMaterializerComponentID {
		t.Fatalf("materialization authority = action:%q workspace:%q subject:%q", auth.Action(), auth.Workspace(), auth.Subject())
	}
	provider := &sourceControlBrokerAuthorityProvider{issuer: issuer, now: func() time.Time { return now }}
	brokerAuth, err := provider.AuthorityForGitRead(t.Context(), "WS-1", "bounded clone")
	if err != nil {
		t.Fatal(err)
	}
	if brokerAuth.Action() != connectors.ActionExecuteGitRead ||
		brokerAuth.Workspace() != "WS-1" ||
		brokerAuth.Subject() != sourceControlMaterializerComponentID {
		t.Fatalf("broker authority = action:%q workspace:%q subject:%q", brokerAuth.Action(), brokerAuth.Workspace(), brokerAuth.Subject())
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := provider.AuthorityForGitRead(cancelled, "WS-1", "bounded clone"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled authority error = %v", err)
	}
	if _, err := provider.AuthorityForGitRead(t.Context(), "", "bounded clone"); !errors.Is(err, authority.ErrInvalidScope) {
		t.Fatalf("empty workspace error = %v", err)
	}
}

func TestSourceControlApplicationMaterializerRejectsInvalidIntentBeforeOwnerSideEffects(t *testing.T) {
	recorder := &sourceControlAPIRecorder{}
	capability := &SourceControlCapability{api: recorder}
	if _, err := capability.prepareRepositoryCheckout(
		t.Context(),
		sourcecontrol.RepositoryAdmissionCheckoutCommand{
			WorkspaceKey: "WS-1", AdmissionID: " ", RepositoryRef: "repo-1",
		},
	); !errors.Is(err, sourcecontrol.ErrInvalid) {
		t.Fatalf("repository materializer error = %v, want %v", err, sourcecontrol.ErrInvalid)
	}
	if _, err := capability.PrepareTaskCheckout(
		t.Context(),
		sourcecontrol.TaskCheckoutCommand{
			WorkspaceKey: "WS-1", TaskRunID: "run-1",
			RepositoryRef: "repo-1", BaseBranch: "../secret",
		},
	); !errors.Is(err, sourcecontrol.ErrInvalid) {
		t.Fatalf("task materializer error = %v, want %v", err, sourcecontrol.ErrInvalid)
	}
	if _, err := capability.PreparePullRequestCheckout(
		t.Context(),
		sourcecontrol.PullRequestCheckoutCommand{
			WorkspaceKey: "WS-1", ReviewID: "review-1", RepositoryRef: "repo-1",
			Number: 7, HeadCommit: "short", BaseBranch: "main",
		},
	); !errors.Is(err, sourcecontrol.ErrInvalid) {
		t.Fatalf("PR materializer error = %v, want %v", err, sourcecontrol.ErrInvalid)
	}
	if recorder.calls != 0 {
		t.Fatalf("owner API calls = %d, want zero before valid application intent", recorder.calls)
	}
}

func TestSourceControlCompositionRejectsMissingDependencies(t *testing.T) {
	issuer := authority.NewIssuer()
	if capability, err := newSourceControlCapability(
		nil,
		nil,
		sourceControlRepositoryResolverFunc(func(context.Context, string, string) (sourcecontrol.RepositoryCheckout, error) {
			return sourcecontrol.RepositoryCheckout{}, nil
		}),
		infralocalgit.Inspector{},
		time.Now,
	); capability != nil || err == nil {
		t.Fatalf("nil issuer composition = %#v, %v", capability, err)
	}
	if capability, err := newSourceControlCapability(
		issuer,
		nil,
		nil,
		infralocalgit.Inspector{},
		time.Now,
	); capability != nil || err == nil {
		t.Fatalf("nil repository resolver composition = %#v, %v", capability, err)
	}
	if capability, err := newSourceControlCapability(
		issuer,
		nil,
		sourceControlRepositoryResolverFunc(func(context.Context, string, string) (sourcecontrol.RepositoryCheckout, error) {
			return sourcecontrol.RepositoryCheckout{}, nil
		}),
		nil,
		time.Now,
	); capability != nil || err == nil {
		t.Fatalf("nil inspector composition = %#v, %v", capability, err)
	}
	if capability, err := newSourceControlCapability(
		issuer,
		nil,
		sourceControlRepositoryResolverFunc(func(context.Context, string, string) (sourcecontrol.RepositoryCheckout, error) {
			return sourcecontrol.RepositoryCheckout{}, nil
		}),
		infralocalgit.Inspector{},
		nil,
	); capability != nil || err == nil {
		t.Fatalf("nil clock composition = %#v, %v", capability, err)
	}
	if capability, err := NewSourceControlCapabilityWithFleetDB(
		"",
		sourceControlRepositoryResolverFunc(func(context.Context, string, string) (sourcecontrol.RepositoryCheckout, error) {
			return sourcecontrol.RepositoryCheckout{}, nil
		}),
		nil,
		nil,
	); capability != nil || err == nil {
		t.Fatalf("nil catalog composition = %#v, %v", capability, err)
	}
	var capability *SourceControlCapability
	if api := capability.SourceControlAPI(); api != nil {
		t.Fatalf("nil capability API = %#v", api)
	}
	if _, err := capability.issueMaterializeAuthority("WS-1", "reason"); !errors.Is(err, sourcecontrol.ErrUnavailable) {
		t.Fatalf("nil capability authority error = %v", err)
	}
}

func sourceControlRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...) //nolint:norawexec,gosec // test helper.
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func sourceControlGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...) //nolint:norawexec,gosec // test helper.
	command.Dir = dir
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}
