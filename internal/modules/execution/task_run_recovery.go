package execution

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	ActionRecoverStaleChildTaskRuns authority.Action = "execution.recover-stale-child-task-runs"
)

type RecoverStaleChildTaskRunsCommand struct {
	WorkspaceKey string
	RequestID    string
	ParentOwner  Owner
	DriverRunID  string
	StaleBefore  time.Time
	ErrorClass   string
	ErrorMessage string
	ObservedAt   time.Time
}

type RecoverStaleTaskRunsResult struct {
	WorkspaceKey        string
	StaleBefore         time.Time
	RecoveredAt         time.Time
	Recovered           int
	Released            int
	SkippedFresh        int
	RecoveredTaskRunIDs []string
}

type TaskRunRecoveryDependencies struct {
	Scopes          TaskRunRecoveryScopePort
	ChildRecoveries TaskRunStaleChildRecoveryPort
}

// TaskRunRecoveryScopePort enumerates workspace identities only. It exists so
// an unscoped runtime can derive a distinct SystemAuthority for each workspace
// before invoking the mutation API.
type TaskRunRecoveryScopePort interface {
	ListTaskRunRecoveryWorkspaces(context.Context) ([]string, error)
}

type TaskRunStaleChildRecoveryPort interface {
	RecoverStaleChildTaskRuns(context.Context, RecoverStaleChildTaskRunsCommand) (RecoverStaleTaskRunsResult, error)
}

type TaskRunRecoveryAPI interface {
	RecoverStaleChildTaskRuns(context.Context, authority.ExecutionAuthority, RecoverStaleChildTaskRunsCommand) (RecoverStaleTaskRunsResult, error)
}

func (service *Service) RecoverStaleChildTaskRuns(ctx context.Context, auth authority.ExecutionAuthority, command RecoverStaleChildTaskRunsCommand) (RecoverStaleTaskRunsResult, error) {
	if err := service.requireOwner(ActionRecoverStaleChildTaskRuns, command.WorkspaceKey, command.ParentOwner, auth); err != nil {
		return RecoverStaleTaskRunsResult{}, err
	}
	if command.ParentOwner.ResourceKind != ResourceDriverRun || strings.TrimSpace(command.DriverRunID) == "" ||
		command.DriverRunID != command.ParentOwner.ResourceID ||
		command.RequestID != RecoverStaleChildTaskRunsRequestID(command.DriverRunID, command.StaleBefore) {
		return RecoverStaleTaskRunsResult{}, ErrInvalid
	}
	if command.StaleBefore.IsZero() || command.ObservedAt.IsZero() || command.StaleBefore.After(command.ObservedAt) ||
		strings.TrimSpace(command.ErrorClass) == "" || strings.TrimSpace(command.ErrorMessage) == "" {
		return RecoverStaleTaskRunsResult{}, ErrInvalid
	}
	port := service.dependencies.TaskRunRecovery.ChildRecoveries
	if port == nil {
		return RecoverStaleTaskRunsResult{}, ErrUnavailable
	}
	result, err := port.RecoverStaleChildTaskRuns(ctx, command)
	if err != nil {
		return RecoverStaleTaskRunsResult{}, err
	}
	if result.WorkspaceKey != command.WorkspaceKey || !result.StaleBefore.Equal(command.StaleBefore) ||
		result.Recovered < 0 || result.Released < 0 || result.SkippedFresh < 0 ||
		result.Recovered != len(result.RecoveredTaskRunIDs) {
		return RecoverStaleTaskRunsResult{}, fmt.Errorf("%w: stale child TaskRun recovery result escaped requested envelope", ErrConflict)
	}
	result.RecoveredTaskRunIDs = append([]string(nil), result.RecoveredTaskRunIDs...)
	sort.Strings(result.RecoveredTaskRunIDs)
	return result, nil
}

func RecoverStaleChildTaskRunsRequestID(driverRunID string, staleBefore time.Time) string {
	return "recover-stale-child-task-runs:" + strings.TrimSpace(driverRunID) + ":" + staleBefore.UTC().Format(time.RFC3339Nano)
}
