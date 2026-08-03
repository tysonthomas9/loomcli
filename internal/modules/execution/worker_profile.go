package execution

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	ActionCreateWorkerProfile authority.Action = "execution.create-worker-profile"
	ActionUpdateWorkerProfile authority.Action = "execution.update-worker-profile"
	ActionDeleteWorkerProfile authority.Action = "execution.delete-worker-profile"
)

// WorkerProfile is Execution's public scheduling-profile snapshot. It is
// deliberately independent of store DTOs so inbound adapters cannot obtain a
// persistence mutation surface from this capability.
type WorkerProfile struct {
	WorkspaceKey  string            `json:"workspace_key"`
	ProfileID     string            `json:"profile_id"`
	Name          string            `json:"name"`
	Role          string            `json:"role"`
	Backend       string            `json:"backend,omitempty"`
	RuntimePolicy map[string]string `json:"runtime_policy,omitempty"`
	Repos         []string          `json:"repos,omitempty"`
	MaxPriority   *int              `json:"max_priority,omitempty"`
	MaxParallel   int               `json:"max_parallel,omitempty"`
	ParentEpic    string            `json:"parent_epic,omitempty"`
	Labels        []string          `json:"labels,omitempty"`
	Capabilities  []string          `json:"capabilities,omitempty"`
	Enabled       bool              `json:"enabled"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type CreateWorkerProfileCommand struct {
	WorkspaceKey  string            `json:"-"`
	RequestID     string            `json:"-"`
	ProfileID     string            `json:"profile_id"`
	Name          string            `json:"name,omitempty"`
	Role          string            `json:"role"`
	Backend       string            `json:"backend,omitempty"`
	RuntimePolicy map[string]string `json:"runtime_policy,omitempty"`
	Repos         []string          `json:"repos,omitempty"`
	MaxPriority   *int              `json:"max_priority,omitempty"`
	MaxParallel   int               `json:"max_parallel,omitempty"`
	ParentEpic    string            `json:"parent_epic,omitempty"`
	Labels        []string          `json:"labels,omitempty"`
	Capabilities  []string          `json:"capabilities,omitempty"`
	Enabled       *bool             `json:"enabled,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type WorkerProfilePatch struct {
	Name             *string            `json:"name,omitempty"`
	Role             *string            `json:"role,omitempty"`
	Backend          *string            `json:"backend,omitempty"`
	RuntimePolicy    *map[string]string `json:"runtime_policy,omitempty"`
	Repos            *[]string          `json:"repos,omitempty"`
	MaxPriority      *int               `json:"max_priority,omitempty"`
	MaxParallel      *int               `json:"max_parallel,omitempty"`
	ClearMaxPriority bool               `json:"clear_max_priority,omitempty"`
	ParentEpic       *string            `json:"parent_epic,omitempty"`
	Labels           *[]string          `json:"labels,omitempty"`
	Capabilities     *[]string          `json:"capabilities,omitempty"`
	Enabled          *bool              `json:"enabled,omitempty"`
	Metadata         *map[string]string `json:"metadata,omitempty"`
}

type UpdateWorkerProfileCommand struct {
	WorkspaceKey       string
	RequestID          string
	ProfileID          string
	ExpectedParentEpic *string
	Patch              WorkerProfilePatch
}

type BindWorkerProfileParentCommand struct {
	WorkspaceKey   string
	RequestID      string
	ProfileID      string
	ExpectedParent string
	Parent         string
	Owner          Owner
}

type DeleteWorkerProfileCommand struct {
	WorkspaceKey string
	RequestID    string
	ProfileID    string
}

type DeleteWorkerProfileResult struct {
	WorkspaceKey string `json:"workspace_key"`
	ProfileID    string `json:"profile_id"`
}

type WorkerProfileFilter struct {
	Role    string `json:"role,omitempty"`
	Backend string `json:"backend,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type WorkerProfileAPI interface {
	GetWorkerProfile(context.Context, string, string) (*WorkerProfile, error)
	ListWorkerProfiles(context.Context, string, WorkerProfileFilter) ([]*WorkerProfile, error)
	CreateWorkerProfile(context.Context, authority.OperatorAuthority, CreateWorkerProfileCommand) (*WorkerProfile, error)
	UpdateWorkerProfile(context.Context, authority.OperatorAuthority, UpdateWorkerProfileCommand) (*WorkerProfile, error)
	DeleteWorkerProfile(context.Context, authority.OperatorAuthority, DeleteWorkerProfileCommand) (DeleteWorkerProfileResult, error)
}

func (service *Service) GetWorkerProfile(ctx context.Context, workspace, profileID string) (*WorkerProfile, error) {
	workspace = strings.TrimSpace(workspace)
	profileID = strings.TrimSpace(profileID)
	if workspace == "" || profileID == "" {
		return nil, ErrInvalid
	}
	port := service.dependencies.Workers.Profiles
	if port == nil {
		return nil, ErrUnavailable
	}
	profile, err := port.GetWorkerProfile(ctx, workspace, profileID)
	if err != nil {
		return nil, err
	}
	if err := validateWorkerProfile(workspace, profileID, profile); err != nil {
		return nil, err
	}
	return cloneWorkerProfile(profile), nil
}

func (service *Service) ListWorkerProfiles(ctx context.Context, workspace string, filter WorkerProfileFilter) ([]*WorkerProfile, error) {
	workspace = strings.TrimSpace(workspace)
	filter.Role = strings.TrimSpace(filter.Role)
	filter.Backend = strings.TrimSpace(filter.Backend)
	filter.Enabled = cloneWorkerProfileBool(filter.Enabled)
	if workspace == "" || filter.Limit < 0 {
		return nil, ErrInvalid
	}
	port := service.dependencies.Workers.Profiles
	if port == nil {
		return nil, ErrUnavailable
	}
	profiles, err := port.ListWorkerProfiles(ctx, workspace, filter)
	if err != nil {
		return nil, err
	}
	out := make([]*WorkerProfile, 0, len(profiles))
	for _, profile := range profiles {
		if profile == nil || profile.WorkspaceKey != workspace {
			return nil, fmt.Errorf("%w: worker profile query escaped requested workspace", ErrConflict)
		}
		if err := validateWorkerProfile(workspace, profile.ProfileID, profile); err != nil {
			return nil, err
		}
		out = append(out, cloneWorkerProfile(profile))
	}
	return out, nil
}

type WorkerProfileMutationPort interface {
	CreateWorkerProfile(context.Context, CreateWorkerProfileCommand) (*WorkerProfile, error)
	UpdateWorkerProfile(context.Context, UpdateWorkerProfileCommand) (*WorkerProfile, error)
	DeleteWorkerProfile(context.Context, DeleteWorkerProfileCommand) error
}

func (service *Service) BindWorkerProfileParent(
	ctx context.Context,
	auth authority.ExecutionAuthority,
	command BindWorkerProfileParentCommand,
) (*WorkerProfile, error) {
	if err := service.requireOwner(ActionBindWorkerProfileParent, command.WorkspaceKey, command.Owner, auth); err != nil {
		return nil, err
	}
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.RequestID = strings.TrimSpace(command.RequestID)
	command.ProfileID = strings.TrimSpace(command.ProfileID)
	command.ExpectedParent = strings.TrimSpace(command.ExpectedParent)
	command.Parent = strings.TrimSpace(command.Parent)
	if command.RequestID == "" || command.ProfileID == "" || command.Parent == "" {
		return nil, ErrInvalid
	}
	port := service.dependencies.Workers.Profiles
	if port == nil {
		return nil, ErrUnavailable
	}
	profile, err := port.UpdateWorkerProfile(ctx, UpdateWorkerProfileCommand{
		WorkspaceKey:       command.WorkspaceKey,
		RequestID:          command.RequestID,
		ProfileID:          command.ProfileID,
		ExpectedParentEpic: &command.ExpectedParent,
		Patch:              WorkerProfilePatch{ParentEpic: &command.Parent},
	})
	if err != nil {
		return nil, err
	}
	if err := validateWorkerProfile(command.WorkspaceKey, command.ProfileID, profile); err != nil {
		return nil, err
	}
	if profile.ParentEpic != command.Parent {
		return nil, fmt.Errorf("%w: worker profile parent did not converge", ErrConflict)
	}
	return cloneWorkerProfile(profile), nil
}

func (service *Service) CreateWorkerProfile(ctx context.Context, auth authority.OperatorAuthority, command CreateWorkerProfileCommand) (*WorkerProfile, error) {
	if err := service.requireOperator(ActionCreateWorkerProfile, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	command = cloneCreateWorkerProfileCommand(command)
	if strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.ProfileID) == "" || strings.TrimSpace(command.Role) == "" || !validWorkerProfilePriority(command.MaxPriority) || command.MaxParallel < 0 {
		return nil, ErrInvalid
	}
	port := service.dependencies.Workers.Profiles
	if port == nil {
		return nil, ErrUnavailable
	}
	profile, err := port.CreateWorkerProfile(ctx, command)
	if err != nil {
		return nil, err
	}
	if err := validateWorkerProfile(command.WorkspaceKey, command.ProfileID, profile); err != nil {
		return nil, err
	}
	return cloneWorkerProfile(profile), nil
}

func (service *Service) UpdateWorkerProfile(ctx context.Context, auth authority.OperatorAuthority, command UpdateWorkerProfileCommand) (*WorkerProfile, error) {
	if err := service.requireOperator(ActionUpdateWorkerProfile, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	command = cloneUpdateWorkerProfileCommand(command)
	if strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.ProfileID) == "" || !validWorkerProfilePatch(command.Patch) {
		return nil, ErrInvalid
	}
	port := service.dependencies.Workers.Profiles
	if port == nil {
		return nil, ErrUnavailable
	}
	profile, err := port.UpdateWorkerProfile(ctx, command)
	if err != nil {
		return nil, err
	}
	if err := validateWorkerProfile(command.WorkspaceKey, command.ProfileID, profile); err != nil {
		return nil, err
	}
	return cloneWorkerProfile(profile), nil
}

func (service *Service) DeleteWorkerProfile(ctx context.Context, auth authority.OperatorAuthority, command DeleteWorkerProfileCommand) (DeleteWorkerProfileResult, error) {
	if err := service.requireOperator(ActionDeleteWorkerProfile, command.WorkspaceKey, auth); err != nil {
		return DeleteWorkerProfileResult{}, err
	}
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.RequestID = strings.TrimSpace(command.RequestID)
	command.ProfileID = strings.TrimSpace(command.ProfileID)
	if command.RequestID == "" || command.ProfileID == "" {
		return DeleteWorkerProfileResult{}, ErrInvalid
	}
	port := service.dependencies.Workers.Profiles
	if port == nil {
		return DeleteWorkerProfileResult{}, ErrUnavailable
	}
	if err := port.DeleteWorkerProfile(ctx, command); err != nil {
		return DeleteWorkerProfileResult{}, err
	}
	return DeleteWorkerProfileResult{WorkspaceKey: command.WorkspaceKey, ProfileID: command.ProfileID}, nil
}

func validWorkerProfilePatch(patch WorkerProfilePatch) bool {
	return validWorkerProfilePatchValues(patch) && workerProfilePatchHasChange(patch)
}

func validWorkerProfilePatchValues(patch WorkerProfilePatch) bool {
	return (patch.Role == nil || strings.TrimSpace(*patch.Role) != "") &&
		(patch.MaxPriority == nil || validWorkerProfilePriority(patch.MaxPriority)) &&
		(patch.MaxParallel == nil || *patch.MaxParallel >= 0) &&
		(!patch.ClearMaxPriority || patch.MaxPriority == nil)
}

func workerProfilePatchHasChange(patch WorkerProfilePatch) bool {
	return patch.Name != nil || patch.Role != nil || patch.Backend != nil || patch.RuntimePolicy != nil || patch.Repos != nil ||
		patch.MaxPriority != nil || patch.MaxParallel != nil || patch.ClearMaxPriority || patch.ParentEpic != nil || patch.Labels != nil ||
		patch.Capabilities != nil || patch.Enabled != nil || patch.Metadata != nil
}

func validWorkerProfilePriority(priority *int) bool {
	return priority == nil || (*priority >= 0 && *priority <= 4)
}

func validateWorkerProfile(workspace, profileID string, profile *WorkerProfile) error {
	if profile == nil || profile.WorkspaceKey != workspace || profile.ProfileID != profileID || strings.TrimSpace(profile.Role) == "" ||
		!validWorkerProfilePriority(profile.MaxPriority) || profile.MaxParallel < 0 || profile.CreatedAt.IsZero() || profile.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: worker profile mutation escaped requested envelope", ErrConflict)
	}
	return nil
}

func cloneCreateWorkerProfileCommand(command CreateWorkerProfileCommand) CreateWorkerProfileCommand {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.RequestID = strings.TrimSpace(command.RequestID)
	command.ProfileID = strings.TrimSpace(command.ProfileID)
	command.Name = strings.TrimSpace(command.Name)
	command.Role = strings.TrimSpace(command.Role)
	command.Backend = strings.TrimSpace(command.Backend)
	command.RuntimePolicy = cloneWorkerProfileMap(command.RuntimePolicy)
	command.Repos = append([]string(nil), command.Repos...)
	command.MaxPriority = cloneWorkerProfileInt(command.MaxPriority)
	command.Labels = append([]string(nil), command.Labels...)
	command.Capabilities = append([]string(nil), command.Capabilities...)
	command.Enabled = cloneWorkerProfileBool(command.Enabled)
	command.Metadata = cloneWorkerProfileMap(command.Metadata)
	return command
}

func cloneUpdateWorkerProfileCommand(command UpdateWorkerProfileCommand) UpdateWorkerProfileCommand {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.RequestID = strings.TrimSpace(command.RequestID)
	command.ProfileID = strings.TrimSpace(command.ProfileID)
	command.ExpectedParentEpic = cloneWorkerProfileString(command.ExpectedParentEpic)
	command.Patch = cloneWorkerProfilePatch(command.Patch)
	return command
}

func cloneWorkerProfile(profile *WorkerProfile) *WorkerProfile {
	if profile == nil {
		return nil
	}
	out := *profile
	out.RuntimePolicy = cloneWorkerProfileMap(profile.RuntimePolicy)
	out.Repos = append([]string(nil), profile.Repos...)
	out.MaxPriority = cloneWorkerProfileInt(profile.MaxPriority)
	out.Labels = append([]string(nil), profile.Labels...)
	out.Capabilities = append([]string(nil), profile.Capabilities...)
	out.Metadata = cloneWorkerProfileMap(profile.Metadata)
	return &out
}

func cloneWorkerProfilePatch(patch WorkerProfilePatch) WorkerProfilePatch {
	patch.Name = cloneWorkerProfileString(patch.Name)
	patch.Role = cloneWorkerProfileString(patch.Role)
	patch.Backend = cloneWorkerProfileString(patch.Backend)
	patch.RuntimePolicy = cloneWorkerProfileMapPtr(patch.RuntimePolicy)
	patch.Repos = cloneWorkerProfileStrings(patch.Repos)
	patch.MaxPriority = cloneWorkerProfileInt(patch.MaxPriority)
	patch.MaxParallel = cloneWorkerProfileInt(patch.MaxParallel)
	patch.ParentEpic = cloneWorkerProfileString(patch.ParentEpic)
	patch.Labels = cloneWorkerProfileStrings(patch.Labels)
	patch.Capabilities = cloneWorkerProfileStrings(patch.Capabilities)
	patch.Enabled = cloneWorkerProfileBool(patch.Enabled)
	patch.Metadata = cloneWorkerProfileMapPtr(patch.Metadata)
	return patch
}

func cloneWorkerProfileString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneWorkerProfileInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneWorkerProfileBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneWorkerProfileStrings(value *[]string) *[]string {
	if value == nil {
		return nil
	}
	copy := append([]string(nil), (*value)...)
	return &copy
}

func cloneWorkerProfileMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	copy := make(map[string]string, len(value))
	for key, item := range value {
		copy[key] = item
	}
	return copy
}

func cloneWorkerProfileMapPtr(value *map[string]string) *map[string]string {
	if value == nil {
		return nil
	}
	copy := cloneWorkerProfileMap(*value)
	return &copy
}
