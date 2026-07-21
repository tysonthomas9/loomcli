package execution

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// TerminalDriverRunWorkRecoveryQueueAPI owns the independently acknowledged
// convergence queue for terminal DriverRun TaskRun and Work Item cleanup. It
// returns the same immutable snapshot as ordinary outcome delivery, but no
// claim, retry, or completion state is shared between the two lanes.
type TerminalDriverRunWorkRecoveryQueueAPI interface {
	ClaimTerminalDriverRunWorkRecoveries(context.Context, authority.SystemAuthority, ClaimTerminalDriverRunWorkRecoveriesCommand) ([]DriverRunOutcome, error)
	CompleteTerminalDriverRunWorkRecovery(context.Context, authority.SystemAuthority, CompleteTerminalDriverRunWorkRecoveryCommand) error
	RetryTerminalDriverRunWorkRecovery(context.Context, authority.SystemAuthority, RetryTerminalDriverRunWorkRecoveryCommand) error
}

type ClaimTerminalDriverRunWorkRecoveriesCommand struct {
	WorkspaceKey string
	ClaimID      string
	Before       time.Time
	Limit        int
}

type CompleteTerminalDriverRunWorkRecoveryCommand struct {
	WorkspaceKey string
	RunID        string
	ClaimID      string
	CompletedAt  time.Time
}

type RetryTerminalDriverRunWorkRecoveryCommand struct {
	WorkspaceKey string
	RunID        string
	ClaimID      string
	Attempt      int
	FailedAt     time.Time
	Cause        string
}

type TerminalDriverRunWorkRecoveryLease struct {
	WorkspaceKey string
	ClaimID      string
	Before       time.Time
	ClaimUntil   time.Time
	Limit        int
}

type TerminalDriverRunWorkRecoveryCompletion struct {
	WorkspaceKey string
	RunID        string
	ClaimID      string
	CompletedAt  time.Time
}

type TerminalDriverRunWorkRecoveryRetry struct {
	WorkspaceKey string
	RunID        string
	ClaimID      string
	AvailableAt  time.Time
	Error        string
}

type TerminalDriverRunWorkRecoveryQueuePort interface {
	ClaimTerminalDriverRunWorkRecoveries(context.Context, TerminalDriverRunWorkRecoveryLease) ([]DriverRunOutcome, error)
	CompleteTerminalDriverRunWorkRecovery(context.Context, TerminalDriverRunWorkRecoveryCompletion) error
	RetryTerminalDriverRunWorkRecovery(context.Context, TerminalDriverRunWorkRecoveryRetry) error
}

func (service *Service) ClaimTerminalDriverRunWorkRecoveries(
	ctx context.Context,
	auth authority.SystemAuthority,
	command ClaimTerminalDriverRunWorkRecoveriesCommand,
) ([]DriverRunOutcome, error) {
	if err := service.requireSystem(ActionClaimTerminalDriverRunWorkRecoveries, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if !validQueueWorkspace(command.WorkspaceKey) || !validQueueClaim(command.ClaimID, command.Before, command.Limit) {
		return nil, ErrInvalid
	}
	if service.dependencies.TerminalWorkRecoveries == nil {
		return nil, ErrUnavailable
	}
	values, err := service.dependencies.TerminalWorkRecoveries.ClaimTerminalDriverRunWorkRecoveries(
		ctx,
		TerminalDriverRunWorkRecoveryLease{
			WorkspaceKey: command.WorkspaceKey,
			ClaimID:      command.ClaimID,
			Before:       command.Before.UTC(),
			ClaimUntil:   command.Before.UTC().Add(ReconciliationClaimLease),
			Limit:        command.Limit,
		},
	)
	if err != nil {
		return nil, err
	}
	if len(values) > command.Limit {
		return nil, ErrConflict
	}
	return append([]DriverRunOutcome(nil), values...), nil
}

func (service *Service) CompleteTerminalDriverRunWorkRecovery(
	ctx context.Context,
	auth authority.SystemAuthority,
	command CompleteTerminalDriverRunWorkRecoveryCommand,
) error {
	if err := service.requireSystem(ActionCompleteTerminalDriverRunWorkRecovery, command.WorkspaceKey, auth); err != nil {
		return err
	}
	if !validQueueWorkspace(command.WorkspaceKey) || !validQueueIdentity(command.RunID, command.ClaimID) || command.CompletedAt.IsZero() {
		return ErrInvalid
	}
	if service.dependencies.TerminalWorkRecoveries == nil {
		return ErrUnavailable
	}
	return service.dependencies.TerminalWorkRecoveries.CompleteTerminalDriverRunWorkRecovery(
		ctx,
		TerminalDriverRunWorkRecoveryCompletion{
			WorkspaceKey: command.WorkspaceKey,
			RunID:        command.RunID,
			ClaimID:      command.ClaimID,
			CompletedAt:  command.CompletedAt.UTC(),
		},
	)
}

func (service *Service) RetryTerminalDriverRunWorkRecovery(
	ctx context.Context,
	auth authority.SystemAuthority,
	command RetryTerminalDriverRunWorkRecoveryCommand,
) error {
	if err := service.requireSystem(ActionRetryTerminalDriverRunWorkRecovery, command.WorkspaceKey, auth); err != nil {
		return err
	}
	if !validQueueWorkspace(command.WorkspaceKey) || !validQueueRetry(command.RunID, command.ClaimID, command.Attempt, command.FailedAt, command.Cause) {
		return ErrInvalid
	}
	if service.dependencies.TerminalWorkRecoveries == nil {
		return ErrUnavailable
	}
	return service.dependencies.TerminalWorkRecoveries.RetryTerminalDriverRunWorkRecovery(
		ctx,
		TerminalDriverRunWorkRecoveryRetry{
			WorkspaceKey: command.WorkspaceKey,
			RunID:        command.RunID,
			ClaimID:      command.ClaimID,
			AvailableAt:  command.FailedAt.UTC().Add(reconciliationRetryDelay(command.Attempt)),
			Error:        boundedReconciliationError(command.Cause),
		},
	)
}

var _ TerminalDriverRunWorkRecoveryQueueAPI = (*Service)(nil)
