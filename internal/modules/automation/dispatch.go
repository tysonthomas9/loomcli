package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func (s *Service) DispatchBinding(ctx context.Context, auth authority.OperatorAuthority, command DispatchBindingCommand) (*DispatchBindingResult, error) {
	normalized, err := normalizeBindingCommand(BindingCommand{WorkspaceKey: command.WorkspaceKey, BindingID: command.BindingID})
	if err != nil {
		return nil, err
	}
	idempotencyKey, err := requireCanonical("idempotency key", command.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if s == nil || s.authority == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.authority.RequireOperator(ActionDispatchBinding, normalized.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if s.execution == nil {
		return nil, ErrUnavailable
	}
	replayed, err := s.probeManualDispatchReplay(ctx, command, normalized, auth.Subject(), idempotencyKey)
	if err != nil {
		return nil, err
	}
	if replayed != nil {
		return replayed, nil
	}
	if s.bindings == nil {
		return nil, ErrUnavailable
	}
	binding, err := s.loadBinding(ctx, normalized.WorkspaceKey, normalized.BindingID)
	if err != nil {
		return nil, err
	}
	effective, err := s.resolveEffectiveVersion(ctx, normalized.WorkspaceKey, binding.DriverID, "automation manual binding dispatch")
	if err != nil {
		return nil, err
	}
	request := manualDispatchRequest(
		command, binding, normalized.WorkspaceKey, effective.Driver.DriverID, effective.Version.VersionID,
		effective.Driver.Revision, effective.Version.SourceDigest, effective.Version.BundleDigest, auth.Subject(), idempotencyKey,
	)
	if err := validateExecutionDispatchRequest(request); err != nil {
		return nil, err
	}
	dispatched, err := s.execution.Dispatch(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("dispatch binding %q: %w", binding.BindingID, err)
	}
	return validateManualDispatchResult(binding.BindingID, dispatched)
}

func (s *Service) probeManualDispatchReplay(
	ctx context.Context,
	command DispatchBindingCommand,
	binding BindingCommand,
	actor, idempotencyKey string,
) (*DispatchBindingResult, error) {
	request := manualReplayRequest(command, binding.WorkspaceKey, binding.BindingID, actor, idempotencyKey)
	if err := validateExecutionDispatchRequest(request); err != nil {
		return nil, err
	}
	replayed, err := s.execution.Dispatch(ctx, request)
	if err == nil {
		return validateManualDispatchResult(binding.BindingID, replayed)
	}
	if errors.Is(err, ErrDispatchReplayNotFound) {
		return nil, nil
	}
	return nil, fmt.Errorf("probe binding %q dispatch replay: %w", binding.BindingID, err)
}

func manualReplayRequest(
	command DispatchBindingCommand,
	workspace, bindingID, actor, idempotencyKey string,
) ExecutionDispatchRequest {
	return ExecutionDispatchRequest{
		WorkspaceKey: workspace, IdempotencyKey: idempotencyKey, ReplayOnly: true,
		TriggerBindingID: bindingID, SubjectRef: strings.TrimSpace(command.SubjectRef),
		EpicID: strings.TrimSpace(command.EpicID), ActorRef: actor,
		RawPayloadRef: strings.TrimSpace(command.RawPayloadRef), Payload: cloneRawMessage(command.Payload),
		SubjectAttrs: cloneStringMap(command.SubjectAttrs),
	}
}

func manualDispatchRequest(
	command DispatchBindingCommand,
	binding *Binding,
	workspace, driverID, versionID string,
	driverRevision uint64,
	sourceDigest, bundleDigest, actor, idempotencyKey string,
) ExecutionDispatchRequest {
	return ExecutionDispatchRequest{
		WorkspaceKey:         workspace,
		IdempotencyKey:       idempotencyKey,
		DriverID:             driverID,
		DriverVersionID:      versionID,
		DriverRevision:       driverRevision,
		SourceDigest:         sourceDigest,
		BundleDigest:         bundleDigest,
		Entrypoint:           binding.TargetEntrypoint,
		TargetAgentServiceID: binding.TargetAgentServiceID,
		SourceKind:           "binding-run",
		SourceRef:            firstNonEmpty(binding.RouteKey, binding.BindingID),
		SubjectRef:           strings.TrimSpace(command.SubjectRef),
		TriggerBindingID:     binding.BindingID,
		SubjectKey:           manualSubjectKey(binding, command),
		ConcurrencyPolicy:    binding.ConcurrencyPolicy,
		EpicID:               strings.TrimSpace(command.EpicID),
		ActorRef:             actor,
		RawPayloadRef:        strings.TrimSpace(command.RawPayloadRef),
		Payload:              cloneRawMessage(command.Payload),
		SubjectAttrs:         cloneStringMap(command.SubjectAttrs),
	}
}

func manualSubjectKey(binding *Binding, command DispatchBindingCommand) string {
	subjectKey, err := renderSubjectKey(binding.SubjectKeyTemplate, subjectInputs{
		bindingID: binding.BindingID, subjectRef: strings.TrimSpace(command.SubjectRef),
		attrs: command.SubjectAttrs,
	})
	if err != nil {
		return defaultSubjectKey(binding.BindingID, command.SubjectRef)
	}
	return subjectKey
}

func validateManualDispatchResult(bindingID string, dispatched *ExecutionDispatchResult) (*DispatchBindingResult, error) {
	if dispatched == nil {
		return nil, ErrInvalidPersistedState
	}
	if dispatched.Busy {
		return nil, ErrExecutionBusy
	}
	runID := strings.TrimSpace(dispatched.RunID)
	var snapshot struct {
		RunID string `json:"run_id"`
	}
	if runID == "" || runID != dispatched.RunID || len(dispatched.RunSnapshot) == 0 || !json.Valid(dispatched.RunSnapshot) ||
		json.Unmarshal(dispatched.RunSnapshot, &snapshot) != nil || snapshot.RunID != runID {
		return nil, ErrInvalidPersistedState
	}
	return &DispatchBindingResult{
		BindingID:   bindingID,
		RunID:       runID,
		Replayed:    dispatched.Replayed,
		RunSnapshot: cloneRawMessage(dispatched.RunSnapshot),
	}, nil
}
