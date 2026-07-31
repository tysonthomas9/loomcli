package sourcecontrol

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type gitReadBrokerFunc func(context.Context, GitCloneRequest) (GitCloneReceipt, error)

func (function gitReadBrokerFunc) Clone(
	ctx context.Context,
	request GitCloneRequest,
) (GitCloneReceipt, error) {
	return function(ctx, request)
}

type gitReadBrokerStub struct {
	clone func(context.Context, GitCloneRequest) (GitCloneReceipt, error)
	fetch func(context.Context, GitFetchRequest) (GitFetchReceipt, error)
}

func (stub gitReadBrokerStub) Clone(
	ctx context.Context,
	request GitCloneRequest,
) (GitCloneReceipt, error) {
	if stub.clone == nil {
		return GitCloneReceipt{}, ErrUnavailable
	}
	return stub.clone(ctx, request)
}

func (stub gitReadBrokerStub) FetchRef(
	ctx context.Context,
	request GitFetchRequest,
) (GitFetchReceipt, error) {
	if stub.fetch == nil {
		return GitFetchReceipt{}, ErrUnavailable
	}
	return stub.fetch(ctx, request)
}

func (gitReadBrokerFunc) FetchRef(
	context.Context,
	GitFetchRequest,
) (GitFetchReceipt, error) {
	return GitFetchReceipt{}, ErrUnavailable
}

type checkoutInspectorFunc func(context.Context, string, string) (CheckoutMatch, error)

func (function checkoutInspectorFunc) CanonicalTarget(
	_ context.Context,
	_ string,
	target string,
) (string, error) {
	return target, nil
}

func (function checkoutInspectorFunc) MatchRemote(
	ctx context.Context,
	path string,
	_ string,
	remote string,
) (CheckoutMatch, error) {
	return function(ctx, path, remote)
}

func (checkoutInspectorFunc) ResolveCommit(context.Context, string, string) (string, error) {
	return "", ErrUnavailable
}

type checkoutInspectorStub struct {
	canonical func(context.Context, string, string) (string, error)
	match     func(context.Context, string, string) (CheckoutMatch, error)
	resolve   func(context.Context, string, string) (string, error)
}

func (stub checkoutInspectorStub) CanonicalTarget(
	ctx context.Context,
	workspace string,
	target string,
) (string, error) {
	return stub.canonical(ctx, workspace, target)
}

func (stub checkoutInspectorStub) MatchRemote(
	ctx context.Context,
	target string,
	_ string,
	remote string,
) (CheckoutMatch, error) {
	return stub.match(ctx, target, remote)
}

func (stub checkoutInspectorStub) ResolveCommit(
	ctx context.Context,
	target string,
	ref string,
) (string, error) {
	if stub.resolve == nil {
		return "", ErrUnavailable
	}
	return stub.resolve(ctx, target, ref)
}

type repositoryResolverFunc func(context.Context, string, string) (RepositoryCheckout, error)

func (function repositoryResolverFunc) ResolveRepositoryCheckout(
	ctx context.Context,
	workspace string,
	_ string,
	repositoryRef string,
) (RepositoryCheckout, error) {
	return function(ctx, workspace, repositoryRef)
}

func (repositoryResolverFunc) RecordRepositoryCheckout(
	context.Context,
	RepositoryCheckout,
	string,
) error {
	return nil
}

type admissionFenceRaceResolver struct {
	repository   RepositoryCheckout
	recovered    atomic.Bool
	resolveCalls atomic.Int32
	recordCalls  atomic.Int32
}

func (resolver *admissionFenceRaceResolver) ResolveRepositoryCheckout(
	context.Context,
	string,
	string,
	string,
) (RepositoryCheckout, error) {
	resolver.resolveCalls.Add(1)
	if resolver.recovered.Load() {
		return RepositoryCheckout{}, fmt.Errorf(
			"%w: repository admission owner generation changed",
			ErrInvalidMaterialization,
		)
	}
	return resolver.repository, nil
}

func (resolver *admissionFenceRaceResolver) RecordRepositoryCheckout(
	context.Context,
	RepositoryCheckout,
	string,
) error {
	resolver.recordCalls.Add(1)
	return nil
}

func TestMaterializeWorkspaceUsesCredentialFreeBrokerAndVerifiesCheckout(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	command := MaterializeCommand{
		WorkspaceKey: "WS-1", MaterializationID: "materialize-1", RepositoryRef: "repo-1",
	}
	repository := RepositoryCheckout{
		WorkspaceKey: "WS-1", RepositoryRef: "repo-1",
		RemoteURL:     "https://github.com/acme/repo.git",
		WorkspacePath: workspacePath, CheckoutName: "repo",
	}
	targetPath := filepath.Join(workspacePath, "repo")
	inspections := 0
	inspector := checkoutInspectorFunc(func(_ context.Context, path, remote string) (CheckoutMatch, error) {
		if path != targetPath || remote != repository.RemoteURL {
			t.Fatalf("inspection = %q %q, want %q %q", path, remote, targetPath, repository.RemoteURL)
		}
		inspections++
		if inspections == 1 {
			return CheckoutMissing, nil
		}
		return CheckoutMatched, nil
	})
	var brokerRequest GitCloneRequest
	broker := gitReadBrokerFunc(func(_ context.Context, request GitCloneRequest) (GitCloneReceipt, error) {
		brokerRequest = request
		return GitCloneReceipt{
			WorkspaceKey: request.WorkspaceKey, OperationID: request.OperationID,
			RepositoryRef: request.RepositoryRef, TargetPath: request.TargetPath,
		}, nil
	})
	resolver := repositoryResolverFunc(func(_ context.Context, workspace, repositoryRef string) (RepositoryCheckout, error) {
		if workspace != command.WorkspaceKey || repositoryRef != command.RepositoryRef {
			t.Fatalf("repository lookup = %q/%q", workspace, repositoryRef)
		}
		return repository, nil
	})
	service, err := New(resolver, broker, inspector, admission)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.MaterializeWorkspace(
		t.Context(),
		issueMaterializeAuthority(t, issuer, "WS-1"),
		command,
	)
	if err != nil {
		t.Fatalf("MaterializeWorkspace: %v", err)
	}
	wantRequest := GitCloneRequest{
		WorkspaceKey: "WS-1", OperationID: "materialize-1", RepositoryRef: "repo-1",
		RemoteURL: repository.RemoteURL, RemoteName: "origin",
		WorkspacePath: workspacePath, TargetPath: targetPath,
	}
	if brokerRequest != wantRequest {
		t.Fatalf("broker request = %#v, want %#v", brokerRequest, wantRequest)
	}
	if inspections != 2 {
		t.Fatalf("inspections = %d, want preflight and postcondition", inspections)
	}
	if result.WorkspaceKey != "WS-1" || result.MaterializationID != "materialize-1" ||
		result.RepositoryRef != "repo-1" || result.CheckoutPath != targetPath || result.Reused {
		t.Fatalf("materialization = %#v", result)
	}
}

func TestMaterializeWorkspaceRevalidatesAdmissionFenceBeforePublishingCheckout(
	t *testing.T,
) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		first CheckoutMatch
	}{
		{name: "generation changes during clone", first: CheckoutMissing},
		{name: "generation changes during matched reuse", first: CheckoutMatched},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			issuer := authority.NewIssuer()
			admission, err := issuer.NewAdmission(OperationRules()...)
			if err != nil {
				t.Fatal(err)
			}
			workspacePath := filepath.Join(t.TempDir(), "workspace")
			resolver := &admissionFenceRaceResolver{
				repository: RepositoryCheckout{
					WorkspaceKey: "WS-1", RepositoryRef: "repo-1",
					RemoteURL:  "https://github.com/acme/repo.git",
					RemoteName: "origin", WorkspacePath: workspacePath,
					CheckoutName: "repo",
				},
			}
			inspections := 0
			service, err := New(
				resolver,
				gitReadBrokerFunc(func(
					_ context.Context,
					request GitCloneRequest,
				) (GitCloneReceipt, error) {
					resolver.recovered.Store(true)
					return GitCloneReceipt{
						WorkspaceKey:  request.WorkspaceKey,
						OperationID:   request.OperationID,
						RepositoryRef: request.RepositoryRef,
						TargetPath:    request.TargetPath,
					}, nil
				}),
				checkoutInspectorFunc(func(
					context.Context,
					string,
					string,
				) (CheckoutMatch, error) {
					inspections++
					if test.first == CheckoutMatched {
						resolver.recovered.Store(true)
						return CheckoutMatched, nil
					}
					if inspections == 1 {
						return CheckoutMissing, nil
					}
					return CheckoutMatched, nil
				}),
				admission,
			)
			if err != nil {
				t.Fatal(err)
			}

			_, err = service.MaterializeWorkspace(
				t.Context(),
				issueMaterializeAuthority(t, issuer, "WS-1"),
				MaterializeCommand{
					WorkspaceKey: "WS-1", MaterializationID: "repository-admission:fenced",
					RepositoryRef: "repo-1",
				},
			)
			if !errors.Is(err, ErrInvalidMaterialization) {
				t.Fatalf("materialization error = %v, want stale fence rejection", err)
			}
			if got := resolver.recordCalls.Load(); got != 0 {
				t.Fatalf("stale checkout publication calls = %d, want 0", got)
			}
			if got := resolver.resolveCalls.Load(); got != 2 {
				t.Fatalf("repository resolution calls = %d, want initial + final", got)
			}
		})
	}
}

func TestMaterializeWorkspaceAdmissionDefersProjectionUntilBatchCommit(
	t *testing.T,
) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	admissionMaterializationID, err := RepositoryAdmissionMaterializationID(
		RepositoryAdmissionCheckoutCommand{
			WorkspaceKey:      "WS-1",
			AdmissionID:       "0123456789abcdef0123456789abcdef",
			RepositoryRef:     "repo-1",
			OwnerID:           "loom-workspace-admission-owner",
			OwnerGenerationID: "abcdef0123456789abcdef0123456789",
			SpecFingerprint:   "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name              string
		materializationID string
		first             CheckoutMatch
		wantRecords       int32
		wantBrokerCalls   int
	}{
		{
			name:              "admission clone is durable progress but not publication",
			materializationID: admissionMaterializationID,
			first:             CheckoutMissing, wantBrokerCalls: 1,
		},
		{
			name:              "admission reuse is not partial publication",
			materializationID: admissionMaterializationID,
			first:             CheckoutMatched,
		},
		{
			name:              "task checkout still publishes",
			materializationID: "task-run:0123456789abcdef:checkout",
			first:             CheckoutMatched, wantRecords: 1,
		},
		{
			name:              "pull request checkout still publishes",
			materializationID: "pr-review:0123456789abcdef:checkout",
			first:             CheckoutMatched, wantRecords: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspacePath := filepath.Join(t.TempDir(), "workspace")
			resolver := &admissionFenceRaceResolver{
				repository: RepositoryCheckout{
					WorkspaceKey: "WS-1", RepositoryRef: "repo-1",
					RemoteURL:  "https://github.com/acme/repo.git",
					RemoteName: "origin", WorkspacePath: workspacePath,
					CheckoutName: "repo",
				},
			}
			inspections := 0
			brokerCalls := 0
			service, serviceErr := New(
				resolver,
				gitReadBrokerFunc(func(
					_ context.Context,
					request GitCloneRequest,
				) (GitCloneReceipt, error) {
					brokerCalls++
					return GitCloneReceipt{
						WorkspaceKey:  request.WorkspaceKey,
						OperationID:   request.OperationID,
						RepositoryRef: request.RepositoryRef,
						TargetPath:    request.TargetPath,
					}, nil
				}),
				checkoutInspectorFunc(func(
					context.Context,
					string,
					string,
				) (CheckoutMatch, error) {
					inspections++
					if test.first == CheckoutMissing && inspections == 1 {
						return CheckoutMissing, nil
					}
					return CheckoutMatched, nil
				}),
				admission,
			)
			if serviceErr != nil {
				t.Fatal(serviceErr)
			}
			if _, serviceErr = service.MaterializeWorkspace(
				t.Context(),
				issueMaterializeAuthority(t, issuer, "WS-1"),
				MaterializeCommand{
					WorkspaceKey:      "WS-1",
					MaterializationID: test.materializationID,
					RepositoryRef:     "repo-1",
				},
			); serviceErr != nil {
				t.Fatalf("MaterializeWorkspace: %v", serviceErr)
			}
			if got := resolver.recordCalls.Load(); got != test.wantRecords {
				t.Fatalf("checkout publication calls = %d, want %d", got, test.wantRecords)
			}
			if brokerCalls != test.wantBrokerCalls {
				t.Fatalf("broker calls = %d, want %d", brokerCalls, test.wantBrokerCalls)
			}
			if got := resolver.resolveCalls.Load(); got != 2 {
				t.Fatalf("repository resolution calls = %d, want initial + final", got)
			}
		})
	}
}

func TestFetchRepositoryRefUsesExactBrokerCoordinatesAndVerifiesCommit(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	targetPath := filepath.Join(workspacePath, "repo")
	const commit = "0123456789abcdef0123456789abcdef01234567"
	command := FetchRefCommand{
		WorkspaceKey: "WS-1", OperationID: "fetch-1", RepositoryRef: "repo-1",
		SourceRef: "refs/pull/7/head", DestinationRef: "refs/loom/pr-reviews/review-1/head",
		ExpectedCommit: commit,
	}
	var gotRequest GitFetchRequest
	service, err := New(
		fixedRepositoryResolver(RepositoryCheckout{
			WorkspaceKey: "WS-1", RepositoryRef: "repo-1",
			RemoteURL: "https://github.com/acme/repo.git", RemoteName: "upstream",
			WorkspacePath: workspacePath, CheckoutName: "repo",
		}),
		gitReadBrokerStub{fetch: func(
			_ context.Context,
			request GitFetchRequest,
		) (GitFetchReceipt, error) {
			gotRequest = request
			return GitFetchReceipt{
				WorkspaceKey: request.WorkspaceKey, OperationID: request.OperationID,
				RepositoryRef: request.RepositoryRef, TargetPath: request.TargetPath,
				RemoteName: request.RemoteName, SourceRef: request.SourceRef,
				DestinationRef: request.DestinationRef,
			}, nil
		}},
		checkoutInspectorStub{
			canonical: func(_ context.Context, _, target string) (string, error) {
				return target, nil
			},
			match: func(_ context.Context, path, remote string) (CheckoutMatch, error) {
				if path != targetPath || remote != "https://github.com/acme/repo.git" {
					t.Fatalf("match = %q %q", path, remote)
				}
				return CheckoutMatched, nil
			},
			resolve: func(_ context.Context, path, ref string) (string, error) {
				if path != targetPath || ref != command.DestinationRef {
					t.Fatalf("resolve = %q %q", path, ref)
				}
				return strings.ToUpper(commit), nil
			},
		},
		admission,
	)
	if err != nil {
		t.Fatal(err)
	}
	auth := issueSourceControlAuthority(t, issuer, "WS-1", ActionFetchRepositoryRef)
	result, err := service.FetchRepositoryRef(t.Context(), auth, command)
	if err != nil {
		t.Fatalf("FetchRepositoryRef: %v", err)
	}
	wantRequest := GitFetchRequest{
		WorkspaceKey: "WS-1", OperationID: "fetch-1", RepositoryRef: "repo-1",
		RemoteURL: "https://github.com/acme/repo.git", WorkspacePath: workspacePath,
		TargetPath: targetPath, RemoteName: "upstream",
		SourceRef: command.SourceRef, DestinationRef: command.DestinationRef,
	}
	if gotRequest != wantRequest {
		t.Fatalf("request = %#v, want %#v", gotRequest, wantRequest)
	}
	if result == nil || result.CommitSHA != commit ||
		result.CheckoutPath != targetPath || result.RemoteName != "upstream" {
		t.Fatalf("fetched ref = %#v", result)
	}
}

func TestFetchRepositoryRefRejectsDriftAndUnboundedRefs(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	const expected = "0123456789abcdef0123456789abcdef01234567"
	const observed = "89abcdef0123456789abcdef0123456789abcdef"
	fetchCalls := 0
	service, err := New(
		fixedRepositoryResolver(RepositoryCheckout{
			WorkspaceKey: "WS-1", RepositoryRef: "repo-1",
			RemoteURL: "/srv/repo.git", WorkspacePath: workspacePath, CheckoutName: "repo",
		}),
		gitReadBrokerStub{fetch: func(
			_ context.Context,
			request GitFetchRequest,
		) (GitFetchReceipt, error) {
			fetchCalls++
			return GitFetchReceipt{
				WorkspaceKey: request.WorkspaceKey, OperationID: request.OperationID,
				RepositoryRef: request.RepositoryRef, TargetPath: request.TargetPath,
				RemoteName: request.RemoteName, SourceRef: request.SourceRef,
				DestinationRef: request.DestinationRef,
			}, nil
		}},
		checkoutInspectorStub{
			canonical: func(_ context.Context, _, target string) (string, error) {
				return target, nil
			},
			match: func(context.Context, string, string) (CheckoutMatch, error) {
				return CheckoutMatched, nil
			},
			resolve: func(context.Context, string, string) (string, error) {
				return observed, nil
			},
		},
		admission,
	)
	if err != nil {
		t.Fatal(err)
	}
	auth := issueSourceControlAuthority(t, issuer, "WS-1", ActionFetchRepositoryRef)
	valid := FetchRefCommand{
		WorkspaceKey: "WS-1", OperationID: "fetch-1", RepositoryRef: "repo-1",
		SourceRef: "refs/pull/7/head", DestinationRef: "refs/loom/reviews/7/head",
		ExpectedCommit: expected,
	}
	var changed *RefChangedError
	if _, err := service.FetchRepositoryRef(t.Context(), auth, valid); !errors.As(err, &changed) ||
		changed.ExpectedCommit != expected || changed.FetchedCommit != observed {
		t.Fatalf("drift error = %#v / %v", changed, err)
	}
	divergent := valid
	divergent.SourceRef = "refs/heads/main"
	if _, err := service.FetchRepositoryRef(
		t.Context(),
		auth,
		divergent,
	); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("divergent fetch replay error = %v, want %v", err, ErrIdempotencyConflict)
	}
	for _, mutate := range []func(*FetchRefCommand){
		func(command *FetchRefCommand) { command.SourceRef = "refs/tags/release" },
		func(command *FetchRefCommand) { command.SourceRef = "refs/pull/7/merge" },
		func(command *FetchRefCommand) { command.SourceRef = "refs/pull/07/head" },
		func(command *FetchRefCommand) { command.SourceRef = "refs/pull/0/head" },
		func(command *FetchRefCommand) { command.DestinationRef = "refs/heads/main" },
		func(command *FetchRefCommand) { command.SourceRef = "refs/heads/../../secret" },
		func(command *FetchRefCommand) { command.ExpectedCommit = "short" },
	} {
		command := valid
		command.OperationID += "-invalid"
		mutate(&command)
		if _, err := service.FetchRepositoryRef(t.Context(), auth, command); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid fetch error = %v, command %#v", err, command)
		}
	}
	if fetchCalls != 1 {
		t.Fatalf("broker fetch calls = %d, want only valid drift fetch", fetchCalls)
	}
}

func TestApplicationCheckoutValidationRejectsInvalidCoordinatesBeforeMaterialization(t *testing.T) {
	for _, command := range []TaskCheckoutCommand{
		{WorkspaceKey: " WS-1", TaskRunID: "run-1", RepositoryRef: "repo-1", BaseBranch: "main"},
		{WorkspaceKey: "WS-1", TaskRunID: "run-1", RepositoryRef: "repo-1"},
		{WorkspaceKey: "WS-1", TaskRunID: "run-1", RepositoryRef: "repo-1", BaseBranch: "../secret"},
	} {
		if err := ValidateTaskCheckoutCommand(command); !errors.Is(err, ErrInvalid) {
			t.Fatalf("task command %#v error = %v, want %v", command, err, ErrInvalid)
		}
	}
	for _, command := range []PullRequestCheckoutCommand{
		{
			WorkspaceKey: "WS-1", ReviewID: "review-1", RepositoryRef: "repo-1",
			Number: 0, HeadCommit: strings.Repeat("a", 40), BaseBranch: "main",
		},
		{
			WorkspaceKey: "WS-1", ReviewID: "review-1", RepositoryRef: "repo-1",
			Number: 7, HeadCommit: "short", BaseBranch: "main",
		},
		{
			WorkspaceKey: "WS-1", ReviewID: "review-1", RepositoryRef: "repo-1",
			Number: 7, HeadCommit: strings.Repeat("a", 40), BaseBranch: "../secret",
		},
	} {
		if err := ValidatePullRequestCheckoutCommand(command); !errors.Is(err, ErrInvalid) {
			t.Fatalf("PR command %#v error = %v, want %v", command, err, ErrInvalid)
		}
	}
}

func TestMaterializeWorkspaceReusesOnlyExactCheckout(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	command := MaterializeCommand{
		WorkspaceKey: "WS-1", MaterializationID: "materialize-1", RepositoryRef: "repo-1",
	}
	resolver := fixedRepositoryResolver(RepositoryCheckout{
		WorkspaceKey: "WS-1", RepositoryRef: "repo-1",
		RemoteURL: "/srv/git/repo.git", WorkspacePath: workspacePath, CheckoutName: "repo",
	})
	brokerCalls := 0
	broker := gitReadBrokerFunc(func(context.Context, GitCloneRequest) (GitCloneReceipt, error) {
		brokerCalls++
		return GitCloneReceipt{}, nil
	})
	service, err := New(resolver, broker, checkoutInspectorFunc(func(context.Context, string, string) (CheckoutMatch, error) {
		return CheckoutMatched, nil
	}), admission)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.MaterializeWorkspace(t.Context(), issueMaterializeAuthority(t, issuer, "WS-1"), command)
	if err != nil {
		t.Fatalf("reuse: %v", err)
	}
	if !result.Reused || brokerCalls != 0 {
		t.Fatalf("reuse result/calls = %#v/%d", result, brokerCalls)
	}

	service, err = New(resolver, broker, checkoutInspectorFunc(func(context.Context, string, string) (CheckoutMatch, error) {
		return CheckoutConflict, nil
	}), admission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MaterializeWorkspace(
		t.Context(),
		issueMaterializeAuthority(t, issuer, "WS-1"),
		command,
	); !errors.Is(err, ErrCheckoutConflict) {
		t.Fatalf("conflicting checkout error = %v", err)
	}
	if brokerCalls != 0 {
		t.Fatalf("broker calls = %d, want zero for existing target", brokerCalls)
	}
}

func TestMaterializeWorkspaceRejectsMismatchedBrokerReceiptAndMissingPostcondition(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	command := MaterializeCommand{
		WorkspaceKey: "WS-1", MaterializationID: "materialize-1", RepositoryRef: "repo-1",
	}
	resolver := fixedRepositoryResolver(RepositoryCheckout{
		WorkspaceKey: "WS-1", RepositoryRef: "repo-1",
		RemoteURL: "/srv/git/repo.git", WorkspacePath: workspacePath, CheckoutName: "repo",
	})
	auth := issueMaterializeAuthority(t, issuer, "WS-1")
	missing := checkoutInspectorFunc(func(context.Context, string, string) (CheckoutMatch, error) {
		return CheckoutMissing, nil
	})
	service, err := New(resolver, gitReadBrokerFunc(func(_ context.Context, request GitCloneRequest) (GitCloneReceipt, error) {
		return GitCloneReceipt{
			WorkspaceKey: request.WorkspaceKey, OperationID: "different-operation",
			RepositoryRef: request.RepositoryRef, TargetPath: request.TargetPath,
		}, nil
	}), missing, admission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MaterializeWorkspace(t.Context(), auth, command); !errors.Is(err, ErrInvalidBrokerReceipt) {
		t.Fatalf("mismatched receipt error = %v", err)
	}

	service, err = New(resolver, gitReadBrokerFunc(func(_ context.Context, request GitCloneRequest) (GitCloneReceipt, error) {
		return GitCloneReceipt{
			WorkspaceKey: request.WorkspaceKey, OperationID: request.OperationID,
			RepositoryRef: request.RepositoryRef, TargetPath: request.TargetPath,
		}, nil
	}), missing, admission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MaterializeWorkspace(t.Context(), auth, command); !errors.Is(err, ErrInvalidMaterialization) {
		t.Fatalf("missing postcondition error = %v", err)
	}
}

func TestMaterializeWorkspaceAdmissionAndInputsFailBeforeRepositoryOrBroker(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	resolutions := 0
	inspections := 0
	brokerCalls := 0
	service, err := New(
		repositoryResolverFunc(func(context.Context, string, string) (RepositoryCheckout, error) {
			resolutions++
			return RepositoryCheckout{}, nil
		}),
		gitReadBrokerFunc(func(context.Context, GitCloneRequest) (GitCloneReceipt, error) {
			brokerCalls++
			return GitCloneReceipt{}, nil
		}),
		checkoutInspectorFunc(func(context.Context, string, string) (CheckoutMatch, error) {
			inspections++
			return CheckoutMissing, nil
		}),
		admission,
	)
	if err != nil {
		t.Fatal(err)
	}
	valid := MaterializeCommand{
		WorkspaceKey: "WS-1", MaterializationID: "materialize-1", RepositoryRef: "repo-1",
	}
	tests := []struct {
		name    string
		auth    authority.SystemAuthority
		command MaterializeCommand
		want    error
	}{
		{name: "zero authority", auth: authority.SystemAuthority{}, command: valid, want: authority.ErrAdmissionDenied},
		{
			name: "wrong workspace", auth: issueMaterializeAuthority(t, issuer, "WS-2"),
			command: valid, want: authority.ErrAdmissionDenied,
		},
		{
			name: "foreign issuer", auth: issueMaterializeAuthority(t, authority.NewIssuer(), "WS-1"),
			command: valid, want: authority.ErrAdmissionDenied,
		},
		{
			name: "noncanonical materialization", auth: issueMaterializeAuthority(t, issuer, "WS-1"),
			command: func() MaterializeCommand {
				value := valid
				value.MaterializationID = " materialize-1"
				return value
			}(),
			want: ErrInvalid,
		},
		{
			name: "control character repository", auth: issueMaterializeAuthority(t, issuer, "WS-1"),
			command: func() MaterializeCommand {
				value := valid
				value.RepositoryRef = "repo\nsecret"
				return value
			}(),
			want: ErrInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.MaterializeWorkspace(t.Context(), test.auth, test.command)
			if !errors.Is(err, test.want) {
				t.Fatalf("MaterializeWorkspace error = %v, want %v", err, test.want)
			}
		})
	}
	if resolutions != 0 || inspections != 0 || brokerCalls != 0 {
		t.Fatalf(
			"rejected request side effects = resolutions:%d inspections:%d broker:%d",
			resolutions,
			inspections,
			brokerCalls,
		)
	}
}

func TestMaterializeWorkspaceRejectsInvalidRepositoryProjectionBeforeGit(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	auth := issueMaterializeAuthority(t, issuer, "WS-1")
	command := MaterializeCommand{
		WorkspaceKey: "WS-1", MaterializationID: "materialize-1", RepositoryRef: "repo-1",
	}
	broker := gitReadBrokerFunc(func(context.Context, GitCloneRequest) (GitCloneReceipt, error) {
		t.Fatal("broker called")
		return GitCloneReceipt{}, nil
	})
	inspector := checkoutInspectorFunc(func(context.Context, string, string) (CheckoutMatch, error) {
		t.Fatal("inspector called")
		return "", nil
	})
	tests := []struct {
		name       string
		repository RepositoryCheckout
		want       error
	}{
		{
			name: "wrong workspace",
			repository: RepositoryCheckout{
				WorkspaceKey: "WS-2", RepositoryRef: "repo-1", RemoteURL: "/srv/repo.git",
				WorkspacePath: workspacePath, CheckoutName: "repo",
			},
			want: ErrInvalidMaterialization,
		},
		{
			name: "wrong repository",
			repository: RepositoryCheckout{
				WorkspaceKey: "WS-1", RepositoryRef: "repo-2", RemoteURL: "/srv/repo.git",
				WorkspacePath: workspacePath, CheckoutName: "repo",
			},
			want: ErrInvalidMaterialization,
		},
		{
			name: "path traversal",
			repository: RepositoryCheckout{
				WorkspaceKey: "WS-1", RepositoryRef: "repo-1", RemoteURL: "/srv/repo.git",
				WorkspacePath: workspacePath, CheckoutName: "../escape",
			},
			want: ErrInvalid,
		},
		{
			name: "relative workspace",
			repository: RepositoryCheckout{
				WorkspaceKey: "WS-1", RepositoryRef: "repo-1", RemoteURL: "/srv/repo.git",
				WorkspacePath: "relative", CheckoutName: "repo",
			},
			want: ErrInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, err := New(fixedRepositoryResolver(test.repository), broker, inspector, admission)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.MaterializeWorkspace(t.Context(), auth, command); !errors.Is(err, test.want) {
				t.Fatalf("MaterializeWorkspace error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestMaterializeWorkspaceRejectsAndDoesNotReflectURLCredentials(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	auth := issueMaterializeAuthority(t, issuer, "WS-1")
	command := MaterializeCommand{
		WorkspaceKey: "WS-1", MaterializationID: "materialize-1", RepositoryRef: "repo-1",
	}
	for _, remote := range []string{
		"https://user:url-userinfo-secret@github.com/acme/repo.git",
		"https://github.com/acme/repo.git?access_token=query-secret",
		"https://github.com/acme/repo.git#fragment-secret",
		"ssh://user:ssh-password-secret@example.test/acme/repo.git",
		"ssh://user@example.test/acme/repo.git",
		"git://user@example.test/acme/repo.git",
		"custom+git://user:custom-password-secret@example.test/acme/repo.git",
		"ssh://example.test/acme/repo.git?access_token=query-secret",
		"file:///srv/repo.git#fragment-secret",
		"user:ssh-password-secret@example.test:acme/repo.git",
		"git@example.test:acme/repo.git?access_token=query-secret",
	} {
		service, err := New(
			fixedRepositoryResolver(RepositoryCheckout{
				WorkspaceKey: "WS-1", RepositoryRef: "repo-1", RemoteURL: remote,
				WorkspacePath: workspacePath, CheckoutName: "repo",
			}),
			gitReadBrokerFunc(func(context.Context, GitCloneRequest) (GitCloneReceipt, error) {
				t.Fatal("broker called")
				return GitCloneReceipt{}, nil
			}),
			checkoutInspectorFunc(func(context.Context, string, string) (CheckoutMatch, error) {
				t.Fatal("inspector called")
				return "", nil
			}),
			admission,
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.MaterializeWorkspace(t.Context(), auth, command)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("remote %q error = %v", remote, err)
		}
		for _, secret := range []string{
			"url-userinfo-secret", "ssh-password-secret", "custom-password-secret",
			"query-secret", "fragment-secret",
		} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error reflected %q: %v", secret, err)
			}
		}
	}
}

func TestMaterializeWorkspaceAllowsCanonicalSCPStyleRemote(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	const remote = "git@example.test:acme/repo.git"
	var brokerRemote string
	service, err := New(
		fixedRepositoryResolver(RepositoryCheckout{
			WorkspaceKey: "WS-1", RepositoryRef: "repo-1", RemoteURL: remote,
			WorkspacePath: workspacePath, CheckoutName: "repo",
		}),
		gitReadBrokerFunc(func(_ context.Context, request GitCloneRequest) (GitCloneReceipt, error) {
			brokerRemote = request.RemoteURL
			return GitCloneReceipt{
				WorkspaceKey: request.WorkspaceKey, OperationID: request.OperationID,
				RepositoryRef: request.RepositoryRef, TargetPath: request.TargetPath,
			}, nil
		}),
		checkoutInspectorFunc(func(context.Context, string, string) (CheckoutMatch, error) {
			if brokerRemote == "" {
				return CheckoutMissing, nil
			}
			return CheckoutMatched, nil
		}),
		admission,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MaterializeWorkspace(
		t.Context(),
		issueMaterializeAuthority(t, issuer, "WS-1"),
		MaterializeCommand{
			WorkspaceKey: "WS-1", MaterializationID: "materialize-1", RepositoryRef: "repo-1",
		},
	); err != nil {
		t.Fatalf("MaterializeWorkspace canonical SCP remote: %v", err)
	}
	if brokerRemote != remote {
		t.Fatalf("broker remote = %q, want %q", brokerRemote, remote)
	}
}

func TestMaterializeWorkspaceRejectsDivergentIdempotencyCoordinates(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	inspections := 0
	service, err := New(
		repositoryResolverFunc(func(_ context.Context, workspace, repositoryRef string) (RepositoryCheckout, error) {
			return RepositoryCheckout{
				WorkspaceKey: workspace, RepositoryRef: repositoryRef,
				RemoteURL:     "/srv/git/" + repositoryRef + ".git",
				WorkspacePath: workspacePath, CheckoutName: repositoryRef,
			}, nil
		}),
		gitReadBrokerFunc(func(context.Context, GitCloneRequest) (GitCloneReceipt, error) {
			t.Fatal("matched checkout must not call broker")
			return GitCloneReceipt{}, nil
		}),
		checkoutInspectorFunc(func(context.Context, string, string) (CheckoutMatch, error) {
			inspections++
			return CheckoutMatched, nil
		}),
		admission,
	)
	if err != nil {
		t.Fatal(err)
	}
	auth := issueMaterializeAuthority(t, issuer, "WS-1")
	first := MaterializeCommand{
		WorkspaceKey: "WS-1", MaterializationID: "materialize-1", RepositoryRef: "repo-1",
	}
	if _, err := service.MaterializeWorkspace(t.Context(), auth, first); err != nil {
		t.Fatalf("first materialization: %v", err)
	}
	divergent := first
	divergent.RepositoryRef = "repo-2"
	if _, err := service.MaterializeWorkspace(
		t.Context(),
		auth,
		divergent,
	); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("divergent materialization error = %v, want ErrIdempotencyConflict", err)
	}
	if inspections != 1 {
		t.Fatalf("inspections = %d, want divergent replay rejected before checkout inspection", inspections)
	}
}

func TestMaterializeWorkspaceSerializesConcurrentSameTarget(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	var checkoutReady atomic.Bool
	var brokerCalls atomic.Int32
	firstBrokerStarted := make(chan struct{})
	secondBrokerStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var firstOnce sync.Once
	var secondOnce sync.Once
	service, err := New(
		fixedRepositoryResolver(RepositoryCheckout{
			WorkspaceKey: "WS-1", RepositoryRef: "repo-1",
			RemoteURL: "/srv/git/repo-1.git", WorkspacePath: workspacePath, CheckoutName: "repo",
		}),
		gitReadBrokerFunc(func(_ context.Context, request GitCloneRequest) (GitCloneReceipt, error) {
			call := brokerCalls.Add(1)
			if call == 1 {
				firstOnce.Do(func() { close(firstBrokerStarted) })
			} else {
				secondOnce.Do(func() { close(secondBrokerStarted) })
			}
			<-releaseFirst
			checkoutReady.Store(true)
			return GitCloneReceipt{
				WorkspaceKey: request.WorkspaceKey, OperationID: request.OperationID,
				RepositoryRef: request.RepositoryRef, TargetPath: request.TargetPath,
			}, nil
		}),
		checkoutInspectorFunc(func(context.Context, string, string) (CheckoutMatch, error) {
			if checkoutReady.Load() {
				return CheckoutMatched, nil
			}
			return CheckoutMissing, nil
		}),
		admission,
	)
	if err != nil {
		t.Fatal(err)
	}
	auth := issueMaterializeAuthority(t, issuer, "WS-1")
	results := make(chan error, 2)
	run := func(id string) {
		_, callErr := service.MaterializeWorkspace(t.Context(), auth, MaterializeCommand{
			WorkspaceKey: "WS-1", MaterializationID: id, RepositoryRef: "repo-1",
		})
		results <- callErr
	}
	go run("materialize-1")
	<-firstBrokerStarted
	go run("materialize-2")

	raced := false
	select {
	case <-secondBrokerStarted:
		raced = true
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent materialization: %v", err)
		}
	}
	if raced {
		t.Fatal("second clone reached broker before the first same-target clone completed")
	}
	if calls := brokerCalls.Load(); calls != 1 {
		t.Fatalf("broker calls = %d, want one clone and one exact reuse", calls)
	}
}

func TestMaterializeWorkspaceRejectsContainmentFailureBeforeBroker(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	brokerCalls := 0
	service, err := New(
		fixedRepositoryResolver(RepositoryCheckout{
			WorkspaceKey: "WS-1", RepositoryRef: "repo-1",
			RemoteURL: "/srv/git/repo.git", WorkspacePath: workspacePath, CheckoutName: "repo",
		}),
		gitReadBrokerFunc(func(context.Context, GitCloneRequest) (GitCloneReceipt, error) {
			brokerCalls++
			return GitCloneReceipt{}, nil
		}),
		checkoutInspectorStub{
			canonical: func(context.Context, string, string) (string, error) {
				return "", ErrInvalid
			},
			match: func(context.Context, string, string) (CheckoutMatch, error) {
				t.Fatal("containment failure reached checkout inspection")
				return "", nil
			},
		},
		admission,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MaterializeWorkspace(
		t.Context(),
		issueMaterializeAuthority(t, issuer, "WS-1"),
		MaterializeCommand{
			WorkspaceKey: "WS-1", MaterializationID: "materialize-1", RepositoryRef: "repo-1",
		},
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("containment error = %v, want ErrInvalid", err)
	}
	if brokerCalls != 0 {
		t.Fatalf("broker calls = %d, want zero", brokerCalls)
	}
}

func TestMaterializeWorkspaceRejectsTargetIdentityChangeUnderLock(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	targetPath := filepath.Join(workspacePath, "repo")
	canonicalCalls := 0
	service, err := New(
		fixedRepositoryResolver(RepositoryCheckout{
			WorkspaceKey: "WS-1", RepositoryRef: "repo-1",
			RemoteURL: "/srv/git/repo.git", WorkspacePath: workspacePath, CheckoutName: "repo",
		}),
		gitReadBrokerFunc(func(context.Context, GitCloneRequest) (GitCloneReceipt, error) {
			t.Fatal("changed target identity reached broker")
			return GitCloneReceipt{}, nil
		}),
		checkoutInspectorStub{
			canonical: func(context.Context, string, string) (string, error) {
				canonicalCalls++
				if canonicalCalls == 1 {
					return targetPath, nil
				}
				return targetPath + "-changed", nil
			},
			match: func(context.Context, string, string) (CheckoutMatch, error) {
				t.Fatal("changed target identity reached checkout inspection")
				return "", nil
			},
		},
		admission,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MaterializeWorkspace(
		t.Context(),
		issueMaterializeAuthority(t, issuer, "WS-1"),
		MaterializeCommand{
			WorkspaceKey: "WS-1", MaterializationID: "materialize-1", RepositoryRef: "repo-1",
		},
	); !errors.Is(err, ErrInvalidMaterialization) {
		t.Fatalf("target identity change error = %v, want ErrInvalidMaterialization", err)
	}
}

func TestSourceControlPublicContractsHaveNoCredentialCarryingFields(t *testing.T) {
	for _, value := range []any{MaterializeCommand{}, Materialization{}} {
		typeOf := reflect.TypeOf(value)
		for index := 0; index < typeOf.NumField(); index++ {
			field := typeOf.Field(index)
			name := strings.ToLower(field.Name + " " + field.Tag.Get("json"))
			for _, forbidden := range []string{
				"credential", "password", "secret", "token", "askpass", "helper", "environment", "header", "remote",
			} {
				if strings.Contains(name, forbidden) {
					t.Fatalf("%s exposes forbidden field %s", typeOf, field.Name)
				}
			}
		}
	}
	if _, ok := reflect.TypeOf(MaterializeCommand{}).FieldByName("RemoteURL"); ok {
		t.Fatal("MaterializeCommand accepts a remote URL")
	}
}

func TestNewSourceControlRejectsMissingComposition(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	repositories := fixedRepositoryResolver(RepositoryCheckout{})
	broker := gitReadBrokerFunc(func(context.Context, GitCloneRequest) (GitCloneReceipt, error) {
		return GitCloneReceipt{}, nil
	})
	inspector := checkoutInspectorFunc(func(context.Context, string, string) (CheckoutMatch, error) {
		return CheckoutMissing, nil
	})
	for _, test := range []struct {
		repositories RepositoryResolver
		broker       GitReadBroker
		inspector    CheckoutInspector
		admission    *authority.Admission
	}{
		{nil, broker, inspector, admission},
		{repositories, nil, inspector, admission},
		{repositories, broker, nil, admission},
		{repositories, broker, inspector, nil},
	} {
		service, err := New(test.repositories, test.broker, test.inspector, test.admission)
		if service != nil || !errors.Is(err, ErrUnavailable) {
			t.Fatalf("New = %#v, %v, want ErrUnavailable", service, err)
		}
	}
}

func fixedRepositoryResolver(repository RepositoryCheckout) RepositoryResolver {
	return repositoryResolverFunc(func(context.Context, string, string) (RepositoryCheckout, error) {
		return repository, nil
	})
}

func issueMaterializeAuthority(
	t *testing.T,
	issuer *authority.Issuer,
	workspace string,
) authority.SystemAuthority {
	return issueSourceControlAuthority(t, issuer, workspace, ActionMaterializeWorkspace)
}

func issueSourceControlAuthority(
	t *testing.T,
	issuer *authority.Issuer,
	workspace string,
	action authority.Action,
) authority.SystemAuthority {
	t.Helper()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "source-control-materializer", Class: authority.ClassSystem,
		Workspace: workspace, Actions: []authority.Action{action},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("derive principal: %v", err)
	}
	auth, err := issuer.IssueSystem(principal, workspace, action, "test source control operation")
	if err != nil {
		t.Fatalf("issue authority: %v", err)
	}
	return auth
}
