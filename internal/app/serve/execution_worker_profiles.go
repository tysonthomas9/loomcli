package serve

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Worker-profile persistence is an outbound Execution port. Inbound HTTP and
// CLI adapters see only execution.WorkerProfileAPI and cannot reach Store.
func (adapter *executionTaskRunPortsAdapter) CreateWorkerProfile(ctx context.Context, command execution.CreateWorkerProfileCommand) (*execution.WorkerProfile, error) {
	profile, err := adapter.dependencies.WorkerProfiles.Create(ctx, store.WorkerProfileCreate{
		WorkspaceKey: command.WorkspaceKey, ProfileID: command.ProfileID, Name: command.Name, Role: command.Role,
		Backend: command.Backend, RuntimePolicy: cloneExecutionStringMap(command.RuntimePolicy),
		Repos: append([]string(nil), command.Repos...), MaxPriority: cloneExecutionInt(command.MaxPriority),
		MaxParallel: command.MaxParallel, ParentEpic: command.ParentEpic, Labels: append([]string(nil), command.Labels...),
		Capabilities: append([]string(nil), command.Capabilities...), Enabled: cloneExecutionBool(command.Enabled),
		Metadata: cloneExecutionStringMap(command.Metadata),
	})
	if err != nil {
		if !errors.Is(err, domain.ErrAlreadyExists) && !errors.Is(err, domain.ErrConflict) {
			return nil, err
		}
		// WorkerProfile creation is a desired-state command keyed by the stable
		// profile ID. If the response was lost after commit, an exact retry must
		// return the committed profile; a different payload must fail closed.
		profile, err = adapter.dependencies.WorkerProfiles.Get(ctx, command.WorkspaceKey, command.ProfileID)
		if err != nil {
			return nil, err
		}
		if !workerProfileMatchesCreateCommand(profile, command) {
			return nil, fmt.Errorf("%w: worker profile %s already exists with different desired state", execution.ErrConflict, command.ProfileID)
		}
	}
	return executionWorkerProfileSnapshot(profile), nil
}

func (adapter *executionTaskRunPortsAdapter) UpdateWorkerProfile(ctx context.Context, command execution.UpdateWorkerProfileCommand) (*execution.WorkerProfile, error) {
	patch := command.Patch
	profile, err := adapter.dependencies.WorkerProfiles.Update(ctx, command.WorkspaceKey, command.ProfileID, store.WorkerProfileUpdate{
		Name: cloneExecutionString(patch.Name), Role: cloneExecutionString(patch.Role), Backend: cloneExecutionString(patch.Backend),
		RuntimePolicy: cloneExecutionStringMapPtr(patch.RuntimePolicy), Repos: cloneExecutionStrings(patch.Repos),
		MaxPriority: cloneExecutionInt(patch.MaxPriority), MaxParallel: cloneExecutionInt(patch.MaxParallel),
		ClearMaxPriority: patch.ClearMaxPriority, ParentEpic: cloneExecutionString(patch.ParentEpic),
		Labels: cloneExecutionStrings(patch.Labels), Capabilities: cloneExecutionStrings(patch.Capabilities),
		Enabled: cloneExecutionBool(patch.Enabled), Metadata: cloneExecutionStringMapPtr(patch.Metadata),
	})
	if err != nil {
		return nil, err
	}
	return executionWorkerProfileSnapshot(profile), nil
}

func (adapter *executionTaskRunPortsAdapter) DeleteWorkerProfile(ctx context.Context, command execution.DeleteWorkerProfileCommand) error {
	err := adapter.dependencies.WorkerProfiles.Delete(ctx, command.WorkspaceKey, command.ProfileID)
	if errors.Is(err, domain.ErrNotFound) {
		// Delete is an absent-state command, so a retry after a lost successful
		// response converges without requiring an in-memory receipt.
		return nil
	}
	return err
}

func workerProfileMatchesCreateCommand(profile *domain.WorkerProfile, command execution.CreateWorkerProfileCommand) bool {
	if profile == nil {
		return false
	}
	name := command.Name
	if strings.TrimSpace(name) == "" {
		name = command.ProfileID
	}
	enabled := true
	if command.Enabled != nil {
		enabled = *command.Enabled
	}
	return profile.WorkspaceKey == command.WorkspaceKey && profile.ProfileID == command.ProfileID &&
		profile.Name == name && profile.Role == command.Role && profile.Backend == command.Backend &&
		maps.Equal(profile.RuntimePolicy, command.RuntimePolicy) && slices.Equal(profile.Repos, command.Repos) &&
		executionIntEqual(profile.MaxPriority, command.MaxPriority) && profile.MaxParallel == command.MaxParallel &&
		profile.ParentEpic == command.ParentEpic && slices.Equal(profile.Labels, command.Labels) &&
		slices.Equal(profile.Capabilities, command.Capabilities) && profile.Enabled == enabled &&
		maps.Equal(profile.Metadata, command.Metadata)
}

func executionIntEqual(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func executionWorkerProfileSnapshot(profile *domain.WorkerProfile) *execution.WorkerProfile {
	if profile == nil {
		return nil
	}
	return &execution.WorkerProfile{
		WorkspaceKey: profile.WorkspaceKey, ProfileID: profile.ProfileID, Name: profile.Name, Role: profile.Role,
		Backend: profile.Backend, RuntimePolicy: cloneExecutionStringMap(profile.RuntimePolicy),
		Repos: append([]string(nil), profile.Repos...), MaxPriority: cloneExecutionInt(profile.MaxPriority),
		MaxParallel: profile.MaxParallel, ParentEpic: profile.ParentEpic, Labels: append([]string(nil), profile.Labels...),
		Capabilities: append([]string(nil), profile.Capabilities...), Enabled: profile.Enabled,
		Metadata: cloneExecutionStringMap(profile.Metadata), CreatedAt: profile.CreatedAt, UpdatedAt: profile.UpdatedAt,
	}
}

func cloneExecutionString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneExecutionInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneExecutionBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneExecutionStrings(value *[]string) *[]string {
	if value == nil {
		return nil
	}
	copy := append([]string(nil), (*value)...)
	return &copy
}

func cloneExecutionStringMapPtr(value *map[string]string) *map[string]string {
	if value == nil {
		return nil
	}
	copy := cloneExecutionStringMap(*value)
	return &copy
}
