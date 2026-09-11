// Package taskdelivery resolves shared delivery policy into an immutable plan
// that task hosts can enforce without consulting mutable settings again.
package taskdelivery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

const PlanSchemaVersion = 1

var (
	ErrInvalidRequirement  = errors.New("invalid task delivery requirement")
	ErrRunOverrideWeakens  = errors.New("run override cannot weaken shared task delivery requirement")
	ErrMissingRunIdentity  = errors.New("task delivery plan requires workspace and run identity")
	ErrMissingDeliveryRepo = errors.New("pull_request delivery requires one resolved repository")
)

type PolicySource string

const (
	SourceMigrationDefault PolicySource = "migration_default"
	SourceWorkspace        PolicySource = "workspace"
	SourceRepository       PolicySource = "repository"
	SourceRunOverride      PolicySource = "run_override"
)

type ResolveInput struct {
	WorkspaceRequirement  domain.TaskDeliveryRequirement
	RepositoryRequirement domain.TaskDeliveryRequirement
	RunOverride           domain.TaskDeliveryRequirement
}

type Resolution struct {
	Requirement domain.TaskDeliveryRequirement `json:"requirement"`
	Source      PolicySource                   `json:"source"`
}

// Resolve returns the strongest shared requirement. Repository requirements
// may strengthen a workspace. A run override is rejected when it attempts to
// weaken the shared result.
func Resolve(in ResolveInput) (Resolution, error) {
	for _, requirement := range []domain.TaskDeliveryRequirement{
		in.WorkspaceRequirement,
		in.RepositoryRequirement,
		in.RunOverride,
	} {
		if !requirement.Valid() {
			return Resolution{}, fmt.Errorf("%w: %q", ErrInvalidRequirement, requirement)
		}
	}

	resolved := Resolution{
		Requirement: domain.TaskDeliveryWorkingCopy,
		Source:      SourceMigrationDefault,
	}
	if in.WorkspaceRequirement != "" {
		resolved.Requirement = in.WorkspaceRequirement
		resolved.Source = SourceWorkspace
	}
	if strength(in.RepositoryRequirement) > strength(resolved.Requirement) {
		resolved.Requirement = in.RepositoryRequirement
		resolved.Source = SourceRepository
	}
	if in.RunOverride != "" {
		if strength(in.RunOverride) < strength(resolved.Requirement) {
			return Resolution{}, ErrRunOverrideWeakens
		}
		resolved.Requirement = in.RunOverride
		resolved.Source = SourceRunOverride
	}
	return resolved, nil
}

type FreezeInput struct {
	RunID        string
	WorkspaceKey string
	Repository   string
	Resolution   Resolution
}

type Plan struct {
	SchemaVersion int                            `json:"schema_version"`
	PlanID        string                         `json:"plan_id"`
	RunID         string                         `json:"run_id"`
	WorkspaceKey  string                         `json:"workspace_key"`
	Repository    string                         `json:"repository,omitempty"`
	Requirement   domain.TaskDeliveryRequirement `json:"requirement"`
	PolicySource  PolicySource                   `json:"policy_source"`
}

// Freeze creates a stable plan identity for one admitted run. Callers persist
// this plan with the run and use it for all later delivery decisions.
func Freeze(in FreezeInput) (Plan, error) {
	if in.RunID == "" || in.WorkspaceKey == "" {
		return Plan{}, ErrMissingRunIdentity
	}
	if !in.Resolution.Requirement.Valid() || in.Resolution.Requirement == "" {
		return Plan{}, ErrInvalidRequirement
	}
	if in.Resolution.Requirement == domain.TaskDeliveryPullRequest && in.Repository == "" {
		return Plan{}, ErrMissingDeliveryRepo
	}

	identity := fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s\x00%s",
		PlanSchemaVersion,
		in.RunID,
		in.WorkspaceKey,
		in.Repository,
		in.Resolution.Requirement,
		in.Resolution.Source,
	)
	sum := sha256.Sum256([]byte(identity))
	return Plan{
		SchemaVersion: PlanSchemaVersion,
		PlanID:        hex.EncodeToString(sum[:]),
		RunID:         in.RunID,
		WorkspaceKey:  in.WorkspaceKey,
		Repository:    in.Repository,
		Requirement:   in.Resolution.Requirement,
		PolicySource:  in.Resolution.Source,
	}, nil
}

func strength(requirement domain.TaskDeliveryRequirement) int {
	if requirement == domain.TaskDeliveryPullRequest {
		return 1
	}
	return 0
}
