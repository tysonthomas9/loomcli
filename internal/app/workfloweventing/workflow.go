package workfloweventing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
)

var (
	ErrInvalidRequest = errors.New("workflow eventing: invalid request")
	ErrUnavailable    = errors.New("workflow eventing: unavailable")
)

// EmitRequest contains only caller-controlled event content. Origin, source,
// route, signature status, emitting run, parent event, epic, hop depth, actor,
// and idempotency are deliberately absent; Automation derives them through the
// ExecutionAuthority and its ExecutionPort emission context.
type EmitRequest struct {
	WorkspaceKey string
	EventID      string
	EventType    string
	SubjectRef   string
	Payload      json.RawMessage
	SubjectAttrs map[string]string
}

// Workflow is the named application workflow for execution-originated event
// admission.
type Workflow struct {
	authority ExecutionAuthorityProvider
	admission automation.WorkflowEventAdmission
}

func New(authorityProvider ExecutionAuthorityProvider, admission automation.WorkflowEventAdmission) (*Workflow, error) {
	switch {
	case authorityProvider == nil:
		return nil, fmt.Errorf("%w: execution authority provider is required", ErrUnavailable)
	case admission == nil:
		return nil, fmt.Errorf("%w: automation admission is required", ErrUnavailable)
	default:
		return &Workflow{authority: authorityProvider, admission: admission}, nil
	}
}

// Emit derives authority from the verified parent and enters Automation's one
// admission use case. The verified parent is a separate server-side argument,
// never part of the caller-controlled request DTO.
func (workflow *Workflow) Emit(ctx context.Context, parent VerifiedRun, request EmitRequest) (*automation.AdmissionResult, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if workflow == nil || workflow.authority == nil || workflow.admission == nil {
		return nil, ErrUnavailable
	}
	request, err := validateProvenance(parent, request)
	if err != nil {
		return nil, err
	}

	executionAuthority, err := workflow.authority.AuthorityForVerifiedRun(ctx, parent)
	if err != nil {
		return nil, fmt.Errorf("derive execution authority: %w", err)
	}
	result, err := workflow.admission.AdmitWorkflowEvent(ctx, executionAuthority, automation.WorkflowEvent{
		WorkspaceKey:          request.WorkspaceKey,
		SourceEventID:         request.EventID,
		EventType:             request.EventType,
		SubjectRef:            request.SubjectRef,
		ExecutionNodeID:       parent.NodeID,
		ExecutionLeaseID:      parent.LeaseID,
		ExecutionFencingToken: parent.FencingToken,
		Payload:               request.Payload,
		SubjectAttrs:          request.SubjectAttrs,
	})
	if err != nil {
		return result, fmt.Errorf("admit workflow event: %w", err)
	}
	return result, nil
}

func validateProvenance(parent VerifiedRun, request EmitRequest) (EmitRequest, error) {
	request.WorkspaceKey = strings.TrimSpace(request.WorkspaceKey)
	if request.WorkspaceKey == "" {
		return EmitRequest{}, fmt.Errorf("%w: workspace is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(parent.WorkspaceKey) == "" || strings.TrimSpace(parent.RunID) == "" || parent.Status != "running" ||
		strings.TrimSpace(parent.NodeID) == "" || strings.TrimSpace(parent.LeaseID) == "" || parent.FencingToken <= 0 {
		return EmitRequest{}, fmt.Errorf("%w: verified running parent is required", ErrInvalidRequest)
	}
	if parent.WorkspaceKey != request.WorkspaceKey {
		return EmitRequest{}, fmt.Errorf("%w: parent workspace does not match request workspace", ErrInvalidRequest)
	}
	return request, nil
}
