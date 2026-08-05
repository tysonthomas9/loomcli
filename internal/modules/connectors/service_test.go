package connectors

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type gitReadExecutorFunc func(context.Context, GitReadCommand) error

func (function gitReadExecutorFunc) ValidateGitRead(
	_ context.Context,
	command GitReadCommand,
) (string, error) {
	return command.TargetPath, nil
}

func (function gitReadExecutorFunc) ExecuteGitRead(ctx context.Context, command GitReadCommand) error {
	return function(ctx, command)
}

type gitReadExecutorStub struct {
	validate func(context.Context, GitReadCommand) (string, error)
	execute  func(context.Context, GitReadCommand) error
}

func (stub gitReadExecutorStub) ValidateGitRead(
	ctx context.Context,
	command GitReadCommand,
) (string, error) {
	return stub.validate(ctx, command)
}

func (stub gitReadExecutorStub) ExecuteGitRead(ctx context.Context, command GitReadCommand) error {
	return stub.execute(ctx, command)
}

func TestGitReadBrokerExecutesOneExactCredentialFreeClone(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	var executed GitReadCommand
	service, err := New(gitReadExecutorFunc(func(_ context.Context, command GitReadCommand) error {
		executed = command
		return nil
	}), admission)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	command := GitReadCommand{
		WorkspaceKey:  "WS-1",
		OperationID:   "materialize-1",
		RepositoryRef: "repo-1",
		Operation:     GitReadClone,
		RemoteURL:     "https://github.com/acme/repo.git",
		RemoteName:    "origin",
		WorkspacePath: workspacePath,
		TargetPath:    filepath.Join(workspacePath, "repo"),
	}
	receipt, err := service.ExecuteGitRead(
		t.Context(),
		issueGitReadAuthority(t, issuer, "WS-1", time.Now().Add(time.Hour)),
		command,
	)
	if err != nil {
		t.Fatalf("ExecuteGitRead: %v", err)
	}
	if executed != command {
		t.Fatalf("executed command = %#v, want %#v", executed, command)
	}
	if receipt.WorkspaceKey != command.WorkspaceKey ||
		receipt.OperationID != command.OperationID ||
		receipt.RepositoryRef != command.RepositoryRef ||
		receipt.Operation != GitReadClone ||
		receipt.RemoteName != command.RemoteName ||
		receipt.TargetPath != command.TargetPath {
		t.Fatalf("receipt = %#v, want exact request coordinates", receipt)
	}
}

func TestGitReadBrokerExecutesOneExactCredentialFreeFetchRef(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	var executed []GitReadCommand
	service, err := New(gitReadExecutorFunc(func(_ context.Context, command GitReadCommand) error {
		executed = append(executed, command)
		return nil
	}), admission)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	command := GitReadCommand{
		WorkspaceKey: "WS-1", OperationID: "fetch-1", RepositoryRef: "repo-1",
		Operation: GitReadFetchRef, RemoteURL: "https://github.com/acme/repo.git",
		WorkspacePath: workspacePath, TargetPath: filepath.Join(workspacePath, "repo"),
		RemoteName: "origin", SourceRef: "refs/pull/7/head",
		DestinationRef: "refs/loom/pr-reviews/review-1/head",
	}
	auth := issueGitReadAuthority(t, issuer, "WS-1", time.Now().Add(time.Hour))
	for range 2 {
		receipt, err := service.ExecuteGitRead(t.Context(), auth, command)
		if err != nil {
			t.Fatalf("ExecuteGitRead: %v", err)
		}
		if receipt.WorkspaceKey != command.WorkspaceKey ||
			receipt.OperationID != command.OperationID ||
			receipt.RepositoryRef != command.RepositoryRef ||
			receipt.Operation != GitReadFetchRef ||
			receipt.TargetPath != command.TargetPath ||
			receipt.RemoteName != command.RemoteName ||
			receipt.SourceRef != command.SourceRef ||
			receipt.DestinationRef != command.DestinationRef {
			t.Fatalf("receipt = %#v", receipt)
		}
	}
	if len(executed) != 2 || executed[0] != command || executed[1] != command {
		t.Fatalf("fetch executions = %#v, want exact replay execution", executed)
	}
	divergent := command
	divergent.DestinationRef = "refs/loom/pr-reviews/review-2/head"
	if _, err := service.ExecuteGitRead(
		t.Context(),
		auth,
		divergent,
	); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("divergent fetch replay error = %v, want %v", err, ErrIdempotencyConflict)
	}
	if len(executed) != 2 {
		t.Fatalf("executions after divergent fetch = %d, want 2", len(executed))
	}
}

func TestGitReadBrokerFailsClosedBeforeExecutor(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	executions := 0
	service, err := New(gitReadExecutorFunc(func(context.Context, GitReadCommand) error {
		executions++
		return nil
	}), admission)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	valid := GitReadCommand{
		WorkspaceKey: "WS-1", OperationID: "materialize-1", RepositoryRef: "repo-1",
		Operation: GitReadClone, RemoteURL: "https://github.com/acme/repo.git",
		WorkspacePath: workspacePath, TargetPath: filepath.Join(workspacePath, "repo"),
	}

	tests := []struct {
		name    string
		auth    authority.SystemAuthority
		command GitReadCommand
		want    error
	}{
		{
			name: "zero authority", auth: authority.SystemAuthority{},
			command: valid, want: authority.ErrAdmissionDenied,
		},
		{
			name:    "wrong workspace",
			auth:    issueGitReadAuthority(t, issuer, "WS-2", time.Now().Add(time.Hour)),
			command: valid, want: authority.ErrAdmissionDenied,
		},
		{
			name:    "foreign issuer",
			auth:    issueGitReadAuthority(t, authority.NewIssuer(), "WS-1", time.Now().Add(time.Hour)),
			command: valid, want: authority.ErrAdmissionDenied,
		},
		{
			name: "unsupported operation",
			auth: issueGitReadAuthority(t, issuer, "WS-1", time.Now().Add(time.Hour)),
			command: func() GitReadCommand {
				value := valid
				value.Operation = "push"
				return value
			}(),
			want: ErrUnsupportedOperation,
		},
		{
			name: "fetch destination outside owner namespace",
			auth: issueGitReadAuthority(t, issuer, "WS-1", time.Now().Add(time.Hour)),
			command: func() GitReadCommand {
				value := valid
				value.Operation = GitReadFetchRef
				value.RemoteName = "origin"
				value.SourceRef = "refs/heads/main"
				value.DestinationRef = "refs/heads/main"
				return value
			}(),
			want: ErrInvalid,
		},
		{
			name: "fetch pull merge ref outside exact read namespace",
			auth: issueGitReadAuthority(t, issuer, "WS-1", time.Now().Add(time.Hour)),
			command: func() GitReadCommand {
				value := valid
				value.Operation = GitReadFetchRef
				value.RemoteName = "origin"
				value.SourceRef = "refs/pull/7/merge"
				value.DestinationRef = "refs/loom/pr-reviews/review-7/head"
				return value
			}(),
			want: ErrInvalid,
		},
		{
			name: "fetch noncanonical pull number outside exact read namespace",
			auth: issueGitReadAuthority(t, issuer, "WS-1", time.Now().Add(time.Hour)),
			command: func() GitReadCommand {
				value := valid
				value.Operation = GitReadFetchRef
				value.RemoteName = "origin"
				value.SourceRef = "refs/pull/07/head"
				value.DestinationRef = "refs/loom/pr-reviews/review-7/head"
				return value
			}(),
			want: ErrInvalid,
		},
		{
			name: "target outside workspace",
			auth: issueGitReadAuthority(t, issuer, "WS-1", time.Now().Add(time.Hour)),
			command: func() GitReadCommand {
				value := valid
				value.TargetPath = filepath.Join(filepath.Dir(workspacePath), "escape")
				return value
			}(),
			want: ErrInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.ExecuteGitRead(t.Context(), test.auth, test.command)
			if !errors.Is(err, test.want) {
				t.Fatalf("ExecuteGitRead error = %v, want %v", err, test.want)
			}
		})
	}
	if executions != 0 {
		t.Fatalf("executor calls = %d, want zero for rejected requests", executions)
	}
}

func TestGitReadBrokerRejectsAndDoesNotReflectURLCredentials(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	executions := 0
	service, err := New(gitReadExecutorFunc(func(context.Context, GitReadCommand) error {
		executions++
		return nil
	}), admission)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	auth := issueGitReadAuthority(t, issuer, "WS-1", time.Now().Add(time.Hour))
	for _, remote := range []string{
		"https://user:url-userinfo-secret@github.com/acme/repo.git",
		"https://github.com/acme/repo.git?access_token=query-secret",
		"https://github.com/acme/repo.git#fragment-secret",
		"https://github.com/acme/repo.git?access_token=%zz-malformed-secret",
		"ssh://user:ssh-password-secret@example.test/acme/repo.git",
		"ssh://user@example.test/acme/repo.git",
		"git://user@example.test/acme/repo.git",
		"custom+git://user:custom-password-secret@example.test/acme/repo.git",
		"ssh://example.test/acme/repo.git?access_token=query-secret",
		"file:///srv/repo.git#fragment-secret",
		"user:ssh-password-secret@example.test:acme/repo.git",
		"git@example.test:acme/repo.git?access_token=query-secret",
	} {
		command := GitReadCommand{
			WorkspaceKey: "WS-1", OperationID: "materialize-1", RepositoryRef: "repo-1",
			Operation: GitReadClone, RemoteURL: remote,
			WorkspacePath: workspacePath, TargetPath: filepath.Join(workspacePath, "repo"),
		}
		_, err := service.ExecuteGitRead(t.Context(), auth, command)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("remote %q error = %v, want ErrInvalid", remote, err)
		}
		for _, secret := range []string{
			"url-userinfo-secret", "ssh-password-secret", "custom-password-secret",
			"query-secret", "fragment-secret", "malformed-secret",
		} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error reflected %q: %v", secret, err)
			}
		}
	}
	if executions != 0 {
		t.Fatalf("executor calls = %d, want zero", executions)
	}
}

func TestGitReadBrokerAllowsCanonicalSCPStyleRemote(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	const remote = "git@example.test:acme/repo.git"
	var executed GitReadCommand
	service, err := New(gitReadExecutorFunc(func(_ context.Context, command GitReadCommand) error {
		executed = command
		return nil
	}), admission)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	command := GitReadCommand{
		WorkspaceKey: "WS-1", OperationID: "materialize-1", RepositoryRef: "repo-1",
		Operation: GitReadClone, RemoteURL: remote,
		WorkspacePath: workspacePath, TargetPath: filepath.Join(workspacePath, "repo"),
	}
	if _, err := service.ExecuteGitRead(
		t.Context(),
		issueGitReadAuthority(t, issuer, "WS-1", time.Now().Add(time.Hour)),
		command,
	); err != nil {
		t.Fatalf("ExecuteGitRead canonical SCP remote: %v", err)
	}
	if executed.RemoteURL != remote {
		t.Fatalf("executed remote = %q, want %q", executed.RemoteURL, remote)
	}
}

func TestGitReadBrokerRejectsDivergentIdempotencyCoordinates(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	executions := 0
	service, err := New(gitReadExecutorFunc(func(context.Context, GitReadCommand) error {
		executions++
		return nil
	}), admission)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	auth := issueGitReadAuthority(t, issuer, "WS-1", time.Now().Add(time.Hour))
	first := GitReadCommand{
		WorkspaceKey: "WS-1", OperationID: "materialize-1", RepositoryRef: "repo-1",
		Operation: GitReadClone, RemoteURL: "/srv/repo-1.git",
		WorkspacePath: workspacePath, TargetPath: filepath.Join(workspacePath, "repo"),
	}
	if _, err := service.ExecuteGitRead(t.Context(), auth, first); err != nil {
		t.Fatalf("first Git read: %v", err)
	}
	if _, err := service.ExecuteGitRead(t.Context(), auth, first); err != nil {
		t.Fatalf("same-coordinate replay: %v", err)
	}
	divergent := first
	divergent.RepositoryRef = "repo-2"
	divergent.RemoteURL = "/srv/repo-2.git"
	if _, err := service.ExecuteGitRead(
		t.Context(),
		auth,
		divergent,
	); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("divergent Git read error = %v, want ErrIdempotencyConflict", err)
	}
	if executions != 1 {
		t.Fatalf("executor calls = %d, want one execution plus one cached replay", executions)
	}
}

func TestGitReadBrokerSerializesConcurrentSameTarget(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	var executions atomic.Int32
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var firstOnce sync.Once
	var secondOnce sync.Once
	service, err := New(gitReadExecutorFunc(func(context.Context, GitReadCommand) error {
		call := executions.Add(1)
		if call == 1 {
			firstOnce.Do(func() { close(firstStarted) })
		} else {
			secondOnce.Do(func() { close(secondStarted) })
		}
		<-releaseFirst
		return nil
	}), admission)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	auth := issueGitReadAuthority(t, issuer, "WS-1", time.Now().Add(time.Hour))
	results := make(chan error, 2)
	run := func(id string) {
		_, callErr := service.ExecuteGitRead(t.Context(), auth, GitReadCommand{
			WorkspaceKey: "WS-1", OperationID: id, RepositoryRef: "repo-1",
			Operation: GitReadClone, RemoteURL: "/srv/repo.git",
			WorkspacePath: workspacePath, TargetPath: filepath.Join(workspacePath, "repo"),
		})
		results <- callErr
	}
	go run("materialize-1")
	<-firstStarted
	go run("materialize-2")
	raced := false
	select {
	case <-secondStarted:
		raced = true
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent Git read: %v", err)
		}
	}
	if raced {
		t.Fatal("second same-target Git operation executed before the first completed")
	}
	if calls := executions.Load(); calls != 2 {
		t.Fatalf("executor calls = %d, want two serialized operations", calls)
	}
}

func TestGitReadBrokerRejectsContainmentFailureBeforeExecution(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	executions := 0
	service, err := New(gitReadExecutorStub{
		validate: func(context.Context, GitReadCommand) (string, error) {
			return "", ErrInvalid
		},
		execute: func(context.Context, GitReadCommand) error {
			executions++
			return nil
		},
	}, admission)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	_, err = service.ExecuteGitRead(
		t.Context(),
		issueGitReadAuthority(t, issuer, "WS-1", time.Now().Add(time.Hour)),
		GitReadCommand{
			WorkspaceKey: "WS-1", OperationID: "materialize-1", RepositoryRef: "repo-1",
			Operation: GitReadClone, RemoteURL: "/srv/repo.git",
			WorkspacePath: workspacePath, TargetPath: filepath.Join(workspacePath, "repo"),
		},
	)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("containment error = %v, want ErrInvalid", err)
	}
	if executions != 0 {
		t.Fatalf("executor calls = %d, want zero", executions)
	}
}

func TestGitReadBrokerRejectsTargetIdentityChangeUnderLock(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	targetPath := filepath.Join(workspacePath, "repo")
	validateCalls := 0
	executions := 0
	service, err := New(gitReadExecutorStub{
		validate: func(context.Context, GitReadCommand) (string, error) {
			validateCalls++
			if validateCalls == 1 {
				return targetPath, nil
			}
			return targetPath + "-changed", nil
		},
		execute: func(context.Context, GitReadCommand) error {
			executions++
			return nil
		},
	}, admission)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ExecuteGitRead(
		t.Context(),
		issueGitReadAuthority(t, issuer, "WS-1", time.Now().Add(time.Hour)),
		GitReadCommand{
			WorkspaceKey: "WS-1", OperationID: "materialize-1", RepositoryRef: "repo-1",
			Operation: GitReadClone, RemoteURL: "/srv/repo.git",
			WorkspacePath: workspacePath, TargetPath: targetPath,
		},
	)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("target identity change error = %v, want ErrInvalid", err)
	}
	if executions != 0 {
		t.Fatalf("executor calls = %d, want zero", executions)
	}
}

func TestGitReadPublicContractsHaveNoCredentialCarryingFields(t *testing.T) {
	for _, value := range []any{GitReadCommand{}, GitReadReceipt{}} {
		typeOf := reflect.TypeOf(value)
		for index := 0; index < typeOf.NumField(); index++ {
			field := typeOf.Field(index)
			name := strings.ToLower(field.Name + " " + field.Tag.Get("json"))
			for _, forbidden := range []string{
				"credential", "password", "secret", "token", "askpass", "helper", "environment", "header",
			} {
				if strings.Contains(name, forbidden) {
					t.Fatalf("%s exposes forbidden field %s", typeOf, field.Name)
				}
			}
		}
	}
}

func TestNewGitReadBrokerRejectsMissingComposition(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	if service, err := New(nil, admission); service != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("New(nil, admission) = %#v, %v", service, err)
	}
	if service, err := New(gitReadExecutorFunc(func(context.Context, GitReadCommand) error {
		return nil
	}), nil); service != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("New(executor, nil) = %#v, %v", service, err)
	}
	var service *Service
	_, err = service.ExecuteGitRead(t.Context(), authority.SystemAuthority{}, GitReadCommand{
		WorkspaceKey: "WS-1", OperationID: "materialize-1", RepositoryRef: "repo-1",
		Operation: GitReadClone, RemoteURL: "/tmp/repo.git",
		WorkspacePath: "/tmp/workspace", TargetPath: "/tmp/workspace/repo",
	})
	if !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("nil service error = %v, want admission denied", err)
	}
}

func issueGitReadAuthority(
	t *testing.T,
	issuer *authority.Issuer,
	workspace string,
	expiresAt time.Time,
) authority.SystemAuthority {
	t.Helper()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "source-control-materializer", Class: authority.ClassSystem,
		Workspace: workspace, Actions: []authority.Action{ActionExecuteGitRead},
		ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("derive principal: %v", err)
	}
	auth, err := issuer.IssueSystem(principal, workspace, ActionExecuteGitRead, "test Git clone")
	if err != nil {
		t.Fatalf("issue authority: %v", err)
	}
	return auth
}
