package workflowcatalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type AvailabilityOutcome string

const (
	AvailabilityOutcomeAvailable        AvailabilityOutcome = "available"
	AvailabilityOutcomeRetryableFailure AvailabilityOutcome = "retryable_failure"
	AvailabilityOutcomePermanentFailure AvailabilityOutcome = "permanent_failure"
)

type AvailabilityCommand struct {
	WorkspaceKey     string              `json:"workspace_key"`
	RequestID        string              `json:"request_id"`
	ExpectedRevision uint64              `json:"expected_revision"`
	DriverID         string              `json:"driver_id"`
	VersionID        string              `json:"version_id"`
	SourceDigest     string              `json:"source_digest"`
	BundleDigest     string              `json:"bundle_digest"`
	Outcome          AvailabilityOutcome `json:"outcome"`
	Failure          string              `json:"failure,omitempty"`
}

type AvailabilityResult struct {
	Driver            *Driver        `json:"driver"`
	Version           *DriverVersion `json:"version"`
	Replayed          bool           `json:"replayed,omitempty"`
	CommittedRevision uint64         `json:"committed_revision"`
	SemanticImpact    string         `json:"semantic_impact"`
}

var _ VersionAvailabilityAPI = (*Service)(nil)

func (s *Service) RecordVersionAvailability(
	ctx context.Context,
	auth authority.SystemAuthority,
	command AvailabilityCommand,
) (*AvailabilityResult, error) {
	normalized, err := normalizeAvailabilityCommand(command)
	if err != nil {
		return nil, err
	}
	if s == nil || s.admission == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.admission.RequireSystem(ActionRecordVersionAvailability, normalized.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if s.availability == nil {
		return nil, ErrUnavailable
	}
	result, err := s.availability.RecordVersionAvailability(ctx, AvailabilityMutation{
		AvailabilityCommand: normalized,
		AuditActor:          auth.Subject(),
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ActionRecordVersionAvailability, err)
	}
	return validateAvailabilityResult(normalized, result)
}

func normalizeAvailabilityCommand(command AvailabilityCommand) (AvailabilityCommand, error) {
	var err error
	command.WorkspaceKey, err = normalizeRequired("workspace", command.WorkspaceKey)
	if err != nil {
		return AvailabilityCommand{}, err
	}
	command.RequestID, err = requireCanonical("request id", command.RequestID)
	if err != nil {
		return AvailabilityCommand{}, err
	}
	command.DriverID, err = requireCanonicalDriverID(command.DriverID)
	if err != nil {
		return AvailabilityCommand{}, err
	}
	command.VersionID, err = requireCanonical("version id", command.VersionID)
	if err != nil {
		return AvailabilityCommand{}, err
	}
	command.SourceDigest, err = requireSHA256Digest("source digest", command.SourceDigest)
	if err != nil {
		return AvailabilityCommand{}, err
	}
	command.BundleDigest, err = requireSHA256Digest("bundle digest", command.BundleDigest)
	if err != nil {
		return AvailabilityCommand{}, err
	}
	if command.ExpectedRevision == 0 || command.ExpectedRevision > MaxExpectedRevision {
		return AvailabilityCommand{}, fmt.Errorf("expected revision must be advanceable: %w", ErrInvalid)
	}
	switch command.Outcome {
	case AvailabilityOutcomeAvailable:
		if command.Failure != "" {
			return AvailabilityCommand{}, fmt.Errorf("available outcome cannot include failure: %w", ErrInvalid)
		}
	case AvailabilityOutcomeRetryableFailure, AvailabilityOutcomePermanentFailure:
		command.Failure = strings.TrimSpace(command.Failure)
		if command.Failure == "" || len(command.Failure) > 128 {
			return AvailabilityCommand{}, fmt.Errorf("failure is required and bounded: %w", ErrInvalid)
		}
	default:
		return AvailabilityCommand{}, fmt.Errorf("availability outcome is invalid: %w", ErrInvalid)
	}
	return command, nil
}

func validateAvailabilityResult(command AvailabilityCommand, result *AvailabilityResult) (*AvailabilityResult, error) {
	if result == nil || result.CommittedRevision != command.ExpectedRevision+1 ||
		result.SemanticImpact != SemanticImpactVersionAvailabilityChanged {
		return nil, ErrInvalidPersistedState
	}
	if err := validateDriver(result.Driver, command.WorkspaceKey, command.DriverID, false); err != nil {
		return nil, err
	}
	if err := validateVersion(result.Version, command.WorkspaceKey, command.DriverID, command.VersionID); err != nil {
		return nil, err
	}
	if result.Driver.Revision < result.CommittedRevision ||
		result.Version.SourceDigest != command.SourceDigest ||
		result.Version.BundleDigest != command.BundleDigest ||
		result.Version.ValidationStatus != DriverVersionValidationPassed {
		return nil, ErrInvalidPersistedState
	}
	switch command.Outcome {
	case AvailabilityOutcomeAvailable:
		if !VersionAvailable(result.Version) {
			return nil, ErrInvalidPersistedState
		}
	case AvailabilityOutcomePermanentFailure:
		if result.Version.AvailabilityStatus != DriverVersionAvailabilityFailed {
			return nil, ErrInvalidPersistedState
		}
	case AvailabilityOutcomeRetryableFailure:
		if result.Version.AvailabilityStatus != DriverVersionAvailabilityPending &&
			result.Version.AvailabilityStatus != DriverVersionAvailabilityFailed {
			return nil, ErrInvalidPersistedState
		}
	}
	out := *result
	out.Driver = cloneDriver(result.Driver)
	out.Version = cloneVersion(result.Version)
	return &out, nil
}
