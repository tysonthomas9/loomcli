package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// RecoverTerminalDriverRunWork invokes the atomic terminal-parent convergence
// command. Authorization is intentionally system-only: a terminal DriverRun's
// former execution lease cannot authorize descendant cleanup.
func (service *Service) RecoverTerminalDriverRunWork(
	ctx context.Context,
	auth authority.SystemAuthority,
	command RecoverTerminalDriverRunWorkCommand,
) (RecoverTerminalDriverRunWorkResult, error) {
	if err := service.requireSystem(ActionRecoverTerminalDriverRunWork, command.WorkspaceKey, auth); err != nil {
		return RecoverTerminalDriverRunWorkResult{}, err
	}
	command.DriverRunID = strings.TrimSpace(command.DriverRunID)
	if command.DriverRunID == "" || !command.ParentStatus.IsTerminal() ||
		command.RequestID != RecoverTerminalDriverRunWorkRequestID(command.DriverRunID, command.ParentStatus) ||
		strings.TrimSpace(command.Reason) == "" || strings.TrimSpace(command.ErrorClass) == "" ||
		command.RecoveredAt.IsZero() {
		return RecoverTerminalDriverRunWorkResult{}, ErrInvalid
	}
	port := service.dependencies.DriverRuns.TerminalWorkRecovery
	if port == nil {
		return RecoverTerminalDriverRunWorkResult{}, ErrUnavailable
	}
	result, err := port.RecoverTerminalDriverRunWork(ctx, command)
	if err != nil {
		return RecoverTerminalDriverRunWorkResult{}, err
	}
	if !validRecoverTerminalDriverRunWorkResult(result, command) {
		return RecoverTerminalDriverRunWorkResult{}, fmt.Errorf("%w: terminal DriverRun work recovery escaped requested envelope", ErrConflict)
	}
	return cloneRecoverTerminalDriverRunWorkResult(result), nil
}

// RecoverTerminalDriverRunWorkRequestID returns the deterministic identity
// shared by the live outcome delivery and every recovery replay for a terminal
// DriverRun/status pair.
func RecoverTerminalDriverRunWorkRequestID(driverRunID string, status DriverRunStatus) string {
	digest := sha256.Sum256([]byte("driver-run-terminal-work-recovery\x00" + strings.TrimSpace(driverRunID) + "\x00" + string(status)))
	return "driver-run-terminal-work:" + hex.EncodeToString(digest[:16])
}

func validRecoverTerminalDriverRunWorkResult(
	result RecoverTerminalDriverRunWorkResult,
	command RecoverTerminalDriverRunWorkCommand,
) bool {
	commit := result.Committed
	if commit == nil || strings.TrimSpace(result.ActionID) == "" ||
		commit.WorkspaceKey != command.WorkspaceKey || commit.DriverRunID != command.DriverRunID ||
		commit.ParentStatus != command.ParentStatus || commit.Reason != command.Reason ||
		commit.ErrorClass != command.ErrorClass ||
		((!result.Replay && !commit.RecoveredAt.Equal(command.RecoveredAt)) || (result.Replay && commit.RecoveredAt.IsZero())) ||
		!canonicalIdentityList(result.RecoveredTaskRunIDs) ||
		!canonicalIdentityList(result.ReleasedWorkItemIDs) ||
		!canonicalIdentityList(result.PreservedSuccessorWorkItemIDs) ||
		!slices.Equal(result.RecoveredTaskRunIDs, commit.RecoveredTaskRunIDs) ||
		!slices.Equal(result.ReleasedWorkItemIDs, commit.ReleasedWorkItemIDs) ||
		!slices.Equal(result.PreservedSuccessorWorkItemIDs, commit.PreservedSuccessorWorkItemIDs) {
		return false
	}
	return disjointIdentities(result.ReleasedWorkItemIDs, result.PreservedSuccessorWorkItemIDs)
}

func canonicalIdentityList(values []string) bool {
	return slices.Equal(values, canonicalDriverRunIDs(values))
}

func disjointIdentities(left, right []string) bool {
	seen := make(map[string]struct{}, len(left))
	for _, value := range left {
		seen[value] = struct{}{}
	}
	for _, value := range right {
		if _, exists := seen[value]; exists {
			return false
		}
	}
	return true
}

func cloneRecoverTerminalDriverRunWorkResult(result RecoverTerminalDriverRunWorkResult) RecoverTerminalDriverRunWorkResult {
	result.RecoveredTaskRunIDs = append([]string(nil), result.RecoveredTaskRunIDs...)
	result.ReleasedWorkItemIDs = append([]string(nil), result.ReleasedWorkItemIDs...)
	result.PreservedSuccessorWorkItemIDs = append([]string(nil), result.PreservedSuccessorWorkItemIDs...)
	if result.Committed != nil {
		commit := *result.Committed
		commit.RecoveredTaskRunIDs = append([]string(nil), result.Committed.RecoveredTaskRunIDs...)
		commit.ReleasedWorkItemIDs = append([]string(nil), result.Committed.ReleasedWorkItemIDs...)
		commit.PreservedSuccessorWorkItemIDs = append([]string(nil), result.Committed.PreservedSuccessorWorkItemIDs...)
		result.Committed = &commit
	}
	return result
}
