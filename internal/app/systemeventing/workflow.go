package systemeventing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
)

var (
	ErrInvalidRequest = errors.New("system eventing: invalid request")
	ErrUnavailable    = errors.New("system eventing: unavailable")
)

// EmitRequest contains event content but no actor, origin, hop depth,
// signature status, authority, or idempotency key. Automation derives those
// fields from the separately verified SystemAuthority.
type EmitRequest struct {
	WorkspaceKey  string
	SourceEventID string
	EventType     string
	SourceRef     string
	SubjectRef    string
	ParentEventID string
	EpicID        string
	OccurredAt    time.Time
	Payload       json.RawMessage
	SubjectAttrs  map[string]string
}

type Workflow struct {
	authority AuthorityProvider
	admission automation.EventAdmission
}

type issueJournalEmitter struct{ workflow *Workflow }
type runOutcomeEmitter struct{ workflow *Workflow }

var _ IssueJournalEmitter = (*issueJournalEmitter)(nil)
var _ RunOutcomeEmitter = (*runOutcomeEmitter)(nil)

func New(provider AuthorityProvider, admission automation.EventAdmission) (*Workflow, error) {
	switch {
	case provider == nil:
		return nil, fmt.Errorf("%w: authority provider is required", ErrUnavailable)
	case admission == nil:
		return nil, fmt.Errorf("%w: automation admission is required", ErrUnavailable)
	default:
		return &Workflow{authority: provider, admission: admission}, nil
	}
}

// BindIssueJournalEmitter captures the registered issue-journal component at
// composition. The returned port has no API through which a consumer can
// impersonate another system producer.
func BindIssueJournalEmitter(workflow *Workflow) (IssueJournalEmitter, error) {
	if workflow == nil || workflow.authority == nil || workflow.admission == nil {
		return nil, ErrUnavailable
	}
	return &issueJournalEmitter{workflow: workflow}, nil
}

// BindRunOutcomeEmitter captures the registered Execution outcome component
// at composition. No-listener is a successful no-op: terminal DriverRun state
// and composition awaits do not depend on a secondary Automation binding.
func BindRunOutcomeEmitter(workflow *Workflow) (RunOutcomeEmitter, error) {
	if workflow == nil || workflow.authority == nil || workflow.admission == nil {
		return nil, ErrUnavailable
	}
	return &runOutcomeEmitter{workflow: workflow}, nil
}

func (emitter *issueJournalEmitter) EmitIssueJournal(
	ctx context.Context,
	workspace, actor string,
	request EmitRequest,
) (*automation.AdmissionResult, error) {
	if emitter == nil || emitter.workflow == nil {
		return nil, ErrUnavailable
	}
	return emitter.workflow.emit(ctx, VerifiedSource{
		ComponentID: IssueJournalBridgeComponentID, WorkspaceKey: workspace, ActorRef: actor,
	}, request)
}

func (emitter *runOutcomeEmitter) EmitRunOutcome(
	ctx context.Context,
	workspace, actor string,
	request EmitRequest,
) (*automation.AdmissionResult, error) {
	if emitter == nil || emitter.workflow == nil {
		return nil, ErrUnavailable
	}
	result, err := emitter.workflow.emit(ctx, VerifiedSource{
		ComponentID: DriverRunOutcomeComponentID, WorkspaceKey: workspace, ActorRef: actor,
	}, request)
	if errors.Is(err, automation.ErrNoMatchingBinding) {
		return nil, nil
	}
	return result, err
}

func (workflow *Workflow) emit(ctx context.Context, source VerifiedSource, request EmitRequest) (*automation.AdmissionResult, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if workflow == nil || workflow.authority == nil || workflow.admission == nil {
		return nil, ErrUnavailable
	}
	request, err := validateRequest(source, request)
	if err != nil {
		return nil, err
	}
	systemAuthority, err := workflow.authority.AuthorityForVerifiedSource(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("derive system authority: %w", err)
	}
	result, err := workflow.admission.AdmitEvent(ctx, automation.NewSystemEventAuthority(systemAuthority), automation.AdmitEventCommand{
		WorkspaceKey:  request.WorkspaceKey,
		SourceKind:    automation.SourceKindInternal,
		SourceRef:     request.SourceRef,
		SourceEventID: request.SourceEventID,
		EventType:     request.EventType,
		SubjectRef:    request.SubjectRef,
		ParentEventID: request.ParentEventID,
		EpicID:        request.EpicID,
		OccurredAt:    request.OccurredAt,
		Payload:       cloneRawMessage(request.Payload),
		SubjectAttrs:  cloneStringMap(request.SubjectAttrs),
	})
	if err != nil {
		return result, fmt.Errorf("admit system event: %w", err)
	}
	return result, nil
}

func validateRequest(source VerifiedSource, request EmitRequest) (EmitRequest, error) {
	if strings.TrimSpace(source.ComponentID) == "" || source.ComponentID != strings.TrimSpace(source.ComponentID) ||
		strings.TrimSpace(source.WorkspaceKey) == "" || source.WorkspaceKey != strings.TrimSpace(source.WorkspaceKey) {
		return EmitRequest{}, fmt.Errorf("%w: verified component and workspace are required", ErrInvalidRequest)
	}
	request.WorkspaceKey = strings.TrimSpace(request.WorkspaceKey)
	request.SourceEventID = strings.TrimSpace(request.SourceEventID)
	request.EventType = strings.TrimSpace(request.EventType)
	if source.ComponentID != DriverRunOutcomeComponentID {
		request.SourceRef = strings.TrimSpace(request.SourceRef)
		request.SubjectRef = strings.TrimSpace(request.SubjectRef)
	}
	request.ParentEventID = strings.TrimSpace(request.ParentEventID)
	request.EpicID = strings.TrimSpace(request.EpicID)
	if request.WorkspaceKey == "" || request.SourceEventID == "" || request.EventType == "" {
		return EmitRequest{}, fmt.Errorf("%w: workspace, event id, and event type are required", ErrInvalidRequest)
	}
	if source.WorkspaceKey != request.WorkspaceKey {
		return EmitRequest{}, fmt.Errorf("%w: verified source workspace does not match request", ErrInvalidRequest)
	}
	request.Payload = cloneRawMessage(request.Payload)
	request.SubjectAttrs = cloneStringMap(request.SubjectAttrs)
	return request, nil
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	clone := make(map[string]string, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}
