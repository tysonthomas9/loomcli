package agentprovisioning

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

var provisioningStepOrder = []Step{StepRole, StepAgent, StepBinding, StepGrant}

//nolint:cyclop,funlen // The durable progress record validates every state-machine coordinate and step invariant together.
func validateProgress(record *Record) error {
	if record == nil {
		return fmt.Errorf("durable provisioning record is required: %w", ErrConflict)
	}
	if !record.State.valid() {
		return fmt.Errorf("durable provisioning state %q is invalid: %w", record.State, ErrConflict)
	}
	if record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() || record.UpdatedAt.Before(record.CreatedAt) {
		return fmt.Errorf("durable provisioning timestamps are invalid: %w", ErrConflict)
	}
	if !isPrefix(record.CompletedSteps, provisioningStepOrder) {
		return fmt.Errorf("completed provisioning steps are not an ordered prefix: %w", ErrConflict)
	}

	grantOrder := make([]string, len(record.Spec.Grants))
	for index, grant := range record.Spec.Grants {
		grantOrder[index] = grant.GrantID
	}
	if !isPrefix(record.CompletedGrants, grantOrder) {
		return fmt.Errorf("completed provisioning grants are not an ordered intent prefix: %w", ErrConflict)
	}
	if len(record.CompletedGrants) > 0 && len(record.CompletedSteps) < 3 {
		return fmt.Errorf("completed grants require role, agent, and binding completion: %w", ErrConflict)
	}

	switch record.State {
	case StatePending:
		if len(record.CompletedSteps) != 0 || len(record.CompletedGrants) != 0 ||
			record.LastErrorClass != "" || record.CompletedAt != nil {
			return fmt.Errorf("pending provisioning cannot carry progress, failure, or completion: %w", ErrConflict)
		}
	case StateRunning:
		if len(record.CompletedSteps) == len(provisioningStepOrder) ||
			record.LastErrorClass != "" || record.CompletedAt != nil {
			return fmt.Errorf("running provisioning has terminal-only fields: %w", ErrConflict)
		}
	case StateRetryableFailed, StatePermanentFailed:
		if len(record.CompletedSteps) == len(provisioningStepOrder) ||
			strings.TrimSpace(record.LastErrorClass) == "" || record.CompletedAt != nil {
			return fmt.Errorf("failed provisioning needs a nonterminal error state: %w", ErrConflict)
		}
		if !validFailureClass(
			record.State,
			record.LastErrorClass,
			provisioningStepOrder[len(record.CompletedSteps)],
		) {
			return fmt.Errorf("failed provisioning has an inconsistent error class: %w", ErrConflict)
		}
	case StateCompleted:
		if !slices.Equal(record.CompletedSteps, provisioningStepOrder) ||
			!slices.Equal(record.CompletedGrants, grantOrder) ||
			record.LastErrorClass != "" || record.CompletedAt == nil ||
			record.CompletedAt.IsZero() || record.CompletedAt.Before(record.CreatedAt) ||
			record.CompletedAt.After(record.UpdatedAt) {
			return fmt.Errorf("completed provisioning is not fully converged: %w", ErrConflict)
		}
	}
	return nil
}

func validFailureClass(state State, class string, failedStep Step) bool {
	prefix := string(failedStep) + "_"
	if !strings.HasPrefix(class, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(class, prefix)
	switch state {
	case StateRetryableFailed:
		return suffix == "unavailable"
	case StatePermanentFailed:
		return suffix == "invalid" || suffix == "conflict"
	default:
		return false
	}
}

func validateTransition(previous, next *Record) error {
	if previous == nil || next == nil {
		return fmt.Errorf("provisioning transition needs previous and next records: %w", ErrConflict)
	}
	if previous.ProvisioningID != next.ProvisioningID ||
		previous.ProvisioningGenerationID != next.ProvisioningGenerationID ||
		previous.WorkspaceKey != next.WorkspaceKey ||
		previous.RequestedBy != next.RequestedBy ||
		previous.SpecFingerprint != next.SpecFingerprint ||
		previous.UnusedRolePolicy != next.UnusedRolePolicy ||
		!previous.CreatedAt.Equal(next.CreatedAt) ||
		!reflect.DeepEqual(previous.Spec, next.Spec) {
		return fmt.Errorf("provisioning transition changed immutable intent: %w", ErrConflict)
	}
	if next.UpdatedAt.Before(previous.UpdatedAt) {
		return fmt.Errorf("provisioning transition regressed updated time: %w", ErrConflict)
	}
	if !isPrefix(previous.CompletedSteps, next.CompletedSteps) ||
		!isPrefix(previous.CompletedGrants, next.CompletedGrants) {
		return fmt.Errorf("provisioning transition regressed durable progress: %w", ErrConflict)
	}
	if !legalStateTransition(previous.State, next.State) {
		return fmt.Errorf("illegal provisioning state transition %q -> %q: %w", previous.State, next.State, ErrConflict)
	}
	if err := validateProgress(next); err != nil {
		return err
	}
	return nil
}

func legalStateTransition(previous, next State) bool {
	switch previous {
	case StatePending:
		return next == StateRunning
	case StateRunning:
		return next == StateRunning || next == StateRetryableFailed ||
			next == StatePermanentFailed || next == StateCompleted
	case StateRetryableFailed:
		return next == StateRunning
	default:
		return false
	}
}

func isPrefix[T comparable](prefix, values []T) bool {
	return len(prefix) <= len(values) && slices.Equal(prefix, values[:len(prefix)])
}
