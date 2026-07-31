package sourcecontrol

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type connectorsBrokerFunc func(context.Context, authority.SystemAuthority, connectors.GitReadCommand) (connectors.GitReadReceipt, error)

func (function connectorsBrokerFunc) ExecuteGitRead(
	ctx context.Context,
	auth authority.SystemAuthority,
	command connectors.GitReadCommand,
) (connectors.GitReadReceipt, error) {
	return function(ctx, auth, command)
}

type authorityProviderFunc func(context.Context, string, string) (authority.SystemAuthority, error)

func (function authorityProviderFunc) AuthorityForGitRead(
	ctx context.Context,
	workspace string,
	reason string,
) (authority.SystemAuthority, error) {
	return function(ctx, workspace, reason)
}

func TestAdapterMapsExactCloneWithoutCredentialFields(t *testing.T) {
	request := GitCloneRequest{
		WorkspaceKey: "WS-1", OperationID: "materialize-1", RepositoryRef: "repo-1",
		RemoteURL: "https://github.com/acme/repo.git", RemoteName: "upstream",
		WorkspacePath: "/workspace", TargetPath: "/workspace/repo",
	}
	var authorityWorkspace, authorityReason string
	var brokerCommand connectors.GitReadCommand
	adapter, err := NewConnectorsGitReadBroker(
		connectorsBrokerFunc(func(
			_ context.Context,
			_ authority.SystemAuthority,
			command connectors.GitReadCommand,
		) (connectors.GitReadReceipt, error) {
			brokerCommand = command
			return connectors.GitReadReceipt{
				WorkspaceKey: command.WorkspaceKey, OperationID: command.OperationID,
				RepositoryRef: command.RepositoryRef, Operation: command.Operation,
				TargetPath: command.TargetPath, RemoteName: command.RemoteName,
			}, nil
		}),
		authorityProviderFunc(func(_ context.Context, workspace, reason string) (authority.SystemAuthority, error) {
			authorityWorkspace = workspace
			authorityReason = reason
			return authority.SystemAuthority{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := adapter.Clone(t.Context(), request)
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	wantCommand := connectors.GitReadCommand{
		WorkspaceKey: request.WorkspaceKey, OperationID: request.OperationID,
		RepositoryRef: request.RepositoryRef, Operation: connectors.GitReadClone,
		RemoteURL: request.RemoteURL, RemoteName: request.RemoteName,
		WorkspacePath: request.WorkspacePath, TargetPath: request.TargetPath,
	}
	if brokerCommand != wantCommand {
		t.Fatalf("broker command = %#v, want %#v", brokerCommand, wantCommand)
	}
	if authorityWorkspace != request.WorkspaceKey || authorityReason == "" {
		t.Fatalf("authority scope/reason = %q/%q", authorityWorkspace, authorityReason)
	}
	if receipt.WorkspaceKey != request.WorkspaceKey || receipt.OperationID != request.OperationID ||
		receipt.RepositoryRef != request.RepositoryRef || receipt.TargetPath != request.TargetPath {
		t.Fatalf("receipt = %#v", receipt)
	}
}

func TestAdapterMapsExactFetchWithoutCredentialFields(t *testing.T) {
	request := GitFetchRequest{
		WorkspaceKey: "WS-1", OperationID: "fetch-1", RepositoryRef: "repo-1",
		RemoteURL:     "https://github.com/acme/repo.git",
		WorkspacePath: "/workspace", TargetPath: "/workspace/repo",
		RemoteName: "origin", SourceRef: "refs/pull/7/head",
		DestinationRef: "refs/loom/pr-reviews/review-1/head",
	}
	var got connectors.GitReadCommand
	adapter, err := NewConnectorsGitReadBroker(
		connectorsBrokerFunc(func(
			_ context.Context,
			_ authority.SystemAuthority,
			command connectors.GitReadCommand,
		) (connectors.GitReadReceipt, error) {
			got = command
			return connectors.GitReadReceipt{
				WorkspaceKey: command.WorkspaceKey, OperationID: command.OperationID,
				RepositoryRef: command.RepositoryRef, Operation: command.Operation,
				TargetPath: command.TargetPath, RemoteName: command.RemoteName,
				SourceRef: command.SourceRef, DestinationRef: command.DestinationRef,
			}, nil
		}),
		authorityProviderFunc(func(context.Context, string, string) (authority.SystemAuthority, error) {
			return authority.SystemAuthority{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := adapter.FetchRef(t.Context(), request)
	if err != nil {
		t.Fatalf("FetchRef: %v", err)
	}
	want := connectors.GitReadCommand{
		WorkspaceKey: request.WorkspaceKey, OperationID: request.OperationID,
		RepositoryRef: request.RepositoryRef, Operation: connectors.GitReadFetchRef,
		RemoteURL: request.RemoteURL, WorkspacePath: request.WorkspacePath,
		TargetPath: request.TargetPath, RemoteName: request.RemoteName,
		SourceRef: request.SourceRef, DestinationRef: request.DestinationRef,
	}
	if got != want {
		t.Fatalf("broker command = %#v, want %#v", got, want)
	}
	if receipt != (GitFetchReceipt{
		WorkspaceKey: request.WorkspaceKey, OperationID: request.OperationID,
		RepositoryRef: request.RepositoryRef, TargetPath: request.TargetPath,
		RemoteName: request.RemoteName, SourceRef: request.SourceRef,
		DestinationRef: request.DestinationRef,
	}) {
		t.Fatalf("receipt = %#v", receipt)
	}
}

func TestAdapterFailsClosedOnAuthorityOrReceiptMismatch(t *testing.T) {
	request := GitCloneRequest{
		WorkspaceKey: "WS-1", OperationID: "materialize-1", RepositoryRef: "repo-1",
		RemoteURL: "/srv/repo.git", RemoteName: "origin",
		WorkspacePath: "/workspace", TargetPath: "/workspace/repo",
	}
	wantAuthorityError := errors.New("authority unavailable")
	brokerCalls := 0
	adapter, err := NewConnectorsGitReadBroker(
		connectorsBrokerFunc(func(context.Context, authority.SystemAuthority, connectors.GitReadCommand) (connectors.GitReadReceipt, error) {
			brokerCalls++
			return connectors.GitReadReceipt{}, nil
		}),
		authorityProviderFunc(func(context.Context, string, string) (authority.SystemAuthority, error) {
			return authority.SystemAuthority{}, wantAuthorityError
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Clone(t.Context(), request); !errors.Is(err, wantAuthorityError) {
		t.Fatalf("authority error = %v", err)
	}
	if brokerCalls != 0 {
		t.Fatalf("broker calls = %d, want zero", brokerCalls)
	}

	adapter, err = NewConnectorsGitReadBroker(
		connectorsBrokerFunc(func(_ context.Context, _ authority.SystemAuthority, command connectors.GitReadCommand) (connectors.GitReadReceipt, error) {
			return connectors.GitReadReceipt{
				WorkspaceKey: command.WorkspaceKey, OperationID: "other",
				RepositoryRef: command.RepositoryRef, Operation: command.Operation,
				TargetPath: command.TargetPath, RemoteName: command.RemoteName,
			}, nil
		}),
		authorityProviderFunc(func(context.Context, string, string) (authority.SystemAuthority, error) {
			return authority.SystemAuthority{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Clone(t.Context(), request); !errors.Is(err, ErrInvalidBrokerReceipt) {
		t.Fatalf("receipt mismatch error = %v", err)
	}
}

func TestNewAdapterRejectsMissingComposition(t *testing.T) {
	provider := authorityProviderFunc(func(context.Context, string, string) (authority.SystemAuthority, error) {
		return authority.SystemAuthority{}, nil
	})
	broker := connectorsBrokerFunc(func(context.Context, authority.SystemAuthority, connectors.GitReadCommand) (connectors.GitReadReceipt, error) {
		return connectors.GitReadReceipt{}, nil
	})
	if adapter, err := NewConnectorsGitReadBroker(nil, provider); adapter != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("New(nil, provider) = %#v, %v", adapter, err)
	}
	if adapter, err := NewConnectorsGitReadBroker(broker, nil); adapter != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("New(broker, nil) = %#v, %v", adapter, err)
	}
}
