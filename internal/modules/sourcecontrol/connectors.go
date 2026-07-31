package sourcecontrol

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// GitReadAuthorityProvider issues only the exact Connectors broker authority for a
// registered Source Control materializer component.
type GitReadAuthorityProvider interface {
	AuthorityForGitRead(context.Context, string, string) (authority.SystemAuthority, error)
}

type connectorsGitReadBroker struct {
	broker      connectors.GitReadBroker
	authorities GitReadAuthorityProvider
}

var _ GitReadBroker = (*connectorsGitReadBroker)(nil)

// NewConnectorsGitReadBroker adapts the public Connectors command to Source
// Control's credential-free outbound port. The returned value exposes no
// credential source or general authority factory.
func NewConnectorsGitReadBroker(
	broker connectors.GitReadBroker,
	authorities GitReadAuthorityProvider,
) (GitReadBroker, error) {
	if broker == nil || authorities == nil {
		return nil, fmt.Errorf("compose Source Control Connectors broker: %w", ErrUnavailable)
	}
	return &connectorsGitReadBroker{broker: broker, authorities: authorities}, nil
}

func (adapter *connectorsGitReadBroker) Clone(
	ctx context.Context,
	request GitCloneRequest,
) (GitCloneReceipt, error) {
	if adapter == nil || adapter.broker == nil || adapter.authorities == nil {
		return GitCloneReceipt{}, ErrUnavailable
	}
	auth, err := adapter.authorities.AuthorityForGitRead(
		ctx,
		request.WorkspaceKey,
		"materialize repository "+request.RepositoryRef+" operation "+request.OperationID,
	)
	if err != nil {
		return GitCloneReceipt{}, err
	}
	receipt, err := adapter.broker.ExecuteGitRead(ctx, auth, connectors.GitReadCommand{
		WorkspaceKey:  request.WorkspaceKey,
		OperationID:   request.OperationID,
		RepositoryRef: request.RepositoryRef,
		Operation:     connectors.GitReadClone,
		RemoteURL:     request.RemoteURL,
		RemoteName:    request.RemoteName,
		WorkspacePath: request.WorkspacePath,
		TargetPath:    request.TargetPath,
	})
	if err != nil {
		return GitCloneReceipt{}, err
	}
	if receipt.WorkspaceKey != request.WorkspaceKey ||
		receipt.OperationID != request.OperationID ||
		receipt.RepositoryRef != request.RepositoryRef ||
		receipt.Operation != connectors.GitReadClone ||
		receipt.RemoteName != request.RemoteName ||
		receipt.TargetPath != request.TargetPath {
		return GitCloneReceipt{}, ErrInvalidBrokerReceipt
	}
	return GitCloneReceipt{
		WorkspaceKey:  receipt.WorkspaceKey,
		OperationID:   receipt.OperationID,
		RepositoryRef: receipt.RepositoryRef,
		TargetPath:    receipt.TargetPath,
	}, nil
}

func (adapter *connectorsGitReadBroker) FetchRef(
	ctx context.Context,
	request GitFetchRequest,
) (GitFetchReceipt, error) {
	if adapter == nil || adapter.broker == nil || adapter.authorities == nil {
		return GitFetchReceipt{}, ErrUnavailable
	}
	auth, err := adapter.authorities.AuthorityForGitRead(
		ctx,
		request.WorkspaceKey,
		"fetch repository "+request.RepositoryRef+" ref operation "+request.OperationID,
	)
	if err != nil {
		return GitFetchReceipt{}, err
	}
	receipt, err := adapter.broker.ExecuteGitRead(ctx, auth, connectors.GitReadCommand{
		WorkspaceKey: request.WorkspaceKey, OperationID: request.OperationID,
		RepositoryRef: request.RepositoryRef, Operation: connectors.GitReadFetchRef,
		RemoteURL: request.RemoteURL, WorkspacePath: request.WorkspacePath,
		TargetPath: request.TargetPath, RemoteName: request.RemoteName,
		SourceRef: request.SourceRef, DestinationRef: request.DestinationRef,
	})
	if err != nil {
		return GitFetchReceipt{}, err
	}
	if receipt.WorkspaceKey != request.WorkspaceKey ||
		receipt.OperationID != request.OperationID ||
		receipt.RepositoryRef != request.RepositoryRef ||
		receipt.Operation != connectors.GitReadFetchRef ||
		receipt.TargetPath != request.TargetPath ||
		receipt.RemoteName != request.RemoteName ||
		receipt.SourceRef != request.SourceRef ||
		receipt.DestinationRef != request.DestinationRef {
		return GitFetchReceipt{}, ErrInvalidBrokerReceipt
	}
	return GitFetchReceipt{
		WorkspaceKey: receipt.WorkspaceKey, OperationID: receipt.OperationID,
		RepositoryRef: receipt.RepositoryRef, TargetPath: receipt.TargetPath,
		RemoteName: receipt.RemoteName, SourceRef: receipt.SourceRef,
		DestinationRef: receipt.DestinationRef,
	}, nil
}
